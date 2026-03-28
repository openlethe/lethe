package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
	"github.com/openlethe/lethe/internal/session"
)

// APIServer provides JSON REST endpoints for the dashboard, plus HTMX-aware
// HTML-fragment responses for use with hx-get.
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

// isHTMX returns true when the request is an HTMX AJAX request.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

// frag renders a named Go template and writes the result as text/html.
// The data argument must be compatible with the template's expectations.
func (s *APIServer) frag(w http.ResponseWriter, r *http.Request, name string, data interface{}) {
	buf := new(bytes.Buffer)
	if err := templates.ExecuteTemplate(buf, name+".html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	buf.WriteTo(w)
}

// fragList renders a collection template with a "items" key for reuse
// across list views (sessions, events, checkpoints, flags).
func (s *APIServer) fragList(w http.ResponseWriter, r *http.Request, name string, items interface{}) {
	s.frag(w, r, name, map[string]interface{}{"items": items})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- Stats ---

func (s *APIServer) handleStats(w http.ResponseWriter, r *http.Request) {
	var stats struct {
		Sessions    int `json:"sessions"`
		Events      int `json:"events"`
		Checkpoints int `json:"checkpoints"`
		Flags       int `json:"flags"`
	}
	stats.Sessions, _ = s.store.CountSessions(r.Context())
	stats.Events, _ = s.store.CountEvents(r.Context())
	stats.Checkpoints, _ = s.store.CountCheckpoints(r.Context())
	stats.Flags, _ = s.store.CountFlags(r.Context())
	if isHTMX(r) {
		s.frag(w, r, "stats", stats)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- Sessions ---

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
	if isHTMX(r) {
		s.fragList(w, r, "sessions", sessions)
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
	if isHTMX(r) {
		s.frag(w, r, "session_row", sess)
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
	if isHTMX(r) {
		s.frag(w, r, "session_summary", map[string]interface{}{
			"session":          sess,
			"checkpoint_count": len(cps),
			"event_count":      len(evts),
			"latest_checkpoint": firstOrNil(cps),
			"recent_events":    evts,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session":           sess,
		"checkpoint_count":  len(cps),
		"event_count":       len(evts),
		"latest_checkpoint": firstOrNil(cps),
		"recent_events":     evts,
	})
}

// --- Events ---

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
	if isHTMX(r) {
		s.fragList(w, r, "events", evts)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": evts})
}

// --- Checkpoints ---

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
	if isHTMX(r) {
		s.fragList(w, r, "checkpoints", cps)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": cps})
}

// --- Compact ---

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

// --- Flags ---

func (s *APIServer) handleGetFlags(w http.ResponseWriter, r *http.Request) {
	limit := 50
	flags, err := s.store.GetFlaggedEvents(r.Context(), limit, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if isHTMX(r) {
		s.fragList(w, r, "flags", flags)
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

// --- Live ---

// handleLive streams session events via Server-Sent Events.
func (s *APIServer) handleLive(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.ResponseWriter).(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	fmt.Fprintf(w, "event: ping\ndata: {\"status\":\"connected\"}\n\n")
	f.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: {\"ts\":%d}\n\n", time.Now().Unix())
			f.Flush()
		}
	}
}

// --- Helpers ---

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
	return fmt.Sprintf("Session summary: %d checkpoints, %d events", len(cps), len(evts))
}

// --- Counter stubs (add to db/store if not present) ---

func (s *APIServer) countRows(ctx context.Context, query string) (int, error) {
	var n int
	err := s.store.DB.QueryRowContext(ctx, query).Scan(&n)
	return n, err
}
