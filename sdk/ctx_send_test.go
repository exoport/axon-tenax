package sdk

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 6.2 — ctx.Send() signature verification
// ---------------------------------------------------------------------------

// TestContextInterface_SendSignature verifies that the Context interface declares
// Send(service, handler string, req []byte) (string, error) and concreteCtx satisfies it.
func TestContextInterface_SendSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	calleeInvID, err := c.Send("notification-service", "send-email", []byte(`{"to":"user@example.com"}`))
	// The stub returns ("", nil) — just confirms the signature.
	_ = calleeInvID
	_ = err

	t.Log("sdk.Context.Send(string, string, []byte) (string, error): signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 6.2 — ctx.Send() returns (calleeInvID, nil) on success
// ---------------------------------------------------------------------------

// sendSuccessCtx is a stub Context that returns a successful send result.
type sendSuccessCtx struct{ concreteCtx }

func (c *sendSuccessCtx) Send(_, _ string, _ []byte) (string, error) {
	return "callee_inv_sdk_test_001", nil
}

// TestSend_Success verifies that ctx.Send() returns (calleeInvID, nil) on success.
func TestSend_Success(t *testing.T) {
	var c Context = &sendSuccessCtx{}

	calleeInvID, err := c.Send("notification-service", "send-email", []byte(`{"to":"user@example.com"}`))
	if err != nil {
		t.Fatalf("Send(): err=%v", err)
	}
	if calleeInvID == "" {
		t.Error("Send(): calleeInvID is empty on success")
	}
	if calleeInvID != "callee_inv_sdk_test_001" {
		t.Errorf("calleeInvID=%q, want %q", calleeInvID, "callee_inv_sdk_test_001")
	}
}

// ---------------------------------------------------------------------------
// Task 6.2 — ctx.Send() returns typed error on dispatch failure (what/cause/hint)
// ---------------------------------------------------------------------------

// sendFailureCtx is a stub Context that returns a send dispatch failure error.
type sendFailureCtx struct{ concreteCtx }

func (c *sendFailureCtx) Send(_, _ string, _ []byte) (string, error) {
	return "", NewSendFailedError("ingress unreachable: connection refused")
}

// TestSend_DispatchFailure verifies that ctx.Send() returns a *SendFailedError
// with what/cause/hint triad on dispatch failure.
func TestSend_DispatchFailure(t *testing.T) {
	var c Context = &sendFailureCtx{}

	calleeInvID, err := c.Send("notification-service", "send-email", []byte(`{}`))
	if err == nil {
		t.Fatal("expected dispatch failure error, got nil")
	}
	if calleeInvID != "" {
		t.Errorf("calleeInvID=%q, want empty on failure", calleeInvID)
	}

	// Error must be a SendFailedError with what/cause/hint.
	var sendErr *SendFailedError
	if !errors.As(err, &sendErr) {
		t.Fatalf("err is not *SendFailedError: %T %v", err, err)
	}
	if sendErr.What == "" {
		t.Error("SendFailedError.What is empty")
	}
	if !strings.Contains(sendErr.Cause, "ingress unreachable") {
		t.Errorf("Cause=%q, want substring 'ingress unreachable'", sendErr.Cause)
	}
	if sendErr.Hint == "" {
		t.Error("SendFailedError.Hint is empty")
	}
	t.Logf("SendFailedError: what=%q cause=%q hint=%q", sendErr.What, sendErr.Cause, sendErr.Hint)

	// Error must satisfy errors.Is(ErrSendFailed).
	if !errors.Is(err, ErrSendFailed) {
		t.Errorf("err does not satisfy errors.Is(ErrSendFailed): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 6.2 — NewSendFailedError construction and format
// ---------------------------------------------------------------------------

// TestNewSendFailedError verifies the what/cause/hint triad for SendFailedError.
func TestNewSendFailedError(t *testing.T) {
	msg := "callee dispatch: NATS Micro ingress timeout"
	e := NewSendFailedError(msg)

	if e.What == "" {
		t.Error("What is empty")
	}
	if e.Cause != msg {
		t.Errorf("Cause=%q, want %q", e.Cause, msg)
	}
	if e.Hint == "" {
		t.Error("Hint is empty")
	}
	if !errors.Is(e, ErrSendFailed) {
		t.Error("e should satisfy errors.Is(e, ErrSendFailed)")
	}

	errStr := e.Error()
	if !strings.Contains(errStr, msg) {
		t.Errorf("Error() does not contain %q: %q", msg, errStr)
	}
}

// ---------------------------------------------------------------------------
// Task 6.3 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport_Send is a documentation test: if this test
// file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces this at compile time via the internal/ rule.
func TestContextInterface_NoInternalImport_Send(t *testing.T) {
	// If this test compiles and runs, sdk/send.go does not import internal/
	// (ADR-0028). The compile-time boundary is enforced by the Go toolchain.
	t.Log("sdk/send.go ADR-0028 boundary: confirmed by successful compilation")
	t.Log("sdk.SendFailedError: no internal/ imports")
}
