package ui

// Memory Git browser — a repository-style UI over the versioned memory store.
// Mounted in git/hybrid mode where the legacy session dashboard is absent.
// Pages mirror repository concepts: Memories (tree), Changesets (commits),
// Refs (branches). Proposals live in Charon's policy database, so this
// surface shows Lethe-side truth: accepted memory, history, and refs.

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// SetupMemoryRoutes registers the Memory Git browser on the root mux under
// /ui. Registered as literal routes (not a sub-mount) so it can coexist with
// the legacy UI sub-router in hybrid mode. rootRedirect controls whether
// /ui redirects to the memory home (git-only mode); in hybrid mode the
// legacy dashboard owns /ui and the rail switcher links here instead.
func SetupMemoryRoutes(r *chi.Mux, baseURL string, rootRedirect bool, middleware ...func(http.Handler) http.Handler) {
	apiBase = baseURL
	var routes chi.Router = r
	if len(middleware) > 0 {
		routes = r.With(middleware...)
	}
	if rootRedirect {
		routes.Get("/ui", redirectTo("/ui/memory"))
	}
	routes.Get("/ui/memory", handleMemoryHome)
	routes.Get("/ui/memory/memories", handleMemoryMemories)
	routes.Get("/ui/memory/changesets", handleMemoryChangesets)
	routes.Get("/ui/memory/changesets/{id}", handleMemoryChangesetDetail)
	routes.Get("/ui/memory/refs", handleMemoryRefs)
}

// memoryProject resolves the project scope. Fresh deployments use the
// conventional "default" project; any project can be typed in the picker.
func memoryProject(r *http.Request) string {
	if p := strings.TrimSpace(r.URL.Query().Get("project")); p != "" {
		return p
	}
	return "default"
}

func memoryRef(r *http.Request) string {
	if ref := strings.TrimSpace(r.URL.Query().Get("ref")); ref != "" {
		return ref
	}
	return "refs/shared/main"
}

// memoryChrome gathers the repo-bar data every memory page needs: the ref
// list for the branch dropdown, the changeset log for counts/latest, the
// context head for the identity strip, and the project list for the project
// dropdown. The context map is always non-nil so templates never see
// "<no value>" placeholders. ctxOK reports whether the context fetch actually
// succeeded — integrity, protection, and projection claims may only render
// when it is true; failures must read as unverified, never as verified.
// The changeset log is fetched with an explicit limit so a truncated page is
// never mistaken for complete history: maybeMore is true when the page is
// full, meaning history likely continues beyond what was loaded.
func memoryChrome(r *http.Request, project, ref string) (refs []interface{}, changesets []interface{}, ctx map[string]interface{}, projects []interface{}, ctxOK bool, maybeMore bool) {
	refs, _ = httpGetJSON[[]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/refs")
	res, err := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/changesets?ref="+url.QueryEscape(ref)+"&limit=200")
	if err == nil && res != nil {
		if c, ok := res["changesets"].([]interface{}); ok {
			changesets = c
		}
	}
	maybeMore = len(changesets) == 200
	ctx, err = httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/context?ref="+url.QueryEscape(ref)+"&limit=1")
	ctxOK = err == nil && ctx != nil
	if ctx == nil {
		ctx = map[string]interface{}{"head_changeset_id": "", "total_active": 0}
	}
	pres, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/projects")
	if pres != nil {
		if p, ok := pres["projects"].([]interface{}); ok {
			projects = p
		}
	}
	return refs, changesets, ctx, projects, ctxOK, maybeMore
}

// handleMemoryHome renders the Treehouse flagship: folder rails, file rows,
// LETHE.md, and a sidebar of integrity, composition, topics, and principals.
func handleMemoryHome(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, changesets, _, projects, ctxOK, maybeMore := memoryChrome(r, project, ref)
	ctxRes, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/context?ref="+url.QueryEscape(ref)+"&limit=200")
	if ctxRes == nil {
		ctxRes = map[string]interface{}{"head_changeset_id": "", "total_active": 0}
	}
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
		"projects":    projects,
		"ctxOK":       ctxOK,
		"maybeMore":   maybeMore,
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
	refs, changesets, _, projects, ctxOK, maybeMore := memoryChrome(r, project, ref)
	ctxRes, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/"+url.PathEscape(project)+"/context?ref="+url.QueryEscape(ref)+"&limit=200")
	if ctxRes == nil {
		ctxRes = map[string]interface{}{"head_changeset_id": "", "total_active": 0}
	}
	var memories []interface{}
	if ctxRes != nil {
		if m, ok := ctxRes["memories"].([]interface{}); ok {
			memories = m
		}
	}

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q != "" {
		var filtered []interface{}
		for _, m := range memories {
			mm, _ := m.(map[string]interface{})
			content, _ := mm["content"].(string)
			hay := strings.ToLower(content + " " + strings.Join(toStrings(mm["tags"]), " ") + " " + fmt.Sprint(mm["memory_id"]))
			if strings.Contains(hay, q) {
				filtered = append(filtered, m)
			}
		}
		memories = filtered
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
		"projects":   projects,
		"ctxOK":      ctxOK,
		"maybeMore":  maybeMore,
		"groups":     groups,
		"q":          r.URL.Query().Get("q"),
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

// handleMemoryChangesets renders the commit log reachable from a ref, split
// into the ref's own segment and the shared ancestry it descends from.
func handleMemoryChangesets(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, changesets, ctx, projects, ctxOK, maybeMore := memoryChrome(r, project, ref)
	var own, ancestry []interface{}
	for _, c := range changesets {
		cm, _ := c.(map[string]interface{})
		if cm["ref_name"] == ref {
			own = append(own, c)
		} else {
			ancestry = append(ancestry, c)
		}
	}
	Render(w, r, "memory_changesets", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"own":        own,
		"ancestry":   ancestry,
		"ctx":        ctx,
		"projects":   projects,
		"ctxOK":      ctxOK,
		"maybeMore":  maybeMore,
		"page":       "changesets",
	})
}

// handleMemoryChangesetDetail renders one changeset as a commit view: header
// plus each semantic operation as a diff-style card.
func handleMemoryChangesetDetail(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	id := chi.URLParam(r, "id")
	cs, _ := httpGetJSON[map[string]interface{}](r.Context(), authTokenFromRequest(r),
		apiBase+"/api/memory/changesets/"+url.PathEscape(id))
	if cs == nil {
		http.Error(w, "changeset not found", http.StatusNotFound)
		return
	}
	refs, changesets, ctx, projects, ctxOK, maybeMore := memoryChrome(r, project, ref)
	Render(w, r, "memory_changeset_detail", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"ctx":        ctx,
		"projects":   projects,
		"ctxOK":      ctxOK,
		"maybeMore":  maybeMore,
		"cs":         cs,
		"page":       "changesets",
	})
}

// handleMemoryRefs renders the branch list with heads and protection status.
func handleMemoryRefs(w http.ResponseWriter, r *http.Request) {
	project := memoryProject(r)
	ref := memoryRef(r)
	refs, changesets, ctx, projects, ctxOK, maybeMore := memoryChrome(r, project, ref)
	Render(w, r, "memory_refs", map[string]interface{}{
		"project":    project,
		"ref":        ref,
		"refs":       refs,
		"changesets": changesets,
		"ctx":        ctx,
		"projects":   projects,
		"ctxOK":      ctxOK,
		"maybeMore":  maybeMore,
		"page":       "refs",
	})
}
