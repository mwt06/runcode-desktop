APP := runcode
MAIN := ./cmd/runcode
BIN_DIR := bin

# Two Go modules share this repo: the root (CLI/TUI + desktop core) and the
# nested engine module. Targets fan out over both; the Wails desktop shell
# (cmd/runcode-desktop) builds via `wails build`, not from here.
MODULES := . engine

.PHONY: all build run test lint fmt tidy audit clean snapshot

all: build

build:
	@mkdir -p $(BIN_DIR)
	go -C engine build ./...
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

# The engine module must never depend on the root module (ports live in the
# engine, implementations in the shells). Empty output = clean.
audit:
	@bad=$$(go -C engine list -deps ./... | grep 'wt68/runcode' | grep -v 'wt68/runcode/engine'); \
	if [ -n "$$bad" ]; then echo "engine depends on root module:"; echo "$$bad"; exit 1; fi
	@echo "dependency direction: clean"

snapshot:
	goreleaser build --snapshot --clean

clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html
