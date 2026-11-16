# HANDOFF — fe-model-form-provider-imggen-9router

HANDOFF_PATH = .agents/shared/planning/sonic-team/fe-model-form-provider-imggen-9router/HANDOFF.md
ABSOLUTE: /home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-fe-model-form-provider-imggen-9router/.agents/shared/planning/sonic-team/fe-model-form-provider-imggen-9router/HANDOFF.md
RANGE: base 2b987bf (origin/latest at plan-open) .. merged a2d488f (code; this handoff commit sits on top)
FEATURE BRANCH: feature/fe-model-form-provider-imggen-9router
MODULES: A fe-model-forms @ 3fe2040 (merged fc7105b, slot 1) | B imggen-test-9router @ 13a8e4e (merged a2d488f, slot 2); both --no-ff, zero conflicts, disjoint partitions.

## Files touched across modules

Module A — fe-model-forms (branch feature/fe-model-forms-providers @ 3fe2040):
- pkg/ui/frontend/src/components/config/DecisionModelForm.tsx
- pkg/ui/frontend/src/components/config/ImgGenModelForm.tsx
- pkg/ui/frontend/src/components/config/__tests__/DecisionModelForm.test.tsx
- pkg/ui/frontend/src/components/config/__tests__/ImgGenModelForm.test.tsx

Module B — imggen-test-9router (branch feature/imggen-test-9router @ 13a8e4e):
- pkg/imggen/test_exec.go
- pkg/imggen/test_exec_test.go
- pkg/imggen/test_exec_live_test.go (new; triple-gated live probe)
- pkg/ui/handlers_imggen.go
- pkg/ui/handlers_imggen_test.go (new)

Union: 9 files, +961/-53. No file touched by both modules.

## What changed

A) FE provider-on-EDIT fix (both forms, symmetric, FE-only):
- ROOT CAUSE (verified in source, NOT fixed server-side by scope decision): the FE wire never carries internal_provider — pkg/ui/server.go:46 Model DTO has no InternalProvider field and modelConfigToDTO (server.go:1946) maps none. initialData.internal_provider was always undefined, so makeInitialState silently fell back to the per-kind default ('minimax' / 'openai') and mis-filtered the credential editor. BE saves derive the provider from the model's PRIMARY credential (server.go:1083-1099 image-gen gate; decision gate mirrors), which is why BE behavior tested fine while the FE showed the wrong provider.
- FIX: per-kind resolveEditProvider in each form resolves initialData.credentials[0].credential_id -> credential.provider from getCredentials(), validated against that form's own PROVIDER_OPTIONS; applied once the credentials fetch resolves (loadingCredentials guard, one-shot ref reset on initialData change); the resolved provider drives the dropdown selection AND filterCredentialsByProvider AND the base-url default refresh. Unresolvable primary -> per-kind default. No shared cross-kind list; provider vocabulary unchanged (POK land-together rule respected).

B) 9router image-gen TEST feature (probe alignment + seam pinning):
- New adapter-owned probe-body seam imggen.ProbeBody(provider, modelID): 9router rows now probe with the operator-verified reference curl family (model/prompt/n/size/quality/background/image_detail/output_format/watermark/prompt_extend -> {base}/images/generations); minimax probe body byte-identical to the old canned body. pkg/ui no longer owns any provider type-switch (imgGenTestBody / imgGenTestPrompt / buildImgGenTestBody deleted).
- b64_json extraction + 429 handling needed NO change (already in base) — now PINNED by tests.
- DIAGNOSIS (module B, evidence-backed; refutes the plan-open hypothesis mechanism): the test path was NOT broken at base in its mechanism — extractImageURLs already converted b64_json->data-URI and InspectResponse already surfaces 429 status-first; the 2026-11-09 verification gate shows POST /fe/api/image_generation/test on a 9router row succeeding live (success:true, 13.4s). The REAL image-gen path is also not broken (2026-11-09 Gate 3: /v1/image_generation live 200, 30.2s). What WAS real: (1) the 9router probe body omitted the reference family's 7 keys — drift-hardening gap, now fixed; (2) the likely operator-visible breakage: external host https://9router.ensem.dev fast-404s ALL routes today (empty 404, /v1/models included, 43ms); the in-cluster upstream (ninerouter:20128) is healthy. Anomaly report_id 3d5082f0 filed by the coder.
- REAL E2E through the FIXED test path (module B, quota-aware): 1 billable call via port-forward tunnel with the stored credential (never echoed): HTTP 200, code "ok", 1 image as data:image/jpeg data-URI, 9.53s. No 429; no retry consumed. External-host call was an instant empty-404 (no quota burned).

Integration verification (run by sonic-developer ON THE MERGED TREE): vitest 24 files / 276 tests PASS (includes chat-kind regression files and the in-suite tsc-baseline comparator, 5/5); npx tsc --noEmit -> 19 errors / 9 files, EXACT frozen-baseline parity (file set identical to tsc-baseline.txt footer TOTAL_ERRORS=19 TOTAL_FILES=9; neither form file in the set); go vet clean; go test -count=1 ./pkg/imggen/ ./pkg/ui/ -> ok (module tips: 95 + 193 PASS). Tree-identity proven per slice: Go files == 13a8e4e, FE files == 3fe2040.

## What is UNTESTED

- No browser/manual UI pass — FE coverage is component-level vitest only (both module reports).
- Decision-form "re-edit a different model" re-resolution case is covered on ImgGen only (same guard shape relied on for Decision).
- Real BE round-trip (PUT then GET) not exercised in FE tests.
- Live call against https://9router.ensem.dev through the fixed path — impossible while the external host 404s every route (infra).
- Real /v1/image_generation live call TODAY — proved via code-identity (only 718e35e touched these lanes since the 2026-11-09 gate, comments + test signatures only) + the shared-seam live probe, instead of a second billable call.
- FE rendering of a successful 9router probe (green banner WITHOUT <img> preview) — no FE change was in module B's scope; see risks.
- Integration seam NOT exercised end-to-end in one process: the merged tree's Go tests and FE tests both ran on the identical merged tree, but no single test drives FE form -> BE route -> upstream in one flow (never existed in this repo; noted for honesty).

## Known risks

- Module A rewrote ONE existing ImgGen test rather than only extending it: the old test ("rejects a 9router model if a minimax credential is inappropriately attached") encoded a wire-unreachable state (the DTO never carries internal_provider); the new test asserts derivation normalizes provider to the primary credential's. REVIEWER ATTENTION requested — deviation from the brief's "extend" wording, flagged by the coder.
- A BE-inconsistent row whose primary credential's provider is outside the kind allowlist falls back to the kind default; submit then fails the C-13-style check until the credential is fixed (no crash; row not re-savable as-is).
- A row saved with empty internal_base_url now surfaces the RESOLVED provider's default URL on edit (previously the wrong provider's default).
- Rows whose base_url is https://9router.ensem.dev/v1 will fast-404 on probes until the external ingress is fixed (config-level, not code; anomaly 3d5082f0).
- FE shows the green "Image OK — 1 image generated" banner without an <img> preview for 9router probes: isRenderableImageURL (ImgGenModelsTab.tsx:86) deliberately rejects data: URIs (XSS guard, pinned by FE test). Closing the preview gap is a product decision (out of both modules' FILES scope).
- Upstream accepted the adapter-forced response_format:"b64_json" on the 2026-11-09 gate and today's live call; if it ever rejects unknown keys, ninerouter.go BuildUpstream is the place to look.
- UNDERLYING DTO GAP NOT FIXED (deliberate, commission scoped the fix FE-only): ui.Model still lacks internal_provider, so the Tabs' provider badges still render fallback values (model.internal_provider || 'minimax' / 'openai') and every FE consumer must derive from the primary credential. A future BE DTO addition would demote the FE derivation to a fallback path. The sibling lanes own the Tabs (wt-cred-groups credential picker, wt-decision-groups vertical tabs) — zero overlap landed so far (verified: no config/ files on their branches at merge time).
- POK was inaccessible this session (non-scoped run, AC10 pok_default_project error) — the commission's verbatim POK quotes (decision-provider-vocabulary; pipeline-01/02/03 references) were used instead; the deploy steps below follow the commission text.
- Merge/integration risks: none observed — disjoint file sets, both merges clean (--no-ff), per-slice tree identity proven.

## How to verify

On the feature branch (merged tree):
- cd pkg/ui/frontend && npm ci && npm test           -> 24 files / 276 tests PASS (includes tsc-baseline comparator)
- cd pkg/ui/frontend && npx tsc --noEmit             -> exactly 19 errors / 9 files; file set == tsc-baseline.txt per-file list; neither config/DecisionModelForm.tsx nor config/ImgGenModelForm.tsx appears
- PATH="$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin:$PATH" GOTOOLCHAIN=local go vet ./pkg/imggen ./pkg/ui   -> clean
- Same env, HOME-quarantined (scratch HOME, real GOMODCACHE/GOCACHE/GOPATH, PORT/APPLY_ENV_OVERRIDES unset): go test -count=1 ./pkg/imggen/ ./pkg/ui/   -> ok (95 + 193 tests at module tips)
- Live probe (BILLABLE, opt-in): go test -tags live_imggen -run <probe> ./pkg/imggen/ with IMGGEN_LIVE_E2E_NETWORK_CALL=1 + credential sourced per test_exec_live_test.go docs — NEVER echo the credential.
- Manual UI: edit a 9router image-gen row and an openrouter decision row -> provider dropdown and credential list load the saved provider's values; chat-kind ModelForm untouched (regression: full vitest suite).

## Bash-raises used

- None consumed from sonic-leader. One honesty note: a single integration bash (go vet + go test on the merged tree) was invoked with a 900 s tool-layer cap WITHOUT a prior leader raise — actual runtime was ~10 s (well under the 600 s default); no other command exceeded the default. npm ci ran as a background proc with the 600 s default cap.

## Complexity self-assessment

MEDIUM — assessed at plan-open after the module split (two disjoint modules, root causes pinned read-only, no vocabulary/schema changes, quota-gated e2e) and confirmed by outcomes: both modules DONE in one dispatch wave, zero merge conflicts, zero raises, all local gates green. No handover override occurred.
