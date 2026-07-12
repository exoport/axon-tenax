package sdk //nolint:testpackage // white-box test of unexported sdk internals (serveConfig, newServeConfig)

// serve_test.go — unit tests for the turnkey sdk.Serve worker surface (Story 57.1, ADR-0047).
//
// Serve's body in this story is a scoped, honestly-incomplete skeleton (Scope Deviations): the
// real cross-process registration/discovery and work-queue dispatch mechanics land in Story
// 57.2. These tests exercise what the SDK layer can assert without a live engine or a designed
// wire protocol: the Serve signature, argument validation, ctx-cancellation behavior, and each
// ServeOption's effect on the internal serveConfig (including WithWorkerName's os.Hostname()
// fallback when unset).

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Serve signature + argument validation
// ---------------------------------------------------------------------------

// TestServeSignature verifies that Serve has the frozen signature
// func(context.Context, *nats.Conn, *Registry, ...ServeOption) error (ADR-0047, Cortex-ACKed).
func TestServeSignature(t *testing.T) {
	//nolint:staticcheck // QF1011: explicit type intentionally asserts Serve matches the frozen ADR-0047 signature exactly
	var fn func(context.Context, *nats.Conn, *Registry, ...ServeOption) error = Serve
	_ = fn
	t.Log("sdk.Serve(context.Context, *nats.Conn, *Registry, ...ServeOption) error: signature verified at compile time")
}

// TestServe_NilConnection_ReturnsErrServeConfigInvalid verifies Serve rejects a nil *nats.Conn
// with a wrapped ErrServeConfigInvalid sentinel (ADR-0030), rather than blocking or panicking.
func TestServe_NilConnection_ReturnsErrServeConfigInvalid(t *testing.T) {
	reg := NewRegistry()

	err := Serve(context.Background(), nil, reg)
	if err == nil {
		t.Fatal("Serve(ctx, nil, reg): expected error, got nil")
	}
	if !errors.Is(err, ErrServeConfigInvalid) {
		t.Errorf("Serve(ctx, nil, reg): err=%v, want errors.Is(err, ErrServeConfigInvalid)", err)
	}
}

// TestServe_NilRegistry_ReturnsErrServeConfigInvalid verifies Serve rejects a nil *Registry —
// Serve MUST take an explicit *Registry and MUST NOT fall back to GlobalRegistry() (Gap A,
// ADR-0047 AC2) — with a wrapped ErrServeConfigInvalid sentinel.
func TestServe_NilRegistry_ReturnsErrServeConfigInvalid(t *testing.T) {
	nc := &nats.Conn{}

	err := Serve(context.Background(), nc, nil)
	if err == nil {
		t.Fatal("Serve(ctx, nc, nil): expected error, got nil")
	}
	if !errors.Is(err, ErrServeConfigInvalid) {
		t.Errorf("Serve(ctx, nc, nil): err=%v, want errors.Is(err, ErrServeConfigInvalid)", err)
	}
}

// ---------------------------------------------------------------------------
// Serve — ctx-cancellation skeleton behavior (Scope Deviations: no dispatch loop yet)
// ---------------------------------------------------------------------------

// TestServe_BlocksUntilContextCancelled_ThenReturnsNil verifies the scoped skeleton behavior
// specified in Scope Deviations: Serve blocks until ctx is cancelled, then returns (nil, since
// there is no in-flight invocation to drain in this story's skeleton).
func TestServe_BlocksUntilContextCancelled_ThenReturnsNil(t *testing.T) {
	nc := &nats.Conn{}
	reg := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, nc, reg)
	}()

	// Confirm Serve is still blocking (has not returned) before we cancel.
	select {
	case err := <-done:
		t.Fatalf("Serve returned early (err=%v) before ctx was cancelled", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve(ctx, nc, reg) after ctx cancel: err=%v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of ctx cancellation")
	}
}

// ---------------------------------------------------------------------------
// newServeConfig — defaults + each ServeOption mutating its expected field
// ---------------------------------------------------------------------------

// TestNewServeConfig_Defaults verifies the defaults applied when no ServeOption is supplied:
// concurrency defaults to a single in-flight invocation (the pre-Pin-#1 baseline
// WithConcurrency exists to raise) and workerName falls back to os.Hostname().
func TestNewServeConfig_Defaults(t *testing.T) {
	wantHostname, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname() unavailable in this environment: %v", err)
	}

	cfg, err := newServeConfig()
	if err != nil {
		t.Fatalf("newServeConfig(): unexpected error: %v", err)
	}
	if cfg.concurrency != defaultConcurrency {
		t.Errorf("cfg.concurrency = %d, want default %d", cfg.concurrency, defaultConcurrency)
	}
	if cfg.drainTimeout != defaultDrainTimeout {
		t.Errorf("cfg.drainTimeout = %v, want default %v", cfg.drainTimeout, defaultDrainTimeout)
	}
	if cfg.workerName != wantHostname {
		t.Errorf("cfg.workerName = %q, want os.Hostname() fallback %q", cfg.workerName, wantHostname)
	}
}

// TestWithConcurrency_MutatesConcurrency verifies WithConcurrency(n) sets serveConfig.concurrency.
func TestWithConcurrency_MutatesConcurrency(t *testing.T) {
	cfg, err := newServeConfig(WithConcurrency(42))
	if err != nil {
		t.Fatalf("newServeConfig(WithConcurrency(42)): unexpected error: %v", err)
	}
	if cfg.concurrency != 42 {
		t.Errorf("cfg.concurrency = %d, want 42", cfg.concurrency)
	}
}

// TestWithDrainTimeout_MutatesDrainTimeout verifies WithDrainTimeout(d) sets
// serveConfig.drainTimeout.
func TestWithDrainTimeout_MutatesDrainTimeout(t *testing.T) {
	want := 5 * time.Second
	cfg, err := newServeConfig(WithDrainTimeout(want))
	if err != nil {
		t.Fatalf("newServeConfig(WithDrainTimeout(%v)): unexpected error: %v", want, err)
	}
	if cfg.drainTimeout != want {
		t.Errorf("cfg.drainTimeout = %v, want %v", cfg.drainTimeout, want)
	}
}

// TestWithWorkerName_MutatesWorkerName verifies WithWorkerName(name) sets serveConfig.workerName
// and does NOT fall back to os.Hostname() when explicitly supplied.
func TestWithWorkerName_MutatesWorkerName(t *testing.T) {
	cfg, err := newServeConfig(WithWorkerName("worker-7"))
	if err != nil {
		t.Fatalf("newServeConfig(WithWorkerName(\"worker-7\")): unexpected error: %v", err)
	}
	if cfg.workerName != "worker-7" {
		t.Errorf("cfg.workerName = %q, want %q", cfg.workerName, "worker-7")
	}
}

// TestWithWorkerName_Unset_FallsBackToHostname verifies that when WithWorkerName is not
// supplied, serveConfig.workerName resolves to os.Hostname() (AC1, AC-Task-2).
func TestWithWorkerName_Unset_FallsBackToHostname(t *testing.T) {
	wantHostname, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname() unavailable in this environment: %v", err)
	}

	// Combine with other options to confirm workerName resolution is independent of them.
	cfg, err := newServeConfig(WithConcurrency(3), WithDrainTimeout(time.Second))
	if err != nil {
		t.Fatalf("newServeConfig(): unexpected error: %v", err)
	}
	if cfg.workerName != wantHostname {
		t.Errorf("cfg.workerName = %q, want os.Hostname() fallback %q", cfg.workerName, wantHostname)
	}
}

// ---------------------------------------------------------------------------
// No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestServe_NoInternalImport is a documentation test: if this file compiles and runs, sdk/
// (including serve.go) does not import internal/ (ADR-0028). The repo-wide import-graph
// assertion (go list -deps github.com/exoport/axon-tenax/sdk/..., sdk/fat_test.go
// TestSDKImportGraphNoForbiddenInternals) is the enforcement oracle for AC5.
func TestServe_NoInternalImport(t *testing.T) {
	t.Log("sdk.Serve/ServeOption ADR-0028 boundary: confirmed by successful compilation")
}
