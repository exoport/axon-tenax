# How to Serve a Remote Worker

> **Quadrant:** How-to — goal-oriented guide for running your handlers in a **separate process** (its
> own binary / Go module) that receives Workflow dispatches from `tenaxd` over NATS via `sdk.Serve`.
> For authoring the handlers themselves, see [Author a Workflow](./author-a-workflow.md). For the
> operator's side — deploying and scaling remote workers — see the engine's
> [Deploy remote workers](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/deploy-remote-workers.md).

By default a handler is **co-located** with the engine: you call `sdk.RegisterWorkflow(wf)` and a
`tenaxd --role worker` discovers and runs it in-process. `sdk.Serve` is the alternative for a
**separately-deployed** worker — your own binary, importing only the public SDK, that consumes
dispatches over NATS:

```go
func Serve(ctx context.Context, nc *nats.Conn, reg *Registry, opts ...ServeOption) error
```

`Serve` advertises the handlers registered in `reg` to `tenaxd`, consumes dispatches for them,
executes each on your goroutines, and publishes the results. It **blocks** until `ctx` is cancelled,
then drains in-flight invocations before returning.

---

## Step 1: Build a Registry (explicitly)

`Serve` takes an **explicit `*Registry`** — not the package-level `sdk.GlobalRegistry()`. A
process-global singleton is exactly what a remote worker must avoid; pass your own registry:

```go
reg := sdk.NewRegistry()

wf := sdk.NewWorkflow("cortex-interpreter").Run(interpreterRun)
if err := reg.RegisterWorkflow(wf); err != nil {
	log.Fatal(err)
}
```

Registration is **static** — register your handler set once at startup. A registry is a set, so you
may register more than one handler kind if a single binary serves several (uncommon).

## Step 2: Call `Serve` from `main`

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exoport/axon-tenax/sdk"
	"github.com/nats-io/nats.go"
)

func main() {
	reg := sdk.NewRegistry()
	if err := reg.RegisterWorkflow(sdk.NewWorkflow("cortex-interpreter").Run(interpreterRun)); err != nil {
		log.Fatal(err)
	}

	nc, err := nats.Connect(os.Getenv("TENAX_NATS_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	// Graceful shutdown: SIGTERM cancels ctx, Serve drains in-flight, then returns.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	if err := sdk.Serve(ctx, nc, reg,
		sdk.WithConcurrency(256),
		sdk.WithDrainTimeout(30*time.Second),
		sdk.WithWorkerName("cortex-interpreter-1"),
	); err != nil {
		log.Fatalf("tenax serve: %v", err)
	}
}
```

That is the whole worker: build a registry, connect to NATS, call `Serve`.

## Step 3: Tune the serve options

| Option | Effect |
| --- | --- |
| `WithConcurrency(n int)` | Max **concurrent independent invocations** the worker runs. Bound to the durable consumer's `MaxAckPending`, so it is honest backpressure — the worker never pulls more than `n` un-acked dispatches. Size it to your per-node throughput target. |
| `WithDrainTimeout(d time.Duration)` | Graceful-shutdown budget: on `ctx` cancellation the worker stops accepting new dispatches and finishes in-flight ones, bounded by `d`. |
| `WithWorkerName(name string)` | Identity advertised to `tenaxd` (for the Run console and traces). Defaults to `os.Hostname()`. |

Concurrency here runs **different** invocations (different keys) on separate goroutines — that is the
intended parallelism and is fully compatible with per-invocation deterministic replay. It is **not**
the intra-handler goroutine hazard that breaks replay; do not spawn goroutines *inside* a handler
body.

## Step 4: What crash-recovery you get (the failure contract)

`sdk.Serve`'s failure semantics are **identical to a co-located (`--runtime inproc`) worker** — this
is a frozen contract, not best-effort:

- If a worker process dies mid-dispatch, the in-flight invocation stays **journal-resumable by any
  restarted worker instance** — it is never pinned to the dead process.
- Redelivery is **at-least-once against the recorded op**, with `opId` dedup bounding external effects
  to the crash-before-journal window.
- A SIGKILL of a worker mid-Run resumes on a fresh worker and each effect executes **exactly once**.

You do not implement any of this — it is what `Serve` guarantees. Run more than one worker instance
for the same handler set and they share the work-queue; any instance can resume any invocation.

## Related

- [Author a Workflow](./author-a-workflow.md) — write the handler `Serve` runs.
- [Dispatch a keyed Workflow](./dispatch-a-keyed-workflow.md) — call/await this worker's Workflow
  from another handler.
- [Deploy remote workers](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/deploy-remote-workers.md)
  — the operator's deployment + scaling guide.
