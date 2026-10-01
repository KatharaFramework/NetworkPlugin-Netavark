VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLUGDIR  ?= $(HOME)/.local/libexec/netavark-plugins

.PHONY: build install clean test

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o bin/katharanp_vde ./cmd/katharanp_vde
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/katharanp ./cmd/katharanp

install: build
	install -D -m 755 bin/katharanp_vde $(PLUGDIR)/katharanp_vde
	install -D -m 755 bin/katharanp $(PLUGDIR)/katharanp

clean:
	rm -rf bin

test:
	go test ./...