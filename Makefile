BINARY := teltonika-sim
ifeq ($(OS),Windows_NT)
BINARY := teltonika-sim.exe
endif

GO ?= go
PORT ?= 5027
DEVICES ?= 10
DURATION ?= 60s

.PHONY: all build test vet fmt check run-local demo clean

all: build

build:
	$(GO) build -trimpath -o $(BINARY) ./cmd/teltonika-sim

test:
	$(GO) test ./... -count=1

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

check: vet
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt found unformatted files" && exit 1)
	$(GO) test ./... -count=1

run-local: build
	PORT=$(PORT) DEVICES=$(DEVICES) DURATION=$(DURATION) bash scripts/run-local.sh

demo: build
	PORT=$(PORT) DEVICES=$(DEVICES) DURATION=$(DURATION) \
		RUN_ARGS="--batch 5 --interval 2s --fragment" bash scripts/run-local.sh

clean:
	rm -f $(BINARY) serve.log run.log
