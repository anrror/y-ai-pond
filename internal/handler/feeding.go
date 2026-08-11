package handler

import (
	"net/http"

	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
)

const listFeedingLogsQuery = `SELECT id, pond_id, speed, duration, decision_json, created_at FROM feeding_logs`

// listFeedingLogs returns feeding logs, optionally filtered by pond_id.
func (h *Handler) listFeedingLogs(c *gin.Context) {
	limit, offset := pagination(c)
	query, args := listQuery(listFeedingLogsQuery, map[string]string{"pond_id": c.Query("pond_id")}, limit, offset)
	rows, err := h.pg.Query(c.Request.Context(), query, args...)
	if err != nil {
		h.internalError(c, err)
		return
	}
	defer rows.Close()

	logs := []store.FeedingLog{}
	for rows.Next() {
		var l store.FeedingLog
		if err := rows.Scan(&l.ID, &l.PondID, &l.Speed, &l.Duration, &l.DecisionJSON, &l.CreatedAt); err != nil {
			h.internalError(c, err)
			return
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}
