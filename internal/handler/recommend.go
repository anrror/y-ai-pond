package handler

import (
	"net/http"

	"github.com/anrror/y-ai-pond/pkg/cloud/recommend"
	"github.com/gin-gonic/gin"
)

// SetRecommendEngine wires the AI recommendation engine into the Handler.
func (h *Handler) SetRecommendEngine(engine *recommend.RecommendEngine) {
	h.recommendEngine = engine
}

// recommendFeedingRequest is the JSON body for POST /api/v1/recommend/feeding.
type recommendFeedingRequest struct {
	PondID          string  `json:"pond_id" binding:"required"`
	DO              float64 `json:"do_mg_l"`
	Temp            float64 `json:"temp_c"`
	NH3             float64 `json:"nh3_mg_l"`
	FishWeight      float64 `json:"fish_weight_g"`
	FCR             float64 `json:"fcr"`
	Species         string  `json:"species"`
	StockingDensity float64 `json:"stocking_density"`
}

// postRecommendFeeding handles POST /api/v1/recommend/feeding.
// It generates an AI feeding recommendation for a pond using the
// AI recommendation engine.
func (h *Handler) postRecommendFeeding(c *gin.Context) {
	var req recommendFeedingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request: "+err.Error())
		return
	}

	if h.recommendEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation engine not available"})
		return
	}

	state := recommend.StateInput{
		PondID:          req.PondID,
		DO:              req.DO,
		Temp:            req.Temp,
		NH3:             req.NH3,
		FishWeight:      req.FishWeight,
		FCR:             req.FCR,
		Species:         req.Species,
		StockingDensity: req.StockingDensity,
	}

	rec := h.recommendEngine.RecommendFeeding(state, nil, nil, nil, nil)
	c.JSON(http.StatusOK, rec)
}

// getRecommendDaily handles GET /api/v1/recommend/daily.
// It generates a daily feeding plan for a pond.
func (h *Handler) getRecommendDaily(c *gin.Context) {
	pondID := c.Query("pond_id")
	if pondID == "" {
		badRequest(c, "pond_id is required")
		return
	}

	if h.recommendEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "recommendation engine not available"})
		return
	}

	// Build a minimal state from the pond ID only — in production this
	// would query the store for current sensor readings and growth data.
	state := recommend.StateInput{
		PondID: pondID,
	}

	daily := h.recommendEngine.RecommendDaily(state, nil, nil, nil, nil)
	c.JSON(http.StatusOK, daily)
}
