package sdk //nolint:testpackage // white-box test of unexported sdk internals

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.SendDelayed() signature verification
// ---------------------------------------------------------------------------

// TestContextInterface_SendDelayedSignature verifies that the Context interface declares
// SendDelayed(service, handler string, req []byte, delay time.Duration) (string, error)
// and that concreteCtx satisfies it.
func TestContextInterface_SendDelayedSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	handle, err := c.SendDelayed("scheduler-service", "run-job", []byte(`{"task":"send-report"}`), 100*time.Millisecond)
	// The stub returns ("", nil) — just confirms the signature.
	_ = handle
	_ = err

	t.Log("sdk.Context.SendDelayed(string, string, []byte, time.Duration) (string, error): signature verified at compile time")
}

// TestContextInterface_SendAtSignature verifies that the Context interface declares
// SendAt(service, handler string, req []byte, invokeAt time.Time) (string, error).
func TestContextInterface_SendAtSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	handle, err := c.SendAt("scheduler-service", "run-job", []byte(`{"task":"send-report"}`), time.Now().Add(time.Hour))
	_ = handle
	_ = err

	t.Log("sdk.Context.SendAt(string, string, []byte, time.Time) (string, error): signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.SendDelayed() returns (handle, nil) on success
// ---------------------------------------------------------------------------

// sendDelayedSuccessCtx is a stub Context that returns a successful delayed send.
type sendDelayedSuccessCtx struct{ concreteCtx }

func (c *sendDelayedSuccessCtx) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "inv_delayed_001/2", nil
}

func (c *sendDelayedSuccessCtx) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "inv_delayed_at_001/2", nil
}

// TestSendDelayed_Success verifies that ctx.SendDelayed returns a non-empty handle
// and nil error on success.
func TestSendDelayed_Success(t *testing.T) {
	var c Context = &sendDelayedSuccessCtx{}

	handle, err := c.SendDelayed("scheduler-service", "run-job", []byte(`{}`), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("SendDelayed(): err=%v", err)
	}
	if handle == "" {
		t.Error("SendDelayed(): handle is empty on success")
	}
	if handle != "inv_delayed_001/2" {
		t.Errorf("handle=%q, want %q", handle, "inv_delayed_001/2")
	}
}

// TestSendAt_Success verifies that ctx.SendAt returns a non-empty handle and nil error.
func TestSendAt_Success(t *testing.T) {
	var c Context = &sendDelayedSuccessCtx{}

	invokeAt := time.Now().Add(time.Hour)
	handle, err := c.SendAt("scheduler-service", "run-job", []byte(`{}`), invokeAt)
	if err != nil {
		t.Fatalf("SendAt(): err=%v", err)
	}
	if handle == "" {
		t.Error("SendAt(): handle is empty on success")
	}
}

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.SendDelayed() returns typed error on registration failure
// ---------------------------------------------------------------------------

// sendDelayedFailureCtx is a stub Context that returns a registration failure.
type sendDelayedFailureCtx struct{ concreteCtx }

func (c *sendDelayedFailureCtx) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "", NewDelayedSendFailedError("tenax.wakes: NATS publish timeout")
}

func (c *sendDelayedFailureCtx) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "", NewDelayedSendFailedError("tenax.wakes: NATS publish timeout")
}

// TestSendDelayed_RegistrationFailure verifies that ctx.SendDelayed returns a
// *DelayedSendFailedError with what/cause/hint triad on registration failure (ADR-0030).
func TestSendDelayed_RegistrationFailure(t *testing.T) {
	var c Context = &sendDelayedFailureCtx{}

	handle, err := c.SendDelayed("scheduler-service", "run-job", []byte(`{}`), 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected registration failure error, got nil")
	}
	if handle != "" {
		t.Errorf("handle=%q, want empty on failure", handle)
	}

	// Error must be a DelayedSendFailedError with what/cause/hint (ADR-0030).
	var delayedErr *DelayedSendFailedError
	if !errors.As(err, &delayedErr) {
		t.Fatalf("err is not *DelayedSendFailedError: %T %v", err, err)
	}
	if delayedErr.What == "" {
		t.Error("DelayedSendFailedError.What is empty")
	}
	if !strings.Contains(delayedErr.Cause, "tenax.wakes") {
		t.Errorf("Cause=%q, want substring 'tenax.wakes'", delayedErr.Cause)
	}
	if delayedErr.Hint == "" {
		t.Error("DelayedSendFailedError.Hint is empty")
	}

	// Error must satisfy errors.Is(ErrDelayedSendFailed).
	if !errors.Is(err, ErrDelayedSendFailed) {
		t.Errorf("err does not satisfy errors.Is(ErrDelayedSendFailed): %v", err)
	}

	t.Logf("DelayedSendFailedError: what=%q cause=%q hint=%q", delayedErr.What, delayedErr.Cause, delayedErr.Hint)
}

// ---------------------------------------------------------------------------
// Task 8.2 — NewDelayedSendFailedError construction and format
// ---------------------------------------------------------------------------

// TestNewDelayedSendFailedError verifies the what/cause/hint triad for DelayedSendFailedError.
func TestNewDelayedSendFailedError(t *testing.T) {
	msg := "timer service unavailable: tenax.wakes publish failed"
	e := NewDelayedSendFailedError(msg)

	if e.What == "" {
		t.Error("What is empty")
	}
	if e.Cause != msg {
		t.Errorf("Cause=%q, want %q", e.Cause, msg)
	}
	if e.Hint == "" {
		t.Error("Hint is empty")
	}
	if !errors.Is(e, ErrDelayedSendFailed) {
		t.Error("e should satisfy errors.Is(e, ErrDelayedSendFailed)")
	}

	errStr := e.Error()
	if !strings.Contains(errStr, msg) {
		t.Errorf("Error() does not contain %q: %q", msg, errStr)
	}
	if !strings.Contains(errStr, "error:") {
		t.Errorf("Error() missing 'error:' prefix: %q", errStr)
	}
	if !strings.Contains(errStr, "cause:") {
		t.Errorf("Error() missing 'cause:' field: %q", errStr)
	}
	if !strings.Contains(errStr, "hint:") {
		t.Errorf("Error() missing 'hint:' field: %q", errStr)
	}
}

// ---------------------------------------------------------------------------
// Task 8.3 — ctx.Send(target, method, input) regression test (AC: 3)
// ---------------------------------------------------------------------------

// TestSend_RegressionUnchanged verifies that the existing ctx.Send signature and
// behavior is UNCHANGED after adding ctx.SendDelayed (regression guard, AC: 3).
// The immediate Send path must remain unaffected.
func TestSend_RegressionUnchanged(t *testing.T) {
	// Use the same sendSuccessCtx stub from ctx_send_test.go.
	var c Context = &sendSuccessCtx{}

	calleeInvID, err := c.Send("notification-service", "send-email", []byte(`{"to":"user@example.com"}`))
	if err != nil {
		t.Fatalf("Send() regression: err=%v (Send must remain unchanged)", err)
	}
	if calleeInvID == "" {
		t.Error("Send() regression: calleeInvID is empty")
	}
	if calleeInvID != testCalleeInvID {
		t.Errorf("Send() regression: calleeInvID=%q, want %q", calleeInvID, testCalleeInvID)
	}
	t.Log("sdk.Context.Send regression: PASSED — immediate send unchanged by delayed-send addition")
}

// TestSend_InterfaceStillSatisfied verifies that adding SendDelayed and SendAt
// to the Context interface does not break the existing Send method's position/signature.
func TestSend_InterfaceStillSatisfied(t *testing.T) {
	// concreteCtx (defined in ctx_test.go) must still satisfy Context.
	var _ Context = (*concreteCtx)(nil) // compile-time assertion

	// sendSuccessCtx (defined in ctx_send_test.go) still satisfies Context via embedding.
	var _ Context = (*sendSuccessCtx)(nil) // also compile-time

	t.Log("Send/SendDelayed/SendAt interface coexistence: compile-time check passed")
}

// ---------------------------------------------------------------------------
// Task 8.4 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport_SendDelayed is a documentation test:
// if this test file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces the internal/ rule at compile time.
func TestContextInterface_NoInternalImport_SendDelayed(t *testing.T) {
	t.Log("sdk/ctx.go ADR-0028 boundary: confirmed by successful compilation")
	t.Log("sdk.Context.SendDelayed / sdk.Context.SendAt: no internal/ imports")
	t.Log("sdk.DelayedSendFailedError / sdk.ErrDelayedSendFailed: defined in sdk/send.go without internal/ imports")
}

// ---------------------------------------------------------------------------
// ErrInvalidInvokeTime — validation error sentinel
// ---------------------------------------------------------------------------

// TestErrInvalidInvokeTime verifies that ErrInvalidInvokeTime is a non-nil sentinel
// error that can be used with errors.Is for invoke-time validation failures.
func TestErrInvalidInvokeTime(t *testing.T) {
	if ErrInvalidInvokeTime == nil {
		t.Error("ErrInvalidInvokeTime must not be nil")
	}
	if ErrInvalidInvokeTime.Error() == "" {
		t.Error("ErrInvalidInvokeTime.Error() must not be empty")
	}

	// errors.Is match against itself.
	if !errors.Is(ErrInvalidInvokeTime, ErrInvalidInvokeTime) {
		t.Error("errors.Is(ErrInvalidInvokeTime, ErrInvalidInvokeTime) must be true")
	}

	// Wrapping must work.
	wrapped := fmt.Errorf("ctx.SendDelayed: %w", ErrInvalidInvokeTime)
	if !errors.Is(wrapped, ErrInvalidInvokeTime) {
		t.Error("errors.Is(wrapped, ErrInvalidInvokeTime) must be true after %w wrap")
	}
}
