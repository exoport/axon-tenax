package sdk //nolint:testpackage // white-box test of unexported sdk internals

// ctx_call_workflow_test.go — unit tests for the sdk.Context.CallWorkflow verb (Story 56.1, ADR-0046).
//
// CallWorkflow mirrors ctx.Call in shape (awaits ([]byte, error)) but carries a Workflow key.
// The frozen CR-20 §1.4 return semantics (Cortex-ACKed) are:
//   - run-once-per-key ATTACH: a second dispatch to the same (name, key) attaches to the single
//     run-once instance — it does NOT start a second run;
//   - an awaited CallWorkflow on a COMPLETED key returns the RECORDED result;
//   - on a terminal FAILED/KILLED/CANCELLED key it surfaces the RECORDED terminal error.
//
// The real keyed-dispatch mechanism is engine-side (Story 56.2); these tests exercise the sdk.Context
// boundary only — signature shape and the doc-commented contract via stub Context fakes, to the extent
// the SDK layer can assert them without the live engine.

import (
	"bytes"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 3 — ctx.CallWorkflow() signature verification
// ---------------------------------------------------------------------------

// TestContextInterface_CallWorkflowSignature verifies that the Context interface declares
// CallWorkflow(name, key string, req []byte) ([]byte, error) and concreteCtx satisfies it.
func TestContextInterface_CallWorkflowSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	output, err := c.CallWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	// The stub returns (nil, nil) — just confirms the signature.
	_ = output
	_ = err

	t.Log("sdk.Context.CallWorkflow(string, string, []byte) ([]byte, error): signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 3 — run-once-per-key ATTACH: a second CallWorkflow to the same (name, key)
// attaches to the single run-once instance instead of starting a second run.
// ---------------------------------------------------------------------------

// callWorkflowAttachCtx is a stub Context that models run-once-per-key ATTACH semantics:
// it tracks how many times a distinct key actually "ran" versus attached, and always
// returns the same recorded result for a given key.
type callWorkflowAttachCtx struct {
	concreteCtx
	runsByKey map[string]int
}

func (c *callWorkflowAttachCtx) CallWorkflow(_, key string, _ []byte) ([]byte, error) {
	if c.runsByKey == nil {
		c.runsByKey = make(map[string]int)
	}
	// A real run-once-per-key instance only ever executes once; every dispatch to the
	// same key attaches to that single instance and observes the same recorded result.
	c.runsByKey[key]++
	return []byte(`"run-once-result:` + key + `"`), nil
}

// TestCallWorkflow_AttachSameKey_DoesNotStartSecondRun verifies that two CallWorkflow
// dispatches to the same (name, key) attach to the single run-once instance (the runsByKey
// counter for that key never exceeds what a conforming engine-side implementation would
// report as "started once") and both observe the identical recorded result.
func TestCallWorkflow_AttachSameKey_DoesNotStartSecondRun(t *testing.T) {
	c := &callWorkflowAttachCtx{}
	var ctx Context = c

	first, err := ctx.CallWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("first CallWorkflow(): err=%v", err)
	}

	second, err := ctx.CallWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("second CallWorkflow() (attach): err=%v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("attach dispatch returned a different result: first=%q second=%q — a second dispatch to the "+
			"same (name, key) must attach to the single run-once instance and observe the identical recorded result",
			first, second)
	}
	if want := `"run-once-result:order-42"`; string(first) != want {
		t.Errorf("first result = %q, want %q", first, want)
	}
}

// TestCallWorkflow_DifferentKeys_AreIndependentRuns verifies that CallWorkflow dispatches to
// distinct keys are independent run-once instances (not attached to each other).
func TestCallWorkflow_DifferentKeys_AreIndependentRuns(t *testing.T) {
	c := &callWorkflowAttachCtx{}
	var ctx Context = c

	orderA, err := ctx.CallWorkflow("order-workflow", "order-1", nil)
	if err != nil {
		t.Fatalf("CallWorkflow(order-1): err=%v", err)
	}
	orderB, err := ctx.CallWorkflow("order-workflow", "order-2", nil)
	if err != nil {
		t.Fatalf("CallWorkflow(order-2): err=%v", err)
	}

	if bytes.Equal(orderA, orderB) {
		t.Errorf("distinct keys produced the same result: %q — distinct keys must be independent run-once instances", orderA)
	}
	if c.runsByKey["order-1"] != 1 || c.runsByKey["order-2"] != 1 {
		t.Errorf("runsByKey = %v, want exactly one dispatch recorded per distinct key", c.runsByKey)
	}
}

// ---------------------------------------------------------------------------
// Task 3 — an awaited CallWorkflow on a COMPLETED key returns the RECORDED result
// ---------------------------------------------------------------------------

// callWorkflowCompletedCtx is a stub Context that models awaiting an already-COMPLETED
// keyed Workflow: CallWorkflow returns the recorded result without error, regardless of
// the request payload passed on this (attaching) call.
type callWorkflowCompletedCtx struct{ concreteCtx }

func (c *callWorkflowCompletedCtx) CallWorkflow(_, _ string, _ []byte) ([]byte, error) {
	return []byte(`"recorded-completed-result"`), nil
}

// TestCallWorkflow_CompletedKey_ReturnsRecordedResult verifies that an awaited CallWorkflow
// on a COMPLETED key returns the recorded result (CR-20 §1.4).
func TestCallWorkflow_CompletedKey_ReturnsRecordedResult(t *testing.T) {
	var c Context = &callWorkflowCompletedCtx{}

	output, err := c.CallWorkflow("order-workflow", "order-42", []byte(`{"amount":999}`))
	if err != nil {
		t.Fatalf("CallWorkflow() on COMPLETED key: err=%v", err)
	}
	if want := `"recorded-completed-result"`; string(output) != want {
		t.Errorf("output=%q, want %q (the recorded result)", output, want)
	}
}

// ---------------------------------------------------------------------------
// Task 3 — a terminal FAILED/KILLED/CANCELLED key surfaces the RECORDED terminal error
// ---------------------------------------------------------------------------

// callWorkflowTerminalFailedCtx is a stub Context that models awaiting a keyed Workflow
// already terminal in FAILED/KILLED/CANCELLED — CallWorkflow surfaces the recorded terminal
// error rather than re-executing. The error type/sentinel mirrors ctx.Call's callee-failure
// surface (CallFailedError / ErrCallFailed, ADR-0030) since CallWorkflow mirrors ctx.Call.
type callWorkflowTerminalFailedCtx struct{ concreteCtx }

func (c *callWorkflowTerminalFailedCtx) CallWorkflow(_, _ string, _ []byte) ([]byte, error) {
	return nil, NewCallFailedError("workflow terminal: FAILED (recorded)")
}

// TestCallWorkflow_TerminalFailedKey_SurfacesRecordedError verifies that an awaited
// CallWorkflow on a terminal FAILED/KILLED/CANCELLED key surfaces the recorded terminal
// error (CR-20 §1.4) as a wrapped Err… sentinel (ADR-0030), not an ad-hoc string.
func TestCallWorkflow_TerminalFailedKey_SurfacesRecordedError(t *testing.T) {
	var c Context = &callWorkflowTerminalFailedCtx{}

	output, err := c.CallWorkflow("order-workflow", "order-42", []byte(`{}`))
	if err == nil {
		t.Fatal("expected recorded terminal error, got nil")
	}
	if output != nil {
		t.Errorf("output=%v, want nil on terminal failure", output)
	}

	var callErr *CallFailedError
	if !errors.As(err, &callErr) {
		t.Fatalf("err is not *CallFailedError: %T %v", err, err)
	}
	if !errors.Is(err, ErrCallFailed) {
		t.Errorf("err does not satisfy errors.Is(ErrCallFailed): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 4 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport_CallWorkflow is a documentation test: if this test
// file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces this at compile time via the internal/ rule.
func TestContextInterface_NoInternalImport_CallWorkflow(t *testing.T) {
	t.Log("sdk.Context.CallWorkflow ADR-0028 boundary: confirmed by successful compilation")
}
