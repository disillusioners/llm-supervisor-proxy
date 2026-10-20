# Image-Generation Test Button — Release Note (2026-10-20)

Operator-facing **Test** button for image-generation models: each image-gen model
row in the Web UI can fire one real, bounded generation probe through the same
production machinery that serves `POST /v1/image_generation`.

## What shipped

- **Backend route** — `POST /fe/api/image_generation/test` (`pkg/ui/handlers_imggen.go`):
  UI-plane endpoint taking a model ID, executing one test call, returning a
  classified result envelope.
- **Reusable seam** — `pkg/imggen/test_exec.go` (`ExecuteTest`): drives the SAME
  production call path as `POST /v1/image_generation` — no forked call stack. A
  cross-path parity test pins this, so the button cannot silently drift from real
  traffic behavior.
- **Frontend** — per-row Test button on image-gen model rows
  (`pkg/ui/frontend/src/components/config/ImgGenModelsTab.tsx`): bounded thumbnail
  preview, classified error display, and a cost hint (≈$0.0035 per call).

## Key invariants

- Exactly one `Content-Type` header (`Set`, never `Add`).
- Client `Accept-Encoding` is never forwarded upstream.
- Timeout is ctx-owned — a single deadline governs the whole probe.
- Single upstream call — no retry, no fallback, no racing; probes fail once, loudly.
- Credential selection: `credentials[0]` by exact ID.
- `IsImageGen()` kind gate — the seam refuses non-image-gen models.
- Probes are **not** metered (usage counters untouched); real `/v1/image_generation`
  traffic still is.
- The UI endpoint always answers HTTP 200 with a classified-failure envelope on
  error; the FE renders the classification, not a bare status code.
- MiniMax business failures inside HTTP 200 bodies are classified — an HTTP-200
  response is not assumed to be success.

## Gate record

- Reviewer: **APPROVE-WITH-NITS** — 0 blockers, 0 majors (nits → follow-ups).
- Tidier: pass.
- Tester: **FIT TO SHIP** — 5 gates, 84/84 behavioural assertions over a real
  built binary.

## Rollout

- `feature/img-gen-test-button` @ `2768f67`
- `latest` @ `a4377c1` (merge of the feature branch)
- `master` @ `383c0de` (feature merge) → `7363c09` (CI re-trigger merge)
- The full rollout record (image digest, host rollout state, live verification)
  is appended here after deployment.

## Notes / follow-ups

- **Shared request-builder refactor** — the test executor duplicates
  request-building logic in-package; extract a shared builder.
- **Per-row 10s cooldown** (FE) — deferred.
- Reason/error string cap — already applied (bounded error strings in the envelope).
- **Claim-first `GetModel` caveat** — `GetModel` resolves `shadow_model_id` claims
  first; the test path inherits this resolution behavior (matches production).
- Toolchain note: build with `go build ./cmd` — repo-root `go build .` builds a
  tracked stub.
- **CI paths-filter / empty-push trap** — the Docker CI push trigger is
  paths-filtered, so a zero-delta push (empty commit + `--no-ff` merge with an
  identical tree) emits no workflow event at all (GitHub API `runs?head_sha=…` →
  total 0); CI cannot be re-triggered that way. Remedy: `workflow_dispatch` added
  to `.github/workflows/docker-ci.yml` (same date, separate commit) so a transient
  runner failure (e.g. `actions/setup-go` provisioning, run 39471362049) can be
  re-run from the Actions tab without any push. Note: dispatch runs re-exercise
  checkout → frontend build → Go build/test (where the transient failure occurred);
  the Docker login/build/push steps remain gated on `github.event_name == 'push'`,
  so image publish still requires a real push event.
