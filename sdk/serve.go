// serve.go — the turnkey sdk.Serve worker surface for a separately-deployed SDK worker
// (Story 57.1, ADR-0047).
//
// Serve is the frozen, Cortex-ACKed worker-side surface that CR-21's remedy (ADR-0047,
// Option 2) decided: a BYO worker binary — its own process, its own Go module — calls Serve to
// consume Workflow dispatches over NATS with the same exactly-once guarantee as
// --runtime inproc.
//
// SCOPE (Story 57.1): this file ships the SURFACE — signature, options, and doc-comments — not
// a working dispatch loop. The cross-process registration/discovery (Gap A) and work-queue
// dispatch mechanics (Gap B) that make Serve functionally live are Story 57.2's job, wired
// behind this same signature. Building them here would mean building against a wire protocol
// that has not been designed yet — an "invented mechanism to look complete" honesty hazard
// (ADR-0017). Serve's body below is therefore a scoped, honestly-incomplete skeleton: it
// validates its arguments, resolves its options (including WithWorkerName's os.Hostname()
// fallback), then blocks on ctx cancellation and returns — no subscribe/dispatch/publish loop
// exists yet.
//
// Import boundary: this file imports only context, errors, fmt, os, time (stdlib) and
// github.com/nats-io/nats.go (the SDK's existing NATS dependency) — zero internal/ import
// (ADR-0028/0045, Never-Do #4).

package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	nats "github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030)
// ---------------------------------------------------------------------------

var (
	// ErrServeConfigInvalid is returned by Serve when a required argument (nc or reg) is nil.
	ErrServeConfigInvalid = errors.New("sdk: invalid Serve configuration")

	// ErrWorkerNameUnresolved is returned by Serve when WithWorkerName is not supplied and
	// os.Hostname() fails to resolve a fallback worker identity.
	ErrWorkerNameUnresolved = errors.New("sdk: worker name unresolved")
)

// ---------------------------------------------------------------------------
// Serve — the frozen worker-side surface (ADR-0047, Cortex-ACKed)
// ---------------------------------------------------------------------------

// Serve runs a remote worker's dispatch loop against a Tenax substrate over NATS: it advertises
// the handlers registered in reg to tenaxd, consumes dispatches for them, executes each, and
// publishes the result — carrying Tenax-Inv-Id (Gap-C fix, E56/56.2). Blocks until ctx is
// cancelled, then drains in-flight invocations (bounded by WithDrainTimeout) before returning.
//
// reg is an explicit *Registry — NEVER the package-level GlobalRegistry() singleton, whose
// same-OS-process-only reuse is the root cause Serve exists to remedy (ADR-0047 Gap A).
// Registry itself is unmodified by Serve: it stays a set (no one-handler assertion),
// statically registered before Serve is called. Serve is scoped to the Interpreter only —
// other worker kinds (e.g. Cortex's cortexd-worker-* binaries) are explicitly not Serve
// consumers.
//
// FAILURE CONTRACT (frozen, identical to --runtime inproc): if a worker process dies
// mid-dispatch, the in-flight invocation remains journal-resumable by ANY restarted worker
// instance (never pinned to the dead process); redrive is at-least-once against the recorded
// op, with opId dedup bounding external effects to the crash-before-journal window. A remote
// worker gives the SAME exactly-once guarantee as inproc — this is the load-bearing invariant.
// (ADR-0047, Pin #2.)
//
// Story 57.1 ships this as a frozen surface only: the dispatch loop's real cross-process
// registration/discovery and work-queue mechanics land in Story 57.2, behind this same
// signature — see the source comment on this function's implementation below.
func Serve(ctx context.Context, nc *nats.Conn, reg *Registry, opts ...ServeOption) error {
	if nc == nil {
		return fmt.Errorf("sdk: Serve: nats connection must not be nil: %w", ErrServeConfigInvalid)
	}
	if reg == nil {
		return fmt.Errorf("sdk: Serve: registry must not be nil: %w", ErrServeConfigInvalid)
	}
	if _, err := newServeConfig(opts...); err != nil {
		return err
	}

	// Story 57.2 wires the real cross-process registration/discovery (Gap A) and work-queue
	// dispatch/execute/publish (Gap B) here, behind this frozen signature (ADR-0047). Building
	// them now would mean building against a wire protocol that does not exist yet — an
	// "invented mechanism to look complete" honesty hazard (ADR-0017; Scope Deviations). Until
	// 57.2 lands there is no in-flight invocation for WithDrainTimeout to bound: this skeleton
	// only respects ctx cancellation.
	<-ctx.Done()
	return nil
}

// ---------------------------------------------------------------------------
// ServeOption — functional options (mirrors sdk/register.go's Option func(*SDK))
// ---------------------------------------------------------------------------

// ServeOption configures a Serve call. Mirrors the SDK's existing functional-options
// convention (Option func(*SDK) in sdk/register.go).
type ServeOption func(*serveConfig)

// serveConfig holds the resolved configuration for a single Serve call.
type serveConfig struct {
	workerName   string
	drainTimeout time.Duration
	concurrency  int
}

const (
	// defaultConcurrency is applied when WithConcurrency is not supplied: a single in-flight
	// invocation per worker — the pre-Pin-#1 baseline WithConcurrency exists to raise
	// (ADR-0047 Pin #1).
	defaultConcurrency = 1

	// defaultDrainTimeout is the graceful-shutdown budget applied when WithDrainTimeout is not
	// supplied.
	defaultDrainTimeout = 30 * time.Second
)

// newServeConfig applies opts over the default serveConfig and resolves WithWorkerName's
// os.Hostname() fallback when unset. Returns ErrWorkerNameUnresolved (wrapped) if no
// WithWorkerName was supplied and os.Hostname() fails.
func newServeConfig(opts ...ServeOption) (*serveConfig, error) {
	cfg := &serveConfig{
		concurrency:  defaultConcurrency,
		drainTimeout: defaultDrainTimeout,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.workerName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("sdk: Serve: resolve worker name: %w: %w", err, ErrWorkerNameUnresolved)
		}
		cfg.workerName = hostname
	}
	return cfg, nil
}

// WithConcurrency sets the maximum number of independent invocations this worker processes
// concurrently (v1-committed, SLO-critical — Pin #1, ADR-0047). Binds to the underlying NATS
// consumer's MaxAckPending so backpressure is honest (no silent over-pull). Concurrency is
// across independent keyed invocations, each on its own goroutine — orthogonal to and
// compatible with per-invocation replay determinism (which forbids goroutines inside a handler
// body — this is not that; NFR-DET-3).
//
// The MaxAckPending binding itself is engineered and proven against a live NATS consumer in
// Story 57.2 (ADR-0047 Pin #1) — this option ships the v1-committed surface only.
func WithConcurrency(n int) ServeOption {
	return func(c *serveConfig) {
		c.concurrency = n
	}
}

// WithDrainTimeout bounds the graceful-shutdown budget: after ctx is cancelled, Serve stops
// accepting new dispatches and waits up to d for in-flight invocations to finish before
// returning.
func WithDrainTimeout(d time.Duration) ServeOption {
	return func(c *serveConfig) {
		c.drainTimeout = d
	}
}

// WithWorkerName sets the identity this worker advertises to tenaxd for cross-process
// registration/discovery. Defaults to os.Hostname() when unset.
func WithWorkerName(name string) ServeOption {
	return func(c *serveConfig) {
		c.workerName = name
	}
}
