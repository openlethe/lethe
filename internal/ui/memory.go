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
	routes.Get("/ui/memory/memories", handleMemoryMemories)
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

// handleMemoryHome renders the Treehouse flagship: folder rails, file rows,
// LETHE.md, and a sidebar of integrity, composition, topics, and principals.
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

	kindColors := map[string]string{
		"decision": "#96690a", "task": "#3a6ea8", "flag": "#c24334", "fact": "#17805a",
		"record": "#1288a5", "observation": "#5c6f64", "outcome": "#7a4fb8",
	}
	kindOrder := []string{"decision", "task", "flag", "fact", "record", "observation", "outcome"}
	counts := map[string]int{}
	tagSet := map[string]bool{}
	var tags []string
	for _, m := range memories {
		mm, _ := m.(map[string]interface{})
		kind, _ := mm["kind"].(string)
		if kind == "" {
			kind = "record"
		}
		counts[kind]++
		for _, t := range toStrings(mm["tags"]) {
			if !tagSet[t] {
				tagSet[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(tags)

	seen := map[string]bool{}
	var rest []string
	for kind := range counts {
		if !seen[kind] {
			rest = append(rest, kind)
		}
	}
	seen = map[string]bool{}
	var ordered []string
	for _, k := range kindOrder {
		if counts[k] > 0 {
			ordered = append(ordered, k)
			seen[k] = true
		}
	}
	var extra []string
	for kind := range counts {
		if !seen[kind] {
			extra = append(extra, kind)
		}
	}
	sort.Strings(extra)
	ordered = append(ordered, extra...)

	var composition []map[string]interface{}
	for _, kind := range ordered {
		color := kindColors[kind]
		if color == "" {
			color = "#8fa096"
		}
		pct := 0
		if len(memories) > 0 {
			pct = counts[kind] * 100 / len(memories)
		}
		composition = append(composition, map[string]interface{}{
			"kind": kind, "color": color, "count": counts[kind], "pct": pct,
		})
	}

	principalColors := []string{"#1288a5", "#3a6ea8", "#96690a", "#7a4fb8", "#17805a", "#c24334"}
	var principals []map[string]interface{}
	seenP := map[string]bool{}
	for _, c := range changesets {
		cm, _ := c.(map[string]interface{})
		author, _ := cm["author_principal"].(string)
		if author == "" || seenP[author] {
			continue
		}
		seenP[author] = true
		initials := author
		if len(author) > 2 {
			initials = author[len(author)-2:]
		}
		principals = append(principals, map[string]interface{}{
			"id": author, "initials": initials, "color": principalColors[(len(principals))%len(principalColors)],
		})
		if len(principals) >= 6 {
			break
		}
	}

	var latest map[string]interface{}
	if len(changesets) > 0 {
		latest, _ = changesets[0].(map[string]interface{})
	}
	var fileRows []interface{}
	if len(memories) > 4 {
		fileRows = memories[:4]
	} else {
		fileRows = memories
	}

	Render(w, r, "memory_home", map[string]interface{}{
		"project":     project,
		"ref":         ref,
		"refs":        refs,
		"changesets":  changesets,
		"ctx":         ctxRes,
		"latest":      latest,
		"fileRows":    fileRows,
		"composition": composition,
		"tags":        tags,
		"principals":  principals,
		"page":        "home",
	})
}

// handleMemoryMemories renders the Treehouse: accepted memory grouped by kind
// ("kinds as folders"), every record expandable.
func handleMemoryMemories(w http.ResponseWriter, r *http.Request) {
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

	kindColors := map[string]string{
		"decision": "#96690a", "task": "#3a6ea8", "flag": "#c24334", "fact": "#17805a",
		"record": "#1288a5", "observation": "#5c6f64", "outcome": "#7a4fb8",
	}
	icons := map[string]string{"decision": "◆", "task": "☑", "flag": "⚑", "fact": "✓", "record": "≡", "observation": "◉"}
	order := []string{"decision", "task", "flag", "fact", "record", "observation"}
	buckets := map[string][]interface{}{}
	for _, m := range memories {
		mm, _ := m.(map[string]interface{})
		kind, _ := mm["kind"].(string)
		if kind == "" {
			kind = "record"
		}
		buckets[kind] = append(buckets[kind], m)
	}
	var groups []map[string]interface{}
	seen := map[string]bool{}
	addGroup := func(kind string) {
		items := buckets[kind]
		if len(items) == 0 {
			return
		}
		icon := icons[kind]
		if icon == "" {
			icon = "≡"
		}
		color := kindColors[kind]
		if color == "" {
			color = "#8fa096"
		}
		groups = append(groups, map[string]interface{}{
			"kind": kind, "icon": icon, "color": color, "items": items,
		})
		seen[kind] = true
	}
	if only := strings.TrimSpace(r.URL.Query().Get("kind")); only != "" {
		addGroup(only)
	} else {
		for _, kind := range order {
			addGroup(kind)
		}
		var rest []string
		for kind := range buckets {
			if !seen[kind] {
				rest = append(rest, kind)
			}
		}
		sort.Strings(rest)
		for _, kind := range rest {
			addGroup(kind)
		}
	}

	Render(w, r, "memory_memories", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"ctx":        ctxRes,
		"groups":     groups,
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
