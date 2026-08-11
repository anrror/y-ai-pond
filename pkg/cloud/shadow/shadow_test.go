package shadow

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockStore implements Store with an in-memory map.
type mockStore struct {
	mu   sync.Mutex
	data map[string]*Shadow
}

func newMockStore() *mockStore {
	return &mockStore{data: make(map[string]*Shadow)}
}

func (m *mockStore) GetShadow(_ context.Context, deviceID string) (*Shadow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.data[deviceID]
	if !ok {
		return nil, errors.New("not found")
	}
	return sh, nil
}

func (m *mockStore) PutShadow(_ context.Context, s *Shadow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	cp.Reported = copyMap(s.Reported)
	cp.Desired = copyMap(s.Desired)
	cp.Delta = copyMap(s.Delta)
	m.data[s.DeviceID] = &cp
	return nil
}

// mockReporter records publish calls for assertions.
type mockReporter struct {
	mu           sync.Mutex
	configCalls  []configCall
	modelCalls   []modelCall
	configErr    error
	modelErr     error
	failCount    int // number of model publish calls to fail before succeeding
	failAttempts int
}

type configCall struct {
	deviceID string
	delta    map[string]any
}

type modelCall struct {
	deviceID string
	cmd      OTACommand
}

func newMockReporter() *mockReporter {
	return &mockReporter{}
}

func (m *mockReporter) PublishConfigUpdate(_ context.Context, deviceID string, delta map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configCalls = append(m.configCalls, configCall{deviceID: deviceID, delta: delta})
	return m.configErr
}

func (m *mockReporter) PublishModelUpdate(_ context.Context, deviceID string, cmd OTACommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelCalls = append(m.modelCalls, modelCall{deviceID: deviceID, cmd: cmd})
	m.failAttempts++
	if m.modelErr != nil && m.failAttempts <= m.failCount {
		return m.modelErr
	}
	return nil
}

func (m *mockReporter) ConfigCalls() []configCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]configCall{}, m.configCalls...)
}

func (m *mockReporter) ModelCalls() []modelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelCall{}, m.modelCalls...)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func copyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func newTestService() (*Service, *mockStore, *mockReporter) {
	store := newMockStore()
	rep := newMockReporter()
	svc := NewService(store, rep)
	return svc, store, rep
}

// ---------------------------------------------------------------------------
// Delta tests
// ---------------------------------------------------------------------------

func TestComputeDelta_when_values_differ(t *testing.T) {
	reported := map[string]any{"fuzzy_kp": 0.8, "fuzzy_ki": 0.1}
	desired := map[string]any{"fuzzy_kp": 0.9, "fuzzy_ki": 0.1}

	delta := ComputeDelta(reported, desired)

	if v, ok := delta["fuzzy_kp"]; !ok || v != 0.9 {
		t.Fatalf("expected delta[fuzzy_kp]=0.9, got %v", delta["fuzzy_kp"])
	}
	if _, ok := delta["fuzzy_ki"]; ok {
		t.Fatal("expected fuzzy_ki NOT in delta (values equal)")
	}
}

func TestComputeDelta_when_key_missing_in_reported(t *testing.T) {
	reported := map[string]any{}
	desired := map[string]any{"new_param": 42}

	delta := ComputeDelta(reported, desired)

	if v, ok := delta["new_param"]; !ok || v != 42 {
		t.Fatalf("expected delta[new_param]=42, got %v", delta["new_param"])
	}
}

func TestComputeDelta_when_reported_has_extra_keys(t *testing.T) {
	reported := map[string]any{"extra": "val", "shared": 1}
	desired := map[string]any{"shared": 1}

	delta := ComputeDelta(reported, desired)

	if len(delta) != 0 {
		t.Fatalf("expected empty delta, got %v", delta)
	}
}

func TestComputeDelta_when_nested_values_differ(t *testing.T) {
	reported := map[string]any{"config": map[string]any{"a": 1, "b": 2}}
	desired := map[string]any{"config": map[string]any{"a": 1, "b": 3}}

	delta := ComputeDelta(reported, desired)

	if _, ok := delta["config"]; !ok {
		t.Fatal("expected delta to contain config")
	}
}

// ---------------------------------------------------------------------------
// Service — UpdateDesired
// ---------------------------------------------------------------------------

func TestShadowDelta(t *testing.T) {
	svc, _, rep := newTestService()
	ctx := context.Background()

	// seed reported via ReportReported
	_, err := svc.ReportReported(ctx, "dev-1", map[string]any{"fuzzy_kp": 0.8})
	if err != nil {
		t.Fatalf("ReportReported: %v", err)
	}

	delta, err := svc.UpdateDesired(ctx, "dev-1", map[string]any{"fuzzy_kp": 0.9})
	if err != nil {
		t.Fatalf("UpdateDesired: %v", err)
	}

	if v, ok := delta["fuzzy_kp"]; !ok || v != 0.9 {
		t.Fatalf("expected delta[fuzzy_kp]=0.9, got %v", delta["fuzzy_kp"])
	}

	calls := rep.ConfigCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 PublishConfigUpdate call, got %d", len(calls))
	}
	if calls[0].deviceID != "dev-1" {
		t.Fatalf("expected deviceID dev-1, got %s", calls[0].deviceID)
	}
	if v, ok := calls[0].delta["fuzzy_kp"]; !ok || v != 0.9 {
		t.Fatalf("published delta missing fuzzy_kp=0.9, got %v", calls[0].delta)
	}
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

func TestShadowSync_when_no_delta(t *testing.T) {
	svc, _, rep := newTestService()
	ctx := context.Background()

	_, err := svc.ReportReported(ctx, "dev-1", map[string]any{"fuzzy_kp": 0.8})
	if err != nil {
		t.Fatalf("ReportReported: %v", err)
	}
	_, err = svc.UpdateDesired(ctx, "dev-1", map[string]any{"fuzzy_kp": 0.8})
	if err != nil {
		t.Fatalf("UpdateDesired: %v", err)
	}

	// Reset mock calls
	rep.mu.Lock()
	rep.configCalls = nil
	rep.mu.Unlock()

	delta, err := svc.Sync(ctx, "dev-1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(delta) != 0 {
		t.Fatalf("expected empty delta, got %v", delta)
	}
	if len(rep.ConfigCalls()) != 0 {
		t.Fatal("expected no publish when delta is empty")
	}
}

// ---------------------------------------------------------------------------
// Protected params
// ---------------------------------------------------------------------------

func TestProtectedParam(t *testing.T) {
	store := newMockStore()
	rep := newMockReporter()
	svc := NewService(store, rep, WithProtectedParams([]string{"do_threshold", "estop"}))
	ctx := context.Background()

	_, err := svc.ReportReported(ctx, "dev-1", map[string]any{"do_threshold": 3.5})
	if err != nil {
		t.Fatalf("ReportReported: %v", err)
	}

	delta, err := svc.UpdateDesired(ctx, "dev-1", map[string]any{
		"do_threshold": 3.0, // should be dropped
		"fuzzy_kp":     0.9,
	})
	if err != nil {
		t.Fatalf("UpdateDesired: %v", err)
	}

	if _, ok := delta["do_threshold"]; ok {
		t.Fatal("do_threshold must NOT appear in delta (protected param)")
	}
	if v, ok := delta["fuzzy_kp"]; !ok || v != 0.9 {
		t.Fatalf("expected delta[fuzzy_kp]=0.9, got %v", delta["fuzzy_kp"])
	}

	calls := rep.ConfigCalls()
	if len(calls) == 0 {
		t.Fatal("expected at least one publish call")
	}
	if _, ok := calls[0].delta["do_threshold"]; ok {
		t.Fatal("do_threshold must NOT appear in published delta")
	}
}

// ---------------------------------------------------------------------------
// OTA — StageSlices
// ---------------------------------------------------------------------------

func TestOTAStageSlices_when_three_chunks(t *testing.T) {
	m := NewOTAManager(10, 100)
	// 25 bytes → 3 chunks: 10 + 10 + 5
	fw := []byte("abcdefghijklmnopqrstuvwxy")

	slices, err := m.StageSlices(fw)
	if err != nil {
		t.Fatalf("StageSlices: %v", err)
	}
	if len(slices) != 3 {
		t.Fatalf("expected 3 slices, got %d", len(slices))
	}
	if string(slices[0]) != "abcdefghij" {
		t.Fatalf("chunk 0 = %q", string(slices[0]))
	}
	if string(slices[1]) != "klmnopqrst" {
		t.Fatalf("chunk 1 = %q", string(slices[1]))
	}
	if string(slices[2]) != "uvwxy" {
		t.Fatalf("chunk 2 = %q", string(slices[2]))
	}
}

func TestOTAStageSlices_when_single_chunk(t *testing.T) {
	m := NewOTAManager(64, 1024)
	fw := []byte("hello")

	slices, err := m.StageSlices(fw)
	if err != nil {
		t.Fatalf("StageSlices: %v", err)
	}
	if len(slices) != 1 {
		t.Fatalf("expected 1 slice, got %d", len(slices))
	}
	if string(slices[0]) != "hello" {
		t.Fatalf("chunk 0 = %q", string(slices[0]))
	}
}

func TestOTAStageSlices_when_empty(t *testing.T) {
	m := NewOTAManager(10, 100)
	_, err := m.StageSlices([]byte{})
	if err == nil {
		t.Fatal("expected error for empty firmware")
	}
}

// ---------------------------------------------------------------------------
// OTA — BuildCommand & VerifyChecksum
// ---------------------------------------------------------------------------

func TestOTABuildCommand_and_VerifyChecksum(t *testing.T) {
	m := NewOTAManager(64*1024, 16*1024*1024)
	fw := []byte("firmware v2.0 binary data goes here")

	cmd, slices, err := m.BuildCommand("dev-1", "v2.0", "https://ota.example.com/fw.bin", fw)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if cmd.DeviceID != "dev-1" {
		t.Fatalf("cmd.DeviceID = %s, want dev-1", cmd.DeviceID)
	}
	if cmd.Version != "v2.0" {
		t.Fatalf("cmd.Version = %s, want v2.0", cmd.Version)
	}
	if cmd.SHA256 == "" {
		t.Fatal("expected non-empty SHA256")
	}
	if cmd.ChunkSize != 64*1024 {
		t.Fatalf("ChunkSize = %d", cmd.ChunkSize)
	}
	if cmd.Chunks != len(slices) || cmd.Chunks != 1 {
		t.Fatalf("Chunks = %d, want %d", cmd.Chunks, len(slices))
	}

	// VerifyChecksum true for correct data
	if !m.VerifyChecksum(fw, cmd.SHA256) {
		t.Fatal("VerifyChecksum should return true for correct data")
	}

	// VerifyChecksum false for corrupted data
	corrupted := append([]byte{}, fw...)
	corrupted[0] ^= 0xFF
	if m.VerifyChecksum(corrupted, cmd.SHA256) {
		t.Fatal("VerifyChecksum should return false for corrupted data")
	}
}

// ---------------------------------------------------------------------------
// OTA — SendOTA retry
// ---------------------------------------------------------------------------

func TestOTASendRetry(t *testing.T) {
	store := newMockStore()
	rep := newMockReporter()
	rep.modelErr = errors.New("mqtt: publish failed")
	rep.failCount = 3 // fail all 3 attempts

	svc := NewService(store, rep)
	ctx := context.Background()
	fw := []byte("ota firmware data")

	err := svc.SendOTA(ctx, "dev-1", "v2.0", "https://ota.example.com/fw.bin", fw)
	if err == nil {
		t.Fatal("expected error after 3 retries")
	}

	modelCalls := rep.ModelCalls()
	if len(modelCalls) != 3 {
		t.Fatalf("expected 3 publish attempts, got %d", len(modelCalls))
	}
}

func TestOTASendRetry_succeeds_on_second(t *testing.T) {
	store := newMockStore()
	rep := newMockReporter()
	rep.modelErr = errors.New("mqtt: publish failed")
	rep.failCount = 2 // fail first 2, succeed on 3rd

	svc := NewService(store, rep)
	ctx := context.Background()
	fw := []byte("ota firmware data")

	err := svc.SendOTA(ctx, "dev-1", "v2.0", "https://ota.example.com/fw.bin", fw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	modelCalls := rep.ModelCalls()
	if len(modelCalls) != 3 {
		t.Fatalf("expected 3 publish attempts, got %d", len(modelCalls))
	}
}

// ---------------------------------------------------------------------------
// OTA — Size limit
// ---------------------------------------------------------------------------

func TestOTASizeLimit(t *testing.T) {
	m := NewOTAManager(10, 5)
	fw := []byte("too long")

	_, err := m.StageSlices(fw)
	if err == nil {
		t.Fatal("expected error for firmware exceeding max size")
	}
}

// ---------------------------------------------------------------------------
// Table-driven delta edge cases
// ---------------------------------------------------------------------------

func TestComputeDelta_tableDriven(t *testing.T) {
	tests := []struct {
		name     string
		reported map[string]any
		desired  map[string]any
		want     map[string]any
	}{
		{
			name:     "both empty",
			reported: map[string]any{},
			desired:  map[string]any{},
			want:     map[string]any{},
		},
		{
			name:     "desired adds key",
			reported: map[string]any{},
			desired:  map[string]any{"a": 1},
			want:     map[string]any{"a": 1},
		},
		{
			name:     "boolean difference",
			reported: map[string]any{"enabled": true},
			desired:  map[string]any{"enabled": false},
			want:     map[string]any{"enabled": false},
		},
		{
			name:     "string difference",
			reported: map[string]any{"mode": "eco"},
			desired:  map[string]any{"mode": "normal"},
			want:     map[string]any{"mode": "normal"},
		},
		{
			name:     "multiple keys differ",
			reported: map[string]any{"a": 1, "b": 2, "c": 3},
			desired:  map[string]any{"a": 1, "b": 99, "c": 3},
			want:     map[string]any{"b": 99},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeDelta(tc.reported, tc.desired)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ComputeDelta() = %v, want %v", got, tc.want)
			}
		})
	}
}
