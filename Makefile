BINARY := mping
BUILD_CMD := go build -o $(BINARY) ./cmd/main/

.PHONY: build install clean

build:
	$(BUILD_CMD)

install: build
	sudo chown root:wheel $(BINARY)
	sudo chmod u+s $(BINARY)

clean:
	rm -f $(BINARY)
