package sdk

import "errors"

// SendFailedError is the SDK-surface send dispatch failure error returned by ctx.Send()
// when the callee dispatch fails (e.g., ingress unreachable, NATS timeout).
// It carries the what/cause/hint triad required by ADR-0030.
//
// ADR-0028: This type is defined in sdk/ so handler authors can type-assert against
// it without importing internal/. The internal/statemachine layer wraps its error into this
// type before returning to handler code.
type SendFailedError struct {
	What  string // one-line problem statement
	Cause string // dispatch error message from the ingress layer
	Hint  string // what to do next
}

// Error implements the error interface.
func (e *SendFailedError) Error() string {
	return "error: " + e.What + "\n  cause: " + e.Cause + "\n  hint:  " + e.Hint
}

// ErrSendFailed is the base sentinel for send dispatch failure errors.
// Use errors.Is(err, sdk.ErrSendFailed) to detect dispatch failures (ADR-0030).
var ErrSendFailed = errors.New("sdk: one-way send dispatch failed")

// Is enables errors.Is matching against ErrSendFailed.
func (e *SendFailedError) Is(target error) bool {
	return target == ErrSendFailed
}

// NewSendFailedError constructs a SendFailedError from the dispatch error message.
// Called by the internal/runtime SDK bridge to wrap internal/statemachine.SendDispatchError.
func NewSendFailedError(errorMsg string) *SendFailedError {
	return &SendFailedError{
		What:  "one-way send dispatch failed",
		Cause: errorMsg,
		Hint:  "check callee target/method exist and ingress is reachable; retry is safe via idempotency key",
	}
}

// ---------------------------------------------------------------------------
// DelayedSendFailedError — what/cause/hint triad for ctx.SendDelayed failures
// ---------------------------------------------------------------------------

// DelayedSendFailedError is the SDK-surface delayed-send registration failure error
// returned by ctx.SendDelayed() / ctx.SendAt() when the journal append or tenax.wakes
// registration fails (e.g., NATS timeout, ingress unreachable).
// It carries the what/cause/hint triad required by ADR-0030.
//
// ADR-0028: This type is defined in sdk/ so handler authors can type-assert against
// it without importing internal/. The internal/statemachine layer wraps its error into this
// type before returning to handler code.
type DelayedSendFailedError struct {
	What  string // one-line problem statement
	Cause string // underlying error message from the registration path
	Hint  string // what to do next
}

// Error implements the error interface.
func (e *DelayedSendFailedError) Error() string {
	return "error: " + e.What + "\n  cause: " + e.Cause + "\n  hint:  " + e.Hint
}

// ErrDelayedSendFailed is the base sentinel for delayed-send registration failure errors.
// Use errors.Is(err, sdk.ErrDelayedSendFailed) to detect registration failures (ADR-0030).
var ErrDelayedSendFailed = errors.New("sdk: delayed send registration failed")

// Is enables errors.Is matching against ErrDelayedSendFailed.
func (e *DelayedSendFailedError) Is(target error) bool {
	return target == ErrDelayedSendFailed
}

// NewDelayedSendFailedError constructs a DelayedSendFailedError from the registration error message.
// Called by the internal/runtime SDK bridge to wrap internal/statemachine delayed-send errors.
func NewDelayedSendFailedError(errorMsg string) *DelayedSendFailedError {
	return &DelayedSendFailedError{
		What:  "delayed send registration failed",
		Cause: errorMsg,
		Hint:  "check timer service availability on tenax.wakes; journal + wake registration must both confirm",
	}
}

// ErrInvalidInvokeTime is returned when ctx.SendDelayed or ctx.SendAt is called with
// an invalid invoke time (e.g., negative InvokeTime derived from a negative delay).
// Wrappable via %w (ADR-0030).
var ErrInvalidInvokeTime = errors.New("sdk: delayed send invoke time invalid")
