package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/bughatti/sentinel/internal/events"
)

// handleListEvents — GET /api/events
// Query params: camera, label, sub_label, after, before, has_clip, has_snapshot,
//               false_positive, zone, limit, skip
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := events.EventFilter{
		Camera:   q.Get("camera"),
		Label:    q.Get("label"),
		SubLabel: q.Get("sub_label"),
	}

	if v := q.Get("after"); v != "" {
		if t, err := strconv.ParseFloat(v, 64); err == nil {
			f.After = &t
		}
	}
	if v := q.Get("before"); v != "" {
		if t, err := strconv.ParseFloat(v, 64); err == nil {
			f.Before = &t
		}
	}
	if v := q.Get("has_clip"); v != "" {
		b := v == "1" || v == "true"
		f.HasClip = &b
	}
	if v := q.Get("has_snapshot"); v != "" {
		b := v == "1" || v == "true"
		f.HasSnapshot = &b
	}
	if v := q.Get("false_positive"); v != "" {
		b := v == "1" || v == "true"
		f.FalsePositive = &b
	}
	if v := q.Get("zone"); v != "" {
		f.Zones = []string{v}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := q.Get("skip"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Skip = n
		}
	}

	evts, err := s.store.ListEvents(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list events: "+err.Error())
		return
	}
	if evts == nil {
		evts = []*events.Event{}
	}
	writeJSON(w, http.StatusOK, evts)
}

// handleEventsSummary — GET /api/events/summary
func (s *Server) handleEventsSummary(w http.ResponseWriter, r *http.Request) {
	cameras := s.cameras.Cameras()
	type summary struct {
		Camera string `json:"camera"`
		Label  string `json:"label"`
		Count  int    `json:"count"`
	}
	var out []summary
	for _, cam := range cameras {
		evts, err := s.store.ListEvents(r.Context(), events.EventFilter{Camera: cam, Limit: 1000})
		if err != nil {
			continue
		}
		counts := make(map[string]int)
		for _, e := range evts {
			counts[e.Label]++
		}
		for label, count := range counts {
			out = append(out, summary{Camera: cam, Label: label, Count: count})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetEvent — GET /api/events/{id}
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	e, err := s.store.GetEvent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleDeleteEvent — DELETE /api/events/{id}
func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetEvent(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}

	// Delete media files from disk.
	_ = os.Remove(filepath.Clean(s.storage.SnapshotPath(id)))
	_ = os.Remove(filepath.Clean(s.storage.ClipPath(id)))

	if err := s.db.Exec(r.Context(), `DELETE FROM events WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete event: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// handleFalsePositive — POST /api/events/{id}/false_positive
func (s *Server) handleFalsePositive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	e, err := s.store.GetEvent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	e.FalsePositive = true
	if err := s.store.UpdateEvent(r.Context(), e); err != nil {
		writeError(w, http.StatusInternalServerError, "update event: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleRetainEvent — POST /api/events/{id}/retain
func (s *Server) handleRetainEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	e, err := s.store.GetEvent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	e.RetainIndefinitely = true
	if err := s.store.UpdateEvent(r.Context(), e); err != nil {
		writeError(w, http.StatusInternalServerError, "update event: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleEventSnapshot — GET /api/events/{id}/snapshot.jpg
func (s *Server) handleEventSnapshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	path := filepath.Clean(s.storage.SnapshotPath(id))

	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=3600")
	_, _ = io.Copy(w, f)
}

// handleEventClip — GET /api/events/{id}/clip.mp4
func (s *Server) handleEventClip(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	path := filepath.Clean(s.storage.ClipPath(id))

	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "clip not found")
		return
	}
	defer f.Close()

	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Cache-Control", "max-age=3600")
	_, _ = io.Copy(w, f)
}
