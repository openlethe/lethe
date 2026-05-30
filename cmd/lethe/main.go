package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/api"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/session"
	"github.com/openlethe/lethe/internal/ui"
)

var (
	httpAddr = flag.String("http", "localhost:18483", "HTTP listen address")
	apiPort  = flag.String("api-port", "", "Port the UI handlers should use to reach the API (defaults to the port in --http)")
	apiURL   = flag.String("api-url", "", "Full base URL for the API (e.g. http://192.168.1.10:18483). Overrides --api-port")
	dbPath   = flag.String("db", "./lethe.db", "path to SQLite database")
	apiKey   = flag.String("api-key", "", "Bearer token required for API/UI/SSE access. Defaults to LETHE_API_KEY if unset; no key keeps trusted localhost mode.")
)

func main() {
	flag.Parse()

	database, err := db.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	sessMgr := session.NewManager(database)
	if *apiKey == "" {
		*apiKey = os.Getenv("LETHE_API_KEY")
	}

	// Resolve API base URL: use --api-url if set, otherwise derive from --http and --api-port.
	var apiBase string
	if *apiURL != "" {
		apiBase = *apiURL
	} else {
		port := *apiPort
		if port == "" {
			_, p, err := net.SplitHostPort(*httpAddr)
			if err != nil {
				log.Fatalf("invalid --http address: %v", err)
			}
			port = p
		}
		host, _, err := net.SplitHostPort(*httpAddr)
		if err != nil {
			host = "127.0.0.1"
		}
		if host == "" {
			host = "127.0.0.1"
		}
		apiBase = "http://" + host + ":" + port
	}
	r := chi.NewRouter()
	apiServer := api.NewServer(database, sessMgr, api.WithAuthToken(*apiKey))
	if *apiKey == "" {
		log.Println("lethe: WARNING: no --api-key/LETHE_API_KEY configured; API/UI/SSE are restricted to localhost clients only")
	} else {
		log.Println("lethe: bearer authentication enabled for API/UI/SSE")
	}
	r.Mount("/api", apiServer.Router())
	ui.SetupRoutes(r, apiBase, apiServer.AuthMiddleware())

	// SSE endpoint — mounted at root so both /live and /ui/live work.
	r.With(apiServer.AuthMiddleware()).Get("/live", apiServer.HandleSSE())

	srv := &http.Server{
		Addr:        *httpAddr,
		Handler:     r,
		ReadTimeout: 10 * time.Second,
		// WriteTimeout must stay disabled for long-lived SSE streams.
		// Per-route middleware timeouts still protect non-SSE API handlers.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		log.Println("lethe: shutting down, checkpointing active sessions...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Interrupt all active sessions with no snapshot — this transitions
		// them to 'interrupted' state so they appear as resumable on next startup.
		if err := sessMgr.InterruptAllActive(ctx); err != nil {
			log.Printf("lethe: session checkpoint error: %v", err)
		} else {
			log.Println("lethe: all sessions checkpointed")
		}
		// Stop the SSE broadcaster goroutine.
		apiServer.StopBroadcaster()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("lethe: HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("lethe: HTTP server starting on %s", *httpAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("lethe: server stopped")
}
