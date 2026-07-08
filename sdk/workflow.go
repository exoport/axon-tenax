package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// QueryContext — read-only context for Workflow query handlers (AC: 2)
// ---------------------------------------------------------------------------

// QueryContext is the restricted execution context available inside Workflow query handlers.
// Mutation operations (Set, Run, Call, Send, Sleep) return TERMINAL PROTOCOL_VIOLATION
// to enforce read-only semantics.
//
// The SDK surface exposes only Get and List — the same state-read operations
// available to Virtual Object handlers (ctx.Get / ctx.List), scoped to the
// workflow's per-VO KV state (tenax_state bucket).
//
// Do NOT pass the full sdk.Context to a query handler — query handlers receive
// only a QueryContext to prevent accidental mutation.
type QueryContext interface {
	// Get returns the value for the given state key.
	// Returns (value, nil) when found; (nil, nil) when absent.
	Get(key string) ([]byte, error)

	// List returns all state keys in the workflow's per-VO KV state, sorted by
	// UTF-16 code units.
	List() ([]string, error)
}

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030)
// ---------------------------------------------------------------------------

var (
	// ErrDuplicateWorkflow is returned by RegisterWorkflow when a Workflow with
	// the same name is already registered.
	ErrDuplicateWorkflow = errors.New("sdk: duplicate workflow registration")

	// ErrInvalidWorkflow is returned by RegisterWorkflow when the supplied
	// Workflow is nil or its name is empty.
	ErrInvalidWorkflow = errors.New("sdk: invalid workflow")

	// ErrDuplicateQueryHandler is returned by Workflow.Query when a query handler
	// with the same name is already registered on this workflow.
	ErrDuplicateQueryHandler = errors.New("sdk: duplicate query handler registration")

	// ErrDuplicateSignalHandler is returned by Workflow.Signal when a signal
	// handler with the same name is already registered on this workflow.
	ErrDuplicateSignalHandler = errors.New("sdk: duplicate signal handler registration")
)

// ---------------------------------------------------------------------------
// QueryHandlerFunc — SDK query handler signature
// ---------------------------------------------------------------------------

// QueryHandlerFunc is the signature for Workflow query handlers.
// ctx is the restricted QueryContext (read-only — mutation ops return PROTOCOL_VIOLATION).
// args is the JCS-encoded query arguments.
// Returns JCS-encoded result or error.
type QueryHandlerFunc func(ctx QueryContext, args json.RawMessage) (json.RawMessage, error)

// ---------------------------------------------------------------------------
// Workflow struct (AC: 1, 2, 3)
// ---------------------------------------------------------------------------

// Workflow is the authoring abstraction for a run-once keyed workflow with
// queryable and signalable lifecycle.
//
// A Workflow IS a Virtual Object (it embeds VirtualObject for the keyed
// single-writer inbox). The main handler registered via Run() executes exactly
// once per workflow key. Query handlers (registered via Query()) expose a
// read-only view of workflow state. Signal channels (registered via Signal())
// are backed by awakeables that the workflow awaits.
//
// Construct with NewWorkflow(name), register handlers with Run/Query/Signal,
// then pass to sdk.RegisterWorkflow() or registry.RegisterWorkflow().
//
// sdk.Workflow MUST NOT import any internal/ package (ADR-0028). The bridge
// to internal/runtime is via the Context interface and the established
// SDK↔state machine protocol.
type Workflow struct {
	run     KeyedHandlerFunc            // main workflow handler (run-once)
	queries map[string]QueryHandlerFunc // registered query handlers
	signals map[string]HandlerFunc      // registered signal handlers (awakeable-backed)
	name    string
}

// NewWorkflow creates a new Workflow definition with the given name.
// The name is the stable registry key used for handler lookup and routing.
// Register the main handler with Run(), query handlers with Query(),
// and signal channels with Signal().
// Pass to sdk.RegisterWorkflow() when registration is complete.
func NewWorkflow(name string) *Workflow {
	return &Workflow{
		name:    name,
		queries: make(map[string]QueryHandlerFunc),
		signals: make(map[string]HandlerFunc),
	}
}

// Run sets the main workflow handler (executed exactly once per workflow key).
// The handler is keyed — it receives (ctx Context, key string, req []byte).
// Returns the workflow for method chaining.
func (w *Workflow) Run(fn KeyedHandlerFunc) *Workflow {
	w.run = fn
	return w
}

// Query registers a named read-only query handler on the workflow.
// The handler receives a QueryContext (read-only — mutation ops return PROTOCOL_VIOLATION).
// Returns (w, error) — ErrDuplicateQueryHandler (wrapped) if name already registered.
func (w *Workflow) Query(name string, fn QueryHandlerFunc) (*Workflow, error) {
	if _, ok := w.queries[name]; ok {
		return w, fmt.Errorf("workflow %q: query handler %q: %w", w.name, name, ErrDuplicateQueryHandler)
	}
	w.queries[name] = fn
	return w, nil
}

// Signal registers a named signal channel on the workflow.
// The signal handler is invoked when an external caller delivers a signal payload.
// Returns (w, error) — ErrDuplicateSignalHandler (wrapped) if name already registered.
//
// Internally, each Signal channel is backed by an awakeable (akb_-prefixed id) that
// the workflow creates via ctx.Awakeable() and awaits. The signal channel id is
// "sig_" + name (ID-prefix table). External callers resolve the signal via the
// ingress gateway: `tenax invocation signal <wf-key> <signal-name> --payload '...'`.
func (w *Workflow) Signal(name string, fn HandlerFunc) (*Workflow, error) {
	if _, ok := w.signals[name]; ok {
		return w, fmt.Errorf("workflow %q: signal %q: %w", w.name, name, ErrDuplicateSignalHandler)
	}
	w.signals[name] = fn
	return w, nil
}

// Name returns the workflow name (stable registry key).
func (w *Workflow) Name() string { return w.name }

// RunHandler returns the main workflow handler function.
// Returns nil if not set.
func (w *Workflow) RunHandler() KeyedHandlerFunc { return w.run }

// LookupQuery returns the QueryHandlerFunc for the given query name.
// Returns (fn, true) when found; (nil, false) when absent.
func (w *Workflow) LookupQuery(name string) (QueryHandlerFunc, bool) {
	fn, ok := w.queries[name]
	return fn, ok
}

// LookupSignal returns the HandlerFunc for the given signal name.
// Returns (fn, true) when found; (nil, false) when absent.
func (w *Workflow) LookupSignal(name string) (HandlerFunc, bool) {
	fn, ok := w.signals[name]
	return fn, ok
}

// HandlerType returns WorkflowType for all Workflow instances.
// Consumed by internal/runtime to identify the dispatch path.
func (w *Workflow) HandlerType() HandlerType { return WorkflowType }

// Note: WorkflowType HandlerType = 2 is defined in service.go (shared HandlerType iota).
