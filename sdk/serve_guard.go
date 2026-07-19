// serve_guard.go — the serve-time durable-ctx.* handler rejection guard (Story 68.1,
// ADR-0045/ADR-0047/ADR-0030/ADR-0017/ADR-0028/ADR-0051).
//
// Story 65.1 review cycle 2 (Opus adversarial) left two findings against sdk.Serve:
//
//   - F1 (specification only, no SDK-repo code): sdk.Serve's original interim guard rejected
//     by HANDLER KIND ALONE (reject Workflow/VirtualObject, permit Service unconditionally).
//     F1 found that insufficient — a Service-only registry may legally call any of the 15
//     non-state-bound durable ctx.* verbs, proven materially (not hypothetically) by Cortex's
//     own cortexd-scheduler, whose Service-registered chain.Arm/chain.Fire reach six ctx.Run
//     call sites — and specified the corrected two-tier design this file implements: an
//     unconditional KIND tier (Workflow/VirtualObject always rejected) plus a
//     reject-by-default ATTESTATION tier for a Service-only registry, lifted only by the
//     caller's explicit WithNoDurableContextAttestation() opt-in.
//   - F2 (deferred, not fixed): remoteDispatchContext.Promise/Now/Rand/UUID (serve.go, this
//     package) return a SILENT ZERO VALUE with a nil error instead of
//     ErrRemoteContextUnsupported — the honesty-invariant violation (Never-Do #7, ADR-0017)
//     Epic 68 exists to close. Those four verbs have no error return in the Context interface
//     (ctx.go: Promise/Now/Rand/UUID) — call-time rejection is structurally impossible, which
//     is exactly why the fix must live here, at Serve() call time before any dispatch begins,
//     rather than inside remoteDispatchContext itself.
//
// This file implements F1's corrected spec as real code for the first time and, by doing so,
// closes F2: no registered handler can ever reach remoteDispatchContext.Promise/Now/Rand/UUID
// without the caller having made the explicit, attributable WithNoDurableContextAttestation()
// claim first (Story 68.1 AC1-AC3).
//
// Serve() cannot introspect handler closure bodies (F1's own conclusion) — do not attempt AST
// parsing or reflection-based body scanning as a substitute; that path was considered and
// rejected as unsound in 65.1's review (a permissive default that guesses wrong is exactly the
// footgun this guard exists to close). A false attestation (a Service handler that calls one of
// these verbs anyway despite the caller's claim) is the caller's own explicit, attributable
// risk, not a silent engine-side lie — the caller made an affirmative false claim, the SDK did
// not fabricate a success on its own.

package sdk

import (
	"errors"
	"fmt"
)

// ErrServeDurableContextRejected is the base sentinel for the serve-time durable-ctx.*
// rejection guard (ADR-0030). Recovered via errors.Is(err, ErrServeDurableContextRejected)
// without string matching; the richer *ServeDurableContextRejectedError carrying the
// what/cause/hint triad is recovered via errors.As(err, &target). Mirrors the engine's own
// internal/substrate.PreflightError struct+Unwrap-to-sentinel shape.
var ErrServeDurableContextRejected = errors.New("sdk: Serve: durable ctx.* handler rejected")

// ServeDurableContextRejectedError is the classed error Serve returns (ADR-0030 what/cause/hint
// triad) when reg contains a handler whose durable ctx.* use cannot be honoured under
// remoteDispatchContext's current single-request/response model. Returned for BOTH rejection
// tiers (kind and attestation) — the What/Cause/Hint text distinguishes which tier fired.
type ServeDurableContextRejectedError struct {
	What  string // one-line problem statement
	Cause string // why: which tier fired and why it cannot be honoured remotely
	Hint  string // what to do next
}

// Error renders the ADR-0030 three-line what/cause/hint triad.
func (e *ServeDurableContextRejectedError) Error() string {
	return "error: " + e.What + "\n  cause: " + e.Cause + "\n  hint:  " + e.Hint
}

// Unwrap resolves to ErrServeDurableContextRejected so errors.Is(err,
// ErrServeDurableContextRejected) works without string matching (ADR-0030).
func (e *ServeDurableContextRejectedError) Unwrap() error { return ErrServeDurableContextRejected }

// WithNoDurableContextAttestation is the additive Serve() opt-in (Story 68.1) that lifts the
// attestation-tier reject-by-default guard for a Service-only registry (zero VirtualObject,
// zero Workflow handlers registered). Passing this option is an explicit, attributable claim by
// the caller that NONE of the registered Service handlers call any durable ctx.* verb that
// requires the engine-side journal — Run/Sleep/Timer/Get/Set/Clear/List/Call/Send/
// CallWorkflow/SendWorkflow/SendDelayed/SendAt/Awakeable/Promise/CompleteAwakeable/
// RejectAwakeable/Now/Rand/UUID/GetVersion/RegisterCompensation/Race/AwaitAny/AwaitAll/
// AwaitFirstSucceeded/AwaitAllSucceeded — 27 verbs total — all of which remoteDispatchContext
// either rejects synchronously with ErrRemoteContextUnsupported (23 of 27 verbs) or, for
// Promise/Now/Rand/UUID specifically, silently returns a zero value with a nil error today
// (Story 65.1 review cycle 2 finding F2, closed by this guard's default-reject behavior).
//
// Serve() cannot verify this claim: it cannot introspect handler closure bodies (Story 65.1
// finding F1's own conclusion). Do NOT attempt AST parsing, reflection-based body scanning, or
// any other form of automatic "detect durable ctx.* use" as a substitute — that path was
// considered and rejected as unsound. A false attestation is the caller's own explicit,
// attributable risk, not a silent engine-side lie.
//
// This option has NO effect on a registry that also contains a VirtualObject or Workflow
// handler: the kind tier rejects those unconditionally regardless of attestation — see
// validateDurableContextGuard.
func WithNoDurableContextAttestation() ServeOption {
	return func(c *serveConfig) {
		c.noDurableContextAttested = true
	}
}

// validateDurableContextGuard implements the two-tier reject-by-default +
// explicit-attestation serve-time guard (Story 65.1 finding F1, implemented here; closes
// finding F2 — Story 68.1 AC1-AC3). Called once at the top of Serve(), before jetstream.New and
// before any dispatch goroutine starts, so a rejection never leaves a partially-started worker
// and no in-flight invocation is ever the first one to discover the gap (AC1).
//
//   - Kind tier (unconditional): reg contains at least one VirtualObject or Workflow handler.
//     These kinds are keyed/durable-state-bound by construction and may call any ctx.* durable
//     verb; this tier is never lifted by attested, regardless of its value.
//   - Attestation tier (reject-by-default): reg is Service-only (zero VirtualObject, zero
//     Workflow handlers) and attested is false. A Service handler may legally call any durable
//     ctx.* verb, and Serve cannot introspect handler bodies to verify it does not — so a
//     Service-only registry is rejected unless the caller opts in via
//     WithNoDurableContextAttestation().
//
// A registry with zero registered handlers of any kind (the pre-existing degenerate case —
// Serve's own doc comment: "skips the Gap A/B loops entirely") passes both tiers unconditionally
// — there is no handler that could ever reach a durable ctx.* verb.
func validateDurableContextGuard(reg *Registry, attested bool) error {
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	if len(reg.virtualObjects) > 0 || len(reg.workflows) > 0 {
		return newKindTierRejectedError(reg)
	}
	if len(reg.services) > 0 && !attested {
		return newAttestationTierRejectedError(reg)
	}
	return nil
}

// newKindTierRejectedError builds the unconditional kind-tier rejection (Story 65.1 F1): reg
// contains at least one Virtual Object or Workflow handler.
func newKindTierRejectedError(reg *Registry) *ServeDurableContextRejectedError {
	return &ServeDurableContextRejectedError{
		What: fmt.Sprintf(
			"sdk.Serve: registry has %d Virtual Object handler(s) and %d Workflow handler(s) registered",
			len(reg.virtualObjects), len(reg.workflows),
		),
		Cause: "Virtual Object and Workflow handlers are keyed/durable-state-bound by construction " +
			"and may call any ctx.* durable verb, including ctx.Promise/ctx.Now/ctx.Rand/ctx.UUID, which " +
			"remoteDispatchContext cannot honour remotely and silently returns a zero value with a nil " +
			"error today (Story 65.1 review cycle 2 finding F2); this rejection tier is unconditional and " +
			"is never lifted by WithNoDurableContextAttestation",
		Hint: "run this handler under tenax/pkg/worker (ADR-0051) instead of sdk.Serve — Virtual Object " +
			"and Workflow handlers are not eligible for sdk.Serve's single-request/response " +
			"remote-dispatch model, attested or not",
	}
}

// newAttestationTierRejectedError builds the reject-by-default attestation-tier rejection
// (Story 65.1 F1): reg is Service-only (zero Virtual Object/Workflow handlers) but the caller
// has not passed WithNoDurableContextAttestation() to Serve.
func newAttestationTierRejectedError(reg *Registry) *ServeDurableContextRejectedError {
	return &ServeDurableContextRejectedError{
		What: fmt.Sprintf(
			"sdk.Serve: registry has %d Service handler(s) registered without WithNoDurableContextAttestation",
			len(reg.services),
		),
		Cause: "a Service handler may legally call any ctx.* durable verb, including " +
			"ctx.Promise/ctx.Now/ctx.Rand/ctx.UUID, which remoteDispatchContext cannot honour remotely " +
			"and silently returns a zero value with a nil error today (Story 65.1 review cycle 2 finding " +
			"F2); Serve cannot introspect handler closure bodies to verify none of them do, so a " +
			"Service-only registry is rejected by default",
		Hint: "pass sdk.WithNoDurableContextAttestation() to Serve only if every registered Service " +
			"handler makes zero durable ctx.* calls, or run this handler under tenax/pkg/worker " +
			"(ADR-0051) instead if it needs them",
	}
}
