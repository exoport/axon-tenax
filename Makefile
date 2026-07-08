.PHONY: build test test-pretty lint fmt vuln align alignfix audit tools toolsupdate clean

# Bingo-pinned Go tool versions (golangci-lint, govulncheck, gofumpt, goimports,
# betteralign, gotestsum, gomajor). Managed by https://github.com/bwplotka/bingo;
# pins live in .bingo/*.mod. `make tools` installs them; `make toolsupdate`
# bumps + re-pins them.
include .bingo/Variables.mk

# build compile-checks all packages.
build:
	go build ./...

# test runs the unit suite.
test:
	go test ./...

# test-pretty runs the unit suite with gotestsum's readable pkgname output.
test-pretty: $(GOTESTSUM)
	$(GOTESTSUM) --format pkgname -- ./...

# lint runs the bingo-pinned golangci-lint via .golangci.yml.
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

# fmt formats the tree with gofumpt then goimports (matches the .golangci.yaml
# formatters block, which uses goimports with no local-prefix).
fmt: $(GOFUMPT) $(GOIMPORTS)
	$(GOFUMPT) -l -w .
	$(GOIMPORTS) -w .

# vuln scans the module for known vulnerabilities (govulncheck).
vuln: $(GOVULNCHECK)
	$(GOVULNCHECK) ./...

# align reports struct field-alignment/padding opportunities (betteralign).
align: $(BETTERALIGN)
	$(BETTERALIGN) ./...

# alignfix applies betteralign's struct field reordering in place.
alignfix: $(BETTERALIGN)
	$(BETTERALIGN) -apply ./...

# audit runs the aggregate quality+security gate. Hard gates (fail the target):
# module checksum verification, vulnerability scan, and lint. The betteralign
# struct-alignment pass is advisory (leading `-` ignores its non-zero exit) —
# run `make align` / `make alignfix` to act on it.
audit: $(GOVULNCHECK) $(BETTERALIGN) lint
	go mod verify
	$(GOVULNCHECK) ./...
	-$(BETTERALIGN) ./...

# tools installs every bingo-pinned Go tool into $(GOBIN) (idempotent).
tools: $(GOLANGCI_LINT) $(GOVULNCHECK) $(GOFUMPT) $(GOIMPORTS) $(BETTERALIGN) $(GOTESTSUM) $(GOMAJOR)
	@echo "bingo-pinned tools installed in $(GOBIN)"

# toolsupdate bumps every bingo-pinned tool to its latest release and re-pins
# it (rewrites .bingo/*.mod + *.sum + Variables.mk). Review + commit the diff.
toolsupdate: $(BINGO)
	@$(BINGO) list | tail -n +3 | awk '{print $$1}' | xargs -tI % sh -c '$(BINGO) get -l %@latest'

# clean removes build/test artifacts.
clean:
	go clean ./...
