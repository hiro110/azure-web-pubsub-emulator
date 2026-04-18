BINARY     := web-pubsub-emulator
CMD        := ./cmd/emulator
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -ldflags "-s -w -X main.version=$(VERSION)"

PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
DIST       := dist

.PHONY: build test test-verbose test-race lint clean docker-build docker-push run release

build:
	mise exec go -- go build $(LDFLAGS) -o $(BINARY) $(CMD)

test:
	mise exec go -- go test ./...

test-verbose:
	mise exec go -- go test -v ./...

test-race:
	mise exec go -- go test -race ./...

lint:
	mise exec go -- go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

docker-push: docker-build
	docker push $(BINARY):$(VERSION)

run: build
	./$(BINARY) --config config.yaml

# Cross-compile binaries for all target platforms into dist/
release:
	mkdir -p $(DIST)
	$(foreach PLATFORM,$(PLATFORMS), \
		$(eval GOOS   := $(word 1,$(subst /, ,$(PLATFORM)))) \
		$(eval GOARCH := $(word 2,$(subst /, ,$(PLATFORM)))) \
		$(eval EXT    := $(if $(filter windows,$(GOOS)),.exe,)) \
		mise exec go -- env GOOS=$(GOOS) GOARCH=$(GOARCH) \
			go build $(LDFLAGS) \
			-o $(DIST)/$(BINARY)-$(VERSION)-$(GOOS)-$(GOARCH)$(EXT) \
			$(CMD); \
	)
