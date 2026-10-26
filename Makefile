.PHONY: build test check fmt vet lint install snapshot

build:
	go build -o bin/booksy ./cmd/booksy

test:
	go test ./... -count=1

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

check: vet lint test
	test -z "$$(gofmt -l .)"

install:
	go install -ldflags "-X main.version=$$(git describe --tags --always --dirty)" ./cmd/booksy

# snapshot builds every release archive locally into dist/, without publishing.
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
