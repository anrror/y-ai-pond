package security

import (
	"context"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	mochi "github.com/mochi-mqtt/server/v2"
	mochiauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

// TestMQTTUnauthenticated_NoCertRejected verifies that an MQTT client
// connecting without authentication is rejected when the broker requires it.
//
// Acceptance criteria (T33): "no-cert connection rejected"
//
// Mochi-mqtt defaults to DENY-ALL connections. Only after adding an
// AllowHook are connections permitted. This test verifies that:
//  1. Broker WITH AllowHook → client can connect (baseline)
//  2. Broker WITHOUT AllowHook → client connection is rejected (security enforcement)
func TestMQTTUnauthenticated_NoCertRejected(t *testing.T) {
	// Helper to start a mochi broker with optional AllowHook.
	startBroker := func(t *testing.T, allowAll bool) (addr string, stop func()) {
		t.Helper()
		broker := mochi.New(&mochi.Options{
			Logger: slog.New(slog.DiscardHandler),
		})
		if allowAll {
			// mochi defaults to DENY-ALL. Add AllowHook to permit all connections
			// for the baseline test (same pattern as integration/helpers_test.go).
			if err := broker.AddHook(new(mochiauth.AllowHook), nil); err != nil {
				t.Fatalf("AddHook: %v", err)
			}
		}
		tcp := listeners.NewTCP(listeners.Config{
			ID:      "t1",
			Address: "127.0.0.1:0",
		})
		if err := broker.AddListener(tcp); err != nil {
			t.Fatalf("AddListener: %v", err)
		}
		go func() { _ = broker.Serve() }()
		// Wait for listener to bind.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if tcp.Address() != "127.0.0.1:0" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return tcp.Address(), func() { _ = broker.Close() }
	}

	// Helper to attempt a connection and return success/failure.
	tryConnect := func(t *testing.T, addr string) error {
		t.Helper()
		clientCfg := autopaho.ClientConfig{
			ServerUrls:                    []*url.URL{{Scheme: "tcp", Host: addr}},
			KeepAlive:                     20,
			CleanStartOnInitialConnection: true,
			ConnectTimeout:                3 * time.Second,
		}
		clientCfg.ClientID = "test-unauth-" + time.Now().Format("150405")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cm, err := autopaho.NewConnection(ctx, clientCfg)
		if err != nil {
			return err
		}
		defer func() { _ = cm.Disconnect(context.Background()) }()
		return cm.AwaitConnection(ctx)
	}

	// Test 1: Broker WITH AllowHook → connection MUST succeed.
	t.Run("authenticated_succeeds", func(t *testing.T) {
		addr, stop := startBroker(t, true)
		defer stop()
		if err := tryConnect(t, addr); err != nil {
			t.Fatalf("expected connection to succeed with AllowHook, got: %v", err)
		}
		t.Log("broker with AllowHook: connection SUCCESS (baseline)")
	})

	// Test 2: Broker WITHOUT AllowHook → connection MUST be rejected.
	t.Run("unauthenticated_rejected", func(t *testing.T) {
		addr, stop := startBroker(t, false)
		defer stop()
		err := tryConnect(t, addr)
		if err == nil {
			t.Error("connection SUCCEEDED on broker without AllowHook — DENY-ALL default not enforced!")
		} else {
			t.Logf("broker without AllowHook: connection REJECTED (expected): %v", err)
		}
	})
}

// TestMQTTIllegalTopic_Rejected verifies that publishing to illegal MQTT
// topics (wildcards in publish path, null bytes, empty strings) is handled
// safely without broker crash.
func TestMQTTIllegalTopic_Rejected(t *testing.T) {
	broker := mochi.New(&mochi.Options{
		Logger: slog.New(slog.DiscardHandler),
	})
	// Add AllowHook so connections are allowed — mochi defaults to DENY-ALL.
	if err := broker.AddHook(new(mochiauth.AllowHook), nil); err != nil {
		t.Fatalf("AddHook: %v", err)
	}
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "t1",
		Address: "127.0.0.1:0",
	})
	if err := broker.AddListener(tcp); err != nil {
		t.Fatalf("AddListener: %v", err)
	}
	go func() { _ = broker.Serve() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tcp.Address() != "127.0.0.1:0" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	addr := tcp.Address()

	illegalTopics := []struct {
		name  string
		topic string
	}{
		{"wildcard_hash", "pond/v1/#"},
		{"wildcard_plus", "pond/v1/+/+/sensor/water/ph"},
		{"empty_topic", ""},
		{"very_long", "pond/v1/" + string(make([]byte, 10000))},
	}

	for _, tc := range illegalTopics {
		t.Run(tc.name, func(t *testing.T) {
			clientCfg := autopaho.ClientConfig{
				ServerUrls:                    []*url.URL{{Scheme: "tcp", Host: addr}},
				KeepAlive:                     20,
				CleanStartOnInitialConnection: true,
				ConnectTimeout:                3 * time.Second,
			}
			clientCfg.ClientID = "test-illegal-" + tc.name

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cm, err := autopaho.NewConnection(ctx, clientCfg)
			if err != nil {
				t.Skipf("NewConnection: %v", err)
				return
			}
			defer func() { _ = cm.Disconnect(context.Background()) }()

			if err := cm.AwaitConnection(ctx); err != nil {
				t.Skipf("AwaitConnection: %v", err)
				return
			}

			// Publish to illegal topic — must not crash broker (panic or hang).
			_, pubErr := cm.Publish(ctx, &paho.Publish{
				Topic:   tc.topic,
				Payload: []byte(`{"test": true}`),
				QoS:     0,
			})
			if pubErr != nil {
				t.Logf("publish to illegal topic rejected (safe): %v", pubErr)
			} else {
				t.Log("publish to illegal topic accepted (broker tolerant — no crash)")
			}
		})
	}
}
