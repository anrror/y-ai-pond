package benchmark

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mqttclient "github.com/anrror/y-ai-pond/pkg/mqtt"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

// startBenchBroker spins up an in-memory mochi MQTT broker on 127.0.0.1:0
// and returns its TCP address and a stop function.
func startBenchBroker(b *testing.B) (addr string, stop func()) {
	b.Helper()
	s := mochi.New(&mochi.Options{})
	if err := s.AddHook(new(auth.AllowHook), nil); err != nil {
		b.Fatalf("add allow hook: %v", err)
	}
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "bench",
		Address: "127.0.0.1:0",
	})
	if err := s.AddListener(tcp); err != nil {
		b.Fatalf("add listener: %v", err)
	}
	go func() { _ = s.Serve() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tcp.Address() != "127.0.0.1:0" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var once sync.Once
	return tcp.Address(), func() {
		once.Do(func() { _ = s.Close() })
	}
}

// newBenchMQTTClient creates a connected MQTT client against the broker.
func newBenchMQTTClient(b *testing.B, addr, clientID string, handler mqttclient.MessageHandler) *mqttclient.Client {
	b.Helper()
	c := mqttclient.New(mqttclient.Config{
		BrokerURL: "tcp://" + addr,
		ClientID:  clientID,
	}, handler, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		b.Fatalf("connect mqtt client %q: %v", clientID, err)
	}
	return c
}

// BenchmarkMQTTThroughput measures end-to-end MQTT publish→subscribe latency
// under concurrent load. Target: p95 < 100ms.
//
//	100 goroutines × 10 messages each = 1000 messages per iteration.
//	Uses mochi-mqtt in-memory TCP broker (real TCP, same as integration tests).
//	Clients are pooled and reused across iterations (connection overhead excluded).
func BenchmarkMQTTThroughput(b *testing.B) {
	const (
		numDevices    = 100
		msgsPerDev    = 10
		totalMsgs     = numDevices * msgsPerDev
		mqttTopic     = "pond/v1/farm-1/pond-1/sensor/water"
		maxPublishers = 20 // reusable pool
	)

	addr, stop := startBenchBroker(b)
	defer stop()

	// Persistent subscriber.
	var seq atomic.Int64
	sentTimes := make(map[int64]time.Time, totalMsgs)
	var sentMu sync.Mutex

	type latEntry struct {
		d time.Duration
	}
	latCh := make(chan latEntry, totalMsgs*2)

	handler := func(_ context.Context, _ string, payload []byte) {
		if len(payload) < 8 {
			return
		}
		seqNum := int64(payload[0])<<56 | int64(payload[1])<<48 |
			int64(payload[2])<<40 | int64(payload[3])<<32 |
			int64(payload[4])<<24 | int64(payload[5])<<16 |
			int64(payload[6])<<8 | int64(payload[7])

		sentMu.Lock()
		sent, ok := sentTimes[seqNum]
		if ok {
			delete(sentTimes, seqNum)
		}
		sentMu.Unlock()

		if ok {
			latCh <- latEntry{d: time.Since(sent)}
		}
	}

	subClient := newBenchMQTTClient(b, addr, "bench-sub", handler)
	if err := subClient.Subscribe(context.Background(), mqttTopic, 1); err != nil {
		b.Fatalf("subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Pre-connect a pool of publisher clients.
	publishers := make([]*mqttclient.Client, maxPublishers)
	for p := 0; p < maxPublishers; p++ {
		publishers[p] = newBenchMQTTClient(b, addr, fmt.Sprintf("bench-pub-%02d", p), nil)
	}
	defer func() {
		for _, pub := range publishers {
			_ = pub.Disconnect(context.Background())
		}
		_ = subClient.Disconnect(context.Background())
	}()

	b.ResetTimer()

	for iter := 0; iter < b.N; iter++ {
		seq.Store(0)
		// Drain stale latCh entries.
		for len(latCh) > 0 {
			<-latCh
		}

		latencies := make([]time.Duration, 0, totalMsgs)

		// Spawn device goroutines reusing pooled publishers.
		var wg sync.WaitGroup
		for dev := 0; dev < numDevices; dev++ {
			wg.Add(1)
			go func(deviceID int) {
				defer wg.Done()
				ctx := context.Background()
				pub := publishers[deviceID%maxPublishers]

				for m := 0; m < msgsPerDev; m++ {
					s := seq.Add(1)
					p := make([]byte, 128)
					p[0] = byte(s >> 56)
					p[1] = byte(s >> 48)
					p[2] = byte(s >> 40)
					p[3] = byte(s >> 32)
					p[4] = byte(s >> 24)
					p[5] = byte(s >> 16)
					p[6] = byte(s >> 8)
					p[7] = byte(s)

					sentMu.Lock()
					sentTimes[s] = time.Now()
					sentMu.Unlock()

					if err := pub.PublishTelemetry(ctx, mqttTopic, p); err != nil {
						b.Errorf("publish dev=%d msg=%d: %v", deviceID, m, err)
					}
				}
			}(dev)
		}

		// Collect all latencies.
		for collected := 0; collected < totalMsgs; collected++ {
			e := <-latCh
			latencies = append(latencies, e.d)
		}

		wg.Wait()

		reportLatencyPercentiles(b, latencies)
	}
}
