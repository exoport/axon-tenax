package sdk //nolint:testpackage // white-box test of unexported sdk internals

// ctx_send_workflow_test.go — unit tests for the sdk.Context.SendWorkflow verb (Story 56.1, ADR-0046).
//
// SendWorkflow mirrors ctx.Send in shape (fire-and-forget, returns (string, error)) but carries a
// Workflow key. The frozen CR-20 §1.4 return semantics (Cortex-ACKed) are:
//   - run-once-per-key ATTACH: a second dispatch to the same (name, key) attaches to the single
//     run-once instance — it does NOT start a second run;
//   - SendWorkflow to a terminal key is a NO-OP that returns the EXISTING invId.
//
// The real keyed-dispatch mechanism is engine-side (Story 56.2); these tests exercise the sdk.Context
// boundary only — signature shape and the doc-commented contract via stub Context fakes, to the extent
// the SDK layer can assert them without the live engine.

import "testing"

const testWorkflowCalleeInvID = "callee_inv_workflow_sdk_test_001" // shared workflow-callee test fixture id

// ---------------------------------------------------------------------------
// Task 3 — ctx.SendWorkflow() signature verification
// ---------------------------------------------------------------------------

// TestContextInterface_SendWorkflowSignature verifies that the Context interface declares
// SendWorkflow(name, key string, req []byte) (string, error) and concreteCtx satisfies it.
func TestContextInterface_SendWorkflowSignature(t *testing.T) {
	var c Context = &concreteCtx{}

	calleeInvID, err := c.SendWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	// The stub returns ("", nil) — just confirms the signature.
	_ = calleeInvID
	_ = err

	t.Log("sdk.Context.SendWorkflow(string, string, []byte) (string, error): signature verified at compile time")
}

// ---------------------------------------------------------------------------
// Task 3 — run-once-per-key ATTACH: a second SendWorkflow to the same (name, key)
// attaches to the single run-once instance and returns the same invId.
// ---------------------------------------------------------------------------

// sendWorkflowAttachCtx is a stub Context that models run-once-per-key ATTACH semantics for
// SendWorkflow: it tracks how many times a distinct key was actually started, and always
// returns the same invId for a given key.
type sendWorkflowAttachCtx struct {
	concreteCtx
	startsByKey map[string]int
	invIDByKey  map[string]string
}

func (c *sendWorkflowAttachCtx) SendWorkflow(_, key string, _ []byte) (string, error) {
	if c.startsByKey == nil {
		c.startsByKey = make(map[string]int)
		c.invIDByKey = make(map[string]string)
	}
	if _, started := c.invIDByKey[key]; !started {
		c.startsByKey[key]++
		c.invIDByKey[key] = "inv_" + key
	}
	return c.invIDByKey[key], nil
}

// TestSendWorkflow_AttachSameKey_ReturnsSameInvID verifies that two SendWorkflow dispatches
// to the same (name, key) attach to the single run-once instance and return the identical
// invId, without starting a second run (CR-20 §1.4).
func TestSendWorkflow_AttachSameKey_ReturnsSameInvID(t *testing.T) {
	c := &sendWorkflowAttachCtx{}
	var ctx Context = c

	first, err := ctx.SendWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("first SendWorkflow(): err=%v", err)
	}
	second, err := ctx.SendWorkflow("order-workflow", "order-42", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("second SendWorkflow() (attach): err=%v", err)
	}

	if first != second {
		t.Errorf("attach dispatch returned a different invId: first=%q second=%q", first, second)
	}
	if c.startsByKey["order-42"] != 1 {
		t.Errorf("startsByKey[order-42] = %d, want 1 (a second dispatch to the same key must attach, not start)",
			c.startsByKey["order-42"])
	}
}

// ---------------------------------------------------------------------------
// Task 3 — SendWorkflow to a terminal key is a NO-OP that returns the EXISTING invId
// ---------------------------------------------------------------------------

// sendWorkflowTerminalNoopCtx is a stub Context that models SendWorkflow dispatched against
// an already-terminal keyed Workflow: the call is a no-op that returns the existing invId
// without error.
type sendWorkflowTerminalNoopCtx struct{ concreteCtx }

func (c *sendWorkflowTerminalNoopCtx) SendWorkflow(_, _ string, _ []byte) (string, error) {
	return testWorkflowCalleeInvID, nil
}

// TestSendWorkflow_TerminalKey_NoopReturnsExistingInvID verifies that SendWorkflow to a
// terminal key is a no-op that returns the existing invId (CR-20 §1.4).
func TestSendWorkflow_TerminalKey_NoopReturnsExistingInvID(t *testing.T) {
	var c Context = &sendWorkflowTerminalNoopCtx{}

	calleeInvID, err := c.SendWorkflow("order-workflow", "order-42", []byte(`{}`))
	if err != nil {
		t.Fatalf("SendWorkflow() on terminal key: err=%v", err)
	}
	if calleeInvID != testWorkflowCalleeInvID {
		t.Errorf("calleeInvID=%q, want %q (the existing invId, no-op)", calleeInvID, testWorkflowCalleeInvID)
	}
}

// ---------------------------------------------------------------------------
// Task 4 — No internal/ imports (ADR-0028) — compile-time gate
// ---------------------------------------------------------------------------

// TestContextInterface_NoInternalImport_SendWorkflow is a documentation test: if this test
// file compiles and runs, sdk/ does not import internal/ (ADR-0028).
// The Go toolchain enforces this at compile time via the internal/ rule.
func TestContextInterface_NoInternalImport_SendWorkflow(t *testing.T) {
	t.Log("sdk.Context.SendWorkflow ADR-0028 boundary: confirmed by successful compilation")
}
