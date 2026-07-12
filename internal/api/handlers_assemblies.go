package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

// ---------------------------------------------------------------------------
// Assembly handlers
// ---------------------------------------------------------------------------

// handleCreateAssembly creates a context assembly record.
func (s *Server) handleCreateAssembly(w http.ResponseWriter, r *http.Request) {
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

	var req struct {
		AssemblyID                  string `json:"assembly_id"`
		Source                      string `json:"source"`
		PluginVersion               string `json:"plugin_version"`
		AssemblerVersion            string `json:"assembler_version"`
		MessageCount                int    `json:"message_count"`
		ProvidedTokenBudget         *int   `json:"provided_token_budget"`
		EstimatorID                 string `json:"estimator_id"`
		SummaryEstimatedTokens      *int   `json:"summary_estimated_tokens"`
		RecentEstimatedTokens       *int   `json:"recent_estimated_tokens"`
		ConversationEstimatedTokens *int   `json:"conversation_estimated_tokens"`
		TotalEstimatedTokens        *int   `json:"total_estimated_tokens"`
		PackedBytes                 int    `json:"packed_bytes"`
		RecentSkipped               bool   `json:"recent_skipped"`
		SkipReason                  string `json:"skip_reason"`
		Notes                       string `json:"notes"`
		Items                       []struct {
			Ordinal         int    `json:"ordinal"`
			ItemKind        string `json:"item_kind"`
			Bucket          string `json:"bucket"`
			EventID         string `json:"event_id"`
			ContentSnapshot string `json:"content_snapshot"`
			ContentSHA256   string `json:"content_sha256"`
			PackedBytes     int    `json:"packed_bytes"`
			EstimatedTokens *int   `json:"estimated_tokens"`
		} `json:"items"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if req.AssemblyID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "assembly_id is required"})
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "source is required"})
		return
	}
	if req.AssemblerVersion == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "assembler_version is required"})
		return
	}

	// Build assembly model.
	assembly := &models.ContextAssembly{
		AssemblyID:                  req.AssemblyID,
		SessionID:                   sess.SessionID,
		ProjectID:                   sess.ProjectID,
		Source:                      req.Source,
		PluginVersion:               req.PluginVersion,
		AssemblerVersion:            req.AssemblerVersion,
		MessageCount:                req.MessageCount,
		ProvidedTokenBudget:         req.ProvidedTokenBudget,
		EstimatorID:                 req.EstimatorID,
		SummaryEstimatedTokens:      req.SummaryEstimatedTokens,
		RecentEstimatedTokens:       req.RecentEstimatedTokens,
		ConversationEstimatedTokens: req.ConversationEstimatedTokens,
		TotalEstimatedTokens:        req.TotalEstimatedTokens,
		PackedBytes:                 req.PackedBytes,
		RecentSkipped:               req.RecentSkipped,
		SkipReason:                  req.SkipReason,
		Notes:                       req.Notes,
	}

	// Validate and convert items.
	seenEventIDs := make(map[string]bool)
	hasSummary := false
	for _, item := range req.Items {
		if item.ItemKind != "summary" && item.ItemKind != "event" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("invalid item_kind: %s", item.ItemKind)})
			return
		}
		if item.ItemKind == "summary" {
			if hasSummary {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "only one summary item allowed"})
				return
			}
			hasSummary = true
			if item.Ordinal != 0 {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "summary must be ordinal 0"})
				return
			}
		}
		if item.ItemKind == "event" {
			if item.EventID == "" {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "event_id required for event items"})
				return
			}
			if seenEventIDs[item.EventID] {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "duplicate event_id in assembly"})
				return
			}
			seenEventIDs[item.EventID] = true

			// Validate event belongs to session.
			event, err := s.store.GetEvent(r.Context(), item.EventID)
			if err != nil || event == nil {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "event not found: " + item.EventID})
				return
			}
			if event.SessionID == nil || *event.SessionID != sess.SessionID {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "event does not belong to session"})
				return
			}
		}
		assembly.Items = append(assembly.Items, models.ContextAssemblyItem{
			Ordinal:         item.Ordinal,
			ItemKind:        item.ItemKind,
			Bucket:          item.Bucket,
			EventID:         item.EventID,
			ContentSnapshot: item.ContentSnapshot,
			ContentSHA256:   item.ContentSHA256,
			PackedBytes:     item.PackedBytes,
			EstimatedTokens: item.EstimatedTokens,
		})
	}

	if err := s.store.CreateContextAssembly(r.Context(), assembly); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"assembly_id": req.AssemblyID,
		"recorded":    true,
	})
}

// handleListAssemblies lists assemblies for a session.
func (s *Server) handleListAssemblies(w http.ResponseWriter, r *http.Request) {
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

	limit := queryLimit(r, "limit", 20, 100)
	assemblies, err := s.store.ListContextAssemblies(r.Context(), sess.SessionID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"assemblies": assemblies,
		"limit":      limit,
	})
}

// handleGetAssembly returns a single assembly with items.
func (s *Server) handleGetAssembly(w http.ResponseWriter, r *http.Request) {
	assemblyID := chi.URLParam(r, "assemblyID")
	assembly, err := s.store.GetContextAssembly(r.Context(), assemblyID)
	if err != nil {
		if err == db.ErrAssemblyNotFound {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "assembly not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, assembly)
}

// handleCreateFeedback creates feedback on an assembly.
func (s *Server) handleCreateFeedback(w http.ResponseWriter, r *http.Request) {
	assemblyID := chi.URLParam(r, "assemblyID")
	var req struct {
		Verdict        string `json:"verdict"`
		RelatedEventID string `json:"related_event_id"`
		Note           string `json:"note"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	// Validate verdict.
	validVerdicts := map[string]bool{"good": true, "stale_included": true, "missing_memory": true, "too_large": true, "irrelevant": true, "other": true}
	if !validVerdicts[req.Verdict] {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid verdict"})
		return
	}

	// Validate assembly exists.
	assembly, err := s.store.GetContextAssembly(r.Context(), assemblyID)
	if err != nil {
		if err == db.ErrAssemblyNotFound {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "assembly not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// If related_event_id provided, validate it belongs to the same project.
	if req.RelatedEventID != "" {
		event, err := s.store.GetEvent(r.Context(), req.RelatedEventID)
		if err != nil || event == nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "related event not found"})
			return
		}
		if event.ProjectID != assembly.ProjectID {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "related event does not belong to assembly project"})
			return
		}
	}

	feedback := &models.ContextAssemblyFeedback{
		FeedbackID:     generateID(),
		AssemblyID:     assemblyID,
		Verdict:        req.Verdict,
		RelatedEventID: req.RelatedEventID,
		Note:           req.Note,
	}
	if err := s.store.CreateContextAssemblyFeedback(r.Context(), feedback); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, feedback)
}
