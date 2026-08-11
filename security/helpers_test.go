package security

import (
	"context"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/auth"
	"github.com/anrror/y-ai-pond/pkg/store"
)

// fakeInfluxWriter implements store.InfluxWriter with no-op methods for tests.
type fakeInfluxWriter struct{}

func (f *fakeInfluxWriter) WriteSensorData(_ context.Context, _ []store.SensorPoint) error {
	return nil
}
func (f *fakeInfluxWriter) QueryTimeRange(_ context.Context, _, _, _ string) ([]store.Point, error) {
	return nil, nil
}
func (f *fakeInfluxWriter) Close() error { return nil }

// issueToken creates a signed JWT for test authentication.
func issueToken(t *testing.T, svc *auth.AuthService, userID, role string, farmIDs []string) string {
	t.Helper()
	pair, err := svc.IssueToken(&auth.User{ID: userID, Role: role, FarmIDs: farmIDs})
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	return pair.AccessToken
}
