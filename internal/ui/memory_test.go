package ui

// Tests for the Memory Git browser. The handlers proxy the local memory API,
// so each test points the package apiBase at an httptest stub serving
// controlled fixtures, then asserts on the rendered truth: routing,
// integrity honesty, protection derivation, ref preservation, and pagination.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func stubAPI(t *testing.T, failContext bool, changesetCount int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/refs"):
			io.WriteString(w, `[
			  {"project_id":"default","ref_name":"refs/shared/main","head_changeset_id":"cs1","protected":true,"created_by_principal":"system"},
			  {"project_id":"default","ref_name":"refs/topics/t1","head_changeset_id":"cs2","protected":false,"created_by_principal":"principal_x"}
			]`)
		case strings.Contains(r.URL.Path, "/changesets") && !strings.Contains(r.URL.Path, "/changesets/cs"):
			rows := make([]string, 0, changesetCount)
			for i := 0; i < changesetCount; i++ {
				rows = append(rows, `{"changeset_id":"cs`+string(rune('a'+i%26))+`","message":"m","author_principal":"p","ref_name":"refs/shared/main","created_at":"2026-01-01T00:00:00Z"}`)
			}
			io.WriteString(w, `{"changesets":[`+strings.Join(rows, ",")+`]}`)
		case strings.Contains(r.URL.Path, "/context"):
			if failContext {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":"unknown project"}`)
				return
			}
			io.WriteString(w, `{"project_id":"default","ref_name":"refs/shared/main","head_changeset_id":"cs1","total_active":2,"memories":[
			  {"memory_id":"m1","content":"alpha","event_type":"record","kind":"decision","tags":["x"]},
			  {"memory_id":"m2","content":"beta","event_type":"record","kind":"task","tags":["y"]}
			]}`)
		case strings.HasSuffix(r.URL.Path, "/projects"):
			io.WriteString(w, `{"projects":[{"project_id":"default","name":"default"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newMux(t *testing.T, stubURL string, rootRedirect bool) *chi.Mux {
	t.Helper()
	if templates == nil {
		t.Skip("templates not initialized in test environment")
	}
	mux := chi.NewRouter()
	SetupMemoryRoutes(mux, stubURL, rootRedirect)
	return mux
}

func get(t *testing.T, mux http.Handler, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(body)
}

func TestGitOnlyRootRedirectsToMemory(t *testing.T) {
	stub := stubAPI(t, false, 2)
	defer stub.Close()
	mux := newMux(t, stub.URL, true)
	req := httptest.NewRequest("GET", "/ui", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound && rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("/ui status = %d, want redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/memory" {
		t.Fatalf("/ui redirect = %q, want /ui/memory", loc)
	}
}

func TestHomeRendersVerifiedOnlyOnSuccess(t *testing.T) {
	stub := stubAPI(t, false, 2)
	defer stub.Close()
	mux := newMux(t, stub.URL, false)
	code, body := get(t, mux, "/ui/memory?project=default")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"chain ✓", "protected · refs/shared/main", "alpha", "beta"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in page", want)
		}
	}
}

func TestIntegrityClaimsSuppressedWhenContextFails(t *testing.T) {
	stub := stubAPI(t, true, 2)
	defer stub.Close()
	mux := newMux(t, stub.URL, false)
	code, body := get(t, mux, "/ui/memory?project=ghost")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with degraded claims", code)
	}
	if strings.Contains(body, ">VERIFIED<") || strings.Contains(body, "reconstructed at head ✓") {
		t.Fatal("projection/integrity claims rendered despite failed context fetch")
	}
	if !strings.Contains(body, "UNVERIFIED") && !strings.Contains(body, "unverified") {
		t.Fatal("expected an explicit unverified state")
	}
}

func TestUnprotectedRefGetsNoProtectedBadge(t *testing.T) {
	stub := stubAPI(t, false, 2)
	defer stub.Close()
	mux := newMux(t, stub.URL, false)
	_, body := get(t, mux, "/ui/memory?project=default&ref=refs/topics/t1")
	if strings.Contains(body, "protected · refs/topics/t1") {
		t.Fatal("protected badge rendered for an unprotected ref")
	}
	if !strings.Contains(body, "refs/topics/t1") {
		t.Fatal("selected ref name missing from identity")
	}
}

func TestChangesetLinksPreserveRef(t *testing.T) {
	stub := stubAPI(t, false, 2)
	defer stub.Close()
	mux := newMux(t, stub.URL, false)
	_, body := get(t, mux, "/ui/memory/changesets?project=default&ref=refs/topics/t1")
	if !strings.Contains(body, "&ref=refs/topics/t1") {
		t.Fatal("changeset row links drop the selected ref")
	}
}

func TestPaginationHonestyWhenPageIsFull(t *testing.T) {
	stub := stubAPI(t, false, 200)
	defer stub.Close()
	mux := newMux(t, stub.URL, false)
	_, body := get(t, mux, "/ui/memory?project=default")
	if !strings.Contains(body, "200+") {
		t.Fatal("full page not marked as possibly truncated")
	}
	if strings.Contains(body, "/200") {
		t.Fatal("truncated page presented as a complete N/N chain")
	}
}

func TestMemoryProjectDefaultsToDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/ui/memory", nil)
	if got := memoryProject(req); got != "default" {
		t.Fatalf("memoryProject() = %q, want default", got)
	}
	req2 := httptest.NewRequest("GET", "/ui/memory?project=Custom", nil)
	if got := memoryProject(req2); got != "Custom" {
		t.Fatalf("memoryProject() = %q, want Custom (case preserved)", got)
	}
}
