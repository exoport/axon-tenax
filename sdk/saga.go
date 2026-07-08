package sdk

import "errors"

// ---------------------------------------------------------------------------
// Sentinel errors for compensation registration (ADR-0030)
// ---------------------------------------------------------------------------

// ErrRecursiveCompensation is returned when a compensation function attempts to
// register another compensation via ctx.RegisterCompensation. Recursive registration
// is forbidden and is a terminal PROTOCOL_VIOLATION (Story 5.6, FR-SAGA-1).
//
// Use errors.Is(err, ErrRecursiveCompensation) to detect recursive registration.
var ErrRecursiveCompensation = errors.New("sdk: recursive compensation registration is forbidden (PROTOCOL_VIOLATION)")

// ErrProtocolViolation is the base sentinel for protocol violation errors.
// Recursive compensation registration wraps this sentinel so callers can detect
// the violation class via errors.Is(err, ErrProtocolViolation).
var ErrProtocolViolation = errors.New("sdk: protocol violation")

// ---------------------------------------------------------------------------
// Saga — convenience builder for SDK authors
// ---------------------------------------------------------------------------

// Saga represents a named compensation stack for a handler invocation.
// Each step is paired with a compensation function; on rollback the
// compensations run in LIFO order as durable journaled effects (FR-SAGA-1).
//
// Usage:
//
//	saga := sdk.NewSaga("payment-flow")
//	saga.AddCompensation(func(ctx sdk.Context) error {
//	    _, err := ctx.Run("refund", func(opID string) ([]byte, error) {
//	        return refundPayment(opID)
//	    })
//	    return err
//	})
type Saga struct {
	name          string
	compensations []CompensationFunc
}

// CompensationFunc is the function signature for saga compensation steps.
// The Context parameter allows compensation functions to call ctx.Run and other
// durable operations (their results are journaled as effects — FR-SAGA-3).
type CompensationFunc func(ctx Context) error

// NewSaga creates a new Saga with the given name.
func NewSaga(name string) *Saga {
	return &Saga{name: name}
}

// AddCompensation registers a compensation step on the saga builder.
// Note: this populates a local Saga builder — to register compensations durably
// with the state machine, call ctx.RegisterCompensation(fn) directly.
func (s *Saga) AddCompensation(fn CompensationFunc) *Saga {
	s.compensations = append(s.compensations, fn)
	return s
}

// Name returns the saga name.
func (s *Saga) Name() string { return s.name }

// Compensations returns the registered compensation functions.
// The returned slice is in registration (forward) order; rollback runs in REVERSE.
func (s *Saga) Compensations() []CompensationFunc {
	out := make([]CompensationFunc, len(s.compensations))
	copy(out, s.compensations)
	return out
}

// RegisterAll registers all compensation steps of the saga with the state machine via
// ctx.RegisterCompensation(fn). Returns the first error encountered, if any.
//
// Each compensation is registered in declaration order; rollback will run
// them in LIFO (last registered runs first).
func (s *Saga) RegisterAll(ctx Context) ([]string, error) {
	ids := make([]string, 0, len(s.compensations))
	for _, fn := range s.compensations {
		id, err := ctx.RegisterCompensation(func(c Context) error {
			return fn(c)
		})
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
