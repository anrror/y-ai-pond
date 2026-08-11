package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// dashboardSummary is the /dashboard/summary response body.
type dashboardSummary struct {
	TotalDevices       int64   `json:"total_devices"`
	OnlineDevices      int64   `json:"online_devices"`
	TodayFeedingAmount float64 `json:"today_feeding_amount"`
	OpenAlerts         int64   `json:"open_alerts"`
}

const dashboardSummaryQuery = `SELECT
	(SELECT COUNT(*) FROM devices) AS total_devices,
	(SELECT COUNT(*) FROM devices WHERE status = 'online') AS online_devices,
	(SELECT COALESCE(SUM(speed * duration), 0) FROM feeding_logs WHERE created_at >= $1) AS today_feeding_amount,
	(SELECT COUNT(*) FROM alerts WHERE status = 'open') AS open_alerts`

// getDashboardSummary returns the dashboard headline metrics.
func (h *Handler) getDashboardSummary(c *gin.Context) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var s dashboardSummary
	err := h.pg.QueryRow(c.Request.Context(), dashboardSummaryQuery, today).
		Scan(&s.TotalDevices, &s.OnlineDevices, &s.TodayFeedingAmount, &s.OpenAlerts)
	if err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, s)
}
