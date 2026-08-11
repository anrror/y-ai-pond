package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/cloud/realtime"
	"github.com/gin-gonic/gin"
)

// parseQueryToken extracts and validates a JWT from the ?token= query parameter.
// Returns nil, error on failure. The caller should abort the request with 401.

// SetHub wires the realtime Hub into the Handler so stream routes can
// publish and subscribe.
func (h *Handler) SetHub(hub *realtime.Hub) {
	h.hub = hub
}

// parseQueryToken extracts and validates a JWT from the ?token= query parameter.
func (h *Handler) parseQueryToken(c *gin.Context) (*auth.Claims, error) {
	tokenStr := c.Query("token")
	if tokenStr == "" {
		return nil, auth.ErrMissingAuthHeader
	}
	return h.auth.ParseToken(tokenStr)
}

// streamSensors handles GET /api/v1/stream/sensors?pond_id=X
// It streams real-time sensor data for a specific pond using Server-Sent Events.
// The token query parameter carries the JWT.
func (h *Handler) streamSensors(c *gin.Context) {
	claims, err := h.parseQueryToken(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	pondID := c.Query("pond_id")
	if pondID == "" {
		badRequest(c, "pond_id is required")
		return
	}

	// Resolve farm_id: use the first farm in the user's claims that the pond
	// belongs to. For multi-farm users, this is guarded by FarmScope but since
	// we're not using the middleware chain, we must validate that pondID belongs
	// to an authorized farm. The MQTT gateway ingests sensor data keyed by
	// (farmID, pondID), so we need both.
	//
	// Strategy: For now, the farm_id query parameter is optional. If not present,
	// we pick the first authorized farm. In production, the frontend should
	// pass farm_id alongside pond_id.
	farmID := c.Query("farm_id")
	if farmID == "" {
		if len(claims.FarmIDs) == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no authorized farms"})
			return
		}
		farmID = claims.FarmIDs[0]
	}

	if !claims.HasFarmAccess(farmID) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": auth.ErrForbidden.Error()})
		return
	}

	room := realtime.SensorRoom(farmID, pondID)
	subID := fmt.Sprintf("sse-sensor-%s-%s-%d", farmID, pondID, time.Now().UnixNano())
	sub, unsubscribe := h.hub.Subscribe(subID, room)
	defer unsubscribe()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	writer := realtime.NewSSEWriter(sub, c.Writer, func() { c.Writer.Flush() }, h.log, realtime.DefaultSSEConfig())
	_ = writer.Run()
}

// streamAlerts handles GET /api/v1/stream/alerts
// It streams real-time alert events for all authorized farms using SSE.
func (h *Handler) streamAlerts(c *gin.Context) {
	claims, err := h.parseQueryToken(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	if len(claims.FarmIDs) == 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no authorized farms"})
		return
	}

	// Subscribe to alert rooms for all authorized farms.
	rooms := make([]string, len(claims.FarmIDs))
	for i, fid := range claims.FarmIDs {
		rooms[i] = realtime.AlertRoom(fid)
	}

	subID := fmt.Sprintf("sse-alert-%d", time.Now().UnixNano())
	sub, unsubscribe := h.hub.Subscribe(subID, rooms...)
	defer unsubscribe()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	writer := realtime.NewSSEWriter(sub, c.Writer, func() { c.Writer.Flush() }, h.log, realtime.DefaultSSEConfig())
	_ = writer.Run()
}

// wsDashboard handles WebSocket /ws/dashboard
// It upgrades to WebSocket and subscribes to dashboard rooms for all authorized farms.
// The client can send control commands as JSON.
func (h *Handler) wsDashboard(c *gin.Context) {
	claims, err := h.parseQueryToken(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	if len(claims.FarmIDs) == 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no authorized farms"})
		return
	}

	subID := fmt.Sprintf("ws-dashboard-%s-%d", claims.UserID, time.Now().UnixNano())
	if err := realtime.WSServe(
		c.Writer, c.Request,
		claims.FarmIDs,
		h.hub,
		subID,
		nil, // onCommand: wire MQTT command publish when gateway is available
		h.log,
	); err != nil {
		h.log.Debug("ws: upgrade failed", "error", err)
	}
}
