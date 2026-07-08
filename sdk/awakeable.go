package sdk

import "errors"

// PromiseValue is the concrete generic Promise implementation.
// Returned by the internal runtime's SDK bridge when ctx.Awakeable() or ctx.Promise(id)
// is called. The runtime injects the Await function that drives the state machine awakeable path.
//
// ADR-0028: PromiseValue does NOT import any internal/ package. The state machine bridge is
// injected via the awaitFn field, which the internal/runtime wires at call time.
type PromiseValue struct {
	// awaitFn is the injected function that drives the state machine awakeable resume path.
	// On first call: blocks until the awakeable is completed externally.
	// On replay:     returns the journaled result immediately.
	// Nil when Promise is returned from a stub (test use only).
	awaitFn func() ([]byte, error)

	// completionID is the engine completion id allocated by allocateCompletionID()
	// at the time this PromiseValue was created by the bridge. It is set by
	// internal/runtime/sm_context.go when constructing the PromiseValue, so that
	// the bridge can later read it back via PromiseCompletionID() to build a
	// combinator leaf node (FutureNode) for the §7.2 future tree.
	//
	// Zero is a valid value for the first-ever allocateCompletionID() call, so
	// the bridge always sets this explicitly; PromiseCompletionID() reads it as-is.
	//
	// ADR-0028: this field is unexported — internal/runtime reads it via the
	// package-level PromiseCompletionID accessor defined below. The accessor is NOT
	// an exported method on sdk.Context (that would expose engine types at the
	// interface level, violating ADR-0028).
	completionID uint32
}

// Await blocks until the promise is resolved and returns the result.
// Returns a typed error (RejectionError shape with what/cause/hint triad) on rejection.
// Implements sdk.Promise.
func (p *PromiseValue) Await() ([]byte, error) {
	if p == nil || p.awaitFn == nil {
		return nil, errors.New("sdk: promise await: not bound to a state machine awakeable (awaitFn is nil)")
	}
	return p.awaitFn()
}

// NewPromise constructs a PromiseValue with the given await function.
// Called by the internal/runtime SDK bridge to create a Promise handle
// backed by a specific state machine awakeable operation.
//
// awaitFn must drive the state machine awakeable path (journaling AwakeableCommand on live,
// returning AwakeableCompletion result on replay).
// Never call from handler code — use ctx.Awakeable() and ctx.Promise(id) instead.
func NewPromise(awaitFn func() ([]byte, error)) *PromiseValue {
	return &PromiseValue{awaitFn: awaitFn}
}

// NewPromiseWithID constructs a PromiseValue with the given await function and completion id.
// Used by the internal/runtime SDK bridge when constructing a PromiseValue from ctx.Awakeable()
// or ctx.Promise(id) to record the engine completion id allocated via allocateCompletionID().
//
// completionID is used later by the bridge to build a combinator leaf node (via
// statemachine.Leaf(completionID)) for a §7.2 future tree. The caller reads it back
// via PromiseCompletionID(p).
//
// Never call from handler code — use ctx.Awakeable() and ctx.Promise(id) instead.
func NewPromiseWithID(completionID uint32, awaitFn func() ([]byte, error)) *PromiseValue {
	return &PromiseValue{completionID: completionID, awaitFn: awaitFn}
}

// PromiseCompletionID returns the engine completion id stored on p.
// Called by internal/runtime/sm_context.go to extract the leaf id when building
// a combinator FutureNode tree (sdk.Context.Race, AwaitAny, AwaitAll etc.).
//
// ADR-0028: this accessor is a package-level function in sdk/, usable by bridge code
// in internal/runtime/ without exposing engine types (statemachine.FutureNode,
// statemachine.CombinatorType) in any exported sdk/ symbol.
//
// Zero is a valid value for the first-ever allocateCompletionID() call; callers
// must always use this accessor rather than assuming zero means "unset".
func PromiseCompletionID(p *PromiseValue) uint32 {
	return p.completionID
}

// AwakeableRejectionError is the SDK-surface rejection error returned by Promise.Await()
// when the external caller rejects the awakeable. It carries the what/cause/hint triad
// required by ADR-0030, surfaced from the internal/awakeable.RejectionError.
//
// ADR-0028: This type is defined in sdk/ so handler authors can type-assert against it
// without importing internal/. The internal/statemachine layer wraps its RejectionError into this
// type before returning to handler code.
type AwakeableRejectionError struct {
	What  string // one-line problem statement
	Cause string // rejection message from the external caller
	Hint  string // what to do next
}

// Error implements the error interface.
func (e *AwakeableRejectionError) Error() string {
	return "error: " + e.What + "\n  cause: " + e.Cause + "\n  hint:  " + e.Hint
}

// NewAwakeableRejectionError constructs an AwakeableRejectionError from the rejection message.
// Called by the internal/runtime SDK bridge to wrap internal/awakeable.RejectionError.
func NewAwakeableRejectionError(errorMsg string) *AwakeableRejectionError {
	return &AwakeableRejectionError{
		What:  "awakeable rejected by external caller",
		Cause: errorMsg,
		Hint:  "handle the rejection or propagate the error to the caller",
	}
}
