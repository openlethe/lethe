package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

// registerMemoryGitRoutes adds Memory Git V1 API endpoints under /api/memory.
func (s *Server) registerMemoryGitRoutes(api chi.Router) {
	api.Route("/memory", func(r chi.Router) {
		// Project-scoped routes.
		r.Route("/{project}", func(r chi.Router) {
			r.Post("/legacy-root", s.handleEnsureLegacyRoot)
			r.Post("/branches", s.handleCreateBranch)
			r.Get("/refs", s.handleListRefs)
			r.Get("/refs/{ref}", s.handleGetRef)
			r.Post("/refs/{ref}/advance", s.handleCASAdvanceRef)
			r.Get("/changesets", s.handleListChangesets)
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
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
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
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrRefCASConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, db.ErrIdempotencyConflict) || errors.Is(err, db.ErrEmptyOps) {
			status = http.StatusBadRequest
		} else if errors.Is(err, db.ErrProtectedRef) {
			status = http.StatusForbidden
		} else if errors.Is(err, db.ErrChangesetNotFound) || errors.Is(err, db.ErrRefNotFound) {
			status = http.StatusNotFound
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
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

// --- Ref ---

func (s *Server) handleGetRef(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	refName := chi.URLParam(r, "ref")
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
		ExpectedHead string `json:"expected_head"`
		NewHead      string `json:"new_head"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}
	ref, err := s.store.CASUpdateRef(r.Context(), project, refName, req.ExpectedHead, req.NewHead)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrRefCASConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, db.ErrRefNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ref)
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
	if err := s.store.CreateManifest(r.Context(), &m); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
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
