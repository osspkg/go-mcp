SHELL=/bin/bash

# Some managed Go installations keep gofmt in GOROOT/bin without exporting
# that directory. goppy invokes gofmt directly, so make the toolchain complete
# for every quality target.
GO_BIN_DIR := $(shell go env GOROOT)/bin
export PATH := $(GO_BIN_DIR):$(PATH)


.PHONY: install
install:
	go install go.osspkg.com/goppy/v3/cmd/goppy@latest
	goppy setup-lib

.PHONY: lint
lint:
	goppy lint

.PHONY: license
license:
	goppy license

.PHONY: build
build:
	goppy build --arch=amd64

.PHONY: tests
tests:
	goppy test

.PHONY: pre-commit
pre-commit: install license lint tests build

.PHONY: ci
ci: pre-commit
