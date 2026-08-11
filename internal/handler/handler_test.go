package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/store"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v4"
)

const testSecret = "test-secret-key-32-chars-minimum"

// fakeInflux implements store.InfluxWriter for handler tests.
type fakeInflux struct {
	points []store.Point
	err    error
}

func (f *fakeInflux) WriteSensorData(context.Context, []store.SensorPoint) error { return nil }
func (f *fakeInflux) QueryTimeRange(context.Context, string, string, string) ([]store.Point, error) {
	return f.points, f.err
}
func (f *fakeInflux) Close() error { return nil }

func newTestHandler(t *testing.T, mock store.PgxPool, influx store.InfluxWriter) (*Handler, *auth.AuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	h := NewHandler(mock, influx, svc, nil)
	return h, svc
}

func testRouter(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

func authHeader(t *testing.T, svc *auth.AuthService, farmIDs []string) string {
	t.Helper()
	pair, err := svc.IssueToken(&auth.User{ID: "user-1", Role: auth.RoleAdmin, FarmIDs: farmIDs})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return "Bearer " + pair.AccessToken
}

func doReq(t *testing.T, r *gin.Engine, method, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGetLatestSensors(t *testing.T) {
	now := time.Now()
	influx := &fakeInflux{points: []store.Point{
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "ph"}, Timestamp: now.Add(-time.Minute), Fields: map[string]float64{"ph": 7.2}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "do"}, Timestamp: now.Add(-30 * time.Second), Fields: map[string]float64{"do": 6.5}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "temp"}, Timestamp: now.Add(-10 * time.Second), Fields: map[string]float64{"temp": 25.3}},
		{Tags: map[string]string{"farm_id": "farm-2", "pond_id": "pond-2", "sensor_type": "ph"}, Timestamp: now, Fields: map[string]float64{"ph": 8.0}},
	}}

	h, svc := newTestHandler(t, nil, influx)
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/sensors/latest?pond_id=pond-1", authHeader(t, svc, []string{"farm-1"}), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got []sensorReading
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("latest count = %d, want 3", len(got))
	}
	values := map[string]float64{}
	for _, s := range got {
		values[s.SensorType] = s.Value
	}
	if values["ph"] != 7.2 || values["do"] != 6.5 || values["temp"] != 25.3 {
		t.Fatalf("unexpected latest values: %+v", values)
	}
}

func TestGetLatestSensors_RequiresPondID(t *testing.T) {
	h, svc := newTestHandler(t, nil, &fakeInflux{})
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/sensors/latest", authHeader(t, svc, nil), "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetHistory(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	influx := &fakeInflux{points: []store.Point{
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "ph"}, Timestamp: base.Add(time.Minute), Fields: map[string]float64{"ph": 7.0}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "ph"}, Timestamp: base.Add(4 * time.Minute), Fields: map[string]float64{"ph": 7.4}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-1", "sensor_type": "do"}, Timestamp: base.Add(2 * time.Minute), Fields: map[string]float64{"do": 6.0}},
		{Tags: map[string]string{"farm_id": "farm-1", "pond_id": "pond-2", "sensor_type": "ph"}, Timestamp: base.Add(3 * time.Minute), Fields: map[string]float64{"ph": 9.0}},
	}}

	h, svc := newTestHandler(t, nil, influx)
	r := testRouter(t, h)
	from := base.Add(-time.Hour).Format(time.RFC3339)
	to := base.Add(2 * time.Hour).Format(time.RFC3339)
	path := "/api/v1/sensors/history?pond_id=pond-1&from=" + from + "&to=" + to + "&window=5m"
	w := doReq(t, r, http.MethodGet, path, authHeader(t, svc, []string{"farm-1"}), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp historyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PondID != "pond-1" || resp.Window != "5m" {
		t.Fatalf("unexpected metadata: %+v", resp)
	}
	if len(resp.Points) != 1 {
		t.Fatalf("bucket count = %d, want 1", len(resp.Points))
	}
	vals := resp.Points[0].Values
	if vals["ph"] != 7.2 || vals["do"] != 6.0 {
		t.Fatalf("unexpected aggregated values: %+v", vals)
	}
}

func TestCreateFarm(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	created := time.Now()
	mock.ExpectQuery("INSERT INTO farms").
		WithArgs("Tilapia Farm A", "Lake District", 1500.5, "tilapia").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "location", "area_m2", "species", "created_at"}).
			AddRow("farm-1", "Tilapia Farm A", "Lake District", 1500.5, "tilapia", created))

	h, svc := newTestHandler(t, mock, nil)
	r := testRouter(t, h)
	body := `{"name":"Tilapia Farm A","location":"Lake District","area_m2":1500.5,"species":"tilapia"}`
	w := doReq(t, r, http.MethodPost, "/api/v1/farms", authHeader(t, svc, []string{"farm-1"}), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var f store.Farm
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if f.ID != "farm-1" || f.Name != "Tilapia Farm A" || f.AreaM2 != 1500.5 {
		t.Fatalf("unexpected farm: %+v", f)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetDashboard(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery("total_devices").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"total_devices", "online_devices", "today_feeding_amount", "open_alerts"}).
			AddRow(int64(5), int64(3), 120.5, int64(2)))

	h, svc := newTestHandler(t, mock, nil)
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/summary", authHeader(t, svc, []string{"farm-1"}), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var s dashboardSummary
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if s.TotalDevices != 5 || s.OnlineDevices != 3 || s.TodayFeedingAmount != 120.5 || s.OpenAlerts != 2 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	h, _ := newTestHandler(t, nil, nil)
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/farms", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestListFeedingLogs(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery("FROM feeding_logs").
		WithArgs("pond-1", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pond_id", "speed", "duration", "decision_json", "created_at"}).
			AddRow("log-1", "pond-1", 60.5, 120, []byte(`{"state":"running"}`), time.Now()))

	h, svc := newTestHandler(t, mock, nil)
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/feeding/logs?pond_id=pond-1", authHeader(t, svc, []string{"farm-1"}), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Logs []store.FeedingLog `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].PondID != "pond-1" || resp.Logs[0].Speed != 60.5 {
		t.Fatalf("unexpected logs: %+v", resp.Logs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListAlerts(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery("FROM alerts").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "farm_id", "pond_id", "level", "type", "message", "status", "created_at", "resolved_at"}).
			AddRow("alert-1", "farm-1", "pond-1", "CRITICAL", "do_low", "DO below 4.0", "open", time.Now(), time.Now()))

	h, svc := newTestHandler(t, mock, nil)
	r := testRouter(t, h)
	w := doReq(t, r, http.MethodGet, "/api/v1/alerts", authHeader(t, svc, []string{"farm-1"}), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Alerts []store.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Alerts) != 1 || resp.Alerts[0].Level != "CRITICAL" || resp.Alerts[0].Status != "open" {
		t.Fatalf("unexpected alerts: %+v", resp.Alerts)
	}
	if resp.Alerts[0].PondID == nil || *resp.Alerts[0].PondID != "pond-1" {
		t.Fatalf("unexpected alert pond_id: %+v", resp.Alerts[0].PondID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCreateDevice(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery("INSERT INTO devices").
		WithArgs("farm-1", "pond-1", "sensor_node", "online", "v1.0", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "farm_id", "pond_id", "type", "status", "firmware_version", "last_heartbeat"}).
			AddRow("dev-1", "farm-1", "pond-1", "sensor_node", "online", "v1.0", time.Now()))

	h, svc := newTestHandler(t, mock, nil)
	r := testRouter(t, h)
	body := `{"farm_id":"farm-1","pond_id":"pond-1","type":"sensor_node","status":"online","firmware_version":"v1.0"}`
	w := doReq(t, r, http.MethodPost, "/api/v1/devices", authHeader(t, svc, []string{"farm-1"}), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var d store.Device
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if d.ID != "dev-1" || d.FarmID != "farm-1" || d.PondID != "pond-1" || d.Type != "sensor_node" {
		t.Fatalf("unexpected device: %+v", d)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
