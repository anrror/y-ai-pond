package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/anrror/y-ai-pond/pkg/dt/visual"
	"github.com/gin-gonic/gin"
)

// SetDTEngine wires the digital twin visualization engine into the Handler.
func (h *Handler) SetDTEngine(engine *visual.Visualizer) {
	h.dtEngine = engine
}

// getDTState handles GET /api/v1/dt/pond/:id/state.
// It returns the current virtual water state for a pond.
func (h *Handler) getDTState(c *gin.Context) {
	pondID := c.Param("id")
	if h.dtEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "digital twin not ready"})
		return
	}
	c.JSON(http.StatusOK, h.dtEngine.State(pondID))
}

// getDTTrajectory handles GET /api/v1/dt/pond/:id/trajectory.
// It returns a paginated simulation trajectory for a pond under a scenario.
// Query params: scenario (heatwave|storm_flood|cold_snap), offset, limit.
func (h *Handler) getDTTrajectory(c *gin.Context) {
	pondID := c.Param("id")
	if h.dtEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "digital twin not ready"})
		return
	}

	scenarioName := c.Query("scenario")
	if scenarioName == "" {
		badRequest(c, "scenario is required (heatwave|storm_flood|cold_snap)")
		return
	}
	offset, _ := strconv.Atoi(c.Query("offset"))
	limit, _ := strconv.Atoi(c.Query("limit"))

	tr, err := h.dtEngine.TrajectoryByName(pondID, scenarioName, offset, limit)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, tr)
}

// getDTCompare handles GET /api/v1/dt/compare.
// It runs multiple scenarios in parallel and returns side-by-side summaries.
// Query params: scenarios (comma-separated names).
func (h *Handler) getDTCompare(c *gin.Context) {
	if h.dtEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "digital twin not ready"})
		return
	}

	raw := c.Query("scenarios")
	if raw == "" {
		badRequest(c, "scenarios is required (comma-separated)")
		return
	}
	names := strings.Split(raw, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}

	res, err := h.dtEngine.Compare(names)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, res)
}

// getDTAnomaly handles GET /api/v1/dt/pond/:id/anomaly.
// It compares physical sensor readings against the virtual baseline and
// reports deviations exceeding thresholds.
// Query params: do_mg_l, temp_c, turbidity_ntu, nh3_mg_l.
func (h *Handler) getDTAnomaly(c *gin.Context) {
	pondID := c.Param("id")
	if h.dtEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "digital twin not ready"})
		return
	}

	phys := visual.PhysicalState{
		DO:           parseFloatQuery(c, "do_mg_l"),
		TemperatureC: parseFloatQuery(c, "temp_c"),
		Turbidity:    parseFloatQuery(c, "turbidity_ntu"),
		NH3:          parseFloatQuery(c, "nh3_mg_l"),
	}
	c.JSON(http.StatusOK, h.dtEngine.Anomaly(pondID, phys))
}

// parseFloatQuery parses a float query parameter, returning 0 on absence/error.
func parseFloatQuery(c *gin.Context, key string) float64 {
	v, err := strconv.ParseFloat(c.Query(key), 64)
	if err != nil {
		return 0
	}
	return v
}