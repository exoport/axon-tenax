# Changelog

All notable changes to the Tenax SDK are documented in this file. Entries describe what is
actually built, not what is planned.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The SDK is versioned in
lockstep with the [Tenax engine](https://github.com/exoar/axon_tenax_engine) release it targets.

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

[0.1.1]: https://github.com/exoport/axon-tenax/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/exoport/axon-tenax/releases/tag/v0.1.0
