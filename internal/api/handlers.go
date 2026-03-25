package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/mentholmike/lethe/internal/models"
)

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStats returns aggregate stats.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// resolveSession looks up a session by sessionKey first, then by sessionID.
// This allows both human-readable keys (OpenClaw sessionKey) and UUIDs (Lethe session_id)
// to be used interchangeably in URL paths.
func (s *Server) resolveSession(ctx context.Context, id string) (*models.Session, error) {
	// URL-decode the id in case it was percent-encoded by the client.
	decodedID, err := url.PathUnescape(id)
	if err != nil {
		return nil, err
	}
	// Try sessionKey first.
	sess, err := s.sessMgr.Store().GetSessionByKey(ctx, decodedID)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	// Fall back to session_id lookup.
	return s.sessMgr.GetSession(ctx, decodedID)
}

// --- Session handlers ---

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionKey  string `json:"session_key"`
		AgentID     string `json:"agent_id"`
		ProjectID   string `json:"project_id"`
		AgentName   string `json:"agent_name"`
		ProjectName string `json:"project_name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if req.AgentID == "" || req.ProjectID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "agent_id and project_id are required"})
		return
	}

	// If a session_key is provided, use StartSessionWithKey to get a stable ID.
	// Otherwise fall back to the regular StartSession.
	var sess *models.Session
	var err error
	if req.SessionKey != "" {
		sess, err = s.sessMgr.StartSessionWithKey(r.Context(), req.SessionKey, req.AgentID, req.ProjectID, req.AgentName, req.ProjectName)
	} else {
		sess, err = s.sessMgr.StartSession(r.Context(), req.AgentID, req.ProjectID, req.AgentName, req.ProjectName)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}
	if err := s.sessMgr.Heartbeat(r.Context(), sess.SessionID); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInterruptSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	var req struct {
		Snapshot struct {
			OpenThreads    []string `json:"open_threads"`
			RecentEventIDs []string `json:"recent_event_ids"`
			CurrentTask   string   `json:"current_task"`
			LastTool      string   `json:"last_tool"`
		} `json:"snapshot"`
	}
	var snapshot *models.Snapshot
	if err := readJSON(r, &req); err == nil && req.Snapshot.CurrentTask != "" {
		snapshot = &models.Snapshot{
			OpenThreads:    req.Snapshot.OpenThreads,
			RecentEventIDs: req.Snapshot.RecentEventIDs,
			CurrentTask:   req.Snapshot.CurrentTask,
			LastTool:      req.Snapshot.LastTool,
		}
	}

	if err := s.sessMgr.InterruptSession(r.Context(), sess, snapshot); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	sess.State = models.SessionInterrupted
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleCompleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	var req struct {
		Summary string `json:"summary"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	if err := s.sessMgr.CompleteSession(r.Context(), sess, req.Summary); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	sess.State = models.SessionCompleted
	sess.Summary = req.Summary
	writeJSON(w, http.StatusOK, sess)
}

// --- Event handlers ---

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	var req CreateEventRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	// DEBUG: log what we received
	log.Printf("[DEBUG] handleCreateEvent: event_type=%q content=%q", req.EventType, req.Content)
	// Default event_type to "log" if empty
	if req.EventType == "" {
		req.EventType = "log"
	}
	// Skip creating event if content is empty/whitespace (treat as no-op)
	if strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"event_id": "", "skipped": "empty content"})
		return
	}
	if req.EventType == "task" && req.TaskTitle == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "task_title is required for task events"})
		return
	}
	if req.EventType == "task" && req.TaskTitle == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "task_title is required for task events"})
		return
	}

	event := &models.Event{
		EventID:       generateID(),
		SessionID:     sess.SessionID,
		ParentEventID: req.ParentEventID,
		EventType:     models.EventType(req.EventType),
		Content:       req.Content,
		Confidence:    req.Confidence,
		Tags:          strings.Join(req.Tags, ","),
		TaskTitle:     req.TaskTitle,
	}
	if req.TaskStatus != "" {
		ts := models.TaskStatus(req.TaskStatus)
		event.TaskStatus = &ts
	}

	if err := s.store.CreateEvent(r.Context(), event); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Broadcast to SSE clients.
	if s.broadcaster != nil {
		s.broadcaster.Broadcast("event", map[string]interface{}{
			"event_id":   event.EventID,
			"session_id": event.SessionID,
			"event_type": event.EventType,
			"content":    truncate(event.Content, 200),
			"created_at": event.CreatedAt,
		})
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"event_id":   event.EventID,
		"created_at": event.CreatedAt,
	})
}

func (s *Server) handleGetSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 200 {
				limit = 200
			}
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	total, err := s.store.GetSessionEventsCount(r.Context(), sess.SessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	events, err := s.store.GetSessionEvents(r.Context(), sess.SessionID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events": events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// --- Checkpoint handlers ---

func (s *Server) handleCreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	var req struct {
		Snapshot struct {
			OpenThreads    []string `json:"open_threads"`
			RecentEventIDs []string `json:"recent_event_ids"`
			CurrentTask   string   `json:"current_task"`
			LastTool      string   `json:"last_tool"`
		} `json:"snapshot"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	cp := &models.Checkpoint{
		CheckpointID: generateID(),
		SessionID:    sess.SessionID,
		Snapshot: models.Snapshot{
			OpenThreads:    req.Snapshot.OpenThreads,
			RecentEventIDs: req.Snapshot.RecentEventIDs,
			CurrentTask:   req.Snapshot.CurrentTask,
			LastTool:      req.Snapshot.LastTool,
		},
	}
	if err := s.store.CreateCheckpoint(r.Context(), cp); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Broadcast to SSE clients.
	if s.broadcaster != nil {
		s.broadcaster.Broadcast("checkpoint", map[string]interface{}{
			"checkpoint_id": cp.CheckpointID,
			"session_id":     cp.SessionID,
			"seq":            cp.Seq,
			"created_at":     cp.CreatedAt,
		})
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"checkpoint_id": cp.CheckpointID,
		"seq":           cp.Seq,
		"created_at":    cp.CreatedAt,
	})
}

func (s *Server) handleGetCheckpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}
	cps, err := s.store.GetCheckpoints(r.Context(), sess.SessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"checkpoints": cps,
		"total":       len(cps),
	})
}

func (s *Server) handleGetTaskChain(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "eventID")
	chain, err := s.store.GetTaskChain(r.Context(), eventID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if len(chain) == 0 {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "event not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"chain": chain,
		"total": len(chain),
	})
}

// handleSSE streams events to connected clients via Server-Sent Events.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch, done := s.broadcaster.AddClient()
	defer done()

	// Send initial ping
	fmt.Fprintf(w, "event: ping\ndata: {\"status\":\"connected\"}\n\n")
	f.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			w.Write(msg)
			f.Flush()
		}
	}
}

// --- Flag handlers ---

func (s *Server) handleGetFlags(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	flags, err := s.store.GetFlaggedEvents(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"flags": flags,
		"total": len(flags),
	})
}

func (s *Server) handleReviewFlag(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "eventID")
	var req struct {
		ReviewerID string `json:"reviewer_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if req.ReviewerID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "reviewer_id is required"})
		return
	}
	if err := s.store.MarkFlagReviewed(r.Context(), eventID, req.ReviewerID); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Request/Response types ---

type CreateEventRequest struct {
	EventType     string   `json:"event_type"`
	Content       string   `json:"content"`
	Confidence    *float64 `json:"confidence,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	ParentEventID string   `json:"parent_event_id,omitempty"`
	TaskTitle     string   `json:"task_title,omitempty"`
	TaskStatus    string   `json:"task_status,omitempty"`
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// handleListSessions returns all sessions ordered by last heartbeat.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	sessions, err := s.store.GetAllSessions(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": sessions})
}

// handleGetSessionSummary returns a full session view: session + latest checkpoint + recent events.
func (s *Server) handleGetSessionSummary(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")

	if s.sessMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "session manager not available"})
		return
	}
	sess, err := s.resolveSession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	cps, _ := s.store.GetCheckpoints(r.Context(), sess.SessionID)
	var latestCP *models.Checkpoint
	if len(cps) > 0 {
		latestCP = cps[0]
	}

	events, _ := s.store.GetSessionEvents(r.Context(), sess.SessionID, 20, 0)

	// Also surface summary at top level for plugin convenience.
	var topSummary string
	if sess.Summary != "" {
		topSummary = sess.Summary
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session":           sess,
		"summary":           topSummary,
		"latest_checkpoint": latestCP,
		"recent_events":     events,
		"checkpoint_count": len(cps),
		"event_count":      len(events),
	})
}

// handleCompact generates a text summary from recent checkpoints and stores it
// in the session's summary field. It returns the summary text.
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "session_id is required"})
		return
	}
	ctx := r.Context()
	sess, err := s.resolveSession(ctx, sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
		return
	}

	cps, err := s.store.GetCheckpoints(ctx, sess.SessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "failed to load checkpoints"})
		return
	}

	evts, err := s.store.GetSessionEvents(ctx, sess.SessionID, 10, 0)
	if err != nil {
		evts = nil // non-fatal
	}

	// Build summary text from last 5 checkpoints + recent events.
	var lines []string
	lines = append(lines, "Session summary:")

	if len(cps) == 0 && evts == nil {
		lines = append(lines, "(no checkpoints or events yet)")
	} else {
		// Checkpoints: cps are ordered DESC by seq (most recent first). Take up to 5.
		if len(cps) > 0 {
			lines = append(lines, "Recent checkpoints:")
			end := 5
			if len(cps) < 5 {
				end = len(cps)
			}
			for _, cp := range cps[:end] {
				snap := cp.Snapshot // already a models.Snapshot struct
				parts := []string{}
				if snap.CurrentTask != "" {
					parts = append(parts, "task: "+snap.CurrentTask)
				}
				if snap.LastTool != "" {
					parts = append(parts, "tool: "+snap.LastTool)
				}
				if len(snap.OpenThreads) > 0 {
					parts = append(parts, fmt.Sprintf("%d open threads", len(snap.OpenThreads)))
				}
				tag := fmt.Sprintf("  seq=%d", cp.Seq)
				if len(parts) > 0 {
					tag += " (" + strings.Join(parts, ", ") + ")"
				}
				lines = append(lines, tag)
			}
		}

		// Events: include last 10 events (record, log, flag, task)
		if evts != nil && len(evts) > 0 {
			lines = append(lines, "Recent events:")
			for _, ev := range evts {
				prefix := fmt.Sprintf("  [%s]", ev.EventType)
				if ev.EventType == "task" && ev.TaskStatus != nil && *ev.TaskStatus != "" {
					prefix += fmt.Sprintf(" [%s]", ev.TaskStatus)
				}
				if ev.EventType == "flag" && ev.Confidence != nil {
					prefix += fmt.Sprintf(" confidence=%.0f%%", *ev.Confidence*100)
				}
				// Truncate long content
				content := ev.Content
				if len(content) > 120 {
					content = content[:120] + "..."
				}
				lines = append(lines, fmt.Sprintf("%s %s", prefix, content))
			}
		}
	}

	summaryText := strings.Join(lines, "\n")
	s.store.CompactSession(ctx, sess.SessionID, summaryText)

	// Count tokens using Unicode codepoint-aware approximation.
	// Words (whitespace-separated) tend to correlate with tokens at ~1.3x.
	// This is more accurate than len()/4 which uses byte count and
	// underestimates non-Latin scripts. Using rune count / 4 as a
	// middle ground that's simple and handles all Unicode correctly.
	tokenCount := utf8.RuneCountInString(summaryText) / 4

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"summary":      summaryText,
		"tokens_after": tokenCount,
	})
}
