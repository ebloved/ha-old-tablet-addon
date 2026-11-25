package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
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
	if interval < 1 {
		interval = 3
	}
	if interval > 30 {
		interval = 30
	}
	ttl := time.Duration(interval) * time.Second
	if interval <= 1 {
		// A 1s client interval would otherwise hit a 1s cache almost every
		// time and effectively halve the frame rate.
		ttl = 0
	} else {
		ttl -= 200 * time.Millisecond
	}

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

	lock := s.cameraLock(key)
	lock.Lock()
	defer lock.Unlock()

	// Another request may have refreshed the frame while we waited.
	s.camMu.Lock()
	cached, hasCache = s.cam[key]
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

func (s *Server) cameraLock(key string) *sync.Mutex {
	s.camMu.Lock()
	defer s.camMu.Unlock()
	m := s.camLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		s.camLocks[key] = m
	}
	return m
}
