# Lesson: gzip E2E harness gotchas (2026-08-28)

Context: first execution of `test/mock_gzip_request_decompression.sh` (gzip request-body feature
verification @ 7a9ecff). Three harness-only bugs found and fixed during the run; product behavior
was correct in all cases. Product fixes: none (report-only mandate).

## 1. macOS gzip refuses process substitution (CVE-2022-1271 hardening)
`gzip -c <(echo -n "$BODY")` emits `gzip: /dev/fd/63 is not a regular file` and produces a 0-byte
file on macOS gzip ≥ 1.13. It can PASS tests incidentally (proxy rejects the empty/non-JSON body
anyway) — masking a broken fixture. **Rule: write bodies to regular temp files before gzip/curl.**

## 2. Anthropic client-response identity must be id-normalized
The Anthropic adapter generates a fresh `msg_...` id per response. Two requests with identical
inputs therefore produce client bodies that differ at the id bytes. Byte-exact client-body
comparison belongs on the OpenAI path only; for /v1/messages, byte-exactness is asserted on the
UPSTREAM (translated) bytes, and client-side diff is informational. Same convention as the rsd_m2
gate ("non-stream id-normalized byte-identity").

## 3. Header-record path derivation
When a mock writes `body_<n>.bin` / `hdr_<n>.txt`, derive the header path by two-step parameter
substitution (`${var/body_/hdr_}` then `${var/.bin/.txt}`) — deriving it as `${body_file}.txt`
silently points at a nonexistent `body_<n>.bin.txt`.

## 4. Spec fact corrections discovered
- SSE heartbeat cadence is **30s** (`pkg/ui/server.go:366`), not 15s as older docs claimed.
  To observe SSE delivery inside a 20s budget, trigger an event (PUT /fe/api/config →
  `config.updated` on the bus) instead of waiting for the heartbeat.
- gzipmw behavior map (from pkg/middleware/gzipmw/gzip.go): over-cap (>100 MiB) → **413**
  `request_body_too_large`; corrupt gzip → **400** `invalid_gzip_body`; non-gzip
  Content-Encoding (e.g. deflate) → **415**.
- `test/e2e_anthropic_thinking_leak` S3 now PASSES (test recalibrated at base fea5874 to expect
  the wire-translated thinking block). PACKS.md's "S3 ❌ REAL BUG" note is stale.

## 5. Coverage bookkeeping
`go list ./...` (37 pkgs) ≠ pack scopes. For per-package mandates, close the residue explicitly:
this sweep needed two closure packs (test/e2e_* dirs; root/cmd/logger/scripts/store-db which
carry no test files). Always request the verbatim `go list ./...` from a worker to verify.
