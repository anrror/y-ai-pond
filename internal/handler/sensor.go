package handler

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
)

// influxMeasurement is the InfluxDB measurement holding sensor telemetry.
const influxMeasurement = "sensor_data"

// sensorReading is a single latest sensor reading in the API response.
type sensorReading struct {
	FarmID     string  `json:"farm_id"`
	PondID     string  `json:"pond_id"`
	SensorType string  `json:"sensor_type"`
	Value      float64 `json:"value"`
	Timestamp  string  `json:"timestamp"`
}

// historyPoint is one aggregation bucket of sensor readings.
type historyPoint struct {
	Timestamp string             `json:"timestamp"`
	Values    map[string]float64 `json:"values"`
}

// historyResponse is the /sensors/history response body.
type historyResponse struct {
	PondID string         `json:"pond_id"`
	Window string         `json:"window"`
	Points []historyPoint `json:"points"`
}

var errInvalidWindow = errors.New("handler: invalid aggregation window")

// getLatestSensors returns the most recent reading per sensor type for a pond.
func (h *Handler) getLatestSensors(c *gin.Context) {
	pondID := c.Query("pond_id")
	if pondID == "" {
		badRequest(c, "pond_id is required")
		return
	}
	points, err := h.influx.QueryTimeRange(c.Request.Context(), influxMeasurement, "now - 1h", "now")
	if err != nil {
		h.internalError(c, err)
		return
	}
	latest := map[string]store.Point{}
	for _, p := range points {
		if p.Tags["pond_id"] != pondID {
			continue
		}
		sensorType := p.Tags["sensor_type"]
		if sensorType == "" {
			continue
		}
		if cur, ok := latest[sensorType]; !ok || p.Timestamp.After(cur.Timestamp) {
			latest[sensorType] = p
		}
	}
	resp := make([]sensorReading, 0, len(latest))
	for sensorType, p := range latest {
		resp = append(resp, sensorReading{
			FarmID:     p.Tags["farm_id"],
			PondID:     p.Tags["pond_id"],
			SensorType: sensorType,
			Value:      p.Fields[sensorType],
			Timestamp:  p.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(resp, func(i, j int) bool { return resp[i].SensorType < resp[j].SensorType })
	c.JSON(http.StatusOK, resp)
}

// getSensorHistory returns window-aggregated sensor readings for a pond.
func (h *Handler) getSensorHistory(c *gin.Context) {
	pondID := c.Query("pond_id")
	fromStr := c.Query("from")
	toStr := c.Query("to")
	if pondID == "" || fromStr == "" || toStr == "" {
		badRequest(c, "pond_id, from, and to are required")
		return
	}
	if _, err := time.Parse(time.RFC3339, fromStr); err != nil {
		badRequest(c, "invalid from, expected RFC3339")
		return
	}
	if _, err := time.Parse(time.RFC3339, toStr); err != nil {
		badRequest(c, "invalid to, expected RFC3339")
		return
	}
	window := c.DefaultQuery("window", "5m")
	dur, err := parseWindow(window)
	if err != nil {
		badRequest(c, "invalid window, expected 1m|5m|1h|1d")
		return
	}
	points, err := h.influx.QueryTimeRange(c.Request.Context(), influxMeasurement, fromStr, toStr)
	if err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, historyResponse{
		PondID: pondID,
		Window: window,
		Points: aggregatePoints(points, pondID, dur),
	})
}

// parseWindow maps a window token (1m/5m/1h/1d) to a duration.
func parseWindow(w string) (time.Duration, error) {
	switch w {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	}
	return 0, errInvalidWindow
}

// aggregatePoints buckets points by window, averaging each sensor field.
func aggregatePoints(points []store.Point, pondID string, window time.Duration) []historyPoint {
	type acc struct {
		sum map[string]float64
		n   map[string]int
	}
	buckets := map[int64]*acc{}
	for _, p := range points {
		if p.Tags["pond_id"] != pondID {
			continue
		}
		b := p.Timestamp.UTC().Truncate(window).Unix()
		a := buckets[b]
		if a == nil {
			a = &acc{sum: map[string]float64{}, n: map[string]int{}}
			buckets[b] = a
		}
		for k, v := range p.Fields {
			a.sum[k] += v
			a.n[k]++
		}
	}
	keys := make([]int64, 0, len(buckets))
	for b := range buckets {
		keys = append(keys, b)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]historyPoint, 0, len(keys))
	for _, b := range keys {
		a := buckets[b]
		vals := make(map[string]float64, len(a.sum))
		for k, s := range a.sum {
			vals[k] = s / float64(a.n[k])
		}
		out = append(out, historyPoint{
			Timestamp: time.Unix(b, 0).UTC().Format(time.RFC3339),
			Values:    vals,
		})
	}
	return out
}
