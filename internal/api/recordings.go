package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/bughatti/sentinel/internal/events"
)

// handleListRecordings — GET /api/recordings
func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := events.RecordingFilter{
		Camera: q.Get("camera"),
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
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}

	recs, err := s.store.ListRecordings(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list recordings: "+err.Error())
		return
	}
	if recs == nil {
		recs = []*events.Recording{}
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleRecordingsSummary — GET /api/recordings/summary
func (s *Server) handleRecordingsSummary(w http.ResponseWriter, r *http.Request) {
	cameras := s.cameras.Cameras()
	var all []events.RecordingSummary
	for _, cam := range cameras {
		sums, err := s.store.GetRecordingsSummary(r.Context(), cam)
		if err != nil {
			continue
		}
		all = append(all, sums...)
	}
	writeJSON(w, http.StatusOK, all)
}

// handleCameraRecordings — GET /api/cameras/{name}/recordings
func (s *Server) handleCameraRecordings(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	q := r.URL.Query()
	f := events.RecordingFilter{Camera: name}
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
	recs, err := s.store.ListRecordings(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list recordings: "+err.Error())
		return
	}
	if recs == nil {
		recs = []*events.Recording{}
	}
	writeJSON(w, http.StatusOK, recs)
}

// handleHLSPlaylist — GET /vod/{year}-{month}-{day}/{hour}/{camera}/index.m3u8
//
// The playlist is built from the segment files actually present in the hour's
// recording directory — the same directory handleHLSSegment serves from. This
// guarantees the playlist never references a segment that does not exist: it
// avoids the timezone skew a DB time-window query is prone to (directories are
// named in local time, epochs are UTC) and never lists the not-yet-recorded
// segments of an in-progress hour.
func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	year := chi.URLParam(r, "year")
	month := chi.URLParam(r, "month")
	day := chi.URLParam(r, "day")
	hour := chi.URLParam(r, "hour")
	camera := chi.URLParam(r, "camera")

	// These become path components — reject traversal.
	for _, p := range []string{year, month, day, hour, camera} {
		if strings.Contains(p, "..") || strings.ContainsRune(p, '/') {
			writeError(w, http.StatusBadRequest, "invalid path parameters")
			return
		}
	}

	dir := filepath.Join(
		s.storage.RecordingDir(camera),
		fmt.Sprintf("%s-%s-%s", year, month, day), hour,
	)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "no recordings for that hour")
		return
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mp4") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		writeError(w, http.StatusNotFound, "no recordings for that hour")
		return
	}

	// Build HLS playlist. Segments are fixed 10s; the last one may be shorter but
	// #EXT-X-TARGETDURATION only needs to be an upper bound.
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:3\n")
	sb.WriteString("#EXT-X-TARGETDURATION:10\n")
	sb.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	sb.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	for _, name := range names {
		sb.WriteString("#EXTINF:10.000,\n")
		sb.WriteString(fmt.Sprintf("/vod/%s-%s-%s/%s/%s/%s\n",
			year, month, day, hour, camera, name))
	}
	sb.WriteString("#EXT-X-ENDLIST\n")

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, sb.String())
}

// handleHLSSegment — GET /vod/{year}-{month}-{day}/{hour}/{camera}/{segment}
// The segment file is served by base-name lookup in the expected directory.
func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	year := chi.URLParam(r, "year")
	month := chi.URLParam(r, "month")
	day := chi.URLParam(r, "day")
	hour := chi.URLParam(r, "hour")
	camera := chi.URLParam(r, "camera")
	segment := chi.URLParam(r, "segment")

	// Security: reject path traversal.
	if strings.Contains(segment, "..") || strings.ContainsRune(segment, '/') {
		writeError(w, http.StatusBadRequest, "invalid segment name")
		return
	}

	// Build path: <recordingsDir>/<camera>/<YYYY-MM-DD>/<HH>/<segment>
	path := filepath.Clean(filepath.Join(
		s.storage.RecordingDir(camera),
		fmt.Sprintf("%s-%s-%s", year, month, day), hour, segment,
	))

	serveFile(w, r, path, "video/mp4")
}

// handleServeClip — GET /clips/{file}
func (s *Server) handleServeClip(w http.ResponseWriter, r *http.Request) {
	file := chi.URLParam(r, "file")
	if strings.Contains(file, "..") || strings.ContainsRune(file, '/') {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	id := strings.TrimSuffix(file, ".mp4")
	path := filepath.Clean(s.storage.ClipPath(id))
	serveFile(w, r, path, "video/mp4")
}

// serveFile serves a file with the given content-type using http.ServeContent
// for range-request support.
func serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "file not found")
		} else {
			writeError(w, http.StatusInternalServerError, "open file: "+err.Error())
		}
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat file: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
