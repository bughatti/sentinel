package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sentinel-nvr/sentinel/internal/events"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins. In production scope this to your domain.
		return true
	},
}

// wsClient represents one connected WebSocket consumer.
type wsClient struct {
	conn     *websocket.Conn
	send     chan []byte
	labels   map[string]bool // empty = subscribe to all
	cameras  map[string]bool // empty = subscribe to all
}

// webSocketHub manages all connected WebSocket clients and fans out events.
type webSocketHub struct {
	bus      *events.EventBus
	clients  map[*wsClient]struct{}
	mu       sync.RWMutex
	register chan *wsClient
	remove   chan *wsClient
}

func newWebSocketHub(bus *events.EventBus) *webSocketHub {
	return &webSocketHub{
		bus:      bus,
		clients:  make(map[*wsClient]struct{}),
		register: make(chan *wsClient, 16),
		remove:   make(chan *wsClient, 16),
	}
}

// run is the hub event loop. It must be started in a goroutine.
func (h *webSocketHub) run(ctx context.Context) {
	eventCh := make(chan events.Event, 256)
	h.bus.Subscribe(eventCh)
	defer h.bus.Unsubscribe(eventCh)

	for {
		select {
		case <-ctx.Done():
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()

		case client := <-h.remove:
			h.mu.Lock()
			delete(h.clients, client)
			h.mu.Unlock()
			close(client.send)

		case e, ok := <-eventCh:
			if !ok {
				return
			}
			// Wrap as {type, payload} — the UI reads msg.type (nav badge) and
			// msg.payload (Events/Live live updates). Sending the flat event
			// broke the payload readers, so real-time list/state updates were dead.
			data, err := json.Marshal(struct {
				Type    events.EventType `json:"type"`
				Payload events.Event     `json:"payload"`
			}{Type: e.Type, Payload: e})
			if err != nil {
				slog.Error("ws: marshal event", "err", err)
				continue
			}
			h.mu.RLock()
			for client := range h.clients {
				// Apply per-client filters.
				if len(client.cameras) > 0 && !client.cameras[e.Camera] {
					continue
				}
				if len(client.labels) > 0 && !client.labels[e.Label] {
					continue
				}
				select {
				case client.send <- data:
				default:
					// Slow client — drop message.
				}
			}
			h.mu.RUnlock()
		}
	}
}

// handleWS upgrades the HTTP connection to WebSocket and starts read/write pumps.
func (h *webSocketHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("ws: upgrade failed", "err", err)
		return
	}

	client := &wsClient{
		conn:    conn,
		send:    make(chan []byte, 256),
		labels:  make(map[string]bool),
		cameras: make(map[string]bool),
	}

	h.register <- client
	go h.writePump(client)
	h.readPump(client) // blocks until client disconnects
}

// readPump reads control messages from the client (subscribe filters).
func (h *webSocketHub) readPump(c *wsClient) {
	defer func() {
		h.remove <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	c.conn.SetReadLimit(4096)

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure) {
				slog.Debug("ws: read error", "err", err)
			}
			return
		}

		// Parse subscribe message: {"subscribe": {"cameras": ["front"], "labels": ["person"]}}
		var sub struct {
			Subscribe struct {
				Cameras []string `json:"cameras"`
				Labels  []string `json:"labels"`
			} `json:"subscribe"`
		}
		if err := json.Unmarshal(msg, &sub); err == nil {
			c.cameras = make(map[string]bool)
			for _, cam := range sub.Subscribe.Cameras {
				c.cameras[cam] = true
			}
			c.labels = make(map[string]bool)
			for _, l := range sub.Subscribe.Labels {
				c.labels[l] = true
			}
		}
	}
}

// writePump pumps outbound messages from the send channel to the WebSocket.
func (h *webSocketHub) writePump(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
