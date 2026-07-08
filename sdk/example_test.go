package sdk_test

// example_test.go — compile-verified ExampleXxx function(s) mirroring
// docs/tutorials/first-durable-handler.md's greeter shape (Story 44.4, AC 5),
// and the same shape examples/greeter/main.go builds as a standalone sample.
//
// package sdk_test (external test package) imports ONLY the public
// github.com/exoport/axon-tenax/sdk surface — zero internal/... imports
// (ADR-0028) — proving the boundary holds for this compile-verified sample
// exactly as it holds for examples/greeter.
//
// Example_greeter carries a trailing "// Output:" comment, so `go test`
// EXECUTES it (not merely compiles it) on every `go test ./sdk/...` run —
// deliberately, because sdk.NewService/Handler/Register are pure in-memory
// registry bookkeeping with zero NATS dependency: exercising them requires
// no live cluster, so there is no live-gate hazard in running this Example
// for real (development/project-context.md's Testing Rules). What this
// Example does NOT do is dispatch a real invocation through greetHandler's
// ctx.Run — that requires a live worker consuming a real invocation off a
// running NATS R3 cluster, which is exactly the live-gate dependency the
// unit tier must never acquire. greetHandler itself is still fully
// compile-checked here (it is referenced as the registered handler), so a
// signature/import drift against the tutorial's shape is caught on every
// unit-test run either way.

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/exoport/axon-tenax/sdk"
)

// greetRequest carries the name to greet — mirrors GreetRequest in
// docs/tutorials/first-durable-handler.md and examples/greeter/main.go.
type greetRequest struct {
	Name string `json:"name"`
}

// greetHandler is a durable handler: it records a greeting via ctx.Run,
// guaranteeing exactly-once execution even if the worker crashes mid-flight.
// Identical shape to the tutorial and examples/greeter/main.go — see either
// for the full field-by-field explanation of why ctx.Run is used here
// instead of a plain function call. Never invoked directly by this file
// (that would require a live NATS R3 cluster and worker to dispatch through)
// — only registered, so it is compile-checked but not executed here.
func greetHandler(ctx sdk.Context, req []byte) ([]byte, error) {
	var r greetRequest
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("greetHandler: decode request: %w", err)
	}

	greeting, err := ctx.Run("greet", func(opID string) ([]byte, error) {
		msg := fmt.Sprintf("Hello, %s! (op=%s)", r.Name, opID)
		return []byte(msg), nil
	})
	if err != nil {
		return nil, err
	}

	return greeting, nil
}

// Example_greeter demonstrates registering a stateless Service with a single
// handler — the same sdk.NewService/Handler/Register pattern
// docs/tutorials/first-durable-handler.md teaches and examples/greeter/main.go
// builds as a standalone binary. Registration is pure in-memory bookkeeping
// (no NATS dial, no journal write), so it is safe to actually execute (and
// verify via "// Output:") in the unit tier — unlike dispatching a real
// invocation through greetHandler, which this Example deliberately never
// does.
//
// "Example_greeter" (not "ExampleNewService" or similar) is the
// identifier-less package-example form with a descriptive suffix — this
// Example demonstrates a usage pattern spanning NewService/Handler/Register
// together, not any single one of those identifiers alone.
func Example_greeter() {
	svc, err := sdk.NewService("greeter").Handler("greet", greetHandler)
	if err != nil {
		log.Fatal(err)
	}
	if err := sdk.Register(svc); err != nil {
		log.Fatal(err)
	}
	fmt.Println("registered service:", svc.Name())
	// tenaxd --role worker discovers the registered service and starts
	// dispatching invocations to it once connected to a live NATS cluster.

	// Output: registered service: greeter
}
