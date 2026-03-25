package ui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"text/template"

	"github.com/go-chi/chi/v5"
)

//go:embed static/*
var staticFS embed.FS

//go:embed templates/*
var templatesFS embed.FS

var templates *template.Template

func init() {
	// Parse base first so named templates are registered, then page templates.
	base, err := template.ParseFS(templatesFS, "templates/base.html")
	if err != nil {
		panic("parse base: " + err.Error())
	}
	templates = template.Must(base.Clone())
	_, err = templates.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		panic("parse templates: " + err.Error())
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

// Render renders a template with the given name and data.
func Render(w http.ResponseWriter, r *http.Request, name string, data interface{}) {
	templates.ExecuteTemplate(w, name+".html", data)
}

// SetupRoutes mounts the UI routes on a chi router.
func SetupRoutes(r *chi.Mux) {
	r.Get("/", redirectTo("/dashboard"))
	r.Get("/dashboard", handleDashboard)
	r.Get("/sessions", handleSessions)
	r.Get("/sessions/{sessionID}", handleSessionDetail)
	r.Get("/sessions/{sessionID}/events", handleSessionEvents)
	r.Get("/sessions/{sessionID}/checkpoints", handleSessionCheckpoints)
	r.Get("/flags", handleFlags)
	r.Get("/live", handleLive)
}

func redirectTo(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, path, http.StatusMovedPermanently)
	}
}

// --- Page handlers ---

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "dashboard", nil)
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "sessions", nil)
}

func handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	Render(w, r, "session_detail", map[string]string{"SessionID": sessionID})
}

func handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	Render(w, r, "session_events", map[string]string{"SessionID": sessionID})
}

func handleSessionCheckpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	Render(w, r, "session_checkpoints", map[string]string{"SessionID": sessionID})
}

func handleFlags(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "flags", nil)
}

func handleLive(w http.ResponseWriter, r *http.Request) {
	Render(w, r, "live", nil)
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
