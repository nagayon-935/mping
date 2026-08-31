BINARY := mping
BUILD_CMD := go build -o $(BINARY) ./cmd/main/

# Analyzer versions for `make lint`. CI pins neither (it tracks the actions'
# latest), so these default to latest to stay in step with it; override them
# to reproduce a specific CI run, e.g. make lint STATICCHECK_VERSION=2025.1.1
STATICCHECK_VERSION ?= latest
GOVULNCHECK_VERSION ?= latest

.PHONY: build install clean test coverage lint

build:
	$(BUILD_CMD)

install: build
	sudo ./install.sh

test:
	go test ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# lint runs the same four checks the CI workflow does, in the same order.
# gofmt is checked rather than applied — CI fails on unformatted files, so
# this has to fail the same way.
#
# Two things stop this from being an exact stand-in for the CI job, both in
# the direction of failing locally where CI passes:
#
#   - govulncheck reports against the toolchain that built the code, while
#     CI resolves `go-version: '1.26'` to the newest patch release. An older
#     local Go turns up standard-library advisories CI will never show —
#     upgrade the toolchain rather than ignoring the output.
#   - gofmt is pointed at the module's own package directories instead of
#     `.`, because plain `gofmt -l .` descends into dot-directories that the
#     other three checks skip (`./...` ignores them). Locally that means
#     .claude/worktrees/, which is git-ignored and absent from CI's clean
#     checkout, so scratch work in a worktree would otherwise fail the build.
lint:
	@fmt_out="$$(gofmt -l $$(go list -f '{{.Dir}}' ./...))"; \
	if [ -n "$$fmt_out" ]; then \
		echo "The following files are not gofmt'ed:"; \
		echo "$$fmt_out"; \
		exit 1; \
	fi
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

clean:
	rm -f $(BINARY) coverage.out
