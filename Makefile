BINARY := aiswap
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test vet fmt clean

build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/aiswap

install: build
	@mkdir -p $(HOME)/.local/bin
	cp bin/$(BINARY) $(HOME)/.local/bin/.$(BINARY).tmp
	mv -f $(HOME)/.local/bin/.$(BINARY).tmp $(HOME)/.local/bin/$(BINARY)

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf bin dist
