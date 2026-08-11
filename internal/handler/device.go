package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const (
	listDevicesQuery  = `SELECT id, farm_id, COALESCE(pond_id, ''), type, status, firmware_version, last_heartbeat FROM devices`
	getDeviceQuery    = `SELECT id, farm_id, COALESCE(pond_id, ''), type, status, firmware_version, last_heartbeat FROM devices WHERE id = $1`
	insertDeviceQuery = `INSERT INTO devices (farm_id, pond_id, type, status, firmware_version, last_heartbeat)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6)
		RETURNING id, farm_id, COALESCE(pond_id, ''), type, status, firmware_version, last_heartbeat`
	updateDeviceQuery = `UPDATE devices SET farm_id = $1, pond_id = $2, type = $3, status = $4,
		firmware_version = $5, last_heartbeat = $6
		WHERE id = $7
		RETURNING id, farm_id, COALESCE(pond_id, ''), type, status, firmware_version, last_heartbeat`
	deleteDeviceQuery = `DELETE FROM devices WHERE id = $1`
)

// deviceRequest is the JSON body for device create and update.
type deviceRequest struct {
	ID              string     `json:"id"`
	FarmID          string     `json:"farm_id" binding:"required"`
	PondID          string     `json:"pond_id"`
	Type            string     `json:"type" binding:"required"`
	Status          string     `json:"status"`
	FirmwareVersion string     `json:"firmware_version"`
	LastHeartbeat   *time.Time `json:"last_heartbeat"`
}

// rowScanner is satisfied by pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanDevice scans a device row whose pond_id is non-null via COALESCE.
func scanDevice(r rowScanner) (store.Device, error) {
	var d store.Device
	if err := r.Scan(&d.ID, &d.FarmID, &d.PondID, &d.Type, &d.Status, &d.FirmwareVersion, &d.LastHeartbeat); err != nil {
		return store.Device{}, err
	}
	return d, nil
}

func (h *Handler) listDevices(c *gin.Context) {
	limit, offset := pagination(c)
	query, args := listQuery(listDevicesQuery, map[string]string{"farm_id": c.Query("farm_id")}, limit, offset)
	rows, err := h.pg.Query(c.Request.Context(), query, args...)
	if err != nil {
		h.internalError(c, err)
		return
	}
	defer rows.Close()

	devices := []store.Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			h.internalError(c, err)
			return
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

func (h *Handler) createDevice(c *gin.Context) {
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	hb := time.Now()
	if req.LastHeartbeat != nil {
		hb = *req.LastHeartbeat
	}
	var d store.Device
	err := h.pg.QueryRow(c.Request.Context(), insertDeviceQuery,
		req.FarmID, req.PondID, req.Type, req.Status, req.FirmwareVersion, hb,
	).Scan(&d.ID, &d.FarmID, &d.PondID, &d.Type, &d.Status, &d.FirmwareVersion, &d.LastHeartbeat)
	if err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, d)
}

func (h *Handler) getDevice(c *gin.Context) {
	d, err := scanDevice(h.pg.QueryRow(c.Request.Context(), getDeviceQuery, c.Param("id")))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			notFound(c, "device not found")
			return
		}
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) updateDevice(c *gin.Context) {
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	hb := time.Now()
	if req.LastHeartbeat != nil {
		hb = *req.LastHeartbeat
	}
	var d store.Device
	err := h.pg.QueryRow(c.Request.Context(), updateDeviceQuery,
		req.FarmID, req.PondID, req.Type, req.Status, req.FirmwareVersion, hb, c.Param("id"),
	).Scan(&d.ID, &d.FarmID, &d.PondID, &d.Type, &d.Status, &d.FirmwareVersion, &d.LastHeartbeat)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			notFound(c, "device not found")
			return
		}
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) deleteDevice(c *gin.Context) {
	tag, err := h.pg.Exec(c.Request.Context(), deleteDeviceQuery, c.Param("id"))
	if err != nil {
		h.internalError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(c, "device not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
