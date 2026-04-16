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
	httpAddr = flag.String("http", "localhost:3421", "HTTP listen address")
	apiPort  = flag.String("api-port", "", "Port the UI handlers should use to reach the API (defaults to the port in --http)")
	apiURL   = flag.String("api-url", "", "Full base URL for the API (e.g. http://192.168.1.10:3421). Overrides --api-port")
	dbPath   = flag.String("db", "./lethe.db", "path to SQLite database")
)

func main() {
	flag.Parse()

	database, err := db.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	sessMgr := session.NewManager(database)

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
	apiServer := api.NewServer(database, sessMgr)
	r.Mount("/api", apiServer.Router())
	ui.SetupRoutes(r, apiBase)

	// SSE endpoint — mounted at root so both /live and /ui/live work.
	r.Get("/live", apiServer.HandleSSE())

	srv := &http.Server{
		Addr:         *httpAddr,
		Handler:     r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
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
