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

# lint runs the same four checks the CI workflow does, in the same order, so
# a green `make lint` locally means the lint job won't be the thing that
# turns the build red. gofmt is checked rather than applied — CI fails on
# unformatted files, so this has to fail the same way.
lint:
	@fmt_out="$$(gofmt -l .)"; \
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
