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
- `latest` @ `a4377c1` (feature merge) → `c268f04` (workflow_dispatch + this note)
- `master` @ `383c0de` (CI failed: setup-go transient, run 39471362049) → `7363c09`
  (zero-delta re-push: no CI run, paths-filter no-op trap) → `49beb28`
  (green build, run 39479296021)
- **Shipped:** master `49beb28a1c7c9ce856413e13329e1348896557c2`, image digest
  `sha256:7ef11e3a1f5295b45cc8cbe861aab6a678e9ed7f0c8a4681f2be2fea532a7a51`
  (tags `49beb28`/`latest`/`master` all same digest; image pushed 2026-10-20T19:08:25Z).
- **Both hosts live @ `sha256:7ef11e3a…`** — backup `llm.daoduc.org` helm rev
  28 → 29 @ 19:11:13Z (28s, `--wait`, pod `…-859557bfc4-xdnhx`, 0 restarts);
  primary `llm.ensem.dev` gen 26 → 27 @ 19:12:09Z (21s, pod `…-76b464d489-27v8w`,
  0 restarts); imageID byte-exact on both; 3.5 min soak clean; failover posture intact.
- **Live verification 3/4 budgeted calls (~$0.0105, 0 retries):** backup API probe
  200 / 16140 ms (credential `minimax-company`, JPEG 103452 B); primary API probe
  200 / 17101 ms (credential `minimax-mindspath` — `credentials[0]`-exact-ID proven
  on both host orderings); primary REAL UI CLICK 200 / 16865 ms — success panel +
  rendered `<img>` with the correct CDN src. `[imggen-test]` log lines 3/3 with
  credential IDs, 0 secret-shaped strings; probes did NOT meter (usage tables clean).
- **Rollback anchors (not exercised):** backup `helm rollback llm-supervisor-proxy 28`
  (rev 28 = `f800c67` @ `sha256:79f90c1c…dad31`); primary
  `kubectl -n llmproxy set image deployment/llm-supervisor-proxy
  llm-supervisor-proxy=docker.io/disillusioners/llm-supervisor-proxy:f800c67`.
- **GitOps:** data-center-scripts `feature/llmproxy-pin-49beb28` → `latest @ ea5eb26`
  — both endpoints pinned, full rollout record at
  `.agents/tester/RESULTS/2026-10-20-llm-supervisor-proxy-49beb28-rollout.md`.

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
