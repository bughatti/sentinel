package api

import (
	"encoding/json"
	"image"
	_ "image/jpeg" // register JPEG decoder
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleListFaces — GET /api/faces
func (s *Server) handleListFaces(w http.ResponseWriter, r *http.Request) {
	ids, err := s.store.ListFaceIdentities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list faces: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

// handleEnrollFace — POST /api/faces/enroll
// Body: {"name": "Bo", "event_id": "<id>"} — enrolls the largest face found in
// that event's snapshot under the given name.
func (s *Server) handleEnrollFace(w http.ResponseWriter, r *http.Request) {
	if s.faces == nil || !s.faces.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "face recognition is disabled")
		return
	}
	var req struct {
		Name    string `json:"name"`
		EventID string `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.EventID == "" {
		writeError(w, http.StatusBadRequest, "event_id is required")
		return
	}

	path := s.storage.SnapshotPath(req.EventID)
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "snapshot not found for event")
		return
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "decode snapshot: "+err.Error())
		return
	}

	emb, box, err := s.faces.EmbedLargestFace(img)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "no usable face in snapshot: "+err.Error())
		return
	}
	id, err := s.store.InsertFaceIdentity(r.Context(), req.Name, emb, req.EventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enroll: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":   id,
		"name": req.Name,
		"box":  []int{box.Min.X, box.Min.Y, box.Max.X, box.Max.Y},
	})
}

// handleDeleteFace — DELETE /api/faces/{id}
func (s *Server) handleDeleteFace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	n, err := s.store.DeleteFaceIdentity(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "delete face: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
