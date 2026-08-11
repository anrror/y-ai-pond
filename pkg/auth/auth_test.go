package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key-32-chars-minimum"

// fakeStore is an in-memory UserStore for testing.
type fakeStore struct {
	users map[string]*User
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: make(map[string]*User)}
}

func (f *fakeStore) addUser(u *User) {
	f.users[u.Username] = u
}

func (f *fakeStore) GetUserByUsername(_ context.Context, username string) (*User, error) {
	u, ok := f.users[username]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

func TestIssueToken_ContainsUserIDRoleFarmIDs(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	user := &User{ID: "user-1", Username: "alice", Role: RoleAdmin, FarmIDs: []string{"farm-a", "farm-b"}}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("expected non-empty access token")
	}
	if pair.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", pair.TokenType)
	}
	if pair.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d, want 3600", pair.ExpiresIn)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", claims.UserID)
	}
	if claims.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
	if len(claims.FarmIDs) != 2 || claims.FarmIDs[0] != "farm-a" || claims.FarmIDs[1] != "farm-b" {
		t.Errorf("FarmIDs = %v, want [farm-a, farm-b]", claims.FarmIDs)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", claims.Subject)
	}
}

func TestIssueToken_DefaultExpiration(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret})
	user := &User{ID: "u1", Role: RoleViewer}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if pair.ExpiresIn != 86400 {
		t.Errorf("ExpiresIn = %d, want 86400 (24h default)", pair.ExpiresIn)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt claim")
	}
	expectedExp := time.Now().Add(24 * time.Hour)
	delta := claims.ExpiresAt.Sub(expectedExp)
	if delta < -time.Second || delta > time.Second {
		t.Errorf("ExpiresAt ~%v, want ~%v (24h from now)", claims.ExpiresAt.Time, expectedExp)
	}
}

func TestParseToken_ExpiredToken(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret})
	// Build a token manually with an expired exp claim — IssueToken's
	// constructor guards negative expiration, so we sign directly.
	now := time.Now()
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
			Subject:   "u1",
		},
		UserID: "u1",
		Role:   RoleOperator,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = svc.ParseToken(tokenString)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestParseToken_ExpiredWithinLeeway(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: -(5 * time.Second)})
	user := &User{ID: "u1", Role: RoleOperator}
	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken (expired within leeway): %v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("UserID = %q, want u1", claims.UserID)
	}
}

func TestParseToken_WrongSecret(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	user := &User{ID: "u1", Role: RoleViewer}
	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	otherSvc := NewAuthService(Config{Secret: "wrong-secret-key-for-testing", Expiration: time.Hour})
	_, err = otherSvc.ParseToken(pair.AccessToken)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestParseToken_EmptySecret(t *testing.T) {
	svc := NewAuthService(Config{Secret: "", Expiration: time.Hour})
	_, err := svc.ParseToken("any.token.here")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestParseToken_Malformed(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	_, err := svc.ParseToken("not-a-jwt")
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestParseToken_EmptyToken(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	_, err := svc.ParseToken("")
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestLogin_ValidCredentials(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	store := newFakeStore()
	store.addUser(&User{ID: "user-1", Username: "alice", Password: "secret123", Role: RoleAdmin, FarmIDs: []string{"farm-1"}})

	pair, err := svc.Login(context.Background(), store, "alice", "secret123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", claims.UserID)
	}
	if claims.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
	if len(claims.FarmIDs) != 1 || claims.FarmIDs[0] != "farm-1" {
		t.Errorf("FarmIDs = %v, want [farm-1]", claims.FarmIDs)
	}
}

func TestLogin_InvalidPassword(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	store := newFakeStore()
	store.addUser(&User{ID: "user-1", Username: "alice", Password: "correct", Role: RoleViewer})

	_, err := svc.Login(context.Background(), store, "alice", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	store := newFakeStore()

	_, err := svc.Login(context.Background(), store, "nobody", "pass")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_NoStore(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	_, err := svc.Login(context.Background(), nil, "alice", "pass")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestClaims_HasFarmAccess(t *testing.T) {
	c := &Claims{FarmIDs: []string{"farm-a", "farm-b"}}
	if !c.HasFarmAccess("farm-a") {
		t.Error("expected access to farm-a")
	}
	if !c.HasFarmAccess("farm-b") {
		t.Error("expected access to farm-b")
	}
	if c.HasFarmAccess("farm-c") {
		t.Error("unexpected access to farm-c")
	}
	if c.HasFarmAccess("") {
		t.Error("unexpected access to empty farm")
	}
}

func TestClaims_HasFarmAccess_EmptyFarms(t *testing.T) {
	c := &Claims{FarmIDs: nil}
	if c.HasFarmAccess("farm-a") {
		t.Error("unexpected access with nil FarmIDs")
	}
}

func TestClaims_IsRole(t *testing.T) {
	c := &Claims{Role: RoleOperator}
	if !c.IsRole(RoleOperator) {
		t.Error("expected operator role")
	}
	if c.IsRole(RoleAdmin) {
		t.Error("unexpected admin role")
	}
	if c.IsRole(RoleViewer) {
		t.Error("unexpected viewer role")
	}
}

func TestClaims_CanWrite(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{RoleAdmin, true},
		{RoleOperator, true},
		{RoleViewer, false},
	}
	for _, tt := range tests {
		c := &Claims{Role: tt.role}
		if got := c.CanWrite(); got != tt.want {
			t.Errorf("CanWrite for %q = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestIssueToken_NilUser(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret})
	_, err := svc.IssueToken(nil)
	if err == nil {
		t.Fatal("expected error for nil user")
	}
}

func TestIssueToken_EmptySecret(t *testing.T) {
	svc := NewAuthService(Config{Secret: ""})
	user := &User{ID: "u1", Role: RoleViewer}
	_, err := svc.IssueToken(user)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestIssueToken_AllRoles(t *testing.T) {
	for _, role := range []string{RoleAdmin, RoleOperator, RoleViewer} {
		svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
		user := &User{ID: "u1", Role: role, FarmIDs: []string{"f1"}}
		pair, err := svc.IssueToken(user)
		if err != nil {
			t.Fatalf("IssueToken for %q: %v", role, err)
		}
		claims, err := svc.ParseToken(pair.AccessToken)
		if err != nil {
			t.Fatalf("ParseToken for %q: %v", role, err)
		}
		if claims.Role != role {
			t.Errorf("Role = %q, want %q", claims.Role, role)
		}
	}
}

func TestIssueToken_MultipleFarms(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	farmIDs := []string{"farm-a", "farm-b", "farm-c", "farm-d"}
	user := &User{ID: "multi", Role: RoleOperator, FarmIDs: farmIDs}
	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	for _, fid := range farmIDs {
		if !claims.HasFarmAccess(fid) {
			t.Errorf("missing access to %q", fid)
		}
	}
}

func TestSentinelErrors_AreDistinct(t *testing.T) {
	errs := []error{
		ErrUnauthorized,
		ErrInvalidToken,
		ErrTokenExpired,
		ErrMissingAuthHeader,
		ErrInvalidAuthFormat,
		ErrInvalidCredentials,
		ErrForbidden,
	}
	for i, a := range errs {
		for j, b := range errs {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) incorrectly matches sentinel %d (%v)", i, a, j, b)
			}
		}
	}
}

func TestParseToken_InvalidSignature(t *testing.T) {
	svc := NewAuthService(Config{Secret: testSecret, Expiration: time.Hour})
	user := &User{ID: "u1", Role: RoleViewer}
	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	// Tamper by appending garbage — breaks base64/signature.
	tampered := pair.AccessToken + "X"
	_, err = svc.ParseToken(tampered)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for tampered token, got %v", err)
	}
}
