package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key-32-chars-minimum"

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestService() *auth.AuthService {
	return auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
}

func issueToken(svc *auth.AuthService, userID, role string, farmIDs []string) string {
	user := &auth.User{ID: userID, Role: role, FarmIDs: farmIDs}
	pair, _ := svc.IssueToken(user)
	return pair.AccessToken
}

func setupRouter(handlers ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	for _, h := range handlers {
		r.Use(h)
	}
	return r
}

func TestAuthRequired_NoToken(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthRequired_MissingHeader(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthRequired_InvalidFormat(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Token xyz")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthRequired_BearerLowerCase(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for lowercase bearer, got %d", w.Code)
	}
}

func TestAuthRequired_ValidToken(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		claims := GetClaims(c)
		c.JSON(http.StatusOK, gin.H{
			"user_id": claims.UserID,
			"role":    claims.Role,
		})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthRequired_ExpiredToken(t *testing.T) {
	// Build an expired token manually — IssueToken guards against negative expiration.
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	now := time.Now()
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
			Subject:   "u1",
		},
		UserID: "u1",
		Role:   auth.RoleAdmin,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	expiredToken, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}

func TestAuthRequired_TamperedToken(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, nil)
	tampered := token + "X" // append garbage
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for tampered token, got %d", w.Code)
	}
}

func TestAuthRequired_NilService(t *testing.T) {
	r := setupRouter(AuthRequired(nil))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer anything")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for nil service (fail-closed), got %d", w.Code)
	}
}

func TestFarmScope_ValidFarm(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", true),
	)
	r.GET("/api/v1/farms/:farm_id/ponds", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleOperator, []string{"farm-a", "farm-b"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/farms/farm-a/ponds", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid farm, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFarmScope_CrossFarmForbidden(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", true),
	)
	r.GET("/api/v1/farms/:farm_id/ponds", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Token only has access to farm-a, but request targets farm-c.
	token := issueToken(svc, "user-1", auth.RoleOperator, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/farms/farm-c/ponds", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cross-farm access, got %d", w.Code)
	}
}

func TestFarmScope_NoClaims(t *testing.T) {
	// FarmScope without AuthRequired before it.
	r := setupRouter(FarmScope("farm_id", true))
	r.GET("/api/v1/farms/:farm_id/ponds", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/farms/farm-a/ponds", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when no claims present, got %d", w.Code)
	}
}

func TestFarmScope_OptionalNoFarm(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", false), // optional
	)
	r.GET("/api/v1/dashboard", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for optional farm, got %d", w.Code)
	}
}

func TestFarmScope_MissingRequiredFarm(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", true),
	)
	r.GET("/api/v1/farms/:other_id/ponds", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, []string{"farm-a"})
	w := httptest.NewRecorder()
	// URL has :other_id, not :farm_id — FarmScope looks for :farm_id which is empty.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/farms/farm-a/ponds?farm_id=", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when required farm missing, got %d", w.Code)
	}
}

func TestFarmScope_QueryParam(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", true),
	)
	r.GET("/api/v1/ponds", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleViewer, []string{"farm-b"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ponds?farm_id=farm-b", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for query param farm, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoleRequired_AdminOnlyPass(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RoleRequired(auth.RoleAdmin),
	)
	r.POST("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	token := issueToken(svc, "admin-1", auth.RoleAdmin, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for admin, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRoleRequired_OperatorBlockedFromAdmin(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RoleRequired(auth.RoleAdmin),
	)
	r.POST("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	token := issueToken(svc, "op-1", auth.RoleOperator, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for operator blocked from admin-only, got %d", w.Code)
	}
}

func TestRoleRequired_MultipleRoles(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RoleRequired(auth.RoleAdmin, auth.RoleOperator),
	)
	r.POST("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	for _, role := range []string{auth.RoleAdmin, auth.RoleOperator} {
		token := issueToken(svc, "u1", role, []string{"farm-a"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("role %q: expected 201, got %d", role, w.Code)
		}
	}
}

func TestRoleRequired_NoRoles(t *testing.T) {
	// Empty role list means "any authenticated user".
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RoleRequired(),
	)
	r.GET("/api/v1/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "v-1", auth.RoleViewer, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for no-role-required, got %d", w.Code)
	}
}

func TestRequireWrite_ViewerPOSTForbidden(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RequireWrite(),
	)
	r.POST("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	token := issueToken(svc, "viewer-1", auth.RoleViewer, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer POST, got %d", w.Code)
	}
}

func TestRequireWrite_ViewerGETAllowed(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RequireWrite(),
	)
	r.GET("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "viewer-1", auth.RoleViewer, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for viewer GET, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequireWrite_OperatorPOSTAllowed(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RequireWrite(),
	)
	r.POST("/api/v1/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	token := issueToken(svc, "op-1", auth.RoleOperator, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for operator POST, got %d", w.Code)
	}
}

func TestRequireWrite_ViewerPUTForbidden(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RequireWrite(),
	)
	r.PUT("/api/v1/devices/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "viewer-1", auth.RoleViewer, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/devices/d1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer PUT, got %d", w.Code)
	}
}

func TestRequireWrite_ViewerDeleteForbidden(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		RequireWrite(),
	)
	r.DELETE("/api/v1/devices/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "viewer-1", auth.RoleViewer, []string{"farm-a"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/d1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer DELETE, got %d", w.Code)
	}
}

func TestFullChain_AuthFarmRole(t *testing.T) {
	// Full chain: AuthRequired → FarmScope → RequireWrite.
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("farm_id", true),
		RequireWrite(),
	)
	r.POST("/api/v1/farms/:farm_id/devices", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})

	t.Run("valid admin", func(t *testing.T) {
		token := issueToken(svc, "admin-1", auth.RoleAdmin, []string{"farm-a"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/farms/farm-a/devices", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", w.Code)
		}
	})

	t.Run("viewer blocked", func(t *testing.T) {
		token := issueToken(svc, "viewer-1", auth.RoleViewer, []string{"farm-a"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/farms/farm-a/devices", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 for viewer, got %d", w.Code)
		}
	})

	t.Run("cross-farm blocked", func(t *testing.T) {
		token := issueToken(svc, "admin-1", auth.RoleAdmin, []string{"farm-a"})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/farms/farm-b/devices", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 for cross-farm, got %d", w.Code)
		}
	})

	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/farms/farm-a/devices", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without token, got %d", w.Code)
		}
	})
}

func TestGetClaims_NilWhenNotSet(t *testing.T) {
	r := setupRouter()
	r.GET("/test", func(c *gin.Context) {
		claims := GetClaims(c)
		if claims != nil {
			t.Error("expected nil claims when AuthRequired not run")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)
}

func TestGetClaims_ValidAfterAuth(t *testing.T) {
	svc := newTestService()
	r := setupRouter(AuthRequired(svc))
	r.GET("/test", func(c *gin.Context) {
		claims := GetClaims(c)
		if claims == nil {
			t.Error("expected non-nil claims after AuthRequired")
			return
		}
		if claims.UserID != "user-1" {
			t.Errorf("UserID = %q, want user-1", claims.UserID)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "user-1", auth.RoleAdmin, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestFarmScope_CustomParamName(t *testing.T) {
	svc := newTestService()
	r := setupRouter(
		AuthRequired(svc),
		FarmScope("id", true),
	)
	r.GET("/api/v1/ponds/:id/sensors", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	token := issueToken(svc, "u1", auth.RoleOperator, []string{"pond-1"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ponds/pond-1/sensors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for custom param name, got %d", w.Code)
	}
}
