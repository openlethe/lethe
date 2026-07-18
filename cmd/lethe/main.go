package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
	trustMode             = flag.String("trust", "", "Trust mode when no API key is set: loopback (loopback only, default) or private (loopback+private+link-local). Defaults to LETHE_TRUST or loopback.")
	serverMode            = flag.String("mode", "", "API mode: legacy, git, or hybrid. Defaults to LETHE_MODE or legacy.")
	assemblyRetentionDays = flag.Int("assembly-retention-days", -1, "Delete assemblies older than N days (0 = disable age-based retention, -1 = default 30).")
	assemblyMaxPerSession = flag.Int("assembly-max-per-session", -1, "Keep at most N assemblies per session (0 = disable count-based retention, -1 = default 500).")
)

func main() {
	flag.Parse()

	// Handle subcommands before server startup
	if len(flag.Args()) > 0 {
		switch flag.Args()[0] {
		case "keygen":
			if err := runKeygen(); err != nil {
				log.Fatalf("keygen failed: %v", err)
			}
			return
		case "memory":
			if err := runMemoryCLI(flag.Args()[1:]); err != nil {
				log.Fatalf("memory command failed: %v", err)
			}
			return
		case "verify-chain":
			if err := runVerifyChain(flag.Args()[1:]); err != nil {
				log.Fatalf("verify-chain failed: %v", err)
			}
			return
		}
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

	// Resolve trust mode: --trust flag > LETHE_TRUST env > default loopback.
	// Loopback-only trust is the safe default: private-network membership is
	// transport locality, not identity, and must never widen access silently.
	modeStr := *trustMode
	if modeStr == "" {
		modeStr = os.Getenv("LETHE_TRUST")
	}
	if modeStr == "" {
		modeStr = "loopback"
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

	// Startup validation for unsafe bind/auth combinations: binding a
	// non-loopback address without an API key would expose unauthenticated
	// memory writes to the network. Fail closed unless an explicit
	// development-only override is set (and loudly logged).
	if err := validateBindAuth(*httpAddr, *apiKey, os.Getenv("LETHE_ALLOW_INSECURE_BIND") == "1"); err != nil {
		log.Fatalf("lethe: %v", err)
	}
	if unsafePublicBindAddr(*httpAddr) && *apiKey == "" {
		log.Printf("lethe: WARNING: development override LETHE_ALLOW_INSECURE_BIND=1: serving WITHOUT authentication on non-loopback address %s; never use in production", *httpAddr)
	}
	if unsafePublicBindAddr(*httpAddr) && modeStr == "private" {
		log.Printf("lethe: WARNING: trust=private with a non-loopback bind treats network locality as access; prefer loopback trust with an API key")
	}

	configuredMode := *serverMode
	if configuredMode == "" {
		configuredMode = os.Getenv("LETHE_MODE")
	}
	resolvedMode, err := api.ParseMode(configuredMode)
	if err != nil {
		log.Fatalf("lethe: %v", err)
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
	// CHARON_MERGE_HMAC_KEYS configures purpose-specific merge keys as
	// "keyid=secret,keyid2=secret2" so rotation keeps an overlap window:
	// envelopes name their key ID and any configured key verifies.
	// CHARON_MERGE_HMAC_KEY (single key, empty key ID) remains for existing
	// deployments. CHARON_HMAC_KEY is the formally deprecated generic fallback:
	// it is accepted only when no purpose-specific key is set and will be
	// removed in a future release.
	mergeKeys := map[string]string{}
	if keysEnv := os.Getenv("CHARON_MERGE_HMAC_KEYS"); keysEnv != "" {
		for _, pair := range strings.Split(keysEnv, ",") {
			id, secret, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || id == "" || len(secret) < 32 {
				log.Fatalf("lethe: invalid CHARON_MERGE_HMAC_KEYS entry %q (want keyid=secret with >= 32 char secret)", pair)
			}
			mergeKeys[id] = secret
		}
	}
	if key := os.Getenv("CHARON_MERGE_HMAC_KEY"); key != "" {
		mergeKeys[""] = key
	}
	if len(mergeKeys) == 0 {
		if legacy := os.Getenv("CHARON_HMAC_KEY"); legacy != "" {
			log.Println("lethe: WARNING: CHARON_HMAC_KEY generic-key fallback is deprecated; set CHARON_MERGE_HMAC_KEY or CHARON_MERGE_HMAC_KEYS")
			mergeKeys[""] = legacy
		}
	}
	if len(mergeKeys) == 0 {
		log.Println("lethe: WARNING: no merge authorization key configured; protected-ref merges are unavailable")
	}
	recoveryReadOnly := os.Getenv("LETHE_RECOVERY_READONLY") == "1"
	if recoveryReadOnly {
		log.Println("lethe: RECOVERY READ-ONLY MODE: mutations are disabled until reconciliation succeeds (LETHE_RECOVERY_READONLY=1)")
	}
	apiServer := api.NewServer(database, sessMgr,
		api.WithAuthToken(*apiKey),
		api.WithCharonMergeKeys(mergeKeys),
		api.WithTrustMode(resolvedTrust),
		api.WithMode(resolvedMode),
		api.WithRecoveryReadOnly(recoveryReadOnly),
	)
	if *apiKey == "" {
		log.Printf("lethe: WARNING: no --api-key/LETHE_API_KEY configured; unauthenticated access is allowed from %s peers only", modeStr)
		log.Println("lethe: Set LETHE_API_KEY for reverse proxies, tunnels, shared networks, or any non-single-user deployment.")
	} else {
		log.Println("lethe: bearer authentication enabled for API, UI, and SSE")
	}
	r.Mount("/api", apiServer.Router())
	if resolvedMode.LegacyEnabled() {
		ui.SetupRoutes(r, apiBase, apiServer.AuthMiddleware())

		// SSE endpoint — mounted at root so both /live and /ui/live work.
		r.With(apiServer.AuthMiddleware()).Get("/live", apiServer.HandleSSE())
	}
	if resolvedMode.GitEnabled() {
		// Repository-style Memory Git browser. In hybrid mode the legacy
		// dashboard owns /ui; in git-only mode /ui redirects to the browser.
		ui.SetupMemoryRoutes(r, apiBase, !resolvedMode.LegacyEnabled(), apiServer.AuthMiddleware())
	}

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

	if recoveryReadOnly {
		// Retention deletes rows; recovery read-only forbids every mutation
		// until reconciliation succeeds, including background pruning.
		log.Println("lethe: assembly retention suspended in recovery read-only mode")
	} else {
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
	}

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
		if resolvedMode.LegacyEnabled() {
			// Interrupt all active sessions with no snapshot — this transitions
			// them to 'interrupted' state so they appear as resumable on next startup.
			if err := sessMgr.InterruptAllActive(shutdownCtx); err != nil {
				log.Printf("lethe: session checkpoint error: %v", err)
			} else {
				log.Println("lethe: all sessions checkpointed")
			}
		}
		// Stop the SSE broadcaster goroutine.
		apiServer.StopBroadcaster()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("lethe: HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("lethe: HTTP server starting on %s (mode=%s)", *httpAddr, resolvedMode)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("lethe: server stopped")
}

// runVerifyChain verifies the integrity digest of every changeset in a
// project and every parent reference, offline against the database: it is the
// restore-drill and tamper-audit tool. Usage: lethe verify-chain <project>.
func runVerifyChain(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lethe verify-chain <project>")
	}
	database, err := db.NewStore(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()
	verified, failures := database.VerifyChangesetChain(context.Background(), args[0])
	for _, failure := range failures {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", failure)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d of %d changesets failed verification", len(failures), verified)
	}
	fmt.Printf("verify-chain: %d changesets verified, 0 failures\n", verified)
	return nil
}

// runMemoryCLI implements the `lethe memory` subcommand family.
func runMemoryCLI(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: lethe memory <command> [args]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  status <project> [ref]        Show ref head and recent changesets")
		fmt.Println("  log <project> [ref]           List changeset history for a ref")
		fmt.Println("  show <changeset-id>           Display a changeset with operations")
		fmt.Println("  diff <base> <target>          Semantic diff between changesets")
		fmt.Println("  branch <project> <ref> <head> Create a new branch ref")
		fmt.Println("  merge-propose ...             Create a merge proposal (see --help)")
		fmt.Println("  manifest <manifest-id>        Show a context manifest")
		return nil
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "status":
		return memoryStatus(rest)
	case "log":
		return memoryLog(rest)
	case "show":
		return memoryShow(rest)
	case "diff":
		return memoryDiff(rest)
	case "branch":
		return memoryBranch(rest)
	case "merge-propose":
		return memoryMergePropose(rest)
	case "manifest":
		return memoryManifest(rest)
	default:
		return fmt.Errorf("unknown memory command: %s", cmd)
	}
}

func memoryAPIClient() *apiClient {
	key := os.Getenv("LETHE_API_KEY")
	base := os.Getenv("LETHE_API_URL")
	if base == "" {
		base = "http://localhost:18483"
	}
	return &apiClient{
		baseURL: base,
		apiKey:  key,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// maxCLIResponseBytes caps API response bodies the CLI will read so a broken
// or hostile server cannot exhaust local memory.
const maxCLIResponseBytes = 8 << 20

type apiClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func (c *apiClient) get(path string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api"+path, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxCLIResponseBytes))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *apiClient) post(path string, body any) ([]byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.baseURL+"/api"+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, maxCLIResponseBytes))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rbody))
	}
	return rbody, nil
}

func memoryStatus(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lethe memory status <project> [ref]")
	}
	project := args[0]
	refName := "refs/shared/main"
	if len(args) > 1 {
		refName = args[1]
	}
	c := memoryAPIClient()
	// Ref names contain slashes, so they cannot ride in a Chi path segment;
	// use the ref-resolution query endpoint with proper encoding.
	q := url.Values{"name": {refName}}
	body, err := c.get(fmt.Sprintf("/memory/%s/refs/resolve?%s", url.PathEscape(project), q.Encode()))
	if err != nil {
		return err
	}
	var ref struct {
		RefName         string `json:"ref_name"`
		HeadChangesetID string `json:"head_changeset_id"`
		Protected       bool   `json:"protected"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		return err
	}
	fmt.Printf("Ref:       %s\n", ref.RefName)
	fmt.Printf("Head:      %s\n", ref.HeadChangesetID)
	fmt.Printf("Protected: %v\n", ref.Protected)

	// Show recent changesets
	logQ := url.Values{"ref": {refName}, "limit": {"5"}}
	logBody, err := c.get(fmt.Sprintf("/memory/%s/changesets?%s", url.PathEscape(project), logQ.Encode()))
	if err != nil {
		return err
	}
	var logResp struct {
		Changesets []struct {
			ChangesetID string `json:"changeset_id"`
			Message     string `json:"message"`
			Author      string `json:"author_principal"`
			CreatedAt   string `json:"created_at"`
		} `json:"changesets"`
	}
	if err := json.Unmarshal(logBody, &logResp); err != nil {
		return err
	}
	fmt.Println("\nRecent changesets:")
	for _, cs := range logResp.Changesets {
		fmt.Printf("  %s  %s  %s\n", cs.ChangesetID[:8], cs.Author, cs.Message)
	}
	return nil
}

func memoryLog(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lethe memory log <project> [ref]")
	}
	project := args[0]
	refName := "refs/shared/main"
	if len(args) > 1 {
		refName = args[1]
	}
	c := memoryAPIClient()
	logQ := url.Values{"ref": {refName}, "limit": {"50"}}
	body, err := c.get(fmt.Sprintf("/memory/%s/changesets?%s", url.PathEscape(project), logQ.Encode()))
	if err != nil {
		return err
	}
	var resp struct {
		Changesets []struct {
			ChangesetID     string   `json:"changeset_id"`
			Message         string   `json:"message"`
			AuthorPrincipal string   `json:"author_principal"`
			CreatedAt       string   `json:"created_at"`
			ParentIDs       []string `json:"parent_ids"`
		} `json:"changesets"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}
	for _, cs := range resp.Changesets {
		parents := ""
		if len(cs.ParentIDs) > 0 {
			parents = fmt.Sprintf(" (parents: %v)", cs.ParentIDs)
		}
		fmt.Printf("%s  %s  %s%s\n", cs.ChangesetID[:8], cs.AuthorPrincipal, cs.Message, parents)
	}
	return nil
}

func memoryShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lethe memory show <changeset-id>")
	}
	id := args[0]
	c := memoryAPIClient()
	body, err := c.get("/memory/changesets/" + id)
	if err != nil {
		return err
	}
	var cs struct {
		ChangesetID     string   `json:"changeset_id"`
		SchemaVersion   string   `json:"schema_version"`
		ProjectID       string   `json:"project_id"`
		RefName         string   `json:"ref_name"`
		ParentIDs       []string `json:"parent_ids"`
		AuthorPrincipal string   `json:"author_principal"`
		ActorID         string   `json:"actor_id"`
		Message         string   `json:"message"`
		CreatedAt       string   `json:"created_at"`
		IdempotencyKey  string   `json:"idempotency_key"`
		IntegrityDigest string   `json:"integrity_digest"`
		Ops             []struct {
			Ordinal          int            `json:"ordinal"`
			OpType           string         `json:"op_type"`
			TargetEventID    string         `json:"target_event_id"`
			ResultingEventID string         `json:"resulting_event_id"`
			Payload          map[string]any `json:"payload"`
		} `json:"ops"`
	}
	if err := json.Unmarshal(body, &cs); err != nil {
		return err
	}
	fmt.Printf("Changeset: %s\n", cs.ChangesetID)
	fmt.Printf("Project:   %s\n", cs.ProjectID)
	fmt.Printf("Ref:       %s\n", cs.RefName)
	fmt.Printf("Author:    %s (actor: %s)\n", cs.AuthorPrincipal, cs.ActorID)
	fmt.Printf("Message:   %s\n", cs.Message)
	fmt.Printf("Created:   %s\n", cs.CreatedAt)
	fmt.Printf("Digest:    %s\n", cs.IntegrityDigest)
	if len(cs.ParentIDs) > 0 {
		fmt.Printf("Parents:   %v\n", cs.ParentIDs)
	}
	fmt.Println("\nOperations:")
	for _, op := range cs.Ops {
		summary := ""
		if s, ok := op.Payload["summary"].(string); ok {
			summary = s
		} else if c, ok := op.Payload["content"].(string); ok {
			if len(c) > 80 {
				summary = c[:80] + "..."
			} else {
				summary = c
			}
		}
		fmt.Printf("  [%d] %s", op.Ordinal, op.OpType)
		if op.TargetEventID != "" {
			fmt.Printf(" target=%s", op.TargetEventID)
		}
		if op.ResultingEventID != "" {
			fmt.Printf(" result=%s", op.ResultingEventID)
		}
		if summary != "" {
			fmt.Printf(" | %s", summary)
		}
		fmt.Println()
	}
	return nil
}

func memoryDiff(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lethe memory diff <base-changeset> <target-changeset>")
	}
	baseID, targetID := args[0], args[1]
	c := memoryAPIClient()
	body, err := c.post("/memory/changesets/"+targetID+"/diff", map[string]string{
		"base_changeset_id": baseID,
	})
	if err != nil {
		return err
	}
	var diff struct {
		MemoriesAdded       []map[string]any `json:"memories_added"`
		Corrections         []map[string]any `json:"corrections_proposed"`
		Superseded          []map[string]any `json:"records_superseded"`
		RelationshipsAdded  []map[string]any `json:"relationships_added"`
		DecisionsChanged    []map[string]any `json:"decisions_changed"`
		TasksFlagsChanged   []map[string]any `json:"tasks_or_flags_changed"`
		Duplicates          []map[string]any `json:"duplicates_detected"`
		VisibilityAffected  []map[string]any `json:"permissions_or_visibility_affected"`
		EvidenceChanged     []map[string]any `json:"evidence_added_or_removed"`
		UnresolvedConflicts []string         `json:"unresolved_conflicts"`
	}
	if err := json.Unmarshal(body, &diff); err != nil {
		return err
	}
	fmt.Printf("Semantic diff: %s → %s\n\n", baseID[:8], targetID[:8])
	printDiffSection("Memories added", diff.MemoriesAdded)
	printDiffSection("Corrections", diff.Corrections)
	printDiffSection("Superseded", diff.Superseded)
	printDiffSection("Relationships added", diff.RelationshipsAdded)
	printDiffSection("Decisions changed", diff.DecisionsChanged)
	printDiffSection("Tasks/flags changed", diff.TasksFlagsChanged)
	printDiffSection("Duplicates", diff.Duplicates)
	printDiffSection("Visibility affected", diff.VisibilityAffected)
	printDiffSection("Evidence changed", diff.EvidenceChanged)
	if len(diff.UnresolvedConflicts) > 0 {
		fmt.Printf("\nUnresolved conflicts: %v\n", diff.UnresolvedConflicts)
	}
	return nil
}

func printDiffSection(name string, items []map[string]any) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("%s (%d):\n", name, len(items))
	for _, item := range items {
		summary := ""
		if s, ok := item["summary"].(string); ok {
			summary = s
		}
		fmt.Printf("  - %s\n", summary)
	}
}

func memoryBranch(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: lethe memory branch <project> <ref-name> <head-changeset-id>")
	}
	project, refName, headID := args[0], args[1], args[2]
	c := memoryAPIClient()
	body, err := c.post(fmt.Sprintf("/memory/%s/branches", project), map[string]any{
		"ref_name":          refName,
		"head_changeset_id": headID,
		"principal":         os.Getenv("USER"),
	})
	if err != nil {
		return err
	}
	var ref map[string]any
	if err := json.Unmarshal(body, &ref); err != nil {
		return err
	}
	fmt.Printf("Created branch: %s → %s\n", ref["ref_name"], ref["head_changeset_id"])
	return nil
}

func memoryMergePropose(args []string) error {
	return fmt.Errorf("merge-propose: use Charon API or Lethe HTTP API directly; CLI wrapper not yet implemented")
}

func memoryManifest(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lethe memory manifest <manifest-id>")
	}
	id := args[0]
	c := memoryAPIClient()
	body, err := c.get("/memory/manifests/" + id)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
	return nil
}

// isLoopbackHost reports whether a bind host is loopback-only.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// unsafePublicBindAddr reports whether the address binds beyond loopback. An
// empty host is a wildcard bind: reachable from the network, never loopback.
func unsafePublicBindAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true // unparseable: treated unsafe
	}
	return !isLoopbackHost(host)
}

// validateBindAuth enforces safe bind/auth combinations: a non-loopback bind
// requires an API key unless the development-only insecure override is set.
func validateBindAuth(addr, apiKey string, allowInsecure bool) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid --http address %q: %v", addr, err)
	}
	if !unsafePublicBindAddr(addr) {
		return nil
	}
	if apiKey != "" {
		return nil
	}
	if allowInsecure {
		return nil
	}
	return fmt.Errorf("refusing to bind %s without an API key; set LETHE_API_KEY or bind a loopback address (LETHE_ALLOW_INSECURE_BIND=1 is a development-only override)", addr)
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
