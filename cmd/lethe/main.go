package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/openlethe/lethe/internal/api"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/session"
	"github.com/openlethe/lethe/internal/ui"
)

var (
	httpAddr              = flag.String("http", "localhost:18483", "HTTP listen address")
	apiPort               = flag.String("api-port", "", "Port the UI handlers should use to reach the API (defaults to the port in --http)")
	apiURL                = flag.String("api-url", "", "Full base URL for the API (e.g. http://192.168.1.10:18483). Overrides --api-port")
	dbPath                = flag.String("db", "./lethe.db", "path to SQLite database")
	apiKey                = flag.String("api-key", "", "Bearer token required for API/UI/SSE access. Defaults to LETHE_API_KEY if unset; no key keeps trusted localhost mode.")
	trustMode             = flag.String("trust", "", "Trust mode when no API key is set: private (loopback+private+link-local) or loopback (loopback only). Defaults to LETHE_TRUST or private.")
	assemblyRetentionDays = flag.Int("assembly-retention-days", -1, "Delete assemblies older than N days (0 = disable age-based retention, -1 = default 30).")
	assemblyMaxPerSession = flag.Int("assembly-max-per-session", -1, "Keep at most N assemblies per session (0 = disable count-based retention, -1 = default 500).")
)

func main() {
	flag.Parse()

	// Handle keygen subcommand before server startup
	if len(flag.Args()) > 0 && flag.Args()[0] == "keygen" {
		if err := runKeygen(); err != nil {
			log.Fatalf("keygen failed: %v", err)
		}
		return
	}

	database, err := db.NewStore(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	sessMgr := session.NewManager(database)
	if *apiKey == "" {
		*apiKey = os.Getenv("LETHE_API_KEY")
	}

	// Resolve trust mode: --trust flag > LETHE_TRUST env > default private.
	modeStr := *trustMode
	if modeStr == "" {
		modeStr = os.Getenv("LETHE_TRUST")
	}
	if modeStr == "" {
		modeStr = "private"
	}
	modeStr = strings.ToLower(modeStr)
	var resolvedTrust api.TrustMode
	switch modeStr {
	case "private":
		resolvedTrust = api.TrustPrivate
	case "loopback":
		resolvedTrust = api.TrustLoopback
	default:
		log.Fatalf("lethe: invalid trust mode %q; must be 'private' or 'loopback'", modeStr)
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
	apiServer := api.NewServer(database, sessMgr, api.WithAuthToken(*apiKey), api.WithTrustMode(resolvedTrust))
	if *apiKey == "" {
		log.Printf("lethe: WARNING: no --api-key/LETHE_API_KEY configured; unauthenticated access is allowed from %s peers only", modeStr)
		log.Println("lethe: Set LETHE_API_KEY for reverse proxies, tunnels, shared networks, or any non-single-user deployment.")
	} else {
		log.Println("lethe: bearer authentication enabled for API, UI, and SSE")
	}
	r.Mount("/api", apiServer.Router())
	ui.SetupRoutes(r, apiBase, apiServer.AuthMiddleware())

	// SSE endpoint — mounted at root so both /live and /ui/live work.
	r.With(apiServer.AuthMiddleware()).Get("/live", apiServer.HandleSSE())

	// Resolve assembly retention settings.
	retentionDays := *assemblyRetentionDays
	if retentionDays == -1 {
		if envVal := os.Getenv("LETHE_ASSEMBLY_RETENTION_DAYS"); envVal != "" {
			if _, err := fmt.Sscanf(envVal, "%d", &retentionDays); err != nil {
				log.Fatalf("lethe: invalid LETHE_ASSEMBLY_RETENTION_DAYS: %v", err)
			}
		}
	}
	if retentionDays == -1 {
		retentionDays = 30
	}
	maxPerSession := *assemblyMaxPerSession
	if maxPerSession == -1 {
		if envVal := os.Getenv("LETHE_ASSEMBLY_MAX_PER_SESSION"); envVal != "" {
			if _, err := fmt.Sscanf(envVal, "%d", &maxPerSession); err != nil {
				log.Fatalf("lethe: invalid LETHE_ASSEMBLY_MAX_PER_SESSION: %v", err)
			}
		}
	}
	if maxPerSession == -1 {
		maxPerSession = 500
	}

	// Assembly retention worker: cleanup every hour, bounded deletes per run.
	const pruneInterval = 1 * time.Hour
	const deleteLimit = 1000
	var pruneFailures int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pruneFn := func() {
		// Skip if both retention dimensions are disabled.
		if retentionDays == 0 && maxPerSession == 0 {
			return
		}
		var olderThan time.Time
		if retentionDays > 0 {
			olderThan = time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		} else {
			// Age-based retention disabled: use a zero time so the age condition matches nothing.
			olderThan = time.Time{}
		}
		deleted, err := database.PruneContextAssemblies(ctx, olderThan, maxPerSession, deleteLimit)
		if err != nil {
			pruneFailures++
			log.Printf("lethe: assembly retention prune failed (failures=%d): %v", pruneFailures, err)
			return
		}
		if deleted > 0 {
			log.Printf("lethe: assembly retention pruned %d assemblies (olderThan=%s, maxPerSession=%d)", deleted, olderThan.Format(time.RFC3339), maxPerSession)
		}
	}

	// One cleanup at startup.
	pruneFn()

	// Periodic cleanup.
	pruneTicker := time.NewTicker(pruneInterval)
	defer pruneTicker.Stop()
	go func() {
		for {
			select {
			case <-pruneTicker.C:
				pruneFn()
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Printf("lethe: assembly retention enabled (days=%d, maxPerSession=%d, interval=%s, batchLimit=%d)", retentionDays, maxPerSession, pruneInterval, deleteLimit)

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
		// Cancel retention worker and other background goroutines.
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		// Interrupt all active sessions with no snapshot — this transitions
		// them to 'interrupted' state so they appear as resumable on next startup.
		if err := sessMgr.InterruptAllActive(shutdownCtx); err != nil {
			log.Printf("lethe: session checkpoint error: %v", err)
		} else {
			log.Println("lethe: all sessions checkpointed")
		}
		// Stop the SSE broadcaster goroutine.
		apiServer.StopBroadcaster()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("lethe: HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("lethe: HTTP server starting on %s", *httpAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("lethe: server stopped")
}

// runKeygen generates a secure API key for Lethe.
// It prints the key to stdout in a format ready for .env files.
func runKeygen() error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("failed to generate random bytes: %w", err)
	}
	key := "lethe_" + hex.EncodeToString(b)

	fmt.Println("# Generated Lethe API Key")
	fmt.Println("# Add this to your .env file:")
	fmt.Println()
	fmt.Printf("LETHE_API_KEY=%s\n", key)
	fmt.Println()
	fmt.Println("# Or export directly:")
	fmt.Printf("# export LETHE_API_KEY=%s\n", key)
	fmt.Println()
	fmt.Println("# Security notes:")
	fmt.Println("# - This is a single shared secret")
	fmt.Println("# - All clients use this same key")
	fmt.Println("# - For multi-client access, use Charon with per-client Obols")
	fmt.Println("# - Store this in .env (not committed to git)")

	return nil
}
