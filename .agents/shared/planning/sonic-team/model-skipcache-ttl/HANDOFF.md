# HANDOFF — model-skipcache-ttl

HANDOFF_PATH = .agents/shared/planning/sonic-team/model-skipcache-ttl/HANDOFF.md
Feature branch: `fix/model-skipcache-ttl` (base `origin/latest` a2952a9, tree-identical to `origin/master` 8d434ba)
Commission: model-layer skip-cache bug investigation + TTL cap (USER OVERRIDE of project routing — see §7)

## Files touched across modules

**Module A — mscttl-modelcache** (`fix/mscttl-modelcache` @ 5fa87db, merged 406d737)
- `pkg/modelcache/modelcache.go` — `DefaultTTL` 15m → 30m + doc.
- `pkg/modelcache/policy.go` — `ApplyWall` caps a parsed-reset window at the configured knob; no-parse path uses the knob; floor/ceiling doc.
- `pkg/modelcache/modelcache_test.go` — +7 tests (cap, sooner-reset, no-reset knob, floor, knob ceiling, expiry/retry, live-body classification pin).

**Module B — mscttl-config** (`fix/mscttl-config` @ 4fb9b16, merged 3e18886)
- `pkg/config/config.go` — `ModelSkipCacheTTL` default 15m → 30m; field/Defaults/Validate comments now state "fixed fallback AND cap".
- `pkg/config/config_test.go` — +3 tests (default 30m, Validate bounds, other-knobs-unmoved).

**Module C — mscttl-pins** (`fix/mscttl-pins` @ 668b52a, merged ea32a36)
- `pkg/proxy/model_skipcache_ttl_test.go` — NEW file, 2 tests.

Not touched: `MarkUntil` never-shorten, `Mark`/`Marked`/`Until`/`Clear`, wall/throttle/veto vocabulary, the suppression sites, win-clear, `MODEL_FALLBACK_*` vocabulary, `pkg/skipcache` (upstream 60s), credential quota cooldown (15m), group member cooldown, request-hash binding lane, group-pick policy.

## What changed

The model-layer mark window is now `min(parsed reset window, configured model_skip_cache_ttl)` with floor `MinMarkWindow` (1m) and the 24h hard ceiling still applied to the knob itself. A stated reset sooner than the cap is honored exactly; a wall body with no parseable reset uses the knob (default now 30m). Previously a parsed reset was clamped only at 24h, so a 5-hour plan wall put a model on the bench for ~5h.

`model_skip_cache_ttl` is now documented (and behaves) as BOTH the fixed fallback window and the cap. `Validate` bounds are unchanged: [1m, 24h].

## What is UNTESTED

- **Live post-deploy behaviour**: no in-memory introspection endpoint exists, so the deployed cap can only be evidenced indirectly (suppression cadence per model in pod logs after the rollout + process restart clearing old marks).
- **`go test -race`** and a full `pkg/proxy` race run were not executed; `make test` (no `-race`) passed `pkg/proxy` 133.8s.
- **The timezone hypothesis (§ Known risks) is UNPROVEN**: the body that minted agentic-Main/quick-main's long mark rotated out of the kubelet log; `--since=9h` returned only 00:32Z onward.
- **The mint-scope question (account-plan body → model mark) is untested by design**: changing it is a mark-write semantics change and was left to adjudication (STOP GATE honoured).
- **Config source on deploy**: the live primary returned `model_skip_cache_ttl=15m0s`. Whether that is the resolved default or an explicit stored value is not distinguishable from `GET /fe/api/config`; if the DB row is explicit, the deployed cap stays 15m (tighter, still bounded) and the new 30m default only applies to fresh configs.
- No FE change; FE gate not run (not required).

## Known risks

1. **The cap does not fix mint scope.** Live 2026-11-17 evidence: an ACCOUNT/plan body — `openai: Token Plan usage limit reached: Upgrade your Token Plan or purchase Credits for more usage. (2056)` — classifies as model-attributable by design (`TestModelWall` asserts `"Token Plan usage limit"` → attributable) and mints a MODEL mark. At 03:56:55Z this suppressed the *same request's* credential-failover attempt (coding4, cred minimax-company → failover to minimax-mindspath → `attempt 1 model coding4 suppressed by model-layer skip cache`), i.e. the failover credential was never tried. Fixing that changes mark-write semantics → adjudication-owned follow-up.
2. **Raising the fixed fallback 15m → 30m doubles the bench window for the no-parse class** (the Token Plan bodies above). This is the ruled policy, but it is a real operational trade-off and must be stated: for that class the change is a regression in blast radius even as it fixes the parsed-reset class.
3. **`ParseReset` parses absolute reset strings as UTC by documented assumption.** z.ai (the credential behind both long-marked models) is a UTC+8 provider. If it emits local time, an 8h overshoot is possible; the cap now bounds any such overshoot to 30m, but the parse assumption itself is unfixed.
4. **Relative resets are now capped too**: a body like `reset after 62h 45m 6s` previously produced a 24h mark, now ≤30m. Intended per ruling, but a genuine multi-day outage will re-attempt every 30m.
5. **Hidden long marks vanish only on process restart** (in-memory map): the deploy is itself the flush. A rollout that does not restart the pod would leave pre-existing long marks live.
6. **A sooner-than-cap reset is honored exactly** — so a pathological upstream emitting `reset in 1s` re-attempts every minute (floor), not immediately. Accepted per ruling.
7. Investigative residue: `explore()` (the one bounded knowledge call at plan-open) **timed out after 300s with no payload**, so no stored KB experience reached the briefs; `pok_query` and the project blueprint were used instead.

## How to verify

On the merged tree (`.agents/…` worktree or a fresh sibling off the feature branch):

```
export PATH=/home/nea/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin:$PATH
export GOTOOLCHAIN=local GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod GOCACHE=/home/nea/.cache/go-build
export HOME=$(mktemp -d)
gofmt -l pkg/modelcache pkg/config/config.go pkg/config/config_test.go pkg/proxy/model_skipcache_ttl_test.go   # empty
go test ./pkg/modelcache/ -v                     # 8 tests incl. the 7 added
go test ./pkg/config/ -v                         # includes the 3 added
go test ./pkg/proxy/ -run 'TestModelSkipCache_' -v
go test ./pkg/modelcache/ ./pkg/config/ ./pkg/proxy/   # package-level gate
make test                                        # full gate; expected sole FAIL = quarantined pkg/mcp httpbin pair
```

Expected evidence of the new policy (package tests):
- `TestApplyWall_ResetWindowCappedAtKnob` — a 5h reset with knob 30m expires at now+30m.
- `TestApplyWall_SoonerResetHonored` — a 10m reset stays 10m.
- `TestApplyWall_NoResetUsesKnob` — no reset → knob (30m).
- `TestApplyWall_ResetFloorOneMinute`, `TestApplyWall_KnobAboveHardCeilingClamped`.
- `TestCache_ExpiryThenRetryIsImmediate` — expired mark ⇒ `Marked()==false` on the next request.
- `TestModelSkipCache_SuppressedAttemptDoesNotExtendMark`, `TestModelSkipCache_MarkedModelStillPicked_ChainServes`.

Post-deploy (primary, then backup):
```
kubectl -n llmproxy get deploy llm-supervisor-proxy -o jsonpath='{.spec.template.spec.containers[0].image}'
kubectl -n llmproxy logs deploy/llm-supervisor-proxy --since=60m | grep -c "suppressed by model-layer"
kubectl -n llmproxy logs deploy/llm-supervisor-proxy --since=60m | grep "suppressed by model-layer" \
  | awk '{print $4}' | sort | uniq -c      # cadence per model; no model should show a >30m continuous bench
```
Pass condition: the two formerly long-benched models (`agentic-Main`, `quick-main`) resume upstream attempts within ≤30m of the rollout, and no model shows a suppression run longer than 30m.

## Bash-raises used

none. Every synchronous bash call stayed under the frozen 600s rule; the full `make test` gate ran as a background `proc` job (1500s auto-kill), so no `[sonic-bash-rule-v1]` raise was consumed.

## Complexity self-assessment

**medium.** Clamp/policy change plus a config default and test pins across three disjoint modules; no mark-write semantics, no routing/fallback/auth change. The investigation was heavy, the diff is not (311 insertions / 34 deletions over 6 files, 4 of them tests).

**ROUTING OVERRIDE (recorded verbatim, original rule kept visible).** Project metadata for llm-supervisor-proxy states: *"ALL llmproxy work routes to the EXPERT team (agent_id=leader) — NEVER sonic-leader/sonic-developer"*, scope *"every commission, hotfix and read-only investigation"*, source *Emperor directive 2026-11-16*, criterion *"CRITICALITY, not complexity. Do not downgrade a task because it looks simple or small."* The user explicitly authorized **this** commission on the sonic team for speed (2026-11-17) and instructed that the override be recorded with the original rule visible. The verdict above is the honest sonic-lane assessment; it does not downgrade the criticality of the substrate.

**Root-cause verdicts from live evidence** (primary pod `llm-supervisor-proxy-9785fcb45-cr75b`, image tag `8d434ba`, log window 00:32–04:53Z, 65,406 lines):
- (a) self-refreshing mark — **DISPROVEN.** `suppressedAttemptError` is neither `*providers.ProviderError` nor quota/rate-limit, `markUpstreamSkipLocked` early-returns on `credFailoverNone`, and the synthetic string matches no `wallMarker` (verified by the added `TestModelWall_LiveBodies_ClassificationPin` sibling cases + 244 observed suppresssions with no re-mark).
- (b) reset-parse timezone — **leading hazard, unproven for this incident**: every long-benched model sits on the UTC+8 z.ai credential, but the minting body is rotated out of the log; reported as a follow-up, not asserted.
- (c) TTL from wrong window — **disproven as stated, corrected**: the literal "5 hour" is only a classifier, BUT the accompanying `reset at …` instant *is* parsed and became the TTL (~5h, ≤24h). That is the actual over-suppression and is what this change caps.
- (d) scope mismatch — **DISPROVEN** (row identity on both the mark and the read key).
- **Actual observed root cause of the symptom**: model-layer marks carried parsed-reset windows of hours. `agentic-Main` was suppressed continuously for >4h11m with **zero provider calls** in the window (`grep -c "Race attempt .*model=agentic"` = 0) while the live knob was 15m; `quick-main` 02:26:38→04:53:00. The exact 04:11:40Z symptom reproduces in the log: agentic-Main suppressed → synthetic 429 → fallback coding4 also suppressed → agentic-go served.
