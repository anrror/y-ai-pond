package security

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/anrror/y-ai-pond/internal/handler"
	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

// TestSQLInjection_GetDeviceParameterized verifies that parameterized queries
// resist SQL injection in the device ID path parameter.
//
// The getDevice route (/api/v1/devices/:id) has no FarmScope middleware,
// only AuthRequired. The handler uses: SELECT ... FROM devices WHERE id = $1
func TestSQLInjection_GetDeviceParameterized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: "test-secret-key-32-chars-minimum"})

	payloads := []struct {
		name    string
		payload string
	}{
		{"OR 1=1 bypass", "' OR 1=1--"},
		{"DROP TABLE", "'; DROP TABLE devices;--"},
		{"UNION SELECT", "' UNION SELECT password FROM users--"},
		{"boolean blind", "' AND 1=1--"},
	}

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			if err != nil {
				t.Fatalf("pgxmock.NewPool: %v", err)
			}
			defer mock.Close()

			mock.ExpectQuery(`SELECT id, farm_id, COALESCE\(pond_id, ''\), type, status, firmware_version, last_heartbeat FROM devices`).
				WithArgs(p.payload).
				WillReturnError(pgx.ErrNoRows)

			h := handler.NewHandler(mock, &fakeInfluxWriter{}, svc, nil)
			r := gin.New()
			h.RegisterRoutes(r)

			token := issueToken(t, svc, "user-1", auth.RoleAdmin, []string{"farm-1"})

			encoded := url.PathEscape(p.payload)
			reqURL := "/api/v1/devices/" + encoded
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code == http.StatusInternalServerError {
				t.Errorf("got 500 — SQL injection may have altered query structure")
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("mock expectations not met (possible bypass): %v", err)
			}
		})
	}
}

// TestSQLInjection_ListFarmsParameterized verifies that listFarms uses
// parameterized LIMIT/OFFSET (no WHERE clause injection possible).
func TestSQLInjection_ListFarmsParameterized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: "test-secret-key-32-chars-minimum"})

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(`SELECT id, name, location, area_m2, species, created_at FROM farms`).
		WithArgs(20, 0).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "location", "area_m2", "species", "created_at"}).
			AddRow("farm-1", "Test Farm", "Test", 1000.0, "tilapia", nil))

	h := handler.NewHandler(mock, &fakeInfluxWriter{}, svc, nil)
	r := gin.New()
	h.RegisterRoutes(r)

	token := issueToken(t, svc, "user-1", auth.RoleAdmin, []string{"farm-1"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/farms?farm_id=farm-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

// TestSQLInjection_CreateFarmParameterized verifies that createFarm uses
// parameterized INSERT ($1, $2, ...).
func TestSQLInjection_CreateFarmParameterized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := auth.NewAuthService(auth.Config{Secret: "test-secret-key-32-chars-minimum"})

	injectionName := "test'; DROP TABLE farms;--"

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	mock.ExpectQuery(`INSERT INTO farms`).
		WithArgs(injectionName, "test-loc", 1000.0, "tilapia").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "location", "area_m2", "species", "created_at"}).
			AddRow("new-farm", injectionName, "test-loc", 1000.0, "tilapia", nil))

	h := handler.NewHandler(mock, &fakeInfluxWriter{}, svc, nil)
	r := gin.New()
	h.RegisterRoutes(r)

	token := issueToken(t, svc, "user-1", auth.RoleAdmin, []string{"farm-1"})

	body := `{"name":"` + injectionName + `","location":"test-loc","area_m2":1000,"species":"tilapia"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/farms", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Error("got 500 — SQL injection may have altered query structure")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}
