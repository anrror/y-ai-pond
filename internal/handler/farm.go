package handler

import (
	"errors"
	"net/http"

	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const (
	listFarmsQuery  = `SELECT id, name, location, area_m2, species, created_at FROM farms`
	getFarmQuery    = `SELECT id, name, location, area_m2, species, created_at FROM farms WHERE id = $1`
	insertFarmQuery = `INSERT INTO farms (name, location, area_m2, species)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, location, area_m2, species, created_at`
	updateFarmQuery = `UPDATE farms SET name = $1, location = $2, area_m2 = $3, species = $4
		WHERE id = $5
		RETURNING id, name, location, area_m2, species, created_at`
	deleteFarmQuery = `DELETE FROM farms WHERE id = $1`
)

// farmRequest is the JSON body for farm create and update.
type farmRequest struct {
	Name     string  `json:"name" binding:"required"`
	Location string  `json:"location"`
	AreaM2   float64 `json:"area_m2"`
	Species  string  `json:"species"`
}

func (h *Handler) listFarms(c *gin.Context) {
	limit, offset := pagination(c)
	query, args := listQuery(listFarmsQuery, nil, limit, offset)
	rows, err := h.pg.Query(c.Request.Context(), query, args...)
	if err != nil {
		h.internalError(c, err)
		return
	}
	defer rows.Close()

	farms := []store.Farm{}
	for rows.Next() {
		var f store.Farm
		if err := rows.Scan(&f.ID, &f.Name, &f.Location, &f.AreaM2, &f.Species, &f.CreatedAt); err != nil {
			h.internalError(c, err)
			return
		}
		farms = append(farms, f)
	}
	if err := rows.Err(); err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"farms": farms})
}

func (h *Handler) createFarm(c *gin.Context) {
	var req farmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	var f store.Farm
	err := h.pg.QueryRow(c.Request.Context(), insertFarmQuery,
		req.Name, req.Location, req.AreaM2, req.Species,
	).Scan(&f.ID, &f.Name, &f.Location, &f.AreaM2, &f.Species, &f.CreatedAt)
	if err != nil {
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, f)
}

func (h *Handler) getFarm(c *gin.Context) {
	var f store.Farm
	err := h.pg.QueryRow(c.Request.Context(), getFarmQuery, c.Param("id")).
		Scan(&f.ID, &f.Name, &f.Location, &f.AreaM2, &f.Species, &f.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			notFound(c, "farm not found")
			return
		}
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, f)
}

func (h *Handler) updateFarm(c *gin.Context) {
	var req farmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err.Error())
		return
	}
	var f store.Farm
	err := h.pg.QueryRow(c.Request.Context(), updateFarmQuery,
		req.Name, req.Location, req.AreaM2, req.Species, c.Param("id"),
	).Scan(&f.ID, &f.Name, &f.Location, &f.AreaM2, &f.Species, &f.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			notFound(c, "farm not found")
			return
		}
		h.internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, f)
}

func (h *Handler) deleteFarm(c *gin.Context) {
	tag, err := h.pg.Exec(c.Request.Context(), deleteFarmQuery, c.Param("id"))
	if err != nil {
		h.internalError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(c, "farm not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
