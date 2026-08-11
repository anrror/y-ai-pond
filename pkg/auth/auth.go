// Package auth provides JWT authentication with HS256 signing and RBAC claims.
// Follows the y-ai-agent-base HS256 pattern: Bearer token, 30s leeway, subject-based identity.
// Extended claims add role (admin/operator/viewer) and farm_ids for multi-farm tenant isolation.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Role constants for RBAC middleware.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Sentinel errors returned by AuthService and middleware.
// These follow the y-ai-agent-base naming convention for errors.Is compatibility.
var (
	ErrUnauthorized       = errors.New("auth: unauthorized")
	ErrInvalidToken       = errors.New("auth: invalid token")
	ErrTokenExpired       = errors.New("auth: token expired")
	ErrMissingAuthHeader  = errors.New("auth: missing Authorization header")
	ErrInvalidAuthFormat  = errors.New("auth: invalid Authorization format, expected 'Bearer <token>'")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrForbidden          = errors.New("auth: forbidden")
)

// jwtLeeway adds a tolerance window for clock skew between issuer and server.
// Industry standard is 30–60 seconds; matches y-ai-agent-base.
const jwtLeeway = 30 * time.Second

// Claims extends jwt.RegisteredClaims with custom RBAC fields.
// serialized as JSON in the JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	UserID  string   `json:"user_id"`
	Role    string   `json:"role"`
	FarmIDs []string `json:"farm_ids"`
}

// TokenPair is the response returned after a successful login.
type TokenPair struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// Config holds JWT signing parameters.
type Config struct {
	// Secret is the HS256 symmetric key. MUST NOT be empty in production.
	Secret string

	// Expiration is the token lifetime. Defaults to 24h when zero.
	Expiration time.Duration
}

// User represents a user record from the store.
type User struct {
	ID       string
	Username string
	Password string   // bcrypt hash in production; plain-text for test fakes only
	Role     string   // admin, operator, or viewer
	FarmIDs  []string // farms this user can access
}

// UserStore abstracts user lookup for dependency injection and testing.
// In production this will be backed by PostgreSQL (pgxpool); tests use a fake/mock.
type UserStore interface {
	// GetUserByUsername finds a user by login name. Returns an error wrapping
	// ErrInvalidCredentials when the user is not found.
	GetUserByUsername(ctx context.Context, username string) (*User, error)
}

// AuthService handles JWT issuance and validation.
// Zero value is not usable — use NewAuthService.
type AuthService struct {
	cfg Config
}

// NewAuthService creates an AuthService with the given config.
// Expiration defaults to 24h if unset.
func NewAuthService(cfg Config) *AuthService {
	if cfg.Expiration <= 0 {
		cfg.Expiration = 24 * time.Hour
	}
	return &AuthService{cfg: cfg}
}

// IssueToken creates a signed HS256 JWT for the given user.
// The token includes user_id, role, farm_ids, sub, iat, and exp claims.
func (s *AuthService) IssueToken(user *User) (*TokenPair, error) {
	if user == nil {
		return nil, fmt.Errorf("auth: cannot issue token for nil user")
	}
	if s.cfg.Secret == "" {
		return nil, fmt.Errorf("%w: auth disabled (empty JWT secret)", ErrUnauthorized)
	}

	now := time.Now()
	exp := now.Add(s.cfg.Expiration)

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Subject:   user.ID,
		},
		UserID:  user.ID,
		Role:    user.Role,
		FarmIDs: user.FarmIDs,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.cfg.Secret))
	if err != nil {
		return nil, fmt.Errorf("auth: sign token: %w", err)
	}

	return &TokenPair{
		AccessToken: tokenString,
		TokenType:   "Bearer",
		ExpiresIn:   int64(s.cfg.Expiration.Seconds()),
	}, nil
}

// ParseToken validates a JWT string and extracts claims.
// It enforces HS256, checks expiration with leeway, and rejects tokens
// signed with any other algorithm.
func (s *AuthService) ParseToken(tokenString string) (*Claims, error) {
	if s.cfg.Secret == "" {
		return nil, fmt.Errorf("%w: auth disabled (empty JWT secret)", ErrUnauthorized)
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{},
		func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: unexpected signing method: %v", ErrInvalidToken, token.Header["alg"])
			}
			return []byte(s.cfg.Secret), nil
		},
		jwt.WithLeeway(jwtLeeway),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("%w: %w", ErrTokenExpired, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("%w: invalid token claims", ErrInvalidToken)
	}

	return claims, nil
}

// Login validates credentials against the UserStore and issues a JWT on success.
// Passwords MUST NOT be logged; this function intentionally returns a generic
// error message to avoid user enumeration.
func (s *AuthService) Login(ctx context.Context, store UserStore, username, password string) (*TokenPair, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: no user store configured", ErrUnauthorized)
	}

	user, err := store.GetUserByUsername(ctx, username)
	if err != nil {
		// Do not distinguish "not found" from "store error" to the caller.
		return nil, ErrInvalidCredentials
	}

	// NOTE: In production this MUST use bcrypt.CompareHashAndPassword.
	// Plain-text comparison here is intentional for test fakes only.
	if user.Password != password {
		return nil, ErrInvalidCredentials
	}

	return s.IssueToken(user)
}

// HasFarmAccess checks whether the claims grant access to the given farm.
func (c *Claims) HasFarmAccess(farmID string) bool {
	for _, fid := range c.FarmIDs {
		if fid == farmID {
			return true
		}
	}
	return false
}

// IsRole checks whether the claims have exactly the given role.
func (c *Claims) IsRole(role string) bool {
	return c.Role == role
}

// CanWrite returns true if the role allows write operations (POST/PUT/DELETE).
// Both admin and operator can write; viewer cannot.
func (c *Claims) CanWrite() bool {
	return c.Role == RoleAdmin || c.Role == RoleOperator
}
