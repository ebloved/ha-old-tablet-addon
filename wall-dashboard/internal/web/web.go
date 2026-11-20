package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"oldtabletdash/internal/config"
	"oldtabletdash/internal/ha"
	"oldtabletdash/internal/rss"
)

//go:embed static/*
var staticFS embed.FS

// assetVersion busts caches whenever any embedded asset changes. It must not
// depend on the config version: updating the add-on does not touch the config,
// and iOS Safari happily serves a stale app.js otherwise.
var assetVersion = func() string {
	h := sha256.New()
	for _, name := range []string{"static/app.js", "static/app.css", "static/admin.js"} {
		b, err := staticFS.ReadFile(name)
		if err == nil {
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

type Server struct {
	cfg     *config.Store
	ha      *ha.Client
	rss     *rss.Cache
	log     *slog.Logger
	ingress bool

	camMu sync.Mutex
	cam   map[string]cachedCamera
}

func New(cfg *config.Store, hac *ha.Client, rc *rss.Cache, log *slog.Logger, ingress bool) *Server {
	return &Server{
		cfg:     cfg,
		ha:      hac,
		rss:     rc,
		log:     log,
		ingress: ingress,
		cam:     make(map[string]cachedCamera),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /app.js", staticFile("app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.css", staticFile("app.css", "text/css; charset=utf-8"))

	mux.HandleFunc("GET /api/config", s.auth(func(w http.ResponseWriter, r *http.Request) {
		c := s.cfg.Get()
		c.DeviceToken = ""
		writeJSON(w, http.StatusOK, c)
	}))

	mux.HandleFunc("GET /api/state", s.auth(func(w http.ResponseWriter, r *http.Request) {
		c := s.cfg.Get()
		out := make(map[string]ha.State)
		for _, wd := range c.Widgets {
			if wd.Entity == "" {
				continue
			}
			if st, ok := s.ha.State(wd.Entity); ok {
				out[wd.Entity] = st
			}
		}
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("GET /api/events", s.auth(s.events))
	mux.HandleFunc("POST /api/action", s.auth(s.action))
	mux.HandleFunc("GET /api/rss", s.auth(s.feed))
	mux.HandleFunc("GET /api/alerts", s.auth(s.alerts))
	mux.HandleFunc("GET /api/camera", s.auth(s.camera))

	if s.ingress {
		mux.HandleFunc("GET /admin", s.adminPage)
		mux.HandleFunc("GET /admin.js", staticFile("admin.js", "application/javascript; charset=utf-8"))
		mux.HandleFunc("GET /api/admin/config", s.adminJSON(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, s.cfg.Get())
		}))
		mux.HandleFunc("PUT /api/admin/config", s.adminJSON(s.saveConfig))
		mux.HandleFunc("POST /api/admin/token", s.adminJSON(s.regenToken))
		mux.HandleFunc("GET /api/admin/qr", s.qr)
		mux.HandleFunc("GET /api/admin/states", s.adminJSON(s.entityList))
		mux.HandleFunc("GET /api/admin/status", s.adminJSON(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"connected": s.ha.Connected(),
				"entities":  len(s.ha.States()),
				"version":   s.cfg.Get().Version,
			})
		}))
	}

	return mux
}

func staticFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

func (s *Server) base(r *http.Request) string {
	base := strings.TrimRight(r.Header.Get("X-Ingress-Path"), "/")
	return base
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	page := string(b)
	page = strings.ReplaceAll(page, "__BASE__", s.base(r))
	page = strings.ReplaceAll(page, "__TITLE__", html.EscapeString(s.cfg.Get().Title))
	adminLink := ""
	if s.ingress {
		adminLink = `<a class="admin" href="admin" title="Настройки">&#9881;</a>`
	}
	page = strings.ReplaceAll(page, "__ADMIN_LINK__", adminLink)
	page = strings.ReplaceAll(page, "__V__", assetVersion)
	c := s.cfg.Get()
	page = strings.ReplaceAll(page, "__THEME__", c.Theme)
	page = strings.ReplaceAll(page, "__TRACKS__", strconv.Itoa(c.Columns*2))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	b, err := staticFS.ReadFile("static/admin.html")
	if err != nil {
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	page := strings.ReplaceAll(string(b), "__BASE__", s.base(r))
	page = strings.ReplaceAll(page, "__V__", assetVersion)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Behind ingress the request is already authenticated by Home Assistant,
		// so the panel and its API work without a device token.
		if s.ingress {
			next(w, r)
			return
		}
		tok := r.Header.Get("X-Dash-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		want := s.cfg.Get().DeviceToken
		if subtle.ConstantTimeCompare([]byte(tok), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) adminJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	stateCh := s.ha.Subscribe()
	defer s.ha.Unsubscribe(stateCh)
	cfgCh := s.cfg.Subscribe()
	defer s.cfg.Unsubscribe(cfgCh)

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case st, open := <-stateCh:
			if !open {
				return
			}
			if !s.watched(st.EntityID) {
				continue
			}
			writeSSE(w, flusher, map[string]any{
				"type":      "state",
				"entity_id": st.EntityID,
				"state":     st,
			})
		case v, open := <-cfgCh:
			if !open {
				return
			}
			writeSSE(w, flusher, map[string]any{"type": "config", "version": v})
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, f http.Flusher, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

func (s *Server) watched(entityID string) bool {
	for _, wd := range s.cfg.Get().Widgets {
		if wd.Entity == entityID {
			return true
		}
	}
	return false
}

var allowedServices = map[string]bool{
	"toggle":           true,
	"turn_on":          true,
	"turn_off":         true,
	"open_cover":       true,
	"close_cover":      true,
	"stop_cover":       true,
	"press":            true,
	"media_play_pause": true,
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntityID string         `json:"entity_id"`
		Service  string         `json:"service"`
		Data     map[string]any `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !validEntity(req.EntityID) {
		http.Error(w, "bad entity_id", http.StatusBadRequest)
		return
	}
	domain := strings.SplitN(req.EntityID, ".", 2)[0]
	if !allowedDomains[domain] {
		http.Error(w, "domain not allowed", http.StatusForbidden)
		return
	}
	if req.Service == "" {
		req.Service = "toggle"
	}
	if !allowedServices[req.Service] {
		http.Error(w, "service not allowed", http.StatusForbidden)
		return
	}
	data := map[string]any{"entity_id": req.EntityID}
	for k, v := range req.Data {
		if k == "entity_id" {
			continue
		}
		data[k] = v
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.ha.CallService(ctx, domain, req.Service, data); err != nil {
		s.log.Warn("service call failed", "entity", req.EntityID, "service", req.Service, "err", err)
		http.Error(w, "service call failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var allowedDomains = map[string]bool{
	"light": true, "switch": true, "fan": true, "cover": true,
	"script": true, "scene": true, "button": true, "input_button": true,
	"input_boolean": true, "media_player": true, "climate": true, "automation": true,
}

func validEntity(id string) bool {
	if len(id) < 3 || len(id) > 128 {
		return false
	}
	parts := strings.Split(id, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var widget *config.Widget
	for _, wd := range s.cfg.Get().Widgets {
		if wd.ID == id && wd.Type == "rss" {
			w := wd
			widget = &w
			break
		}
	}
	if widget == nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}
	if widget.Feed == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := s.rss.Get(r.Context(), widget.Feed, widget.Limit)
	if err != nil {
		s.log.Warn("rss failed", "feed", widget.Feed, "err", err)
		http.Error(w, "feed unavailable", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+$`)

func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" || !hostRe.MatchString(host) {
		host = "homeassistant.local"
	}

	link := "http://" + host + ":8088/?t=" + s.cfg.Get().DeviceToken
	png, err := qrcode.Encode(link, qrcode.Medium, 360)
	if err != nil {
		http.Error(w, "qr failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	var in config.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	out, err := s.cfg.Save(in)
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) regenToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.cfg.RegenerateToken()
	if err != nil {
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func (s *Server) entityList(w http.ResponseWriter, r *http.Request) {
	type item struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
		State    string `json:"state"`
	}
	states := s.ha.States()
	out := make([]item, 0, len(states))
	for id, st := range states {
		name := id
		if v, ok := st.Attributes["friendly_name"].(string); ok && v != "" {
			name = v
		}
		out = append(out, item{EntityID: id, Name: name, State: st.State})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
