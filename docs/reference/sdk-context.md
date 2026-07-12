# SDK Context Reference (`sdk.Context`)

> **Quadrant:** Reference — exhaustive list of `ctx.*` verbs, handler registration patterns,
> and determinism rules. For _why_ determinism is required, see
> [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md).

`sdk.Context` is the durable execution context injected into every Tenax handler invocation.
It is the only correct API surface for durable side effects, state, timers, promises, and
nondeterministic inputs inside handler bodies.

**Do NOT type-assert `sdk.Context` to any concrete type.** The runtime injects a live
implementation backed by `internal/statemachine`; the concrete type is not part of the
public contract (ADR-0028).

---

## Handler Registration

### `sdk.NewService(name string) *Service`

Creates a stateless durable-service descriptor. Handlers registered on a `Service` share no
per-key state. Multiple handlers can be registered on the same service.

Register a handler with `(*Service).Handler(name string, fn HandlerFunc) (*Service, error)`,
then register the built service in the package-level default registry with
`sdk.Register(svc *Service) error`:

```go
svc, err := sdk.NewService("orders").Handler("charge", chargeHandler)
if err != nil {
    return err
}
if err := sdk.Register(svc); err != nil {
    return err
}
```

### `sdk.NewVirtualObject(name string) *VirtualObject`

Creates a keyed Virtual Object descriptor. Handlers on a `VirtualObject` share per-key KV
state (`ctx.Get`/`ctx.Set`/`ctx.Clear`/`ctx.List`). Only one invocation per key runs at a
time (single-writer guarantee).

Register a handler with `(*VirtualObject).Handler(name string, fn KeyedHandlerFunc)
(*VirtualObject, error)`. There is no package-level `sdk.RegisterVirtualObject` function —
register the built object on the default registry via
`sdk.GlobalRegistry().RegisterVirtualObject(obj) error`:

```go
obj, err := sdk.NewVirtualObject("order").Handler("charge", chargeKeyedHandler)
if err != nil {
    return err
}
if err := sdk.GlobalRegistry().RegisterVirtualObject(obj); err != nil {
    return err
}
```

`KeyedHandlerFunc` is the Virtual Object handler signature — distinct from `HandlerFunc`
below by the extra `key` parameter carrying the object's routing key:

```go
type KeyedHandlerFunc func(ctx sdk.Context, key string, req []byte) ([]byte, error)
```

### `sdk.NewWorkflow(name string) *Workflow`

Creates a workflow descriptor. Workflow handlers support durable compensation stacks
(`ctx.RegisterCompensation`) in addition to all service-level verbs.

Register the run-once handler with `(*Workflow).Run(fn KeyedHandlerFunc) *Workflow`, then
register the workflow in the package-level default registry with
`sdk.RegisterWorkflow(wf *Workflow) error`:

```go
wf := sdk.NewWorkflow("onboarding").Run(runHandler)
if err := sdk.RegisterWorkflow(wf); err != nil {
    return err
}
```

Query handlers are registered separately with `(*Workflow).Query(name string, fn
QueryHandlerFunc) (*Workflow, error)`. `QueryHandlerFunc` receives a restricted, read-only
`QueryContext` exposing only `Get(key string) ([]byte, error)` and `List() ([]string,
error)` — mutation verbs (`Set`/`Run`/`Call`/`Send`/`Sleep`) are not reachable from a query
handler by construction:

```go
type QueryHandlerFunc func(ctx sdk.QueryContext, args json.RawMessage) (json.RawMessage, error)
```

### Handler function signature

```go
func(ctx sdk.Context, req []byte) ([]byte, error)
```

- `ctx` — the durable execution context (this page)
- `req` — raw request body (caller-supplied payload)
- Returns `([]byte, error)` — response payload and terminal error

This is `HandlerFunc` — the signature for `Service` handlers. `VirtualObject` handlers use
`KeyedHandlerFunc` (adds a `key string` parameter) and Workflow query handlers use
`QueryHandlerFunc` (restricted `QueryContext`) — see above.

---

## §1 — Durable Side Effects

### `ctx.Run(name string, fn func(opID string) ([]byte, error)) ([]byte, error)`

Executes `fn` as a durable once-exactly side effect.

- **Live execution:** `fn` is called once; the `opID` (`"<invID>/<entryIndex>"`) is stable
  across replays, enabling downstream deduplication (ADR-0006).
- **Replay:** `fn` is NOT called; the journaled result is returned verbatim (NFR-EO-3).
- The `RunCommand` is journaled BEFORE `fn` is called; the `RunCompletion` is journaled
  BEFORE `ctx.Run` returns to user code (FR-SM-1, ADR-0002).

```go
result, err := ctx.Run("charge-card", func(opID string) ([]byte, error) {
    return chargeCard(opID, req.Amount)
})
```

---

## §2 — Timers

### `ctx.Sleep(d time.Duration) error`

Suspends the invocation for `d`. The timer is journaled durably; the invocation resumes
after the timer fires.

- Invocation enters `SUSPENDED (reason: sleep)` while waiting.
- On replay, returns immediately without re-suspending.

```go
err := ctx.Sleep(24 * time.Hour)
```

### `ctx.Timer(d time.Duration) (Promise, error)`

Returns a raceable `Promise` for a durable timer of duration `d` — usable anywhere a
`Promise` is accepted, including `ctx.Race`/`ctx.AwaitAny`/`ctx.AwaitAll`/
`ctx.AwaitFirstSucceeded`/`ctx.AwaitAllSucceeded`, alongside a `Call`- or
`Awakeable`-backed promise (Story 52.3).

Unlike `ctx.Sleep` (which blocks the invocation unconditionally until the timer fires),
`ctx.Timer` lets a handler express "whichever of a timeout and an RPC/awakeable finishes
first" — the canonical timeout-guard-around-an-RPC pattern.

- Maps to the SAME already-frozen §7 Sleep-class combinator leaf wire shape `ctx.Sleep`
  uses (`SleepCommand`=21/`SleepCompletion`=22) — zero new wire entry type.
- The invocation enters `SUSPENDED (reason: sleep)` while waiting, exactly as `ctx.Sleep`
  does, whether awaited directly (`Await()`) or via a combinator.
- The returned `Promise` never resolves before the durable wake fires and is journaled —
  no fabricated early resolution (ADR-0017), including when `ctx.Timer` races a
  `Call`/`Awakeable` in a combinator tree and a crash-redrive lands with `SleepCommand`
  journaled but `SleepCompletion` absent: the engine distinguishes "the wake genuinely
  fired" from "a sibling leg's completion caused this redrive" via an invoker-injected
  signal threaded from the wake/redrive path (Story 52.4, ADR-0042 — CR-11 remediation
  of a Story 52.3 review finding). A sibling-triggered redrive re-suspends the timer leaf
  instead of fabricating its completion; this is literally true in the raced (multi-leaf)
  case as well as the single-await `ctx.Sleep` case.
- On replay, `Await()` (or the combinator path) returns the recorded result immediately
  without re-suspending.

```go
timerPromise, err := ctx.Timer(5 * time.Second)
if err != nil {
    return nil, err
}
result, err := ctx.Race(timerPromise, callPromise)
if errors.Is(err, sdk.ErrCombinatorFailed) {
    // ...
}
// result == nil, err == nil means the timer fired first (a void-presence-only
// combinator leaf, the same shape ctx.Sleep's single-await path already produces).
```

---

## §3 — Virtual Object State

These verbs are **only valid inside Virtual Object handlers**. Calling them from a Service
or Workflow handler returns `ErrStateNotKeyed` at runtime.

### `ctx.Get(key string) ([]byte, bool, error)`

Returns the value for the given state key. On first (live) call reads from the per-VO KV
bucket and journals as `GetState`. On replay returns the journaled value.
Returns `(nil, false, nil)` when absent.

### `ctx.Set(key string, value []byte) error`

Stores a JCS-encoded value for the given state key. Journals as `SetState`. Immediately
visible to subsequent `Get` calls within the same invocation.

### `ctx.Clear(key string) error`

Removes the state entry for the given key. Journals as `ClearState`. No-op if key absent.

### `ctx.List() ([]string, error)`

Returns all state keys for this Virtual Object instance, sorted by UTF-16 code units.
Journals as `GetStateKeys`.

---

## §4 — Synchronous Calls

### `ctx.Call(service, handler string, req []byte) ([]byte, error)`

Invokes a target service handler and awaits its result (request-response).

- Journals a `CallCommand`; the invocation enters `SUSPENDED (reason: call)`.
- Returns the callee result on resume.
- Creates a parent-child call-tree edge; cancel propagates to the child invocation.

```go
resp, err := ctx.Call("inventory", "reserve", payload)
```

> **No `tenax invocation call` CLI verb exists.** To submit an invocation externally, publish
> to the NATS Micro subject `tenax.ingress.call`. See
> [Admin API reference](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/admin-api.md) for the ingress subjects.

### `ctx.CallWorkflow(name, key string, req []byte) ([]byte, error)`

Starts (or attaches to) the keyed Workflow `(name, key)` and awaits its result — the Workflow
counterpart of `ctx.Call`. Dispatch is **run-once-per-key attach**: a second `CallWorkflow` to the
same `(name, key)` attaches to the single run-once instance rather than starting a second run.

- Journals a keyed `CallCommand`; the invocation enters `SUSPENDED (reason: call)`.
- An awaited `CallWorkflow` on a `COMPLETED` key returns the **recorded** result; on a terminal
  `FAILED` / `KILLED` / `CANCELLED` key it surfaces the **recorded** terminal error (never a
  fabricated status). Multiple distinct in-flight callers awaiting the same key all resume.
- Creates the same parent-child call-tree edge as `ctx.Call`; cancel/kill propagates to the keyed
  child. The `key` is a routing value, not a nondeterminism source (ADR-0011).

```go
result, err := ctx.CallWorkflow("cortex-interpreter", childKey, req)
```

See [Dispatch a keyed Workflow](../how-to/dispatch-a-keyed-workflow.md) for the task guide.

---

## §5 — One-Way Fire-and-Forget

### `ctx.Send(service, handler string, req []byte) (string, error)`

Dispatches a one-way message to a target handler. The caller does NOT suspend.
Returns the callee invocation ID on success.

The callee is dispatched as its own durable invocation with an idempotency key derived
from the caller's `(inv,index)` opId — duplicate dispatches are deduplicated (ADR-0006).

### `ctx.SendDelayed(service, handler string, req []byte, delay time.Duration) (string, error)`

Schedules a message for dispatch after `delay`. Returns the handle for the scheduled
invocation. The caller does NOT suspend.

`InvokeTime` is computed using `ctx.Now()` inside the state machine (journaled for deterministic
replay — ADR-0011). **Never use `time.Now()` to compute `InvokeTime` directly.**

### `ctx.SendAt(service, handler string, req []byte, invokeAt time.Time) (string, error)`

Schedules a message at the absolute UTC time `invokeAt`. Equivalent to
`SendDelayed(..., time.Until(invokeAt))` but expressed in absolute terms.

### `ctx.SendWorkflow(name, key string, req []byte) (string, error)`

Starts (or attaches to) the keyed Workflow `(name, key)` fire-and-forget — the Workflow counterpart
of `ctx.Send`. The caller does NOT suspend; returns the child invocation ID. Dispatch is
**run-once-per-key attach**; `SendWorkflow` to an already-terminal key is a **no-op** that returns the
existing invocation ID.

See [Dispatch a keyed Workflow](../how-to/dispatch-a-keyed-workflow.md) for the task guide.

---

## §6 — Awakeables (Durable Promises)

### `ctx.Awakeable() (id string, promise Promise, err error)`

Creates a durable promise that can be resolved externally. Returns the awakeable `id` and a
`Promise` handle.

The returned `id` carries the `akb_` prefix (ADR-0037): e.g. `akb_3q2-7wAAAAc`. Share this
ID with the external system; the external system resolves the awakeable via
`ctx.CompleteAwakeable` or `ctx.RejectAwakeable` (below) or via the ingress API.

```go
id, p, err := ctx.Awakeable()
// id is "akb_"-prefixed, e.g. "akb_3q2-7wAAAAc"
// share id with external system
result, err := p.Await()  // suspends SUSPENDED (reason: awakeable)
```

### `ctx.Promise(id string) Promise`

Returns a durable handle to an existing awakeable by its `akb_`-prefixed `id`. Calling
`Await()` on the returned handle suspends until the awakeable is resolved or rejected.

### `ctx.CompleteAwakeable(id string, value []byte) error`

Resolves another invocation's awakeable with the given value. The resolution is durable and
replay-safe: the `CompleteAwakeable` entry is journaled BEFORE the external effect executes
(ADR-0002, ADR-0011).

Returns `ErrDuplicateCompletion` when the awakeable is already resolved (first-writer-wins,
ADR-0004).

### `ctx.RejectAwakeable(id string, reason string) error`

Rejects another invocation's awakeable with the given reason.
Returns `ErrDuplicateCompletion` when already resolved (ADR-0004).

---

## §7 — Promise Combinators (E19)

The Promise-Combinator Engine (§7 of the state-machine contract) provides race/any/all
semantics over sets of `Promise` handles. All combinators are single-threaded — no goroutine
or `select` is used; the state machine manages suspension.

A `Promise` returned by `ctx.Timer` (§2, Story 52.3) is an ordinary combinator child —
usable alongside `Awakeable`- or `Call`-backed promises with no special-casing. The
combinators extract each promise's completion id polymorphically; they never learn
whether a given leaf came from `ctx.Timer`, `ctx.Awakeable`, or `ctx.Call`. No new error
code and no new `CombinatorType` were introduced to support this.

Error codes in this section come from the closed §11 numeric registry (ADR-0009):

- **572** (`AWAITING_TWO_ASYNC_RESULTS`) — a second independent await tree was opened at the
  same logical await point while an unresolved tree was already active.
- **573** (`UNSUPPORTED_FEATURE`) — an empty `Race`/`AwaitAny` (first-of-nothing has
  undefined semantics, §7.2) or a combinator kind outside the valid range.

Both codes are returned wrapped as `*sdk.CombinatorError`. Test with
`errors.Is(err, sdk.ErrCombinatorFailed)`.

### `ctx.Race(promises ...Promise) ([]byte, error)`

Awaits the first-completed promise. Maps to §7.2 `CombinatorType FIRST_COMPLETED (1)`.

- The first promise to complete (success or failure) wins; the rest are cancelled.
- Returns error code **573** if `promises` is empty.
- Returns error code **572** if a concurrent await tree collision is detected.

```go
result, err := ctx.Race(p1, p2, p3)
if errors.Is(err, sdk.ErrCombinatorFailed) {
    ce := &sdk.CombinatorError{}
    if errors.As(err, &ce) && ce.Code == 573 {
        // empty race
    }
}
```

### `ctx.AwaitAny(promises ...Promise) ([]byte, error)`

Alias for `ctx.Race`. Awaits the first-completed promise.
Maps to §7.2 `CombinatorType FIRST_COMPLETED (1)`.

### `ctx.AwaitAll(promises ...Promise) ([]byte, error)`

Awaits completion of all promises (FAILED children still count as completed).
Maps to §7.2 `CombinatorType ALL_COMPLETED (2)`.

- Empty `promises` slice resolves immediately with success (all-of-nothing identity, §7.2).
- Returns error code **572** on await-tree collision.

```go
results, err := ctx.AwaitAll(p1, p2, p3)
```

### `ctx.AwaitFirstSucceeded(promises ...Promise) ([]byte, error)`

Awaits the first SUCCEEDED promise; resolves FAILED only if all fail.
Maps to §7.2 `CombinatorType FIRST_SUCCEEDED_OR_ALL_FAILED (3)`.

### `ctx.AwaitAllSucceeded(promises ...Promise) ([]byte, error)`

Awaits all promises to SUCCEED; resolves FAILED on the first FAILED child.
Maps to §7.2 `CombinatorType ALL_SUCCEEDED_OR_FIRST_FAILED (4)`.

**camelCase JSON representation (ADR-0032):**

```json
{
  "invId": "inv_abc",
  "commandIndex": 5,
  "combinatorType": 1,
  "state": "COMPLETED"
}
```

---

## §8 — Sagas (Durable Compensation)

### `ctx.RegisterCompensation(fn func(ctx Context) error) (string, error)`

Registers a compensation function for the current invocation. On graceful cancel
(`CANCELLING → CANCELLED`) or terminal handler failure, all registered compensations run
in **reverse registration order (LIFO)** as durable journaled effects.

- Returns a `"cmp_"`-prefixed UUID assigned via `ctx.UUID()` (journaled for replay, ADR-0011).
- **cancel = graceful + compensating** (ADR-0017): saga runs on `CANCELLING → CANCELLED`.
- **kill = forceful + non-compensating** (ADR-0017): saga does NOT run on `kill`.
- Recursive registration from inside a compensation function returns a terminal
  `PROTOCOL_VIOLATION` error.

```go
compID, err := ctx.RegisterCompensation(func(ctx sdk.Context) error {
    return refundCard(ctx, chargeID)
})
```

---

## §8.5 — Feature Pinning (`ctx.GetVersion`)

### `ctx.GetVersion(changeID string, minVersion, maxVersion int) (int, error)`

Resolves the feature/patch version pinned to `changeID` for this invocation (state-machine
contract §6.9). Feature pinning is the deterministic-replay complement to version _routing_:
routing chooses a version for a **new** invocation, while `ctx.GetVersion` guarantees an
**existing** invocation stays on its chosen version for its entire lifetime, even as newer
versions are activated underneath it.

- **First (live) call:** journals a `GetVersion` Command entry (EntryType 40) with
  `Version = maxVersion` **before** returning (durable-path invariant #4,
  journal-before-return), then returns `maxVersion`.
- **Replay:** returns the recorded version from the journal without re-resolution — the
  version is never re-derived from host state.
- **`DEFAULT_VERSION` (`0`) grace path:** if no `GetVersion` entry exists at the replayed
  cursor — i.e. a `ctx.GetVersion` call site was added to the code _after_ an in-flight
  invocation's journal was already written — the call returns `DEFAULT_VERSION` (`0`)
  **without advancing the cursor**, leaving that entry unconsumed for the next real op in
  the invocation's (older) execution path to match against. The call is treated as
  unpatched forever for that invocation (state-machine contract §13.6, §14.2: "adding a
  patch is safe for in-flight invocations").
- **Out-of-window / changeID mismatch:** if the recorded version falls outside
  `[minVersion, maxVersion]`, or the recorded changeID does not match the one passed in,
  the call returns an error wrapping a RETRYABLE journal-mismatch condition — test with
  `errors.Is(err, statemachine.ErrJournalMismatch)`. This is never a terminal error.

```go
v, err := ctx.GetVersion("add-discount-field", 1, 2)
if err != nil {
    return nil, err
}
if v >= 2 {
    // new behavior — discount field present
} else {
    // old behavior — invocations already pinned to v1 keep running it safely
}
```

See [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) for why resolving
nondeterministic inputs once and journaling them is required. Governed by **ADR-0040**
(`development/governance/adrs/adr-0040_version-routing-canary-and-contract-rebaseline.md`),
the design of record for EntryType 40's semantics and the authorized contract re-baseline
that shipped it, and **FEAT-4-5**
(`development/functionality/features/feat-4-5_per-invocation-feature-pinning-getversion.md`),
the feature record for per-invocation feature pinning.

---

## §9 — Nondeterminism via `ctx` (FORBIDDEN patterns)

**On the replay path, the following are FORBIDDEN in handler bodies:**

| Forbidden call                             | Replay hazard                                                    | Use instead             |
| ------------------------------------------ | ---------------------------------------------------------------- | ----------------------- |
| `time.Now()`                               | Returns a different time on every replay — diverges branch logic | `ctx.Now()`             |
| `math/rand.Float64()`, `rand.Intn()`, etc. | Different random values on every replay                          | `ctx.Rand()`            |
| `github.com/google/uuid` (any UUID gen)    | Different UUID on every replay                                   | `ctx.UUID()`            |
| `github.com/oklog/ulid`                    | Same as UUID — non-deterministic                                 | `ctx.UUID()`            |
| Goroutines inside handler body             | Concurrency outside the replay model — not journaled             | `ctx.Call` / `ctx.Send` |

See [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) for the full
explanation of why these calls diverge and how the journal-and-replay mechanism prevents
nondeterminism violations.

### `ctx.Now() time.Time`

Returns the current wall-clock time, journaled deterministically.

On first (live) execution, the state machine captures `time.Now()` inside the engine,
journals it as a `NowCommand`/`NowCompletion` pair, and returns it. On replay, the recorded
time is returned — the system clock is never consulted again for that entry index.

```go
// CORRECT — deterministic:
t := ctx.Now()

// WRONG — replay hazard:
// t := time.Now()  ← FORBIDDEN in handler bodies (ADR-0011)
```

### `ctx.Rand() float64`

Returns a pseudo-random float64 in [0,1) from a ChaCha20 PRNG seeded by the invocation ID,
journaled deterministically. On replay, the recorded float64 is returned.

### `ctx.UUID() string`

Returns an RFC 4122 v4 UUID string from the ChaCha20 PRNG, journaled deterministically.
On replay, the recorded UUID is returned.

---

## §10 — Cancel Awareness

### `sdk.ErrCancelled`

Error returned by `ctx` operations when the invocation has received a graceful cancel signal
(`CANCELLING` status). Handlers should check for this at safe points:

```go
if errors.Is(err, sdk.ErrCancelled) {
    // graceful cancel — saga compensation will run
    return sdk.ErrCancelled
}
```

### `sdk.ErrKilled`

Error returned when the invocation has been forcefully killed (`KILLED` status). No saga
compensation runs.

### `sdk.CancelAware` interface

Optional interface that `sdk.Context` implementations may provide:

```go
if ca, ok := ctx.(sdk.CancelAware); ok && ca.Cancelled() {
    return sdk.ErrCancelled
}
```

---

## §11 — SUSPENDED State and Reason Requirement

`SUSPENDED` is never a bare status word. It always carries a reason from the closed set:

| Reason      | Triggering verb                             |
| ----------- | ------------------------------------------- |
| `sleep`     | `ctx.Sleep`                                 |
| `awakeable` | `ctx.Awakeable()` / `ctx.Promise().Await()` |
| `call`      | `ctx.Call`                                  |

See [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md) for the full closed status word set.

**JSON representation (camelCase, ADR-0032):**

```json
{"invId": "inv_abc", "state": "SUSPENDED", "suspendReason": "sleep"}
{"invId": "inv_abc", "state": "SUSPENDED", "suspendReason": "awakeable"}
{"invId": "inv_abc", "state": "SUSPENDED", "suspendReason": "call"}
```

---

## §12 — Closed Status Lexicon (ADR-0017)

All status values returned by `ctx.*` operations and observable in invocation state are
drawn from the **closed lexicon** (ADR-0017). This is the authoritative, byte-identical set
across SDK, CLI, admin API, logs, and metrics:

| Status       | Meaning                                               |
| ------------ | ----------------------------------------------------- |
| `PENDING`    | Queued, not yet started                               |
| `RUNNING`    | Actively executing                                    |
| `SUSPENDED`  | Awaiting an external event (sleep / awakeable / call) |
| `COMPLETED`  | Finished successfully                                 |
| `FAILED`     | Finished with a terminal error                        |
| `CANCELLING` | Graceful cancel requested; compensation stack running |
| `CANCELLED`  | Graceful cancel complete                              |
| `KILLED`     | Forcefully terminated; no compensation                |

No surface may invent synonyms or alternate spellings. `SUSPENDED` is always paired with a
reason from the closed set (`sleep`, `awakeable`, `call`) — see §11 above.

See [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md) for the full reference.

---

## §13 — Fat-Worker Minimal SDK Shim (ADR-0036 D1)

In the **fat co-located engine** deployment mode (ADR-0036), the worker binary is linked
with the Tenax engine packages directly instead of dispatching over a NATS Micro round-trip.
The fat adapter exposes a **minimal, purpose-scoped SDK shim** — not a raw re-export of
the engine internals.

What is accessible in fat mode (promoted surface, ADR-0036 D1):

- The `HasTerminal()` idempotency guard — checked before every fat-mode dispatch to prevent
  re-execution of already-completed invocations under crash-before-ack redelivery.
- A submit-result primitive that journals the terminal output via CAS-only append
  (`Nats-Expected-Last-Subject-Sequence`, ADR-0004).

What is **NOT** exposed in fat mode:

- Raw `internal/journal` append API.
- Raw `internal/kvstate` KV write APIs.
- Raw `internal/lease` acquire/renew APIs.
- Raw `internal/idempotency` dedup APIs.
- Any `internal/statemachine` type not part of the D1 shim.

Go's `internal/` package rule remains the enforcement boundary. All `ctx.*` verbs described
on this page work identically in thin-executor mode and fat mode — the shim presents the same
SDK surface. The only difference is the dispatch path (direct function call vs NATS Micro
request).

For the full trust model — credential scoping (D2), version-pinning startup check (D3, exit
code 4 on violation), and the `HasTerminal()` guard (D4) — see
[Multi-tenancy and trust model](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/multi-tenancy-and-trust-model.md).

---

## See Also

- [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) — why handler code must be deterministic
- [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md) — the 8 closed-set invocation status words
- [Admin API reference](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/admin-api.md) — NATS Micro ingress subjects for submitting invocations
- [Multi-tenancy and trust model](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/multi-tenancy-and-trust-model.md) — fat-worker trust model, credential scoping, version pinning
- [Your first durable handler](../tutorials/first-durable-handler.md) — tutorial walkthrough
- [Back to docs index](https://github.com/exoar/axon_tenax_engine/blob/main/docs/index.md)
