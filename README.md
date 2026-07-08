# axon-tenax (Axon Tenax SDK)

The public Go SDK for [Tenax](https://github.com/exoar/axon_tenax_engine) — a
durable-execution engine built natively on NATS.

You write ordinary Go functions (Services, Virtual Objects, Workflows) that survive
process crashes, restarts, and infrastructure failures. This module is the only
public, importable surface; the engine internals live in a separate (private) repo.

## Install

```bash
go get github.com/exoport/axon-tenax/sdk@latest
```

```go
import "github.com/exoport/axon-tenax/sdk"
```

## Packages

- `sdk` — handler authoring, the `ctx.*` durable surface, Services / Virtual Objects / Workflows, sagas, promises, combinators.
- `sdk/provision` — programmatic provisioning helpers.

## Docs

Author-facing documentation lives under [`docs/`](docs/index.md): tutorials, how-to
guides, and the [`ctx.*` reference](docs/reference/sdk-context.md). See the
[docs index](docs/index.md) for the full list. Engine/operations docs (cluster,
release, metrics, contract) live in the engine repo.

## Versioning

This module is versioned in lockstep with the Tenax engine release it targets.

## Development

Go development tools are version-pinned with [bingo](https://github.com/bwplotka/bingo)
(`.bingo/*.mod`). Common targets:

```shell
make tools        # install the pinned tools (golangci-lint, govulncheck, gofumpt, ...)
make test         # go test ./...
make lint         # golangci-lint run
make fmt          # gofumpt + goimports
make vuln         # govulncheck ./...
make audit        # go mod verify + govulncheck + lint + betteralign
make toolsupdate  # bump + re-pin all tools (review the diff)
```

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
