# How to Author a Workflow

> **Quadrant:** How-to — goal-oriented, task-oriented guide for authoring a Workflow with a
> run-once handler, query handlers, and the read-only `QueryContext` restriction. For the
> exhaustive `ctx.RegisterCompensation` verb definition, see
> [SDK context reference §8](../reference/sdk-context.md#8--sagas-durable-compensation) (this
> page's own `ctx.Run`/`ctx.Set` usage is covered by SDK context reference §1/§3, not §8).
> For durable compensation stacks in general (not Workflow-specific), see
> [Write a saga](./write-a-saga.md) — this page does not duplicate that content.

A **Workflow** is a keyed durable handler (same routing-key model as a Virtual Object) whose
`Run` handler executes exactly once per key, and which additionally supports:

- **Durable compensation stacks** (`ctx.RegisterCompensation`) — the same mechanism
  [Write a saga](./write-a-saga.md) documents in full; a Workflow's `Run` handler is simply
  one more place that mechanism is available. This page's distinct value over that one is
  Workflow _registration_ and _query handlers_ — read on.
- **Query handlers** — a separate, read-only handler kind registered alongside `Run`, that
  external callers can invoke to inspect the workflow's state WITHOUT running any mutation
  logic.

---

## Step 1: Write the Run Handler

The `Run` handler uses `KeyedHandlerFunc` — identical signature to a Virtual Object handler
(`ctx sdk.Context, key string, req []byte`). It executes once per key: Tenax runs the
handler to completion (or terminal failure) and does not re-invoke it for the same key.

```go
package main

import (
	"encoding/json"
	"log"

	"github.com/exoport/axon-tenax/sdk"
)

type OnboardRequest struct {
	Email string `json:"email"`
}

func onboardRunHandler(ctx sdk.Context, key string, req []byte) ([]byte, error) {
	var r OnboardRequest
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, err
	}

	// Forward step: provision the account.
	provisionResult, err := ctx.Run("provision-account", func(opID string) ([]byte, error) {
		return provisionAccount(opID, key, r.Email)
	})
	if err != nil {
		return nil, err
	}

	// Register a compensation exactly as any saga step would (see Write a
	// saga for the full mechanism) — Workflow.Run handlers are ordinary
	// ctx.RegisterCompensation call sites.
	if _, err := ctx.RegisterCompensation(func(ctx sdk.Context) error {
		_, err := ctx.Run("deprovision-account", func(opID string) ([]byte, error) {
			return deprovisionAccount(opID, key)
		})
		return err
	}); err != nil {
		return nil, err
	}

	// Track onboarding status in per-key state, readable by the query
	// handler below.
	if err := ctx.Set("status", []byte(`"onboarded"`)); err != nil {
		return nil, err
	}

	return provisionResult, nil
}
```

---

## Step 2: Write a Query Handler

Query handlers receive a restricted `QueryContext`, not the full `sdk.Context` — mutation
verbs (`Set`/`Run`/`Call`/`Send`/`Sleep`) are not reachable from a query handler BY
CONSTRUCTION (the type does not expose them, not merely a runtime check):

```go
type QueryHandlerFunc func(ctx sdk.QueryContext, args json.RawMessage) (json.RawMessage, error)
```

`QueryContext` exposes exactly two methods:

- `Get(key string) ([]byte, error)`
- `List() ([]string, error)`

```go
func onboardStatusQuery(ctx sdk.QueryContext, _ json.RawMessage) (json.RawMessage, error) {
	status, err := ctx.Get("status")
	if err != nil {
		return nil, err
	}
	return status, nil
}
```

Because `QueryContext` has no `Set`/`Run`/`Call`/`Send`/`Sleep` methods at all, a query
handler cannot accidentally mutate workflow state or perform a durable side effect — this is
enforced by the Go type system, not by a runtime guard a handler author could bypass.

---

## Step 3: Register the Workflow

```go
func main() {
	wf := sdk.NewWorkflow("onboarding").Run(onboardRunHandler)

	wf, err := wf.Query("status", onboardStatusQuery)
	if err != nil {
		log.Fatal(err)
	}

	if err := sdk.RegisterWorkflow(wf); err != nil {
		log.Fatal(err)
	}
	// tenaxd --role worker discovers the registered Workflow and dispatches
	// both the run-once Run invocation and any query-handler calls to it.
}
```

`sdk.RegisterWorkflow` is the package-level default-registry registration function for
Workflows — parallel to `sdk.Register` for Services, but there is no package-level
`RegisterVirtualObject` (that one goes through `sdk.GlobalRegistry()` — see
[How to author a Virtual Object](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/author-a-virtual-object.md)).

---

## See Also

- [Write a saga](./write-a-saga.md) — the full `ctx.RegisterCompensation` mechanism this
  page's `Run` handler uses; read that page for compensation-stack semantics
- [How to author a Virtual Object](https://github.com/exoar/axon_tenax_engine/blob/main/docs/how-to/author-a-virtual-object.md) — the keyed-handler shape a
  Workflow's `Run` handler shares
- [SDK context reference §8](../reference/sdk-context.md#8--sagas-durable-compensation) —
  exhaustive `ctx.RegisterCompensation` verb definition
- [Back to docs index](https://github.com/exoar/axon_tenax_engine/blob/main/docs/index.md)
