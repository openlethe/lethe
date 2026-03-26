package ui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templatesFS embed.FS

var templates *template.Template

func init() {
	funcMap := template.FuncMap{
		"since": func(v interface{}) string {
			var t time.Time
			switch x := v.(type) {
			case time.Time:
				t = x
			case string:
				var err error
				t, err = time.Parse(time.RFC3339, x)
				if err != nil {
					return x
				}
			}
			d := time.Since(t)
			if d < time.Minute {
				return fmt.Sprintf("%.0fs", d.Seconds())
			}
			if d < time.Hour {
				return fmt.Sprintf("%.0fm", d.Minutes())
			}
			if d < 24*time.Hour {
				return fmt.Sprintf("%.0fh", d.Hours())
			}
			return fmt.Sprintf("%.0fd", d.Hours()/24)
		},
		"mul": func(a, b float64) float64 { return a * b },
		"slice": func(s string, start, end int) string {
			if start < 0 {
				start = 0
			}
			runes := []rune(s)
			if start >= len(runes) {
				return ""
			}
			if end > len(runes) {
				end = len(runes)
			}
			return string(runes[start:end])
		},
	}

	// Parse base first so named templates are registered, then page templates.
	// Each page template defines title/content blocks that base's layout references.
	base, err := template.ParseFS(templatesFS, "templates/base")
	if err != nil {
		panic("parse base: " + err.Error())
	}
	base.Funcs(funcMap)
	base, err = base.Clone()
	if err != nil {
		panic("clone base: " + err.Error())
	}
	templates = base
	// Parse page templates - they define title and content blocks, no template calls.
	// File order matters: later files can overwrite title blocks, so parse in
	// alphabetical order so the Render call can execute the specific template.
	_, err = templates.ParseFS(templatesFS,
		"templates/dashboard",
		"templates/flags",
		"templates/live",
		"templates/session_checkpoints",
		"templates/session_detail",
		"templates/session_events",
		"templates/sessions",
	)
	if err != nil {
		panic("parse templates: " + err.Error())
	}
	_, err = templates.ParseFS(templatesFS, "templates/fragments/*")
	if err != nil {
		panic("parse fragments: " + err.Error())
	}
}

// StaticFS returns the static filesystem for use in embedding.
func StaticFS() embed.FS { return staticFS }

// UseSubFS makes static files accessible under /static/ prefix.
func UseSubFS(fsys embed.FS, prefix string) (http.FileSystem, error) {
	dir, err := fs.Sub(fsys, prefix)
	if err != nil {
		return nil, err
	}
	return http.FS(dir), nil
}

// RenderData is the data passed to the layout template.
type RenderData struct {
	Title        string
	Content      string
	Layout       string // extra CSS class for the page wrapper (e.g. "page-with-sidebar")
	Data         interface{}
	Request      *http.Request
	CurrentRoute string
}

type renderOption func(*RenderData)

func withLayout(layout string) renderOption {
	return func(rd *RenderData) { rd.Layout = layout }
}

// RenderWithLayout renders a page with a specific layout class (e.g. "page-with-sidebar").
func RenderWithLayout(w http.ResponseWriter, r *http.Request, name string, layout string, data interface{}) {
	Render(w, r, name, data, withLayout(layout))
}

// RenderWithData renders a page with custom page data (for pre-populated pages like session detail).
func RenderWithData(w http.ResponseWriter, r *http.Request, name string, data interface{}) {
	Render(w, r, name, data)
}

// Render renders a template with the given name and data.
func Render(w http.ResponseWriter, r *http.Request, name string, data interface{}, opts ...renderOption) {
	// Page titles derived from template name to avoid {{define}} conflicts.
	var title string
	switch name {
	case "dashboard":
		title = "Dashboard"
	case "sessions":
		title = "Sessions"
	case "session_detail":
		title = "Session Detail"
	case "session_events":
		title = "Session Events"
	case "session_checkpoints":
		title = "Session Checkpoints"
	case "flags":
		title = "Flags"
	case "live":
		title = "Live"
	default:
		title = "Lethe"
	}

	type RenderData struct {
		Title        string
		Content      string
		Layout       string // optional extra CSS class for the page wrapper
		Data         interface{}
		Request      *http.Request
		CurrentRoute string
	}

	// Pre-render page content to a string so each page is independent.
	// This avoids Go's flat {{define}} namespace conflict where later
	// parsed templates overwrite earlier ones' blocks.
	var pageContent string
	if name != "layout" {
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
			// If the named template doesn't exist, use empty content
			log.Printf("Render(%q) page template error: %v", name, err)
			pageContent = ""
		} else {
			pageContent = buf.String()
		}
	}

	rd := RenderData{
		Title:        title + " — Lethe",
		Content:      pageContent,
		Data:         data,
		Request:      r,
		CurrentRoute: name,
		Layout:       "",
	}
	if err := templates.ExecuteTemplate(w, "layout", rd); err != nil {
		log.Printf("Render(%q) layout error: %v", name, err)
		http.Error(w, err.Error(), 500)
	}
}

// SetupRoutes mounts the UI routes on a chi router under /ui.
// Uses a dedicated sub-router so UI routes don't collide with API routes
// which are mounted at /api on the same root mux.
func SetupRoutes(r *chi.Mux) {
	ui := chi.NewRouter()
	ui.Get("/", redirectTo("/ui/dashboard"))
	ui.Get("/dashboard", handleDashboard)
	ui.Get("/sessions", handleSessions)
	ui.Get("/sessions/{sessionID}", handleSessionDetail)
	ui.Get("/sessions/{sessionID}/events", handleSessionEvents)
	ui.Get("/sessions/{sessionID}/checkpoints", handleSessionCheckpoints)
	ui.Get("/session/{sessionID}/data", handleSessionDetailData)
	ui.Get("/session/{sessionID}/events-data", handleSessionEventsData)
	ui.Get("/session/{sessionID}/checkpoints-data", handleSessionCheckpointsData)
	ui.Get("/flags", handleFlags)
	ui.Get("/live", handleLive)
	// HTMX data endpoints — return rendered HTML fragments
	ui.Get("/sessions-data", handleSessionsData)
	ui.Get("/flags-data", handleFlagsData)
	ui.Get("/debug/templates", func(w http.ResponseWriter, r *http.Request) {
		var names []string
		for _, t := range templates.Templates() {
			names = append(names, t.Name())
		}
		fmt.Fprintf(w, "Templates: %v\n", names)
	})
	r.Mount("/ui", ui)
}

// httpGetJSON fetches a JSON resource and returns the parsed map.
func httpGetJSON[T map[string]int | map[string]interface{}](ctx context.Context, url string) (T, error) {
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			ch <- result{err: err}
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer resp.Body.Close()
		var val T
		if err := json.NewDecoder(resp.Body).Decode(&val); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{val: val}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.val, r.err
	}
}

func redirectTo(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, path, http.StatusMovedPermanently)
	}
}

// --- Page handlers ---

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/stats")
	if err != nil || stats == nil {
		stats = map[string]interface{}{"sessions": 0, "events": 0, "checkpoints": 0, "flags": 0}
	}

	// Find the most recent active session for the live token meter.
	var currentSessionKey string
	var currentTokenBudget int
	activeSessions, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions?limit=10")
	if err == nil {
		if sessions, ok := activeSessions["sessions"].([]interface{}); ok {
			for _, s := range sessions {
				if sm, ok := s.(map[string]interface{}); ok {
					if state, ok := sm["state"].(string); ok && state == "active" {
						currentSessionKey, _ = sm["session_key"].(string)
						if tb, ok := sm["token_budget"].(float64); ok {
							currentTokenBudget = int(tb)
						}
						break
					}
				}
			}
		}
	}

	Render(w, r, "dashboard", map[string]interface{}{
		"stats":              stats,
		"currentSessionKey":   currentSessionKey,
		"currentTokenBudget": currentTokenBudget,
	})
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "sessions", nil)
}

func handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")

	// Pre-fetch all session data so the page is fully rendered on load.
	// HTMX will still update events/checkpoints fragments dynamically.
	sessData, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID)
	if err != nil || sessData == nil {
		sessData = map[string]interface{}{}
	}

	eventsResult, _ := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID+"/events?limit=50")
	var events []map[string]interface{}
	if eventsResult != nil {
		if e, ok := eventsResult["events"].([]interface{}); ok {
			for _, v := range e {
				if m, ok := v.(map[string]interface{}); ok {
					events = append(events, m)
				}
			}
		}
	}

	cpResult, _ := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID+"/checkpoints")
	var checkpoints []map[string]interface{}
	if cpResult != nil {
		if c, ok := cpResult["checkpoints"].([]interface{}); ok {
			for _, v := range c {
				if m, ok := v.(map[string]interface{}); ok {
					checkpoints = append(checkpoints, m)
				}
			}
		}
	}

	pageData := map[string]interface{}{
		"session":    sessData,
		"events":     events,
		"checkpoints": checkpoints,
	}

	RenderWithLayout(w, r, "session_detail", "page-with-sidebar", pageData)
}

func handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "session_events", nil)
}

func handleSessionCheckpoints(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "session_checkpoints", nil)
}

// handleSessionDetailData returns session data fragment for HTMX.
func handleSessionDetailData(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	data, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID)
	if err != nil || data == nil {
		data = map[string]interface{}{}
	}
	data["session_id"] = sessionID
	// Render fragment directly — no Render() wrapper
	if err := templates.ExecuteTemplate(w, "session_detail_data", data); err != nil {
		log.Printf("session_detail_data error: %v", err)
	}
}

// handleSessionEventsData returns events list fragment for a session.
func handleSessionEventsData(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	result, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID+"/events?limit=50")
	var events []map[string]interface{}
	if err == nil {
		if e, ok := result["events"].([]interface{}); ok {
			for _, v := range e {
				if m, ok := v.(map[string]interface{}); ok {
					events = append(events, m)
				}
			}
		}
	}
	if events == nil {
		events = []map[string]interface{}{}
	}
	data := map[string]interface{}{"items": events}
	if err := templates.ExecuteTemplate(w, "events_list", data); err != nil {
		log.Printf("events_list error: %v", err)
	}
}

// handleSessionCheckpointsData returns checkpoints list fragment for a session.
func handleSessionCheckpointsData(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	result, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions/"+sessionID+"/checkpoints")
	var checkpoints []map[string]interface{}
	if err == nil {
		if c, ok := result["checkpoints"].([]interface{}); ok {
			for _, v := range c {
				if m, ok := v.(map[string]interface{}); ok {
					checkpoints = append(checkpoints, m)
				}
			}
		}
	}
	if checkpoints == nil {
		checkpoints = []map[string]interface{}{}
	}
	data := map[string]interface{}{"checkpoints": checkpoints}
	if err := templates.ExecuteTemplate(w, "checkpoints_list", data); err != nil {
		log.Printf("checkpoints_list error: %v", err)
	}
}

func handleFlags(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "flags", nil)
}

func handleLive(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "live", nil)
}

// handleSessionsData returns the sessions list fragment for HTMX.
func handleSessionsData(w http.ResponseWriter, r *http.Request) {
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "5"
	}
	var sessions []map[string]interface{}
	stats, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/sessions?limit="+limit)
	if err == nil {
		if s, ok := stats["sessions"].([]interface{}); ok {
			for _, v := range s {
				if m, ok := v.(map[string]interface{}); ok {
					sessions = append(sessions, m)
				}
			}
		}
	}
	if sessions == nil {
		sessions = []map[string]interface{}{}
	}
	data := map[string]interface{}{"sessions": sessions}
	if err := templates.ExecuteTemplate(w, "sessions_list", data); err != nil {
		log.Printf("sessions_list error: %v", err)
	}
}

// handleFlagsData returns the flags list fragment for HTMX.
func handleFlagsData(w http.ResponseWriter, r *http.Request) {
	var flags []map[string]interface{}
	result, err := httpGetJSON[map[string]interface{}](r.Context(), "http://127.0.0.1:8080/api/flags?limit=5")
	if err == nil {
		if f, ok := result["flags"].([]interface{}); ok {
			for _, v := range f {
				if m, ok := v.(map[string]interface{}); ok {
					flags = append(flags, m)
				}
			}
		}
	}
	if flags == nil {
		flags = []map[string]interface{}{}
	}
	data := map[string]interface{}{"flags": flags}
	if err := templates.ExecuteTemplate(w, "flags_list", data); err != nil {
		log.Printf("flags_list error: %v", err)
	}
}

// APIProxy proxies API calls to the Lethe server.
// Handlers in this package are mounted at /*, so /api is forwarded to the API server.
func APIProxy(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("%s%s", target, r.URL.Path)
		req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header = r.Header.Clone()
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		fmt.Fprint(w, resp.Body)
	}
}
