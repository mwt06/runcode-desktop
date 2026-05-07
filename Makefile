APP := runcode
MAIN := ./cmd/runcode
BIN_DIR := bin

.PHONY: all build run test lint fmt tidy clean snapshot

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN_DIR)/$(APP) $(MAIN)

run:
	go run $(MAIN) chat

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w $$(go list -f '{{.Dir}}' ./...)

tidy:
	go mod tidy

snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html
