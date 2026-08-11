// Package middleware provides Gin HTTP middleware for JWT authentication,
// farm-scope tenant isolation, and role-based access control.
//
// The middleware chain follows the pattern: AuthRequired → FarmScope → RoleRequired.
// Each middleware extracts or enriches claims stored in the Gin context under CtxClaims.
package middleware

import (
	"net/http"
	"strings"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/gin-gonic/gin"
)

// CtxClaims is the Gin context key for *auth.Claims injected by AuthRequired.
const CtxClaims = "auth.claims"

// AuthRequired extracts and validates a Bearer JWT from the Authorization header.
// On success, the parsed *auth.Claims are stored in the Gin context under CtxClaims.
// On failure, returns 401 with a JSON error body.
//
// The token is never logged; only the failure reason (sentinel error) appears in logs.
func AuthRequired(svc *auth.AuthService) gin.HandlerFunc {
	if svc == nil {
		// Fail-closed: no auth service = all requests are rejected.
		return func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrUnauthorized.Error(),
			})
		}
	}

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrMissingAuthHeader.Error(),
			})
			return
		}

		token, ok := parseBearer(authHeader)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrInvalidAuthFormat.Error(),
			})
			return
		}

		claims, err := svc.ParseToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": err.Error(),
			})
			return
		}

		c.Set(CtxClaims, claims)
		c.Next()
	}
}

// FarmScope enforces farm-level tenant isolation. It extracts the farm_id from
// a URL path parameter (default: "farm_id") or a query parameter, then verifies
// the authenticated user's Claims.FarmIDs include that farm.
//
// Returns 403 if the user does not have access to the requested farm, or if no
// farm_id is present in the request and requireFarm is true.
//
// Use FarmScope(false) for endpoints where the farm is optional — the handler
// receives the farm_id via the Gin context but no 403 is issued if absent.
func FarmScope(paramName string, requireFarm bool) gin.HandlerFunc {
	if paramName == "" {
		paramName = "farm_id"
	}

	return func(c *gin.Context) {
		farmID := c.Param(paramName)
		if farmID == "" {
			farmID = c.Query(paramName)
		}

		if farmID == "" {
			if requireFarm {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "farm_id is required",
				})
				return
			}
			c.Next()
			return
		}

		claims := getClaims(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrUnauthorized.Error(),
			})
			return
		}

		if !claims.HasFarmAccess(farmID) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": auth.ErrForbidden.Error(),
			})
			return
		}

		c.Next()
	}
}

// RoleRequired returns a middleware that checks the authenticated user's role.
//
// When roles is empty, ALL authenticated users are allowed (no-op beyond
// requiring AuthRequired to have run).
//
// When roles is non-empty, the user's Claims.Role must match at least one
// entry in the list. Returns 403 on mismatch.
//
// Common usage:
//
//	RoleRequired(auth.RoleAdmin)                            // admin only
//	RoleRequired(auth.RoleAdmin, auth.RoleOperator)         // admin or operator
//	RoleRequired()                                          // any authenticated user
func RoleRequired(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(roles) == 0 {
			c.Next()
			return
		}

		claims := getClaims(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrUnauthorized.Error(),
			})
			return
		}

		for _, r := range roles {
			if claims.Role == r {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": auth.ErrForbidden.Error(),
		})
	}
}

// RequireWrite returns a middleware that blocks viewers from write operations
// (POST/PUT/DELETE/PATCH). Admin and operator roles pass through.
//
// This is a convenience wrapper around RoleRequired that checks the HTTP method
// rather than a static role list — viewer GET requests are still allowed.
func RequireWrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			// Methods that modify resources — must have write access.
		default:
			c.Next()
			return
		}

		claims := getClaims(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": auth.ErrUnauthorized.Error(),
			})
			return
		}

		if !claims.CanWrite() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": auth.ErrForbidden.Error(),
			})
			return
		}

		c.Next()
	}
}

// GetClaims retrieves the parsed *auth.Claims from the Gin context.
// Returns nil if AuthRequired has not run or the token was invalid.
func GetClaims(c *gin.Context) *auth.Claims {
	return getClaims(c)
}

// getClaims is the internal helper. It performs a type-assertion on the
// value stored under CtxClaims.
func getClaims(c *gin.Context) *auth.Claims {
	val, exists := c.Get(CtxClaims)
	if !exists {
		return nil
	}
	claims, ok := val.(*auth.Claims)
	if !ok {
		return nil
	}
	return claims
}

// parseBearer extracts the token from an Authorization: Bearer <token> header.
// Case-insensitive prefix match — matches y-ai-agent-base behavior.
func parseBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}
