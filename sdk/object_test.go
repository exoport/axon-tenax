package sdk //nolint:testpackage // white-box test of unexported sdk internals

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 5.1 — NewVirtualObject + Handler + Lookup round-trip
// ---------------------------------------------------------------------------

// TestNewVirtualObject_registers_handlers verifies NewVirtualObject creates a
// VirtualObject and Handler() registers KeyedHandlerFuncs retrievable via Lookup.
func TestNewVirtualObject_registers_handlers(t *testing.T) {
	processFn := KeyedHandlerFunc(func(_ Context, key string, _ []byte) ([]byte, error) {
		return []byte("processed:" + key), nil
	})

	obj, err := NewVirtualObject("order").Handler("process", processFn)
	if err != nil {
		t.Fatalf("Handler() returned unexpected error: %v", err)
	}

	fn, ok := obj.Lookup("process")
	if !ok {
		t.Fatal("Lookup(\"process\") returned false; want true")
	}
	if fn == nil {
		t.Fatal("Lookup(\"process\") returned nil KeyedHandlerFunc")
	}

	// Call the function to verify identity.
	result, err := fn(nil, "order-42", nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if string(result) != "processed:order-42" {
		t.Errorf("handler result = %q; want %q", string(result), "processed:order-42")
	}
}

// TestNewVirtualObject_Name verifies Name() returns the object name.
func TestNewVirtualObject_Name(t *testing.T) {
	obj, _ := NewVirtualObject("order").Handler("process", func(_ Context, _ string, _ []byte) ([]byte, error) {
		return nil, nil
	})
	if obj.Name() != "order" {
		t.Errorf("Name() = %q; want %q", obj.Name(), "order")
	}
}

// TestNewVirtualObject_Lookup_missing returns false for an unregistered handler.
func TestNewVirtualObject_Lookup_missing(t *testing.T) {
	obj := NewVirtualObject("order")
	_, ok := obj.Lookup("nonexistent")
	if ok {
		t.Error("Lookup(\"nonexistent\") returned true; want false")
	}
}

// ---------------------------------------------------------------------------
// Task 5.2 — Duplicate handler registration returns ErrDuplicateHandler
// ---------------------------------------------------------------------------

// TestNewVirtualObject_duplicate_handler_returns_error verifies that registering
// the same handler name twice wraps ErrDuplicateHandler.
func TestNewVirtualObject_duplicate_handler_returns_error(t *testing.T) {
	noop := KeyedHandlerFunc(func(_ Context, _ string, _ []byte) ([]byte, error) {
		return nil, nil
	})

	obj, err := NewVirtualObject("order").Handler("process", noop)
	if err != nil {
		t.Fatalf("first Handler() unexpectedly errored: %v", err)
	}

	_, err = obj.Handler("process", noop)
	if err == nil {
		t.Fatal("second Handler(\"process\") expected error; got nil")
	}
	if !errors.Is(err, ErrDuplicateHandler) {
		t.Errorf("errors.Is(err, ErrDuplicateHandler) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.3 — VirtualObjectType sentinel is 1, distinct from ServiceType
// ---------------------------------------------------------------------------

// TestVirtualObject_type_sentinel verifies VirtualObjectType is 1 and is distinct
// from ServiceType (0). Also verifies the HandlerType() method.
func TestVirtualObject_type_sentinel(t *testing.T) {
	if VirtualObjectType != HandlerType(1) {
		t.Errorf("VirtualObjectType = %d; want 1", VirtualObjectType)
	}
	if ServiceType != HandlerType(0) {
		t.Errorf("ServiceType = %d; want 0", ServiceType)
	}
	if VirtualObjectType == ServiceType {
		t.Error("VirtualObjectType must be distinct from ServiceType")
	}

	// The WorkflowType (2) is reserved — just verify VirtualObjectType != it.
	if VirtualObjectType == WorkflowType {
		t.Error("VirtualObjectType must be distinct from WorkflowType")
	}

	obj := NewVirtualObject("order")
	if obj.HandlerType() != VirtualObjectType {
		t.Errorf("VirtualObject.HandlerType() = %d; want VirtualObjectType(%d)",
			obj.HandlerType(), VirtualObjectType)
	}
}

// ---------------------------------------------------------------------------
// Task 5.4 — Registry.RegisterVirtualObject stores object; LookupVirtualObject returns it
// ---------------------------------------------------------------------------

// TestRegisterVirtualObject_stores_object verifies RegisterVirtualObject + LookupVirtualObject round-trip.
func TestRegisterVirtualObject_stores_object(t *testing.T) {
	reg := NewRegistry()

	obj := NewVirtualObject("order")
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject() error: %v", err)
	}

	got, ok := reg.LookupVirtualObject("order")
	if !ok {
		t.Fatal("LookupVirtualObject(\"order\") returned false; want true")
	}
	if got == nil {
		t.Fatal("LookupVirtualObject(\"order\") returned nil")
	}
	if got.Name() != "order" {
		t.Errorf("LookupVirtualObject result Name = %q; want %q", got.Name(), "order")
	}
}

// TestRegisterVirtualObject_Lookup_missing returns (nil, false) for unregistered objects.
func TestRegisterVirtualObject_Lookup_missing(t *testing.T) {
	reg := NewRegistry()
	got, ok := reg.LookupVirtualObject("nonexistent")
	if ok {
		t.Error("LookupVirtualObject returned true for unregistered object")
	}
	if got != nil {
		t.Errorf("LookupVirtualObject returned non-nil for unregistered object: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Task 5.5 — Duplicate virtual object registration returns ErrDuplicateVirtualObject
// ---------------------------------------------------------------------------

// TestRegisterVirtualObject_duplicate_object_returns_error verifies registering
// the same object name twice wraps ErrDuplicateVirtualObject.
func TestRegisterVirtualObject_duplicate_object_returns_error(t *testing.T) {
	reg := NewRegistry()

	obj1 := NewVirtualObject("order")
	if err := reg.RegisterVirtualObject(obj1); err != nil {
		t.Fatalf("first RegisterVirtualObject() error: %v", err)
	}

	obj2 := NewVirtualObject("order")
	err := reg.RegisterVirtualObject(obj2)
	if err == nil {
		t.Fatal("second RegisterVirtualObject(\"order\") expected error; got nil")
	}
	if !errors.Is(err, ErrDuplicateVirtualObject) {
		t.Errorf("errors.Is(err, ErrDuplicateVirtualObject) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.6 — KeyedHandlerFunc signature is distinct from HandlerFunc
// ---------------------------------------------------------------------------

// TestKeyedHandlerFunc_signature verifies the KeyedHandlerFunc type has the
// correct signature func(Context, string, []byte) ([]byte, error), distinct from
// HandlerFunc which lacks the key parameter.
func TestKeyedHandlerFunc_signature(t *testing.T) {
	// Compile-time verification: KeyedHandlerFunc takes (Context, string, []byte).
	var kfn KeyedHandlerFunc = func(_ Context, key string, _ []byte) ([]byte, error) {
		return []byte(key), nil
	}

	// Compile-time verification: HandlerFunc takes (Context, []byte) — no key param.
	var fn HandlerFunc = func(_ Context, req []byte) ([]byte, error) {
		return req, nil
	}

	// Runtime verification: call each with distinct arguments.
	keyResult, err := kfn(nil, "my-key", []byte("req"))
	if err != nil {
		t.Fatalf("KeyedHandlerFunc error: %v", err)
	}
	if string(keyResult) != "my-key" {
		t.Errorf("KeyedHandlerFunc result = %q; want %q", string(keyResult), "my-key")
	}

	plainResult, err := fn(nil, []byte("data"))
	if err != nil {
		t.Fatalf("HandlerFunc error: %v", err)
	}
	if string(plainResult) != "data" {
		t.Errorf("HandlerFunc result = %q; want %q", string(plainResult), "data")
	}

	// Verify the types are not assignable to each other at the type level.
	// (This is a compile-time guarantee; tested here as documentation.)
	t.Log("KeyedHandlerFunc and HandlerFunc are distinct named types (compile-time verified)")
}

// ---------------------------------------------------------------------------
// Task 5.7 — SDK package has no internal/ imports (ADR-0028)
// ---------------------------------------------------------------------------

// TestSDKPackageHasNoInternalImports_VO verifies that sdk/ imports zero
// github.com/exoar/axon_tenax_engine/tenax/internal paths (ADR-0028).
func TestSDKPackageHasNoInternalImports_VO(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.Imports}}", "github.com/exoport/axon-tenax/sdk") //nolint:noctx // boundary test shells out to go list
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list failed (may be running inside a restricted env): %v", err)
	}

	output := string(out)
	if strings.Contains(output, "github.com/exoar/axon_tenax_engine/tenax/internal") {
		t.Errorf("sdk/ imports internal/ packages (ADR-0028 violation):\n%s", output)
	} else {
		t.Logf("sdk/ boundary check passed: no internal/ imports found: %s", output)
	}
}

// ---------------------------------------------------------------------------
// Additional: ErrDuplicateVirtualObject is distinct from other sentinels
// ---------------------------------------------------------------------------

func TestSentinelErrors_VirtualObject_AreDistinct(t *testing.T) {
	if errors.Is(ErrDuplicateVirtualObject, ErrDuplicateService) {
		t.Error("ErrDuplicateVirtualObject and ErrDuplicateService must be distinct")
	}
	if errors.Is(ErrDuplicateVirtualObject, ErrDuplicateHandler) {
		t.Error("ErrDuplicateVirtualObject and ErrDuplicateHandler must be distinct")
	}
	if errors.Is(ErrDuplicateVirtualObject, ErrHandlerNotFound) {
		t.Error("ErrDuplicateVirtualObject and ErrHandlerNotFound must be distinct")
	}
	if errors.Is(ErrDuplicateVirtualObject, ErrInvalidService) {
		t.Error("ErrDuplicateVirtualObject and ErrInvalidService must be distinct")
	}
}

// ---------------------------------------------------------------------------
// Additional: nil and empty-name VirtualObject handling
// ---------------------------------------------------------------------------

// TestRegisterVirtualObject_nil wraps ErrInvalidVirtualObject (usage error).
func TestRegisterVirtualObject_nil_wraps_invalid(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterVirtualObject(nil)
	if err == nil {
		t.Fatal("RegisterVirtualObject(nil) expected error; got nil")
	}
	if !errors.Is(err, ErrInvalidVirtualObject) {
		t.Errorf("errors.Is(err, ErrInvalidVirtualObject) = false; err = %v", err)
	}
}

// TestRegisterVirtualObject_emptyName wraps ErrInvalidVirtualObject.
func TestRegisterVirtualObject_emptyName_wraps_invalid(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterVirtualObject(NewVirtualObject(""))
	if err == nil {
		t.Fatal("RegisterVirtualObject(empty-name) expected error; got nil")
	}
	if !errors.Is(err, ErrInvalidVirtualObject) {
		t.Errorf("errors.Is(err, ErrInvalidVirtualObject) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Additional: LookupKeyedHandler bridge — used by internal/runtime
// ---------------------------------------------------------------------------

// TestRegistry_LookupKeyedHandler verifies LookupKeyedHandler returns the registered
// KeyedHandlerFunc for the (objectName, handlerName) pair.
func TestRegistry_LookupKeyedHandler(t *testing.T) {
	reg := NewRegistry()

	kfn := KeyedHandlerFunc(func(_ Context, key string, _ []byte) ([]byte, error) {
		return []byte(key), nil
	})
	obj, err := NewVirtualObject("order").Handler("process", kfn)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject() error: %v", err)
	}

	fn, ok := reg.LookupKeyedHandler("order", "process")
	if !ok {
		t.Fatal("LookupKeyedHandler(\"order\", \"process\") returned false")
	}
	if fn == nil {
		t.Fatal("LookupKeyedHandler returned nil")
	}
}

// TestRegistry_LookupKeyedHandler_object_not_found returns (nil, false).
func TestRegistry_LookupKeyedHandler_object_not_found(t *testing.T) {
	reg := NewRegistry()
	fn, ok := reg.LookupKeyedHandler("ghost", "handler")
	if ok {
		t.Error("LookupKeyedHandler for unregistered object returned true")
	}
	if fn != nil {
		t.Errorf("LookupKeyedHandler returned non-nil for unregistered object: %v", fn)
	}
}

// TestRegistry_LookupKeyedHandler_handler_not_found returns (nil, false).
func TestRegistry_LookupKeyedHandler_handler_not_found(t *testing.T) {
	reg := NewRegistry()
	obj := NewVirtualObject("order")
	if err := reg.RegisterVirtualObject(obj); err != nil {
		t.Fatalf("RegisterVirtualObject() error: %v", err)
	}
	fn, ok := reg.LookupKeyedHandler("order", "ghost")
	if ok {
		t.Error("LookupKeyedHandler for unregistered handler returned true")
	}
	if fn != nil {
		t.Errorf("LookupKeyedHandler returned non-nil for unregistered handler: %v", fn)
	}
}

// ---------------------------------------------------------------------------
// Additional: VirtualObject chaining — multiple handlers via Handler() calls
// ---------------------------------------------------------------------------

func TestNewVirtualObject_chaining_multiple_handlers(t *testing.T) {
	noop := KeyedHandlerFunc(func(_ Context, _ string, _ []byte) ([]byte, error) {
		return nil, nil
	})

	obj := NewVirtualObject("order")
	obj, err := obj.Handler("process", noop)
	if err != nil {
		t.Fatalf("Handler(process) error: %v", err)
	}
	obj, err = obj.Handler("cancel", noop)
	if err != nil {
		t.Fatalf("Handler(cancel) error: %v", err)
	}

	_, ok1 := obj.Lookup("process")
	_, ok2 := obj.Lookup("cancel")
	if !ok1 {
		t.Error("Lookup(\"process\") returned false after chaining")
	}
	if !ok2 {
		t.Error("Lookup(\"cancel\") returned false after chaining")
	}
}
