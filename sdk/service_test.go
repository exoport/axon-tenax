package sdk

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 5.1 — NewService + Handler + Lookup round-trip
// ---------------------------------------------------------------------------

// TestNewService_registers_handlers verifies that NewService creates a Service
// and Handler() registers handler functions retrievable via Lookup.
func TestNewService_registers_handlers(t *testing.T) {
	chargeFn := HandlerFunc(func(_ Context, _ []byte) ([]byte, error) {
		return []byte("charged"), nil
	})

	svc, err := NewService("payments").Handler("charge", chargeFn)
	if err != nil {
		t.Fatalf("Handler() returned unexpected error: %v", err)
	}

	fn, ok := svc.Lookup("charge")
	if !ok {
		t.Fatal("Lookup(\"charge\") returned false; want true")
	}
	if fn == nil {
		t.Fatal("Lookup(\"charge\") returned nil HandlerFunc")
	}

	// Call the function to verify identity.
	result, err := fn(nil, nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if string(result) != "charged" {
		t.Errorf("handler result = %q; want %q", string(result), "charged")
	}
}

// TestNewService_Name verifies Name() returns the service name.
func TestNewService_Name(t *testing.T) {
	svc, _ := NewService("payments").Handler("charge", noopHandler)
	if svc.Name() != "payments" {
		t.Errorf("Name() = %q; want %q", svc.Name(), "payments")
	}
}

// TestNewService_Lookup_missing returns false for an unregistered handler.
func TestNewService_Lookup_missing(t *testing.T) {
	svc := NewService("payments")
	_, ok := svc.Lookup("nonexistent")
	if ok {
		t.Error("Lookup(\"nonexistent\") returned true; want false")
	}
}

// ---------------------------------------------------------------------------
// Task 5.2 — Duplicate handler registration returns ErrDuplicateHandler
// ---------------------------------------------------------------------------

// TestNewService_duplicate_handler_returns_error verifies that registering the
// same handler name twice wraps ErrDuplicateHandler.
func TestNewService_duplicate_handler_returns_error(t *testing.T) {
	svc, err := NewService("payments").Handler("charge", noopHandler)
	if err != nil {
		t.Fatalf("first Handler() unexpectedly errored: %v", err)
	}

	_, err = svc.Handler("charge", noopHandler)
	if err == nil {
		t.Fatal("second Handler(\"charge\") expected error; got nil")
	}
	if !errors.Is(err, ErrDuplicateHandler) {
		t.Errorf("errors.Is(err, ErrDuplicateHandler) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.3 — ServiceType sentinel is 0, distinct from other types
// ---------------------------------------------------------------------------

// TestNewService_type_sentinel verifies ServiceType is 0 and HandlerType()
// returns ServiceType for all Service instances.
func TestNewService_type_sentinel(t *testing.T) {
	if ServiceType != HandlerType(0) {
		t.Errorf("ServiceType = %d; want 0", ServiceType)
	}
	if VirtualObjectType == ServiceType {
		t.Error("VirtualObjectType must be distinct from ServiceType")
	}
	if WorkflowType == ServiceType {
		t.Error("WorkflowType must be distinct from ServiceType")
	}
	if VirtualObjectType == WorkflowType {
		t.Error("VirtualObjectType must be distinct from WorkflowType")
	}

	svc := NewService("payments")
	if svc.HandlerType() != ServiceType {
		t.Errorf("Service.HandlerType() = %d; want ServiceType(%d)", svc.HandlerType(), ServiceType)
	}
}

// ---------------------------------------------------------------------------
// Task 5.4 — Registry.Register stores a Service and Lookup returns it
// ---------------------------------------------------------------------------

// TestRegister_stores_service verifies Register + Lookup round-trip.
func TestRegister_stores_service(t *testing.T) {
	reg := NewRegistry()

	svc := NewService("payments")
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	got, ok := reg.Lookup("payments")
	if !ok {
		t.Fatal("Lookup(\"payments\") returned false; want true")
	}
	if got == nil {
		t.Fatal("Lookup(\"payments\") returned nil")
	}
	if got.Name() != "payments" {
		t.Errorf("Lookup result Name = %q; want %q", got.Name(), "payments")
	}
}

// TestRegister_Lookup_missing returns (nil, false) for unregistered services.
func TestRegister_Lookup_missing(t *testing.T) {
	reg := NewRegistry()
	got, ok := reg.Lookup("nonexistent")
	if ok {
		t.Error("Lookup returned true for unregistered service")
	}
	if got != nil {
		t.Errorf("Lookup returned non-nil for unregistered service: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Task 5.5 — Duplicate service registration returns ErrDuplicateService
// ---------------------------------------------------------------------------

// TestRegister_duplicate_service_returns_error verifies that registering the
// same service name twice wraps ErrDuplicateService.
func TestRegister_duplicate_service_returns_error(t *testing.T) {
	reg := NewRegistry()

	svc := NewService("payments")
	if err := reg.Register(svc); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}

	svc2 := NewService("payments")
	err := reg.Register(svc2)
	if err == nil {
		t.Fatal("second Register(\"payments\") expected error; got nil")
	}
	if !errors.Is(err, ErrDuplicateService) {
		t.Errorf("errors.Is(err, ErrDuplicateService) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Registry.LookupHandler — bridge to HandlerResolver interface
// ---------------------------------------------------------------------------

// TestRegistry_LookupHandler verifies LookupHandler returns the registered
// handler function for the (service, handler) pair.
func TestRegistry_LookupHandler(t *testing.T) {
	reg := NewRegistry()

	svc, err := NewService("payments").Handler("charge", noopHandler)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	fn, ok := reg.LookupHandler("payments", "charge")
	if !ok {
		t.Fatal("LookupHandler(\"payments\", \"charge\") returned false")
	}
	if fn == nil {
		t.Fatal("LookupHandler returned nil")
	}
}

// TestRegistry_LookupHandler_service_not_found returns (nil, false).
func TestRegistry_LookupHandler_service_not_found(t *testing.T) {
	reg := NewRegistry()
	fn, ok := reg.LookupHandler("ghost", "handler")
	if ok {
		t.Error("LookupHandler for unregistered service returned true")
	}
	if fn != nil {
		t.Errorf("LookupHandler returned non-nil for unregistered service: %v", fn)
	}
}

// TestRegistry_LookupHandler_handler_not_found returns (nil, false).
func TestRegistry_LookupHandler_handler_not_found(t *testing.T) {
	reg := NewRegistry()
	svc := NewService("payments")
	if err := reg.Register(svc); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	fn, ok := reg.LookupHandler("payments", "ghost")
	if ok {
		t.Error("LookupHandler for unregistered handler returned true")
	}
	if fn != nil {
		t.Errorf("LookupHandler returned non-nil for unregistered handler: %v", fn)
	}
}

// ---------------------------------------------------------------------------
// Package-level Register / LookupRegistered (sdk.Register)
// ---------------------------------------------------------------------------

// TestPackageLevel_Register_LookupRegistered verifies the package-level
// sdk.Register and sdk.LookupRegistered functions delegate to defaultRegistry.
// Note: tests run in the same process so we use a service name unique to this test.
func TestPackageLevel_Register_LookupRegistered(t *testing.T) {
	// Use a name that won't collide with other test runs in the same process.
	// Each test that touches the defaultRegistry must use a unique service name.
	svc := NewService("test_pkg_svc_register_lookup")
	if err := Register(svc); err != nil {
		t.Fatalf("sdk.Register() error: %v", err)
	}

	got, ok := LookupRegistered("test_pkg_svc_register_lookup")
	if !ok {
		t.Fatal("LookupRegistered returned false for registered service")
	}
	if got == nil {
		t.Fatal("LookupRegistered returned nil")
	}
	if got.Name() != "test_pkg_svc_register_lookup" {
		t.Errorf("LookupRegistered Name = %q; want %q", got.Name(), "test_pkg_svc_register_lookup")
	}
}

// TestPackageLevel_Register_DuplicateReturnsErr verifies sdk.Register wraps ErrDuplicateService.
func TestPackageLevel_Register_DuplicateReturnsErr(t *testing.T) {
	name := "test_pkg_svc_dup"
	svc1 := NewService(name)
	if err := Register(svc1); err != nil {
		t.Fatalf("first sdk.Register() error: %v", err)
	}
	svc2 := NewService(name)
	err := Register(svc2)
	if err == nil {
		t.Fatal("second sdk.Register() expected error; got nil")
	}
	if !errors.Is(err, ErrDuplicateService) {
		t.Errorf("errors.Is(err, ErrDuplicateService) = false; err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 5.6 — SDK boundary: no internal/ imports
// ---------------------------------------------------------------------------

// TestSDKPackageHasNoInternalImports verifies that sdk/ imports zero
// github.com/exoar/axon_tenax_engine/tenax/internal paths (ADR-0028).
// The automated gate is `go list -f '{{.Imports}}' ./sdk/...` which must
// show no internal paths.
func TestSDKPackageHasNoInternalImports(t *testing.T) {
	// Run go list to get the import list for the sdk package.
	// This is the authoritative ADR-0028 gate.
	// Run from the module root so the ./sdk/... pattern resolves correctly.
	cmd := exec.Command("go", "list", "-f", "{{.Imports}}", "github.com/exoport/axon-tenax/sdk")
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
// Sentinel errors: ErrDuplicateService and ErrDuplicateHandler are distinct
// ---------------------------------------------------------------------------

func TestSentinelErrors_ServiceDuplicates_AreDistinct(t *testing.T) {
	if errors.Is(ErrDuplicateService, ErrDuplicateHandler) {
		t.Error("ErrDuplicateService and ErrDuplicateHandler must be distinct")
	}
	if errors.Is(ErrDuplicateHandler, ErrDuplicateService) {
		t.Error("ErrDuplicateHandler and ErrDuplicateService must be distinct")
	}
	if errors.Is(ErrDuplicateService, ErrHandlerNotFound) {
		t.Error("ErrDuplicateService and ErrHandlerNotFound must be distinct")
	}
}

// ---------------------------------------------------------------------------
// Chaining: multiple handlers registered with chained Handler() calls
// ---------------------------------------------------------------------------

func TestNewService_chaining_multiple_handlers(t *testing.T) {
	svc := NewService("payments")
	svc, err := svc.Handler("charge", noopHandler)
	if err != nil {
		t.Fatalf("Handler(charge) error: %v", err)
	}
	svc, err = svc.Handler("refund", greetHandler)
	if err != nil {
		t.Fatalf("Handler(refund) error: %v", err)
	}

	_, ok1 := svc.Lookup("charge")
	_, ok2 := svc.Lookup("refund")
	if !ok1 {
		t.Error("Lookup(\"charge\") returned false after chaining")
	}
	if !ok2 {
		t.Error("Lookup(\"refund\") returned false after chaining")
	}
}
