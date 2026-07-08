# How to Write a Saga

> **Quadrant:** How-to — goal-oriented guide for registering durable compensation stacks.

A saga is a sequence of forward steps, each paired with a compensation (undo) step. If a
graceful cancel fires after some forward steps have completed, the compensation stack runs
in **reverse registration order (LIFO)** — so later steps are undone before earlier ones.

**How sagas run in Tenax:** the `tenaxd` daemon in **worker** role picks up saga invocations
from the partition work-queue, hydrates the journal via the state machine, and executes the
compensation stack through `internal/saga` on graceful cancel. The SDK surface (`ctx.*` verbs)
is the same whether the handler runs in the thin-executor path or the fat co-located engine
(ADR-0036). Code compiled against `github.com/exoport/axon-tenax/sdk` runs unchanged on both paths.

**`cancel` vs `kill`:**

- `cancel` = graceful + compensating: the saga compensation stack runs on `CANCELLING → CANCELLED`.
- `kill` = forceful + non-compensating: the saga does NOT run on kill.

---

## Using `ctx.RegisterCompensation` Directly

Register a compensation function immediately after each forward step. The compensation
itself uses `ctx.Run` so the undo effect is also exactly-once.

```go
// Pattern: register compensation immediately after the forward step.
func payAndShipHandler(ctx sdk.Context, req []byte) ([]byte, error) {
    // Forward step: charge.
    chargeResult, err := ctx.Run("charge", func(opID string) ([]byte, error) {
        return chargeCard(opID)
    })
    if err != nil {
        return nil, err
    }

    // Register compensation: if cancel fires after this point, refund the charge.
    // The compensation itself uses ctx.Run so the refund is also exactly-once.
    if _, err := ctx.RegisterCompensation(func(ctx sdk.Context) error {
        _, err := ctx.Run("refund", func(opID string) ([]byte, error) {
            return refundCard(string(chargeResult), opID)
        })
        return err
    }); err != nil {
        return nil, err
    }

    // Forward step: ship.
    shipResult, err := ctx.Run("ship", func(opID string) ([]byte, error) {
        return shipOrder(string(chargeResult), opID)
    })
    if err != nil {
        return nil, err
    }

    return shipResult, nil
}
```

**What `ctx.RegisterCompensation` returns:** a `"cmp_"`-prefixed compensation ID assigned
by the state machine via `ctx.UUID()` (journaled for deterministic replay). On replay,
the recorded compId is returned from the journal without re-registering.

**Restriction:** Calling `RegisterCompensation` from inside a compensation function is
forbidden and returns `ErrRecursiveCompensation` (a terminal `PROTOCOL_VIOLATION`).

---

## Using the `sdk.NewSaga` Convenience Builder

`sdk.NewSaga` + `saga.RegisterAll(ctx)` is a shorthand for registering all compensations
at once:

```go
saga := sdk.NewSaga("pay-and-ship")
saga.AddCompensation(func(ctx sdk.Context) error {
    _, err := ctx.Run("refund", func(opID string) ([]byte, error) {
        return refundCard(chargeRef, opID)
    })
    return err
})
// RegisterAll calls ctx.RegisterCompensation for each step in declaration order.
if _, err := saga.RegisterAll(ctx); err != nil {
    return nil, err
}
```

---

## Triggering Cancel from the CLI

```bash
# Graceful cancel — saga compensations run:
tenax invocation cancel inv_abc123 -y

# Forceful kill — no compensation:
tenax invocation kill inv_abc123 -y
```

State transitions:

- `cancel`: `RUNNING` or `SUSPENDED` → `CANCELLING` → `CANCELLED`
- `kill`: `RUNNING` or `SUSPENDED` → `KILLED`

For the full cancel vs kill semantics, see [cancel or kill an invocation](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/cancel-or-kill-invocation.md).

---

## See Also

- [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) — why compensations must use `ctx.Run`
- [Cancel or kill an invocation](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/cancel-or-kill-invocation.md) — ops procedure
- [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md) — `CANCELLING`, `CANCELLED`, `KILLED` status words
- [Back to docs index](https://github.com/exoar/axon_tenax_engine/blob/main/docs/index.md)
