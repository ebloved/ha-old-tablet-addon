package web

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type cachedCamera struct {
	data []byte
	ct   string
	at   time.Time
}

func (s *Server) camera(w http.ResponseWriter, r *http.Request) {
	d, ok := s.dashboardFor(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := r.URL.Query().Get("id")
	entity := ""
	interval := 3
	found := false
	for _, wd := range d.Widgets {
		if wd.ID == id && wd.Type == "camera" {
			entity = wd.Entity
			interval = wd.Interval
			found = true
			break
		}
	}
	if !found || entity == "" {
		http.Error(w, "camera not found", http.StatusNotFound)
		return
	}
	if interval < 2 {
		interval = 3
	}
	if interval > 30 {
		interval = 30
	}
	ttl := time.Duration(interval) * time.Second

	key := d.ID + "/" + id
	if id == "" {
		key = d.ID + "/" + entity
	}

	s.camMu.Lock()
	cached, hasCache := s.cam[key]
	s.camMu.Unlock()
	if hasCache && time.Since(cached.at) < ttl {
		writeImage(w, cached)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	data, ct, err := s.ha.CameraSnapshot(ctx, entity)
	if err != nil {
		s.log.Warn("camera snapshot failed", "entity", entity, "err", err)
		if hasCache {
			writeImage(w, cached)
			return
		}
		http.Error(w, "camera unavailable", http.StatusBadGateway)
		return
	}
	if ct == "" || !strings.HasPrefix(ct, "image/") {
		ct = "image/jpeg"
	}
	fresh := cachedCamera{data: data, ct: ct, at: time.Now()}

	s.camMu.Lock()
	s.cam[key] = fresh
	s.camMu.Unlock()

	writeImage(w, fresh)
}

func writeImage(w http.ResponseWriter, c cachedCamera) {
	w.Header().Set("Content-Type", c.ct)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(c.data)
}
