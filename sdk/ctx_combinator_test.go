package sdk_test

// ctx_combinator_test.go — unit tests for the sdk.Context combinator API surface.
// (Story 19.7, Task 5.1)
//
// These tests verify the sdk/ package-level combinator types and the interface
// shape WITHOUT importing any internal/ package (ADR-0028 boundary).
//
// Coverage:
//  - CombinatorError.Error() returns a human-readable string containing the code
//  - errors.Is(err, sdk.ErrCombinatorFailed) returns true for a *CombinatorError
//  - Race and AwaitAny are registered (non-nil) on the sdk.Context interface via a mock
//  - NewCombinatorError constructs a *CombinatorError with the correct code and message
//  - NewPromiseWithID / PromiseCompletionID round-trip correctly

import (
	"errors"
	"testing"
	"time"

	"github.com/exoport/axon-tenax/sdk"
)

// ---------------------------------------------------------------------------
// Minimal full-interface stub for compile-time + runtime interface checks
// ---------------------------------------------------------------------------

// combinatorTestCtx is a stub that satisfies the full sdk.Context interface.
// Used to verify that Race/AwaitAny/AwaitAll/AwaitFirstSucceeded/AwaitAllSucceeded
// are present on the interface and callable (Task 5.1.c).
type combinatorTestCtx struct {
	raceCalled     bool
	awaitAnyCalled bool
	awaitAllCalled bool
	timerCalled    bool
}

// Compile-time assertion: *combinatorTestCtx must satisfy sdk.Context.
var _ sdk.Context = (*combinatorTestCtx)(nil)

func (c *combinatorTestCtx) Run(_ string, fn func(string) ([]byte, error)) ([]byte, error) {
	return fn("inv_test/0")
}
func (c *combinatorTestCtx) Sleep(_ time.Duration) error { return nil }
func (c *combinatorTestCtx) Timer(_ time.Duration) (sdk.Promise, error) {
	c.timerCalled = true
	return sdk.NewPromiseWithID(0, func() ([]byte, error) { return nil, nil }), nil
}
func (c *combinatorTestCtx) Get(_ string) ([]byte, bool, error)         { return nil, false, nil }
func (c *combinatorTestCtx) Set(_ string, _ []byte) error               { return nil }
func (c *combinatorTestCtx) Clear(_ string) error                       { return nil }
func (c *combinatorTestCtx) List() ([]string, error)                    { return nil, nil }
func (c *combinatorTestCtx) Call(_, _ string, _ []byte) ([]byte, error) { return nil, nil }
func (c *combinatorTestCtx) Send(_, _ string, _ []byte) (string, error) { return "", nil }
func (c *combinatorTestCtx) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "", nil
}

func (c *combinatorTestCtx) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "", nil
}
func (c *combinatorTestCtx) Awakeable() (string, sdk.Promise, error)    { return "", nil, nil }
func (c *combinatorTestCtx) Promise(_ string) sdk.Promise               { return nil }
func (c *combinatorTestCtx) CompleteAwakeable(_ string, _ []byte) error { return nil }
func (c *combinatorTestCtx) RejectAwakeable(_, _ string) error          { return nil }
func (c *combinatorTestCtx) Now() time.Time                             { return time.Time{} }
func (c *combinatorTestCtx) Rand() float64                              { return 0 }
func (c *combinatorTestCtx) UUID() string                               { return "" }
func (c *combinatorTestCtx) GetVersion(_ string, _, _ int) (int, error) { return 0, nil }
func (c *combinatorTestCtx) RegisterCompensation(_ func(sdk.Context) error) (string, error) {
	return "cmp_stub", nil
}

func (c *combinatorTestCtx) Race(_ ...sdk.Promise) ([]byte, error) {
	c.raceCalled = true
	return nil, nil
}

func (c *combinatorTestCtx) AwaitAny(_ ...sdk.Promise) ([]byte, error) {
	c.awaitAnyCalled = true
	return nil, nil
}

func (c *combinatorTestCtx) AwaitAll(_ ...sdk.Promise) ([]byte, error) {
	c.awaitAllCalled = true
	return nil, nil
}

func (c *combinatorTestCtx) AwaitFirstSucceeded(_ ...sdk.Promise) ([]byte, error) {
	return nil, nil
}

func (c *combinatorTestCtx) AwaitAllSucceeded(_ ...sdk.Promise) ([]byte, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Task 5.1.a — CombinatorError.Error() and errors.Is
// ---------------------------------------------------------------------------

func TestCombinatorError_ErrorContainsCode572(t *testing.T) {
	t.Parallel()

	e := sdk.NewCombinatorError(572, "two independent trees")
	got := e.Error()
	if got == "" {
		t.Fatal("CombinatorError.Error() returned empty string")
	}
	// Must contain the numeric code.
	if !strContains(got, "572") {
		t.Errorf("CombinatorError.Error() = %q; want it to contain \"572\"", got)
	}
}

func TestCombinatorError_ErrorContainsCode573(t *testing.T) {
	t.Parallel()

	e := sdk.NewCombinatorError(573, "empty race")
	got := e.Error()
	if !strContains(got, "573") {
		t.Errorf("CombinatorError.Error() = %q; want it to contain \"573\"", got)
	}
}

func TestCombinatorError_IsErrCombinatorFailed(t *testing.T) {
	t.Parallel()

	e := sdk.NewCombinatorError(572, "two independent trees")

	if !errors.Is(e, sdk.ErrCombinatorFailed) {
		t.Errorf("errors.Is(%v, sdk.ErrCombinatorFailed) = false; want true", e)
	}
}

func TestCombinatorError_IsDoesNotMatchOtherErrors(t *testing.T) {
	t.Parallel()

	e := sdk.NewCombinatorError(572, "two independent trees")
	other := errors.New("other error")

	if errors.Is(e, other) {
		t.Errorf("errors.Is(%v, other) = true; want false", e)
	}
}

func TestErrCombinatorFailed_IsItself(t *testing.T) {
	t.Parallel()

	if !errors.Is(sdk.ErrCombinatorFailed, sdk.ErrCombinatorFailed) {
		t.Error("errors.Is(ErrCombinatorFailed, ErrCombinatorFailed) = false; want true")
	}
}

// ---------------------------------------------------------------------------
// Task 5.1.b — NewCombinatorError code and message fields
// ---------------------------------------------------------------------------

func TestNewCombinatorError_CodeAndMessageFields(t *testing.T) {
	t.Parallel()

	e := sdk.NewCombinatorError(573, "unsupported feature")
	if e.Code != 573 {
		t.Errorf("CombinatorError.Code = %d; want 573", e.Code)
	}
	if e.Message != "unsupported feature" {
		t.Errorf("CombinatorError.Message = %q; want %q", e.Message, "unsupported feature")
	}
}

// ---------------------------------------------------------------------------
// Task 5.1.c — Race and AwaitAny are registered on sdk.Context interface
// ---------------------------------------------------------------------------

func TestSDKContext_RaceAndAwaitAny_AreRegistered(t *testing.T) {
	t.Parallel()

	// Ensure Race and AwaitAny exist on the interface and are callable via a mock.
	var ctx sdk.Context = &combinatorTestCtx{}

	_, _ = ctx.Race()
	_, _ = ctx.AwaitAny()
	_, _ = ctx.AwaitAll()

	mc := ctx.(*combinatorTestCtx)
	if !mc.raceCalled {
		t.Error("Race() was not dispatched through sdk.Context interface")
	}
	if !mc.awaitAnyCalled {
		t.Error("AwaitAny() was not dispatched through sdk.Context interface")
	}
	if !mc.awaitAllCalled {
		t.Error("AwaitAll() was not dispatched through sdk.Context interface")
	}
}

// Task 5.1.c — AwaitAll with empty slice does NOT return 573 on a mock implementation.
// (The engine identity rule is tested in sm_context_combinator_test.go; here we verify
// the sdk.Context interface accepts empty variadic slices without panic.)
func TestSDKContext_AwaitAll_EmptySliceAccepted(t *testing.T) {
	t.Parallel()

	var ctx sdk.Context = &combinatorTestCtx{}
	result, err := ctx.AwaitAll() // empty slice — no panic, no 573 on mock
	if err != nil {
		t.Errorf("AwaitAll() with empty slice returned error on mock: %v", err)
	}
	_ = result
}

// ---------------------------------------------------------------------------
// Story 52.3 — ctx.Timer is registered on sdk.Context and usable as a combinator
// child (SDK-surface-only check; the engine-level polymorphic dispatch through
// runCombinator is covered by internal/runtime/sm_context_timer_test.go).
// ---------------------------------------------------------------------------

// TestSDKContext_Timer_IsRegistered verifies that ctx.Timer(d) exists on the
// sdk.Context interface, is callable, and returns a *sdk.PromiseValue whose
// completion id round-trips via sdk.PromiseCompletionID — the exact shape
// ctx.Race/AwaitAny/etc. require of any combinator child (ADR-0028: the
// polymorphism is at the *sdk.PromiseValue level, not a Timer-specific type).
func TestSDKContext_Timer_IsRegistered(t *testing.T) {
	t.Parallel()

	var ctx sdk.Context = &combinatorTestCtx{}

	p, err := ctx.Timer(5 * time.Second)
	if err != nil {
		t.Fatalf("Timer() returned error on mock: %v", err)
	}
	if p == nil {
		t.Fatal("Timer() returned nil Promise")
	}

	mc := ctx.(*combinatorTestCtx)
	if !mc.timerCalled {
		t.Error("Timer() was not dispatched through sdk.Context interface")
	}

	// Timer's Promise must be usable exactly like an Awakeable/Call-backed promise:
	// combinator methods extract the completion id via sdk.PromiseCompletionID without
	// any Timer-specific type assertion.
	pv, ok := p.(*sdk.PromiseValue)
	if !ok {
		t.Fatalf("Timer() Promise type = %T; want *sdk.PromiseValue", p)
	}
	_ = sdk.PromiseCompletionID(pv) // must not panic; value itself is mock-defined (0)

	// Usable directly as a Race/AwaitAll argument — no special-casing required.
	if _, err := ctx.Race(p); err != nil {
		t.Errorf("Race(timerPromise) returned error on mock: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.1.d — NewPromiseWithID / PromiseCompletionID round-trip
// ---------------------------------------------------------------------------

func TestNewPromiseWithID_RoundTrip(t *testing.T) {
	t.Parallel()

	const wantID uint32 = 42
	p := sdk.NewPromiseWithID(wantID, func() ([]byte, error) {
		return []byte("hello"), nil
	})

	gotID := sdk.PromiseCompletionID(p)
	if gotID != wantID {
		t.Errorf("PromiseCompletionID = %d; want %d", gotID, wantID)
	}
}

func TestNewPromiseWithID_ZeroID(t *testing.T) {
	t.Parallel()

	// Zero is a valid completion id (first allocateCompletionID() call).
	const wantID uint32 = 0
	p := sdk.NewPromiseWithID(wantID, func() ([]byte, error) { return nil, nil })

	gotID := sdk.PromiseCompletionID(p)
	if gotID != wantID {
		t.Errorf("PromiseCompletionID = %d; want %d", gotID, wantID)
	}
}

func TestNewPromiseWithID_AwaitFnCalledAndReturnsValue(t *testing.T) {
	t.Parallel()

	called := false
	wantVal := []byte("resolved-value")
	p := sdk.NewPromiseWithID(7, func() ([]byte, error) {
		called = true
		return wantVal, nil
	})

	val, err := p.Await()
	if err != nil {
		t.Fatalf("Await() error: %v", err)
	}
	if !called {
		t.Error("awaitFn was not called by Await()")
	}
	if string(val) != string(wantVal) {
		t.Errorf("Await() = %q; want %q", val, wantVal)
	}
}

func TestNewPromiseWithID_AwaitFnPropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("suspended")
	p := sdk.NewPromiseWithID(3, func() ([]byte, error) {
		return nil, wantErr
	})

	_, err := p.Await()
	if !errors.Is(err, wantErr) {
		t.Errorf("Await() error = %v; want %v", err, wantErr)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// strContains returns true when s contains the substring sub.
func strContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
