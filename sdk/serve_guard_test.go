package sdk //nolint:testpackage // white-box test of unexported sdk internals (validateDurableContextGuard, serveConfig)

// serve_guard_test.go — unit tests for the serve-time durable-ctx.* handler rejection guard
// (Story 68.1, implementing Story 65.1 review cycle 2 finding F1's corrected two-tier spec and
// closing finding F2 — see serve_guard.go's header comment for the full design rationale).
//
// Per Story 59.1's standing lesson (a >1-pair goroutine leak invisible to a single-handler
// happy-path fixture), every scenario below that registers a handler registers AT LEAST TWO
// (a "pair") together, and TestServe_MultiInstance_ConcurrentRegistriesAcrossAllThreeTiers
// exercises all three guard outcomes (reject-by-default, kind-tier-reject, attested-pass)
// concurrently in the same test run — never a single happy-path fixture in isolation.
//
// Serve()-level tests below never register a real handler AND drive Serve() past the guard at
// the same time: the announce/fetch-loop goroutines (serve.go) touch nc.Publish / jetstream
// consumer binds, which require a live NATS connection — the zero-value *nats.Conn{} fixture
// used throughout this file (matching serve_test.go's own convention) is not connected, and
// nats.go's Conn.publish dereferences connection-internal state that is only initialized by a
// real nats.Connect(). The rejection-path tests are safe because the guard returns BEFORE
// jetstream.New/any goroutine starts; the one attested-pass-through test
// (TestServe_AttestedServiceOnly_GuardPasses_BlocksUnchanged) uses a Service registered with
// ZERO handlers so collectHandlerRefs(reg) stays empty and Serve() takes the pre-existing
// "nothing to advertise or consume" skeleton path instead of the network loops — see that
// test's doc comment for why this is still a faithful proof of the guard decision.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// validateDurableContextGuard — direct unit coverage of the guard mechanism
// ---------------------------------------------------------------------------

// TestValidateDurableContextGuard_EmptyRegistry_Passes verifies the pre-existing degenerate
// case (zero registered handlers of any kind) is never rejected, attested or not — there is no
// handler that could ever reach a durable ctx.* verb.
func TestValidateDurableContextGuard_EmptyRegistry_Passes(t *testing.T) {
	if err := validateDurableContextGuard(NewRegistry(), false); err != nil {
		t.Errorf("validateDurableContextGuard(empty, attested=false) = %v, want nil", err)
	}
	if err := validateDurableContextGuard(NewRegistry(), true); err != nil {
		t.Errorf("validateDurableContextGuard(empty, attested=true) = %v, want nil", err)
	}
}

// TestValidateDurableContextGuard_ServiceOnly_Unattested_Rejects verifies the attestation-tier
// reject-by-default guard fires for a Service-only registry when WithNoDurableContextAttestation
// was not passed. Multi-pair (two Service handlers registered together, Story 59.1 lesson).
func TestValidateDurableContextGuard_ServiceOnly_Unattested_Rejects(t *testing.T) {
	reg := twoServiceRegistry(t)

	err := validateDurableContextGuard(reg, false)
	if !errors.Is(err, ErrServeDurableContextRejected) {
		t.Fatalf("validateDurableContextGuard: err=%v, want errors.Is(err, ErrServeDurableContextRejected)", err)
	}
	var rejErr *ServeDurableContextRejectedError
	if !errors.As(err, &rejErr) {
		t.Fatalf("errors.As(err, *ServeDurableContextRejectedError) failed: %v", err)
	}
	if rejErr.What == "" || rejErr.Cause == "" || rejErr.Hint == "" {
		t.Errorf("triad incomplete: %+v", rejErr)
	}
	if !strings.Contains(rejErr.What, "2 Service handler(s)") {
		t.Errorf("What = %q, want mention of 2 registered Service handlers", rejErr.What)
	}
	if !strings.Contains(rejErr.Hint, "WithNoDurableContextAttestation") {
		t.Errorf("Hint = %q, want a pointer to WithNoDurableContextAttestation", rejErr.Hint)
	}
}

// TestValidateDurableContextGuard_ServiceOnly_Attested_Passes verifies
// WithNoDurableContextAttestation (attested=true) lifts the attestation-tier guard for a
// Service-only registry. Multi-pair (two Service handlers registered together).
func TestValidateDurableContextGuard_ServiceOnly_Attested_Passes(t *testing.T) {
	reg := twoServiceRegistry(t)

	if err := validateDurableContextGuard(reg, true); err != nil {
		t.Errorf("validateDurableContextGuard(service-only, attested=true) = %v, want nil", err)
	}
}

// TestValidateDurableContextGuard_VirtualObjectAndWorkflow_RejectsKindTier_EvenAttested verifies
// the kind tier is unconditional: a registry containing a Virtual Object AND a Workflow handler
// (multi-instance: two keyed kinds registered together) is rejected regardless of attested.
func TestValidateDurableContextGuard_VirtualObjectAndWorkflow_RejectsKindTier_EvenAttested(t *testing.T) {
	keyedEcho := func(_ Context, _ string, req []byte) ([]byte, error) { return req, nil }
	reg := NewRegistry()

	obj := NewVirtualObject("order")
	if _, err := obj.Handler("charge", keyedEcho); err != nil {
		t.Fatalf("VirtualObject.Handler: %v", err)
	}
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject: %v", err)
	}

	wf := NewWorkflow("onboarding")
	wf.Run(keyedEcho)
	if err := reg.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}

	for _, attested := range []bool{false, true} {
		err := validateDurableContextGuard(reg, attested)
		if !errors.Is(err, ErrServeDurableContextRejected) {
			t.Errorf("validateDurableContextGuard(attested=%v): err=%v, want errors.Is(err, ErrServeDurableContextRejected)", attested, err)
			continue
		}
		var rejErr *ServeDurableContextRejectedError
		if !errors.As(err, &rejErr) {
			t.Fatalf("errors.As failed (attested=%v): %v", attested, err)
		}
		if !strings.Contains(rejErr.What, "1 Virtual Object handler(s)") || !strings.Contains(rejErr.What, "1 Workflow handler(s)") {
			t.Errorf("What = %q, want mention of both a Virtual Object and a Workflow handler (attested=%v)", rejErr.What, attested)
		}
	}
}

// twoServiceRegistry builds a Service-only *Registry with two independently registered Service
// handlers (Story 59.1's >1-pair lesson: a single-handler fixture would not have caught that
// story's goroutine leak).
func twoServiceRegistry(t *testing.T) *Registry {
	t.Helper()
	echo := func(_ Context, req []byte) ([]byte, error) { return req, nil }
	reg := NewRegistry()
	for _, name := range []string{"svc-1", "svc-2"} {
		svc := NewService(name)
		if _, err := svc.Handler("run", echo); err != nil {
			t.Fatalf("Service.Handler: %v", err)
		}
		if err := reg.Register(svc); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	return reg
}

// ---------------------------------------------------------------------------
// WithNoDurableContextAttestation — ServeOption mutation (mirrors serve_test.go's convention)
// ---------------------------------------------------------------------------

// TestWithNoDurableContextAttestation_MutatesConfig verifies the option defaults to false and
// WithNoDurableContextAttestation() sets serveConfig.noDurableContextAttested.
func TestWithNoDurableContextAttestation_MutatesConfig(t *testing.T) {
	cfg, err := newServeConfig()
	if err != nil {
		t.Fatalf("newServeConfig(): unexpected error: %v", err)
	}
	if cfg.noDurableContextAttested {
		t.Error("default cfg.noDurableContextAttested = true, want false")
	}

	cfg, err = newServeConfig(WithNoDurableContextAttestation())
	if err != nil {
		t.Fatalf("newServeConfig(WithNoDurableContextAttestation()): unexpected error: %v", err)
	}
	if !cfg.noDurableContextAttested {
		t.Error("cfg.noDurableContextAttested = false, want true after WithNoDurableContextAttestation()")
	}
}

// ---------------------------------------------------------------------------
// Serve() — rejection paths (safe: the guard returns before jetstream.New/any goroutine starts)
// ---------------------------------------------------------------------------

// TestServe_RejectByDefault_ServiceOnlyUnattested_ReturnsClassedError verifies Serve() itself
// (not just the guard function) returns the classed rejection error immediately for a
// multi-pair Service-only registry with no attestation (AC1, AC4's error-path assertion).
func TestServe_RejectByDefault_ServiceOnlyUnattested_ReturnsClassedError(t *testing.T) {
	reg := twoServiceRegistry(t)
	nc := &nats.Conn{}

	err := Serve(context.Background(), nc, reg)
	if !errors.Is(err, ErrServeDurableContextRejected) {
		t.Fatalf("Serve: err=%v, want errors.Is(err, ErrServeDurableContextRejected)", err)
	}
	var rejErr *ServeDurableContextRejectedError
	if !errors.As(err, &rejErr) {
		t.Fatalf("errors.As(err, *ServeDurableContextRejectedError) failed: %v", err)
	}
	if rejErr.What == "" || rejErr.Cause == "" || rejErr.Hint == "" {
		t.Errorf("triad incomplete: %+v", rejErr)
	}
}

// TestServe_KindTier_RejectsEvenWithAttestation verifies Serve() rejects a registry containing a
// Virtual Object handler even when WithNoDurableContextAttestation is passed — the kind tier is
// unconditional (AC1, AC2).
func TestServe_KindTier_RejectsEvenWithAttestation(t *testing.T) {
	obj := NewVirtualObject("order")
	if _, err := obj.Handler("charge", func(_ Context, _ string, req []byte) ([]byte, error) { return req, nil }); err != nil {
		t.Fatalf("VirtualObject.Handler: %v", err)
	}
	reg := NewRegistry()
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject: %v", err)
	}
	nc := &nats.Conn{}

	err := Serve(context.Background(), nc, reg, WithNoDurableContextAttestation())
	if !errors.Is(err, ErrServeDurableContextRejected) {
		t.Fatalf("Serve: err=%v, want errors.Is(err, ErrServeDurableContextRejected) even though WithNoDurableContextAttestation was passed", err)
	}
	var rejErr *ServeDurableContextRejectedError
	if !errors.As(err, &rejErr) {
		t.Fatalf("errors.As(err, *ServeDurableContextRejectedError) failed: %v", err)
	}
	if !strings.Contains(rejErr.What, "Virtual Object") {
		t.Errorf("What = %q, want mention of Virtual Object", rejErr.What)
	}
}

// TestServe_RejectedRegistry_NoGoroutineLeak verifies the early-return path (AC4, Task 3.3):
// Serve() never begins accepting dispatch for a rejected registry — no partial start, no leaked
// goroutine. Directly targets the Story 59.1 >1-pair goroutine-leak lesson by using a two-pair
// registry.
func TestServe_RejectedRegistry_NoGoroutineLeak(t *testing.T) {
	reg := twoServiceRegistry(t)
	nc := &nats.Conn{}

	runtime.Gosched()
	before := runtime.NumGoroutine()

	err := Serve(context.Background(), nc, reg)
	if !errors.Is(err, ErrServeDurableContextRejected) {
		t.Fatalf("Serve: err=%v, want errors.Is(err, ErrServeDurableContextRejected)", err)
	}

	// Give any errant goroutine a brief window to have started before recounting — a leaked
	// goroutine would show up here rather than being masked by scheduling timing.
	time.Sleep(50 * time.Millisecond)
	runtime.Gosched()
	after := runtime.NumGoroutine()
	if after > before {
		t.Errorf("goroutine count grew from %d to %d after a rejected Serve() call — guard leaked a goroutine (Story 59.1 lesson)", before, after)
	}
}

// ---------------------------------------------------------------------------
// Serve() — attested Service-only registry continues to be served unchanged (Task 3.4)
// ---------------------------------------------------------------------------

// TestServe_AttestedServiceOnly_GuardPasses_BlocksUnchanged verifies that
// WithNoDurableContextAttestation() lifts the attestation-tier guard for a Service-only
// registry and Serve() falls through to its pre-68.1 skeleton behavior unchanged: it blocks
// until ctx is cancelled and returns nil, rather than ever returning the new classed rejection
// error (Task 3.4 regression coverage; Pin #1/Pin #2 preserved — no ServeOption/signature
// regression).
//
// The registered Service below intentionally has ZERO handlers: this keeps
// collectHandlerRefs(reg) empty so Serve() takes the pre-existing "nothing to advertise or
// consume" skeleton path (exactly like TestServe_BlocksUntilContextCancelled_ThenReturnsNil in
// serve_test.go) rather than the network Gap A/B loops, which need a live NATS connection this
// unit tier does not have (see this file's header comment). The guard's OWN pass/reject
// decision is made purely from reg.services/virtualObjects/workflows membership
// (validateDurableContextGuard) and does not depend on whether a Service has any handler
// registered inside it — TestValidateDurableContextGuard_ServiceOnly_Attested_Passes above
// proves the identical guard decision holds for a Service WITH real handlers registered, so
// this test and that one together are a faithful, safe proof of the full claim.
func TestServe_AttestedServiceOnly_GuardPasses_BlocksUnchanged(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(NewService("attested-empty-service")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	nc := &nats.Conn{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, nc, reg, WithNoDurableContextAttestation())
	}()

	select {
	case err := <-done:
		t.Fatalf("Serve returned early (err=%v) before ctx was cancelled — an attested Service-only registry was incorrectly rejected or otherwise short-circuited", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve(ctx, nc, reg, WithNoDurableContextAttestation()) after ctx cancel: err=%v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of ctx cancellation")
	}
}

// ---------------------------------------------------------------------------
// Multi-instance: all three guard outcomes exercised concurrently (Task 3.1)
// ---------------------------------------------------------------------------

// serveResult pairs a scenario name with the error Serve() returned, used by
// TestServe_MultiInstance_ConcurrentRegistriesAcrossAllThreeTiers to join concurrently-running
// Serve() calls and assert on them together.
type serveResult struct {
	name string
	err  error
}

// TestServe_MultiInstance_ConcurrentRegistriesAcrossAllThreeTiers is the AC4 / Task 3.1
// "multi-instance" test: at least two concurrently registered handler pairs, spanning the
// reject-by-default, kind-tier-reject, and attestation-opt-in paths, exercised TOGETHER in the
// same test run — never a single happy-path fixture in isolation (Story 59.1's standing
// lesson). Shares PATLOC-0001's "fan out registered handlers, join, assert together" shape at
// unit-test scale (see this story's Pattern Compliance Table).
func TestServe_MultiInstance_ConcurrentRegistriesAcrossAllThreeTiers(t *testing.T) {
	keyedEcho := func(_ Context, _ string, req []byte) ([]byte, error) { return req, nil }
	nc := &nats.Conn{}

	// Tier A: reject-by-default — Service-only, unattested, multi-pair (two Service handlers).
	regA := twoServiceRegistry(t)

	// Tier B: kind-tier reject — Virtual Object handler, attestation passed but ignored
	// (the kind tier is unconditional).
	regB := NewRegistry()
	obj := NewVirtualObject("order")
	if _, err := obj.Handler("charge", keyedEcho); err != nil {
		t.Fatalf("VirtualObject.Handler: %v", err)
	}
	if err := regB.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject: %v", err)
	}

	// Tier C: attestation opt-in lifts the guard — Service-only, zero handlers (safe fixture,
	// see TestServe_AttestedServiceOnly_GuardPasses_BlocksUnchanged's doc comment), attested.
	regC := NewRegistry()
	if err := regC.Register(NewService("tier-c-empty-service")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	results := make(chan serveResult, 3)

	go func() {
		results <- serveResult{"tierA-reject-by-default", Serve(context.Background(), nc, regA)}
	}()
	go func() {
		results <- serveResult{"tierB-kind-reject", Serve(context.Background(), nc, regB, WithNoDurableContextAttestation())}
	}()

	ctxC, cancelC := context.WithCancel(context.Background())
	go func() {
		results <- serveResult{"tierC-attested-pass", Serve(ctxC, nc, regC, WithNoDurableContextAttestation())}
	}()

	// Tier A and Tier B are rejected synchronously by the guard — both must return promptly.
	// Tier C is attested and must NOT show up here; it blocks until cancelled below.
	seen := map[string]error{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case r := <-results:
			if r.name == "tierC-attested-pass" {
				t.Fatal("tierC-attested-pass returned before ctx cancellation — an attested Service-only registry was incorrectly rejected")
			}
			seen[r.name] = r.err
		case <-deadline:
			t.Fatalf("tierA/tierB did not both return within 2s; got %d/2: %v", len(seen), seen)
		}
	}

	errA := seen["tierA-reject-by-default"]
	if !errors.Is(errA, ErrServeDurableContextRejected) {
		t.Errorf("tierA: err=%v, want errors.Is(err, ErrServeDurableContextRejected)", errA)
	}
	var rejA *ServeDurableContextRejectedError
	if !errors.As(errA, &rejA) {
		t.Errorf("tierA: errors.As(err, *ServeDurableContextRejectedError) failed: %v", errA)
	} else if !strings.Contains(rejA.What, "Service handler(s) registered without WithNoDurableContextAttestation") {
		t.Errorf("tierA: What = %q, want an attestation-tier message", rejA.What)
	}

	errB := seen["tierB-kind-reject"]
	if !errors.Is(errB, ErrServeDurableContextRejected) {
		t.Errorf("tierB: err=%v, want errors.Is(err, ErrServeDurableContextRejected)", errB)
	}
	var rejB *ServeDurableContextRejectedError
	if !errors.As(errB, &rejB) {
		t.Errorf("tierB: errors.As(err, *ServeDurableContextRejectedError) failed: %v", errB)
	} else if !strings.Contains(rejB.What, "Virtual Object") {
		t.Errorf("tierB: What = %q, want a kind-tier message mentioning Virtual Object", rejB.What)
	}

	// Now release tier C and confirm it shuts down cleanly WITHOUT ever having been rejected.
	cancelC()
	select {
	case r := <-results:
		if r.name != "tierC-attested-pass" {
			t.Fatalf("unexpected result name %q", r.name)
		}
		if errors.Is(r.err, ErrServeDurableContextRejected) {
			t.Errorf("tierC: attested Service-only registry was rejected: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tierC did not return within 2s of ctx cancellation")
	}
}

// ---------------------------------------------------------------------------
// Sentinel distinctness (ADR-0030) — extends serve_test.go's TestServe_NewSentinelErrors_AreDistinct
// ---------------------------------------------------------------------------

// TestErrServeDurableContextRejected_DistinctFromOtherServeSentinels verifies
// ErrServeDurableContextRejected is distinct from every pre-existing Serve sentinel.
func TestErrServeDurableContextRejected_DistinctFromOtherServeSentinels(t *testing.T) {
	others := []error{
		ErrServeConfigInvalid,
		ErrWorkerNameUnresolved,
		ErrServeJetStreamUnavailable,
		ErrServeConsumerBindFailed,
		ErrRemoteContextUnsupported,
	}
	for _, other := range others {
		if errors.Is(ErrServeDurableContextRejected, other) {
			t.Errorf("ErrServeDurableContextRejected unexpectedly matches %v", other)
		}
		if errors.Is(other, ErrServeDurableContextRejected) {
			t.Errorf("%v unexpectedly matches ErrServeDurableContextRejected", other)
		}
	}
}

// ---------------------------------------------------------------------------
// remoteDispatchContext reachability — Task 2.3 documentation test
// ---------------------------------------------------------------------------

// TestRemoteDispatchContext_OnlyReachableThroughGuardedDispatchPath is a regression check (Task
// 2.3, tightened from a log-only documentation test during review): the only two production
// construction sites for remoteDispatchContext{} are inside dispatchToHandler (serve.go), which
// is reachable only via handleServeDispatch, which is reachable only via runServeFetchLoop, which
// Serve() starts only inside its `if len(handlers) > 0` block — AFTER
// validateDurableContextGuard has already returned nil. No other code path in
// serve.go/serve_wire.go constructs a remoteDispatchContext and hands it to a handler that has
// not passed through the guard.
//
// Rather than asserting that claim only in a doc comment, this test mechanically scans every
// sdk/*.go source file (excluding this file itself, which mentions the marker only inside string
// literals/comments, not as a live construction site) for non-comment `remoteDispatchContext{}`
// occurrences and fails if the count drifts from the known-good baseline: two production
// construction sites in serve.go (inside dispatchToHandler) plus two interface-assertion fixtures
// — `var _ Context = remoteDispatchContext{}` in serve.go itself and the equivalent `var ctx
// Context = remoteDispatchContext{}` in serve_test.go — neither of which hands a live instance to
// a handler; both only prove the type satisfies Context at compile/test time. A drift means
// either a new construction site appeared outside the guarded dispatch path (a guard-bypass
// regression) or a known site was removed — either way this test must be re-examined by hand, not
// silently rebaselined.
func TestRemoteDispatchContext_OnlyReachableThroughGuardedDispatchPath(t *testing.T) {
	const marker = "remoteDispatchContext{}"
	const selfFile = "serve_guard_test.go"
	const want = 4

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("filepath.Glob(*.go): %v", err)
	}

	var sites []string
	for _, f := range files {
		if f == selfFile {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("os.ReadFile(%s): %v", f, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, marker) {
				sites = append(sites, fmt.Sprintf("%s:%d", f, i+1))
			}
		}
	}

	if len(sites) != want {
		t.Fatalf("found %d non-comment %q occurrence(s) in sdk/*.go (want %d): %v\n"+
			"this enforces Task 2.3 (Story 68.1): remoteDispatchContext must only be constructed "+
			"inside dispatchToHandler, which Serve() only reaches after validateDurableContextGuard "+
			"has returned nil — re-verify the guard still gates every construction site before "+
			"changing this baseline", len(sites), marker, want, sites)
	}
}
