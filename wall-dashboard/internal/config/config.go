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
var idRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

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

type Dashboard struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DeviceToken string   `json:"device_token,omitempty"`
	Orientation string   `json:"orientation,omitempty"`
	Columns     int      `json:"columns,omitempty"`
	Device      string   `json:"device,omitempty"`
	Widgets     []Widget `json:"widgets"`
}

type Config struct {
	Version        int         `json:"version"`
	LayoutVersion  int         `json:"layout_version"`
	Title          string      `json:"title"`
	Theme          string      `json:"theme"`
	ThemeMode      string      `json:"theme_mode,omitempty"`
	ThemeEntity    string      `json:"theme_entity,omitempty"`
	ThemeDarkState string      `json:"theme_dark_state,omitempty"`
	ThemeDarkFrom  string      `json:"theme_dark_from,omitempty"`
	ThemeLightFrom string      `json:"theme_light_from,omitempty"`
	Dashboards     []Dashboard `json:"dashboards"`

	// Legacy single-layout fields, read once during migration.
	LegacyColumns int      `json:"columns,omitempty"`
	LegacyDevice  string   `json:"device,omitempty"`
	LegacyWidgets []Widget `json:"widgets,omitempty"`
	LegacyToken   string   `json:"device_token,omitempty"`
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

	migrate(&s.cfg)
	if s.cfg.LayoutVersion < 2 {
		for i := range s.cfg.Dashboards {
			packLayout(s.cfg.Dashboards[i].Widgets, s.cfg.Dashboards[i].Columns)
		}
		s.cfg.LayoutVersion = 2
	}
	s.cfg = sanitize(s.cfg)
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyConfig(s.cfg)
}

func (s *Store) DashboardByID(id string) (Dashboard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.cfg.Dashboards {
		if d.ID == id {
			return copyDashboard(d), true
		}
	}
	return Dashboard{}, false
}

func (s *Store) DashboardByToken(token string) (Dashboard, bool) {
	if token == "" {
		return Dashboard{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.cfg.Dashboards {
		if d.DeviceToken == token {
			return copyDashboard(d), true
		}
	}
	return Dashboard{}, false
}

func (s *Store) Save(in Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	migrate(&in)
	cols := make([]int, len(in.Dashboards))
	for i, d := range in.Dashboards {
		cols[i] = d.Columns
		if cols[i] < 2 || cols[i] > 12 {
			cols[i] = 6
		}
	}
	if in.LayoutVersion < 2 {
		for i := range in.Dashboards {
			packLayout(in.Dashboards[i].Widgets, cols[i])
		}
	}

	c := sanitize(in)
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
	return copyConfig(c), nil
}

func (s *Store) RegenerateToken(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := newToken()
	if err != nil {
		return "", err
	}
	found := false
	for i := range s.cfg.Dashboards {
		if s.cfg.Dashboards[i].ID == id {
			s.cfg.Dashboards[i].DeviceToken = t
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("dashboard %q not found", id)
	}
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
		return copyConfig(s.cfg), nil
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
	return copyConfig(s.cfg), nil
}

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

func migrate(c *Config) {
	if len(c.Dashboards) == 0 {
		d := Dashboard{
			ID:          "main",
			Name:        "Основной",
			DeviceToken: c.LegacyToken,
			Orientation: "landscape",
			Columns:     c.LegacyColumns,
			Device:      c.LegacyDevice,
			Widgets:     c.LegacyWidgets,
		}
		if d.Widgets == nil {
			d.Widgets = []Widget{{ID: "clock", Type: "clock", W: 2, H: 1}}
		}
		c.Dashboards = []Dashboard{d}
	}
	c.LegacyColumns = 0
	c.LegacyDevice = ""
	c.LegacyWidgets = nil
	c.LegacyToken = ""
}

func sanitize(in Config) Config {
	out := Config{
		LayoutVersion:  2,
		Title:          strings.TrimSpace(in.Title),
		Theme:          in.Theme,
		ThemeMode:      in.ThemeMode,
		ThemeEntity:    strings.TrimSpace(in.ThemeEntity),
		ThemeDarkState: strings.TrimSpace(in.ThemeDarkState),
		ThemeDarkFrom:  in.ThemeDarkFrom,
		ThemeLightFrom: in.ThemeLightFrom,
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

	seenIDs := make(map[string]bool)
	seenTokens := make(map[string]bool)
	for i, d := range in.Dashboards {
		out.Dashboards = append(out.Dashboards, sanitizeDashboard(d, i+1, seenIDs, seenTokens))
	}
	if len(out.Dashboards) == 0 {
		out.Dashboards = []Dashboard{{
			ID:          "main",
			Name:        "Основной",
			DeviceToken: mustToken(),
			Orientation: "landscape",
			Columns:     6,
			Device:      "ipad-9.7",
			Widgets:     []Widget{{ID: "clock", Type: "clock", W: 2, H: 1}},
		}}
	}
	return out
}

func sanitizeDashboard(in Dashboard, n int, seenIDs, seenTokens map[string]bool) Dashboard {
	d := Dashboard{
		ID:          strings.ToLower(strings.TrimSpace(in.ID)),
		Name:        strings.TrimSpace(in.Name),
		DeviceToken: strings.TrimSpace(in.DeviceToken),
		Orientation: strings.ToLower(strings.TrimSpace(in.Orientation)),
		Columns:     in.Columns,
		Device:      strings.TrimSpace(in.Device),
	}
	if d.ID == "" || !idRe.MatchString(d.ID) || seenIDs[d.ID] {
		d.ID = fmt.Sprintf("d%d", n)
		for seenIDs[d.ID] {
			d.ID += "x"
		}
	}
	seenIDs[d.ID] = true
	if d.Name == "" {
		d.Name = fmt.Sprintf("Планшет %d", n)
	}
	if d.DeviceToken == "" || seenTokens[d.DeviceToken] {
		d.DeviceToken = mustToken()
	}
	seenTokens[d.DeviceToken] = true
	if d.Orientation != "portrait" {
		d.Orientation = "landscape"
	}
	if d.Columns < 2 || d.Columns > 12 {
		d.Columns = 6
	}
	if !devices[d.Device] {
		d.Device = "ipad-9.7"
	}
	d.Widgets = sanitizeWidgets(in.Widgets, d.Columns)
	return d
}

func sanitizeWidgets(widgets []Widget, columns int) []Widget {
	out := make([]Widget, 0, len(widgets))
	seen := make(map[string]bool)
	for i, w := range widgets {
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
		if w.W > float64(columns) {
			w.W = float64(columns)
		}
		w.H = normalizeH(w.H)

		tracks := columns * 2
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
		out = append(out, w)
	}
	return out
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

func normalizeClock(v, fallback string) string {
	v = strings.TrimSpace(v)
	if clockRe.MatchString(v) {
		return v
	}
	return fallback
}

func copyConfig(c Config) Config {
	out := c
	out.Dashboards = make([]Dashboard, len(c.Dashboards))
	for i, d := range c.Dashboards {
		out.Dashboards[i] = copyDashboard(d)
	}
	return out
}

func copyDashboard(d Dashboard) Dashboard {
	out := d
	out.Widgets = append([]Widget(nil), d.Widgets...)
	return out
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func mustToken() string {
	t, err := newToken()
	if err != nil {
		return "dev-token"
	}
	return t
}
