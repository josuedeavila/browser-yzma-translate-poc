# Prerequisites: Go (any recent version; TinyGo is optional, see build-wasm-tinygo).
#
# Quick start:
#   make deps            # go mod tidy
#   make vendor-yzma      # fetch yzma-loader.js/worker.js/wasm_exec.js + the
#                         # prebuilt llama.cpp WASM build into web/vendor/yzma
#   make build-wasm       # compile wasm/main.go -> web/vendor/yzma/yzma.wasm
#   make serve            # run devserver on :8090
#
# Then open http://localhost:8090.

YZMA_VERSION ?= v1.27.0
VENDOR_DIR := web/vendor/yzma
CLONE_DIR := /tmp/browser-yzma-poc-src

.PHONY: deps
deps:
	go mod tidy
	GOOS=js GOARCH=wasm go mod tidy

# vendor-yzma clones yzma at the version pinned in go.mod (must match, or the
# llama.cpp WASM ABI and the Go bindings drift apart — see README
# "Troubleshooting"), copies its JS glue, builds its `yzma` CLI, and uses that
# CLI to download the matching prebuilt llama.cpp WASM libraries. TinyGo
# cannot build the C++ of llama.cpp itself, so this is the supported way to
# get it, not a `go build`.
.PHONY: vendor-yzma
vendor-yzma:
	rm -rf $(CLONE_DIR)
	git clone --depth 1 --branch $(YZMA_VERSION) https://github.com/hybridgroup/yzma $(CLONE_DIR)
	cp $(CLONE_DIR)/wasm/yzma-loader.js $(CLONE_DIR)/wasm/worker.js $(VENDOR_DIR)/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(VENDOR_DIR)/
	cd $(CLONE_DIR) && go build -o /tmp/yzma-cli .
	/tmp/yzma-cli install -lib $(VENDOR_DIR) -os wasm -q

# build-wasm compiles this PoC's Go WASM entry with the standard toolchain —
# no TinyGo required. The binary is a few MB larger than a TinyGo build, which
# does not matter next to a several-hundred-MB model download.
.PHONY: build-wasm
build-wasm:
	GOOS=js GOARCH=wasm go build -o $(VENDOR_DIR)/yzma.wasm ./wasm

# build-wasm-tinygo is the smaller-binary alternative, if TinyGo is installed
# (`tinygo version`). Not required for the PoC to work.
.PHONY: build-wasm-tinygo
build-wasm-tinygo:
	tinygo build -target wasm -o $(VENDOR_DIR)/yzma.wasm ./wasm

.PHONY: serve
serve: build-wasm
	go run ./devserver -port 8090

# serve-single-thread runs a second instance on :8091 with no COOP/COEP, so
# yzma-loader.js is forced onto the single-threaded CPU build. Compare its
# tokens/s (?mode=cpu on that port) against :8090's multi-threaded one to
# check whether the thread pool is helping or thrashing on this host — see
# README "Performance sem GPU".
.PHONY: serve-single-thread
serve-single-thread: build-wasm
	go run ./devserver -port 8091 -isolate=false

.PHONY: clean
clean:
	rm -f $(VENDOR_DIR)/yzma.wasm
	rm -rf $(CLONE_DIR)
