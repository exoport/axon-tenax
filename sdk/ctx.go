package sdk

import "time"

// Context is the durable execution context passed to every handler invocation.
// The runtime injects a live implementation backed by internal/statemachine.
// Handler authors MUST NOT cast this interface to any concrete type.
//
// All nondeterministic operations (time, random, UUID) must be obtained via
// ctx.Now, ctx.Rand, ctx.UUID respectively — never call time.Now(), math/rand,
// or uuid libraries directly in handler bodies (ADR-0011).
type Context interface {
	// Run executes the given function as a durable side effect, exactly once live.
	// fn receives opID of the form "<invID>/<entryIndex>" where entryIndex is the
	// RunCommand's 0-based journal position. The opID is stable across crash-and-replay,
	// enabling downstream APIs to dedup via the same opID (ADR-0006).
	// On replay, fn is NOT called; the journaled result is returned verbatim (NFR-EO-3).
	// RunCommand is journaled BEFORE fn is called; RunCompletion is journaled BEFORE
	// ctx.Run returns to user code (FR-SM-1, ADR-0002).
	Run(name string, fn func(opID string) ([]byte, error)) ([]byte, error)

	// Sleep suspends the invocation for d. The timer is journaled durably;
	// the invocation resumes after the timer fires on a subsequent attempt.
	Sleep(d time.Duration) error

	// Timer returns a raceable Promise for a durable timer of duration d — usable
	// anywhere a Promise is accepted, including ctx.Race/AwaitAny/AwaitAll/
	// AwaitFirstSucceeded/AwaitAllSucceeded, alongside a Call- or Awakeable-backed
	// promise. Unlike Sleep (which blocks the invocation unconditionally), Timer
	// lets a handler express "whichever of a timeout and an RPC/awakeable finishes
	// first" (e.g. a timeout guard around ctx.Call).
	//
	// Timer maps to the SAME already-frozen §7 Sleep-class combinator leaf wire
	// shape ctx.Sleep uses (SleepCommand=21/SleepCompletion=22) — zero new
	// wire.EntryType, zero existing byte-shape change. The timer is journaled
	// durably exactly as ctx.Sleep's is; the returned Promise never resolves
	// (via Await() or a combinator) before the durable wake fires and is
	// journaled — no fabricated early resolution (ADR-0017), including when
	// Timer races a Call/Awakeable and a crash-redrive lands with SleepCommand
	// journaled but SleepCompletion absent: internal/statemachine's PrepareSleep
	// distinguishes "the wake genuinely fired" from "a sibling leg's completion
	// caused this redrive" via an invoker-injected pendingSleepFired signal
	// threaded from the wake/redrive path (Story 52.4, ADR-0042 — CR-11
	// remediation of a Story 52.3 review finding). A sibling-triggered redrive
	// re-suspends the timer leaf instead of fabricating its completion; this
	// guarantee is literally true in the raced (multi-leaf) case as well as the
	// single-await ctx.Sleep case.
	//
	// (Story 52.3/52.4, FEAT-7-6, §7.8.2/§7.8.3/§7.8.6, ADR-0009, ADR-0011, ADR-0017, ADR-0028, ADR-0042, PATLOC-0001)
	Timer(d time.Duration) (Promise, error)

	// Get returns the value for the given state key. On first (live) call reads
	// from per-VO KV, journals as GetState. On replay returns the journaled value.
	// Returns (zero, false, nil) when absent.
	//
	// Only valid for Virtual Object (keyed) handlers.
	// Calling from a Service handler returns ErrStateNotKeyed at runtime —
	// state operations require a Virtual Object handler (ADR-0011).
	Get(key string) ([]byte, bool, error)

	// Set stores a JCS-encoded value for the given state key, journals as SetState.
	// Immediately visible to subsequent Get within this invocation.
	//
	// Only valid for Virtual Object (keyed) handlers.
	// Calling from a Service handler returns ErrStateNotKeyed at runtime —
	// state operations require a Virtual Object handler (ADR-0011).
	Set(key string, value []byte) error

	// Clear removes the state entry for the given state key. Journals as ClearState.
	// No-op if key absent.
	//
	// Only valid for Virtual Object (keyed) handlers.
	// Calling from a Service handler returns ErrStateNotKeyed at runtime —
	// state operations require a Virtual Object handler (ADR-0011).
	Clear(key string) error

	// List returns all state keys for this Virtual Object instance, sorted by
	// UTF-16 code units. Journals as GetStateKeys.
	//
	// Only valid for Virtual Object (keyed) handlers.
	// Calling from a Service handler returns ErrStateNotKeyed at runtime —
	// state operations require a Virtual Object handler (ADR-0011).
	List() ([]string, error)

	// Call invokes target service handler and awaits its result (request-response).
	// Journals CallCommand, emits Suspend, and returns the callee result on resume (Story 5.2).
	Call(service, handler string, req []byte) ([]byte, error)

	// Send sends a one-way fire-and-forget message to a target handler.
	// Returns the callee invocationId and nil on success. The caller does NOT suspend —
	// execution continues immediately after Send returns (unlike ctx.Call).
	// The callee is dispatched as its own durable invocation with an idempotency key
	// derived from the caller's (inv,index) opId so duplicate dispatches are deduplicated.
	// (Story 5.3, ADR-0006, ADR-0025)
	Send(service, handler string, req []byte) (string, error)

	// SendDelayed schedules a one-way fire-and-forget message to be dispatched at or
	// after ctx.Now() + delay. Returns the scheduled invocation's eventual handle and
	// nil on success. The caller does NOT suspend — execution continues immediately
	// after SendDelayed returns.
	//
	// InvokeTime = ctx.Now().UnixNano() + delay.Nanoseconds() is computed inside the
	// state machine using ctx.Now() (journaled for deterministic replay, ADR-0011). NEVER use
	// time.Now() directly — replay would compute a different InvokeTime.
	//
	// The callee is dispatched via the durable timer service (tenax.wakes) with
	// idempotency key "<callerInv>/<sendCommandEntryIndex>" (ADR-0006, ADR-0015).
	// Exactly-once delivery survives process relocation and timer failover.
	//
	// delay == 0 is valid (equivalent to Send — immediate dispatch, InvokeTime = 0
	// but routed through the delayed-send path). Use Send for clarity on the immediate
	// path, though the outcome is the same.
	// (Story 5.4, FR-TIMER-1, ADR-0011, ADR-0025)
	SendDelayed(service, handler string, req []byte, delay time.Duration) (string, error)

	// SendAt schedules a one-way fire-and-forget message to be dispatched at or after
	// the absolute UTC time invokeAt. Returns the scheduled invocation's eventual handle
	// and nil on success. The caller does NOT suspend.
	//
	// Equivalent to SendDelayed(service, handler, req, time.Until(invokeAt)), but
	// allows callers to express scheduling in absolute terms. invokeAt.UnixNano() is
	// used as InvokeTime — must be a future time (> 0, > ctx.Now()).
	// (Story 5.4, FR-TIMER-1, ADR-0025)
	SendAt(service, handler string, req []byte, invokeAt time.Time) (string, error)

	// Awakeable creates a durable promise that can be resolved externally.
	// Returns the awakeable ID and a Promise handle to await.
	Awakeable() (id string, promise Promise, err error)

	// Promise returns a durable handle to an external awakeable by id.
	// Calling Await() on the returned handle suspends the invocation until the
	// awakeable is resolved (via ctx.CompleteAwakeable or the ingress path) and
	// returns the resolved value or rejection error. On replay, Await() returns
	// the recorded value without re-suspending.
	//
	// The suspension error propagates only through Await() ([]byte, error) —
	// Promise() itself never returns an error (ADR-0025: interface unchanged).
	//
	// (Story 18.2, FR-AWAIT-1, FR-AWAIT-2, ADR-0025)
	Promise(id string) Promise

	// CompleteAwakeable resolves another invocation's awakeable with the given value.
	// The resolution is durable and replay-safe: the CompleteAwakeable entry (#34) is
	// journaled BEFORE the external effect executes; on replay the recorded result is
	// returned without re-invoking the resolver (ADR-0002, ADR-0011).
	//
	// Returns ErrDuplicateCompletion when the awakeable is already resolved/rejected
	// (first-writer-wins, N-81, ADR-0004).
	//
	// (Story 18.1, FR-AWAIT-1, ADR-0006, ADR-0025)
	CompleteAwakeable(id string, value []byte) error

	// RejectAwakeable rejects another invocation's awakeable with the given reason.
	// The rejection is durable and replay-safe (ADR-0002, ADR-0011).
	// Returns ErrDuplicateCompletion when the awakeable is already resolved/rejected (ADR-0004).
	//
	// (Story 18.1, FR-AWAIT-2, ADR-0025)
	RejectAwakeable(id string, reason string) error

	// Now returns the current wall-clock time, journaled deterministically.
	// On first (live) execution the state machine captures time.Now() inside the engine,
	// journals it, and returns it. On replay, the recorded time is returned.
	// Do not call time.Now() directly in handler bodies — use ctx.Now().
	Now() time.Time

	// Rand returns a pseudo-random float64 in [0,1) from a ChaCha20 PRNG seeded
	// by the invocation id, journaled deterministically. On replay, the recorded
	// float64 is returned. Do not call math/rand directly in handler bodies —
	// use ctx.Rand().
	Rand() float64

	// UUID returns a RFC 4122 v4 UUID string from the ChaCha20 PRNG, journaled
	// deterministically. On replay, the recorded UUID is returned. Do not call
	// uuid generators directly in handler bodies — use ctx.UUID().
	UUID() string

	// GetVersion resolves the feature/patch version for changeID (state-machine contract §6.9).
	// On first (live) call, journals the entry and returns maxVersion. On replay, returns the
	// recorded version without re-resolution. Returns DEFAULT_VERSION (0) when no GetVersion
	// entry exists at the replayed cursor (patch added after this invocation's journal was
	// written — the call is treated as unpatched forever for this invocation, per N-95).
	// Returns an error wrapping a journal-mismatch condition (RETRYABLE, never terminal) if the
	// recorded version falls outside [minVersion, maxVersion] (N-97).
	GetVersion(changeID string, minVersion, maxVersion int) (int, error)

	// RegisterCompensation registers a compensation function for the current invocation.
	// On cancel (graceful) or terminal handler failure, all registered compensations run
	// in REVERSE registration order (LIFO) as durable journaled effects (FR-SAGA-1, FR-SAGA-2).
	//
	// The returned compId is a "cmp_"-prefixed UUID assigned by the state machine via ctx.UUID()
	// during live execution (ADR-0011: journaled for deterministic replay). On replay,
	// the recorded compId is served from the journal; the fn is re-registered without
	// a new journal write.
	//
	// Recursive registration (calling RegisterCompensation from inside a compensation fn)
	// is forbidden and returns a terminal PROTOCOL_VIOLATION error.
	//
	// cancel = graceful + compensating (ADR-0017): saga runs on CANCELLING → CANCELLED.
	// kill = forceful + non-compensating (ADR-0017): saga does NOT run on kill.
	//
	// (Story 5.6, FR-SAGA-1, ADR-0011, ADR-0025)
	RegisterCompensation(fn func(ctx Context) error) (string, error)

	// Race awaits the first-completed promise out of the given set.
	// Maps to §7.2 CombinatorType FIRST_COMPLETED (1). Single-threaded — no goroutine or select.
	// Returns TERMINAL 572 (wrapped as *CombinatorError) if a second independent await tree
	// is opened at the same logical await point.
	// Returns TERMINAL 573 (wrapped as *CombinatorError) if promises is empty
	// (first-of-nothing has undefined semantics, §7.2).
	//
	// (Story 19.7, §7.2 CombinatorFirstCompleted=1, ADR-0009, ADR-0028, PATLOC-0001)
	Race(promises ...Promise) ([]byte, error)

	// AwaitAny is an alias for Race — awaits the first-completed promise.
	// Maps to §7.2 CombinatorType FIRST_COMPLETED (1). Single-threaded — no goroutine or select.
	//
	// (Story 19.7, §7.2 CombinatorFirstCompleted=1, ADR-0009, ADR-0028, PATLOC-0001)
	AwaitAny(promises ...Promise) ([]byte, error)

	// AwaitAll awaits completion of all promises (FAILED children still count as completed).
	// Maps to §7.2 CombinatorType ALL_COMPLETED (2). Single-threaded — no goroutine or select.
	// Empty promises slice resolves SUCCEEDED immediately (all-of-nothing identity per §7.2).
	//
	// (Story 19.7, §7.2 CombinatorAllCompleted=2, ADR-0009, ADR-0028, PATLOC-0001)
	AwaitAll(promises ...Promise) ([]byte, error)

	// AwaitFirstSucceeded awaits the first SUCCEEDED promise; resolves FAILED only if all fail.
	// Maps to §7.2 CombinatorType FIRST_SUCCEEDED_OR_ALL_FAILED (3). Single-threaded.
	// Empty promises slice resolves SUCCEEDED immediately (all-of-nothing identity per §7.2).
	//
	// (Story 19.7, §7.2 CombinatorFirstSucceededOrAllFailed=3, ADR-0009, ADR-0028, PATLOC-0001)
	AwaitFirstSucceeded(promises ...Promise) ([]byte, error)

	// AwaitAllSucceeded awaits all promises to SUCCEED; resolves FAILED on the first FAILED child.
	// Maps to §7.2 CombinatorType ALL_SUCCEEDED_OR_FIRST_FAILED (4). Single-threaded.
	// Empty promises slice resolves SUCCEEDED immediately (all-of-nothing identity per §7.2).
	//
	// (Story 19.7, §7.2 CombinatorAllSucceededOrFirstFailed=4, ADR-0009, ADR-0028, PATLOC-0001)
	AwaitAllSucceeded(promises ...Promise) ([]byte, error)
}

// Promise is a durable handle to an awaitable result.
// Obtained via ctx.Awakeable() or ctx.Promise(id).
type Promise interface {
	// Await blocks until the promise is resolved and returns the result.
	Await() ([]byte, error)
}

// ---------------------------------------------------------------------------
// Cancel-awareness (Story 5.5, ADR-0025, ADR-0028)
// ---------------------------------------------------------------------------

// ErrCancelled is the error returned by ctx operations when the invocation
// has received a graceful cancel signal (status=CANCELLING).
// Handlers that want to react to cancellation check for this error.
//
// cancel = graceful + compensating (ADR-0017): saga compensation (Story 5.6)
// will run after the invocation observes CANCELLING.
//
// MUST NOT be confused with ErrKilled (forceful kill — no saga).
var ErrCancelled = cancelledError{}

// ErrKilled is the error returned when the invocation has been forcefully killed.
// kill = forceful + non-compensating (ADR-0017): no saga, direct KILLED state.
//
// MUST NOT be confused with ErrCancelled (graceful cancel — runs saga).
var ErrKilled = killedError{}

// cancelledError is the error type for graceful cancellation (ADR-0017).
type cancelledError struct{}

func (cancelledError) Error() string { return "invocation cancelled (CANCELLING)" }
func (cancelledError) Is(target error) bool {
	_, ok := target.(cancelledError)
	return ok
}

// killedError is the error type for forceful kill (ADR-0017).
type killedError struct{}

func (killedError) Error() string { return "invocation killed (KILLED)" }
func (killedError) Is(target error) bool {
	_, ok := target.(killedError)
	return ok
}

// CancelAware is an optional interface that SDK Context implementations MAY
// implement to expose cancel-awareness to handler code.
//
// A Context that implements CancelAware allows handlers to check whether a
// graceful cancel or forceful kill has been signalled. SDK implementations
// MUST NOT import any internal/ package to implement this interface (ADR-0028).
//
// Usage in handlers:
//
//	if ca, ok := ctx.(sdk.CancelAware); ok && ca.Cancelled() {
//	    // graceful cancel: clean up, return ErrCancelled
//	    return sdk.ErrCancelled
//	}
type CancelAware interface {
	// Cancelled returns true when a graceful cancel signal (CANCELLING) has
	// been received by this invocation. Handler code should check Cancelled()
	// at safe points and return ErrCancelled to allow saga compensation to run.
	// cancel = graceful + compensating (ADR-0017).
	Cancelled() bool

	// Killed returns true when a forceful kill (KILLED) has been applied to
	// this invocation. Handler code should check Killed() at safe points.
	// kill = forceful + non-compensating; no saga will run (ADR-0017).
	Killed() bool
}
