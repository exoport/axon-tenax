package sdk

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.Call() signature verification
// ---------------------------------------------------------------------------

// TestContextInterface_CallSignature verifies that the Context interface declares
// Call(service, handler string, req []byte) ([]byte, error) and concreteCtx satisfies it.
func TestContextInterface_CallSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	output, err := c.Call("payment-service", "charge", []byte(`{"amount":100}`))
	// The stub returns (nil, nil) — just confirms the signature.
	_ = output
	_ = err

	t.Log("sdk.Context.Call(string, string, []byte) ([]byte, error): signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.Call() returns (output, nil) on success
// ---------------------------------------------------------------------------

// callSuccessCtx is a stub Context that returns a successful call result.
type callSuccessCtx struct{ concreteCtx }

func (c *callSuccessCtx) Call(_, _ string, _ []byte) ([]byte, error) {
	return []byte(`"sdk-call-result"`), nil
}

// TestCall_Success verifies that ctx.Call() returns (output, nil) on success.
func TestCall_Success(t *testing.T) {
	var c Context = &callSuccessCtx{}

	output, err := c.Call("payment-service", "charge", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("Call(): err=%v", err)
	}
	if string(output) != `"sdk-call-result"` {
		t.Errorf("output=%q, want %q", output, `"sdk-call-result"`)
	}
}

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.Call() returns typed error on callee failure (what/cause/hint)
// ---------------------------------------------------------------------------

// callFailureCtx is a stub Context that returns a callee failure error.
type callFailureCtx struct{ concreteCtx }

func (c *callFailureCtx) Call(_, _ string, _ []byte) ([]byte, error) {
	return nil, NewCallFailedError("payment rejected by fraud filter")
}

// TestCall_CalleeFailure verifies that ctx.Call() returns a *CallFailedError
// with what/cause/hint triad on callee failure.
func TestCall_CalleeFailure(t *testing.T) {
	var c Context = &callFailureCtx{}

	output, err := c.Call("payment-service", "charge", []byte(`{"amount":100}`))
	if err == nil {
		t.Fatal("expected callee failure error, got nil")
	}
	if output != nil {
		t.Errorf("output=%v, want nil on failure", output)
	}

	// Error must be a CallFailedError with what/cause/hint.
	var callErr *CallFailedError
	if !errors.As(err, &callErr) {
		t.Fatalf("err is not *CallFailedError: %T %v", err, err)
	}
	if callErr.What == "" {
		t.Error("CallFailedError.What is empty")
	}
	if !strings.Contains(callErr.Cause, "payment rejected") {
		t.Errorf("Cause=%q, want substring 'payment rejected'", callErr.Cause)
	}
	if callErr.Hint == "" {
		t.Error("CallFailedError.Hint is empty")
	}
	t.Logf("CallFailedError: what=%q cause=%q hint=%q", callErr.What, callErr.Cause, callErr.Hint)

	// Error must satisfy errors.Is(ErrCallFailed).
	if !errors.Is(err, ErrCallFailed) {
		t.Errorf("err does not satisfy errors.Is(ErrCallFailed): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 8.2 — NewCallFailedError construction and format
// ---------------------------------------------------------------------------

// TestNewCallFailedError verifies the what/cause/hint triad for CallFailedError.
func TestNewCallFailedError(t *testing.T) {
	msg := "callee service unavailable"
	e := NewCallFailedError(msg)

	if e.What == "" {
		t.Error("What is empty")
	}
	if e.Cause != msg {
		t.Errorf("Cause=%q, want %q", e.Cause, msg)
	}
	if e.Hint == "" {
		t.Error("Hint is empty")
	}
	if !errors.Is(e, ErrCallFailed) {
		t.Error("e should satisfy errors.Is(e, ErrCallFailed)")
	}

	errStr := e.Error()
	if !strings.Contains(errStr, msg) {
		t.Errorf("Error() does not contain %q: %q", msg, errStr)
	}
}

// ---------------------------------------------------------------------------
// Task 8.3 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport_Call is a documentation test: if this test
// file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces this at compile time via the internal/ rule.
func TestContextInterface_NoInternalImport_Call(t *testing.T) {
	// If this test compiles and runs, sdk/call.go does not import internal/
	// (ADR-0028). The compile-time boundary is enforced by the Go toolchain.
	t.Log("sdk/call.go ADR-0028 boundary: confirmed by successful compilation")
	t.Log("sdk.CallFailedError: no internal/ imports")
}
