# browser-yzma-translate-poc

PoC: tradutor de erros técnicos de plataforma (marketplace) para frases
simples em português, rodando **inteiramente no navegador** — sem servidor,
sem chamada de API — via [`hybridgroup/yzma`](https://github.com/hybridgroup/yzma),
que compila `llama.cpp` para WebAssembly.

## Como funciona

```
page (index.html)
      │ postMessage
Web Worker (web/vendor/yzma/worker.js)
   │                    \
Go program               llama.cpp module
(wasm/main.go,      -->  (build pré-compilado, baixado
 GOOS=js GOARCH=wasm)     via `make vendor-yzma`)
```

`wasm/main.go` é baseado no exemplo oficial `examples/wasm/chat` do yzma:
carrega um modelo GGUF e expõe `generate(prompt, maxTokens)` para o JS. Não há
lógica de tradução no Go — o `web/app.js` monta o prompt (system prompt +
JSON do erro, ver `web/samples.js`) e manda como texto livre para
`generate()`. Isso mantém o código simples: qualquer mudança no prompt é só
JS, sem recompilar wasm.

A única lógica adicionada sobre o exemplo original é um modo de "priming"
(`generate(prompt, maxTokens<0)`) que decodifica e cacheia um prefixo fixo (o
system prompt) uma vez, para que as chamadas seguintes só precisem decodificar
o sufixo curto (o JSON do erro) — ver §"Performance sem GPU".

## Setup

### 1. Dependências

Só precisa do Go. **TinyGo não é necessário** — o build usa o toolchain
padrão (`GOOS=js GOARCH=wasm go build`), que já roda WebAssembly sem instalar
nada a mais (existe `make build-wasm-tinygo` como alternativa, só se quiser
um binário menor).

### 2. Baixar os artefatos do yzma

```bash
make deps          # go mod tidy (host + GOOS=js GOARCH=wasm)
make vendor-yzma    # clona yzma na MESMA tag do go.mod, copia yzma-loader.js/
                     # worker.js/wasm_exec.js e baixa o build pré-compilado do
                     # llama.cpp para WASM (web/vendor/yzma/, gitignored)
make build-wasm      # compila wasm/main.go -> web/vendor/yzma/yzma.wasm
```

**Importante:** `make vendor-yzma` clona a tag `YZMA_VERSION` do Makefile, que
deve bater com a versão em `go.mod`. Se divergirem, o Go program (via
`pkg/llamawasm`) e o módulo `llama.cpp` pré-compilado falam ABIs diferentes e
o programa trava com `panic: JavaScript error: unreachable` /
`this build of yzma drives ABI 1 to 6` ao tentar gerar texto — foi exatamente
isso que aconteceu ao testar com a `main` do yzma (ABI 8) contra a lib
`v1.27.0` do Go (ABI ≤6); usar a mesma tag nos dois lados resolve.

### 3. Servir

```bash
make serve   # devserver em :8090
```

Abra `http://localhost:8090`.

## Usando

1. Clique em **"1. Carregar modelo"** — baixa o modelo GGUF (padrão:
   `Qwen2.5-0.5B-Instruct-GGUF`, quant `Q4_0`, ~410 MB, direto do Hugging
   Face), carrega no worker e decodifica o prefixo fixo (system prompt) uma
   vez ("priming", ver §"Performance sem GPU").
2. Escolha um dos 3 payloads de exemplo (ou edite o JSON do erro à mão).
3. Clique em **"2. Traduzir"**.

## Modelo

Padrão: **`Qwen/Qwen2.5-0.5B-Instruct-GGUF`, quant `Q4_0`** (~410 MB, campo
"Modelo" da página, editável) — instruct-tunado e multilíngue (importante
porque a saída precisa ser PT-BR), num quant leve o suficiente pra rodar sem
GPU. Buscado direto do Hugging Face pelo browser (`llamawasm.FetchModelFile`);
a URL final do CDN da HF envia `Access-Control-Allow-Origin: *`, então
funciona sem proxy.

Alternativas (trocar a URL no campo "Modelo"), da mais rápida pra mais lenta:
- `qwen2.5-0.5b-instruct-q2_k.gguf` — ainda mais rápido que `Q4_0`, com queda
  de qualidade mais perceptível.
- `HuggingFaceTB/SmolLM2-360M-Instruct-GGUF` — modelo menor, instruct-tunado;
  velocidade entre o `Q2_K` do Qwen e o `SmolLM-135M` abaixo, qualidade de
  tradução ainda não testada nesta PoC.
- `QuantFactory/SmolLM-135M-GGUF` (`Q2_K`, ~90 MB) — o modelo usado nos
  próprios testes do yzma; é o que valida a pipeline aqui, mas é um modelo
  *base* (não instruct) e às vezes não para de gerar sozinho no fim da
  resposta — `web/app.js` já corta a saída na primeira quebra de linha para
  compensar isso.
- `Qwen2.5-1.5B-Instruct-GGUF` (`Q4_K_M`, ~1 GB) — melhor fluência PT-BR que
  o 0.5B, mas mais lento.

Fallback offline: baixe o `.gguf` uma vez com `curl` para
`web/vendor/models/<arquivo>.gguf` (gitignored) e troque a URL do campo
"Modelo" pelo caminho local (`vendor/models/<arquivo>.gguf`) — mesmo
same-origin que os outros arquivos estáticos, sem depender de rede na hora da
demo.

## Performance sem GPU

Num host sem WebGPU funcional (ou rodando forçado em `?mode=cpu`, ver
"Troubleshooting"), a tradução sai correta mas pode ser lenta — algo entre
~1 e ~6 tokens/s dependendo do host, muito abaixo dos ~55-63 tokens/s que o
benchmark do próprio yzma reporta para um modelo bem menor (135M) no Chrome
com boa CPU. Ajustes já aplicados:

1. **Loop de geração corrigido para contar tokens novos de fato.** A versão
   original (baseada no exemplo do yzma) usava a posição cumulativa da
   sequência como critério de parada — em prompts curtos isso não fazia
   diferença, mas com um prompt maior (system prompt + JSON do erro) o
   `maxTokens` passado pela página virava, na prática, um teto de posição
   absoluta (prompt + gerado), não "quantos tokens novos gerar". Corrigido em
   `wasm/main.go`.
2. **`maxTokens` baixo** (`web/app.js` manda `40`, não `200`) — a resposta
   certa tem ~10-25 tokens; a system prompt pede frases curtas, e como o
   prompt é uma completion crua (sem chat template), o modelo nem sempre para
   sozinho no fim, então um teto baixo evita desperdiçar tempo "alucinando"
   uma continuação falsa dos exemplos few-shot.
3. **System prompt enxuto**: `web/samples.js` mantém só 3 dos 6 exemplos
   few-shot originais (um padrão de campo numérico, um de sanitização de
   URL/imagem, um genérico) — menos tokens pra decodificar em cada request.
4. **Reuso de KV-cache do prefixo fixo entre chamadas** ("priming"): ao
   carregar o modelo, `web/app.js` manda o system prompt sozinho com
   `maxTokens: -1` — `wasm/main.go` decodifica e cacheia esse prefixo uma
   vez (sem amostrar nada) e testa se `MemorySeqRm` está disponível
   (`pkg/llamawasm`, ABI ≥6 do llama.cpp). Se sim, toda tradução seguinte
   manda só o sufixo (JSON do erro) e o Go usa `MemorySeqRm` pra "rebobinar"
   o contexto de volta pro fim do prefixo em vez de limpar e redecodificar
   tudo. Se a build não suportar (ABI mais antiga), cai automaticamente no
   comportamento original (prompt completo a cada chamada) — sem exigir nada
   do usuário.

   **Pegadinha real encontrada implementando isso**: o `worker.js`
   vendorizado (cópia inalterada do yzma) roteia a chamada como
   `self.yzmaGenerate(message.prompt, message.maxTokens || 128)` — `0` é
   falsy em JS, então mandar `maxTokens: 0` pra sinalizar "priming" virava
   silenciosamente `128` (uma geração de verdade) antes de chegar no Go. Por
   isso o sinal de priming é `-1` (número negativo sobrevive ao `||`), não
   `0` — ver o comentário em `generate()` no `wasm/main.go`.

   Só ajuda a partir da 2ª tradução na mesma sessão do browser (a 1ª ainda
   paga o custo do priming), e não persiste entre reloads de página —
   `pkg/llamawasm` não tem "saved state" (documentado no próprio
   `wasm/README.md` do yzma), então cada F5 perde o cache.
5. **Quant mais leve**: `Q4_0` em vez de `Q4_K_M` como padrão — menos bytes
   por token lido da memória.

Se ainda estiver lento demais, troque para um quant/modelo menor (ver
§"Modelo"). Para checar se threads estão ajudando ou atrapalhando neste host:

```bash
make serve                  # :8090, multi-thread (padrão)
make serve-single-thread    # :8091, força single-thread (sem COOP/COEP)
```

Abra `http://localhost:8091/?mode=cpu` e compare tokens/s com `:8090`. Sem
GPU, um modelo de ~0.5B rodando em WASM puro no browser não chega perto da
velocidade de um binário nativo — isso por si só já é um dado relevante desta
PoC (viabilidade vs. custo de latência), não algo a "esconder" otimizando
demais.

## Troubleshooting

- **Saída sem sentido (ex: "is, is, and, and, and, and…") e geração lenta ao
  mesmo tempo**, com o status mostrando `backend: webgpu (WebGPU)`: no Linux o
  Chrome mantém o Vulkan desativado por padrão, e o WebGPU cai para um modo de
  compatibilidade (ANGLE/OpenGLES) que em várias placas **calcula valores
  errados** sem que o self-test do yzma detecte — bug documentado na seção
  "Vulkan in Chrome on Linux" do
  [`wasm/README.md` do yzma](https://github.com/hybridgroup/yzma/blob/main/wasm/README.md#vulkan-in-chrome-on-linux)
  (issue [#341](https://github.com/hybridgroup/yzma/issues/341)). Esse caminho
  quebrado é lento *e* incorreto ao mesmo tempo, o que explica os dois
  sintomas juntos.
  - Diagnóstico: acesse `http://localhost:8090/?mode=cpu` e recarregue o
    modelo — se o texto sair coerente, confirma o bug de GPU (não é o
    modelo/prompt).
  - Para tentar WebGPU de verdade no Linux: feche todas as janelas do Chrome e
    abra com `google-chrome --enable-features=Vulkan
    --enable-dawn-features=vulkan_enable_f16_on_nvidia`, e confira em
    `chrome://gpu` se aparece "Vulkan: Enabled" com um adapter "Vulkan
    backend" (não "OpenGLES ... Compatibility Mode"). Nem sempre funciona
    (depende do driver) — `?mode=cpu` é o caminho garantido.
- **`panic: JavaScript error: unreachable` / menção a "ABI"** ao gerar texto:
  a versão do yzma clonada em `make vendor-yzma` não bate com a do `go.mod`.
  Ajuste `YZMA_VERSION` no `Makefile` para a mesma versão de
  `require github.com/hybridgroup/yzma` em `go.mod` e rode `make vendor-yzma
  build-wasm` de novo.
- **`SharedArrayBuffer is not defined`** no console: os headers COOP/COEP não
  chegaram ao browser — confirme que está acessando via `devserver`
  (`make serve`), não abrindo `web/index.html` direto do disco.
  `window.crossOriginIsolated` deve ser `true`.
- **Erro de rede/CORS ao baixar o modelo**: use o fallback same-origin (ver
  §"Modelo" acima).
- Console do navegador mostra qual backend o `yzma-loader.js` escolheu
  (`webgpu`, `cpu-threads` ou `cpu`). WebGPU exige Chrome/Edge ≥137 ou
  Firefox ≥153 com flags; sem COOP/COEP cai para single-thread (mais lento,
  mas funciona em qualquer browser moderno). Detalhes completos em
  [`wasm/README.md` do yzma](https://github.com/hybridgroup/yzma/blob/main/wasm/README.md).

## Origem

O system prompt em `web/samples.js` (trimado para 3 exemplos, ver
§"Performance sem GPU") originou de um agente de tradução de erros de
plataforma real usado em produção em outro projeto — extraído aqui como PoC
isolada, sem nenhuma dependência desse projeto original.
