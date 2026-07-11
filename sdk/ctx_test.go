package sdk //nolint:testpackage // white-box test of unexported sdk internals

import (
	"testing"
	"time"
)

// concreteCtx is a stub implementation used exclusively for compile-time verification
// that the sdk.Context interface is satisfied with the correct Run signature.
// It MUST NOT import any internal/ package (ADR-0028).
// This stub is intentionally unexported and lives only in the test file.
type concreteCtx struct{}

// Compile-time assertion: concreteCtx must satisfy sdk.Context.
// If the Run signature or any other method signature changes incompatibly,
// this line will produce a compile error.
var _ Context = (*concreteCtx)(nil)

// Run satisfies the Context.Run interface method with the exact signature:
// Run(name string, fn func(opID string) ([]byte, error)) ([]byte, error)
func (c *concreteCtx) Run(_ string, fn func(string) ([]byte, error)) ([]byte, error) {
	return fn("inv_test/0")
}

// The remaining stub methods are required to satisfy the full Context interface.

func (c *concreteCtx) Sleep(_ time.Duration) error { return nil }

func (c *concreteCtx) Timer(_ time.Duration) (Promise, error) { return nil, nil } //nolint:nilnil // test stub double returns no value and no error

func (c *concreteCtx) Get(_ string) (value []byte, found bool, err error) { return nil, false, nil }

func (c *concreteCtx) Set(_ string, _ []byte) error { return nil }

func (c *concreteCtx) Clear(_ string) error { return nil }

func (c *concreteCtx) List() ([]string, error) { return nil, nil }

func (c *concreteCtx) Call(_, _ string, _ []byte) ([]byte, error) { return nil, nil }

func (c *concreteCtx) Send(_, _ string, _ []byte) (string, error) { return "", nil }

func (c *concreteCtx) CallWorkflow(_, _ string, _ []byte) ([]byte, error) { return nil, nil }

func (c *concreteCtx) SendWorkflow(_, _ string, _ []byte) (string, error) { return "", nil }

func (c *concreteCtx) SendDelayed(_, _ string, _ []byte, _ time.Duration) (string, error) {
	return "", nil
}

func (c *concreteCtx) SendAt(_, _ string, _ []byte, _ time.Time) (string, error) {
	return "", nil
}

func (c *concreteCtx) Awakeable() (string, Promise, error) { return "", nil, nil }

func (c *concreteCtx) Promise(_ string) Promise { return nil }

func (c *concreteCtx) CompleteAwakeable(_ string, _ []byte) error { return nil }

func (c *concreteCtx) RejectAwakeable(_, _ string) error { return nil }

func (c *concreteCtx) Now() time.Time { return time.Time{} }

func (c *concreteCtx) Rand() float64 { return 0 }

func (c *concreteCtx) UUID() string { return "" }

func (c *concreteCtx) GetVersion(_ string, _, _ int) (int, error) { return 0, nil }

func (c *concreteCtx) RegisterCompensation(_ func(ctx Context) error) (string, error) {
	return "cmp_stub", nil
}

func (c *concreteCtx) Race(_ ...Promise) ([]byte, error)                { return nil, nil }
func (c *concreteCtx) AwaitAny(_ ...Promise) ([]byte, error)            { return nil, nil }
func (c *concreteCtx) AwaitAll(_ ...Promise) ([]byte, error)            { return nil, nil }
func (c *concreteCtx) AwaitFirstSucceeded(_ ...Promise) ([]byte, error) { return nil, nil }
func (c *concreteCtx) AwaitAllSucceeded(_ ...Promise) ([]byte, error)   { return nil, nil }

// TestContextInterface_RunSignature verifies that the Context interface declares
// Run(name string, fn func(opID string) ([]byte, error)) ([]byte, error) and that
// concreteCtx satisfies it. The compile-time assertion above is the real gate.
func TestContextInterface_RunSignature(t *testing.T) {
	// Compile-time check: var _ Context = (*concreteCtx)(nil) above will fail if
	// the Run method signature is wrong. This test confirms the package builds
	// successfully with the expected interface shape.
	var c Context = &concreteCtx{}
	result, err := c.Run("charge", func(opID string) ([]byte, error) {
		if opID == "" {
			t.Error("opID must not be empty")
		}
		return []byte(`"ok"`), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(result) != `"ok"` {
		t.Fatalf("Run result: got %q, want %q", string(result), `"ok"`)
	}
}

// TestContextInterface_NowRandUUID verifies that Now() time.Time, Rand() float64,
// and UUID() string are present on the Context interface with correct signatures
// (Task 7.5, AC: 7, ADR-0028).
func TestContextInterface_NowRandUUID(t *testing.T) {
	// Compile-time check: var _ Context = (*concreteCtx)(nil) above already
	// validates these signatures. This runtime test confirms correct return types.
	var c Context = &concreteCtx{}

	// Now() must return time.Time (no error).
	ts := c.Now()
	_ = ts // value verified; type is enforced at compile time

	// Rand() must return float64 (no error).
	r := c.Rand()
	_ = r

	// UUID() must return string (no error).
	u := c.UUID()
	_ = u

	// Confirm the zero values of the stubs compile and run without panic.
	t.Logf("Now()=%v Rand()=%v UUID()=%q", ts, r, u)
}
