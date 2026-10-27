.PHONY: build test check fmt vet lint install snapshot docs docs-check

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

# docs regenerates the site's command reference from the cobra tree;
# docs-check is what CI runs, failing when the committed pages are stale.
docs:
	go run ./cmd/docsgen

docs-check: docs
	git diff --exit-code -- docs-site/content/docs/reference
	test -z "$$(git ls-files --others --exclude-standard -- docs-site/content/docs/reference)"
