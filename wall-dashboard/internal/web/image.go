package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"oldtabletdash/internal/config"
)

type cachedImage struct {
	data []byte
	ct   string
	at   time.Time
}

func (s *Server) feedImage(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dashboardFor(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.URL.Query().Get("id")
	idx, _ := strconv.Atoi(r.URL.Query().Get("i"))

	var widget *config.Widget
	for _, wd := range d.Widgets {
		if wd.ID == id && wd.Type == "rss" {
			w := wd
			widget = &w
			break
		}
	}
	if widget == nil || widget.Feed == "" {
		http.NotFound(w, r)
		return
	}

	items, err := s.rss.Get(r.Context(), widget.Feed, widget.Limit)
	if err != nil {
		http.Error(w, "feed unavailable", http.StatusBadGateway)
		return
	}
	if idx < 0 || idx >= len(items) {
		http.NotFound(w, r)
		return
	}
	src := strings.TrimSpace(items[idx].Image)
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.NotFound(w, r)
		return
	}

	s.imgMu.Lock()
	cached, has := s.img[src]
	s.imgMu.Unlock()
	if has && time.Since(cached.at) < time.Hour {
		writeImageBytes(w, cached)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, src, nil)
	if err != nil {
		http.Error(w, "bad image", http.StatusBadGateway)
		return
	}
	req.Header.Set("User-Agent", "old-tablet-dashboard/0.1")
	resp, err := s.httpc.Do(req)
	if err != nil {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	data, err := readBodyLimit(resp.Body, 8<<20)
	if err != nil {
		http.Error(w, "image read failed", http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	fresh := cachedImage{data: data, ct: ct, at: time.Now()}

	s.imgMu.Lock()
	if len(s.img) > 128 {
		s.img = make(map[string]cachedImage)
	}
	s.img[src] = fresh
	s.imgMu.Unlock()

	writeImageBytes(w, fresh)
}

func writeImageBytes(w http.ResponseWriter, c cachedImage) {
	w.Header().Set("Content-Type", c.ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(c.data)
}
