package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mentholmike/lethe/internal/db"
	"github.com/mentholmike/lethe/internal/models"
	"github.com/mentholmike/lethe/internal/session"
)

// APIServer provides JSON REST endpoints for the dashboard.
type APIServer struct {
	store   *db.Store
	sessMgr *session.Manager
}

// NewAPIServer creates a new API server for the UI.
func NewAPIServer(store *db.Store, sessMgr *session.Manager) *APIServer {
	return &APIServer{store: store, sessMgr: sessMgr}
}

// SetupRoutes mounts all API routes.
func (s *APIServer) SetupRoutes(r *chi.Mux) {
	r.Get("/api/stats", s.handleStats)
	r.Get("/api/sessions", s.handleListSessions)
	r.Get("/api/sessions/{sessionID}", s.handleGetSession)
	r.Get("/api/sessions/{sessionID}/summary", s.handleGetSessionSummary)
	r.Get("/api/sessions/{sessionID}/events", s.handleGetSessionEvents)
	r.Get("/api/sessions/{sessionID}/checkpoints", s.handleGetCheckpoints)
	r.Post("/api/sessions/{sessionID}/compact", s.handleCompact)
	r.Get("/api/flags", s.handleGetFlags)
	r.Put("/api/flags/{eventID}/review", s.handleReviewFlag)
	r.Get("/api/live", s.handleLive)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *APIServer) handleStats(w http.ResponseWriter, r *http.Request) {
	type Stats struct {
		Sessions    int `json:"sessions"`
		Events      int `json:"events"`
		Checkpoints int `json:"checkpoints"`
		Flags       int `json:"flags"`
	}
	// These would need to be added to the store; return placeholder for now.
	writeJSON(w, http.StatusOK, Stats{Sessions: 0, Events: 0, Checkpoints: 0, Flags: 0})
}

func (s *APIServer) handleListSessions(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	sessions, err := s.store.GetAllSessions(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": sessions})
}

func (s *APIServer) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *APIServer) handleGetSessionSummary(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cps, _ := s.store.GetCheckpoints(r.Context(), sess.SessionID)
	evts, _ := s.store.GetSessionEvents(r.Context(), sess.SessionID, 20, 0)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session":           sess,
		"checkpoint_count":  len(cps),
		"event_count":       len(evts),
		"latest_checkpoint": firstOrNil(cps),
		"recent_events":     evts,
	})
}

func (s *APIServer) handleGetSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	evts, err := s.store.GetSessionEvents(r.Context(), sess.SessionID, limit, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": evts})
}

func (s *APIServer) handleGetCheckpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cps, err := s.store.GetCheckpoints(r.Context(), sess.SessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": cps})
}

func (s *APIServer) handleCompact(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cps, _ := s.store.GetCheckpoints(r.Context(), sess.SessionID)
	evts, _ := s.store.GetSessionEvents(r.Context(), sess.SessionID, 10, 0)

	summaryText := buildSummary(cps, evts)
	s.store.CompactSession(r.Context(), sess.SessionID, summaryText)
	writeJSON(w, http.StatusOK, map[string]interface{}{"summary": summaryText, "status": "ok"})
}

func (s *APIServer) handleGetFlags(w http.ResponseWriter, r *http.Request) {
	limit := 50
	flags, err := s.store.GetFlaggedEvents(r.Context(), limit, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"flags": flags})
}

func (s *APIServer) handleReviewFlag(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "eventID")
	var req struct {
		ReviewerID string `json:"reviewer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReviewerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reviewer_id required"})
		return
	}
	if err := s.store.MarkFlagReviewed(r.Context(), eventID, req.ReviewerID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLive streams session events via Server-Sent Events.
func (s *APIServer) handleLive(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Subscribe to new events via polling (SSE lacks native pub/sub).
	// For a production system this would use a proper pub/sub mechanism.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Send initial ping
	fmt.Fprintf(w, "event: ping\ndata: {\"status\":\"connected\"}\n\n")
	f.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Could emit recent events here if we had a subscription system
			fmt.Fprintf(w, "event: ping\ndata: {\"ts\":%d}\n\n", time.Now().Unix())
			f.Flush()
		}
	}
}

func (s *APIServer) resolveSession(ctx context.Context, id string) (*models.Session, error) {
	sess, _ := s.store.GetSessionByKey(ctx, id)
	if sess != nil {
		return sess, nil
	}
	return s.store.GetSession(ctx, id)
}

func firstOrNil(cps []*models.Checkpoint) interface{} {
	if len(cps) == 0 {
		return nil
	}
	return cps[0]
}

func buildSummary(cps []*models.Checkpoint, evts []*models.Event) string {
	// Simplified summary builder — shared logic with API compact handler
	return fmt.Sprintf("Session summary: %d checkpoints, %d events", len(cps), len(evts))
}
