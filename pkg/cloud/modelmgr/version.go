package modelmgr

import (
	"fmt"

	"github.com/blang/semver/v4"
)

// ============================================================================
// SemanticVersion
// ============================================================================

// SemanticVersion wraps a blang/semver.Version for model version comparison
// and ordering. It supports parsing, comparison, and string conversion.
type SemanticVersion struct {
	sv semver.Version
}

// ParseVersion parses a semantic version string (e.g. "1.2.0", "0.1.0-alpha").
// Returns an error if the string is not valid semver.
func ParseVersion(v string) (SemanticVersion, error) {
	sv, err := semver.Parse(v)
	if err != nil {
		return SemanticVersion{}, fmt.Errorf("invalid version %q: %w", v, err)
	}
	return SemanticVersion{sv: sv}, nil
}

// String returns the canonical semver string (e.g. "1.2.0").
func (v SemanticVersion) String() string {
	return v.sv.String()
}

// Compare compares two semantic versions.
// Returns -1 if v < other, 0 if v == other, 1 if v > other.
func (v SemanticVersion) Compare(other SemanticVersion) int {
	return v.sv.Compare(other.sv)
}

// LessThan returns true if v is strictly less than other.
func (v SemanticVersion) LessThan(other SemanticVersion) bool {
	return v.sv.LT(other.sv)
}

// GreaterThan returns true if v is strictly greater than other.
func (v SemanticVersion) GreaterThan(other SemanticVersion) bool {
	return v.sv.GT(other.sv)
}

// Equal returns true if the two versions are identical.
func (v SemanticVersion) Equal(other SemanticVersion) bool {
	return v.sv.EQ(other.sv)
}

// MustParseVersion is like ParseVersion but panics on error.
// Only use in tests or init-time configuration.
func MustParseVersion(v string) SemanticVersion {
	sv, err := ParseVersion(v)
	if err != nil {
		panic(fmt.Sprintf("MustParseVersion(%q): %v", v, err))
	}
	return sv
}

// ============================================================================
// Version helpers for the registry
// ============================================================================

// compareVersionStrings parses two version strings and compares them.
// Returns -1, 0, 1, or an error if parsing fails.
func compareVersionStrings(a, b string) (int, error) {
	va, err := ParseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("parsing version %q: %w", a, err)
	}
	vb, err := ParseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("parsing version %q: %w", b, err)
	}
	return va.Compare(vb), nil
}
