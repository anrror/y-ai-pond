// Package handler implements the y-ai-pond cloud REST API (Gin): farms, devices,
// sensors, feeding logs, alerts, and dashboard summaries.
//
// Handlers depend on store.PgxPool for parameterized PostgreSQL SQL and on
// store.InfluxWriter for time-series queries; both are injectable so httptest
// unit tests can use pgxmock and a fake InfluxWriter.
package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/anrror/y-ai-pond/internal/middleware"
	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/cloud/realtime"
	"github.com/anrror/y-ai-pond/pkg/cloud/recommend"
	"github.com/anrror/y-ai-pond/pkg/dt/visual"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
)

// Handler implements the REST API route handlers.
type Handler struct {
	pg              store.PgxPool
	influx          store.InfluxWriter
	auth            *auth.AuthService
	hub             *realtime.Hub
	recommendEngine *recommend.RecommendEngine
	dtEngine        *visual.Visualizer
	log             *slog.Logger
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(pg store.PgxPool, influx store.InfluxWriter, svc *auth.AuthService, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{pg: pg, influx: influx, auth: svc, log: log}
}

// RegisterRoutes mounts the /api/v1 routes on a Gin engine or router group.
// Every route requires JWT auth; write routes additionally require a writable
// role (admin/operator). Farm-scope isolation is enforced for farm_id query
// parameters and farm path parameters.
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	v1 := r.Group("/api/v1")
	v1.Use(middleware.AuthRequired(h.auth))
	v1.Use(middleware.FarmScope("farm_id", false))

	v1.GET("/farms", h.listFarms)
	v1.POST("/farms", middleware.RequireWrite(), h.createFarm)
	v1.GET("/farms/:id", middleware.FarmScope("id", false), h.getFarm)
	v1.PUT("/farms/:id", middleware.RequireWrite(), middleware.FarmScope("id", false), h.updateFarm)
	v1.DELETE("/farms/:id", middleware.RequireWrite(), middleware.FarmScope("id", false), h.deleteFarm)

	v1.GET("/devices", h.listDevices)
	v1.POST("/devices", middleware.RequireWrite(), h.createDevice)
	v1.GET("/devices/:id", h.getDevice)
	v1.PUT("/devices/:id", middleware.RequireWrite(), h.updateDevice)
	v1.DELETE("/devices/:id", middleware.RequireWrite(), h.deleteDevice)

	v1.GET("/sensors/latest", h.getLatestSensors)
	v1.GET("/sensors/history", h.getSensorHistory)
	v1.GET("/feeding/logs", h.listFeedingLogs)
	v1.GET("/alerts", h.listAlerts)
	v1.GET("/dashboard/summary", h.getDashboardSummary)

	// AI recommendation engine (advisory only — never auto-executed).
	v1.POST("/recommend/feeding", h.postRecommendFeeding)
	v1.GET("/recommend/daily", h.getRecommendDaily)

	// Digital twin visualization (TIER 3).
	v1.GET("/dt/pond/:id/state", h.getDTState)
	v1.GET("/dt/pond/:id/trajectory", h.getDTTrajectory)
	v1.GET("/dt/compare", h.getDTCompare)
	v1.GET("/dt/pond/:id/anomaly", h.getDTAnomaly)

	// SSE streaming — auth via ?token= query param (browsers cannot set
	// headers on EventSource). These routes are NOT behind the middleware
	// chain; each handler parses the token manually.
	r.GET("/api/v1/stream/sensors", h.streamSensors)
	r.GET("/api/v1/stream/alerts", h.streamAlerts)

	// WebSocket — auth via ?token= query param, same reason.
	r.GET("/ws/dashboard", h.wsDashboard)
}

// queryFilterColumns is the canonical order for list endpoint equality filters.
var queryFilterColumns = []string{"farm_id", "pond_id", "status"}

// listQuery appends equality filters (bound as parameters) and pagination to a
// list query. The base query must contain no WHERE clause or semicolon.
func listQuery(base string, filterVals map[string]string, limit, offset int) (string, []any) {
	query := base
	args := make([]any, 0, len(filterVals)+2)
	clauses := make([]string, 0, len(filterVals))
	for _, col := range queryFilterColumns {
		if v := filterVals[col]; v != "" {
			args = append(args, v)
			clauses = append(clauses, col+" = $"+strconv.Itoa(len(args)))
		}
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)
	return query, args
}

// pagination parses page and page_size query params (defaults: page 1, size 20,
// max 100) into LIMIT/OFFSET values.
func pagination(c *gin.Context) (limit, offset int) {
	limit = 20
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	page := 1
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	return limit, (page - 1) * limit
}

// badRequest writes a 400 JSON error.
func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

// notFound writes a 404 JSON error.
func notFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"error": msg})
}

// internalError logs the error and writes a generic 500 JSON response.
func (h *Handler) internalError(c *gin.Context, err error) {
	h.log.Error("handler: request failed", "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}
