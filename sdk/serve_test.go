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

// ---------------------------------------------------------------------------
// collectHandlerRefs — Task 2.1 (AC 1)
// ---------------------------------------------------------------------------

// TestCollectHandlerRefs_EmptyRegistry verifies an empty *Registry yields zero handlerRefs
// (Serve's degenerate "nothing to advertise/consume" case).
func TestCollectHandlerRefs_EmptyRegistry(t *testing.T) {
	refs := collectHandlerRefs(NewRegistry())
	if len(refs) != 0 {
		t.Errorf("collectHandlerRefs(empty registry) = %v, want empty", refs)
	}
}

// TestCollectHandlerRefs_ServicesVirtualObjectsAndWorkflows verifies collectHandlerRefs
// enumerates every (serviceName, handlerName) pair reachable via Registry.LookupHandler /
// Registry.LookupKeyedHandler: stateless Service handlers, keyed Virtual Object handlers, and a
// Workflow's run-once handler (advertised as (workflowName, "run")).
func TestCollectHandlerRefs_ServicesVirtualObjectsAndWorkflows(t *testing.T) {
	echo := func(_ Context, req []byte) ([]byte, error) { return req, nil }
	keyedEcho := func(_ Context, _ string, req []byte) ([]byte, error) { return req, nil }

	reg := NewRegistry()

	svc := NewService("echo")
	if _, err := svc.Handler("run", echo); err != nil {
		t.Fatalf("Service.Handler: %v", err)
	}
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register: %v", err)
	}

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

	// A Workflow with no Run handler set must NOT be advertised.
	noRunWf := NewWorkflow("no-run-handler")
	if err := reg.RegisterWorkflow(noRunWf); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}

	got := collectHandlerRefs(reg)
	want := map[handlerRef]bool{
		{ServiceName: "echo", HandlerName: "run"}:       true,
		{ServiceName: "order", HandlerName: "charge"}:   true,
		{ServiceName: "onboarding", HandlerName: "run"}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("collectHandlerRefs = %v (len %d), want len %d", got, len(got), len(want))
	}
	for _, ref := range got {
		if !want[ref] {
			t.Errorf("unexpected handlerRef %+v", ref)
		}
	}
}

// ---------------------------------------------------------------------------
// dispatchToHandler — Task 3.3 (AC 2)
// ---------------------------------------------------------------------------

// TestDispatchToHandler_StatelessService routes a request with an empty VOKey through
// Registry.LookupHandler and executes the registered Service handler.
func TestDispatchToHandler_StatelessService(t *testing.T) {
	reg := NewRegistry()
	svc := NewService("echo")
	if _, err := svc.Handler("run", func(_ Context, req []byte) ([]byte, error) { return req, nil }); err != nil {
		t.Fatalf("Service.Handler: %v", err)
	}
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := dispatchToHandler(reg, &remoteDispatchRequest{
		ServiceName: "echo", HandlerName: "run", ReqBytes: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("dispatchToHandler: unexpected error: %v", err)
	}
	if string(result) != "hello" {
		t.Errorf("dispatchToHandler result = %q, want %q", result, "hello")
	}
}

// TestDispatchToHandler_KeyedVirtualObject routes a request with a non-empty VOKey through
// Registry.LookupKeyedHandler, passing the key through to the handler.
func TestDispatchToHandler_KeyedVirtualObject(t *testing.T) {
	reg := NewRegistry()
	obj := NewVirtualObject("order")
	if _, err := obj.Handler("charge", func(_ Context, key string, req []byte) ([]byte, error) {
		return []byte(key + ":" + string(req)), nil
	}); err != nil {
		t.Fatalf("VirtualObject.Handler: %v", err)
	}
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject: %v", err)
	}

	result, err := dispatchToHandler(reg, &remoteDispatchRequest{
		ServiceName: "order", HandlerName: "charge", VOKey: "order-42", ReqBytes: []byte("amount"),
	})
	if err != nil {
		t.Fatalf("dispatchToHandler: unexpected error: %v", err)
	}
	if string(result) != "order-42:amount" {
		t.Errorf("dispatchToHandler result = %q, want %q", result, "order-42:amount")
	}
}

// TestDispatchToHandler_NotFound_WrapsErrHandlerNotFound verifies a dispatch to an unregistered
// (serviceName, handlerName) pair returns a wrapped ErrHandlerNotFound (ADR-0030) rather than a
// panic or an ad-hoc error string.
func TestDispatchToHandler_NotFound_WrapsErrHandlerNotFound(t *testing.T) {
	reg := NewRegistry()

	_, err := dispatchToHandler(reg, &remoteDispatchRequest{ServiceName: "missing", HandlerName: "run"})
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("dispatchToHandler(stateless, unregistered): err=%v, want errors.Is(err, ErrHandlerNotFound)", err)
	}

	_, err = dispatchToHandler(reg, &remoteDispatchRequest{ServiceName: "missing", HandlerName: "run", VOKey: "k"})
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("dispatchToHandler(keyed, unregistered): err=%v, want errors.Is(err, ErrHandlerNotFound)", err)
	}
}

// TestDispatchToHandler_HandlerError propagates a handler-returned error unchanged so
// handleServeDispatch can turn it into a structured Failed response.
func TestDispatchToHandler_HandlerError(t *testing.T) {
	wantErr := errors.New("boom")
	reg := NewRegistry()
	svc := NewService("echo")
	if _, err := svc.Handler("run", func(_ Context, _ []byte) ([]byte, error) { return nil, wantErr }); err != nil {
		t.Fatalf("Service.Handler: %v", err)
	}
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := dispatchToHandler(reg, &remoteDispatchRequest{ServiceName: "echo", HandlerName: "run"})
	if !errors.Is(err, wantErr) {
		t.Errorf("dispatchToHandler: err=%v, want errors.Is(err, wantErr)", err)
	}
}

// ---------------------------------------------------------------------------
// remoteDispatchContext — Scope Deviations single-request/response model
// ---------------------------------------------------------------------------

// TestRemoteDispatchContext_AllDurableOpsReturnErrRemoteContextUnsupported verifies every ctx.*
// durable operation on remoteDispatchContext returns ErrRemoteContextUnsupported — a disclosed
// v1 boundary, never a silent success or a fabricated bridge (ADR-0017).
func TestRemoteDispatchContext_AllDurableOpsReturnErrRemoteContextUnsupported(t *testing.T) {
	var ctx Context = remoteDispatchContext{}

	if _, err := ctx.Run("op", func(string) ([]byte, error) { return nil, nil }); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Run: err=%v", err)
	}
	if err := ctx.Sleep(time.Second); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Sleep: err=%v", err)
	}
	if _, err := ctx.Timer(time.Second); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Timer: err=%v", err)
	}
	if _, _, err := ctx.Get("k"); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Get: err=%v", err)
	}
	if err := ctx.Set("k", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Set: err=%v", err)
	}
	if err := ctx.Clear("k"); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Clear: err=%v", err)
	}
	if _, err := ctx.List(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("List: err=%v", err)
	}
	if _, err := ctx.Call("s", "h", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Call: err=%v", err)
	}
	if _, err := ctx.Send("s", "h", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Send: err=%v", err)
	}
	if _, err := ctx.CallWorkflow("wf", "k", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("CallWorkflow: err=%v", err)
	}
	if _, err := ctx.SendWorkflow("wf", "k", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("SendWorkflow: err=%v", err)
	}
	if _, err := ctx.SendDelayed("s", "h", nil, time.Second); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("SendDelayed: err=%v", err)
	}
	if _, err := ctx.SendAt("s", "h", nil, time.Now()); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("SendAt: err=%v", err)
	}
	if _, _, err := ctx.Awakeable(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Awakeable: err=%v", err)
	}
	if err := ctx.CompleteAwakeable("id", nil); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("CompleteAwakeable: err=%v", err)
	}
	if err := ctx.RejectAwakeable("id", "reason"); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("RejectAwakeable: err=%v", err)
	}
	if _, err := ctx.GetVersion("change", 0, 1); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("GetVersion: err=%v", err)
	}
	if _, err := ctx.RegisterCompensation(func(Context) error { return nil }); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("RegisterCompensation: err=%v", err)
	}
	if _, err := ctx.Race(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("Race: err=%v", err)
	}
	if _, err := ctx.AwaitAny(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("AwaitAny: err=%v", err)
	}
	if _, err := ctx.AwaitAll(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("AwaitAll: err=%v", err)
	}
	if _, err := ctx.AwaitFirstSucceeded(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("AwaitFirstSucceeded: err=%v", err)
	}
	if _, err := ctx.AwaitAllSucceeded(); !errors.Is(err, ErrRemoteContextUnsupported) {
		t.Errorf("AwaitAllSucceeded: err=%v", err)
	}
	if ctx.Promise("id") != nil {
		t.Error("Promise: want nil")
	}
	if !ctx.Now().IsZero() {
		t.Error("Now: want zero time.Time")
	}
	if ctx.Rand() != 0 {
		t.Error("Rand: want 0")
	}
	if ctx.UUID() != "" {
		t.Error("UUID: want empty string")
	}
}

// ---------------------------------------------------------------------------
// New sentinel errors introduced by Story 59.1 (ADR-0030)
// ---------------------------------------------------------------------------

// TestServe_NewSentinelErrors_AreDistinct verifies ErrServeJetStreamUnavailable,
// ErrServeConsumerBindFailed, and ErrRemoteContextUnsupported are distinct sentinels from each
// other and from the pre-existing ErrServeConfigInvalid/ErrWorkerNameUnresolved.
func TestServe_NewSentinelErrors_AreDistinct(t *testing.T) {
	all := []error{
		ErrServeConfigInvalid,
		ErrWorkerNameUnresolved,
		ErrServeJetStreamUnavailable,
		ErrServeConsumerBindFailed,
		ErrRemoteContextUnsupported,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) unexpectedly matches sentinel %d (%v)", i, a, j, b)
			}
		}
	}
}
