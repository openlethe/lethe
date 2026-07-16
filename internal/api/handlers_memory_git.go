package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

// memoryGitErrorStatus maps store errors to HTTP statuses. Lock contention
// that survived the bounded retry policy is an infrastructure condition
// (503), and an idempotency key replayed with a different request is a
// conflict (409), not a client format error.
func memoryGitErrorStatus(err error) int {
	switch {
	case db.IsBusyError(err):
		return http.StatusServiceUnavailable
	case errors.Is(err, db.ErrRefCASConflict), errors.Is(err, db.ErrIdempotencyMismatch),
		errors.Is(err, db.ErrIdempotencyConflict):
		return http.StatusConflict
	case errors.Is(err, db.ErrProtectedRef):
		return http.StatusForbidden
	case errors.Is(err, db.ErrChangesetNotFound), errors.Is(err, db.ErrRefNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// registerMemoryGitRoutes adds Memory Git V1 API endpoints under /api/memory.
func (s *Server) registerMemoryGitRoutes(api chi.Router) {
	api.Route("/memory", func(r chi.Router) {
		// Project-scoped routes.
		r.Route("/{project}", func(r chi.Router) {
			r.Post("/legacy-root", s.handleEnsureLegacyRoot)
			r.Post("/branches", s.handleCreateBranch)
			r.Get("/refs", s.handleListRefs)
			r.Get("/refs/{ref}", s.handleGetRef)
			r.Get("/refs/resolve", s.handleGetRef)
			r.Post("/refs/{ref}/advance", s.handleCASAdvanceRef)
			r.Post("/refs/advance", s.handleCASAdvanceRef)
			r.Post("/refs/merge", s.handleMergeAdvanceRef)
			r.Get("/changesets", s.handleListChangesets)
			r.Get("/context", s.handleGetMemoryContext)
			r.Post("/context", s.handleCreateMemoryContext)
			r.Post("/conflicts/detect", s.handleDetectConflicts)
			r.Get("/conflicts", s.handleListConflicts)
			r.Post("/conflicts/persist", s.handlePersistConflicts)
			r.Post("/conflicts/retire", s.handleRetireConflicts)
			r.Post("/conflicts/{id}/resolve", s.handleResolveConflict)
		})
		// Changeset by ID (global lookup, project check enforced by DB).
		r.Get("/changesets/{id}", s.handleGetChangeset)
		r.Post("/changesets/{id}/diff", s.handleDiffChangeset)
		// Changeset creation.
		r.Post("/changesets", s.handleCreateChangeset)
		// Manifests.
		r.Post("/manifests", s.handleCreateManifest)
		r.Get("/manifests/{id}", s.handleGetManifest)
	})
}

// --- Legacy root ---

func (s *Server) handleEnsureLegacyRoot(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	root, ref, err := s.store.EnsureLegacyRoot(r.Context(), project, "system")
	if err != nil {
		writeJSON(w, memoryGitErrorStatus(err), ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changeset": root,
		"ref":       ref,
	})
}

// --- Changeset ---

func (s *Server) handleCreateChangeset(w http.ResponseWriter, r *http.Request) {
	var req db.CreateChangesetRequest
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	cs, err := s.store.CreateChangeset(r.Context(), req)
	if err != nil {
		status := memoryGitErrorStatus(err)
		if errors.Is(err, db.ErrEmptyOps) || errors.Is(err, db.ErrInvalidSemanticOp) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, cs)
}

func (s *Server) handleGetChangeset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cs, err := s.store.GetChangeset(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrChangesetNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleListChangesets(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	refName := r.URL.Query().Get("ref")
	limit := 20
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 200 {
		limit = n
	}
	log, err := s.store.ListChangesets(r.Context(), project, refName, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changesets": log,
		"project":    project,
		"ref":        refName,
	})
}

// --- Branch ---

func (s *Server) handleCreateBranch(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	var req struct {
		RefName         string `json:"ref_name"`
		HeadChangesetID string `json:"head_changeset_id"`
		Principal       string `json:"principal"`
		Protected       bool   `json:"protected,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	ref, err := s.store.CreateMemoryBranch(r.Context(), project, req.RefName, req.HeadChangesetID, req.Principal, req.Protected)
	if err != nil {
		status := http.StatusBadRequest
		if db.IsBusyError(err) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

// --- Ref ---

func (s *Server) handleGetRef(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	refName := chi.URLParam(r, "ref")
	if refName == "" || refName == "resolve" {
		refName = r.URL.Query().Get("name")
	}
	if refName == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "ref name required"})
		return
	}
	ref, err := s.store.GetMemoryRef(r.Context(), project, refName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if ref == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "ref not found"})
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

func (s *Server) handleListRefs(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	refs, err := s.store.ListMemoryRefs(r.Context(), project)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, refs)
}

func (s *Server) handleCASAdvanceRef(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	refName := chi.URLParam(r, "ref")
	var req struct {
		RefName      string `json:"ref_name"`
		ExpectedHead string `json:"expected_head"`
		NewHead      string `json:"new_head"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if refName == "" {
		refName = req.RefName
	}
	if refName == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "ref_name required"})
		return
	}
	existingRef, err := s.store.GetMemoryRef(r.Context(), project, refName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if models.IsProtectedRef(refName) || existingRef != nil && existingRef.Protected {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "protected ref requires merge path"})
		return
	}
	ref, err := s.store.CASUpdateRef(r.Context(), project, refName, req.ExpectedHead, req.NewHead)
	if err != nil {
		status := memoryGitErrorStatus(err)
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

func (s *Server) handleMergeAdvanceRef(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	var req struct {
		RefName           string `json:"ref_name"`
		ExpectedHead      string `json:"expected_head"`
		NewHead           string `json:"new_head"`
		MergeProposalID   string `json:"merge_proposal_id"`
		ReviewerPrincipal string `json:"reviewer_principal"`
		Authorization     string `json:"merge_authorization"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	existingRef, err := s.store.GetMemoryRef(r.Context(), project, req.RefName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if existingRef == nil || !existingRef.Protected || req.MergeProposalID == "" || req.ReviewerPrincipal == "" || req.Authorization == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "protected ref and signed merge authorization required"})
		return
	}
	// Every authorization is a versioned, expiring, single-use envelope bound
	// to the exact merge fields, the proposal state digest, and the reviewer.
	envelope, err := verifyMergeAuthorizationV2(s.mergeKeys, project, req.RefName,
		req.ExpectedHead, req.NewHead, req.MergeProposalID, req.ReviewerPrincipal, req.Authorization, time.Now().UTC())
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, errAuthorizationExpired) || errors.Is(err, errAuthorizationMalformed) || errors.Is(err, errAuthorizationFields) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, ErrorResponse{Error: "invalid merge authorization"})
		return
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, envelope.ExpiresAt)
	ref, err := s.store.CASMergeProtectedRefAuthorized(r.Context(), db.MergeAdvancement{
		ProjectID:         project,
		RefName:           req.RefName,
		ExpectedHead:      req.ExpectedHead,
		NewHead:           req.NewHead,
		ProposalID:        envelope.ProposalID,
		ProposalDigest:    envelope.ProposalDigest,
		ReviewerPrincipal: envelope.ReviewerPrincipal,
		MergerPrincipal:   envelope.MergerPrincipal,
		Strategy:          envelope.Strategy,
		Nonce:             envelope.Nonce,
		KeyID:             envelope.KeyID,
		ExpiresAt:         expiresAt,
	})
	if err != nil {
		status := memoryGitErrorStatus(err)
		if errors.Is(err, db.ErrAuthorizationReplay) || errors.Is(err, db.ErrMergeShape) || errors.Is(err, db.ErrInvalidStrategy) {
			status = http.StatusConflict
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

func (s *Server) handleDetectConflicts(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	var req struct {
		BaseChangesetID  string `json:"base_changeset_id"`
		LeftChangesetID  string `json:"left_changeset_id"`
		RightChangesetID string `json:"right_changeset_id"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	// Conflict analysis is pure: it never writes. Conflicts are persisted only
	// by an explicit proposal operation (see handlePersistConflicts), so
	// detection retries are always side-effect-free.
	conflicts, err := db.NewConflictDetector(s.store).DetectBetween(
		r.Context(), project, req.BaseChangesetID, req.LeftChangesetID, req.RightChangesetID,
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

// --- Conflict lifecycle ---

func (s *Server) handlePersistConflicts(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	var req struct {
		ProposalID string                   `json:"proposal_id"`
		Conflicts  []*models.MemoryConflict `json:"conflicts"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if req.ProposalID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "proposal_id required"})
		return
	}
	if err := s.store.PersistConflicts(r.Context(), project, req.ProposalID, req.Conflicts); err != nil {
		status := http.StatusBadRequest
		if db.IsBusyError(err) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"persisted": len(req.Conflicts), "proposal_id": req.ProposalID})
}

func (s *Server) handleResolveConflict(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	conflictID := chi.URLParam(r, "id")
	var req struct {
		ResolutionNote string `json:"resolution_note"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if err := s.store.ResolveConflict(r.Context(), project, conflictID, req.ResolutionNote); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, db.ErrConflictNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": conflictID})
}

func (s *Server) handleRetireConflicts(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	var req struct {
		ProposalID string `json:"proposal_id"`
		Status     string `json:"status"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	retired, err := s.store.RetireConflictsForProposal(r.Context(), project, req.ProposalID, req.Status)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retired": retired, "proposal_id": req.ProposalID, "status": req.Status})
}

func (s *Server) handleListConflicts(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	status := r.URL.Query().Get("status")
	limit := 100
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 500 {
		limit = n
	}
	conflicts, err := s.store.ListConflicts(r.Context(), project, status, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts, "count": len(conflicts)})
}

// --- Accepted context projection ---

type memoryContextRequest struct {
	RefName         string `json:"ref_name,omitempty"`
	HeadChangesetID string `json:"head_changeset_id,omitempty"`
	Query           string `json:"query,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	ActorID         string `json:"actor_id,omitempty"`
	CreateManifest  bool   `json:"create_manifest,omitempty"`
}

func (s *Server) handleGetMemoryContext(w http.ResponseWriter, r *http.Request) {
	req := memoryContextRequest{
		RefName:         r.URL.Query().Get("ref"),
		HeadChangesetID: r.URL.Query().Get("head"),
		Query:           r.URL.Query().Get("query"),
	}
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 {
		req.Limit = n
	}
	s.serveMemoryContext(w, r, req)
}

func (s *Server) handleCreateMemoryContext(w http.ResponseWriter, r *http.Request) {
	var req memoryContextRequest
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	s.serveMemoryContext(w, r, req)
}

func (s *Server) serveMemoryContext(w http.ResponseWriter, r *http.Request, req memoryContextRequest) {
	project := chi.URLParam(r, "project")
	if req.RefName == "" {
		req.RefName = models.RefSharedMain
	}
	view, err := s.store.BuildMemoryContext(
		r.Context(), project, req.RefName, req.HeadChangesetID, req.Query, req.Limit,
	)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, db.ErrRefNotFound) || errors.Is(err, db.ErrChangesetNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}

	if req.CreateManifest {
		// Git mode has no legacy session API, so Charon's MCP session ID is the
		// canonical attribution value. Hybrid mode retains legacy lookup and
		// project validation for OpenLethe session keys and IDs.
		sessionID := req.SessionID
		if req.SessionID != "" && s.mode.LegacyEnabled() {
			session, err := s.resolveSession(r.Context(), req.SessionID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
				return
			}
			if session == nil {
				writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
				return
			}
			if session.ProjectID != project {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "session project mismatch"})
				return
			}
			sessionID = session.SessionID
		}

		selectedIDs := make([]string, 0, len(view.Memories))
		for _, memory := range view.Memories {
			selectedIDs = append(selectedIDs, memory.MemoryID)
		}
		manifest := &models.MemoryManifest{
			Direction:           "input",
			ProjectID:           project,
			RefName:             view.RefName,
			HeadChangesetID:     view.HeadChangesetID,
			SelectedMemoryIDs:   selectedIDs,
			InclusionReasons:    view.InclusionReasons,
			ExclusionReasons:    view.ExclusionReasons,
			UnresolvedConflicts: view.UnresolvedConflicts,
			SessionID:           sessionID,
			ActorID:             req.ActorID,
		}
		if err := s.store.CreateManifest(r.Context(), manifest); err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		view.ManifestID = manifest.ManifestID
	}

	writeJSON(w, http.StatusOK, view)
}

// --- Diff ---

func (s *Server) handleDiffChangeset(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		BaseID string `json:"base_changeset_id,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	// Determine project from changeset.
	cs, err := s.store.GetChangeset(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrChangesetNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	diff, err := s.store.DiffChangesets(r.Context(), cs.ProjectID, req.BaseID, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// --- Manifest ---

func (s *Server) handleCreateManifest(w http.ResponseWriter, r *http.Request) {
	var m models.MemoryManifest
	if err := readJSON(w, r, &m); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	if m.SessionID != "" && s.mode.LegacyEnabled() {
		session, err := s.resolveSession(r.Context(), m.SessionID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		if session == nil {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "session not found"})
			return
		}
		if session.ProjectID != m.ProjectID {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "session project mismatch"})
			return
		}
		m.SessionID = session.SessionID
	}
	if err := s.store.CreateManifest(r.Context(), &m); err != nil {
		status := http.StatusBadRequest
		if db.IsBusyError(err) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleGetManifest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.store.GetManifest(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if m == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "manifest not found"})
		return
	}
	writeJSON(w, http.StatusOK, m)
}
