.PHONY: test vet cover bench install clean

GO ?= go
BIN = bin/wsitools

test:
	$(GO) test ./... -race -count=1

vet:
	$(GO) vet ./...

cover:
	$(GO) test ./... -race -count=1 -coverprofile=coverage.txt -covermode=atomic
	$(GO) tool cover -func=coverage.txt | tail -1

bench:
	$(GO) test ./tests/bench/... -bench=. -benchmem -run=^$$

install:
	$(GO) install -tags nohtj2k ./cmd/wsitools

build:
	$(GO) build -tags nohtj2k -o $(BIN) ./cmd/wsitools

clean:
	rm -rf bin/ coverage.txt
