// fat.go — fat-mode D1 SDK shim (Story 30.1, ADR-0036 D1).
//
// Exposes the minimal D1 surface required for fat-mode dispatch:
//
//	(a) HasTerminal — idempotency check (ADR-0036 D4) via MachineSnapshot interface
//	(b) SubmitResult — CAS-only journal append (ADR-0036 D1, ADR-0004)
//	(c) FatWorkerSDKVersion — engine version constant for skew check (ADR-0036 D3)
//
// Import boundary: this file does NOT import any of the five forbidden engine packages
// (internal/journal, internal/kvstate, internal/statemachine, internal/lease,
// internal/idempotency), nor any other internal/ package. This preserves the ADR-0028
// boundary test (TestSDKPackageHasNoInternalImports). Go's internal/ rule prevents BYO
// workers from importing those packages directly; this shim provides the only promoted
// surface (ADR-0028, ADR-0036 D1).
//
// JCS encoding is performed via github.com/gowebpki/jcs (NOT gobl/c14n, which produces
// different bytes — CLAUDE.md Never-Do #3) combined with encoding/json — the same pipeline
// as internal/jcs.Marshal without the internal/ import.
//
// ADR-0016 reconciliation: ADR-0016 prohibits Nex-placed binaries from importing the five
// forbidden engine packages. ADR-0036 defines the bounded fat-mode exception: the D1 shim
// exposed here is the ONLY surface promoted to sdk/ for fat-mode use. ADR-0016 remains in
// force for all thin adapters (NexAdapter, HTTPAdapter, ContainerAdapter). ADR-0036 is the
// amendment that defines the bounded fat-mode exception. See also the reconciliation comment
// in internal/runtime/nex_adapter.go near line 66-71 (ADR-0016 boundary comment block).

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	gojcs "github.com/gowebpki/jcs"
	"github.com/nats-io/nats.go/jetstream"
)

// ---------------------------------------------------------------------------
// Version stamp and sentinel errors (ADR-0036 D3, ADR-0030)
// ---------------------------------------------------------------------------

// FatWorkerSDKVersion is the engine version stamp embedded in every fat-worker binary.
// Story 30.3 reads this constant at startup and compares it against the running tenaxd's
// declared compatible engine version range. If the embedded version falls outside the
// declared compatible window, startup fails with exit code 4 (error class "precondition").
//
// This constant must be updated on every breaking engine change — see ADR-0036 D3.
const FatWorkerSDKVersion = "v0.1.0"

// ErrVersionSkew is returned when the fat-worker SDK version does not fall within
// the engine's declared compatible version range (ADR-0036 D3).
// Error class: precondition (exit code 4, aligns with ADR-0008 preflight semantics).
// Wrap with %w at call sites.
var ErrVersionSkew = errors.New("fat worker SDK version skew")

// ErrFatConflict is returned by SubmitResult when the CAS fence is violated
// (wrong-last-sequence). The caller must re-read LastJournalSeq and retry.
// Maps to error class "conflict", exit code 4 (ADR-0030).
var ErrFatConflict = errors.New("sdk/fat: CAS conflict — wrong last sequence")

// ErrFatTimeout is returned when all SubmitResult retry attempts are exhausted.
// Maps to error class "timeout", exit code 5 (ADR-0030).
var ErrFatTimeout = errors.New("sdk/fat: operation timed out after max retries")

// ErrFatUsage is returned on API misuse (e.g. expectedLast=0 for SubmitResult).
// Maps to error class "usage", exit code 2 (ADR-0030).
var ErrFatUsage = errors.New("sdk/fat: API usage error")

// ---------------------------------------------------------------------------
// Internal constants — mirrors internal/journal without importing it
// ---------------------------------------------------------------------------

// fatSubmitTimeout is the per-attempt deadline for SubmitResult CAS operations.
// Short per-op deadline per ADR-0007 to avoid stalls during NATS leader failover.
const fatSubmitTimeout = 5 * time.Second

// fatJournalSubjectPrefix is the per-invocation journal subject prefix.
// Subject form: "tenax.journal.<inv>" — mirrors journal.SubjectForInv (ADR-0020).
const fatJournalSubjectPrefix = "tenax.journal."

// fatJournalStreamName is the JetStream stream name for the journal.
// Value: "TENAX_JOURNAL" — mirrors journal.StreamJournal (ADR-0020).
const fatJournalStreamName = "TENAX_JOURNAL"

// fatTypeOutput is the terminal Output/End entry type byte (value 3, frozen by ADR-0009).
// Mirrors journal.TypeOutput without importing internal/journal.
const fatTypeOutput uint8 = 3

// fatMaxAttempts is the CAS retry budget for SubmitResult (mirrors journal.maxAttempts).
const fatMaxAttempts = 3

// fatChecksumTable is the Castagnoli CRC-32 lookup table for app-level checksums (ADR-0009).
var fatChecksumTable = crc32.MakeTable(crc32.Castagnoli)

// ---------------------------------------------------------------------------
// MachineSnapshot — minimal interface for HasTerminal (ADR-0036 D4)
// ---------------------------------------------------------------------------

// MachineSnapshot is the minimal interface that the fat adapter's HasTerminal guard
// requires. *statemachine.Machine satisfies this interface; fat-mode callers pass
// the hydrated machine directly without sdk/fat.go importing internal/statemachine.
//
// This interface is the promoted surface for D4 of ADR-0036. The reference
// implementation is internal/statemachine/hydrate.go:HasTerminal().
type MachineSnapshot interface {
	// HasTerminal reports whether the machine's journal snapshot already contains
	// a terminal entry (Output(#3) COMPLETED or Error(#5) FAILED).
	HasTerminal() bool
}

// HasTerminal reports whether the machine's in-memory journal snapshot already
// contains a terminal entry.
//
// HasTerminal returns true if the machine's in-memory journal snapshot already contains
// a terminal entry. Fat-mode dispatch MUST call this before any state-machine write.
// If true, return the recorded result without re-executing. See ADR-0036 D4.
//
// This is a thin promotion of internal/statemachine/hydrate.go:HasTerminal().
// It delegates to the MachineSnapshot interface — *statemachine.Machine satisfies it —
// ensuring bit-identical behavior with the in-process adapter path
// (internal/runtime/inproc.go:268,295).
//
// DO NOT reimplement the terminal-entry scan; always delegate via MachineSnapshot.
func HasTerminal(m MachineSnapshot) bool {
	return m.HasTerminal()
}

// ---------------------------------------------------------------------------
// Internal payload types — mirrors internal/journal without importing it
// ---------------------------------------------------------------------------

// fatOutputPayload is the JCS-encoded body of a terminal Output/End journal entry.
// Mirrors journal.OutputPayload without importing internal/journal.
// No omitempty on any field — present-with-default and absent are distinct (ADR-0009).
type fatOutputPayload struct {
	// Result is the raw JCS-encoded result bytes returned by the invocation.
	Result []byte `json:"result"`
	// IsTerminal is always true for Output/End entries.
	IsTerminal bool `json:"isTerminal"`
}

// fatJournalEntry is the outer journal entry envelope.
// Mirrors journal.Entry without importing internal/journal.
// SeqNo is 0 for entries constructed before append (assigned by JetStream).
// No omitempty — present-with-default and absent must be distinguishable (ADR-0009).
type fatJournalEntry struct {
	Payload     []byte `json:"payload"`
	SeqNo       uint64 `json:"seqNo"`
	AppChecksum uint32 `json:"appChecksum"`
	Type        uint8  `json:"type"`
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// subjectForInv returns the canonical journal subject for inv.
// Format: "tenax.journal.<inv>" — mirrors journal.SubjectForInv (ADR-0020).
func subjectForInv(inv string) string { return fatJournalSubjectPrefix + inv }

// fatJCSMarshal serializes v to RFC 8785 JCS canonical form.
// Mirrors internal/jcs.Marshal using github.com/gowebpki/jcs (not gobl/c14n).
// All durable-path serialization must use this (ADR-0010, CLAUDE.md Never-Do #3).
func fatJCSMarshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("sdk/fat: JCS marshal: json.Marshal: %w", err)
	}
	return gojcs.Transform(raw)
}

// isFatConflict returns true when err indicates a NATS wrong-last-msg-seq conflict.
// Mirrors journal.isWrongLastSeq without importing internal/journal.
func isFatConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return fatContains(msg, "wrong last msg seq") ||
		fatContains(msg, "wrong last sequence") ||
		fatContains(msg, "nats: wrong last msg seq")
}

// isFatTransient returns true when err is a transient error safe to retry.
// Mirrors journal.isTransient without importing internal/journal.
func isFatTransient(err error) bool {
	if err == nil {
		return false
	}
	if isFatConflict(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return fatContains(msg, "timeout") ||
		fatContains(msg, "deadline exceeded") ||
		fatContains(msg, "nats: connection closed") ||
		fatContains(msg, "nats: connection reset") ||
		fatContains(msg, "no responders")
}

// fatContains reports whether s contains sub.
func fatContains(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fatNatsCtx returns a fresh context with a short per-operation deadline.
// Mirrors natsutil.Ctx without importing internal/natsutil (ADR-0007).
// context.Background() is intentional here (not a caller-supplied parent) — this is a
// leaf per-operation deadline per ADR-0007, not a cancellation-propagation context.
func fatNatsCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// ---------------------------------------------------------------------------
// SubmitResult — CAS-only terminal journal append (ADR-0036 D1, ADR-0004)
// ---------------------------------------------------------------------------

// SubmitResult journals the terminal output for invocation inv via CAS-only append
// (Nats-Expected-Last-Subject-Sequence per ADR-0004). Never exposes raw journal.Append
// or kv.Put — only the CAS-gated terminal-output write.
//
// Parameters:
//   - ctx: parent context (reserved for cancellation propagation; per-attempt deadlines
//     are created inside via fatNatsCtx per ADR-0007)
//   - js: JetStream handle; passed in, never stored at package scope (ADR-0001)
//   - inv: invocation ID
//   - expectedLast: the caller's known last journal sequence (the CAS fence, ADR-0004)
//   - payload: raw JCS-encoded result bytes (written as the Result field of OutputPayload)
//
// The function constructs an OutputPayload{Result: payload, IsTerminal: true},
// JCS-encodes it (ADR-0010), computes a CRC-32C app-level checksum (ADR-0009), wraps it
// in an Output(#3) Entry, and appends it with the CAS fence.
//
// opID MUST have been embedded in the payload by the caller before calling SubmitResult
// to satisfy the (inv, index) opId requirement on every external effect (ADR-0006).
//
// Returns (newSeq, nil) on success. Returns (0, ErrFatConflict) on CAS fence violation —
// the caller must re-read LastJournalSeq and retry.
//
// NEVER call time.Now(), math/rand, or UUID generators in the fat adapter's handler body —
// use ctx.Now, ctx.Rand, ctx.UUID on the machine context (ADR-0011).
func SubmitResult(ctx context.Context, js jetstream.JetStream, inv string, expectedLast uint64, payload []byte) (uint64, error) {
	_ = ctx // per-op deadline created inside via fatNatsCtx (ADR-0007)

	if expectedLast == 0 {
		return 0, fmt.Errorf(
			"error: SubmitResult called with expectedLast=0\n  cause: at least one prior journal entry (Start) must exist\n  hint:  hydrate the journal before calling SubmitResult: %w",
			ErrFatUsage,
		)
	}

	// Build the OutputPayload — byte-identical to journal.OutputPayload (ADR-0009).
	op := fatOutputPayload{
		Result:     payload,
		IsTerminal: true,
	}
	innerBytes, err := fatJCSMarshal(op)
	if err != nil {
		return 0, fmt.Errorf("sdk/fat: SubmitResult inv %s: marshal OutputPayload: %w", inv, err)
	}

	// Compute CRC-32C app-level checksum over inner payload bytes (ADR-0009).
	checksum := crc32.Checksum(innerBytes, fatChecksumTable)

	// Build the outer Entry envelope — byte-identical to journal.Entry (ADR-0009).
	entry := fatJournalEntry{
		SeqNo:       0,
		Type:        fatTypeOutput,
		Payload:     innerBytes,
		AppChecksum: checksum,
	}
	outerBytes, err := fatJCSMarshal(entry)
	if err != nil {
		return 0, fmt.Errorf("sdk/fat: SubmitResult inv %s: marshal Entry: %w", inv, err)
	}

	subj := subjectForInv(inv)

	// CAS retry loop — mirrors journal.appendCore (ADR-0004, ADR-0007).
	// Each attempt uses a fresh short-deadline context; same expectedLast across attempts
	// (idempotent because the CAS fence prevents double-apply, ADR-0004).
	for range fatMaxAttempts {
		opCtx, cancel := fatNatsCtx(fatSubmitTimeout)
		ack, pubErr := js.Publish(opCtx, subj, outerBytes, //nolint:contextcheck // fresh short per-op deadline ctx on the durable path
			jetstream.WithExpectLastSequencePerSubject(expectedLast))
		cancel()

		if pubErr == nil {
			return ack.Sequence, nil
		}
		if isFatConflict(pubErr) {
			return 0, fmt.Errorf("sdk/fat: SubmitResult inv %s at seq %d: %w", inv, expectedLast, ErrFatConflict)
		}
		if !isFatTransient(pubErr) {
			return 0, fmt.Errorf("sdk/fat: SubmitResult inv %s: non-transient substrate error: %w", inv, pubErr)
		}
		// transient — retry with same expectedLast (idempotent)
	}
	return 0, fmt.Errorf("sdk/fat: SubmitResult inv %s at seq %d after %d attempts: %w",
		inv, expectedLast, fatMaxAttempts, ErrFatTimeout)
}

// ---------------------------------------------------------------------------
// LastJournalSeq — fence value for SubmitResult
// ---------------------------------------------------------------------------

// LastJournalSeq returns the last stream sequence for invocation inv's journal.
// Fat-mode workers call this to obtain the expectedLast fence before SubmitResult.
//
// Returns 0 when no entries exist yet.
// Uses short per-op deadlines via fatNatsCtx (ADR-0007).
func LastJournalSeq(ctx context.Context, js jetstream.JetStream, inv string) (uint64, error) {
	_ = ctx // per-op deadline created inside via fatNatsCtx (ADR-0007)

	opCtx, cancel := fatNatsCtx(fatSubmitTimeout)
	s, err := js.Stream(opCtx, fatJournalStreamName) //nolint:contextcheck // fresh short per-op deadline ctx on the durable path
	cancel()
	if err != nil {
		return 0, fmt.Errorf("sdk/fat: LastJournalSeq inv %s: open stream: %w", inv, err)
	}

	opCtx2, cancel2 := fatNatsCtx(fatSubmitTimeout)
	m, err := s.GetLastMsgForSubject(opCtx2, subjectForInv(inv)) //nolint:contextcheck // fresh short per-op deadline ctx on the durable path
	cancel2()
	if err != nil {
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("sdk/fat: LastJournalSeq inv %s: %w", inv, err)
	}
	return m.Sequence, nil
}

// ---------------------------------------------------------------------------
// NatsCtx — per-op deadline helper (ADR-0007)
// ---------------------------------------------------------------------------

// NatsCtx returns a context with a short per-operation deadline (ADR-0007).
// Fat-mode workers use this to create fresh per-NATS-call deadline contexts.
// d should be short (e.g. 5s) to avoid stalls during NATS leader failover.
func NatsCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return fatNatsCtx(d)
}
