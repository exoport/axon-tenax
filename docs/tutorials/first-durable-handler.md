# Tutorial: Your First Durable Handler

> **Quadrant:** Tutorial — learning-oriented, step-by-step, ordered.
>
> **Persona:** Dana — new SDK developer building her first durable handler.
>
> **What you will build:** A simple `greeter` service with a single `greet` handler that
> uses `ctx.Run` to record a side effect durably. You will invoke it, watch it reach
> `COMPLETED`, and understand why every step is exactly-once even across crashes.

---

## Prerequisites

- Go 1.26 or later installed (`go version`)
- A running Tenax cluster (3-node R3). If you have not set one up yet, follow
  [Configure NATS R3 substrate](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/configure-nats-r3-substrate.md) first.
- The `tenax` and `tenaxd` binaries built from source (see Step 3 below, or run
  `make build` if you have already cloned the repo).

---

## Step 1: Fetch the SDK

Create a new Go module for your service:

```bash
mkdir greeter && cd greeter
go mod init github.com/example/greeter
go get github.com/exoport/axon-tenax/sdk@latest
```

---

## Step 2: Write the Handler

Create `main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/exoport/axon-tenax/sdk"
)

// GreetRequest carries the name to greet.
type GreetRequest struct {
	Name string `json:"name"`
}

// greetHandler is a durable handler: it records a greeting via ctx.Run,
// guaranteeing exactly-once execution even if the worker crashes mid-flight.
//
// IMPORTANT: never call time.Now(), math/rand, or a UUID generator directly
// inside a handler body. Use ctx.Now(), ctx.Rand(), ctx.UUID() instead.
// See: ../explanation/replay-and-determinism.md
func greetHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	var r GreetRequest
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("greetHandler: decode request: %w", err)
	}

	// ctx.Run records a durable side effect.
	//
	// First execution: fn is called live; the result is journaled BEFORE
	// ctx.Run returns. A crash after the journal write but before return is
	// handled transparently — on replay the journaled result is returned and
	// fn is NOT called again.
	//
	// opID = "<invocationID>/<entryIndex>" (e.g. "inv_abc/0").
	// Pass it to any external API as an idempotency key.
	greeting, err := ctx.Run("greet", func(opID string) ([]byte, error) {
		msg := fmt.Sprintf("Hello, %s! (op=%s)", r.Name, opID)
		return []byte(msg), nil
	})
	if err != nil {
		return nil, err
	}

	return greeting, nil
}

func main() {
	svc := sdk.NewService("greeter")
	if _, err := svc.Handler("greet", greetHandler); err != nil {
		log.Fatal(err)
	}
	if err := sdk.Register(svc); err != nil {
		log.Fatal(err)
	}
	// tenaxd --role worker discovers the registered service and starts dispatching.
}
```

Key points:

- `ctx.Run("greet", fn)` — the string `"greet"` is the journal entry name; use
  short, descriptive names unique within the handler.
- `opID` is stable across replays — safe to pass as an idempotency key to external
  systems (payment APIs, email providers, etc.).
- No `time.Now()`, no `math/rand` — the handler body must be deterministic on
  every replay. See [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md)
  for the full explanation.

---

## Step 3: Build

From a checkout of the Tenax engine repo (`axon_tenax_engine`):

```bash
make build
```

This produces the four binaries under `bin/`: `tenaxd`, `tenax`, `tenax-operator`,
and `tenax-nex-autoscaler`. The `tenaxd` and `tenax` binaries are what you need here.

For your own greeter service, build it the standard Go way:

```bash
go build -o greeter .
```

---

## Step 4: Start the Worker

Open a terminal and run the worker role, pointing it at your NATS cluster:

```bash
tenaxd --role worker --nats nats://localhost:4222
```

You should see log output confirming the worker is connected and listening:

```
level=INFO msg="worker started" partitions=1 nats=nats://localhost:4222
```

In a second terminal, also start the ingress role (it accepts inbound invocation
requests via NATS Micro):

```bash
tenaxd --role ingress --nats nats://localhost:4222
```

---

## Step 5: Invoke the Handler

Tenax uses NATS Micro as its ingress gateway. You can submit an invocation using the
`nats` CLI (install from [nats.io/download](https://nats.io/download)):

```bash
nats request tenax.ingress.call '{"service":"greeter","handler":"greet","payload":{"name":"Dana"}}' \
  --header Tenax-Idempotency-Key:greet-dana-001
```

The ingress responds with an invocation ID:

```json
{
  "idempotencyKey": "greet-dana-001",
  "invId": "inv_abc123",
  "status": "PENDING"
}
```

Note the `invId` — you will use it in the next step.

> **Forward reference:** A dedicated `tenax invocation call` CLI verb is planned for
> the SDK context reference (Story 24.4). For now, `nats request` against the NATS
> Micro subject is the direct invocation path.
> See [CLI reference](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/cli.md) for all current CLI verbs.

---

## Step 6: Observe COMPLETED

Inspect the invocation to confirm it completed:

```bash
tenax invocation inspect inv_abc123
```

Expected output:

```
┌ Invocation Detail ──────────────────────────────────────┐
  id:              inv_abc123
  identity:        greeter
  key:             —
  state:           COMPLETED
  deployment:      —
  partition:       0
  epoch:           1
  idempotency:     greet-dana-001
└─────────────────────────────────────────────────────────┘

Journal Timeline:
  #0  ⚡ RunCommand    greet          (effect-once)
  #1  ✔ RunCompletion greet          "Hello, Dana! (op=inv_abc123/0)"
  #2  ✔ Output/End
```

`COMPLETED` is emitted only after the terminal `Output/End` journal entry is
durably committed to the replicated JetStream stream. It is never shown speculatively.

For the full status vocabulary, see [Status lexicon](https://github.com/exoar/axon_tenax_engine/blob/main/docs/reference/status-lexicon.md).

---

## What Just Happened

1. You sent a request to the NATS Micro ingress (`tenax.ingress.call`).
2. The ingress wrote a `Start` entry to the invocation journal and routed to a worker.
3. The worker replayed the journal (empty on first attempt) then called `greetHandler`.
4. `ctx.Run("greet", fn)` wrote a `RunCommand` entry, called `fn` live, then wrote
   `RunCompletion` — all before returning to handler code.
5. The handler returned its result; the runtime wrote `Output/End`.
6. The invocation reached `COMPLETED`.

At no point could a crash between any two of these steps cause `fn` to be called twice
— the journal entry written before the live call acts as a fence.

---

## What Would Go Wrong Without Tenax

If you called an external API directly (without `ctx.Run`), a crash after the API call
but before the response was stored would cause the call to be replayed on restart — a
double-charge, a double-email, a double-shipment. `ctx.Run` eliminates this window.

---

## Next Steps

- [Survive a crash](./survive-a-crash.md) — watch a handler survive a `kill -9` mid-execution
- [Replay and determinism](https://github.com/exoar/axon_tenax_engine/blob/main/docs/explanation/replay-and-determinism.md) — why handler code
  must be deterministic and what happens when it is not
- [`ctx.*` verb reference](../reference/sdk-context.md) — full SDK surface including
  `ctx.Sleep`, `ctx.Call`, `ctx.Send`, `ctx.Now`, `ctx.Rand`, `ctx.UUID`
  _(arrives in Story 24.4)_
