package sdk

import (
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// HandlerType sentinel — identifies the authoring type of a registered entity
// ---------------------------------------------------------------------------

// HandlerType identifies the authoring type (Service, VirtualObject, Workflow)
// of a registered handler set. Used by internal/runtime to select the correct
// dispatch path without importing sdk/ directly.
type HandlerType int

const (
	// ServiceType is the HandlerType sentinel for stateless Services.
	// A stateless Service holds no per-key state; ctx.Get/Set/Clear/List
	// will return ErrStateNotKeyed (statemachine package) when called from its handlers.
	ServiceType HandlerType = 0

	// VirtualObjectType is the HandlerType sentinel for keyed Virtual Objects.
	VirtualObjectType HandlerType = 1

	// WorkflowType is the HandlerType sentinel for run-once Workflows.
	WorkflowType HandlerType = 2
)

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030)
// ---------------------------------------------------------------------------

var (
	// ErrDuplicateService is returned by Register when a Service with the same
	// name is already in the package-level registry.
	ErrDuplicateService = errors.New("sdk: duplicate service name")

	// ErrInvalidService is returned by Register when the supplied Service is
	// invalid as an argument — it is nil, or its name is empty. This is a usage
	// error distinct from ErrDuplicateService (a name-collision conflict).
	ErrInvalidService = errors.New("sdk: invalid service")
)

// ---------------------------------------------------------------------------
// Service struct — stateless durable handler set (AC: 1, 2, 5)
// ---------------------------------------------------------------------------

// Service is a stateless durable handler set addressable by name.
// Register handlers with Handler(), then pass to sdk.Register().
//
// Stateless Services have no per-key state surface: ctx.Get, ctx.Set,
// ctx.Clear, and ctx.List will return ErrStateNotKeyed at runtime if called
// from a Service handler — state operations require a Virtual Object
// handler (ADR-0011).
//
// All nondeterministic operations must use ctx.Now(), ctx.Rand(), ctx.UUID()
// rather than time.Now(), math/rand, or UUID libraries directly (ADR-0011).
type Service struct {
	name     string
	handlers map[string]HandlerFunc
}

// NewService creates a new stateless Service authoring definition with the
// given name. The name is the stable registry key (e.g. "payments").
// Call Handler() to register handler functions, then pass to sdk.Register().
func NewService(name string) *Service {
	return &Service{name: name, handlers: make(map[string]HandlerFunc)}
}

// Handler registers fn under the given handler name.
// Returns the receiver for method chaining.
// Returns (s, ErrDuplicateHandler) wrapped with %w if name is already registered.
//
// Note: if the existing Story 2.4 builder pattern (ServiceBuilder.Handle) is used
// in parallel, those registrations are independent and do not share this map.
func (s *Service) Handler(name string, fn HandlerFunc) (*Service, error) {
	if _, ok := s.handlers[name]; ok {
		return s, fmt.Errorf("service %q: handler %q already registered: %w",
			s.name, name, ErrDuplicateHandler)
	}
	s.handlers[name] = fn
	return s, nil
}

// Lookup returns the HandlerFunc registered under the given handler name.
// Safe for concurrent reads after the Service has been passed to sdk.Register().
func (s *Service) Lookup(name string) (HandlerFunc, bool) {
	fn, ok := s.handlers[name]
	return fn, ok
}

// Name returns the service name (the stable registry key).
func (s *Service) Name() string { return s.name }

// HandlerType returns ServiceType for all Service instances.
// Consumed by internal/runtime to identify the dispatch path.
func (s *Service) HandlerType() HandlerType { return ServiceType }

// ---------------------------------------------------------------------------
// ServiceBuilder — fluent builder for ServiceDescription (Story 2.4 pattern)
// ---------------------------------------------------------------------------

// ServiceBuilder is a fluent builder for ServiceDescription.
// Obtain one via Service(name); add handlers with Handle; finalize with Build.
//
// Example:
//
//	sdk := sdk.New(
//	    sdk.WithService(
//	        sdk.Service("greeter").Handle("greet", greetHandler).Build(),
//	    ),
//	)
type ServiceBuilder struct {
	name     string
	handlers map[string]HandlerFunc
}

// NewServiceBuilder creates a new ServiceBuilder for a stateless service with the
// given name. The name is the stable registry key used for handler lookup (FR-SM-6).
// Use NewService(name) for the idiomatic Service struct; use NewServiceBuilder
// when constructing a ServiceDescription for sdk.New(sdk.WithService(...)).
func NewServiceBuilder(name string) *ServiceBuilder {
	return &ServiceBuilder{name: name, handlers: make(map[string]HandlerFunc)}
}

// Handle registers a named handler function on the service builder.
// Returns the builder for chaining.
func (b *ServiceBuilder) Handle(name string, fn HandlerFunc) *ServiceBuilder {
	b.handlers[name] = fn
	return b
}

// Build returns the ServiceDescription produced by this builder.
// All public types in this file are composed of SDK-defined or stdlib types only;
// no internal/ types leak through the public surface (ADR-0028).
func (b *ServiceBuilder) Build() ServiceDescription {
	return ServiceDescription{Name: b.name, Handlers: b.handlers}
}
