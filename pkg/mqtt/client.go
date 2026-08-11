// Package mqtt wraps paho.golang/autopaho to provide a MQTT v5 client
// tailored for y-ai-pond's telemetry and command flows.
//
// Design decisions (see .omo/notepads/y-ai-pond/decisions.md):
//   - High-frequency sensor telemetry is published as opaque binary payloads
//     (Protobuf) via QoS 0; commands are JSON via QoS 1. The protocol layer is
//     kept out of this package — callers pass already-encoded payloads.
//   - Message handlers never block the autopaho read loop: every inbound
//     message is dispatched to a handler goroutine.
//   - Reconnect uses exponential backoff 1s -> 30s; KeepAlive 20s;
//     SessionExpiry 3600s; OnConnectionUp re-subscribes registered topics.
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const (
	// DefaultKeepAlive matches config/config.yaml mqtt.keepalive (seconds).
	DefaultKeepAlive uint16 = 20
	// DefaultSessionExpiry matches config/config.yaml mqtt.session_expiry (seconds).
	DefaultSessionExpiry uint32 = 3600

	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
)

// MessageHandler receives a decoded inbound MQTT message.
// Handlers MUST NOT block for long; they run in a fresh goroutine per message.
type MessageHandler func(ctx context.Context, topic string, payload []byte)

// Config configures the MQTT client.
type Config struct {
	BrokerURL        string // e.g. tcp://localhost:1883
	ClientID         string
	KeepAlive        uint16 // seconds; 0 -> DefaultKeepAlive
	SessionExpiry    uint32 // seconds; 0 -> DefaultSessionExpiry
	Username         string
	Password         []byte
	OnConnectionLoss func() // optional; invoked when CONNACK is lost (may return to retry)
}

// Command is an outbound or processed inbound control message.
type Command struct {
	Topic   string
	Payload []byte
}

// Client is a managed autopaho ConnectionManager.
type Client struct {
	cfg        Config
	cm         *autopaho.ConnectionManager
	subsMu     sync.Mutex
	subs       []string
	handler    MessageHandler
	log        *slog.Logger
	connUp     chan struct{}
	connUpOnce sync.Once
	cancel     context.CancelFunc
	disconnect sync.Once
}

// PublishResponse mirrors autopaho PublishResponse reason code (QoS 1+ acks).
type PublishResponse struct {
	ReasonCode byte
}

// New builds a Client but does not connect. Call Connect to start.
func New(cfg Config, handler MessageHandler, log *slog.Logger) *Client {
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = DefaultKeepAlive
	}
	if cfg.SessionExpiry == 0 {
		cfg.SessionExpiry = DefaultSessionExpiry
	}
	if cfg.ClientID == "" {
		cfg.ClientID = fmt.Sprintf("y-ai-pond-%d", time.Now().UnixNano())
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		cfg:     cfg,
		handler: handler,
		log:     log,
		connUp:  make(chan struct{}),
	}
}

// Connect starts the connection loop. It returns after the first successful
// CONNACK (or when connectCtx is cancelled).
func (c *Client) Connect(connectCtx context.Context) error {
	brokerURL, err := url.Parse(c.cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("mqtt: invalid broker URL %q: %w", c.cfg.BrokerURL, err)
	}
	if brokerURL.Scheme != "tcp" && brokerURL.Scheme != "tls" {
		return fmt.Errorf("mqtt: unsupported scheme %q (want tcp or tls)", brokerURL.Scheme)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		KeepAlive:                     c.cfg.KeepAlive,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         c.cfg.SessionExpiry,
		ConnectRetryDelay:             minBackoff,
		ReconnectBackoff: func(attempt int) time.Duration {
			return exponentialBackoff(attempt)
		},
		ConnectTimeout:  10 * time.Second,
		ConnectUsername: c.cfg.Username,
		ConnectPassword: c.cfg.Password,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			c.log.Info("mqtt: connection up")
			c.resubscribe(cm)
			c.connUpOnce.Do(func() { close(c.connUp) })
		},
		OnConnectionDown: func() bool {
			c.log.Warn("mqtt: connection down, will retry")
			if c.cfg.OnConnectionLoss != nil {
				c.cfg.OnConnectionLoss()
			}
			return true // instruct autopaho to keep retrying
		},
		OnConnectError: func(err error) {
			c.log.Warn("mqtt: connect error", "error", err)
		},
		ClientConfig: paho.ClientConfig{
			ClientID: c.cfg.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					c.dispatch(pr)
					return true, nil
				},
			},
		},
	}

	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		cancel()
		return fmt.Errorf("mqtt: new connection: %w", err)
	}
	c.cm = cm

	if err := cm.AwaitConnection(connectCtx); err != nil {
		cancel()
		return fmt.Errorf("mqtt: connect: %w", err)
	}
	return nil
}

// Publish sends a raw payload to topic. Returns reason code of the ack.
func (c *Client) Publish(ctx context.Context, topic string, payload []byte, qos byte) (*PublishResponse, error) {
	if c.cm == nil {
		return nil, fmt.Errorf("mqtt: not connected")
	}
	pr, err := c.cm.Publish(ctx, &paho.Publish{
		Topic:   topic,
		Payload: payload,
		QoS:     qos,
	})
	if err != nil {
		return nil, fmt.Errorf("mqtt: publish %q: %w", topic, err)
	}
	return &PublishResponse{ReasonCode: pr.ReasonCode}, nil
}

// PublishTelemetry is a QoS 0 fire-and-forget publish for high-frequency
// sensor/camera data (binary Protobuf payloads as designed in proto/).
func (c *Client) PublishTelemetry(ctx context.Context, topic string, payload []byte) error {
	_, err := c.Publish(ctx, topic, payload, 0)
	return err
}

// PublishCommand is a QoS 1 publish for control/status message flows (JSON).
func (c *Client) PublishCommand(ctx context.Context, topic string, payload []byte) error {
	_, err := c.Publish(ctx, topic, payload, 1)
	return err
}

// Subscribe registers a topic filter. On (re)connection it is re-applied
// automatically by OnConnectionUp.
func (c *Client) Subscribe(ctx context.Context, topic string, qos byte) error {
	c.subsMu.Lock()
	c.subs = append(c.subs, topic)
	c.subsMu.Unlock()
	if c.cm == nil {
		return nil // will be applied on connect
	}
	_, err := c.cm.Subscribe(ctx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: qos}},
	})
	if err != nil {
		return fmt.Errorf("mqtt: subscribe %q: %w", topic, err)
	}
	return nil
}

// resubscribe re-applies all registered subscriptions after reconnect.
func (c *Client) resubscribe(cm *autopaho.ConnectionManager) {
	c.subsMu.Lock()
	topics := make([]string, len(c.subs))
	copy(topics, c.subs)
	c.subsMu.Unlock()
	for _, t := range topics {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, err := cm.Subscribe(ctx, &paho.Subscribe{
			Subscriptions: []paho.SubscribeOptions{{Topic: t, QoS: 1}},
		}); err != nil {
			c.log.Warn("mqtt: resubscribe failed", "topic", t, "error", err)
		}
		cancel()
	}
}

// dispatch hands each inbound message to a fresh goroutine so the autopaho
// read loop is never blocked.
func (c *Client) dispatch(pr paho.PublishReceived) {
	handler := c.handler
	if handler == nil || pr.Packet == nil {
		return
	}
	go handler(context.Background(), pr.Packet.Topic, pr.Packet.Payload)
}

// AwaitConnected blocks until the client has an active connection or ctx ends.
func (c *Client) AwaitConnected(ctx context.Context) error {
	select {
	case <-c.connUp:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Disconnect cleanly shuts down the client. Idempotent.
func (c *Client) Disconnect(ctx context.Context) error {
	var err error
	c.disconnect.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.cm != nil {
			err = c.cm.Disconnect(ctx)
		}
	})
	return err
}

// exponentialBackoff returns 1s*2^(attempt-1) capped at 30s.
func exponentialBackoff(attempt int) time.Duration {
	d := minBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}