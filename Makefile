APP := runcode
MAIN := ./cmd/runcode
BIN_DIR := bin

# Three Go modules share this repo: the root (CLI/TUI + desktop core), the
# server skeleton, and the Wails desktop shell. The engine lives in its own
# repository (gitlab.ouc-online.com.cn/aibase/agentloop), consumed through
# each go.mod's replace pointing at a sibling checkout ../agentloop. The
# desktop shell packages via `wails build`; here it only gets a Go-side
# compile check.
MODULES := . cmd/runcode-server

.PHONY: all build run test lint fmt tidy clean snapshot

all: build

build:
	@mkdir -p $(BIN_DIR)
	go -C cmd/runcode-server build ./...
	go -C cmd/runcode-desktop build ./...
	go build -trimpath -o $(BIN_DIR)/$(APP) $(MAIN)

run:
	go run $(MAIN) chat

test:
	@for m in $(MODULES); do go -C $$m test -race ./... || exit 1; done

lint:
	@for m in $(MODULES); do (cd $$m && golangci-lint run ./...) || exit 1; done

fmt:
	@for m in $(MODULES); do gofmt -w $$(go -C $$m list -f '{{.Dir}}' ./...); done

tidy:
	@for m in $(MODULES); do go -C $$m mod tidy || exit 1; done
	go -C cmd/runcode-desktop mod tidy

snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html
