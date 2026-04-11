package token

import (
	tk "github.com/pkoukk/tiktoken-go"
	"log"
	"os"
	"strings"
	"sync"
)

// prefixEntry maps model prefixes to encoding names
// Longer/more-specific prefixes MUST come first for correct matching
type prefixEntry struct {
	prefix   string
	encoding string
}

var prefixTable = []prefixEntry{
	{"gpt-4o", "o200k_base"},
	{"gpt-4", "cl100k_base"},
	{"gpt-3.5", "cl100k_base"},
	{"o3-", "o200k_base"},
	{"o1-", "o200k_base"},
	{"deepseek", "cl100k_base"},
	{"mistral", "cl100k_base"},
	{"claude", "cl100k_base"},
	{"gemini", "cl100k_base"},
	{"llama", "cl100k_base"},
	{"qwen", "cl100k_base"},
	{"text-", "p50k_base"},
	{"code-", "p50k_base"},
}

// Tokenizer handles token counting with encoding caching
type Tokenizer struct {
	fallbackEncoding string
	encodingCache    sync.Map // encodingName → *tk.Tiktoken
}

// Singleton
var (
	globalTokenizer     *Tokenizer
	globalTokenizerOnce sync.Once
)

func GetTokenizer() *Tokenizer {
	globalTokenizerOnce.Do(func() {
		globalTokenizer = &Tokenizer{
			fallbackEncoding: "cl100k_base",
		}
	})
	return globalTokenizer
}

// resolveEncoding maps a model name to tiktoken encoding using prefixTable
func (t *Tokenizer) resolveEncoding(model string) string {
	for _, entry := range prefixTable {
		if strings.HasPrefix(model, entry.prefix) {
			return entry.encoding
		}
	}
	return t.fallbackEncoding
}

// CountTokens counts tokens in the given text for the given model
func (t *Tokenizer) CountTokens(text string, model string) (int, error) {
	encodingName := t.resolveEncoding(model)

	// Check cache first
	if cached, ok := t.encodingCache.Load(encodingName); ok {
		enc := cached.(*tk.Tiktoken)
		return len(enc.Encode(text, nil, nil)), nil
	}

	// Load and cache
	enc, err := tk.GetEncoding(encodingName)
	if err != nil {
		// fallback to simplest estimation: len/4
		log.Printf("[DEBUG][fallback-token-count] encoding %q failed to load: %v, using len/4 estimate", encodingName, err)
		return len(text) / 4, nil
	}
	t.encodingCache.Store(encodingName, enc)
	return len(enc.Encode(text, nil, nil)), nil
}

// CountPromptTokens extracts prompt from request body and counts tokens
func (t *Tokenizer) CountPromptTokens(requestBody []byte, model string) (int, error) {
	promptText := extractPromptText(requestBody)
	if promptText == "" {
		return 0, nil
	}
	return t.CountTokens(promptText, model)
}

// CountCompletionTokens counts tokens in the completion text
func (t *Tokenizer) CountCompletionTokens(text string, model string) (int, error) {
	if text == "" {
		return 0, nil
	}
	return t.CountTokens(text, model)
}

// FallbackEnabled checks if fallback counting is enabled via env var
func FallbackEnabled() bool {
	val := strings.ToLower(os.Getenv("TOKEN_FALLBACK_ENABLED"))
	return val != "false" && val != "0" && val != "no" && val != "off" && val != "disabled"
}
