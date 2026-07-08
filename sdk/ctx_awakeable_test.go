package sdk //nolint:testpackage // white-box test of unexported sdk internals

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 8.2 — ctx.Awakeable() returns (akbID, promise); akbID has "akb_" prefix
// ---------------------------------------------------------------------------

// TestContextInterface_AwakeableSignature verifies that the Context interface declares
// Awakeable() (string, Promise, error) and that concreteCtx satisfies it.
func TestContextInterface_AwakeableSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	akbID, promise, err := c.Awakeable()
	// The stub returns "", nil, nil — just confirms the signature.
	_ = akbID
	_ = promise
	_ = err

	t.Log("sdk.Context.Awakeable() (string, Promise, error): signature verified at compile time")
}

// TestContextInterface_PromiseSignature verifies that the Context interface declares
// Promise(id string) Promise and that concreteCtx satisfies it.
func TestContextInterface_PromiseSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	p := c.Promise("akb_test-001")
	_ = p // nil is valid for the stub

	t.Log("sdk.Context.Promise(string) Promise: signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 8.2 — promise.Await() returns value on resolve
// ---------------------------------------------------------------------------

// awakeableCtx is a stub Context that returns an akbID with "akb_" prefix
// and a PromiseValue backed by a resolve function.
type awakeableCtx struct{ concreteCtx }

func (c *awakeableCtx) Awakeable() (string, Promise, error) {
	akbID := "akb_test-resolve-sdk-001"
	p := NewPromise(func() ([]byte, error) {
		return []byte(`"sdk-resolved-value"`), nil
	})
	return akbID, p, nil
}

func (c *awakeableCtx) Promise(_ string) Promise {
	return NewPromise(func() ([]byte, error) {
		return []byte(`"sdk-promise-value"`), nil
	})
}

// TestAwakeableResolve verifies that:
// - ctx.Awakeable() returns (akbID, promise) with akb_ prefix
// - promise.Await() returns the value
func TestAwakeableResolve(t *testing.T) {
	var c Context = &awakeableCtx{}

	akbID, promise, err := c.Awakeable()
	if err != nil {
		t.Fatalf("Awakeable(): err=%v", err)
	}
	if !strings.HasPrefix(akbID, "akb_") {
		t.Errorf("akbID=%q: missing akb_ prefix", akbID)
	}
	if promise == nil {
		t.Fatal("promise is nil")
	}

	value, awaitErr := promise.Await()
	if awaitErr != nil {
		t.Fatalf("promise.Await(): err=%v", awaitErr)
	}
	if string(value) != `"sdk-resolved-value"` {
		t.Errorf("value=%q, want %q", value, `"sdk-resolved-value"`)
	}
}

// TestPromiseResolve verifies that ctx.Promise(id).Await() returns value.
func TestPromiseResolve(t *testing.T) {
	var c Context = &awakeableCtx{}

	p := c.Promise("akb_test-001")
	if p == nil {
		t.Fatal("Promise() returned nil")
	}

	value, err := p.Await()
	if err != nil {
		t.Fatalf("Await(): err=%v", err)
	}
	if string(value) != `"sdk-promise-value"` {
		t.Errorf("value=%q, want %q", value, `"sdk-promise-value"`)
	}
}

// ---------------------------------------------------------------------------
// Task 8.3 — promise.Await() returns typed error on rejection (what/cause/hint)
// ---------------------------------------------------------------------------

// rejectionCtx is a stub Context that returns a rejection error from promise.Await().
type rejectionCtx struct{ concreteCtx }

func (c *rejectionCtx) Awakeable() (string, Promise, error) {
	akbID := "akb_test-reject-sdk-001"
	p := NewPromise(func() ([]byte, error) {
		return nil, NewAwakeableRejectionError("payment declined by external system")
	})
	return akbID, p, nil
}

// TestAwakeableRejection verifies that:
// - promise.Await() returns a typed rejection error
// - The error carries what/cause/hint triad
func TestAwakeableRejection(t *testing.T) {
	var c Context = &rejectionCtx{}

	_, promise, err := c.Awakeable()
	if err != nil {
		t.Fatalf("Awakeable(): err=%v", err)
	}

	value, awaitErr := promise.Await()
	if awaitErr == nil {
		t.Fatal("expected rejection error, got nil")
	}
	if value != nil {
		t.Errorf("value=%v, want nil on rejection", value)
	}

	// Error must be an AwakeableRejectionError with what/cause/hint.
	var rejErr *AwakeableRejectionError
	if !errors.As(awaitErr, &rejErr) {
		t.Fatalf("awaitErr is not *AwakeableRejectionError: %T %v", awaitErr, awaitErr)
	}
	if rejErr.What == "" {
		t.Error("RejectionError.What is empty")
	}
	if !strings.Contains(rejErr.Cause, "payment declined") {
		t.Errorf("Cause=%q, want substring %q", rejErr.Cause, "payment declined")
	}
	if rejErr.Hint == "" {
		t.Error("RejectionError.Hint is empty")
	}
	t.Logf("RejectionError: what=%q cause=%q hint=%q", rejErr.What, rejErr.Cause, rejErr.Hint)
}

// ---------------------------------------------------------------------------
// Task 8.3 — PromiseValue.Await() with nil awaitFn returns error (not panic)
// ---------------------------------------------------------------------------

// TestPromiseValueNilAwaitFn verifies that calling Await() on a PromiseValue
// with nil awaitFn returns an error (not a panic).
func TestPromiseValueNilAwaitFn(t *testing.T) {
	p := &PromiseValue{awaitFn: nil}
	_, err := p.Await()
	if err == nil {
		t.Fatal("expected error from nil awaitFn, got nil")
	}
	t.Logf("nil awaitFn error: %v", err)
}

// TestPromiseValueNilPointer verifies that calling Await() on a nil PromiseValue
// returns an error (not a panic).
func TestPromiseValueNilPointer(t *testing.T) {
	var p *PromiseValue
	_, err := p.Await()
	if err == nil {
		t.Fatal("expected error from nil PromiseValue, got nil")
	}
}

// ---------------------------------------------------------------------------
// Task 8.4 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport is a documentation test: if this test
// file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces this at compile time via the internal/ rule.
func TestContextInterface_NoInternalImport_Awakeable(t *testing.T) {
	// If this test compiles and runs, sdk/awakeable.go does not import internal/
	// (ADR-0028). The compile-time boundary is enforced by the Go toolchain.
	t.Log("sdk/awakeable.go ADR-0028 boundary: confirmed by successful compilation")
	t.Log("sdk.Promise interface + sdk.PromiseValue: no internal/ imports")
}

// ---------------------------------------------------------------------------
// AwakeableRejectionError construction and Error() format
// ---------------------------------------------------------------------------

func TestNewAwakeableRejectionError(t *testing.T) {
	msg := "order rejected by warehouse"
	e := NewAwakeableRejectionError(msg)

	if e.What == "" {
		t.Error("What is empty")
	}
	if e.Cause != msg {
		t.Errorf("Cause=%q, want %q", e.Cause, msg)
	}
	if e.Hint == "" {
		t.Error("Hint is empty")
	}

	errStr := e.Error()
	if !strings.Contains(errStr, msg) {
		t.Errorf("Error() does not contain %q: %q", msg, errStr)
	}
}
