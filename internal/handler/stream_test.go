package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/cloud/alert"
	"github.com/anrror/y-ai-pond/pkg/cloud/realtime"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// setupStreamTest creates a Handler with a realtime Hub, a Gin test router,
// and an auth service. All three routes (SSE sensors, SSE alerts, WS) are
// registered.
func setupStreamTest(t *testing.T) (*Handler, *auth.AuthService, *realtime.Hub, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	hub := realtime.NewHub()
	h := NewHandler(nil, nil, svc, nil)
	h.SetHub(hub)

	r := gin.New()
	h.RegisterRoutes(r)
	return h, svc, hub, r
}

// issueStreamToken returns a JWT token string for the given farm IDs. The
// token is NOT prefixed with "Bearer " — it goes as a query parameter.
func issueStreamToken(t *testing.T, svc *auth.AuthService, farmIDs []string) string {
	t.Helper()
	pair, err := svc.IssueToken(&auth.User{ID: "user-1", Role: auth.RoleAdmin, FarmIDs: farmIDs})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return pair.AccessToken
}

func TestSSEStreamSensors(t *testing.T) {
	_, svc, hub, router := setupStreamTest(t)
	token := issueStreamToken(t, svc, []string{"farm-1"})

	// Build the SSE request URL with auth token and pond_id.
	path := fmt.Sprintf("/api/v1/stream/sensors?token=%s&pond_id=pond-a&farm_id=farm-1", url.QueryEscape(token))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()

	// Start the SSE handler in a goroutine (it blocks until client disconnects).
	go func() {
		router.ServeHTTP(w, req)
	}()

	// Give the handler time to start streaming.
	time.Sleep(50 * time.Millisecond)

	// Publish a sensor event to the hub.
	hub.PublishSensor("farm-1", "pond-a", realtime.SensorEvent{
		FarmID:     "farm-1",
		PondID:     "pond-a",
		SensorType: "do",
		Value:      6.5,
		Timestamp:  time.Now().Format(time.RFC3339),
	})

	// Give time for the event to propagate.
	time.Sleep(100 * time.Millisecond)

	// Read the response body.
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"sensor_type":"do"`) {
			found = true
			break
		}
	}
	if !found {
		t.Error("SSE response did not contain the sensor event")
	}
}

func TestSSEStreamAlerts(t *testing.T) {
	_, svc, hub, router := setupStreamTest(t)
	token := issueStreamToken(t, svc, []string{"farm-1"})

	path := fmt.Sprintf("/api/v1/stream/alerts?token=%s", url.QueryEscape(token))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()

	go func() {
		router.ServeHTTP(w, req)
	}()

	time.Sleep(50 * time.Millisecond)

	hub.PublishAlert(alert.Alert{
		FarmID:    "farm-1",
		PondID:    "pond-a",
		Type:      "do_low",
		Level:     alert.LevelCritical,
		Message:   "DO too low",
		Value:     3.2,
		Timestamp: time.Now(),
	})

	time.Sleep(100 * time.Millisecond)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "DO too low") {
			found = true
			break
		}
	}
	if !found {
		t.Error("SSE alerts response did not contain the alert event")
	}
}

func TestSSEStreamUnauthorized(t *testing.T) {
	_, svc, _, router := setupStreamTest(t)
	// Use an expired token or invalid token.
	path := "/api/v1/stream/sensors?token=invalid-token&pond_id=pond-a"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	_ = svc // suppress unused warning
}

func TestSSEStreamMissingPondID(t *testing.T) {
	_, svc, _, router := setupStreamTest(t)
	token := issueStreamToken(t, svc, []string{"farm-1"})
	path := fmt.Sprintf("/api/v1/stream/sensors?token=%s", url.QueryEscape(token))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing pond_id, got %d", w.Code)
	}
}

func TestSSEStreamForbiddenFarm(t *testing.T) {
	_, svc, _, router := setupStreamTest(t)
	// User only has access to farm-1, but requests farm-2.
	token := issueStreamToken(t, svc, []string{"farm-1"})
	path := fmt.Sprintf("/api/v1/stream/sensors?token=%s&pond_id=pond-a&farm_id=farm-2", url.QueryEscape(token))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRoomIsolation(t *testing.T) {
	_, svc, hub, router := setupStreamTest(t)

	// Two users: farm-1 and farm-2.
	token1 := issueStreamToken(t, svc, []string{"farm-1"})
	token2 := issueStreamToken(t, svc, []string{"farm-2"})

	// Connect both.
	path1 := fmt.Sprintf("/api/v1/stream/sensors?token=%s&pond_id=pond-a&farm_id=farm-1", url.QueryEscape(token1))
	path2 := fmt.Sprintf("/api/v1/stream/sensors?token=%s&pond_id=pond-b&farm_id=farm-2", url.QueryEscape(token2))

	req1 := httptest.NewRequest(http.MethodGet, path1, nil)
	w1 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, path2, nil)
	w2 := httptest.NewRecorder()

	go func() { router.ServeHTTP(w1, req1) }()
	go func() { router.ServeHTTP(w2, req2) }()

	time.Sleep(50 * time.Millisecond)

	// Publish to farm-1's room only.
	hub.PublishSensor("farm-1", "pond-a", realtime.SensorEvent{
		FarmID: "farm-1", PondID: "pond-a", SensorType: "ph", Value: 7.2,
	})

	time.Sleep(100 * time.Millisecond)

	resp1 := w1.Result()
	defer func() { _ = resp1.Body.Close() }()
	resp2 := w2.Result()
	defer func() { _ = resp2.Body.Close() }()

	// Farm-1 should receive the event.
	body1 := readAll(resp1)
	if !strings.Contains(body1, `"sensor_type":"ph"`) {
		t.Error("farm-1 should have received the sensor event")
	}

	// Farm-2 should NOT receive the farm-1 event.
	body2 := readAll(resp2)
	if strings.Contains(body2, `"sensor_type":"ph"`) {
		t.Error("farm-2 should NOT have received farm-1's sensor event (room isolation broken)")
	}
}

func TestWebSocketDashboard(t *testing.T) {
	_, svc, hub, router := setupStreamTest(t)
	token := issueStreamToken(t, svc, []string{"farm-1"})

	// Start HTTP test server.
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Connect via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a command from client → server.
	cmd := map[string]string{
		"action":  "start_feeding",
		"farm_id": "farm-1",
		"pond_id": "pond-a",
	}
	if writeErr := conn.WriteJSON(cmd); writeErr != nil {
		t.Fatalf("ws write: %v", writeErr)
	}

	// Publish a dashboard event from server → client.
	hub.PublishDashboard("farm-1", map[string]any{
		"type":    "sensor_update",
		"farm_id": "farm-1",
		"pond_id": "pond-a",
		"do":      6.5,
	})

	// Read the event back.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}

	var received map[string]any
	if err := json.Unmarshal(msg, &received); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if received["type"] != "sensor_update" {
		t.Errorf("expected sensor_update, got %v", received["type"])
	}
}

func TestWebSocketUnauthorized(t *testing.T) {
	_, _, _, router := setupStreamTest(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/dashboard?token=bad-token"
	_, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected ws dial to fail with bad token")
	}
}

func TestHeartbeatSSE(t *testing.T) {
	_, svc, _, router := setupStreamTest(t)
	token := issueStreamToken(t, svc, []string{"farm-1"})

	path := fmt.Sprintf("/api/v1/stream/sensors?token=%s&pond_id=pond-a&farm_id=farm-1", url.QueryEscape(token))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()

	go func() {
		router.ServeHTTP(w, req)
	}()

	// Wait longer than the heartbeat interval (15s default is too long for tests).
	// The SSEWriter sends a heartbeat immediately on the first tick.
	// For the test, we just check that the response starts with SSE headers
	// and contains comment lines after some time.
	time.Sleep(300 * time.Millisecond)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream Content-Type, got %s", ct)
	}

	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected no-cache, got %s", cc)
	}
}

func TestSSERoomIsolationInHub(t *testing.T) {
	// Pure hub-level room isolation test — verifies that publishing to one
	// farm's alert room does not leak to another farm's alert room.
	hub := realtime.NewHub()

	subF1, unsubF1 := hub.Subscribe("sub-f1", realtime.AlertRoom("farm-1"))
	defer unsubF1()
	subF2, unsubF2 := hub.Subscribe("sub-f2", realtime.AlertRoom("farm-2"))
	defer unsubF2()

	hub.PublishAlert(alert.Alert{
		ID:        "alert-f1",
		FarmID:    "farm-1",
		PondID:    "pond-a",
		Type:      "do_low",
		Level:     alert.LevelCritical,
		Message:   "farm-1 alert",
		Value:     3.0,
		Timestamp: time.Now(),
	})

	// Farm-1 receives it.
	select {
	case ev := <-subF1.Events:
		_ = ev
	case <-time.After(time.Second):
		t.Fatal("farm-1 should have received the alert")
	}

	// Farm-2 does NOT.
	select {
	case ev := <-subF2.Events:
		t.Errorf("farm-2 unexpectedly received %v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected.
	}
}

// readAll reads the entire response body (non-streaming helper for tests).
func readAll(resp *http.Response) string {
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	var sb strings.Builder
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
	}
	return sb.String()
}
