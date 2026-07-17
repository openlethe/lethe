// Package metrics is a minimal in-process metrics registry with Prometheus
// text exposition. No external dependency; safe for concurrent use.
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing count.
type Counter struct{ v atomic.Int64 }

// Inc adds 1 to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n to the counter.
func (c *Counter) Add(n int64) { c.v.Add(n) }

// Gauge is a value that can go up and down.
type Gauge struct{ v atomic.Int64 }

// Set replaces the gauge value.
func (g *Gauge) Set(n int64) { g.v.Store(n) }

var (
	registryMu sync.RWMutex
	counters   = map[string]*Counter{}
	gauges     = map[string]*Gauge{}
)

// GetCounter returns the named counter, creating it on first use.
func GetCounter(name string) *Counter {
	registryMu.RLock()
	c, ok := counters[name]
	registryMu.RUnlock()
	if ok {
		return c
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if c, ok := counters[name]; ok {
		return c
	}
	c = &Counter{}
	counters[name] = c
	return c
}

// GetGauge returns the named gauge, creating it on first use.
func GetGauge(name string) *Gauge {
	registryMu.RLock()
	g, ok := gauges[name]
	registryMu.RUnlock()
	if ok {
		return g
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if g, ok := gauges[name]; ok {
		return g
	}
	g = &Gauge{}
	gauges[name] = g
	return g
}

// Inc adds 1 to the named counter.
func Inc(name string) { GetCounter(name).Inc() }

// Add adds n to the named counter.
func Add(name string, n int64) { GetCounter(name).Add(n) }

// SetGauge sets the named gauge.
func SetGauge(name string, n int64) { GetGauge(name).Set(n) }

// Expose renders the registry in Prometheus text exposition format.
func Expose() string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	var sb strings.Builder
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&sb, "# TYPE %s counter\n%s %d\n", name, name, counters[name].v.Load())
	}
	gaugeNames := make([]string, 0, len(gauges))
	for name := range gauges {
		gaugeNames = append(gaugeNames, name)
	}
	sort.Strings(gaugeNames)
	for _, name := range gaugeNames {
		fmt.Fprintf(&sb, "# TYPE %s gauge\n%s %d\n", name, name, gauges[name].v.Load())
	}
	return sb.String()
}

// Lethe metric names. Keep aligned with docs/observability.md.
const (
	BusyExhausted           = "lethe_sqlite_busy_exhausted_total"
	BusyRetries             = "lethe_sqlite_busy_retries_total"
	ChangesetCreated        = "lethe_changesets_created_total"
	ChangesetRejected       = "lethe_changesets_rejected_total"
	CASConflicts            = "lethe_ref_cas_conflicts_total"
	ConflictsPersisted      = "lethe_conflicts_persisted_total"
	ConflictsRetired        = "lethe_conflicts_retired_total"
	ConflictsResolved       = "lethe_conflicts_resolved_total"
	MergeAuthorized         = "lethe_merge_authorized_total"
	MergeAuthorizationFails = "lethe_merge_authorization_failures_total"
	MergeReplayRejects      = "lethe_merge_replay_rejections_total"
	RebuildOps              = "lethe_context_reconstructions_total"
	RebuildChangesets       = "lethe_context_reconstruction_changesets_total"
	RebuildDurationMS       = "lethe_context_reconstruction_ms_total"
	IdempotentReplays       = "lethe_idempotent_replays_total"
	IdempotencyMismatches   = "lethe_idempotency_mismatches_total"
)
