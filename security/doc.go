// Package security holds security audit tests for y-ai-pond:
// SQL injection resistance, MQTT authentication enforcement, rate limiting,
// Protobuf fuzz testing, and JWT token security.
//
// These tests verify the security design specified in README §12 and
// implement the acceptance criteria for T33.
//
// All tests are self-contained — no Docker, no external services required.
package security
