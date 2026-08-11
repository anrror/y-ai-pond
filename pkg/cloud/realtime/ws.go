package realtime

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSUpgrader is the gorilla/websocket upgrader with permissive CheckOrigin
// for dev/test. In production, restrict CheckOrigin to the known frontend origin.
var WSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// WSPingPeriod is the interval for WebSocket ping control frames.
const WSPingPeriod = 15 * time.Second

// WSWriteWait is how long the write goroutine waits for a write before giving up.
const WSWriteWait = 10 * time.Second

// WSReadLimit is the maximum message size accepted from the client (1 MiB).
const WSReadLimit = 1 << 20

// WSCommand represents a control command sent by the dashboard client.
type WSCommand struct {
	Action string          `json:"action"`
	FarmID string          `json:"farm_id"`
	PondID string          `json:"pond_id"`
	Params json.RawMessage `json:"params,omitempty"`
}

// CommandHandler is called when the WebSocket client sends a control command.
// Implementations should publish the command to MQTT or another channel.
type CommandHandler func(cmd WSCommand) error

// WSServe upgrades an HTTP connection to WebSocket, subscribes the client to
// dashboard rooms for the given farms, and runs the read/write pumps. Returns
// when the connection closes.
//
// Parameters:
//   - w, r: the HTTP response/request from the Gin context
//   - farms: farm IDs the client is authorized for (one WS room per farm)
//   - hub: the realtime Hub for pub/sub
//   - subID: unique subscriber identifier
//   - onCommand: called when the client sends a control command (may be nil)
//   - log: structured logger
func WSServe(
	w http.ResponseWriter, r *http.Request,
	farms []string,
	hub *Hub,
	subID string,
	onCommand CommandHandler,
	log *slog.Logger,
) error {
	if log == nil {
		log = slog.Default()
	}

	conn, err := WSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	rooms := make([]string, len(farms))
	for i, f := range farms {
		rooms[i] = DashboardRoom(f)
	}

	sub, unsubscribe := hub.Subscribe(subID, rooms...)
	defer unsubscribe()

	// Write pump goroutine — single writer to satisfy gorilla/websocket's
	// concurrent-write constraint.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer func() { _ = conn.Close() }()

		ticker := time.NewTicker(WSPingPeriod)
		defer ticker.Stop()

		for {
			select {
			case ev, ok := <-sub.Events:
				_ = conn.SetWriteDeadline(time.Now().Add(WSWriteWait))
				if !ok {
					// Channel closed — send close frame and exit.
					_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
				if err := conn.WriteJSON(ev); err != nil {
					log.Debug("ws: write error", "sub_id", subID, "error", err)
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(WSWriteWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Debug("ws: ping error", "sub_id", subID, "error", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read pump — reads commands from the client. gorilla/websocket
	// handles pong replies internally (no manual pong handler needed).
	conn.SetReadLimit(WSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Debug("ws: read error", "sub_id", subID, "error", err)
			}
			break
		}

		var cmd WSCommand
		if err := json.Unmarshal(msg, &cmd); err != nil {
			log.Debug("ws: invalid command JSON", "sub_id", subID, "raw", string(msg))
			continue
		}

		if onCommand != nil {
			if err := onCommand(cmd); err != nil {
				log.Warn("ws: command handler error", "sub_id", subID, "action", cmd.Action, "error", err)
			}
		}
	}

	// Signal write pump to exit and wait for it.
	close(done)
	wg.Wait()

	return nil
}
