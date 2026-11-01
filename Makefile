BIN := cronsoul

.PHONY: dev dev-file build test race vet fmt check

# Zero-config run: dev mode derives a secret key and keeps everything in memory.
dev:
	go run ./cmd/cronsoul

# Same, but keeps data on disk so jobs and channels survive a restart.
dev-file:
	CRONSOUL_DB=./cronsoul.db go run ./cmd/cronsoul

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X main.buildVersion=$(VERSION)" -o $(BIN) ./cmd/cronsoul

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Everything CI enforces, runnable locally in one shot.
check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go test -count=1 ./...
	go test -race -count=1 ./...
	CGO_ENABLED=0 go build -o /tmp/cronsoul-check ./cmd/cronsoul
	node --check cmd/cronsoul/ui/app.js
	git diff --check
