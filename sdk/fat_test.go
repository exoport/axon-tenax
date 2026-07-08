package sdk_test

// fat_test.go — unit tests for the D1 fat-mode SDK shim (Story 30.1, AC: 1).
//
// Tests:
//   - Import graph assertion: go list -deps ./sdk/... must NOT include the five
//     forbidden internal/ engine packages (ADR-0028, ADR-0036 D1).
//   - HasTerminal: returns correct bool for terminal/non-terminal MachineSnapshot.
//   - FatWorkerSDKVersion: constant is non-empty.
//   - ErrVersionSkew: is a properly defined sentinel error.
//
// No build tags — runs with plain `go test ./sdk/...` (no NATS required, Story 30.1 Task 5.2).

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/exoport/axon-tenax/sdk"
)

// ---------------------------------------------------------------------------
// Import graph assertion (AC: 1, Task 5.1)
// ---------------------------------------------------------------------------

// forbiddenImports are the five engine packages that MUST NOT appear in
// the sdk/ transitive import graph. BYO workers cannot import these directly
// (Go's internal/ rule), and the D1 shim must not re-export them (ADR-0028).
var forbiddenImports = []string{
	"github.com/exoar/axon_tenax_engine/tenax/internal/journal",
	"github.com/exoar/axon_tenax_engine/tenax/internal/kvstate",
	"github.com/exoar/axon_tenax_engine/tenax/internal/statemachine",
	"github.com/exoar/axon_tenax_engine/tenax/internal/lease",
	"github.com/exoar/axon_tenax_engine/tenax/internal/idempotency",
}

// TestSDKImportGraphNoForbiddenInternals asserts that the sdk/ package's transitive
// import graph does NOT include any of the five forbidden internal/ engine packages.
//
// This is the compile-time boundary enforcement oracle for Story 30.1's primary AC.
// It runs go list -deps ./sdk/... and checks the output for forbidden packages.
func TestSDKImportGraphNoForbiddenInternals(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("go list not available on wasm/js")
	}

	// Run: go list -deps github.com/exoport/axon-tenax/sdk/...
	// This lists all transitive dependencies of the sdk package(s).
	cmd := exec.Command("go", "list", "-deps", "github.com/exoport/axon-tenax/sdk/...") //nolint:noctx // boundary test shells out to go list
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps sdk/... failed: %v\noutput: %s", err, out)
	}

	deps := string(out)
	lines := strings.Split(deps, "\n")

	// Build a set of resolved dep names for efficient lookup.
	depSet := make(map[string]bool, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			depSet[line] = true
		}
	}

	// Assert none of the forbidden engine packages appear in the dep set.
	for _, forbidden := range forbiddenImports {
		if depSet[forbidden] {
			t.Errorf("FAIL: sdk/ imports forbidden internal engine package %q\n"+
				"  The D1 shim in sdk/fat.go must NOT import this package directly (ADR-0028, ADR-0036 D1).\n"+
				"  Refactor sdk/fat.go to delegate via an interface instead of a direct import.", forbidden)
		}
	}

	if !t.Failed() {
		t.Logf("PASS: sdk/ import graph contains none of the %d forbidden internal packages", len(forbiddenImports))
	}
}

// ---------------------------------------------------------------------------
// HasTerminal tests (AC: 1, Task 2)
// ---------------------------------------------------------------------------

// mockMachineSnapshot is a test-double that implements sdk.MachineSnapshot.
// Used to verify HasTerminal without needing a real statemachine.Machine.
type mockMachineSnapshot struct {
	terminal bool
}

func (m *mockMachineSnapshot) HasTerminal() bool { return m.terminal }

// TestHasTerminal_ReturnsTrue verifies that HasTerminal returns true when the
// MachineSnapshot reports a terminal entry (crash-before-ack redelivery guard).
func TestHasTerminal_ReturnsTrue(t *testing.T) {
	m := &mockMachineSnapshot{terminal: true}
	if !sdk.HasTerminal(m) {
		t.Error("HasTerminal returned false for a terminal machine snapshot; expected true")
	}
}

// TestHasTerminal_ReturnsFalse verifies that HasTerminal returns false when the
// MachineSnapshot has no terminal entry (live dispatch path).
func TestHasTerminal_ReturnsFalse(t *testing.T) {
	m := &mockMachineSnapshot{terminal: false}
	if sdk.HasTerminal(m) {
		t.Error("HasTerminal returned true for a non-terminal machine snapshot; expected false")
	}
}

// ---------------------------------------------------------------------------
// FatWorkerSDKVersion tests (AC: 1, Task 1.3)
// ---------------------------------------------------------------------------

// TestFatWorkerSDKVersion_NonEmpty verifies that the version stamp constant is
// non-empty. An empty constant would prevent Story 30.3's skew check from working.
func TestFatWorkerSDKVersion_NonEmpty(t *testing.T) {
	if sdk.FatWorkerSDKVersion == "" {
		t.Error("FatWorkerSDKVersion is empty; must be a non-empty version string (e.g. \"v0.1.0\")")
	}
}

// TestFatWorkerSDKVersion_StartsWithV verifies the version stamp follows semver "vX.Y.Z" form.
func TestFatWorkerSDKVersion_StartsWithV(t *testing.T) {
	v := sdk.FatWorkerSDKVersion
	if v == "" || v[0] != 'v' {
		t.Errorf("FatWorkerSDKVersion %q does not start with 'v'; expected semver form vX.Y.Z", v)
	}
}

// ---------------------------------------------------------------------------
// Sentinel error tests (ADR-0030)
// ---------------------------------------------------------------------------

// TestErrVersionSkew_NotNil verifies ErrVersionSkew is a non-nil sentinel.
func TestErrVersionSkew_NotNil(t *testing.T) {
	if sdk.ErrVersionSkew == nil {
		t.Error("ErrVersionSkew is nil; must be a non-nil sentinel error")
	}
}

// TestErrFatConflict_NotNil verifies ErrFatConflict is a non-nil sentinel.
func TestErrFatConflict_NotNil(t *testing.T) {
	if sdk.ErrFatConflict == nil {
		t.Error("ErrFatConflict is nil; must be a non-nil sentinel error")
	}
}

// TestErrFatTimeout_NotNil verifies ErrFatTimeout is a non-nil sentinel.
func TestErrFatTimeout_NotNil(t *testing.T) {
	if sdk.ErrFatTimeout == nil {
		t.Error("ErrFatTimeout is nil; must be a non-nil sentinel error")
	}
}

// TestErrFatUsage_NotNil verifies ErrFatUsage is a non-nil sentinel.
func TestErrFatUsage_NotNil(t *testing.T) {
	if sdk.ErrFatUsage == nil {
		t.Error("ErrFatUsage is nil; must be a non-nil sentinel error")
	}
}
