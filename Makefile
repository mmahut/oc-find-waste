BINARY := oc-find-waste
PKG    := ./...

.PHONY: build test lint run

build:
	go build -o $(BINARY) ./cmd/oc-find-waste

test:
	go test $(PKG)

lint:
	gofmt -l .
	go vet $(PKG)

run:
	go run ./cmd/oc-find-waste scan -v
