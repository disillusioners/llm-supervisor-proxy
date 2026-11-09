package imggen

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestExtractImageURLs_B64(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46})
	body := []byte(`{"data":[{"b64_json":"` + b64 + `"}]}`)
	got := extractImageURLs(body)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if !strings.HasPrefix(got[0], "data:image/jpeg;base64,") {
		t.Errorf("got %q", got[0])
	}
}

func TestExtractImageURLs_Mixed(t *testing.T) {
	body := []byte(`{"data":[{"image_urls":["https://x/a.png"]},{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) + `"}]}`)
	got := extractImageURLs(body)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}
