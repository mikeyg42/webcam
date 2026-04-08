package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/mikeyg42/webcam/internal/recorder/storage"
)

// RecordingBrowser defines the interface for browsing stored recordings
type RecordingBrowser interface {
	ListRecordings(ctx context.Context, q storage.RecordingQuery) ([]*storage.Recording, error)
	GetRecordingWithSegments(ctx context.Context, id string) (*storage.Recording, error)
	DeleteRecording(ctx context.Context, id string) error
	UpdateRecordingName(ctx context.Context, id string, name string) error
	GenerateStreamURL(ctx context.Context, recordingID string, segmentIndex int) (string, error)
	GetObjectStore() storage.ObjectStore
}

// RecordingsBrowserHandler handles recording browsing API endpoints
type RecordingsBrowserHandler struct {
	browser RecordingBrowser
}

func NewRecordingsBrowserHandler(browser RecordingBrowser) *RecordingsBrowserHandler {
	return &RecordingsBrowserHandler{browser: browser}
}

func (h *RecordingsBrowserHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/recordings", h.handleList)
	mux.HandleFunc("GET /api/recordings/{id}", h.handleGet)
	mux.HandleFunc("DELETE /api/recordings/{id}", h.handleDelete)
	mux.HandleFunc("PATCH /api/recordings/{id}", h.handleUpdate)
	mux.HandleFunc("GET /api/recordings/{id}/download", h.handleDownload)
	mux.HandleFunc("GET /api/recordings/{id}/segments/{index}/stream", h.handleSegmentStream)
}

func (h *RecordingsBrowserHandler) handleList(w http.ResponseWriter, r *http.Request) {
	q := storage.RecordingQuery{
		OrderBy:   "started_at",
		OrderDesc: true,
		Limit:     50,
	}

	if t := r.URL.Query().Get("type"); t != "" {
		q.Type = t
	}
	if s := r.URL.Query().Get("status"); s != "" {
		q.Status = s
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			q.Offset = n
		}
	}
	if v := r.URL.Query().Get("start_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.StartTime = t
		}
	}
	if v := r.URL.Query().Get("end_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.EndTime = t
		}
	}

	recordings, err := h.browser.ListRecordings(r.Context(), q)
	if err != nil {
		log.Printf("[API] Failed to list recordings: %v", err)
		http.Error(w, "Failed to list recordings", http.StatusInternalServerError)
		return
	}
	if recordings == nil {
		recordings = []*storage.Recording{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recordings)
}

func (h *RecordingsBrowserHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing recording ID", http.StatusBadRequest)
		return
	}

	rec, err := h.browser.GetRecordingWithSegments(r.Context(), id)
	if err != nil {
		log.Printf("[API] Failed to get recording %s: %v", id, err)
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rec)
}

func (h *RecordingsBrowserHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing recording ID", http.StatusBadRequest)
		return
	}

	if err := h.browser.DeleteRecording(r.Context(), id); err != nil {
		log.Printf("[API] Failed to delete recording %s: %v", id, err)
		http.Error(w, "Failed to delete recording", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *RecordingsBrowserHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing recording ID", http.StatusBadRequest)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if body.Name != "" {
		if err := h.browser.UpdateRecordingName(r.Context(), id, body.Name); err != nil {
			log.Printf("[API] Failed to update recording %s: %v", id, err)
			http.Error(w, "Failed to update recording", http.StatusInternalServerError)
			return
		}
	}

	rec, err := h.browser.GetRecordingWithSegments(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to retrieve updated recording", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rec)
}

func (h *RecordingsBrowserHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing recording ID", http.StatusBadRequest)
		return
	}

	rec, err := h.browser.GetRecordingWithSegments(r.Context(), id)
	if err != nil {
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}
	if len(rec.Segments) == 0 {
		http.Error(w, "Recording has no segments", http.StatusNotFound)
		return
	}

	// Build a human-readable filename: custom name, or "type_timestamp"
	filename := ""
	if rec.Metadata != nil {
		if name, ok := rec.Metadata["name"].(string); ok && name != "" {
			filename = name
		}
	}
	if filename == "" {
		prefix := "recording"
		if rec.Type == "event" {
			prefix = "motion"
		}
		filename = fmt.Sprintf("%s_%s", prefix, rec.StartedAt.Format("2006-01-02_15-04-05"))
	}

	w.Header().Set("Content-Type", "video/webm")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.webm"`, filename))

	objStore := h.browser.GetObjectStore()
	flusher, _ := w.(http.Flusher)

	for _, seg := range rec.Segments {
		if seg.StorageKey == "" {
			continue
		}
		reader, err := objStore.Get(r.Context(), seg.StorageKey)
		if err != nil {
			log.Printf("[API] Failed to read segment %s: %v", seg.StorageKey, err)
			return
		}
		io.Copy(w, reader)
		reader.Close()
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (h *RecordingsBrowserHandler) handleSegmentStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	indexStr := r.PathValue("index")
	if id == "" || indexStr == "" {
		http.Error(w, "Missing recording ID or segment index", http.StatusBadRequest)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil {
		http.Error(w, "Invalid segment index", http.StatusBadRequest)
		return
	}

	// Get the segment's storage key to stream directly from MinIO
	// (redirect approach fails because CSP blocks cross-origin media loads)
	rec, err := h.browser.GetRecordingWithSegments(r.Context(), id)
	if err != nil {
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}

	var storageKey string
	for _, seg := range rec.Segments {
		if seg.Index == index {
			storageKey = seg.StorageKey
			break
		}
	}
	if storageKey == "" {
		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}

	objStore := h.browser.GetObjectStore()
	reader, err := objStore.Get(r.Context(), storageKey)
	if err != nil {
		log.Printf("[API] Failed to read segment %s: %v", storageKey, err)
		http.Error(w, "Failed to read segment", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "video/webm")
	w.Header().Set("Accept-Ranges", "none")
	io.Copy(w, reader)
}
