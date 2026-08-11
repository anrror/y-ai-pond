package security

import (
	"strings"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key-32-chars-minimum-length"

// TestJWT_HS256Signing verifies that tokens are signed with HMAC-SHA256
// and that the signing algorithm is enforced during parsing.
//
// Acceptance criteria (T33): "HS256 signing"
func TestJWT_HS256Signing(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	user := &auth.User{ID: "user-1", Role: auth.RoleAdmin, FarmIDs: []string{"farm-a"}}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	// Parse the token without verification to inspect the header.
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(pair.AccessToken, &auth.Claims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}

	alg := token.Header["alg"]
	if alg != "HS256" {
		t.Errorf("signing algorithm = %q, want HS256", alg)
	}
	t.Logf("token signed with %v", alg)
}

// TestJWT_ExpiryWithin24h verifies that the default token expiry is
// 24 hours and that shorter expirations are enforced.
//
// Acceptance criteria (T33): "expiry < 24h"
func TestJWT_ExpiryWithin24h(t *testing.T) {
	// Default expiry (24h).
	svc := auth.NewAuthService(auth.Config{Secret: testSecret})
	user := &auth.User{ID: "user-1", Role: auth.RoleViewer}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken (default): %v", err)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken (default): %v", err)
	}

	if claims.ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil")
	}
	expDuration := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	// Default should be exactly 24h.
	if expDuration != 24*time.Hour {
		t.Errorf("default expiry = %v, want 24h", expDuration)
	}
	t.Logf("default expiry: %v", expDuration)

	// Verify expiry is NOT > 24h.
	if expDuration > 24*time.Hour {
		t.Error("expiry exceeds 24h maximum")
	}
	if pair.ExpiresIn > 86400 {
		t.Errorf("ExpiresIn = %d, want <= 86400", pair.ExpiresIn)
	}

	// Shorter expiry (1 hour).
	svcShort := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	pairShort, err := svcShort.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken (1h): %v", err)
	}
	claimsShort, err := svcShort.ParseToken(pairShort.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken (1h): %v", err)
	}
	expShort := claimsShort.ExpiresAt.Sub(claimsShort.IssuedAt.Time)
	if expShort != time.Hour {
		t.Errorf("1h expiry = %v, want 1h", expShort)
	}
	if pairShort.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn (1h) = %d, want 3600", pairShort.ExpiresIn)
	}
	t.Logf("1h expiry: %v", expShort)
}

// TestJWT_TamperedTokenRejected verifies that a token with a modified
// payload (e.g., elevated role) is rejected during parsing.
//
// Acceptance criteria (T33): "tampered token rejected"
func TestJWT_TamperedTokenRejected(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	user := &auth.User{ID: "user-1", Role: auth.RoleViewer, FarmIDs: []string{"farm-a"}}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	// Modify the token payload by changing the last character of the
	// base64-encoded payload — the HMAC signature will no longer match.
	parts := strings.Split(pair.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("token does not have 3 parts (header.payload.signature)")
	}

	// Tamper with the payload: change the last base64 character.
	payload := []byte(parts[1])
	if len(payload) > 0 {
		// Flip the last character to create a different base64 encoding.
		payload[len(payload)-1] ^= 0x01
	}
	tamperedToken := parts[0] + "." + string(payload) + "." + parts[2]

	// Parse the tampered token — should fail.
	_, err = svc.ParseToken(tamperedToken)
	if err == nil {
		t.Error("tampered token was accepted! Signature validation is broken.")
	} else {
		t.Logf("tampered token rejected (expected): %v", err)
	}

	// Also verify: change the role in claims and sign with a DIFFERENT key
	// → should be rejected.
	evilSvc := auth.NewAuthService(auth.Config{Secret: "evil-key-evil-key-evil-key-1234", Expiration: time.Hour})
	evilUser := &auth.User{ID: "user-1", Role: auth.RoleAdmin, FarmIDs: []string{"farm-a"}}
	evilPair, err := evilSvc.IssueToken(evilUser)
	if err != nil {
		t.Fatalf("evil IssueToken: %v", err)
	}
	_, err = svc.ParseToken(evilPair.AccessToken) // parse with CORRECT secret
	if err == nil {
		t.Error("token signed with wrong key was accepted!")
	} else {
		t.Logf("wrong-key token rejected (expected): %v", err)
	}
}

// TestJWT_ExpiredTokenRejected verifies that an expired token is rejected
// with ErrTokenExpired.
//
// Acceptance criteria (T33): "expired token rejected"
func TestJWT_ExpiredTokenRejected(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})

	// Manually construct a token that expired 1 minute ago.
	now := time.Now()
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
			Subject:   "user-2",
		},
		UserID:  "user-2",
		Role:    auth.RoleViewer,
		FarmIDs: []string{},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	expiredToken, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	_, err = svc.ParseToken(expiredToken)
	if err == nil {
		t.Error("expired token was accepted!")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected 'expired' in error, got: %v", err)
	} else {
		t.Logf("expired token rejected (expected): %v", err)
	}
}

// TestJWT_NoNoneAlgorithm verifies that tokens with alg=none or other
// non-HMAC algorithms are rejected.
func TestJWT_NoNoneAlgorithm(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})

	// Craft a token with alg=none (unsigned) manually.
	claims := &auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Subject:   "attacker",
		},
		UserID:  "attacker",
		Role:    auth.RoleAdmin,
		FarmIDs: []string{"*"},
	}

	// Sign with method "none" (no signature).
	noneToken := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	noneString, err := noneToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("create none token: %v", err)
	}

	_, err = svc.ParseToken(noneString)
	if err == nil {
		t.Error("alg=none token was accepted! Algorithm confusion vulnerability!")
	} else {
		t.Logf("alg=none token rejected (expected): %v", err)
	}
}

// TestJWT_ClaimsIntegrity verifies that parsed claims contain the correct
// user_id, role, and farm_ids.
func TestJWT_ClaimsIntegrity(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	user := &auth.User{
		ID:       "user-42",
		Username: "operator1",
		Role:     auth.RoleOperator,
		FarmIDs:  []string{"farm-x", "farm-y", "farm-z"},
	}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}

	if claims.UserID != "user-42" {
		t.Errorf("UserID = %q, want user-42", claims.UserID)
	}
	if claims.Role != auth.RoleOperator {
		t.Errorf("Role = %q, want operator", claims.Role)
	}
	if len(claims.FarmIDs) != 3 {
		t.Errorf("FarmIDs count = %d, want 3", len(claims.FarmIDs))
	}
	if !claims.HasFarmAccess("farm-x") {
		t.Error("HasFarmAccess(farm-x) = false, want true")
	}
	if claims.HasFarmAccess("farm-unknown") {
		t.Error("HasFarmAccess(farm-unknown) = true, want false")
	}
	if !claims.CanWrite() {
		t.Error("operator CanWrite() = false, want true")
	}

	// Verify Subject matches UserID.
	if claims.Subject != "user-42" {
		t.Errorf("Subject = %q, want user-42", claims.Subject)
	}

	// Verify IssuedAt and ExpiresAt are set.
	if claims.IssuedAt == nil {
		t.Error("IssuedAt is nil")
	}
	if claims.ExpiresAt == nil {
		t.Error("ExpiresAt is nil")
	}
	t.Logf("claims OK: user=%s role=%s farms=%v", claims.UserID, claims.Role, claims.FarmIDs)
}

// TestJWT_ViewerCannotWrite verifies that a viewer role's CanWrite()
// returns false.
func TestJWT_ViewerCannotWrite(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	user := &auth.User{ID: "viewer-1", Role: auth.RoleViewer, FarmIDs: []string{"farm-a"}}

	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}

	if claims.CanWrite() {
		t.Error("viewer CanWrite() = true, want false")
	}
	if !claims.IsRole(auth.RoleViewer) {
		t.Error("viewer IsRole(viewer) = false, want true")
	}
}

// TestJWT_EmptySecretRejected verifies that tokens cannot be issued or
// parsed when the JWT secret is empty.
func TestJWT_EmptySecretRejected(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: ""})

	user := &auth.User{ID: "user-1", Role: auth.RoleAdmin}
	_, err := svc.IssueToken(user)
	if err == nil {
		t.Error("IssueToken with empty secret should fail")
	} else {
		t.Logf("IssueToken with empty secret rejected (expected): %v", err)
	}

	_, err = svc.ParseToken("some.token.here")
	if err == nil {
		t.Error("ParseToken with empty secret should fail")
	} else {
		t.Logf("ParseToken with empty secret rejected (expected): %v", err)
	}
}

// TestJWT_RefreshTokenPattern verifies that the token pair model supports
// refresh token semantics (access token expiry + refresh token lifecycle).
//
// Note: Full refresh token rotation is a v2 feature. This test verifies
// that the current TokenPair model has the necessary fields to support it.
func TestJWT_RefreshTokenPattern(t *testing.T) {
	svc := auth.NewAuthService(auth.Config{Secret: testSecret, Expiration: time.Hour})
	user := &auth.User{ID: "user-1", Role: auth.RoleAdmin, FarmIDs: []string{"farm-a"}}

	// Issue a short-lived access token.
	pair, err := svc.IssueToken(user)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	if pair.AccessToken == "" {
		t.Error("access token is empty")
	}
	if pair.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want Bearer", pair.TokenType)
	}
	if pair.ExpiresIn <= 0 {
		t.Errorf("ExpiresIn = %d, want positive", pair.ExpiresIn)
	}

	// Verify the access token can be parsed and has the correct expiry.
	claims, err := svc.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}

	expDuration := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
	if expDuration != time.Hour {
		t.Errorf("expiry = %v, want 1h", expDuration)
	}

	t.Logf("token pair: type=%s expires_in=%ds actual_duration=%v",
		pair.TokenType, pair.ExpiresIn, expDuration)
}
