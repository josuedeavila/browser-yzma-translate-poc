//go:build js && wasm

// Chat runs llama.cpp inference in a browser.
//
// Based on github.com/hybridgroup/yzma's examples/wasm/chat/main.go. It is
// intentionally generic (load a model, generate text from an arbitrary
// prompt) — the translation-specific system prompt lives in web/samples.js
// and is sent from the JavaScript side, so this file needs no
// translation-specific logic of its own. The one addition over the upstream
// example is generate()'s maxTokens==0 "priming" mode, which lets a caller
// reuse a fixed prompt prefix's KV-cache across calls (see its doc comment).
//
// Build it with the standard toolchain (no TinyGo required):
//
//	GOOS=js GOARCH=wasm go build -o web/vendor/yzma/yzma.wasm ./wasm
//
// Or with TinyGo, for a smaller binary:
//
//	tinygo build -target wasm -o web/vendor/yzma/yzma.wasm ./wasm
//
// See ../README.md for how the result is served.
package main

import (
	"fmt"
	"syscall/js"
	"time"

	"github.com/hybridgroup/yzma/pkg/llamawasm"
)

const modelPath = "/models/model.gguf"

var (
	model   llamawasm.Model
	ctx     llamawasm.Context
	vocab   llamawasm.Vocab
	sampler llamawasm.Sampler

	// prefixPrimed and prefixLen support reusing the KV-cache of a fixed
	// prompt prefix (the translation system prompt) across calls, so a
	// generate() call after the first only has to decode the short suffix
	// (the error JSON), not the whole prompt again. Set once by a
	// maxTokens==0 "priming" call to generate(), which also probes whether
	// MemorySeqRm is available on this llama.cpp build (ABI >=6) — if not,
	// prefixPrimed stays false forever and every call falls back to the
	// original behavior (full prompt, full MemoryClear). See ../README.md
	// "Performance sem GPU".
	prefixPrimed bool
	prefixLen    int32
)

func main() {
	if err := llamawasm.Load(""); err != nil {
		post("error", err.Error())
		return
	}

	llamawasm.LogSet(llamawasm.LogSilent())
	llamawasm.Init()

	// The page calls these.
	js.Global().Set("yzmaLoadModel", js.FuncOf(loadModel))
	js.Global().Set("yzmaOpenModel", js.FuncOf(openModel))
	js.Global().Set("yzmaGenerate", js.FuncOf(generate))

	post("ready", backendReport())

	// Keep the program alive so that the page can call into it.
	<-make(chan struct{})
}

// loadModel(url) gets a model over the network and makes a context for it.
func loadModel(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		post("error", "loadModel needs a URL")
		return nil
	}
	url := args[0].String()

	go func() {
		post("status", "downloading the model")

		err := llamawasm.FetchModelFile(modelPath, url, func(done, total int64) {
			if total > 0 {
				post("progress", fmt.Sprintf("%d%%", done*100/total))
			}
		})
		if err != nil {
			post("error", err.Error())
			return
		}

		open(modelPath)
	}()

	return nil
}

// openModel(path) loads a model that is already in the filesystem of the
// llama.cpp module. A test puts the file there itself.
func openModel(this js.Value, args []js.Value) any {
	path := modelPath
	if len(args) > 0 && args[0].Truthy() {
		path = args[0].String()
	}

	go open(path)

	return nil
}

// open loads the model at path and makes a context for it.
func open(path string) {
	post("status", "loading the model")

	// A freshly opened context has no primed prefix, even if a previous
	// context (before a reload) did.
	prefixPrimed = false
	prefixLen = 0

	params := llamawasm.ModelDefaultParams()

	// A WebGPU build has a device, thus put each layer on it. A CPU build has no
	// device and ignores this value.
	if llamawasm.GPUDevice() != "" {
		params.NGpuLayers = 999
	}

	var err error
	if model, err = llamawasm.ModelLoadFromFile(path, params); err != nil {
		post("error", err.Error())
		return
	}

	ctxParams := llamawasm.ContextDefaultParams()
	// generate() below decodes the whole prompt in a single BatchGetOne call
	// (unchunked), and this PoC's prompt is the full production system prompt
	// (with few-shot examples) plus the error JSON — a few hundred tokens more
	// than yzma's own chat demo assumes. NBatch must cover the whole prompt in
	// one call or llama.cpp aborts; NCtx must cover prompt + generated tokens.
	ctxParams.NCtx = 4096
	ctxParams.NBatch = 4096

	if ctx, err = llamawasm.InitFromModel(model, ctxParams); err != nil {
		post("error", err.Error())
		return
	}

	vocab = llamawasm.ModelGetVocab(model)

	post("loaded", llamawasm.ModelDesc(model)+", "+backendReport())
}

// backendReport gives the name of the backend that computes.
func backendReport() string {
	if device := llamawasm.GPUDevice(); device != "" {
		return fmt.Sprintf("backend: %s (%s)", llamawasm.Backend(), device)
	}
	return fmt.Sprintf("backend: %s, %d threads", llamawasm.Backend(), llamawasm.Threads())
}

// generate(prompt, maxTokens) makes text and sends each piece to the page.
//
// maxTokens<0 is a "priming" call: it decodes prompt as a fixed prefix,
// caches it (does not clear memory afterward), and returns without sampling
// anything. It is negative rather than 0 because the vendored worker.js
// routes this call through `message.maxTokens || 128` — 0 is falsy in
// JavaScript, so it would silently become 128 there before Go ever saw it;
// a negative number survives that `||` untouched. It also probes whether
// this llama.cpp build supports
// MemorySeqRm (ABI >=6) by calling it right away on the prefix it just
// decoded. If priming and the probe both succeed, every later call reuses
// that cached prefix — tokenizing and decoding only its own (much shorter)
// prompt, which by then holds just the suffix a caller appends after the
// primed prefix. If not, prefixPrimed stays false and every call keeps
// behaving exactly as before this PoC's performance pass: the whole prompt
// is tokenized and decoded from a cleared context each time.
func generate(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		post("error", "generate needs a prompt")
		return nil
	}
	prompt := args[0].String()

	// maxTokens==0 is a valid, meaningful argument (see the priming doc
	// comment above) — JS's 0 is falsy, so this must check args[1].Type()
	// and not args[1].Truthy(), or a caller could never actually prime.
	maxTokens := int32(128)
	if len(args) > 1 && args[1].Type() == js.TypeNumber {
		maxTokens = int32(args[1].Int())
	}

	go func() {
		if model == 0 || ctx == 0 {
			post("error", "load a model first")
			return
		}

		priming := maxTokens < 0

		addSpecial := true
		if priming {
			// A priming call always starts a fresh sequence.
			if err := llamawasm.MemoryClear(ctx, true); err != nil {
				post("error", err.Error())
				return
			}
			prefixPrimed = false
		} else if prefixPrimed {
			// Roll the KV-cache back to right after the primed prefix
			// instead of clearing it, so this call's Decode only has to
			// process the (short) suffix that follows the prefix.
			if ok, err := llamawasm.MemorySeqRm(ctx, 0, llamawasm.Pos(prefixLen), -1); err != nil || !ok {
				post("status", "prefix reuse unavailable, falling back to a full prompt")
				prefixPrimed = false
				if err := llamawasm.MemoryClear(ctx, true); err != nil {
					post("error", err.Error())
					return
				}
			} else {
				addSpecial = false
			}
		} else {
			if err := llamawasm.MemoryClear(ctx, true); err != nil {
				post("error", err.Error())
				return
			}
		}

		tokens := llamawasm.Tokenize(vocab, prompt, addSpecial, false)
		if len(tokens) == 0 {
			post("error", "the prompt has no tokens")
			return
		}

		batch := llamawasm.BatchGetOne(tokens)
		if _, err := llamawasm.Decode(ctx, batch); err != nil {
			post("error", err.Error())
			return
		}

		if priming {
			// Probe MemorySeqRm on the prefix just decoded. A no-op range
			// (p0==p1, nothing to remove) still exercises the call itself.
			// This posts a distinct kind (not "status") because it is a
			// terminal outcome of priming, same as "primed" — the page uses
			// it to know priming is over and a translation can safely start
			// (concurrent generate() goroutines would race on the shared ctx).
			n := int32(len(tokens))
			if ok, err := llamawasm.MemorySeqRm(ctx, 0, llamawasm.Pos(n), llamawasm.Pos(n)); err != nil || !ok {
				post("primeUnsupported", "older ABI, no MemorySeqRm")
				return
			}
			prefixLen = n
			prefixPrimed = true
			post("primed", fmt.Sprintf("%d tokens", n))
			return
		}

		if sampler != 0 {
			llamawasm.SamplerFree(sampler)
		}
		sampler = llamawasm.SamplerChainInit(llamawasm.SamplerChainDefaultParams())
		llamawasm.SamplerChainAdd(sampler, llamawasm.SamplerInitGreedy())

		buf := make([]byte, 64)
		start := time.Now()

		var count int32
		for count < maxTokens {
			token := llamawasm.SamplerSample(sampler, ctx, -1)
			if llamawasm.VocabIsEOG(vocab, token) {
				break
			}
			llamawasm.SamplerAccept(sampler, token)

			if n := llamawasm.TokenToPiece(vocab, token, buf, 0, true); n > 0 {
				post("token", string(buf[:n]))
			}

			count++
			if count >= maxTokens {
				// No further sample will read this decode's logits.
				break
			}
			batch = llamawasm.BatchGetOne([]llamawasm.Token{token})
			if _, err := llamawasm.Decode(ctx, batch); err != nil {
				post("error", err.Error())
				return
			}
		}

		elapsed := time.Since(start).Seconds()
		if elapsed > 0 {
			post("done", fmt.Sprintf("%d tokens, %.2f tokens/s", count, float64(count)/elapsed))
			return
		}
		post("done", fmt.Sprintf("%d tokens", count))
	}()

	return nil
}

// post sends a message to the container of this module. In a worker that is
// the page, and in Node it is the console.
func post(kind, text string) {
	message := map[string]any{"kind": kind, "text": text}

	if fn := js.Global().Get("postMessage"); fn.Type() == js.TypeFunction {
		fn.Invoke(message)
		return
	}
	if fn := js.Global().Get("yzmaOnMessage"); fn.Type() == js.TypeFunction {
		fn.Invoke(message)
		return
	}
	fmt.Printf("%s: %s\n", kind, text)
}
