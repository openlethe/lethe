.PHONY: all build build-all test vet fmt lint clean docker release

BINARY := lethe
LDFLAGS := -s -w

# Default build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/lethe

# Cross-platform builds
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

build-all: clean
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1); \
		GOARCH=$$(echo $$platform | cut -d/ -f2); \
		OUTPUT=dist/$(BINARY)-$$GOOS-$$GOARCH; \
		if [ "$$GOOS" = "windows" ]; then OUTPUT=$$OUTPUT.exe; fi; \
		echo "Building $$platform -> $$OUTPUT"; \
		GOOS=$$GOOS GOARCH=$$GOARCH CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $$OUTPUT ./cmd/lethe; \
	done
	@echo "Done. Binaries in dist/"

# Release: build all + create checksums
release: build-all
	@cd dist && shasum -a 256 * > checksums.txt
	@echo "Release artifacts ready in dist/"

test:
	go test -race -v ./...

vet:
	go vet ./...

fmt:
	gofmt -w -s .

lint: fmt vet

run:
	go run ./cmd/lethe serve

clean:
	rm -rf dist/
	rm -f $(BINARY)

docker:
	docker build -t $(BINARY):latest .

.DEFAULT_GOAL := build
