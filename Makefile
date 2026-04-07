BINARY := mping
BUILD_CMD := go build -o $(BINARY) ./cmd/main/

.PHONY: build install clean test coverage

build:
	$(BUILD_CMD)

install: build
	sudo ./install.sh

test:
	go test ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

clean:
	rm -f $(BINARY) coverage.out
