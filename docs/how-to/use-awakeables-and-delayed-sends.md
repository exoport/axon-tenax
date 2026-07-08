# How to Use Awakeables and Delayed Sends

> **Quadrant:** How-to — goal-oriented, task-oriented guide for resolving a durable promise
> from an external system (awakeables) and scheduling a message for later dispatch (delayed
> sends). For the exhaustive `ctx.*` verb list, see
> [SDK context reference §5–§6](../reference/sdk-context.md#5--one-way-fire-and-forget).

## Awakeables: Waiting on an External System

An **awakeable** is a durable promise your handler can hand to an external system (a webhook
callback, a human-approval step, a third-party async API) and later suspend on, resuming only
once that external system resolves or rejects it.

### Step 1: Create the Awakeable and Share Its ID

```go
func approvalHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	id, promise, err := ctx.Awakeable()
	if err != nil {
		return nil, err
	}

	// id carries the "akb_"-prefixed form, e.g. "akb_3q2-7wAAAAc" (ADR-0037).
	// Share it with the external system — e.g. embed it in an approval-request
	// email/webhook payload so the approver's callback can reference it.
	if _, err := ctx.Run("send-approval-request", func(opID string) ([]byte, error) {
		return sendApprovalEmail(opID, id)
	}); err != nil {
		return nil, err
	}

	// Suspend until the awakeable is resolved or rejected. The invocation
	// enters SUSPENDED (reason: awakeable) while waiting.
	result, err := promise.Await()
	if err != nil {
		return nil, err // rejected, or ErrDuplicateCompletion-adjacent failure
	}
	return result, nil
}
```

### Step 2: Resolve or Reject the Awakeable

The **external system** (not the suspended handler) resolves the awakeable — typically from
a _different_ invocation's handler, once it receives the approval callback:

```go
func approvalCallbackHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	var cb struct {
		AwakeableID string `json:"awakeableId"`
		Approved    bool   `json:"approved"`
	}
	if err := json.Unmarshal(req, &cb); err != nil {
		return nil, err
	}

	if cb.Approved {
		if err := ctx.CompleteAwakeable(cb.AwakeableID, []byte(`"approved"`)); err != nil {
			// ErrDuplicateCompletion if already resolved (first-writer-wins, ADR-0004).
			return nil, err
		}
	} else {
		if err := ctx.RejectAwakeable(cb.AwakeableID, "approver declined"); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
```

An already-resolved awakeable's second resolution attempt returns `ErrDuplicateCompletion` —
first-writer-wins, so a duplicate callback delivery (e.g. a webhook retry) is safe.

### Resuming a Handle to an Existing Awakeable

If the awaiting handler is not the one that created the awakeable, obtain a `Promise` handle
from the ID directly:

```go
p := ctx.Promise(awakeableID)
result, err := p.Await()
```

---

## Delayed Sends: Scheduling a Message for Later

`ctx.SendDelayed` and `ctx.SendAt` dispatch a one-way message at a future time WITHOUT
suspending the caller — the caller returns immediately; the target handler runs later, as its
own independent invocation.

```go
func orderPlacedHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	orderID, err := ctx.Run("record-order", func(opID string) ([]byte, error) {
		return recordOrder(opID, req)
	})
	if err != nil {
		return nil, err
	}

	// Send a reminder in 24 hours if the order hasn't shipped by then.
	// The caller does NOT suspend — this returns immediately.
	if _, err := ctx.SendDelayed("orders", "remind-if-unshipped", orderID, 24*time.Hour); err != nil {
		return nil, err
	}

	return orderID, nil
}
```

`ctx.SendAt` is the absolute-time equivalent — use it when you have a specific deadline
rather than a relative delay:

```go
deadline := ctx.Now().Add(24 * time.Hour) // ctx.Now(), never time.Now() (ADR-0011)
if _, err := ctx.SendAt("orders", "remind-if-unshipped", orderID, deadline); err != nil {
	return nil, err
}
```

**Always compute the target time via `ctx.Now()`, never `time.Now()`** — `InvokeTime` is
journaled for deterministic replay (ADR-0011); calling `time.Now()` directly inside a handler
body is a forbidden replay hazard (see
[SDK context reference §9](../reference/sdk-context.md#9--nondeterminism-via-ctx-forbidden-patterns)).

Both `ctx.SendDelayed` and `ctx.SendAt` return the scheduled invocation's handle (its ID) on
success — the same one-way dispatch/dedup guarantees `ctx.Send` provides apply (an
idempotency key derived from the caller's `(inv,index)` opId, ADR-0006).

---

## See Also

- [SDK context reference §5–§6](../reference/sdk-context.md#5--one-way-fire-and-forget) —
  exhaustive `ctx.Send`/`SendDelayed`/`SendAt`/`Awakeable`/`Promise`/`CompleteAwakeable`/
  `RejectAwakeable` verb definitions
- [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) — why `ctx.Now()`
  replaces `time.Now()`
- [Write a saga](./write-a-saga.md) — durable compensation, a related suspend/resume pattern
- [Back to docs index](https://github.com/exoar/axon_tenax_engine/blob/main/docs/index.md)
