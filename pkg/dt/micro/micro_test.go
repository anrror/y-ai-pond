package micro

import (
	"errors"
	"sync"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/dt/scenario"
)

// ============================================================================
// TestMicroDTSync: edge_state → cloud_state sync
// ============================================================================

func TestMicroDTSync(t *testing.T) {
	dt := New(Config{
		Scenario:  DefaultScenario(),
		MaxBuffer: 20,
	})

	const steps = 10

	// Simulate steps and verify each returns a valid state.
	for i := 0; i < steps; i++ {
		st, err := dt.Step()
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if st.TemperatureC <= 0 || st.DO <= 0 {
			t.Fatalf("step %d: implausible state: temp=%.2f DO=%.2f", i, st.TemperatureC, st.DO)
		}
	}

	if n := dt.BufferLen(); n != steps {
		t.Fatalf("buffer len: want %d, got %d", steps, n)
	}

	// Sync: cloud receives all 10 states in order.
	uploaded := dt.Sync()
	if len(uploaded) != steps {
		t.Fatalf("sync count: want %d, got %d", steps, len(uploaded))
	}

	for i, bs := range uploaded {
		if bs.Seq != int64(i+1) {
			t.Errorf("uploaded[%d].seq: want %d, got %d", i, i+1, bs.Seq)
		}
	}

	// After Sync the buffer must be empty.
	if n := dt.BufferLen(); n != 0 {
		t.Fatalf("after sync, buffer len: want 0, got %d", n)
	}

	// State() should still return the last state.
	final := dt.State()
	if final.TemperatureC <= 0 {
		t.Fatalf("State() after sync: implausible temp=%.2f", final.TemperatureC)
	}
}

// ============================================================================
// TestMicroDTOffline: disconnect → local sim continues → reconnect → backfill
// ============================================================================

func TestMicroDTOffline(t *testing.T) {
	dt := New(Config{
		Scenario:  DefaultScenario(),
		MaxBuffer: 100,
	})

	// Start connected, run a few steps.
	dt.SetConnected(true)
	for i := 0; i < 3; i++ {
		if _, err := dt.Step(); err != nil {
			t.Fatalf("connected step %d: %v", i, err)
		}
	}
	if !dt.IsConnected() {
		t.Fatal("expected connected after SetConnected(true)")
	}

	// Flush the connected-phase buffer (i.e. synced to cloud).
	_ = dt.Sync()
	if n := dt.BufferLen(); n != 0 {
		t.Fatalf("buffer not empty after sync: %d", n)
	}

	// Disconnect: local simulation continues independently.
	dt.SetConnected(false)
	if dt.IsConnected() {
		t.Fatal("expected disconnected after SetConnected(false)")
	}

	const offlineSteps = 15
	for i := 0; i < offlineSteps; i++ {
		if _, err := dt.Step(); err != nil {
			t.Fatalf("offline step %d: %v", i, err)
		}
	}

	if n := dt.BufferLen(); n != offlineSteps {
		t.Fatalf("offline buffer len: want %d, got %d", offlineSteps, n)
	}

	// Reconnect: Sync backfills all offline states.
	dt.SetConnected(true)
	if !dt.IsConnected() {
		t.Fatal("expected connected after reconnect")
	}

	backfill := dt.Sync()
	if len(backfill) != offlineSteps {
		t.Fatalf("backfill count: want %d, got %d", offlineSteps, len(backfill))
	}

	// Verify sequence numbers are contiguous starting from where we left off.
	for i, bs := range backfill {
		wantSeq := int64(4 + i) // 3 connected steps + 1 = start of offline seq
		if bs.Seq != wantSeq {
			t.Errorf("backfill[%d].seq: want %d, got %d", i, wantSeq, bs.Seq)
		}
	}

	// Buffer must be empty after backfill.
	if n := dt.BufferLen(); n != 0 {
		t.Fatalf("buffer not empty after backfill: %d", n)
	}
}

// ============================================================================
// TestMicroDTOffline_EmptySync: Sync on empty buffer returns nil, not panic.
// ============================================================================

func TestMicroDTOffline_EmptySync(t *testing.T) {
	dt := New(Config{MaxBuffer: 10})
	backfill := dt.Sync()
	if backfill != nil {
		t.Fatalf("empty Sync: want nil, got %d entries", len(backfill))
	}
}

// ============================================================================
// TestMicroDTOffline_LRUEviction: overflow → oldest evicted, ErrBufferOverflow returned.
// ============================================================================

func TestMicroDTOffline_LRUEviction(t *testing.T) {
	const maxBuf = 10
	dt := New(Config{
		Scenario:  DefaultScenario(),
		MaxBuffer: maxBuf,
	})

	// Fill buffer to capacity.
	for i := 0; i < maxBuf; i++ {
		_, err := dt.Step()
		if err != nil {
			t.Fatalf("step %d (pre-overflow): unexpected error: %v", i, err)
		}
	}

	// One more step: overflow triggers LRU eviction.
	st, err := dt.Step()
	if !errors.Is(err, ErrBufferOverflow) {
		t.Fatalf("overflow step: want ErrBufferOverflow, got %v", err)
	}
	if st.TemperatureC <= 0 {
		t.Fatalf("overflow step returned invalid state")
	}

	if n := dt.BufferLen(); n != maxBuf {
		t.Fatalf("after eviction, buffer len: want %d, got %d", maxBuf, n)
	}

	// The oldest (seq=1) must be gone; the newest (seq=11) must be present.
	uploaded := dt.Sync()
	if len(uploaded) != maxBuf {
		t.Fatalf("post-eviction sync count: want %d, got %d", maxBuf, len(uploaded))
	}

	firstSeq := uploaded[0].Seq
	if firstSeq != 2 {
		t.Errorf("first retained seq: want 2 (seq 1 evicted), got %d", firstSeq)
	}
	lastSeq := uploaded[len(uploaded)-1].Seq
	if lastSeq != int64(maxBuf+1) {
		t.Errorf("last retained seq: want %d, got %d", maxBuf+1, lastSeq)
	}
}

// ============================================================================
// TestMicroDTOffline_MultipleSync: repeated Sync calls behave as expected.
// ============================================================================

func TestMicroDTOffline_MultipleSync(t *testing.T) {
	dt := New(Config{MaxBuffer: 50})

	// Batch 1
	for i := 0; i < 3; i++ {
		dt.Step() //nolint:errcheck
	}
	b1 := dt.Sync()
	if len(b1) != 3 {
		t.Fatalf("batch 1: want 3, got %d", len(b1))
	}

	// Batch 2
	for i := 0; i < 5; i++ {
		dt.Step() //nolint:errcheck
	}
	b2 := dt.Sync()
	if len(b2) != 5 {
		t.Fatalf("batch 2: want 5, got %d", len(b2))
	}

	// Verify the two batches have non-overlapping sequence numbers.
	for _, b := range b1 {
		for _, c := range b2 {
			if b.Seq == c.Seq {
				t.Fatalf("seq collision: batch1 has seq %d, batch2 also has seq %d", b.Seq, c.Seq)
			}
		}
	}
}

// ============================================================================
// TestMicroDTOffline_ConcurrentSafety: Step and Sync from multiple goroutines.
// ============================================================================

func TestMicroDTOffline_ConcurrentSafety(t *testing.T) {
	dt := New(Config{
		Scenario:  DefaultScenario(),
		MaxBuffer: 1000,
	})

	var wg sync.WaitGroup
	const goroutines = 8
	const stepsPerG = 50

	// Parallel Step calls.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < stepsPerG; i++ {
				dt.Step() //nolint:errcheck
			}
		}()
	}
	wg.Wait()

	totalSteps := goroutines * stepsPerG
	if n := dt.BufferLen(); n != totalSteps {
		t.Fatalf("after concurrent steps, buffer len: want %d, got %d", totalSteps, n)
	}

	// Sync from one goroutine; verify ordered.
	uploaded := dt.Sync()
	if len(uploaded) != totalSteps {
		t.Fatalf("concurrent sync count: want %d, got %d", totalSteps, len(uploaded))
	}

	// Verify seq monotonicity (concurrent calls may interleave seq, but they
	// must be strictly increasing and cover exactly 1..totalSteps).
	seen := make(map[int64]bool, totalSteps)
	var prevSeq int64
	for i, bs := range uploaded {
		if i > 0 && bs.Seq <= prevSeq {
			t.Errorf("seq not monotonic at index %d: prev=%d cur=%d", i, prevSeq, bs.Seq)
		}
		if seen[bs.Seq] {
			t.Errorf("duplicate seq %d at index %d", bs.Seq, i)
		}
		seen[bs.Seq] = true
		prevSeq = bs.Seq
	}

	if int64(len(seen)) != int64(totalSteps) {
		t.Fatalf("expected %d unique seqs, got %d", totalSteps, len(seen))
	}

	// Verify all sequences 1..totalSteps exist.
	for s := int64(1); s <= int64(totalSteps); s++ {
		if !seen[s] {
			t.Errorf("missing seq %d", s)
		}
	}
}

// ============================================================================
// TestMicroDTOffline_StatePreserved: State() returns the most recent state
// across connected/disconnected transitions.
// ============================================================================

func TestMicroDTOffline_StatePreserved(t *testing.T) {
	dt := New(Config{
		Scenario:  DefaultScenario(),
		MaxBuffer: 100,
	})

	// Run 5 steps, capture state.
	for i := 0; i < 5; i++ {
		dt.Step() //nolint:errcheck
	}
	s1 := dt.State()
	dt.Sync()

	// Disconnect, run 3 more steps.
	dt.SetConnected(false)
	for i := 0; i < 3; i++ {
		dt.Step() //nolint:errcheck
	}
	s2 := dt.State()

	// Reconnect, sync, state is still the last one.
	dt.SetConnected(true)
	dt.Sync()
	s3 := dt.State()

	// After sync the state should still be the same as the last offline step.
	if s3.TemperatureC != s2.TemperatureC || s3.DO != s2.DO {
		t.Fatalf("State changed after sync: before=%+v after=%+v", s2, s3)
	}

	// State should have advanced from s1 to s2 (temp relaxation, DO, etc.).
	if s1.TemperatureC == s2.TemperatureC && s1.DO == s2.DO {
		t.Log("warning: state did not change over 3 steps (may happen if scenario delta is zero)")
	}
}

// ============================================================================
// TestMicroDTOffline_DefaultScenario: Config with zero Scenario uses DefaultScenario.
// ============================================================================

func TestMicroDTOffline_DefaultScenario(t *testing.T) {
	dt := New(Config{
		MaxBuffer: 10,
	})

	for i := 0; i < 5; i++ {
		st, err := dt.Step()
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		// Baseline scenario has TempDeltaC=0, so temperature stays near 25°C.
		if st.TemperatureC < 20 || st.TemperatureC > 30 {
			t.Errorf("step %d: unexpected temp %.2f", i, st.TemperatureC)
		}
	}
}

// ============================================================================
// TestMicroDTOffline_HeatWaveScenario: micro DT works with extreme weather scenario.
// ============================================================================

func TestMicroDTOffline_HeatWaveScenario(t *testing.T) {
	dt := New(Config{
		Scenario:  scenario.HeatWaveScenario(),
		MaxBuffer: 500,
	})

	// Heat wave pushes temperature up over many steps.
	for i := 0; i < 200; i++ {
		dt.Step() //nolint:errcheck
	}

	st := dt.State()
	if st.TemperatureC < 28.0 {
		t.Errorf("heat wave temp too low: %.2f (expected > 28 after 200 steps)", st.TemperatureC)
	}
	// DO saturation drops at high temperature; verify it dropped from baseline 7.0.
	if st.DO > 5.0 {
		t.Errorf("heat wave DO not depressed: %.2f (expected < 5 due to temp-driven saturation drop)", st.DO)
	}
	if st.DO <= 0 {
		t.Errorf("heat wave DO implausibly zero: %.2f", st.DO)
	}
}

// ============================================================================
// TestMicroDTOffline_StormFloodScenario: micro DT works with storm flood.
// ============================================================================

func TestMicroDTOffline_StormFloodScenario(t *testing.T) {
	dt := New(Config{
		Scenario:  scenario.StormFloodScenario(),
		MaxBuffer: 50,
	})

	// Storm flood depresses DO and raises turbidity.
	for i := 0; i < 24; i++ {
		dt.Step() //nolint:errcheck
	}

	st := dt.State()
	if st.Turbidity <= 12.0 {
		t.Errorf("flood turbidity not elevated: %.2f (expected > 12)", st.Turbidity)
	}
	if st.DO >= 7.0 {
		t.Errorf("flood DO not depressed: %.2f (expected < 7)", st.DO)
	}
}

// ============================================================================
// TestMicroDTOffline_ColdSnapScenario: micro DT works with cold snap.
// ============================================================================

func TestMicroDTOffline_ColdSnapScenario(t *testing.T) {
	dt := New(Config{
		Scenario:  scenario.ColdSnapScenario(),
		MaxBuffer: 100,
	})

	for i := 0; i < 48; i++ {
		dt.Step() //nolint:errcheck
	}

	st := dt.State()
	if st.TemperatureC > 20.0 {
		t.Errorf("cold snap temp not depressed: %.2f (expected < 20)", st.TemperatureC)
	}
}
