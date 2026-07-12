# Tenax SDK Docs

Author-facing documentation for the `axon-tenax` Go SDK, organized by
[Diátaxis](https://diataxis.fr) quadrant. Engine and operations docs (cluster
setup, release, metrics, the state-machine contract, ADRs) live in the
[engine repo](https://github.com/exoar/axon_tenax_engine).

## Tutorials

Learning-oriented, step-by-step. Start here if you are new to the SDK.

- [Your first durable handler](tutorials/first-durable-handler.md) — write a
  `greeter` service, invoke it, and watch it reach `COMPLETED`.
- [Survive a crash](tutorials/survive-a-crash.md) — kill a worker mid-execution
  and watch the handler resume with no double-charge.

## How-to Guides

Goal-oriented recipes for a specific task.

- [Author a Workflow](how-to/author-a-workflow.md) — run-once handlers, query
  handlers, and the read-only `QueryContext`.
- [Write a saga](how-to/write-a-saga.md) — register durable compensation
  stacks with `ctx.RegisterCompensation`.
- [Run parallel calls](how-to/run-parallel-calls.md) — fan out with
  `ctx.Race` / `ctx.AwaitAny` / `ctx.AwaitAll`.
- [Use awakeables and delayed sends](how-to/use-awakeables-and-delayed-sends.md)
  — wait on external systems and schedule messages for later.
- [Dispatch a keyed Workflow](how-to/dispatch-a-keyed-workflow.md) — start/await a
  keyed child Workflow with `ctx.CallWorkflow` / `ctx.SendWorkflow`.
- [Serve a remote worker](how-to/serve-a-remote-worker.md) — run handlers in a
  separate process with `sdk.Serve`.

## Reference

Exhaustive, information-oriented.

- [SDK context reference (`sdk.Context`)](reference/sdk-context.md) — every
  `ctx.*` verb, handler registration pattern, and determinism rule.

## See Also

- [Repository README](../README.md) — install instructions and package layout.
- [Engine repo docs](https://github.com/exoar/axon_tenax_engine) — cluster
  operations, explanation-quadrant docs (replay and determinism, multi-tenancy
  and trust model), CLI and admin API reference, and ADRs.
