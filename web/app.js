// app.js drives the translator UI: it owns the Web Worker that runs
// yzma/WASM (entirely in the browser) and renders the result. See
// wasm/main.go and web/vendor/yzma/{worker.js,yzma-loader.js} for the local
// inference side.

const modelUrlInput = document.getElementById("modelUrl");
const sampleSelect = document.getElementById("sample");
const errorJsonInput = document.getElementById("errorJson");
const loadModelBtn = document.getElementById("loadModelBtn");
const runBtn = document.getElementById("runBtn");
const modelStatus = document.getElementById("modelStatus");

const localStatus = document.getElementById("localStatus");
const localOutput = document.getElementById("localOutput");
const localTiming = document.getElementById("localTiming");

let currentSample = SAMPLES[0];
let modelReady = false;
let generating = false;
let localGenStart = 0;
let localRawText = "";
// Set once the worker confirms the system-prompt prefix is primed and cached
// (see wasm/main.go's maxTokens<0 "priming" path and its MemorySeqRm probe).
// While false, every call sends the full prompt (system prompt + suffix), the
// original/fallback behavior.
let prefixPrimed = false;

SAMPLES.forEach((sample, idx) => {
  const opt = document.createElement("option");
  opt.value = String(idx);
  opt.textContent = sample.name;
  sampleSelect.appendChild(opt);
});

function applySample(sample) {
  currentSample = sample;
  errorJsonInput.value = JSON.stringify(sample.error, null, 2);
}
applySample(SAMPLES[0]);

sampleSelect.addEventListener("change", () => {
  applySample(SAMPLES[Number(sampleSelect.value)]);
});

// --- Web Worker: runs llama.cpp + the Go program compiled to WASM. ---
// ?mode=cpu / ?mode=webgpu on this page's own URL is forwarded to the worker,
// same as yzma's own wasm/index.html demo, so a reader can force the CPU
// build to rule out a GPU backend that computes wrong values (see README
// "Troubleshooting" — Vulkan in Chrome on Linux).
const workerMode = new URLSearchParams(location.search).get("mode");
const worker = new Worker(
  "vendor/yzma/worker.js" + (workerMode ? "?mode=" + encodeURIComponent(workerMode) : ""),
);

worker.onmessage = (event) => {
  const { kind, text } = event.data || {};
  switch (kind) {
    case "ready":
      modelStatus.textContent = "worker pronto (" + text + ") — clique em \"Carregar modelo\"";
      break;
    case "status":
      modelStatus.textContent = text;
      break;
    case "progress":
      modelStatus.textContent = "baixando modelo: " + text;
      break;
    case "loaded":
      modelReady = true;
      modelStatus.textContent = "modelo carregado: " + text + " — preparando prefixo…";
      loadModelBtn.disabled = false;
      loadModelBtn.textContent = "Recarregar modelo";
      // runBtn stays disabled until priming (below) reaches a terminal state
      // ("primed" or "primeUnsupported") — generate() runs in a Go goroutine,
      // so a translation started before priming's own goroutine finishes
      // would race it on the shared llama.cpp context.
      primePrefix();
      break;
    case "primed":
      prefixPrimed = true;
      modelStatus.textContent = "modelo carregado, prefixo cacheado (" + text + ")";
      runBtn.disabled = false;
      break;
    case "primeUnsupported":
      modelStatus.textContent = "modelo carregado (sem reuso de prefixo: " + text + ")";
      runBtn.disabled = false;
      break;
    case "token":
      if (generating) {
        localRawText += text;
        localOutput.textContent = localRawText;
      }
      break;
    case "done":
      generating = false;
      runBtn.disabled = false;
      // The system prompt asks for one short sentence. A small/base model
      // sometimes keeps going past that (e.g. hallucinating a further
      // "Input:/Output:" turn) instead of emitting an end-of-generation
      // token — trim the display to the first line, which is the answer.
      localOutput.textContent = (localRawText.split("\n")[0] || localRawText).trim();
      localTiming.textContent =
        "latência de geração: " + Math.round(performance.now() - localGenStart) + " ms — " + text;
      localStatus.textContent = "concluído";
      break;
    case "error":
      loadModelBtn.disabled = false;
      // Only re-enable "Traduzir" if a model is actually loaded — this error
      // might be the model load itself failing, not a translation.
      runBtn.disabled = !modelReady;
      generating = false;
      modelStatus.textContent = "erro: " + text;
      modelStatus.classList.add("error");
      localStatus.textContent = "erro: " + text;
      localStatus.classList.add("error");
      break;
    default:
      modelStatus.textContent = kind + ": " + text;
  }
};

loadModelBtn.addEventListener("click", () => {
  modelReady = false;
  prefixPrimed = false;
  runBtn.disabled = true;
  loadModelBtn.disabled = true;
  modelStatus.classList.remove("error");
  modelStatus.textContent = "iniciando download do modelo…";
  worker.postMessage({ kind: "load", url: modelUrlInput.value });
});

// primePrefix asks the worker to decode+cache SYSTEM_PROMPT once, so every
// translation after this only has to decode the short suffix. maxTokens is
// -1, not 0: worker.js (vendored, unmodified) forwards this as
// `message.maxTokens || 128`, and 0 is falsy in JS — it would silently
// become a real 128-token generation instead of a priming call. -1 survives
// that `||` and wasm/main.go treats any maxTokens<0 as "priming" (see its
// doc comment). If the llama.cpp build is too old for the MemorySeqRm call
// priming needs, the worker posts "primeUnsupported" and prefixPrimed stays
// false — runLocal() below then falls back to the full prompt every time,
// exactly as before this optimization.
function primePrefix() {
  worker.postMessage({ kind: "generate", prompt: SYSTEM_PROMPT, maxTokens: -1 });
}

runBtn.addEventListener("click", () => {
  let errorObj;
  try {
    errorObj = JSON.parse(errorJsonInput.value);
  } catch (err) {
    alert("JSON do erro inválido: " + err.message);
    return;
  }

  runLocal(errorObj);
});

function runLocal(errorObj) {
  if (!modelReady) {
    localStatus.textContent = "carregue o modelo primeiro";
    return;
  }

  // Disabled for the whole generation, not just while a "done"/"error" is
  // pending: generate() runs in a Go goroutine, and a second click before it
  // finishes would race the first call on the shared llama.cpp context.
  runBtn.disabled = true;
  localStatus.classList.remove("error");
  localStatus.textContent = "gerando…";
  localOutput.textContent = "";
  localTiming.textContent = "";
  localRawText = "";
  generating = true;
  localGenStart = performance.now();

  // Once the prefix is primed (see primePrefix()), only the short suffix
  // needs to be sent/decoded — the system prompt's KV-cache is reused. If
  // priming never succeeded (older llama.cpp WASM build), send the full
  // prompt every time, exactly as before.
  const prompt = prefixPrimed ? buildSuffix(errorObj) : buildPrompt(errorObj);
  // The system prompt asks for one short sentence (~10-25 tokens), and this
  // is a raw completion (no chat template) so a small/mid model does not
  // reliably emit an end-of-generation token — left uncapped it burns the
  // whole budget hallucinating a fake continuation of the few-shot examples.
  // Capping this low is the single biggest lever on wall-clock time without a
  // GPU: it bounds worst case latency regardless of tokens/s.
  worker.postMessage({ kind: "generate", prompt, maxTokens: 40 });
}
