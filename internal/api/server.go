package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	router     *chi.Mux
	store      *db.Store
	sessMgr    *session.Manager
	httpServer *http.Server
	broadcaster *broadcaster
}

// broadcaster manages SSE client connections.
type broadcaster struct {
	clients    map[chan []byte]struct{}
	addCh      chan chan []byte
	removeCh   chan chan []byte
	broadcastCh chan []byte
	stopCh     chan struct{}
}

func newBroadcaster() *broadcaster {
	b := &broadcaster{
		clients:    make(map[chan []byte]struct{}),
		addCh:      make(chan chan []byte),
		removeCh:   make(chan chan []byte),
		broadcastCh: make(chan []byte),
		stopCh:     make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *broadcaster) run() {
	for {
		select {
		case ch := <-b.addCh:
			b.clients[ch] = struct{}{}
		case ch := <-b.removeCh:
			delete(b.clients, ch)
			close(ch)
		case msg := <-b.broadcastCh:
			for ch := range b.clients {
				select {
				case ch <- msg:
				default:
					// slow client — skip
				}
			}
		case <-b.stopCh:
			for ch := range b.clients {
				close(ch)
			}
			return
		}
	}
}

// Broadcast sends an event to all SSE clients.
func (b *broadcaster) Broadcast(eventType string, data interface{}) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.Encode(data)
	fmt.Fprintf(&buf, "event: %s\ndata: %s\n\n", eventType, buf.String())
	b.broadcastCh <- buf.Bytes()
}

func (b *broadcaster) AddClient() (<-chan []byte, func()) {
	ch := make(chan []byte, 50)
	b.addCh <- ch
	done := func() { b.removeCh <- ch }
	return ch, done
}

func (b *broadcaster) Stop() {
	close(b.stopCh)
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
		router:     r,
		store:      store,
		sessMgr:    sessMgr,
		broadcaster: newBroadcaster(),
	}

	s.registerRoutes()
	return s
}

// registerRoutes sets up all API routes.
func (s *Server) registerRoutes() {
	r := s.router

	r.Get("/health", s.handleHealth)
	r.Get("/stats", s.handleStats)

	// Sessions.
	r.Route("/sessions", func(r chi.Router) {
		r.Post("/", s.handleCreateSession)
		r.Get("/", s.handleListSessions)
		r.Get("/{sessionID}", s.handleGetSession)
		r.Get("/{sessionID}/summary", s.handleGetSessionSummary)
		r.Post("/{sessionID}/compact", s.handleCompact)
		r.Post("/{sessionID}/heartbeat", s.handleHeartbeat)
		r.Post("/{sessionID}/interrupt", s.handleInterruptSession)
		r.Post("/{sessionID}/resume", s.handleResumeSession)
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

	// SSE live stream.
	r.Get("/live", s.handleSSE)

	// Task chain.
	r.Get("/events/{eventID}/chain", s.handleGetTaskChain)
}

// Router returns the underlying chi router for mounting.
func (s *Server) Router() *chi.Mux { return s.router }

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
	s.broadcaster.Stop()
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
