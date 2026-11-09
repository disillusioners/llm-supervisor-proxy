package imggen

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// defaultImageMIME is the MIME type used when the decoded magic bytes do
// not match any of the sniffed signatures below. It is the truthful fallback
// for unknown magic bytes (application/octet-stream) — we do not claim an image
// type we did not verify. The FE data-URI preview allowlist will not preview
// an octet-stream URI, which is acceptable and consistent.
const defaultImageMIME = "application/octet-stream"

// imageDataURIFromBase64 converts a base64-encoded image payload into a
// data-URI, sniffing the MIME type from the DECODED magic bytes.
//
// Sniff table (in order — JPEG is the PRIMARY signature per the in-cluster
// measurement; PNG is retained as a secondary branch for future providers):
//
//	FF D8 FF        -> image/jpeg   (JPEG / JFIF, the observed 9router output)
//	89 50 4E 47    -> image/png    (PNG magic \x89PNG)
//	47 49 46 38    -> image/gif    (GIF87a / GIF89a)
//	RIFF....WEBP   -> image/webp   (RIFF container with WEBP form)
//	(anything else) -> defaultImageMIME (application/octet-stream) — truthful fallback
//
// The returned data-URI carries the base64 of the decoded bytes re-encoded
// canonically (no embedded whitespace), so the data-URI is self-contained
// and stable.
func imageDataURIFromBase64(b64 string) (string, error) {
	// Strip all whitespace (some upstreams wrap base64 lines) before
	// decoding — base64.StdEncoding rejects embedded newlines.
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, strings.TrimSpace(b64))
	if clean == "" {
		return "", fmt.Errorf("empty base64 payload")
	}
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("invalid base64 image payload: %w", err)
	}
	mime := sniffImageMIME(raw)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// sniffImageMIME returns the MIME type for the given decoded image bytes.
func sniffImageMIME(raw []byte) string {
	switch {
	case len(raw) >= 3 && raw[0] == 0xFF && raw[1] == 0xD8 && raw[2] == 0xFF:
		return "image/jpeg"
	case len(raw) >= 8 && raw[0] == 0x89 && raw[1] == 0x50 && raw[2] == 0x4E && raw[3] == 0x47 && raw[4] == 0x0D && raw[5] == 0x0A && raw[6] == 0x1A && raw[7] == 0x0A:
		return "image/png"
	case len(raw) >= 6 && (string(raw[:6]) == "GIF87a" || string(raw[:6]) == "GIF89a"):
		return "image/gif"
	case len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return "image/webp"
	default:
		return defaultImageMIME
	}
}
