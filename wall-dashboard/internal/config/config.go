package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var entityRe = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

var allowedTypes = map[string]bool{
	"entity":  true,
	"sensor":  true,
	"clock":   true,
	"rss":     true,
	"weather": true,
	"alerts":  true,
	"camera":  true,
	"spacer":  true,
}

type Widget struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Entity   string  `json:"entity,omitempty"`
	Name     string  `json:"name,omitempty"`
	Feed     string  `json:"feed,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	Interval int     `json:"interval,omitempty"`
	W        float64 `json:"w,omitempty"`
	H        int     `json:"h,omitempty"`
	NewRow   bool    `json:"new_row,omitempty"`
}

type Config struct {
	Version     int      `json:"version"`
	Title       string   `json:"title"`
	Theme       string   `json:"theme"`
	Columns     int      `json:"columns"`
	Device      string   `json:"device"`
	DeviceToken string   `json:"device_token,omitempty"`
	Widgets     []Widget `json:"widgets"`
}

var devices = map[string]bool{
	"ipad-9.7":  true,
	"ipad-10.2": true,
	"ipad-10.9": true,
	"ipad-12.9": true,
	"custom":    true,
}

type Store struct {
	mu   sync.Mutex
	path string
	cfg  Config
	subs map[chan int]struct{}
}

func NewStore(dir string) (*Store, error) {
	s := &Store{
		path: filepath.Join(dir, "config.json"),
		subs: make(map[chan int]struct{}),
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
	case !os.IsNotExist(err):
		return nil, err
	}
	if s.cfg.DeviceToken == "" {
		t, err := newToken()
		if err != nil {
			return nil, err
		}
		s.cfg.DeviceToken = t
	}
	if s.cfg.Version == 0 {
		s.cfg.Version = 1
	}
	if s.cfg.Title == "" {
		s.cfg.Title = "Home"
	}
	if s.cfg.Theme != "light" {
		s.cfg.Theme = "dark"
	}
	if s.cfg.Columns < 2 || s.cfg.Columns > 12 {
		s.cfg.Columns = 6
	}
	if !devices[s.cfg.Device] {
		s.cfg.Device = "ipad-9.7"
	}
	if s.cfg.Widgets == nil {
		s.cfg.Widgets = []Widget{{ID: "clock", Type: "clock", W: 2, H: 1}}
	}
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Widgets = append([]Widget(nil), s.cfg.Widgets...)
	return c
}

func (s *Store) Save(in Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := sanitize(in, s.cfg.DeviceToken)
	c.Version = s.cfg.Version + 1
	s.cfg = c
	if err := s.persistLocked(); err != nil {
		return Config{}, err
	}
	for ch := range s.subs {
		select {
		case ch <- c.Version:
		default:
		}
	}
	return c, nil
}

func (s *Store) RegenerateToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := newToken()
	if err != nil {
		return "", err
	}
	s.cfg.DeviceToken = t
	s.cfg.Version++
	if err := s.persistLocked(); err != nil {
		return "", err
	}
	for ch := range s.subs {
		select {
		case ch <- s.cfg.Version:
		default:
		}
	}
	return t, nil
}

func (s *Store) SetTheme(theme string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if theme != "light" {
		theme = "dark"
	}
	if s.cfg.Theme == theme {
		c := s.cfg
		c.Widgets = append([]Widget(nil), s.cfg.Widgets...)
		return c, nil
	}
	s.cfg.Theme = theme
	s.cfg.Version++
	if err := s.persistLocked(); err != nil {
		return Config{}, err
	}
	for ch := range s.subs {
		select {
		case ch <- s.cfg.Version:
		default:
		}
	}
	c := s.cfg
	c.Widgets = append([]Widget(nil), s.cfg.Widgets...)
	return c, nil
}

// Subscribe returns a buffered channel that receives the new config version
// every time the config is saved.
func (s *Store) Subscribe() chan int {
	ch := make(chan int, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *Store) Unsubscribe(ch chan int) {
	s.mu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sanitize(in Config, token string) Config {
	out := Config{
		Title:       strings.TrimSpace(in.Title),
		Theme:       in.Theme,
		Columns:     in.Columns,
		Device:      in.Device,
		DeviceToken: token,
	}
	if out.Title == "" {
		out.Title = "Home"
	}
	if out.Theme != "light" {
		out.Theme = "dark"
	}
	if out.Columns < 2 || out.Columns > 12 {
		out.Columns = 6
	}
	if !devices[out.Device] {
		out.Device = "ipad-9.7"
	}
	seen := make(map[string]bool)
	for i, w := range in.Widgets {
		w.ID = strings.TrimSpace(w.ID)
		w.Type = strings.TrimSpace(w.Type)
		w.Entity = strings.TrimSpace(w.Entity)
		w.Name = strings.TrimSpace(w.Name)
		w.Feed = strings.TrimSpace(w.Feed)

		if !allowedTypes[w.Type] {
			continue
		}
		if w.Type == "entity" || w.Type == "sensor" || w.Type == "weather" || w.Type == "alerts" {
			if w.Entity != "" && !entityRe.MatchString(w.Entity) {
				continue
			}
		}
		if w.Type == "camera" {
			if w.Entity != "" && (!entityRe.MatchString(w.Entity) || !strings.HasPrefix(w.Entity, "camera.")) {
				continue
			}
			if w.Interval < 2 {
				w.Interval = 3
			}
			if w.Interval > 30 {
				w.Interval = 30
			}
		}
		if w.Type == "rss" {
			if w.Feed != "" && !strings.HasPrefix(w.Feed, "http://") && !strings.HasPrefix(w.Feed, "https://") {
				continue
			}
		}
		if w.ID == "" || seen[w.ID] {
			w.ID = fmt.Sprintf("w%d", i+1)
			for seen[w.ID] {
				w.ID += "x"
			}
		}
		seen[w.ID] = true

		w.W = math.Round(w.W*2) / 2
		if w.W < 0.5 {
			w.W = 1
		}
		if w.W > float64(out.Columns) {
			w.W = float64(out.Columns)
		}
		if w.H < 1 {
			w.H = 1
		}
		if w.H > 6 {
			w.H = 6
		}
		if w.Limit < 1 {
			w.Limit = 8
		}
		if w.Limit > 30 {
			w.Limit = 30
		}
		out.Widgets = append(out.Widgets, w)
	}
	if out.Widgets == nil {
		out.Widgets = []Widget{}
	}
	return out
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
