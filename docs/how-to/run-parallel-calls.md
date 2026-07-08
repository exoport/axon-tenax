# How to Run Parallel Calls

This guide explains how to launch parallel tool calls from a durable handler using the
Promise-Combinator Engine (§7 of the state-machine contract). The combinators —
`ctx.Race`, `ctx.AwaitAny`, and `ctx.AwaitAll` — are replay-safe: all futures are journaled
before any combinator resolves, so the result is byte-identical on re-execution.

**How combinators run in Tenax:** the E19 combinator engine is fully daemon-dispatched.
The `tenaxd` worker role drives the combinator evaluation; the SDK verbs (`ctx.AwaitAny`,
`ctx.AwaitAll`, `ctx.Race`) express intent inside the handler, and the state machine
manages suspension and resumption via the journal. No goroutines or `select` statements
are needed inside a durable handler.

**Per-key total-order guarantee (ADR-0035):** under the N-concurrent-fetcher consumer,
each Virtual Object key has its own per-key CAS lock (the `tenax_pklocks` bucket). This
means combinator scenarios — even when multiple completions arrive at nearly the same time —
are evaluated in FIFO arrival order per voKey (N-511 guarantee). Distinct voKeys may process
concurrently. Stateless Services dispatch lock-free regardless of ordering mode.

## When to use which combinator

| Combinator                 | Behaviour                                                                                      | Use case                                                         |
| -------------------------- | ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `ctx.Race(futures...)`     | Returns the first-completed future (success or failure). Error code 573 if `futures` is empty. | Fan-out where you want the fastest result regardless of outcome. |
| `ctx.AwaitAny(futures...)` | Alias for `ctx.Race`. Returns the first-completed future.                                      | Preferred verb when the intent is "any success".                 |
| `ctx.AwaitAll(futures...)` | Awaits completion of all futures; returns slice of results or propagates the first failure.    | Fan-out where you need every result.                             |

## Steps

1. Start independent futures inside your handler using `ctx.Call` or `ctx.Send`.
2. Collect the returned `Promise` (or `Awaitable`) values.
3. Pass them to the combinator of your choice.
4. Handle error codes: 572 (`AWAITING_TWO_ASYNC_RESULTS`) or 573 (`UNSUPPORTED_FEATURE` /
   empty-race / all-failed) wrapped as `*sdk.CombinatorError`.

```go
// Example: await two downstream calls in parallel, take whichever completes first
a := ctx.Call("svcA", "handleA", payload)
b := ctx.Call("svcB", "handleB", payload)
result, err := ctx.AwaitAny(a, b)
```

```go
// Example: fan-out to three services and collect all results
p1 := ctx.Call("svc1", "work", req1)
p2 := ctx.Call("svc2", "work", req2)
p3 := ctx.Call("svc3", "work", req3)
results, err := ctx.AwaitAll(p1, p2, p3)
if errors.Is(err, sdk.ErrCombinatorFailed) {
    // one or more children failed
}
```

> The SDK evaluates combinators single-threaded. Do NOT use goroutines or `select` inside a
> durable handler — all concurrency is expressed through the combinator API.

## Error codes

Both error codes are returned wrapped as `*sdk.CombinatorError`. Test with
`errors.Is(err, sdk.ErrCombinatorFailed)`.

- **572** (`AWAITING_TWO_ASYNC_RESULTS`) — a second independent await tree was opened at the
  same logical await point while an unresolved tree was already active.
- **573** (`UNSUPPORTED_FEATURE`) — an empty `Race`/`AwaitAny` (first-of-nothing has
  undefined semantics, §7.2) or a combinator kind outside the valid range.

## Reference

See [`docs/reference/sdk-context.md`](../reference/sdk-context.md) for the full combinator verb
signatures, error code definitions (572/573), and replay-safety guarantees.

See also [`docs/explanation/overview.md`](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/overview.md) for the workers/ordering
configuration (`per-key` default vs `total` opt-in) and how the per-key CAS lock (ADR-0035)
provides the N-511 total-order guarantee that makes combinator evaluation deterministic across
concurrent dispatches.
