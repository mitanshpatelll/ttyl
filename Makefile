VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test install clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/dmz ./cmd/dmz

test:
	go vet ./...
	go test ./...

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/dmz

clean:
	rm -rf bin
