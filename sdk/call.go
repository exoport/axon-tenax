package sdk

import "errors"

// CallFailedError is the SDK-surface callee failure error returned by ctx.Call()
// when the callee invocation returns an error or is killed. It carries the
// what/cause/hint triad required by ADR-0030.
//
// ADR-0028: This type is defined in sdk/ so handler authors can type-assert against
// it without importing internal/. The internal/call layer wraps its error into this
// type before returning to handler code.
type CallFailedError struct {
	What  string // one-line problem statement
	Cause string // callee error message from the CallCompletion journal entry
	Hint  string // what to do next
}

// Error implements the error interface.
func (e *CallFailedError) Error() string {
	return "error: " + e.What + "\n  cause: " + e.Cause + "\n  hint:  " + e.Hint
}

// ErrCallFailed is the base sentinel for callee failure errors.
// Use errors.Is(err, sdk.ErrCallFailed) to detect callee failures (ADR-0030).
var ErrCallFailed = errors.New("sdk: callee invocation failed")

// Is enables errors.Is matching against ErrCallFailed.
func (e *CallFailedError) Is(target error) bool {
	return target == ErrCallFailed
}

// NewCallFailedError constructs a CallFailedError from the callee error message.
// Called by the internal/runtime SDK bridge to wrap internal/call.CalleeFailedError.
func NewCallFailedError(errorMsg string) *CallFailedError {
	return &CallFailedError{
		What:  "callee invocation failed",
		Cause: errorMsg,
		Hint:  "handle the callee failure or propagate the error to the caller",
	}
}
