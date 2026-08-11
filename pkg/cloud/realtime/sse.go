package realtime

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// SSEConfig holds SSE stream tuning parameters.
type SSEConfig struct {
	// HeartbeatInterval is how often a comment ping is sent (default 15s).
	HeartbeatInterval time.Duration
	// BufferSize overrides the default subscriber channel buffer.
	BufferSize int
}

// DefaultSSEConfig returns the recommended SSE streaming config.
func DefaultSSEConfig() SSEConfig {
	return SSEConfig{
		HeartbeatInterval: 15 * time.Second,
		BufferSize:        256,
	}
}

// WriteSSE writes an event in SSE format to the given writer.
// Format: "data: {json}\n\n". Returns any write error.
func WriteSSE(w io.Writer, ev any) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("sse: marshal: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("sse: write: %w", err)
	}
	return nil
}

// WriteSSEComment writes an SSE comment line (": ping\n\n") used as a
// keepalive heartbeat. Browsers ignore comments.
func WriteSSEComment(w io.Writer, msg string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", msg); err != nil {
		return fmt.Errorf("sse: write comment: %w", err)
	}
	return nil
}

// SSEWriter is a streaming helper that reads from a subscriber channel and
// writes SSE-formatted events to an io.Writer. It sends heartbeat comments
// at the configured interval to keep the connection alive.
type SSEWriter struct {
	sub     *Subscriber
	cfg     SSEConfig
	out     io.Writer
	log     *slog.Logger
	flusher func()
}

// NewSSEWriter creates an SSE streaming writer. The out writer is the Gin
// ResponseWriter; flusher should call c.Writer.Flush().
func NewSSEWriter(sub *Subscriber, out io.Writer, flusher func(), log *slog.Logger, cfg SSEConfig) *SSEWriter {
	if log == nil {
		log = slog.Default()
	}
	return &SSEWriter{
		sub:     sub,
		cfg:     cfg,
		out:     out,
		log:     log,
		flusher: flusher,
	}
}

// Run blocks reading from the subscriber channel and writing SSE events.
// It returns when the subscriber channel is closed or the write fails
// (client disconnected). Heartbeat comments are sent periodically.
func (s *SSEWriter) Run() error {
	ticker := time.NewTicker(s.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-s.sub.Events:
			if !ok {
				return nil // channel closed (clean shutdown)
			}
			if err := WriteSSE(s.out, ev); err != nil {
				s.log.Debug("sse: write error (client disconnected?)", "sub_id", s.sub.ID, "error", err)
				return err
			}
			s.flusher()

		case <-ticker.C:
			if err := WriteSSEComment(s.out, "ping"); err != nil {
				s.log.Debug("sse: heartbeat write error", "sub_id", s.sub.ID, "error", err)
				return err
			}
			s.flusher()
		}
	}
}
