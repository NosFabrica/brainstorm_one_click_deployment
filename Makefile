BINARY := brainstorm
PREFIX ?= /usr/local
INSTALL_DIR := $(PREFIX)/bin

.PHONY: build install uninstall tidy clean

build:
	go build -o bin/$(BINARY) ./cmd/brainstorm

install: build
	install -m 0755 bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "installed $(INSTALL_DIR)/$(BINARY)"

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

tidy:
	go mod tidy

clean:
	rm -rf bin
