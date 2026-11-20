package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var errAuthInvalid = errors.New("access token rejected")

type State struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

type Client struct {
	base  string
	wsURL []string
	token string
	log   *slog.Logger
	httpc *http.Client

	mu        sync.RWMutex
	states    map[string]State
	connected bool

	subMu sync.Mutex
	subs  map[chan State]struct{}

	themeMu sync.RWMutex
	onTheme func(string)

	wmu sync.Mutex
}

func New(base, token string, log *slog.Logger) *Client {
	base = strings.TrimRight(base, "/")
	return &Client{
		base:   base,
		wsURL:  wsCandidates(base),
		token:  token,
		log:    log,
		httpc:  &http.Client{Timeout: 15 * time.Second},
		states: make(map[string]State),
		subs:   make(map[chan State]struct{}),
	}
}

// wsCandidates returns possible WebSocket endpoints. The Supervisor proxies
// Home Assistant Core at http://supervisor/core and exposes the WebSocket API
// at /core/websocket, while a direct Core connection uses /api/websocket.
func wsCandidates(base string) []string {
	var paths []string
	if strings.HasSuffix(base, "/core") {
		paths = []string{"/websocket", "/api/websocket"}
	} else {
		paths = []string{"/api/websocket", "/websocket"}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		u := base + p
		switch {
		case strings.HasPrefix(u, "https://"):
			u = "wss://" + strings.TrimPrefix(u, "https://")
		case strings.HasPrefix(u, "http://"):
			u = "ws://" + strings.TrimPrefix(u, "http://")
		}
		out = append(out, u)
	}
	return out
}

func (c *Client) State(entityID string) (State, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st, ok := c.states[entityID]
	return st, ok
}

func (c *Client) States() map[string]State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]State, len(c.states))
	for k, v := range c.states {
		out[k] = v
	}
	return out
}

func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func (c *Client) SetThemeHandler(fn func(string)) {
	c.themeMu.Lock()
	c.onTheme = fn
	c.themeMu.Unlock()
}

func (c *Client) Subscribe() chan State {
	ch := make(chan State, 64)
	c.subMu.Lock()
	c.subs[ch] = struct{}{}
	c.subMu.Unlock()
	return ch
}

func (c *Client) Unsubscribe(ch chan State) {
	c.subMu.Lock()
	if _, ok := c.subs[ch]; ok {
		delete(c.subs, ch)
		close(ch)
	}
	c.subMu.Unlock()
}

func (c *Client) broadcast(st State) {
	c.subMu.Lock()
	for ch := range c.subs {
		select {
		case ch <- st:
		default:
		}
	}
	c.subMu.Unlock()
}

func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.session(ctx)
		c.setConnected(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		c.log.Warn("ha websocket disconnected", "err", err, "retry_in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (c *Client) setConnected(v bool) {
	c.mu.Lock()
	c.connected = v
	c.mu.Unlock()
}

type wsMsg struct {
	Type    string          `json:"type"`
	ID      int             `json:"id"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Event   json.RawMessage `json:"event"`
}

func (c *Client) session(ctx context.Context) error {
	var failures []string
	for _, u := range c.wsURL {
		connected, err := c.runOnce(ctx, u)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failures = append(failures, u+": "+err.Error())
		if connected {
			break
		}
	}
	return errors.New(strings.Join(failures, " | "))
}

func (c *Client) runOnce(ctx context.Context, url string) (bool, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	conn.SetReadLimit(8 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	})

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ping.C:
				c.write(conn, websocket.PingMessage, nil)
			}
		}
	}()

	authed := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return authed, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))

		var m wsMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}

		switch m.Type {
		case "auth_required":
			if err := c.writeJSON(conn, map[string]any{
				"type":         "auth",
				"access_token": c.token,
			}); err != nil {
				return authed, err
			}
		case "auth_invalid":
			return authed, errAuthInvalid
		case "auth_ok":
			authed = true
			c.setConnected(true)
			c.log.Info("ha websocket connected", "url", url)
			if err := c.writeJSON(conn, map[string]any{"id": 1, "type": "get_states"}); err != nil {
				return authed, err
			}
			if err := c.writeJSON(conn, map[string]any{
				"id": 2, "type": "subscribe_events", "event_type": "state_changed",
			}); err != nil {
				return authed, err
			}
			if err := c.writeJSON(conn, map[string]any{
				"id": 3, "type": "subscribe_events", "event_type": "old_tablet_dashboard_theme",
			}); err != nil {
				return authed, err
			}
		case "result":
			if m.ID == 1 && authed {
				var list []State
				if err := json.Unmarshal(m.Result, &list); err == nil {
					c.mu.Lock()
					for _, st := range list {
						c.states[st.EntityID] = st
					}
					c.mu.Unlock()
					c.log.Info("ha states loaded", "count", len(list))
				}
			}
		case "event":
			var ev struct {
				EventType string          `json:"event_type"`
				Data      json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(m.Event, &ev); err != nil {
				continue
			}
			switch ev.EventType {
			case "state_changed":
				var d struct {
					EntityID string `json:"entity_id"`
					NewState *State `json:"new_state"`
				}
				if err := json.Unmarshal(ev.Data, &d); err != nil || d.NewState == nil {
					continue
				}
				st := *d.NewState
				c.mu.Lock()
				c.states[st.EntityID] = st
				c.mu.Unlock()
				c.broadcast(st)
			case "old_tablet_dashboard_theme":
				var d struct {
					Theme string `json:"theme"`
				}
				if err := json.Unmarshal(ev.Data, &d); err != nil {
					continue
				}
				c.themeMu.RLock()
				fn := c.onTheme
				c.themeMu.RUnlock()
				if fn != nil {
					fn(d.Theme)
				}
			}
		}
	}
}

func (c *Client) writeJSON(conn *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(conn, websocket.TextMessage, b)
}

func (c *Client) write(conn *websocket.Conn, mt int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(mt, data)
}

func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	url := c.base + "/api/services/" + domain + "/" + service
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ha service call failed: %s", resp.Status)
	}
	return nil
}
