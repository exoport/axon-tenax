# How to Dispatch a Keyed Workflow

> **Quadrant:** How-to — goal-oriented guide for starting and awaiting a **keyed child Workflow**
> from inside a handler using `ctx.CallWorkflow` / `ctx.SendWorkflow`. For the exhaustive verb
> definitions and return-contract, see
> [SDK context reference §4 (`ctx.CallWorkflow`)](../reference/sdk-context.md#4--synchronous-calls)
> and [§5 (`ctx.SendWorkflow`)](../reference/sdk-context.md#5--one-way-fire-and-forget). For
> authoring the callee Workflow itself, see [Author a Workflow](./author-a-workflow.md) — this page
> does not duplicate that content.

`ctx.Call`/`ctx.Send` target a `(service, handler)` pair and cannot reach a keyed
[Workflow](./author-a-workflow.md), whose only concurrency is child runs on **distinct keys**. The
two keyed-dispatch verbs let handler code start and await a child Workflow on a specific key:

```go
// Synchronous — journals a keyed Call, suspends, returns the child's result on resume.
ctx.CallWorkflow(name, key string, req []byte) ([]byte, error)

// Fire-and-forget — detached start, returns the child's invocation id immediately.
ctx.SendWorkflow(name, key string, req []byte) (string, error)
```

They mirror `ctx.Call`/`ctx.Send` in shape, but carry a **Workflow key** instead of a handler name —
the same key model as a Virtual Object.

---

## Step 1: Start-or-attach with `CallWorkflow`

`CallWorkflow` starts the keyed Workflow `(name, key)` and awaits its result. Dispatch is
**run-once-per-key attach**: a second `CallWorkflow` to the same `(name, key)` attaches to the single
run-once instance — it does **not** start a second run. This makes crash-redrive of a parent
idempotent: re-dispatching the same derived child key attaches instead of double-spawning.

```go
func (h *Interpreter) Run(ctx sdk.Context, runKey string, req []byte) ([]byte, error) {
	childKey := deriveChildKey(runKey, req) // your own deterministic key derivation

	// Starts the child Workflow if new; attaches if it's already running for childKey.
	result, err := ctx.CallWorkflow("cortex-interpreter", childKey, req)
	if err != nil {
		return nil, err // callee's recorded terminal error is surfaced here — see Step 3
	}
	return result, nil
}
```

Derive **distinct** child keys for distinct child runs; identical keys deliberately converge on one
instance.

## Step 2: Fan out with `SendWorkflow`

To start N child Workflows without blocking on each, use `SendWorkflow` — it returns the child's
invocation id immediately and does not suspend the caller. Await the results later via awakeables or
a follow-up `CallWorkflow` on the same key (which attaches and returns the recorded result once the
child is terminal).

```go
for _, childKey := range childKeys { // distinct keys → distinct child runs
	invID, err := ctx.SendWorkflow("cortex-worker", childKey, payloadFor(childKey))
	if err != nil {
		return nil, err
	}
	track(childKey, invID)
}
```

## Step 3: The frozen return contract

Both verbs carry a fixed, byte-stable return contract:

- **Run-once-per-key attach** — a second dispatch to the same `(name, key)` attaches to the one
  instance; it never starts a second run.
- **Awaited `CallWorkflow` on a terminal key returns the recorded outcome** — a `COMPLETED` key
  returns the **recorded** result; a `FAILED` / `KILLED` / `CANCELLED` key surfaces the **recorded**
  terminal error (never a fabricated status). This holds whether the key was already terminal at
  dispatch time **or** goes terminal while you await it.
- **`SendWorkflow` to a terminal key is a no-op** that returns the **existing** invocation id.
- **Multiple in-flight callers all resume** — a second, distinct caller awaiting a child that is
  still running attaches and receives the same recorded result when it completes (no caller hangs).
- **Cancel/kill propagates to the child** — the child inherits the caller's cancel-tree edge exactly
  as a `ctx.Call` child does; keying does not change propagation.

## Notes

- The `key` is a **routing value**, not a source of nondeterminism — supply it deterministically
  (do not mint it with `time`/`rand`/UUID on the replay path; use `ctx.UUID`/`ctx.Rand` if you must
  derive one).
- These verbs journal their intent through the existing `sdk.Context` seam; the keyed dispatch
  mechanism lives engine-side. No engine-internal type appears in the signatures — `[]byte`/`string`
  only.

## Related

- [Author a Workflow](./author-a-workflow.md) — write the callee's run-once + query handlers.
- [Serve a remote worker](./serve-a-remote-worker.md) — run the callee Workflow in a separate
  process via `sdk.Serve`.
- [SDK context reference §4/§5](../reference/sdk-context.md#4--synchronous-calls) — full verb
  definitions.
