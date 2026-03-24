package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/mentholmike/lethe/internal/db"
	"github.com/mentholmike/lethe/internal/models"
	"github.com/mentholmike/lethe/internal/session"
)

// Server is the HTTP API server.
type Server struct {
	router    *chi.Mux
	store     *db.Store
	sessMgr   *session.Manager
	httpServer *http.Server
}

// NewServer creates a new API server.
func NewServer(store *db.Store, sessMgr *session.Manager) *Server {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	s := &Server{
		router:  r,
		store:   store,
		sessMgr: sessMgr,
	}

	s.registerRoutes()
	return s
}

// registerRoutes sets up all API routes.
func (s *Server) registerRoutes() {
	r := s.router

	r.Get("/health", s.handleHealth)

	// Sessions.
	r.Route("/sessions", func(r chi.Router) {
		r.Post("/", s.handleCreateSession)
		r.Get("/", s.handleListSessions)
		r.Get("/{sessionID}", s.handleGetSession)
		r.Get("/{sessionID}/summary", s.handleGetSessionSummary)
		r.Post("/{sessionID}/heartbeat", s.handleHeartbeat)
		r.Post("/{sessionID}/interrupt", s.handleInterruptSession)
		r.Post("/{sessionID}/complete", s.handleCompleteSession)
	})

	// Events.
	r.Route("/sessions/{sessionID}/events", func(r chi.Router) {
		r.Post("/", s.handleCreateEvent)
		r.Get("/", s.handleGetSessionEvents)
	})

	// Checkpoints.
	r.Route("/sessions/{sessionID}/checkpoints", func(r chi.Router) {
		r.Post("/", s.handleCreateCheckpoint)
		r.Get("/", s.handleGetCheckpoints)
	})

	// Flag review.
	r.Get("/flags", s.handleGetFlags)
	r.Put("/flags/{eventID}/review", s.handleReviewFlag)

	// Task chain.
	r.Get("/events/{eventID}/chain", s.handleGetTaskChain)
}

// Listen starts the HTTP server.
func (s *Server) Listen(addr string) error {
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:     s.router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// readJSON reads a JSON request body into v.
func readJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// generateID returns a new random UUID string.
func generateID() string {
	return uuid.New().String()
}

// ErrorResponse represents an API error.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// StoreInterface captures the store methods used by handlers (allows test doubles).
type StoreInterface interface {
	GetSessionEvents(ctx context.Context, sessionID string, limit, offset int) ([]*models.Event, error)
	GetSessionEventsCount(ctx context.Context, sessionID string) (int, error)
	CreateCheckpoint(ctx context.Context, c *models.Checkpoint) error
	GetCheckpoints(ctx context.Context, sessionID string) ([]*models.Checkpoint, error)
	CreateEvent(ctx context.Context, e *models.Event) error
	GetTaskChain(ctx context.Context, eventID string) ([]*models.Event, error)
}
