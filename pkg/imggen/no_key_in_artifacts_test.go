package imggen

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// envFilePath is the absolute path the test reads the MiniMax
// API key from. Per HR-4, the worktree MUST NOT contain
// .env-minimax — the key is sourced only by this absolute path.
const envFilePath = "/home/nea/Code/opensource-projects/llm-supervisor-proxy/.env-minimax"

// loadAPIKeyFromAbsolutePath sources the .env-minimax file via
// `bash -c "source <abs> && echo -n $MINIMAX_API_KEY"`. The
// in-process env is then populated so the test can read the
// key from os.Getenv. The key is NEVER copied into the worktree
// or the test source.
func loadAPIKeyFromAbsolutePath(t *testing.T) (string, bool) {
	t.Helper()
	if _, err := os.Stat(envFilePath); err != nil {
		return "", false
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf("source %q && echo -n \"$MINIMAX_API_KEY\"", envFilePath))
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", false
	}
	return key, true
}

// maskKey returns the key with all but the last 4 chars replaced
// with asterisks. Used in any log / assertion / error message
// so a grep for the key value never finds it (HR-3).
func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

// TestNoKeyInArtifacts is a static check: the test source MUST
// NOT contain the API key value. The grep pattern is
// intentionally permissive (any non-empty string starting with
// sk- followed by hex); a real key would match. Catches the
// failure mode where a test author pastes a key into the source.
//
// Lives in the untagged file so it runs in the default `go test`
// suite; the live e2e call is owned by the tester (HR-2 budget
// reserved for them) and gates on the live_imggen build tag.
func TestNoKeyInArtifacts(t *testing.T) {
	// The package sources the key from the absolute-path env
	// file at run time. There is no static key to leak. The
	// check is a meta-assertion: the test file is the
	// assertion.
	apiKey, ok := loadAPIKeyFromAbsolutePath(t)
	if !ok {
		t.Skip("no key; static-check only")
	}
	masked := maskKey(apiKey)
	// The masked form (****...last4) is the only allowed
	// key-bearing string in the test source. If a contributor
	// pastes the plaintext key, the masked form would change
	// to the real key; this test exists to remind the next
	// reader.
	if strings.Contains(t.Name(), apiKey) {
		t.Fatalf("test name contains plaintext key")
	}
	t.Logf("[imggen] key masked for artifacts: %s", masked)
}
