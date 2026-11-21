package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"oldtabletdash/internal/ha"
)

type alertItem struct {
	Text  string `json:"text"`
	Level string `json:"level"`
}

type alertsResponse struct {
	Items  []alertItem `json:"items"`
	More   int         `json:"more"`
	Source string      `json:"source"`
}

// skipAlertDomains are noisy by design: they are off most of the time and do
// not represent a problem.
var skipAlertDomains = map[string]bool{
	"automation": true, "script": true, "scene": true, "button": true,
	"input_button": true, "person": true, "sun": true, "zone": true,
	"weather": true, "update": true, "todo": true, "conversation": true,
	"tts": true, "stt": true, "tag": true, "persistent_notification": true,
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dashboardFor(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.URL.Query().Get("id")
	entity := ""
	found := false
	for _, wd := range d.Widgets {
		if wd.ID == id && wd.Type == "alerts" {
			entity = wd.Entity
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "alerts widget not found", http.StatusNotFound)
		return
	}

	if entity != "" {
		st, ok := s.ha.State(entity)
		if !ok {
			writeJSON(w, http.StatusOK, alertsResponse{Items: []alertItem{}, Source: "entity"})
			return
		}
		items := alertsFromEntity(st)
		writeJSON(w, http.StatusOK, alertsResponse{Items: items, Source: "entity"})
		return
	}

	items, more := s.scanAlerts()
	writeJSON(w, http.StatusOK, alertsResponse{Items: items, More: more, Source: "scan"})
}

func alertsFromEntity(st ha.State) []alertItem {
	var items []alertItem

	for _, key := range []string{"alerts", "items"} {
		list, ok := st.Attributes[key].([]any)
		if !ok {
			continue
		}
		for _, v := range list {
			if text, ok := v.(string); ok && strings.TrimSpace(text) != "" {
				items = append(items, alertItem{Text: strings.TrimSpace(text), Level: "warn"})
			}
		}
		if len(items) > 0 {
			return items
		}
	}

	state := strings.TrimSpace(st.State)
	if state == "" {
		return items
	}

	if n, err := strconv.ParseFloat(state, 64); err == nil {
		if n <= 0 {
			return items
		}
		text := state
		if msg, ok := st.Attributes["message"].(string); ok && strings.TrimSpace(msg) != "" {
			text = msg
		}
		return []alertItem{{Text: text, Level: "error"}}
	}

	switch strings.ToLower(state) {
	case "off", "ok", "okay", "none", "no", "clear", "0", "idle":
		return items
	}

	text := state
	if msg, ok := st.Attributes["message"].(string); ok && strings.TrimSpace(msg) != "" {
		text = msg
	}
	return []alertItem{{Text: text, Level: "warn"}}
}

func (s *Server) scanAlerts() ([]alertItem, int) {
	var items []alertItem

	for id, st := range s.ha.States() {
		domain := id
		if i := strings.IndexByte(id, '.'); i > 0 {
			domain = id[:i]
		}
		if skipAlertDomains[domain] {
			continue
		}
		name := id
		if v, ok := st.Attributes["friendly_name"].(string); ok && v != "" {
			name = v
		}

		if st.State == "unavailable" {
			items = append(items, alertItem{Text: name + " — недоступен", Level: "error"})
			continue
		}

		if dc, ok := st.Attributes["device_class"].(string); ok && dc == "battery" {
			if lvl, err := strconv.ParseFloat(st.State, 64); err == nil && lvl < 20 {
				items = append(items, alertItem{
					Text:  name + " — " + strconv.FormatFloat(lvl, 'f', 0, 64) + "%",
					Level: "warn",
				})
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Level != items[j].Level {
			return items[i].Level == "error"
		}
		return items[i].Text < items[j].Text
	})

	const limit = 10
	more := 0
	if len(items) > limit {
		more = len(items) - limit
		items = items[:limit]
	}
	return items, more
}
