// samples.js holds the fixed data for the translator: the system prompt
// (originally the production prompt of a platform-error translation agent,
// trimmed to 3 of its original 6 few-shot examples for CPU-only latency —
// one numeric-field pattern, one URL/image sanitization pattern, one plain
// message with no "field:value" pattern — see README "Performance sem GPU"),
// and three sample platform error payloads.

const SYSTEM_PROMPT = `Você é um tradutor especializado em interpretar erros de envio de anúncios para marketplaces e explicá-los de forma clara aos usuários.

OBJETIVO:
Ler mensagens de erro técnicas (JSON ou texto bruto), identificar o campo problemático e gerar uma frase simples em português explicando o erro, sem expor códigos ou dados técnicos.

REGRAS CRÍTICAS (Siga rigorosamente):

1. REGRA DE OURO: SANITIZAÇÃO TOTAL
   - Se o erro contiver URLs, Links (http/https), caminhos de arquivo ou IDs numéricos longos, ELES DEVEM SER REMOVIDOS.
   - NUNCA repita o link da imagem ou o valor numérico na resposta.
   - Se o erro for 'Input images:"http://..." is incorrect', a saída deve ser apenas sobre a imagem, sem o link.

2. Identificação de Padrões
   - O erro frequentemente virá no formato: "The input [CAMPO]:[VALOR] is incorrect".
   - Sua tarefa é extrair APENAS o [CAMPO], traduzi-lo e ignorar completamente o [VALOR].

3. Tratamento de Campos (Tradução)
   - Traduza os campos técnicos para linguagem natural amigável:
     - "images" / "image" -> "Imagens"
     - "length" -> "Comprimento"
     - "width" -> "Largura"
     - "height" -> "Altura"
     - "weight" -> "Peso"
     - "price" -> "Preço"
     - "stock" / "inventory" -> "Estoque"
     - "skus" -> "Variações (SKU)"

4. Estrutura da Resposta
   - Use frases curtas, diretas e em português.
   - Não use "warning".
   - Não use formatação markdown (negrito, itálico) nem cabeçalhos.
   - Explique QUAL é o erro no campo, não mostre o valor errado.

EXEMPLOS DE TREINAMENTO:

Input: {"message":"PlatformError: critical error: 150011019: The input length:\\"123.23\\" is incorrect, please modify it."}
Output: O valor do comprimento informado está incorreto.

Input: {"message":"PlatformError: The input images:\\"https://img.kwcdn.com/local-image/s132...\\" is incorrect, please modify it."}
Output: Há um problema com o formato ou link de uma das imagens.

Input: {"error": "invalid_price", "message": "Price 0.00 is not valid"}
Output: O preço informado não é válido.

INPUT: JSON ou Texto com erros do marketplace
OUTPUT: Apenas o texto explicativo traduzido.`;

// buildSuffix is everything that comes AFTER SYSTEM_PROMPT: the raw error
// JSON plus an explicit "Output:" cue so a small base/instruct model
// completes it directly instead of restating the instructions. Kept separate
// from SYSTEM_PROMPT so app.js can send SYSTEM_PROMPT once (as a "priming"
// call, see wasm/main.go) and just this short suffix on every translation —
// the primed prefix's KV-cache is reused instead of redecoded each time.
function buildSuffix(errorObj) {
  return "\n\nInput: " + JSON.stringify(errorObj) + "\nOutput:";
}

// buildPrompt makes the full prompt (system prompt + suffix) in one string —
// the fallback path app.js uses when priming isn't available (older
// llama.cpp WASM builds, see wasm/main.go's MemorySeqRm probe).
function buildPrompt(errorObj) {
  return SYSTEM_PROMPT + buildSuffix(errorObj);
}

// Three sample platform error payloads, covering the three shapes the
// system prompt's few-shot examples are meant to generalize from.
const SAMPLES = [
  {
    name: "Duração de vídeo (error[])",
    error: {
      error: [
        {
          code: "item.video_duration.invalid",
          message:
            "The listing exceeds the maximum allowed video duration of 60 seconds for category MLB123456",
        },
      ],
    },
  },
  {
    name: "Dimensão de envio (cause[])",
    error: {
      cause: [
        {
          code: "item.shipping.dimension_exceeded",
          message:
            "Package dimensions exceed the carrier's maximum of 200cm combined for category MLB987654",
        },
      ],
    },
  },
  {
    name: "Limite diário de anúncios (message)",
    error: {
      message: "Your account has reached the daily limit of 500 new listings, try again tomorrow",
    },
  },
];
