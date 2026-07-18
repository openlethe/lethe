package ui

// Memory Git browser — a repository-style UI over the versioned memory store.
// Mounted in git/hybrid mode where the legacy session dashboard is absent.
// Pages mirror repository concepts: Memories (tree), Changesets (commits),
// Refs (branches). Proposals live in Charon's policy database, so this
// surface shows Lethe-side truth: accepted memory, history, and refs.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// SetupMemoryRoutes registers the Memory Git browser on the root mux under
// /ui. Registered as literal routes (not a sub-mount) so it can coexist with
// the legacy UI sub-router in hybrid mode.
func SetupMemoryRoutes(r *chi.Mux, baseURL string, middleware ...func(http.Handler) http.Handler) {
	apiBase = baseURL
	var routes chi.Router = r
	if len(middleware) > 0 {
		routes = r.With(middleware...)
	}
	routes.Get("/ui", redirectTo("/ui/memory"))
	routes.Get("/ui/memory", handleMemoryHome)
	routes.Get("/ui/memory/changesets", handleMemoryChangesets)
	routes.Get("/ui/memory/changesets/{id}", handleMemoryChangesetDetail)
	routes.Get("/ui/memory/refs", handleMemoryRefs)
}

// memoryProject resolves the project scope. Prototype default; a projects
// list endpoint lands when this is ported from the playground.
func memoryProject(r *http.Request) string {
	if p := strings.TrimSpace(r.URL.Query().Get("project")); p != "" {
		return p
	}
	return "Archimedes"
}

// handleMemoryHome renders accepted memory at the head of refs/shared/main —
// the repository "tree" view.
func handleMemoryHome(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ctxRes, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/context?ref=refs/shared/main&limit=200")
	var memories []interface{}
	if ctxRes != nil {
		if m, ok := ctxRes["memories"].([]interface{}); ok {
			memories = m
		}
	}
	Render(w, r, "memory_home", map[string]interface{}{
		"project":  project,
		"ctx":      ctxRes,
		"memories": memories,
		"page":     "memory",
	})
}

// handleMemoryChangesets renders the commit log reachable from a ref
// (defaults to the protected shared ref).
func handleMemoryChangesets(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		ref = "refs/shared/main"
	}
	res, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/changesets?ref="+url.QueryEscape(ref))
	var changesets []interface{}
	if res != nil {
		if c, ok := res["changesets"].([]interface{}); ok {
			changesets = c
		}
	}
	Render(w, r, "memory_changesets", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"changesets": changesets,
		"page":       "changesets",
	})
}

// handleMemoryChangesetDetail renders one changeset as a commit view: header
// plus each semantic operation as a diff-style card.
func handleMemoryChangesetDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cs, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/changesets/"+url.PathEscape(id))
	if cs == nil {
		http.Error(w, "changeset not found", http.StatusNotFound)
		return
	}
	Render(w, r, "memory_changeset_detail", map[string]interface{}{
		"cs":   cs,
		"page": "changesets",
	})
}

// handleMemoryRefs renders the branch list with heads and protection status.
func handleMemoryRefs(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	refs, _ := httpGetJSON[[]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/refs")
	Render(w, r, "memory_refs", map[string]interface{}{
		"project": project,
		"refs":    refs,
		"page":    "refs",
	})
}
