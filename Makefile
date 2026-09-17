.PHONY: build test check
build:
	go build -o bin/brp ./cmd/brp
test:
	go test -race ./...
check: test
	go vet ./...
