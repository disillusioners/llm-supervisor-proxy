package token

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func TestResolveEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  string
		desc  string
	}{
		// gpt-4o group — MUST resolve to o200k_base, never cl100k_base
		{"gpt-4o", "o200k_base", "gpt-4o exact"},
		{"gpt-4o-mini", "o200k_base", "gpt-4o-mini"},
		{"gpt-4o-2024-08-06", "o200k_base", "gpt-4o with date"},
		{"gpt-4o-realtime", "o200k_base", "gpt-4o-realtime"},

		// gpt-4 group — MUST resolve to cl100k_base
		{"gpt-4", "cl100k_base", "gpt-4 exact"},
		{"gpt-4-0314", "cl100k_base", "gpt-4 with date suffix"},
		{"gpt-4-32k", "cl100k_base", "gpt-4-32k"},
		{"gpt-4-0613", "cl100k_base", "gpt-4-0613"},
		{"gpt-4-turbo", "cl100k_base", "gpt-4-turbo"},

		// gpt-3.5 group
		{"gpt-3.5-turbo", "cl100k_base", "gpt-3.5-turbo exact"},
		{"gpt-3.5-turbo-0301", "cl100k_base", "gpt-3.5-turbo-0301"},
		{"gpt-3.5-turbo-16k", "cl100k_base", "gpt-3.5-turbo-16k"},

		// o3 / o1 groups
		{"o1-preview", "o200k_base", "o1-preview"},
		{"o1-mini", "o200k_base", "o1-mini"},
		{"o3-mini", "o200k_base", "o3-mini"},
		{"o3-mini-2025-01-09", "o200k_base", "o3-mini with date"},

		// deepseek
		{"deepseek-chat", "cl100k_base", "deepseek-chat"},
		{"deepseek-coder", "cl100k_base", "deepseek-coder"},
		{"deepseek-reasoner", "cl100k_base", "deepseek-reasoner"},

		// mistral
		{"mistral-large", "cl100k_base", "mistral-large"},
		{"mistral-small", "cl100k_base", "mistral-small"},

		// claude
		{"claude-3-5-sonnet", "cl100k_base", "claude-3-5-sonnet"},
		{"claude-3-opus", "cl100k_base", "claude-3-opus"},

		// gemini
		{"gemini-2.0-flash", "cl100k_base", "gemini-2.0-flash"},
		{"gemini-pro", "cl100k_base", "gemini-pro"},

		// llama
		{"llama-3-70b", "cl100k_base", "llama-3-70b"},
		{"llama-3.1-8b", "cl100k_base", "llama-3.1-8b"},

		// qwen
		{"qwen-2.5-72b", "cl100k_base", "qwen-2.5-72b"},
		{"qwen-turbo", "cl100k_base", "qwen-turbo"},

		// text- / code- prefix
		{"text-davinci-003", "p50k_base", "text-davinci-003"},
		{"text-embedding-ada-002", "p50k_base", "text-embedding-ada-002"},
		{"code-davinci-002", "p50k_base", "code-davinci-002"},

		// Unknown model — should fall back to cl100k_base
		{"unknown-model-xyz", "cl100k_base", "unknown model falls back"},
		{"foobar", "cl100k_base", "random string falls back"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			tok := GetTokenizer()
			got := tok.resolveEncoding(tt.model)
			if got != tt.want {
				t.Errorf("resolveEncoding(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

// TestResolveEncodingDeterminism is the critical gpt-4o vs gpt-4 collision test.
// The fix in ae1acdb addressed non-deterministic map iteration by using a sorted
// prefixTable where gpt-4o appears BEFORE gpt-4. We run 200 iterations to ensure
// the ordering is stable and gpt-4o-family models NEVER resolve to cl100k_base.
func TestResolveEncodingDeterminism(t *testing.T) {
	tok := GetTokenizer()
	iterations := 200

	gpt4oModels := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4o-2024-08-06",
		"gpt-4o-realtime",
		"gpt-4o-search",
	}

	gpt4Models := []string{
		"gpt-4",
		"gpt-4-0314",
		"gpt-4-32k",
		"gpt-4-turbo",
	}

	for i := 0; i < iterations; i++ {
		for _, model := range gpt4oModels {
			got := tok.resolveEncoding(model)
			if got != "o200k_base" {
				t.Errorf("iteration %d: resolveEncoding(%q) = %q, want o200k_base — DETERMINISM FAILURE",
					i, model, got)
			}
		}
		for _, model := range gpt4Models {
			got := tok.resolveEncoding(model)
			if got != "cl100k_base" {
				t.Errorf("iteration %d: resolveEncoding(%q) = %q, want cl100k_base",
					i, model, got)
			}
		}
	}
}

func TestCountTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		text   string
		model  string
		minVal int // at least this many tokens
		maxVal int // at most this many tokens (use 0 to skip)
	}{
		{
			name:   "hello_world_cl100k",
			text:   "hello world",
			model:  "gpt-4",
			minVal: 1,
			maxVal: 10,
		},
		{
			name:   "empty_string",
			text:   "",
			model:  "gpt-4",
			minVal: 0,
			maxVal: 0,
		},
		{
			name:   "single_char",
			text:   "a",
			model:  "gpt-4",
			minVal: 1,
			maxVal: 5,
		},
		{
			name:   "sentence_cl100k",
			text:   "The quick brown fox jumps over the lazy dog.",
			model:  "gpt-4",
			minVal: 5,
			maxVal: 30,
		},
		{
			name:   "sentence_o200k",
			text:   "The quick brown fox jumps over the lazy dog.",
			model:  "gpt-4o",
			minVal: 5,
			maxVal: 30,
		},
		{
			name:   "very_long_text",
			text:   strings.Repeat("hello world ", 1000),
			model:  "gpt-4",
			minVal: 500,
			maxVal: 15000,
		},
		{
			name:   "emoji_text",
			text:   "Hello 👋🌍🎉",
			model:  "gpt-4o",
			minVal: 1,
			maxVal: 30,
		},
		{
			name:   "cjk_characters",
			text:   "你好世界",
			model:  "gpt-4o",
			minVal: 1,
			maxVal: 30,
		},
		{
			name:   "korean_characters",
			text:   "안녕하세요",
			model:  "gpt-4o",
			minVal: 1,
			maxVal: 30,
		},
		{
			name:   "arabic_characters",
			text:   "مرحبا بالعالم",
			model:  "gpt-4o",
			minVal: 1,
			maxVal: 30,
		},
		{
			name:   "mixed_unicode",
			text:   "Hello 世界 🌍 🎉 Привет",
			model:  "gpt-4o",
			minVal: 1,
			maxVal: 40,
		},
		{
			name:   "special_chars_whitespace",
			text:   "tab\there\nnew line\r\n",
			model:  "gpt-4",
			minVal: 1,
			maxVal: 20,
		},
		{
			name:   "code_snippet",
			text:   "func main() { fmt.Println(\"Hello, World!\") }",
			model:  "gpt-4",
			minVal: 5,
			maxVal: 30,
		},
		{
			name:   "json_like_text",
			text:   `{"role": "user", "content": "Hello world"}`,
			model:  "gpt-4",
			minVal: 5,
			maxVal: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := GetTokenizer()
			got, err := tok.CountTokens(tt.text, tt.model)
			if err != nil {
				t.Errorf("CountTokens(%q, %q) returned error: %v", tt.text, tt.model, err)
				return
			}
			if tt.maxVal > 0 {
				if got < tt.minVal || got > tt.maxVal {
					t.Errorf("CountTokens(%q, %q) = %d, want between %d and %d",
						tt.text, tt.model, got, tt.minVal, tt.maxVal)
				}
			} else {
				if got != tt.minVal {
					t.Errorf("CountTokens(%q, %q) = %d, want %d",
						tt.text, tt.model, got, tt.minVal)
				}
			}
		})
	}
}

func TestCountTokensFallback(t *testing.T) {
	t.Parallel()

	// Note: The fallback to len(text)/4 is triggered when GetEncoding fails.
	// Since cl100k_base (the fallback encoding) always exists in tiktoken-go,
	// this path cannot be triggered through normal model names.
	// The fallback logic is tested here for documentation purposes:
	// if resolveEncoding returns cl100k_base but the encoding load fails,
	// CountTokens would return len(text)/4.
	//
	// We verify the fallbackEncoding field is set correctly.
	tok := GetTokenizer()
	if tok.fallbackEncoding != "cl100k_base" {
		t.Errorf("fallbackEncoding = %q, want cl100k_base", tok.fallbackEncoding)
	}
}

func TestFallbackEnabled(t *testing.T) {
	// Save and restore the environment variable
	originalVal := os.Getenv("TOKEN_FALLBACK_ENABLED")
	defer os.Setenv("TOKEN_FALLBACK_ENABLED", originalVal)

	tests := []struct {
		name  string
		setup func()
		want  bool
	}{
		{
			name:  "unset_env_returns_true",
			setup: func() { os.Unsetenv("TOKEN_FALLBACK_ENABLED") },
			want:  true,
		},
		{
			name:  "true_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "true") },
			want:  true,
		},
		{
			name:  "yes_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "yes") },
			want:  true,
		},
		{
			name:  "one_string",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "1") },
			want:  true,
		},
		{
			name:  "TRUE_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "TRUE") },
			want:  true,
		},
		{
			name:  "YES_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "YES") },
			want:  true,
		},
		{
			name:  "True_mixed",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "True") },
			want:  true,
		},
		{
			name:  "Yes_mixed",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "Yes") },
			want:  true,
		},
		{
			name:  "1_numeric",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "1") },
			want:  true,
		},
		{
			name:  "false_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "false") },
			want:  false,
		},
		{
			name:  "0_string",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "0") },
			want:  false,
		},
		{
			name:  "no_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "no") },
			want:  false,
		},
		{
			name:  "off_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "off") },
			want:  false,
		},
		{
			name:  "disabled_lowercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "disabled") },
			want:  false,
		},
		{
			name:  "FALSE_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "FALSE") },
			want:  false,
		},
		{
			name:  "False_mixed",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "False") },
			want:  false,
		},
		{
			name:  "NO_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "NO") },
			want:  false,
		},
		{
			name:  "OFF_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "OFF") },
			want:  false,
		},
		{
			name:  "disabled_uppercase",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "DISABLED") },
			want:  false,
		},
		{
			name:  "random_string",
			setup: func() { os.Setenv("TOKEN_FALLBACK_ENABLED", "random-garbage") },
			want:  true, // anything not in the off-list returns true
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			got := FallbackEnabled()
			if got != tt.want {
				t.Errorf("FallbackEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetTokenizerSingleton(t *testing.T) {
	t.Parallel()

	// Calling GetTokenizer multiple times should return the same pointer
	t1 := GetTokenizer()
	t2 := GetTokenizer()
	t3 := GetTokenizer()

	if t1 != t2 || t2 != t3 {
		t.Errorf("GetTokenizer() did not return the same pointer: %p vs %p vs %p", t1, t2, t3)
	}

	// Verify the singleton is properly initialized
	if t1.fallbackEncoding != "cl100k_base" {
		t.Errorf("singleton fallbackEncoding = %q, want cl100k_base", t1.fallbackEncoding)
	}
}

func TestGetTokenizerConcurrent(t *testing.T) {
	// Test that concurrent calls to GetTokenizer are safe
	const numGoroutines = 100
	var wg sync.WaitGroup
	var results [numGoroutines]*Tokenizer
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tok := GetTokenizer()
			mu.Lock()
			results[idx] = tok
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// All results should be the same pointer
	first := results[0]
	for i := 1; i < numGoroutines; i++ {
		if results[i] != first {
			t.Errorf("goroutine %d got different tokenizer pointer", i)
		}
	}
}

func TestCountPromptTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		requestBody string
		model       string
		wantMin     int
		wantMax     int
		wantErr     bool
	}{
		{
			name:        "simple_message",
			requestBody: `{"messages": [{"role": "user", "content": "Hello"}]}`,
			model:       "gpt-4",
			wantMin:     1,
			wantMax:     20,
		},
		{
			name:        "multiple_messages",
			requestBody: `{"messages": [{"role": "system", "content": "You are helpful"}, {"role": "user", "content": "Hi"}]}`,
			model:       "gpt-4",
			wantMin:     1,
			wantMax:     50,
		},
		{
			name:        "empty_body",
			requestBody: `{}`,
			model:       "gpt-4",
			wantMin:     0,
			wantMax:     0,
		},
		{
			name:        "prompt_field",
			requestBody: `{"prompt": "Translate this"}`,
			model:       "gpt-4",
			wantMin:     1,
			wantMax:     20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := GetTokenizer()
			got, err := tok.CountPromptTokens([]byte(tt.requestBody), tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("CountPromptTokens() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("CountPromptTokens() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCountCompletionTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		model   string
		wantMin int
		wantMax int
	}{
		{
			name:    "normal_text",
			text:    "The answer is 42.",
			model:   "gpt-4",
			wantMin: 1,
			wantMax: 20,
		},
		{
			name:    "empty_text",
			text:    "",
			model:   "gpt-4",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "long_text",
			text:    strings.Repeat("Hello ", 100),
			model:   "gpt-4o",
			wantMin: 10,
			wantMax: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := GetTokenizer()
			got, err := tok.CountCompletionTokens(tt.text, tt.model)
			if err != nil {
				t.Errorf("CountCompletionTokens() error = %v", err)
				return
			}
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("CountCompletionTokens() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}
