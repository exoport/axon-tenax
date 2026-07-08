package sdk

import (
	"errors"
	"fmt"
	"sync"
)

// HandlerFunc is the signature all Service and Virtual Object handlers must satisfy.
// ctx provides the durable execution surface (ctx.Run, ctx.Sleep, etc.).
// req is the JCS-encoded request payload (raw bytes from the wire).
// Returns the JCS-encoded response payload, or an error.
type HandlerFunc func(ctx Context, req []byte) ([]byte, error)

// Sentinel errors returned by the SDK registry.
var (
	// ErrHandlerNotFound is returned by LookupService or LookupObject when
	// no handler matching the requested service/object and handler name is registered.
	ErrHandlerNotFound = errors.New("sdk: handler not found")

	// ErrDuplicateHandler is returned by Build when the same service or object
	// name is registered more than once via WithService or WithObject.
	ErrDuplicateHandler = errors.New("sdk: duplicate handler registration")
)

// ---------------------------------------------------------------------------
// Registry — package-level handler resolver used by internal/runtime (ADR-0028)
// ---------------------------------------------------------------------------

// Registry holds registered Services and Virtual Objects by name. It is the
// bridge between sdk/ and internal/runtime: internal/runtime accepts
// HandlerResolver and KeyedHandlerResolver interfaces (defined in that package)
// which Registry implicitly satisfies — no internal/ import from sdk/ required.
//
// Registry is safe for concurrent reads after all Register calls complete (i.e.
// after the worker process has finished its startup registration phase).
type Registry struct {
	mu             sync.RWMutex
	services       map[string]*Service
	virtualObjects map[string]*VirtualObject
	workflows      map[string]*Workflow // Story 5.7: run-once keyed workflows
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		services:       make(map[string]*Service),
		virtualObjects: make(map[string]*VirtualObject),
		workflows:      make(map[string]*Workflow),
	}
}

// Register stores svc in the registry by its Name().
// Returns ErrDuplicateService (wrapped) if a Service with the same name is
// already registered.
// Returns ErrInvalidService (wrapped) if svc is nil or its name is empty.
func (r *Registry) Register(svc *Service) error {
	if svc == nil {
		return fmt.Errorf("sdk: Register: service must not be nil: %w", ErrInvalidService)
	}
	if svc.name == "" {
		return fmt.Errorf("sdk: Register: service name must not be empty: %w", ErrInvalidService)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.services[svc.name]; exists {
		return fmt.Errorf("service %q: %w", svc.name, ErrDuplicateService)
	}
	r.services[svc.name] = svc
	return nil
}

// Lookup returns the Service registered under serviceName.
// Returns (svc, true) when found; (nil, false) when absent.
// Safe for concurrent calls after startup registration is complete.
func (r *Registry) Lookup(serviceName string) (*Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[serviceName]
	return svc, ok
}

// LookupHandler returns the HandlerFunc for the given service and handler names.
// Implements the HandlerResolver interface expected by internal/runtime
// (internal/runtime defines that interface; Registry satisfies it implicitly).
// Returns ErrHandlerNotFound (wrapped) when service or handler is not found.
func (r *Registry) LookupHandler(serviceName, handlerName string) (HandlerFunc, bool) {
	svc, ok := r.Lookup(serviceName)
	if !ok {
		return nil, false
	}
	fn, ok := svc.Lookup(handlerName)
	return fn, ok
}

// ---------------------------------------------------------------------------
// VirtualObject methods — RegisterVirtualObject, LookupVirtualObject, LookupKeyedHandler
// ---------------------------------------------------------------------------

// RegisterVirtualObject stores obj in the registry by its Name().
// Returns ErrDuplicateVirtualObject (wrapped) if a VirtualObject with the same
// name is already registered.
// Returns ErrInvalidVirtualObject (wrapped) if obj is nil or its name is empty.
func (r *Registry) RegisterVirtualObject(obj *VirtualObject) error {
	if obj == nil {
		return fmt.Errorf("sdk: RegisterVirtualObject: object must not be nil: %w", ErrInvalidVirtualObject)
	}
	if obj.name == "" {
		return fmt.Errorf("sdk: RegisterVirtualObject: object name must not be empty: %w", ErrInvalidVirtualObject)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.virtualObjects[obj.name]; exists {
		return fmt.Errorf("virtual object %q: %w", obj.name, ErrDuplicateVirtualObject)
	}
	r.virtualObjects[obj.name] = obj
	return nil
}

// LookupVirtualObject returns the VirtualObject registered under objectName.
// Returns (obj, true) when found; (nil, false) when absent.
// Safe for concurrent calls after startup registration is complete.
func (r *Registry) LookupVirtualObject(objectName string) (*VirtualObject, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	obj, ok := r.virtualObjects[objectName]
	return obj, ok
}

// LookupKeyedHandler returns the KeyedHandlerFunc for the given object and handler names.
// Implements the KeyedHandlerResolver interface expected by internal/runtime
// (internal/runtime defines that interface; Registry satisfies it implicitly).
// Returns (fn, true) when found; (nil, false) when object or handler is not registered.
func (r *Registry) LookupKeyedHandler(objectName, handlerName string) (KeyedHandlerFunc, bool) {
	if obj, ok := r.LookupVirtualObject(objectName); ok {
		if fn, ok := obj.Lookup(handlerName); ok {
			return fn, true
		}
	}
	// A Workflow's run-once handler is resolvable as a keyed handler under
	// (workflowName, "run") so workflow run dispatch (Story 14.3) routes through
	// InprocRuntime.DispatchKeyed exactly like a VirtualObject handler. Query and
	// signal sub-types route via ExecuteQuery/DeliverSignal, not keyed dispatch,
	// so only "run" maps here.
	if handlerName == workflowRunHandlerName {
		if wf, ok := r.LookupWorkflow(objectName); ok {
			if fn := wf.RunHandler(); fn != nil {
				return fn, true
			}
		}
	}
	return nil, false
}

// workflowRunHandlerName is the keyed-handler name under which a Workflow's
// run-once handler is resolved (Story 14.3). It matches the handler name passed
// to InprocRuntime.DispatchKeyed by the workflow "run" dispatch path.
const workflowRunHandlerName = "run"

// ---------------------------------------------------------------------------
// Workflow methods — RegisterWorkflow, LookupWorkflow (Story 5.7)
// ---------------------------------------------------------------------------

// RegisterWorkflow stores wf in the registry by its Name().
// Returns ErrDuplicateWorkflow (wrapped) if a Workflow with the same name is
// already registered.
// Returns ErrInvalidWorkflow (wrapped) if wf is nil or its name is empty.
func (r *Registry) RegisterWorkflow(wf *Workflow) error {
	if wf == nil {
		return fmt.Errorf("sdk: RegisterWorkflow: workflow must not be nil: %w", ErrInvalidWorkflow)
	}
	if wf.name == "" {
		return fmt.Errorf("sdk: RegisterWorkflow: workflow name must not be empty: %w", ErrInvalidWorkflow)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.workflows[wf.name]; exists {
		return fmt.Errorf("workflow %q: %w", wf.name, ErrDuplicateWorkflow)
	}
	r.workflows[wf.name] = wf
	return nil
}

// LookupWorkflow returns the Workflow registered under workflowName.
// Returns (wf, true) when found; (nil, false) when absent.
// Safe for concurrent calls after startup registration is complete.
func (r *Registry) LookupWorkflow(workflowName string) (*Workflow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	wf, ok := r.workflows[workflowName]
	return wf, ok
}

// ---------------------------------------------------------------------------
// Package-level default registry — sdk.Register / sdk.LookupRegistered
// ---------------------------------------------------------------------------

// defaultRegistry is the package-level singleton registry.
// sdk.Register and sdk.LookupRegistered delegate to it.
// Safe for concurrent use after all Register calls complete.
var defaultRegistry = NewRegistry()

// Register registers svc in the package-level default registry.
// This is the entry-point shown in the SDK author guide:
//
//	sdk.Register(svc)
//
// Returns ErrDuplicateService (wrapped) if the service name is already registered.
// Call Register before starting the runtime; the registry is read-only during execution.
func Register(svc *Service) error {
	return defaultRegistry.Register(svc)
}

// LookupRegistered returns the Service registered under serviceName in the
// package-level default registry.
func LookupRegistered(serviceName string) (*Service, bool) {
	return defaultRegistry.Lookup(serviceName)
}

// RegisterWorkflow registers wf in the package-level default registry.
// Parallel to Register (Service) and RegisterVirtualObject (Story 5.7).
// Returns ErrDuplicateWorkflow (wrapped) if the workflow name is already registered.
// Call RegisterWorkflow before starting the runtime; the registry is read-only during execution.
func RegisterWorkflow(wf *Workflow) error {
	return defaultRegistry.RegisterWorkflow(wf)
}

// GlobalRegistry returns the package-level default handler Registry singleton.
// It is the accessor used by cmd/tenaxd worker startup to wire internal/runtime
// with the handler set registered via sdk.Register (ADR-0025, Story 3.3).
// The returned pointer is read-only safe after all Register calls complete.
func GlobalRegistry() *Registry {
	return defaultRegistry
}

// ServiceDescription describes a stateless Service and its handlers.
// Name is the stable string used as the registry key.
// Handlers maps handler names to their HandlerFunc implementations.
type ServiceDescription struct {
	Name     string
	Handlers map[string]HandlerFunc
}

// ObjectDescription describes a Virtual Object (keyed, single-writer) and its handlers.
// Name is the stable string used as the registry key.
// Handlers maps handler names to their HandlerFunc implementations.
type ObjectDescription struct {
	Name     string
	Handlers map[string]HandlerFunc
}

// Option is a functional option for SDK construction.
// It is the extension point for engine-injected runtime config.
type Option func(*SDK)

// SDK holds the registered services and objects for a Tenax worker process.
// Construct with New(...Option) and finalize with Build().
// After Build() returns nil, the registry is read-only and safe for concurrent
// Lookup calls.
type SDK struct {
	services map[string]ServiceDescription
	objects  map[string]ObjectDescription
	buildErr error
}

// New constructs a new SDK and applies the provided options.
// Call Build() to finalize and validate the registry.
// jetstream.JetStream and context.Context are never stored as package-level
// variables — they are passed into every function that needs them.
func New(opts ...Option) *SDK {
	s := &SDK{
		services: make(map[string]ServiceDescription),
		objects:  make(map[string]ObjectDescription),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Build finalizes the SDK state and returns the first registration error.
// After Build returns nil, the registry is read-only and safe for concurrent
// Lookup calls.
func (s *SDK) Build() error {
	return s.buildErr
}

// WithService returns an Option that registers a ServiceDescription.
// If a service with the same name was already registered, the option records
// ErrDuplicateHandler; the error is surfaced via Build().
func WithService(svc ServiceDescription) Option {
	return func(s *SDK) {
		if _, exists := s.services[svc.Name]; exists {
			if s.buildErr == nil {
				s.buildErr = fmt.Errorf("service %q: %w", svc.Name, ErrDuplicateHandler)
			}
			return
		}
		s.services[svc.Name] = svc
	}
}

// WithObject returns an Option that registers an ObjectDescription.
// If an object with the same name was already registered, the option records
// ErrDuplicateHandler; the error is surfaced via Build().
func WithObject(obj ObjectDescription) Option {
	return func(s *SDK) {
		if _, exists := s.objects[obj.Name]; exists {
			if s.buildErr == nil {
				s.buildErr = fmt.Errorf("object %q: %w", obj.Name, ErrDuplicateHandler)
			}
			return
		}
		s.objects[obj.Name] = obj
	}
}

// LookupService returns the handler function for the given service and handler name.
// Safe for concurrent access after Build() returns nil — the underlying maps are
// read-only post-Build; no mutex is needed on the read path.
func (s *SDK) LookupService(name, handler string) (HandlerFunc, error) {
	svc, ok := s.services[name]
	if !ok {
		return nil, fmt.Errorf("service %q handler %q: %w", name, handler, ErrHandlerNotFound)
	}
	fn, ok := svc.Handlers[handler]
	if !ok {
		return nil, fmt.Errorf("service %q handler %q: %w", name, handler, ErrHandlerNotFound)
	}
	return fn, nil
}

// LookupObject returns the handler function for the given object and handler name.
// Safe for concurrent access after Build() returns nil — the underlying maps are
// read-only post-Build; no mutex is needed on the read path.
func (s *SDK) LookupObject(name, handler string) (HandlerFunc, error) {
	obj, ok := s.objects[name]
	if !ok {
		return nil, fmt.Errorf("object %q handler %q: %w", name, handler, ErrHandlerNotFound)
	}
	fn, ok := obj.Handlers[handler]
	if !ok {
		return nil, fmt.Errorf("object %q handler %q: %w", name, handler, ErrHandlerNotFound)
	}
	return fn, nil
}
