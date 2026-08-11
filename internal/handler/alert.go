package handler

import (
	"net/http"

	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
)

const listAlertsQuery = `SELECT id, farm_id, pond_id, level, type, message, status, created_at, resolved_at FROM alerts`

// listAlerts returns alerts, optionally filtered by farm_id, pond_id, and status.
func (h *Handler) listAlerts(c *gin.Context) {
	limit, offset := pagination(c)
	query, args := listQuery(listAlertsQuery, map[string]string{
		"farm_id": c.Query("farm_id"),
		"pond_id": c.Query("pond_id"),
		"status":  c.Query("status"),
	}, limit, offset)
	rows, err := h.pg.Query(c.Request.Context(), query, args...)
	if err != nil {
		h.internalError(c, err)
		return
	}
	defer rows.Close()

	alerts := []store.Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			h.internalError(c, err)
			return
		}
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

// scanAlert scans an alert row, mapping nullable pond_id and resolved_at.
func scanAlert(r rowScanner) (store.Alert, error) {
	var a store.Alert
	var pondID pgtype.Text
	var resolvedAt pgtype.Timestamptz
	if err := r.Scan(&a.ID, &a.FarmID, &pondID, &a.Level, &a.Type, &a.Message, &a.Status, &a.CreatedAt, &resolvedAt); err != nil {
		return store.Alert{}, err
	}
	if pondID.Valid {
		a.PondID = &pondID.String
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		a.ResolvedAt = &t
	}
	return a, nil
}
