# Tutorial: Survive a Crash

> **Quadrant:** Tutorial — learning-oriented, step-by-step, ordered.
>
> **Persona:** Dana — SDK developer who has completed
> [Your First Durable Handler](./first-durable-handler.md) and now wants to see
> crash-transparent execution in practice.
>
> **What you will learn:** How a durable handler survives a worker crash between two
> side effects — the `charge → sleep → ship` pattern. You will watch the invocation
> reach `SUSPENDED (reason: sleep)`, kill the worker process, and confirm the handler
> resumes and reaches `COMPLETED` after the timer fires, with no double-charge.

---

## Prerequisites

- Completed [Your First Durable Handler](./first-durable-handler.md) — you know how to
  start a worker and inspect invocations.
- A running Tenax cluster (3-node R3). See
  [Configure NATS R3 substrate](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/configure-nats-r3-substrate.md).

---

## The Handler: charge → sleep → ship

This is the canonical Tenax durability demo. It shows how a handler survives a crash
between `ctx.Sleep` and the ship step without re-charging the customer.

### Annotated Code

```go
package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/exoport/axon-tenax/sdk"
)

// OrderRequest carries the order details to process.
type OrderRequest struct {
	OrderID string  `json:"orderId"`
	Amount  float64 `json:"amount"`
}

// orderHandler runs: charge → sleep → ship.
// Each step is durable — a crash between any two steps is transparent to the caller.
func orderHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	var order OrderRequest
	if err := json.Unmarshal(req, &order); err != nil {
		return nil, err
	}

	// ── Step 1: Charge ──────────────────────────────────────────────────────
	// ctx.Run journals a RunCommand BEFORE calling fn.
	// fn is called live (first attempt); the result is journaled as RunCompletion
	// BEFORE ctx.Run returns to handler code.
	// On REPLAY, fn is NOT called — the journaled result is returned verbatim.
	// The charge is never re-executed, even if the worker crashes between steps.
	chargeResult, err := ctx.Run("charge", func(opID string) ([]byte, error) {
		// opID = "<invocationID>/<entryIndex>" (e.g., "inv_abc/0")
		// Pass opID to the payment API as an idempotency key to dedup
		// within the honest at-least-once window.
		result, err := chargeCard(order.Amount, opID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	if err != nil {
		return nil, err
	}

	// ── Step 2: Sleep ───────────────────────────────────────────────────────
	// ctx.Sleep(d) journals a SleepCommand and suspends the invocation.
	// The runtime registers a durable timer on the NATS timer service (tenax.wakes).
	// The invocation reaches SUSPENDED (reason: sleep) and the attempt ends.
	// The timer fires after d and resumes the invocation on a subsequent attempt.
	// A crash here is transparent: the timer is durable in NATS JetStream.
	if err := ctx.Sleep(2 * time.Second); err != nil {
		return nil, err
	}

	// ── Step 3: Ship ────────────────────────────────────────────────────────
	// On resume, the journal is replayed. ctx.Run("charge") is encountered again
	// but does NOT call chargeCard — it reads the journaled RunCompletion and
	// returns the recorded result. No second charge. This is exactly-once.
	//
	// ctx.Sleep is encountered: the journal shows the timer already fired
	// (SleepCompletion is present), so Sleep returns immediately with nil.
	//
	// ctx.Run("ship") is new — it is live-executed for the first time.
	shipResult, err := ctx.Run("ship", func(opID string) ([]byte, error) {
		return shipOrder(order.OrderID, string(chargeResult), opID)
	})
	if err != nil {
		return nil, err
	}

	return shipResult, nil
}

func chargeCard(amount float64, idempotencyKey string) (string, error) {
	// ... call payment gateway with idempotencyKey to dedup ...
	return "charge_ok", nil
}

func shipOrder(orderID, chargeRef, idempotencyKey string) ([]byte, error) {
	// ... dispatch shipment with idempotencyKey to dedup ...
	return []byte(`"ship_ok"`), nil
}

func main() {
	svc := sdk.NewService("orders")
	if _, err := svc.Handler("process", orderHandler); err != nil {
		log.Fatal(err)
	}
	if err := sdk.Register(svc); err != nil {
		log.Fatal(err)
	}
}
```

---

## The Forbidden Pattern: Why `time.Now()` Is Banned in Handler Bodies

> **ADR-0011 warning — read this before writing any handler code.**

Inside a handler body you must **never** call:

- `time.Now()` — returns a different time on each replay; timer logic diverges
- `math/rand.Float64()` / `rand.Intn()` / etc. — different values on each replay; branching diverges
- Any UUID library (`github.com/google/uuid`, `github.com/oklog/ulid`, etc.) — different IDs on each replay; idempotency keys break

**Use the ctx alternatives instead:**

| Forbidden     | Use instead  |
| ------------- | ------------ |
| `time.Now()`  | `ctx.Now()`  |
| `math/rand.*` | `ctx.Rand()` |
| UUID library  | `ctx.UUID()` |

These ctx methods route nondeterminism through the journal: the result is recorded on
first execution and returned verbatim on every replay. The handler sees the same value
on every attempt.

**Why does this matter for crash survival?** If you call `time.Now()` live in a handler,
the first attempt records a timestamp like `2026-06-28T10:00:00Z`. After a crash and
replay, the second attempt calls `time.Now()` again and gets `2026-06-28T10:00:05Z` — a
different value. If your handler branches on that timestamp, the two attempts diverge.
Tenax detects this as a protocol violation and kills the invocation.

See [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) for the full
explanation and the complete nondeterminism prohibition table.

---

## Sequence Diagram

```
Handler (Attempt 1 — Live)
│
├── ctx.Run("charge", fn)
│       ├── journal: RunCommand(name="charge", index=0)
│       ├── live: fn(opID="inv_abc/0") → "charge_ok"
│       └── journal: RunCompletion(result="charge_ok")
│                                              ← ctx.Run returns "charge_ok"
│
├── ctx.Sleep(2s)
│       ├── journal: SleepCommand(wakeAt=T+2s, index=1)
│       └── runtime: registers durable timer on tenax.wakes
│                                              ← invocation reaches SUSPENDED (reason: sleep)
│                                              ← attempt 1 ends
│
─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ (crash or relocation possible here)

Timer fires at T+2s
│
└── tenax.wakes delivers wake event → invocation re-enqueued

Handler (Attempt 2 — Replay then Live)
│
├── ctx.Run("charge", fn)                     ← REPLAY: fn NOT called
│       └── journal: RunCompletion found → returns "charge_ok" (no second charge)
│
├── ctx.Sleep(2s)                             ← REPLAY: SleepCompletion found → returns nil immediately
│
└── ctx.Run("ship", fn)                       ← LIVE: first time reaching this entry
        ├── journal: RunCommand(name="ship", index=2)
        ├── live: fn(opID="inv_abc/2") → "ship_ok"
        └── journal: RunCompletion(result="ship_ok")
                                               ← ctx.Run returns "ship_ok"
                                               ← handler returns → journal: Output/End
                                               ← invocation reaches COMPLETED
```

Note that `COMPLETED` is emitted only after the terminal `Output/End` journal entry
is durably committed to the replicated JetStream stream. It is never shown speculatively.

---

## The Replay Explanation

When the timer fires, the runtime re-dispatches the invocation. The handler function
re-executes from the top, but the runtime intercepts every `ctx.*` call and checks the
journal:

- `ctx.Run("charge", fn)` — a `RunCompletion` entry exists at index 0. The journaled
  result (`"charge_ok"`) is returned. `fn` is not called. **No second charge.**
- `ctx.Sleep(2s)` — a `SleepCompletion` entry exists at index 1. Sleep returns `nil`
  immediately. The timer is not re-registered.
- `ctx.Run("ship", fn)` — no entry at index 2. This is new territory. The runtime
  calls `fn` live, journals `RunCommand` before calling and `RunCompletion` before
  returning.

This is the fundamental replay model: **the journal is the source of truth for completed
steps; live code runs only for steps not yet in the journal**.

---

## Live Demo: Surviving kill -9

Run this yourself to see crash survival in action.

### 1. Start the stack

Terminal A — ingress:

```bash
tenaxd --role ingress --nats nats://localhost:4222
```

Terminal B — worker:

```bash
tenaxd --role worker --nats nats://localhost:4222
```

Terminal C — timer:

```bash
tenaxd --role timer --nats nats://localhost:4222
```

### 2. Submit the invocation

```bash
nats request tenax.ingress.call \
  '{"service":"orders","handler":"process","payload":{"orderId":"ord-42","amount":99.50}}' \
  --header Tenax-Idempotency-Key:order-42-process
```

Response:

```json
{
  "idempotencyKey": "order-42-process",
  "invId": "inv_abc123",
  "status": "PENDING"
}
```

### 3. Watch it suspend

```bash
tenax invocation inspect inv_abc123
```

Within a moment, the state shows:

```
state: SUSPENDED (sleep)
```

This means:

- Step 1 (`charge`) completed and was journaled — no second charge possible.
- The 2-second sleep timer was registered in NATS JetStream.
- The worker attempt ended. The invocation is durable.

### 4. Kill the worker

While the invocation is `SUSPENDED (reason: sleep)`, send SIGKILL to the worker:

```bash
# Find the worker PID
ps aux | grep "tenaxd --role worker"

# Kill it hard
kill -9 <worker-pid>
```

### 5. Restart the worker (optional)

The timer service will still fire and re-enqueue the invocation. Restart the worker in
Terminal B:

```bash
tenaxd --role worker --nats nats://localhost:4222
```

### 6. Confirm COMPLETED

After ~2 seconds from when the sleep started, inspect again:

```bash
tenax invocation inspect inv_abc123
```

Expected output:

```
┌ Invocation Detail ──────────────────────────────────────┐
  id:              inv_abc123
  identity:        orders
  key:             —
  state:           COMPLETED
  deployment:      —
  partition:       0
  epoch:           2
  idempotency:     order-42-process
└─────────────────────────────────────────────────────────┘

Journal Timeline:
  #0  ⚡ RunCommand    charge          (effect-once)
  #1  ✔ RunCompletion charge          "charge_ok"
  #2  💤 SleepCommand                 wake=T+2s
  #3  ✔ SleepCompletion
  #4  ⚡ RunCommand    ship            (effect-once)
  #5  ✔ RunCompletion ship            "ship_ok"
  #6  ✔ Output/End
```

The `epoch` counter (2 vs 1) shows the worker restarted, but the handler reached
`COMPLETED` with exactly-once semantics — `chargeCard` was called once, `shipOrder`
was called once.

For the full status vocabulary, see [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md).

---

## Summary

| What happened                         | Why it worked                                                                                  |
| ------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Worker crashed between Sleep and Ship | The sleep timer is durable in NATS JetStream — it fires regardless of the worker process       |
| `chargeCard` was not called again     | `RunCompletion` for index 0 was in the journal — replay returned it without calling `fn`       |
| `shipOrder` ran exactly once          | No `RunCompletion` at index 2 on second attempt — ran live, journaled, completed               |
| No `time.Now()` in handler            | The replay model requires deterministic handler code — all nondeterminism routes through `ctx` |

---

## Next Steps

- [First cluster](https://github.com/exoar/axon_tenax_engine/blob/main/docs/tutorials/first-cluster.md) — stand up a 3-node R3 cluster from scratch
- [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) — deeper explanation
  of why determinism is required and what happens when it is violated
- [`ctx.*` verb reference](../reference/sdk-context.md) — full SDK surface including
  `ctx.Sleep`, `ctx.Now`, `ctx.Rand`, `ctx.UUID`, `ctx.Call`, `ctx.Send`
  _(arrives in Story 24.4)_
