package sdk

import (
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// KeyedHandlerFunc — VirtualObject handler signature
// ---------------------------------------------------------------------------

// KeyedHandlerFunc is the signature all Virtual Object handlers must satisfy.
// ctx provides the durable execution surface (ctx.Run, ctx.Get, ctx.Set, etc.).
// key is the Virtual Object routing key (e.g. "order-42") — the stable identifier
// that scopes per-VO KV state and serializes concurrent invocations.
// req is the JCS-encoded request payload (raw bytes from the wire).
// Returns the JCS-encoded response payload, or an error.
//
// This is DISTINCT from HandlerFunc (stateless Service handlers) — the extra
// key string parameter carries the object's routing key. Do NOT use HandlerFunc
// for Virtual Object handlers.
//
// ctx.Get/Set/Clear/List are VALID inside a KeyedHandlerFunc.
// ctx.Now/Rand/UUID MUST be used instead of time.Now/math/rand/UUID libraries.
type KeyedHandlerFunc func(ctx Context, key string, req []byte) ([]byte, error)

// ---------------------------------------------------------------------------
// Sentinel errors (ADR-0030)
// ---------------------------------------------------------------------------

var (
	// ErrDuplicateVirtualObject is returned by RegisterVirtualObject when a
	// VirtualObject with the same name is already in the registry.
	// This is a name-collision conflict, distinct from ErrInvalidVirtualObject.
	ErrDuplicateVirtualObject = errors.New("sdk: duplicate virtual object name")

	// ErrInvalidVirtualObject is returned by RegisterVirtualObject when the
	// supplied VirtualObject is invalid — it is nil, or its name is empty.
	// This is a usage error distinct from ErrDuplicateVirtualObject.
	ErrInvalidVirtualObject = errors.New("sdk: invalid virtual object")
)

// ---------------------------------------------------------------------------
// VirtualObject struct — keyed durable handler set (AC: 1, 2, 5)
// ---------------------------------------------------------------------------

// VirtualObject is a keyed durable handler set addressable by name and key.
// Register handlers with Handler(), then pass to sdk.Registry.RegisterVirtualObject().
//
// Concurrent invocations targeting the same Virtual Object key are serialized by
// the partition processor (internal/partition) — the SDK only registers the type
// and its handler table; single-writer enforcement happens in the runtime layer
// (ADR-0014).
//
// ctx.Get, ctx.Set, ctx.Clear, and ctx.List ARE valid for VirtualObject handlers.
// These state operations are per-key: state set by key "order-42" is isolated
// from state set by key "order-99" in the same VirtualObject.
//
// All nondeterministic operations must use ctx.Now(), ctx.Rand(), ctx.UUID()
// rather than time.Now(), math/rand, or UUID libraries directly (ADR-0011).
type VirtualObject struct {
	handlers map[string]KeyedHandlerFunc
	name     string
}

// NewVirtualObject creates a new VirtualObject authoring definition with the
// given name. The name is the stable registry key (e.g. "order").
// Call Handler() to register keyed handler functions, then pass to
// sdk.Registry.RegisterVirtualObject().
func NewVirtualObject(name string) *VirtualObject {
	return &VirtualObject{name: name, handlers: make(map[string]KeyedHandlerFunc)}
}

// Handler registers fn under the given handler name.
// Returns the receiver for method chaining.
// Returns (o, ErrDuplicateHandler) wrapped with %w if name is already registered.
//
// The fn parameter must be a KeyedHandlerFunc — it receives (ctx, key, req).
// Do not pass a HandlerFunc here; the explicit key parameter distinguishes
// Virtual Object handlers from stateless Service handlers.
func (o *VirtualObject) Handler(name string, fn KeyedHandlerFunc) (*VirtualObject, error) {
	if _, ok := o.handlers[name]; ok {
		return o, fmt.Errorf("virtual object %q: handler %q already registered: %w",
			o.name, name, ErrDuplicateHandler)
	}
	o.handlers[name] = fn
	return o, nil
}

// Lookup returns the KeyedHandlerFunc registered under the given handler name.
// Returns (fn, true) when found; (nil, false) when absent.
// Safe for concurrent reads after the VirtualObject has been passed to
// RegisterVirtualObject and startup registration is complete.
func (o *VirtualObject) Lookup(name string) (KeyedHandlerFunc, bool) {
	fn, ok := o.handlers[name]
	return fn, ok
}

// Name returns the virtual object name (the stable registry key).
func (o *VirtualObject) Name() string { return o.name }

// HandlerType returns VirtualObjectType for all VirtualObject instances.
// Consumed by internal/runtime to identify the dispatch path and enable
// state ops (ctx.Get/Set/Clear/List) for this handler type.
func (o *VirtualObject) HandlerType() HandlerType { return VirtualObjectType }

// ---------------------------------------------------------------------------
// ObjectBuilder — legacy fluent builder for ObjectDescription (Story 2.4 pattern)
// ---------------------------------------------------------------------------

// ObjectBuilder is a fluent builder for ObjectDescription.
// Obtain one via Object(name); add handlers with Handle; finalize with Build.
//
// Example:
//
//	sdk := sdk.New(
//	    sdk.WithObject(
//	        sdk.Object("counter").Handle("increment", incrementHandler).Build(),
//	    ),
//	)
//
// Note: ObjectBuilder uses HandlerFunc (the unkeyed signature). For Virtual Object
// handlers that need the key parameter and per-key state, use NewVirtualObject instead.
type ObjectBuilder struct {
	handlers map[string]HandlerFunc
	name     string
}

// Object creates a new ObjectBuilder for a Virtual Object (keyed, single-writer)
// with the given name. The name is the stable registry key used for handler lookup.
// All handlers on a given key are serialized; the key is used for KV state isolation.
//
// Note: for the full VirtualObject API (keyed handlers, single-writer semantics,
// and per-key state), use NewVirtualObject(name) instead.
func Object(name string) *ObjectBuilder {
	return &ObjectBuilder{name: name, handlers: make(map[string]HandlerFunc)}
}

// Handle registers a named handler function on the object builder.
// Returns the builder for chaining.
func (b *ObjectBuilder) Handle(name string, fn HandlerFunc) *ObjectBuilder {
	b.handlers[name] = fn
	return b
}

// Build returns the ObjectDescription produced by this builder.
// All public types in this file are composed of SDK-defined or stdlib types only;
// no internal/ types leak through the public surface (ADR-0028).
func (b *ObjectBuilder) Build() ObjectDescription {
	return ObjectDescription{Name: b.name, Handlers: b.handlers}
}
