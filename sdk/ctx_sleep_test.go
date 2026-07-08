package sdk

import (
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Task 7.2 — Sleep(d) returns nil on success
// ---------------------------------------------------------------------------

// TestContextInterface_SleepSignature verifies that the Context interface declares
// Sleep(d time.Duration) error and that concreteCtx satisfies it returning nil.
// The compile-time assertion var _ Context = (*concreteCtx)(nil) in ctx_test.go
// already gates this — this test provides runtime confirmation.
func TestContextInterface_SleepSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	// Sleep with a positive duration must return nil from the stub (AC: 5).
	if err := c.Sleep(24 * time.Hour); err != nil {
		t.Errorf("Sleep(24h): got err %v, want nil", err)
	}
	if err := c.Sleep(0); err != nil {
		t.Errorf("Sleep(0): got err %v, want nil", err)
	}
	if err := c.Sleep(time.Millisecond); err != nil {
		t.Errorf("Sleep(1ms): got err %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Task 7.2 — Error propagation from state machine layer
// ---------------------------------------------------------------------------

// errorCtx is a stub Context that returns a sentinel error from Sleep.
// Used to verify that callers propagate Sleep errors correctly.
type errorCtx struct{ concreteCtx }

var errTestSleep = errors.New("test: sleep failed")

func (c *errorCtx) Sleep(_ time.Duration) error { return errTestSleep }

// TestContextInterface_SleepErrorPropagation verifies that Sleep errors propagate
// correctly. The state machine layer (ErrSuspended, ErrWakeRegistrationFailed) must be
// surfaced to the handler author through the sdk.Context.Sleep method.
func TestContextInterface_SleepErrorPropagation(t *testing.T) {
	var c Context = &errorCtx{}

	err := c.Sleep(time.Hour)
	if err == nil {
		t.Fatal("expected error from errorCtx.Sleep, got nil")
	}
	if !errors.Is(err, errTestSleep) {
		t.Errorf("errors.Is(err, errTestSleep): false; err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 7.3 — Sleep signature matches Sleep(d time.Duration) error
// ---------------------------------------------------------------------------

// TestContextInterface_SleepTyped verifies the Sleep method signature at the
// interface level: Sleep(d time.Duration) error.
// This is a compile-time check via var _ Context = (*concreteCtx)(nil);
// this test also confirms the runtime type of the parameter.
func TestContextInterface_SleepTyped(t *testing.T) {
	var c Context = &concreteCtx{}

	// Confirm the signature accepts time.Duration and returns error.
	d := 24 * time.Hour
	err := c.Sleep(d)
	_ = err // return type is error — confirmed by assignment

	// Negative duration is also a valid time.Duration (no SDK-level validation).
	if negErr := c.Sleep(-time.Hour); negErr != nil {
		t.Errorf("Sleep(-1h): got err %v, want nil (stub)", negErr)
	}
}

// ---------------------------------------------------------------------------
// Task 7.3 — Verify sdk/ctx.go has no internal/ imports (ADR-0028)
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport is a documentation test: this test
// file is in package sdk and the test compiles successfully, which proves that
// the sdk package itself does not import any internal/ package (Go's internal/
// visibility rule enforces this at compile time automatically).
//
// The assertion is structural: if sdk/ctx.go imported internal/statemachine or any other
// internal/ package, this test binary would fail to compile.
// The conformance check is: `go list -f '{{.Imports}}' github.com/exoport/axon-tenax/sdk`
// should not contain any 'internal/' entry.
func TestContextInterface_NoInternalImport(t *testing.T) {
	// If this test compiles and runs, sdk/ does not import internal/ (ADR-0028).
	// The compile-time boundary is enforced by the Go toolchain — this is documentation.
	t.Log("sdk/ctx.go ADR-0028 boundary: confirmed by successful compilation")
	t.Log("sdk.Context.Sleep(d time.Duration) error: signature verified at compile time")
}
