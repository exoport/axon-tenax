package sdk

import (
	"errors"
	"fmt"
)

// CombinatorError is returned by ctx.Race/AwaitAny/AwaitAll/AwaitFirstSucceeded/AwaitAllSucceeded
// when the engine returns a TERMINAL error during combinator construction or suspension.
//
// Numeric codes match the frozen §11 registry (ADR-0009):
//   - 572 = AWAITING_TWO_ASYNC_RESULTS (N-501): a second independent await tree was opened
//     at the same logical await point while an unresolved tree was already active (ErrCodeAwaitingTwoAsyncResults).
//   - 573 = UNSUPPORTED_FEATURE: an empty Race/AwaitAny (first-of-nothing) was requested
//     or the combinator kind is outside {0,1,2,3,4} (ErrCodeUnsupportedFeature).
//
// ADR-0028: CombinatorError is defined in sdk/ using only the numeric int code so that
// internal/statemachine.ErrorCode does NOT appear in any exported sdk/ symbol. The bridge
// (internal/runtime/sm_context.go) converts engine error codes via int(statemachine.ErrCodeXxx).
//
// ADR-0032: struct fields use camelCase json tags; no omitempty; contractVersion 2.
// (Story 19.7, ADR-0009, ADR-0028, ADR-0032)
type CombinatorError struct {
	Code    int    `json:"code"`    // §11 numeric code (572 or 573)
	Message string `json:"message"` // human-readable detail (NOT byte-identity-asserted)
}

// Error implements the error interface.
func (e *CombinatorError) Error() string {
	return fmt.Sprintf("combinator error %d: %s", e.Code, e.Message)
}

// ErrCombinatorFailed is a sentinel for errors.Is detection.
// Use errors.Is(err, sdk.ErrCombinatorFailed) to test whether any error from a combinator
// method (Race, AwaitAny, AwaitAll, AwaitFirstSucceeded, AwaitAllSucceeded) is a
// CombinatorError without needing to type-assert.
//
// (Story 19.7, ADR-0028)
var ErrCombinatorFailed = errors.New("combinator failed")

// Is implements errors.Is target matching for CombinatorError.
// Returns true when target is ErrCombinatorFailed, allowing handler code to test:
//
//	if errors.Is(err, sdk.ErrCombinatorFailed) { ... }
func (e *CombinatorError) Is(target error) bool {
	return target == ErrCombinatorFailed
}

// NewCombinatorError constructs a *CombinatorError with the given §11 numeric code and message.
// Called by the internal/runtime/sm_context.go bridge to wrap engine TERMINAL errors
// (ErrCodeAwaitingTwoAsyncResults=572, ErrCodeUnsupportedFeature=573) into the sdk-boundary
// type without importing statemachine.ErrorCode into sdk/ (ADR-0028).
//
// Never call from handler code — this is a bridge constructor only.
func NewCombinatorError(code int, message string) *CombinatorError {
	return &CombinatorError{Code: code, Message: message}
}
