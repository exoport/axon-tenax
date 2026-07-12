# Changelog

All notable changes to the Tenax SDK are documented in this file. Entries describe what is
actually built, not what is planned.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The SDK is versioned in
lockstep with the [Tenax engine](https://github.com/exoar/axon_tenax_engine) release it targets.

## [0.1.3] - 2026-07-12

### Added

- **Keyed-Workflow dispatch verbs** (`sdk.Context`): `CallWorkflow(name, key string, req []byte)
  ([]byte, error)` and `SendWorkflow(name, key string, req []byte) (string, error)` — mirror
  `ctx.Call`/`ctx.Send` in shape but carry a Workflow `key`, letting handler code start and await a
  keyed child Workflow (Story 56.1, ADR-0046, CR-20). Dispatch is run-once-per-key **attach**: a
  second dispatch to the same `(name, key)` attaches to the single run-once instance rather than
  starting a second run. An awaited `CallWorkflow` on a `COMPLETED` key returns the **recorded**
  result; on a terminal `FAILED`/`KILLED`/`CANCELLED` key it surfaces the **recorded** terminal
  error. `SendWorkflow` to a terminal key is a **no-op** that returns the existing invId. No
  `internal/` import added (ADR-0028/0045 boundary preserved); the engine-side keyed dispatch wiring
  that makes these verbs reachable on the live path lands in the engine's Story 56.2 (`require` bump
  to this tag).
- **`sdk.Serve` turnkey worker-serve surface — now FUNCTIONAL** (`sdk`): `Serve(ctx
  context.Context, nc *nats.Conn, reg *Registry, opts ...ServeOption) error` plus the
  v1-committed options `WithConcurrency(n int)`, `WithDrainTimeout(d time.Duration)`, and
  `WithWorkerName(name string)` (defaults to `os.Hostname()` when unset) — the frozen,
  Cortex-ACKed worker-side surface a separately-deployed SDK worker binary (its own process, its
  own Go module) calls to consume Workflow dispatches over NATS (Story 57.1 surface, Story 59.1
  body, ADR-0047, CR-21). `Serve` takes an **explicit `*Registry`** — never the package-level
  `GlobalRegistry()` singleton — and is scoped to the **Interpreter only**. `Serve` now genuinely
  **advertises** the registered `(serviceName, handlerName)` pairs to `tenaxd` via periodic
  `WorkerAnnouncement` heartbeats, **binds a durable pull consumer** per pair on the shared
  remote-dispatch work queue (`WithConcurrency(n)` binds directly to the consumer's
  `MaxAckPending` — Pin #1), and **consumes, executes, and replies** to real dispatches: it acks
  each pulled message only *after* computing the handler result and publishing the reply (Pin #2
  ack-after-journal ordering — `tenaxd` journals the terminal entry from the reply before its own
  dispatch returns), so a worker SIGKILLed mid-dispatch leaves the invocation un-acked and
  JetStream redelivers it to **any** live worker on the same durable consumer — never pinned to
  the dead process, the same exactly-once guarantee as `--runtime inproc`. `WithDrainTimeout`
  bounds how long `Serve` waits for in-flight dispatches to finish after `ctx` is cancelled before
  stopping. The wire envelopes (`RemoteDispatchRequest`/`Response`, `WorkerAnnouncement`/
  `HandlerRef`) and substrate names are re-derived SDK-pure — byte-identical JSON tags, RFC 8785
  JCS via `github.com/gowebpki/jcs` — conforming to the engine's frozen remote-dispatch corpus; no
  `internal/` import added (ADR-0028/0045 boundary preserved, zero-imports boundary test green).
  A handler dispatched via `Serve` receives a minimal `sdk.Context` whose `ctx.*` durable
  operations (`Run`/`Sleep`/`Get`/`Set`/`Call`/...) return a clear, documented
  `ErrRemoteContextUnsupported` — the single-request/response model (a full remote `ctx.*`
  durable-primitive bridge is a documented v1 boundary, not yet built) (Story 59.1).

### Changed

- **`CallWorkflow`/`SendWorkflow` doc-comments** (`sdk/ctx.go`): extended to state the in-flight
  multi-registrant attach behavior — a second (and Nth) **IN-FLIGHT** caller to the same
  `(name, key)`, dispatched while the callee is still `RUNNING`, attaches and (for an awaited
  `CallWorkflow`) receives the same **recorded** terminal result once the callee terminates (no
  permanent hang), consistent with the already-`COMPLETED` case documented above. `SendWorkflow`
  stays fire-and-forget; the doc-comment clarifies that any caller which does await via
  `CallWorkflow` gets the same recorded-result guarantee (Story 58.1, ADR-0048 — the engine-side
  durable multi-registrant completion fan-out; hardens ADR-0046). Verb signatures are unchanged —
  doc-comment-only.

## [0.1.2] - 2026-07-08

### Changed

- **Lint config**: replaced the engine-derived `.golangci.yml` with the company-standard
  `.golangci.yaml` (`default: all` minus the standard disable set), with **no** SDK-specific config
  carve-outs. The few linters that don't fit the SDK's design are handled with inline `//nolint`
  directives at the call sites instead: `interfacebloat` on the `ctx.*` `Context` interface,
  `contextcheck` on the durable-path fat shim, and `testpackage`/`noctx`/`nilnil` on white-box test
  files. The lone `goconst` finding was resolved with a shared test constant. Lints clean.

## [0.1.1] - 2026-07-08

### Added

- **Tooling**: `Makefile` + bingo-pinned Go tools (golangci-lint, govulncheck, gofumpt,
  goimports, betteralign, gotestsum, gomajor) with `make lint` / `fmt` / `vuln` / `audit` /
  `tools` / `toolsupdate`, and this changelog.

### Changed

- Struct fields reordered for memory efficiency (betteralign). No API change; the durable/wire
  path is encoded with JCS canonical form (RFC 8785, sorted keys), so struct field order does not
  affect serialized bytes there. Full test suite re-verified green.

## [0.1.0] - 2026-07-08

First release. Extracted into its own public module from the Tenax engine monorepo; the module
boundary now structurally guarantees the SDK imports zero engine internals (ADR-0028).

### Added

- **Handler authoring** (`sdk`): stateless Services, keyed Virtual Objects, and Workflows
  (run-once / query / signal), plus handler registration.
- **The `ctx.*` durable surface**: `ctx.Run` effect-once side effects; `ctx.Now` / `ctx.Rand` /
  `ctx.UUID` determinism helpers; `ctx.Get` / `Set` / `Clear` / `List` state; `ctx.Sleep` and a
  raceable `ctx.Timer`; awakeables and durable promises; request-response, fire-and-forget, and
  delayed sends; call-tree cancellation; saga compensation stacks; the promise combinators
  `Race` / `AwaitAny` / `AwaitAll` / `AwaitFirst`; and `ctx.GetVersion` feature-pinning.
- **Provisioning helpers** (`sdk/provision`): programmatic provisioning.
- **Self-contained module**: the only external dependency is `github.com/nats-io/nats.go`;
  builds standalone via `go get github.com/exoport/axon-tenax/sdk`.
- **License**: Apache License 2.0.

[0.1.2]: https://github.com/exoport/axon-tenax/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/exoport/axon-tenax/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/exoport/axon-tenax/releases/tag/v0.1.0
