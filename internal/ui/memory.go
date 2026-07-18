package ui

// Memory Git browser — a repository-style UI over the versioned memory store.
// Mounted in git/hybrid mode where the legacy session dashboard is absent.
// Pages mirror repository concepts: Memories (tree), Changesets (commits),
// Refs (branches). Proposals live in Charon's policy database, so this
// surface shows Lethe-side truth: accepted memory, history, and refs.

import (
	"net/http"
	"net/url"
	"sort"
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

func memoryRef(r *http.Request) string {
	if ref := strings.TrimSpace(r.URL.Query().Get("ref")); ref != "" {
		return ref
	}
	return "refs/shared/main"
}

// memoryChrome gathers the repo-bar data every memory page needs: the ref
// list for the branch dropdown and the changeset log for counts/latest.
func memoryChrome(r *http.Request, project, ref string) (refs []interface{}, changesets []interface{}) {
	refs, _ = httpGetJSON[[]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/refs")
	res, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/changesets?ref="+url.QueryEscape(ref))
	if res != nil {
		if c, ok := res["changesets"].([]interface{}); ok {
			changesets = c
		}
	}
	return refs, changesets
}

// handleMemoryHome renders accepted memory at a ref head — the repository
// "tree" view, defaulting to the protected shared ref.
func handleMemoryHome(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, changesets := memoryChrome(r, project, ref)
	ctxRes, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/context?ref="+url.QueryEscape(ref)+"&limit=200")
	var memories []interface{}
	if ctxRes != nil {
		if m, ok := ctxRes["memories"].([]interface{}); ok {
			memories = m
		}
	}
	tagSet := map[string]bool{}
	var tags []string
	for _, m := range memories {
		mm, _ := m.(map[string]interface{})
		for _, t := range toStrings(mm["tags"]) {
			if !tagSet[t] {
				tagSet[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(tags)
	Render(w, r, "memory_home", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"ctx":        ctxRes,
		"memories":   memories,
		"tags":       tags,
		"page":       "memory",
	})
}

// toStrings coerces a decoded JSON array into strings.
func toStrings(v interface{}) []string {
	var out []string
	if arr, ok := v.([]interface{}); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// handleMemoryChangesets renders the commit log reachable from a ref.
func handleMemoryChangesets(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, changesets := memoryChrome(r, project, ref)
	Render(w, r, "memory_changesets", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"page":       "changesets",
	})
}

// handleMemoryChangesetDetail renders one changeset as a commit view: header
// plus each semantic operation as a diff-style card.
func handleMemoryChangesetDetail(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	id := chi.URLParam(r, "id")
	cs, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/changesets/"+url.PathEscape(id))
	if cs == nil {
		http.Error(w, "changeset not found", http.StatusNotFound)
		return
	}
	refs, _ := httpGetJSON[[]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/refs")
	Render(w, r, "memory_changeset_detail", map[string]interface{}{
		"project": project,
		"refs":    refs,
		"cs":      cs,
		"page":    "changesets",
	})
}

// handleMemoryRefs renders the branch list with heads and protection status.
func handleMemoryRefs(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, _ := httpGetJSON[[]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/refs")
	Render(w, r, "memory_refs", map[string]interface{}{
		"project": project,
		"ref":     ref,
		"refs":    refs,
		"page":    "refs",
	})
}
