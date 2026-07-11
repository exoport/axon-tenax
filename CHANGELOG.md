# Changelog

All notable changes to the Tenax SDK are documented in this file. Entries describe what is
actually built, not what is planned.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The SDK is versioned in
lockstep with the [Tenax engine](https://github.com/exoar/axon_tenax_engine) release it targets.

## [0.1.3] - PREPARED, not yet tagged

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
