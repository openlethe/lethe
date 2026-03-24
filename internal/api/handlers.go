package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/mentholmike/lethe/internal/models"
)

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// resolveSession looks up a session by sessionKey first, then by sessionID.
// This allows both human-readable keys (OpenClaw sessionKey) and UUIDs (Lethe session_id)
// to be used interchangeably in URL paths.
func (s *Server) resolveSession(ctx context.Context, id string) (*models.Session, error) {
	// Try sessionKey first.
	sess, err := s.sessMgr.Store().GetSessionByKey(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	// Fall back to session_id lookup.
	return s.sessMgr.GetSession(ctx, id)
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
	if req.EventType == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "event_type is required"})
		return
	}
	if req.Content == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "content is required"})
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
		Tags:          req.Tags,
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
	Tags          string   `json:"tags,omitempty"`
	ParentEventID string   `json:"parent_event_id,omitempty"`
	TaskTitle     string   `json:"task_title,omitempty"`
	TaskStatus    string   `json:"task_status,omitempty"`
}

// handleListSessions returns all sessions for an agent+project.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// TODO: add agent_id/project_id query filters
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": []interface{}{}})
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session":        sess,
		"latest_checkpoint": latestCP,
		"recent_events":  events,
		"checkpoint_count": len(cps),
		"event_count":    len(events),
	})
}
