# HANDOFF — model-skipcache-ttl (rev 2: re-dispatch #1, max-1 fix round)

HANDOFF_PATH = .agents/shared/planning/sonic-team/model-skipcache-ttl/HANDOFF.md
Feature branch: `fix/model-skipcache-ttl` (base `origin/latest` a2952a9 → rev1 ship d22f697 → rev2 fix tip)
Commission: model-layer skip-cache bug investigation + TTL cap (USER OVERRIDE of project routing — see §7)
Verdicts that drove rev2: TEST: PASS (sonic-tester 2267a03f) · REVIEW: NEEDS-REVISION (sonic-reviewer b9fc4ab1, F1+F2) · both fixed in this round.

## Files touched across modules

**Module A — mscttl-modelcache** (`5fa87db`, merged rev1): `pkg/modelcache/modelcache.go` (DefaultTTL 15m→30m), `pkg/modelcache/policy.go` (ApplyWall caps parsed-reset at the knob; floor 1m; knob clamped [1m,24h]), `pkg/modelcache/modelcache_test.go` (+7 tests).
**Module A-fix — mscttl-f2-modelcache** (`968acf4`, merged rev2): `pkg/modelcache/modelcache_test.go` — F2: the four absolute-reset fixtures anchored in UTC (`now := time.Now().UTC().Truncate(time.Second)` at the CappedAtKnob / SoonerReset / Floor / Expiry sites). Anchoring the VARIABLE, not the format call, is load-bearing: a `.UTC()` render with a local `now` would flip the window arithmetic negative.
**Module B — mscttl-config** (`4fb9b16`, merged rev1): `pkg/config/config.go` (default 30m; fallback+cap comments), `pkg/config/config_test.go` (+3 tests).
**Module B-fix — mscttl-f1-store** (`82a58c6`, merged rev2): `pkg/store/database/store.go` — F1: merge-seam comment `s/15m/30m/` (1 line; presence-detection at :219 untouched). Cross-module doc reference the 3-way split missed; sweep found no other stale model-layer "15m".
**Module C — mscttl-pins** (`668b52a`, merged rev1): `pkg/proxy/model_skipcache_ttl_test.go` (NEW, 2 tests).

Net rev2 delta vs rev1 ship: 2 files, +5/−5 — zero production behavior change (comment + test-render only).

Not touched: `ParseReset` production (stop-gate), `MarkUntil` never-shorten, vocabulary, suppression sites, win-clear, `MODEL_FALLBACK_*`, `pkg/skipcache` (60s), quota cooldown (15m), group cooldown, request-hash binding lane, group pick.

## What changed

The model-layer mark window is `min(parsed reset window, configured model_skip_cache_ttl)` with floor 1m and the 24h hard ceiling on the knob. A sooner stated reset is honored exactly; a no-parse wall uses the knob (default now 30m). `model_skip_cache_ttl` is documented and behaves as BOTH fixed fallback and cap; `Validate` bounds unchanged [1m, 24h]. Previously a parsed reset was clamped only at 24h — a 5-hour plan wall benched a model for ~5h. Rev2 corrects one stale cross-module doc claim (F1) and makes the absolute-reset test fixtures TZ-portable (F2) so `make test` is green on non-UTC hosts (the operator is UTC+7).

## What is UNTESTED

- `go test -race` was not run in either round.
- FE gate not run (no FE files touched).
- Live-cache introspection: no endpoint exists; post-deploy evidence is indirect (suppression cadence in pod logs + knob readback).
- The timezone question is UNPROVEN in production: the minting body that benched agentic-Main/quick-main rotated out of the kubelet log. `ParseReset` still parses absolute strings as UTC by documented assumption (stop-gate held).
- The Token-Plan mint-scope question (account body → model mark) is untested by design — adjudication-owned.
- No change needs a store/database behavioral test: F1 is comment-only (Pack A re-ran the full store suite regardless: ok 38.6s).

## Known risks

1. Mint scope (rev1, still open): `Token Plan usage limit reached … purchase Credits` mints a MODEL mark by design (pinned by `TestModelWall`); at 03:56:55Z it suppressed the same request's credential-failover attempt. Semantics change → user ruling needed.
2. The fixed fallback rose 15m→30m: the no-parse class (Token Plan bodies) is benched twice as long as before. Ruled policy; the real fix for that class is risk #1.
3. `ParseReset` UTC assumption unfixed (stop-gate): a UTC+8 upstream emitting local absolute resets still overshoots — now bounded by the 30m cap. Follow-up: capture a z.ai reset body and add an explicit zone-aware parse.
4. Relative resets are capped too (a `reset after 62h` body now ≤30m, was 24h) — intended, but a genuine multi-day outage re-attempts every 30m.
5. In-memory marks die only on process restart — the rev1 deploy already flushed both hosts; rev2's redeploy re-flushes.
6. dcs GitOps pins were NOT pushed: prepare-only (see §7 report). Until the user authorizes the dcs push, any apply of `infranstructure/k8s/llmproxy/rancher-local-ensem-dev/deployment.yaml` or a helm reuse of `values-pinned.yaml` rolls a host back to `8d434ba` and re-opens the live bug.
7. Process note: the plan-open `explore()` timed out (no KB payload); `pok_query` + blueprint carried the briefs instead.

## How to verify

On the merged tree (fresh sibling off the branch, or the integration worktree):

```
export PATH=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin:$PATH
export GOTOOLCHAIN=local GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build HOME=$(mktemp -d)
gofmt -l pkg/modelcache pkg/config/config.go pkg/config/config_test.go pkg/proxy/model_skipcache_ttl_test.go pkg/store/database/store.go   # empty
TZ=Asia/Ho_Chi_Minh go test -count=1 ./pkg/modelcache/ ./pkg/config/   # ok (rev2 F2 proof)
TZ=Etc/UTC           go test -count=1 ./pkg/modelcache/ ./pkg/config/   # ok
TZ=Asia/Ho_Chi_Minh go test ./pkg/proxy/ -run 'TestModelSkipCache_' -v  # 2× --- PASS
timeout 300 go test -count=1 -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...        # Pack A EXIT 0
timeout 300 go test -count=1 -timeout=240s $(go list ./... | grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)')  # Pack B: only the 2 quarantined pkg/mcp httpbin tests may fail (QUARANTINE.md row 11)
```
Rev2 gate results (this tree): Pack A EXIT=0 (9 pkgs ok; proxy 130.7s, store/database 38.6s); Pack B 38 ok, sole FAILs = the quarantined pair; TZ proofs green in both zones; gofmt clean on all 7 touched files.

Post-deploy: digest-bound pods, healthz 200, `model_skip_cache_ttl=30m0s` on both hosts, suppression count 0, agentic-Main winning.

## Bash-raises used

none (both rounds). All synchronous bash calls stayed under the frozen 600s rule; Pack A/B gates and the rev1 `make test` ran as background `proc` jobs (auto-kill ≤1500s/700s), so no `[sonic-bash-rule-v1]` raise was consumed.

## Complexity self-assessment

**medium** (unchanged across rounds; rev2 is a 5-line doc/test correction on a shipped, tested change — no complexity escalation).

**ROUTING OVERRIDE (recorded verbatim, original rule kept visible).** Project metadata for llm-supervisor-proxy states: *"ALL llmproxy work routes to the EXPERT team (agent_id=leader) — NEVER sonic-leader/sonic-developer"*, scope *"every commission, hotfix and read-only investigation"*, source *Emperor directive 2026-11-16*, criterion *"CRITICALITY, not complexity. Do not downgrade a task because it looks simple or small."* The user explicitly authorized **this** commission on the sonic team for speed (2026-11-17) and instructed that the override be recorded with the original rule visible. The verdict above is the honest sonic-lane assessment; it does not downgrade the criticality of the substrate.

**Root-cause verdicts from live evidence** (primary pod, image tag `8d434ba`, log window 00:32–04:53Z, 65,406 lines):
- (a) self-refreshing mark — **DISPROVEN** (synthetic error classifies `credFailoverNone`, matches no wallMarker; 244 suppressions with no re-mark).
- (b) reset-parse timezone — **leading hazard, unproven for this incident** (minting body rotated out; every long-benched model sits on the UTC+8 z.ai cred). Follow-up, not a guess.
- (c) TTL from wrong window — **disproven as stated, corrected**: the literal "5 hour" is only a classifier, but the body's `reset at …` instant *is* parsed and became the TTL (~5h, ≤24h) — the actual over-suppression, now capped.
- (d) scope mismatch — **DISPROVEN** (row identity on both key sides).
- Observed: agentic-Main suppressed >4h11m with **zero provider calls** while the live knob was 15m; quick-main 02:26:38→04:53:00; the 04:11:40Z symptom reproduces verbatim. Post-rev1-deploy proof: suppressions 0 on both hosts, glm-5.3/-flash attempts resumed, agentic-Main won 15 requests in 8 minutes.
