BIN ?= $(HOME)/.local/bin/ktop

test:
	go vet ./...
	go test ./...

build:
	go build -o dist/ktop ./cmd/ktop

install: build
	install -m 0755 dist/ktop $(BIN)

.PHONY: test build install
