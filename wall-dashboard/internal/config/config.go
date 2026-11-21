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
	Images   bool    `json:"images,omitempty"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	W        float64 `json:"w"`
	H        int     `json:"h"`
	NewRow   bool    `json:"new_row,omitempty"`
}

type Config struct {
	Version        int      `json:"version"`
	LayoutVersion  int      `json:"layout_version"`
	Title          string   `json:"title"`
	Theme          string   `json:"theme"`
	ThemeMode      string   `json:"theme_mode,omitempty"`
	ThemeEntity    string   `json:"theme_entity,omitempty"`
	ThemeDarkState string   `json:"theme_dark_state,omitempty"`
	ThemeDarkFrom  string   `json:"theme_dark_from,omitempty"`
	ThemeLightFrom string   `json:"theme_light_from,omitempty"`
	Columns        int      `json:"columns"`
	Device         string   `json:"device"`
	DeviceToken    string   `json:"device_token,omitempty"`
	Widgets        []Widget `json:"widgets"`
}

var devices = map[string]bool{
	"ipad-9.7":  true,
	"ipad-10.2": true,
	"ipad-10.9": true,
	"ipad-12.9": true,
	"custom":    true,
}

var themeModes = map[string]bool{
	"manual": true,
	"sun":    true,
	"entity": true,
	"time":   true,
}

var clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func normalizeClock(v, fallback string) string {
	v = strings.TrimSpace(v)
	if clockRe.MatchString(v) {
		return v
	}
	return fallback
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
	if !themeModes[s.cfg.ThemeMode] {
		s.cfg.ThemeMode = "manual"
	}
	if s.cfg.ThemeDarkState == "" {
		s.cfg.ThemeDarkState = "on"
	}
	s.cfg.ThemeDarkFrom = normalizeClock(s.cfg.ThemeDarkFrom, "21:00")
	s.cfg.ThemeLightFrom = normalizeClock(s.cfg.ThemeLightFrom, "07:00")
	if s.cfg.Columns < 2 || s.cfg.Columns > 12 {
		s.cfg.Columns = 6
	}
	if !devices[s.cfg.Device] {
		s.cfg.Device = "ipad-9.7"
	}
	if s.cfg.Widgets == nil {
		s.cfg.Widgets = []Widget{{ID: "clock", Type: "clock", W: 2, H: 1}}
	}
	if s.cfg.LayoutVersion < 2 {
		packLayout(s.cfg.Widgets, s.cfg.Columns)
		s.cfg.LayoutVersion = 2
	}
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// packLayout assigns x/y to widgets that predate free placement. It mimics
// the old auto-flow grid so existing dashboards keep their look.
func packLayout(widgets []Widget, columns int) {
	tracks := columns * 2
	if tracks < 2 {
		tracks = 12
	}
	x, y, rowH := 0, 0, 0
	for i := range widgets {
		w := &widgets[i]
		span := spanOf(w.W)
		if w.NewRow && x != 0 {
			x = 0
			y += rowH
			rowH = 0
		}
		if x+span > tracks {
			x = 0
			y += rowH
			rowH = 0
		}
		w.X = x
		w.Y = y
		x += span
		if h := normalizeH(w.H); h > rowH {
			rowH = h
		}
	}
}

func spanOf(w float64) int {
	span := int(math.Round(w * 2))
	if span < 1 {
		span = 1
	}
	return span
}

func normalizeH(h int) int {
	if h < 1 {
		return 1
	}
	if h > 6 {
		return 6
	}
	return h
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

	cols := in.Columns
	if cols < 2 || cols > 12 {
		cols = 6
	}
	if in.LayoutVersion < 2 {
		packLayout(in.Widgets, cols)
	}
	c := sanitize(in, s.cfg.DeviceToken)
	c.LayoutVersion = 2
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
		Title:          strings.TrimSpace(in.Title),
		Theme:          in.Theme,
		ThemeMode:      in.ThemeMode,
		ThemeEntity:    strings.TrimSpace(in.ThemeEntity),
		ThemeDarkState: strings.TrimSpace(in.ThemeDarkState),
		ThemeDarkFrom:  in.ThemeDarkFrom,
		ThemeLightFrom: in.ThemeLightFrom,
		Columns:        in.Columns,
		Device:         in.Device,
		DeviceToken:    token,
	}
	if out.Title == "" {
		out.Title = "Home"
	}
	if out.Theme != "light" {
		out.Theme = "dark"
	}
	if !themeModes[out.ThemeMode] {
		out.ThemeMode = "manual"
	}
	if out.ThemeDarkState == "" {
		out.ThemeDarkState = "on"
	}
	if out.ThemeEntity != "" && !entityRe.MatchString(out.ThemeEntity) {
		out.ThemeEntity = ""
	}
	out.ThemeDarkFrom = normalizeClock(out.ThemeDarkFrom, "21:00")
	out.ThemeLightFrom = normalizeClock(out.ThemeLightFrom, "07:00")
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
		w.H = normalizeH(w.H)

		tracks := out.Columns * 2
		span := spanOf(w.W)
		if span > tracks {
			span = tracks
			w.W = float64(tracks) / 2
		}
		if w.X < 0 {
			w.X = 0
		}
		if w.X+span > tracks {
			w.X = tracks - span
		}
		if w.Y < 0 {
			w.Y = 0
		}
		if w.Y > 200 {
			w.Y = 200
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
