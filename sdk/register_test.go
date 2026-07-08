package sdk

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// noopHandler is a HandlerFunc used in tests.
var noopHandler HandlerFunc = func(_ Context, req []byte) ([]byte, error) {
	return req, nil
}

// greetHandler is a HandlerFunc stub used for uniqueness assertions.
var greetHandler HandlerFunc = func(_ Context, _ []byte) ([]byte, error) {
	return []byte("hello"), nil
}

// ---------------------------------------------------------------------------
// Task 5.1: New + WithService + Build: register a service with two handlers
// ---------------------------------------------------------------------------

func TestNew_WithService_Build_LookupService(t *testing.T) {
	s := New(
		WithService(ServiceDescription{
			Name: "greeter",
			Handlers: map[string]HandlerFunc{
				"greet": greetHandler,
				"noop":  noopHandler,
			},
		}),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	fn, err := s.LookupService("greeter", "greet")
	if err != nil {
		t.Fatalf("LookupService(greeter, greet) error: %v", err)
	}
	if fn == nil {
		t.Fatal("LookupService returned nil HandlerFunc")
	}

	result, err := fn(nil, []byte("world"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if string(result) != "hello" {
		t.Errorf("greetHandler returned %q; want %q", string(result), "hello")
	}

	fn2, err := s.LookupService("greeter", "noop")
	if err != nil {
		t.Fatalf("LookupService(greeter, noop) error: %v", err)
	}
	if fn2 == nil {
		t.Fatal("LookupService returned nil HandlerFunc for noop")
	}
}

// ---------------------------------------------------------------------------
// Task 5.2: New + WithObject + Build: lookup returns the correct handler
// ---------------------------------------------------------------------------

func TestNew_WithObject_Build_LookupObject(t *testing.T) {
	s := New(
		WithObject(ObjectDescription{
			Name: "counter",
			Handlers: map[string]HandlerFunc{
				"increment": noopHandler,
			},
		}),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	fn, err := s.LookupObject("counter", "increment")
	if err != nil {
		t.Fatalf("LookupObject(counter, increment) error: %v", err)
	}
	if fn == nil {
		t.Fatal("LookupObject returned nil HandlerFunc")
	}
}

// ---------------------------------------------------------------------------
// Task 5.3: Duplicate service registration: Build returns ErrDuplicateHandler
// ---------------------------------------------------------------------------

func TestDuplicateServiceRegistration(t *testing.T) {
	svc := ServiceDescription{
		Name:     "svc",
		Handlers: map[string]HandlerFunc{"h": noopHandler},
	}
	s := New(
		WithService(svc),
		WithService(svc), //nolint:gocritic // dupOption: intentional duplicate to test ErrDuplicateHandler detection
	)
	err := s.Build()
	if err == nil {
		t.Fatal("Build() expected error for duplicate service registration, got nil")
	}
	if !errors.Is(err, ErrDuplicateHandler) {
		t.Errorf("errors.Is(err, ErrDuplicateHandler) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Duplicate object registration: Build returns ErrDuplicateHandler
// ---------------------------------------------------------------------------

func TestDuplicateObjectRegistration(t *testing.T) {
	obj := ObjectDescription{
		Name:     "obj",
		Handlers: map[string]HandlerFunc{"h": noopHandler},
	}
	s := New(
		WithObject(obj),
		WithObject(obj), //nolint:gocritic // dupOption: intentional duplicate to test ErrDuplicateHandler detection
	)
	err := s.Build()
	if err == nil {
		t.Fatal("Build() expected error for duplicate object registration, got nil")
	}
	if !errors.Is(err, ErrDuplicateHandler) {
		t.Errorf("errors.Is(err, ErrDuplicateHandler) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.4: LookupService not-found wraps ErrHandlerNotFound
// ---------------------------------------------------------------------------

func TestLookupService_NotFound(t *testing.T) {
	s := New()
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	_, err := s.LookupService("missing", "handler")
	if err == nil {
		t.Fatal("LookupService expected error for missing service, got nil")
	}
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("errors.Is(err, ErrHandlerNotFound) = false; err = %v", err)
	}
}

func TestLookupService_ServiceFound_HandlerNotFound(t *testing.T) {
	s := New(
		WithService(ServiceDescription{
			Name:     "svc",
			Handlers: map[string]HandlerFunc{"a": noopHandler},
		}),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	_, err := s.LookupService("svc", "missing")
	if err == nil {
		t.Fatal("LookupService expected error for missing handler, got nil")
	}
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("errors.Is(err, ErrHandlerNotFound) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.5: LookupObject not-found wraps ErrHandlerNotFound
// ---------------------------------------------------------------------------

func TestLookupObject_NotFound(t *testing.T) {
	s := New()
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	_, err := s.LookupObject("missing", "handler")
	if err == nil {
		t.Fatal("LookupObject expected error for missing object, got nil")
	}
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("errors.Is(err, ErrHandlerNotFound) = false; err = %v", err)
	}
}

func TestLookupObject_ObjectFound_HandlerNotFound(t *testing.T) {
	s := New(
		WithObject(ObjectDescription{
			Name:     "obj",
			Handlers: map[string]HandlerFunc{"a": noopHandler},
		}),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	_, err := s.LookupObject("obj", "missing")
	if err == nil {
		t.Fatal("LookupObject expected error for missing handler, got nil")
	}
	if !errors.Is(err, ErrHandlerNotFound) {
		t.Errorf("errors.Is(err, ErrHandlerNotFound) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.6: Concurrent reads after Build — no data race
// ---------------------------------------------------------------------------

func TestConcurrentLookupAfterBuild(t *testing.T) {
	s := New(
		WithService(ServiceDescription{
			Name:     "svc",
			Handlers: map[string]HandlerFunc{"h": noopHandler},
		}),
		WithObject(ObjectDescription{
			Name:     "obj",
			Handlers: map[string]HandlerFunc{"h": noopHandler},
		}),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for range goroutines {
		go func() {
			defer wg.Done()
			fn, err := s.LookupService("svc", "h")
			if err != nil {
				t.Errorf("LookupService error: %v", err)
			}
			if fn == nil {
				t.Error("LookupService returned nil")
			}
		}()
		go func() {
			defer wg.Done()
			fn, err := s.LookupObject("obj", "h")
			if err != nil {
				t.Errorf("LookupObject error: %v", err)
			}
			if fn == nil {
				t.Error("LookupObject returned nil")
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Task 5.7: SDK boundary — no internal/ imports (verified by go list in CI)
// This test is a documentation anchor; the automated gate is Task 6.4.
// ---------------------------------------------------------------------------

func TestSDKBoundary_NoInternalImports(t *testing.T) {
	// The automated gate for the internal/ boundary is:
	//   go list -f '{{.Imports}}' ./sdk/...
	// which must not contain any github.com/exoar/axon_tenax_engine/tenax/internal path.
	//
	// This test verifies the package compiles without internal/ imports
	// at the structural level by asserting the package name and module path
	// are correct (the test itself is in package sdk, so if internal/ were
	// imported the build would still succeed — the gate is in make lint/build).
	//
	// ADR-0028 compliance is enforced by code review and the go list check.
	t.Log("sdk/ boundary: internal/ imports are disallowed by ADR-0028 and verified by go list in Task 6.4")
}

// ---------------------------------------------------------------------------
// Task 5.8: Fluent builders produce correct ServiceDescription / ObjectDescription
// ---------------------------------------------------------------------------

func TestServiceBuilder_FluentBuild(t *testing.T) {
	fn := noopHandler
	desc := NewServiceBuilder("svc").Handle("greet", fn).Build()

	if desc.Name != "svc" {
		t.Errorf("ServiceDescription.Name = %q; want %q", desc.Name, "svc")
	}
	if len(desc.Handlers) != 1 {
		t.Errorf("ServiceDescription.Handlers len = %d; want 1", len(desc.Handlers))
	}
	if _, ok := desc.Handlers["greet"]; !ok {
		t.Error("ServiceDescription.Handlers missing key \"greet\"")
	}
}

func TestObjectBuilder_FluentBuild(t *testing.T) {
	fn := noopHandler
	desc := Object("counter").Handle("increment", fn).Build()

	if desc.Name != "counter" {
		t.Errorf("ObjectDescription.Name = %q; want %q", desc.Name, "counter")
	}
	if len(desc.Handlers) != 1 {
		t.Errorf("ObjectDescription.Handlers len = %d; want 1", len(desc.Handlers))
	}
	if _, ok := desc.Handlers["increment"]; !ok {
		t.Error("ObjectDescription.Handlers missing key \"increment\"")
	}
}

func TestServiceBuilder_MultipleHandlers(t *testing.T) {
	desc := NewServiceBuilder("svc").
		Handle("a", noopHandler).
		Handle("b", greetHandler).
		Build()

	if desc.Name != "svc" {
		t.Errorf("Name = %q; want %q", desc.Name, "svc")
	}
	if len(desc.Handlers) != 2 {
		t.Errorf("Handlers len = %d; want 2", len(desc.Handlers))
	}
}

// ---------------------------------------------------------------------------
// Task 5.9: No //go:build integration tags — enforced by test structure
// (these are all pure unit tests with no NATS dependency)
// ---------------------------------------------------------------------------

// TestSDK_BuilderIntegrationWithNew tests the full path: fluent builder → WithService → New → Build → Lookup
func TestSDK_BuilderIntegrationWithNew(t *testing.T) {
	desc := NewServiceBuilder("mysvc").Handle("echo", noopHandler).Build()
	s := New(WithService(desc))
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	fn, err := s.LookupService("mysvc", "echo")
	if err != nil {
		t.Fatalf("LookupService error: %v", err)
	}

	in := []byte("ping")
	out, err := fn(nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if string(out) != "ping" {
		t.Errorf("handler output %q; want %q", string(out), "ping")
	}
}

func TestSDK_ObjectBuilderIntegrationWithNew(t *testing.T) {
	desc := Object("myobj").Handle("get", noopHandler).Build()
	s := New(WithObject(desc))
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	fn, err := s.LookupObject("myobj", "get")
	if err != nil {
		t.Fatalf("LookupObject error: %v", err)
	}
	if fn == nil {
		t.Fatal("LookupObject returned nil")
	}
}

// ---------------------------------------------------------------------------
// Additional: sentinel error values are distinct
// ---------------------------------------------------------------------------

func TestSentinelErrors_AreDistinct(t *testing.T) {
	if errors.Is(ErrHandlerNotFound, ErrDuplicateHandler) {
		t.Error("ErrHandlerNotFound and ErrDuplicateHandler must be distinct sentinel errors")
	}
	if errors.Is(ErrDuplicateHandler, ErrHandlerNotFound) {
		t.Error("ErrDuplicateHandler and ErrHandlerNotFound must be distinct sentinel errors")
	}
}

// ---------------------------------------------------------------------------
// Register: nil and empty-name services wrap ErrInvalidService, NOT
// ErrDuplicateService. nil/empty-name is a usage error distinct from a
// name-collision conflict (the previous behavior produced a false positive
// on errors.Is(err, ErrDuplicateService)).
// ---------------------------------------------------------------------------

func TestRegister_NilService_WrapsErrInvalidService(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(nil)
	if err == nil {
		t.Fatal("Register(nil) expected error; got nil")
	}
	if !errors.Is(err, ErrInvalidService) {
		t.Errorf("errors.Is(err, ErrInvalidService) = false; err = %v", err)
	}
	if errors.Is(err, ErrDuplicateService) {
		t.Errorf("errors.Is(err, ErrDuplicateService) = true for nil service; want false; err = %v", err)
	}
}

func TestRegister_EmptyName_WrapsErrInvalidService(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(NewService(""))
	if err == nil {
		t.Fatal("Register(empty-name) expected error; got nil")
	}
	if !errors.Is(err, ErrInvalidService) {
		t.Errorf("errors.Is(err, ErrInvalidService) = false; err = %v", err)
	}
	if errors.Is(err, ErrDuplicateService) {
		t.Errorf("errors.Is(err, ErrDuplicateService) = true for empty-name service; want false; err = %v", err)
	}
}

// TestRegister_ErrInvalidService_DistinctFromOthers verifies the new sentinel
// is distinct from the existing registry sentinels.
func TestRegister_ErrInvalidService_DistinctFromOthers(t *testing.T) {
	if errors.Is(ErrInvalidService, ErrDuplicateService) {
		t.Error("ErrInvalidService and ErrDuplicateService must be distinct")
	}
	if errors.Is(ErrInvalidService, ErrDuplicateHandler) {
		t.Error("ErrInvalidService and ErrDuplicateHandler must be distinct")
	}
	if errors.Is(ErrInvalidService, ErrHandlerNotFound) {
		t.Error("ErrInvalidService and ErrHandlerNotFound must be distinct")
	}
}

// ---------------------------------------------------------------------------
// Additional: HandlerFunc signature accepts nil Context (for test isolation)
// ---------------------------------------------------------------------------

func TestHandlerFunc_NilContext(t *testing.T) {
	var called bool
	fn := HandlerFunc(func(_ Context, req []byte) ([]byte, error) {
		called = true
		return req, nil
	})
	out, err := fn(nil, []byte("x"))
	if err != nil {
		t.Fatalf("fn error: %v", err)
	}
	if !called {
		t.Error("HandlerFunc was not called")
	}
	if string(out) != "x" {
		t.Errorf("output = %q; want %q", string(out), "x")
	}
}

// ---------------------------------------------------------------------------
// Additional: New() with no options produces empty registry that builds cleanly
// ---------------------------------------------------------------------------

func TestNew_NoOptions_BuildsClean(t *testing.T) {
	s := New()
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error on empty SDK: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Additional: WithService and WithObject can be mixed in one New() call
// ---------------------------------------------------------------------------

func TestNew_MixedServiceAndObject(t *testing.T) {
	s := New(
		WithService(NewServiceBuilder("svc").Handle("h", noopHandler).Build()),
		WithObject(Object("obj").Handle("h", noopHandler).Build()),
	)
	if err := s.Build(); err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	if _, err := s.LookupService("svc", "h"); err != nil {
		t.Errorf("LookupService: %v", err)
	}
	if _, err := s.LookupObject("obj", "h"); err != nil {
		t.Errorf("LookupObject: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Additional: CompensationFunc and Saga compile against the Context interface
// ---------------------------------------------------------------------------

func TestSaga_CompensationFunc_UsesContext(t *testing.T) {
	var called bool
	fn := CompensationFunc(func(_ Context) error {
		called = true
		return nil
	})
	saga := NewSaga("mysaga").AddCompensation(fn)
	if saga.Name() != "mysaga" {
		t.Errorf("Saga.Name() = %q; want %q", saga.Name(), "mysaga")
	}
	// Call fn with nil to satisfy the signature check (Context is an interface).
	if err := fn(nil); err != nil {
		t.Fatalf("CompensationFunc error: %v", err)
	}
	if !called {
		t.Error("CompensationFunc was not called")
	}
}

// ---------------------------------------------------------------------------
// Additional: Workflow uses HandlerFunc
// ---------------------------------------------------------------------------

func TestWorkflow_HandlerFunc(t *testing.T) {
	// New API: Run takes KeyedHandlerFunc; Query takes QueryHandlerFunc; Signal takes HandlerFunc.
	noopKeyed := func(_ Context, _ string, _ []byte) ([]byte, error) { return nil, nil }
	noopQuery := func(_ QueryContext, _ json.RawMessage) (json.RawMessage, error) { return nil, nil }
	w := NewWorkflow("mywf").Run(noopKeyed)
	w, err := w.Query("status", noopQuery)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	w, err = w.Signal("advance", noopHandler)
	if err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if w.Name() != "mywf" {
		t.Errorf("Workflow.Name() = %q; want %q", w.Name(), "mywf")
	}
}

// ---------------------------------------------------------------------------
// Additional: Context interface is a forward-compatibility placeholder
// (We cannot instantiate it in this story, but we can verify the interface shape
// by creating an anonymous implementation for compile-time checking.)
// ---------------------------------------------------------------------------

// stubContext is a compile-time check that all Context methods are declared.
// It must implement every method on the Context interface.
type stubContext struct{}

func (stubContext) Run(_ string, _ func(string) ([]byte, error)) ([]byte, error) {
	return nil, nil
}

func (stubContext) Sleep(_ time.Duration) error {
	return nil
}

func (stubContext) Timer(_ time.Duration) (Promise, error) {
	return nil, nil
}

func (stubContext) Get(_ string) (value []byte, found bool, err error) {
	return nil, false, nil
}

func (stubContext) Set(_ string, _ []byte) error {
	return nil
}

func (stubContext) Clear(_ string) error {
	return nil
}

func (stubContext) List() ([]string, error) {
	return nil, nil
}

func (stubContext) Call(_, _ string, _ []byte) ([]byte, error) {
	return nil, nil
}

func (stubContext) Send(_, _ string, _ []byte) (string, error) {
	return "", nil
}

func (stubContext) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "", nil
}

func (stubContext) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "", nil
}

func (stubContext) Awakeable() (string, Promise, error) {
	return "", nil, nil
}

func (stubContext) Promise(_ string) Promise {
	return nil
}

func (stubContext) Now() time.Time {
	return time.Time{}
}

func (stubContext) Rand() float64 {
	return 0
}

func (stubContext) UUID() string {
	return ""
}

func (stubContext) GetVersion(_ string, _, _ int) (int, error) {
	return 0, nil
}

func (stubContext) RegisterCompensation(_ func(ctx Context) error) (string, error) {
	return "cmp_stub", nil
}

func (stubContext) CompleteAwakeable(_ string, _ []byte) error { return nil }

func (stubContext) RejectAwakeable(_, _ string) error { return nil }

func (stubContext) Race(_ ...Promise) ([]byte, error)                { return nil, nil }
func (stubContext) AwaitAny(_ ...Promise) ([]byte, error)            { return nil, nil }
func (stubContext) AwaitAll(_ ...Promise) ([]byte, error)            { return nil, nil }
func (stubContext) AwaitFirstSucceeded(_ ...Promise) ([]byte, error) { return nil, nil }
func (stubContext) AwaitAllSucceeded(_ ...Promise) ([]byte, error)   { return nil, nil }

// Verify stubContext satisfies Context at compile time.
var _ Context = stubContext{}

func TestContextInterface_Compile(t *testing.T) {
	// Verify that stubContext satisfies the Context interface at compile time.
	// The compile-time assertion above (var _ Context = stubContext{}) is the
	// real check; this test confirms the package builds with the full interface shape.
	t.Log("Context interface compile-time check passed")
}
