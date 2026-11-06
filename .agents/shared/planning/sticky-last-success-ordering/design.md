# Design: Sticky Last-Success Ordering for the Proxy Model Race

**Branch:** `feature/sticky-last-success-ordering` @ `63df316` (worktree `llm-supervisor-proxy-wt-sticky-last-success-ordering`)
**Revision:** v2 — reviewer verdict applied (B1 blocking fix in §5.2; 🟡-1 §8.12 reword; 🟡-2 FE delta mandatory §5.3; 🟢 doc pass throughout). All other sections unchanged in substance from v1.
**Status:** Design doc only — no feature code in this document's scope.
**Verification method:** every load-bearing claim verified against the worktree — architect spot-checks plus two dispatched design workers; v2 additionally re-verified the reviewer's claims. Receipts marked **[A]** (architect re-read), **[W-A]** (structural worker), **[W-B]** (data-flow worker), **[R]** (reviewer claim, architect-confirmed).

---

## 1. Overview

At race start, if a last-success model is recorded for the request's conversation key, **move it to index 0** (stable reorder — every other model keeps its original relative order). Goal: first-shot hit on the freshest provider prompt cache → lower latency, higher cache-hit rate, lower cost.

**Scope locks (from the leader, honored throughout):**
- Sticky key = the EXISTING `rc.conversationKey` / `arc.conversationKey` verbatim. Never a second hash. Empty key ⇒ skip sticky entirely (same gate the LB engines use).
- Value = the model ID that actually **delivered success** — never `race_winner_selected` (first-byte in live mode) and never `race_deadline_pick` (best-partial artifact).
- Choke points = post-group-rewrite, pre-coordinator-construction. Slice discipline: reordered copy assigned back.
- New package `pkg/stickyorder` following the credentiallb binding-registry house pattern; in-memory only.
- Scope is EXACTLY race-chain ORDER: no upstream changes, no chain-membership changes, **no group-LB coupling**.
- Worst case degrades to today's behavior (gate off / nil engine / empty key / no record / TTL expiry / invalidation ⇒ un-reordered racing — byte-identical **on the request path**; the only non-request-path delta is the engine construction + janitor goroutine at boot).

**One locked-decision refinement (correctness, not a challenge):** the leader's open item asked to "verify the exact field" for the Anthropic served identity (`arc.reqLog.Model` / `RouteModel`). Verified: **neither field works** (§4.4). The design adds a minimal `arc.servedModel` field set at walk-top. This is the resolution of that open item, inside the locked frame.

---

## 2. Verified receipts

| # | Claim | Receipt |
|---|-------|---------|
| R1 | Key = `ComputeConversationKey(modelID, tokenID, leadingUserRun)` = 64-hex SHA-256 of `modelID\|tokenID\|leadingUserRun`; hash covers the **leading USER-message run only — system content EXCLUDED**; run ends at first assistant/tool msg; empty ⇒ `""` | `pkg/credentiallb/key.go:48-50` **[A]**, `:91-128` (system exclusion `:102-104`) **[W-B]** |
| R2 | Key computed once per lane at `handler.go:569-573` (`rc.resolvedModel.ID` = TOP-LEVEL requested id — the GROUP id for group requests) and `handler_anthropic.go:334`; empty-run gates at `handler.go:563-566` / `handler_anthropic.go:329-331` | **[A]** grep-anchored, **[W-B]** read |
| R3 | Group rewrite fixes the grouplb sticky member INSIDE `RewriteGroupEntries` (`pickServableGroupMember(row, conversationKey)` at `store_group_rewrite.go:61`), executed at `handler.go:594` and `handler_anthropic.go:357` — all BEFORE any reorder point; execution list = fresh slice with group entry **replaced** by `[member.ID] + member.FallbackChain` (`:73-74`); no group ID survives (production DB backend) | `handler.go:593-613` **[A]**; `store_group_rewrite.go:45-81` **[W-A/W-B]** |
| R4 | Coordinator constructor **aliases** (not copies) the model list: `models: models` at `race_coordinator.go:318`; 11-param signature `(:294-306)` ending `engine, conversationKey, liveFirstByteGate`; `race_started` payload carries `{"models": c.models}` at `:360-362`; `len==0` ⇒ `cfg.ModelID` fallback `:307-309` | **[A]** (`:294-333`), **[W-A]** (`:360-362`) |
| R5 | Success sites (OpenAI): stream `handler.go:1745-1751` (`"model": winner.GetModelID()` at `:1748`), non-stream `:1989-1994` (`:1991`); `rc` (incl. `rc.conversationKey`, `rc.conf`) in scope in both | **[A]** read both |
| R6 | `race_winner_selected` = first-byte eligibility (`buffer.TotalLen() > 0` under `liveFirstByteGate`), emit `race_coordinator.go:623-630`; deadline path emits `race_deadline_pick` at `:976-983` — neither proves delivered success | **[W-A]** |
| R7 | Attempt failure funnel (OpenAI): `executeRequest` at `race_coordinator.go:1156` → `req.MarkFailed(err)` at `:1159` → `[RACE]` log `:1166` → `race_request_failed` payload with `"model": req.modelID` at `:1172-1179`; `c.conversationKey` (field `:185`) in scope | **[A]** (`:1145-1183`) |
| R8 | Cred-failover exhaustion `:1780-1806`: every underlying attempt already flowed through the R7 hook — a separate exhaustion hook is redundant | **[W-B]** |
| R9 | Anthropic walk: `loop:` label at `:395`, outer `for {` `:396`, `for modelIndex, currentModel := range arc.modelList` at `:397`; attempt `:457`; `if success { return }` `:458-459`; failure restore block `:462-467`; failure arm `arc.reqLog.Status = "failed"` `:477-478`; client-disconnect `break loop` `:402-405` and headersSent `break loop` `:418-420` are loop-level aborts, NOT model failures | **[A]** (`:390-484`) — anchor corrected in v2 |
| R10 | `finalizeAnthropicSuccess` `:1597-1619` takes only `arc`; sets `Status="completed"` `:1614`, `h.store.Add` `:1617`, publishes `request_completed` `:1618`; it is the ONLY completed-site funnel — all 8 call sites (`:999/:1031/:1075/:1107/:1266/:1482/:1854/:1929`) converge there; several `success==true` paths (client disconnect, headers-sent partials) return WITHOUT reaching finalize | **[A]** (`:1595-1619`), **[W-B]** call-site census |
| R11 | `arc.reqLog.Model` = client-facing requested id, set once `:138`, never updated in the walk (GROUP id for group requests) — WRONG source; `arc.reqLog.RouteModel` (`memory.go:146-151` = group-served member) is set at FIRST group pick (`:382`, `handler_groups.go:397-401`) — also wrong when the walk falls to a fallback member | **[W-B]** |
| R12 | House pattern: plain maps + explicit `RWMutex`, NO `sync.Map` (`credentiallb/doc.go:54`); bindings map + sliding TTL refresh-on-hit (`engine.go:344-345`, `:258/:274`); janitor `runJanitor` `:129` / `sweepOnce` `:149` with `defer/recover` `:150-152`; `stopCh`/`sweepDone`/`stopOnce` `:49-51`; consts `DefaultBindingTTL = 24h`, `DefaultSweepInterval = 5m` (`engine.go:17-18`); no-rehydration ruling `grouplb/engine.go:106-109` | **[W-B]** |
| R13 | Config clone source: field `config.go:80`, interface `:143`, default `:236`, env override block `:490-492` (`RACE_RETRY_ENABLED` case at `:490`, assignment `:491`), getter `:748-752`; snapshot build `handler.go:39-63` (per-request `Config.Clone()`), field `:49`/`:74`; `rc.conf`/`arc.conf` carry it to all choke points | **[A]** (`:485-494` read; anchor corrected in v2), **[W-A]** |
| R14 | Merge gotcha: `mergeConfig` (`store.go:86`) starts from `existing`; race family merged ONLY under `if isRaceRetryProvided(incoming)` (`:118-123`); `isRaceRetryProvided` `:204-213` = sibling-nonzero OR — a boolean `false` is indistinguishable from "not sent"; the family block copies ALL FOUR race fields from `incoming` when it fires | **[A]** (`:84-128`) |
| R15 | FE sends genuinely partial payloads: `SettingsPage.tsx:291-294` PUTs only loop-detection; the proxy-Apply payload carries the full race block (`:190-211`, `race_max_parallel` non-zero) but NO sticky key today; `handleConfig` GET whole-struct (`ui/server.go:555-558`), PUT decodes into a FRESH zero-filled `config.Config` (`:560-568`, decode `:564-565`) — absent fields arrive as zero values | **[A]** (`:553-568`), **[W-A]** |
| R16 | Duplicates constructible at choke points: no dedup in chain build (`handler_functions.go:214-216` = `[resolvedModel.ID] + FallbackChain`); validation caps length but has no self-reference ban (`models/config.go:1030-1038`) → `[A, A]` writable; group expansion appends unconditionally (`store_group_rewrite.go:73-74`); G4 dedups only within one group (`models/group.go:163-167`) | **[W-A]** |
| R17 | `modelTypeSecond` is a spawn TYPE (`case` block `race_coordinator.go:387-395`) resolving to `c.models[0]`, not an independent list entry; coordinator list frozen post-construction | **[W-A]**; anchor **[R]**-corrected v2 |
| R18 | DI: `credLB := modelsMgr.Engine()` `main.go:135`; `proxy.NewHandler(...)` — 7th positional arg, sole production call `main.go:260`; `Handler.credEngine` field `handler.go:116` set `:141`; constructor-only injection documented `handler.go:1128-1133`; `defer credLB.Stop()` `main.go:186-188` (LIFO discipline `:182-185`) | **[A]** (`:131-144`, `:255-266`, `:178-193`, call-site census) |
| R19 | Anthropic re-entry (`tryGroupReentryAnthropic` `handler_groups.go:380-401`) rebuilds `arc.modelList` at `:395` from `modelListOriginal` and restarts the inner walk WITHOUT re-passing the `:357` rewrite preamble — explicit re-application at (d) is structurally MANDATORY | **[A]** (`:380-401`) |
| R20 | Non-coupling: credentiallb cooldowns are CREDENTIAL-id-keyed inside per-model state (`engine.go:695-698`); sticky is conversation-keyed — disjoint keyspaces | **[W-B]** |
| R21 | Race-family live readers: `race_coordinator.go:674` `if running < c.cfg.RaceMaxParallel` (spawn gate — 0 ⇒ no parallel/second/fallback spawns ever); `:447` `newUpstreamRequest(..., c.cfg.RaceMaxBufferBytes)`; `:779` `c.cfg.RaceParallelOnIdle` idle-spawn gate — zero-clobbering these siblings is a silent behavior regression, not a cosmetic one | **[R]** claim, **[A]**-confirmed (`:668-681`, `:443-452`, `:774-783`) |
| R22 | Mid-attempt cancels DO surface as attempt failures: `race_executor.go:638-639` (`ctx.Done()` → `return ctx.Err()`) and `:646-647` (`IsCancelled()` → `return context.Canceled`) — these errors return through `executeRequest` (`race_coordinator.go:1156`) into the `MarkFailed` arm (`:1159`) where the invalidation hook fires | **[R]** claim, **[A]**-confirmed (`:632-648`) |
| R23 | Ultimate-path `request_completed` emit at `handler.go:1060-1065` (`"ultimate_model": true`, model = `ultimateModelID`) — a completed-event that is NOT a race delivered-success and whose model is NOT in the raced list | **[R]** claim, **[A]**-confirmed |
| R24 | Stream client-write-fail early-return at `handler.go:1183-1195`: `streamResult` error ⇒ `[STREAM] Client write failed` log, `coordinator.cancelAllExcept(winner)` `:1192`, `return` `:1195` — control never reaches a success publish for that path | **[R]** claim, **[A]**-confirmed |
| R25 | `handleModelFailure` (`handler_finalize.go:46-75`) has no production consumer in this design's paths — production failure handling is coordinator-side; likewise the `newRaceCoordinator` 6-arg wrapper (`race_coordinator.go:276-277`) is a TEST-ONLY convenience (production constructs via `newRaceCoordinatorWithEvents`, `handler.go:1157`; direct `newRaceCoordinator(` uses appear only in `*_test.go`) | **[R]** claim, **[A]**-confirmed (`:44-84` read + grep census) |
| R26 | FE embed discipline: `TestEmbedStatic_AllAssetReferencesResolve` (`pkg/ui/embed_regression_test.go:67`) pins that all asset references in the embedded `pkg/ui/static` resolve — FE source changes require regenerated content-hashed bundles committed in the SAME commit or the UI 404s | **[R]** claim, **[A]**-confirmed (test located) |
| R27 | Test-handler churn surface: 48 in-package `NewHandler(` call sites measured in `pkg/proxy/*_test.go` via `= NewHandler(`/`:= NewHandler(` (reviewer counted ~53 including other call forms); exactly ONE production call (`cmd/main.go:260`) | **[A]** grep census |

---

## 3. Component design — `pkg/stickyorder`

### 3.1 Engine

```go
// pkg/stickyorder/engine.go
package stickyorder

const (
    DefaultStickyTTL     = 30 * time.Minute  // sliding idle TTL; justified in §7.1 — NOT a config field
    DefaultSweepInterval = 5 * time.Minute   // house default (credentiallb engine.go:18, grouplb engine.go:39)
)

type winnerEntry struct {
    modelID   string
    updatedAt time.Time
}

type Engine struct {
    mu        sync.RWMutex            // house rule: plain map + explicit RWMutex, NO sync.Map (credentiallb/doc.go:54)
    winners   map[string]winnerEntry  // conversationKey → last delivered-success model
    ttl       time.Duration
    sweepInt  time.Duration
    stopCh    chan struct{}
    sweepDone chan struct{}
    stopOnce  sync.Once
}

func NewEngine(ttl, sweepInterval time.Duration) *Engine
```

- **All methods are nil-receiver-safe no-ops.** One deliberate, documented deviation from the credentiallb shape: it removes the need for nil guards at six call-site families and mirrors the documented nil-`credEngine` seam contract (`handler.go:123-127`).
- Janitor: goroutine + ticker → `sweepOnce()` wrapped in `defer/recover` (panic → `[WARN] [STICKY] janitor sweep recovered from panic` and continue), deleting `now.Sub(updatedAt) > ttl`. `Stop()` via `stopOnce` closes `stopCh`, acks `sweepDone`; idempotent (grouplb twin `engine.go:668-670`).
- No RNG (no selection happens here), no cooldown map, no persistence — **in-memory only; restart = cold start** (house ruling `grouplb/engine.go:106-109`). No rehydration. No boot log line (v2: dropped — the byte-identical-off claim is scoped to the request path; the only gate-off deltas are engine construction + janitor goroutine at boot, outside request handling).
- API (minimal, per the leader's sketch — `Get` name kept):

```go
func (e *Engine) Get(key string) (modelID string, ok bool)
    // Read-only. Does NOT refresh the TTL (§7.1). No-op on "" key / nil receiver.

func (e *Engine) RecordSuccess(key, modelID string)
    // No-op on "" key or "" modelID / nil receiver. Sets/overwrites the winner
    // and refreshes updatedAt (sliding). Logs: [STICKY] success recorded key=%s… model=%s

func (e *Engine) InvalidateOnFailure(key, modelID string)
    // Deletes the entry ONLY when modelID equals the recorded winner; any other
    // failure is a no-op. No-op on "" key / nil receiver. Logs on actual deletion only:
    // [STICKY] invalidated key=%s… model=%s reason=attempt_failure

func (e *Engine) Stop()   // sync.Once, idempotent, nil-safe
```

Log lines live inside the engine so all call sites stay 1–2 lines; key printed as 8-char prefix (precedent `handler.go:575`).

### 3.2 Pure reorder helper

```go
// pkg/stickyorder/order.go
// PromoteToFront returns a NEW slice with the FIRST occurrence of target at
// index 0; all other elements keep relative order. Never mutates the input;
// never changes membership or length. Returns a fresh copy on EVERY path
// (including no-ops), so callers never reason about aliasing.
func PromoteToFront(list []string, target string) []string
```

- No-ops (returning a fresh equal-length copy): `len(list) <= 1`, `target == ""`, target absent, target already at index 0.
- **Always-fresh-copy contract**: the coordinator aliases its input slice (`race_coordinator.go:318`) and `modelListOriginal` aliases the pre-rewrite array (`handler.go:611`, `handler_anthropic.go:376`) — a uniform fresh-result contract removes all aliasing reasoning from the four call sites. Cost: one small alloc per gated request — noise next to the race itself.
- First-occurrence semantics for duplicates (§8.6).

### 3.3 Handler-side orchestrator (one line per choke point)

```go
// pkg/proxy/handler_groups.go (next to applyGroupRewrite)
func (h *Handler) applyStickyOrder(conf *ConfigSnapshot, list []string, conversationKey, reqID string) []string {
    if !conf.StickyLastSuccessEnabled || conversationKey == "" || h.stickyEngine == nil {
        return list
    }
    last, ok := h.stickyEngine.Get(conversationKey)
    if !ok || last == "" {
        return list
    }
    out := stickyorder.PromoteToFront(list, last)
    if !slices.Equal(out, list) {   // moved (target present and not already front)
        log.Printf("[STICKY] request %s: promoted last-success model=%s to index 0 key=%s… from=%v to=%v",
            reqID, last, conversationKey[:8], list, out)
    }
    return out
}
```

Gates, in order: config flag (snapshot, per-request fresh — R13), empty key (same gate as the LB engines, R2), nil engine, no record. **Never calls `ComputeConversationKey`** — the key arrives as an argument, satisfying the locked decision.

---

## 4. Wiring

### 4.1 Choke points (4 sites — all post-group-rewrite, pre-construction/attempt)

| Site | File : line (insert) | Call |
|------|----------------------|------|
| **(a) OpenAI race** | `pkg/proxy/handler.go` — at the TOP of the race loop body, between `for {` (`:1156`) and `coordinator := newRaceCoordinatorWithEvents(...)` (`:1157`) | `rc.modelList = h.applyStickyOrder(&rc.conf, rc.modelList, rc.conversationKey, rc.reqID)` |
| **(b) Anthropic walk** | `pkg/proxy/handler_anthropic.go` — after the group-rewrite if/else closes (`:376-377` swap … block end), before the `loop:` label (`:395`) | `arc.modelList = h.applyStickyOrder(&arc.conf, arc.modelList, arc.conversationKey, arc.reqID)` |
| **(c) OpenAI re-entry** | **Covered by (a) by construction**: `tryGroupReentry` rebuilds (`rc.modelList = newList`, `:1258`) and returns true (`:1296`) into the SAME `:1156` loop, whose top re-applies the orchestrator on the rebuilt list. Idempotent (re-promoting an already-front winner no-ops); an explicit post-`:1258` call would be harmless duplication — optional defense-in-depth, not required. | — |
| **(d) Anthropic re-entry** | `pkg/proxy/handler_groups.go` — immediately after `arc.modelList = newList` (`:395`). **Mandatory explicit site**: the re-entry restarts the inner walk WITHOUT re-passing the `:357` preamble (R19) | `arc.modelList = h.applyStickyOrder(&arc.conf, arc.modelList, arc.conversationKey, arc.reqID)` |

Notes:
- The Anthropic lane has NO race coordinator — it is a sequential fallback walk (`:395-479`); reordering changes walk ATTEMPT order only (index 0 = first attempt), preserving lane semantics.
- Post-rewrite lists contain only concrete member IDs in production (group entries are replaced, never kept — R3). Choke-point order therefore operates on exactly the keyspace the record sites produce (§4.4).

### 4.2 Success recording — delivered success only, with explicit exclusions

**OpenAI lane** — at both race `request_completed` sites, immediately after the `publishEvent` (stream: after `handler.go:1745-1751`, before `return nil` `:1754`; non-stream: after `:1989-1994`):

```go
if rc.conf.StickyLastSuccessEnabled && rc.conversationKey != "" {
    h.stickyEngine.RecordSuccess(rc.conversationKey, winner.GetModelID())
}
```

`winner.GetModelID()` is the CONCRETE raced id: the race runs the post-rewrite member list (`rc.modelList` `:594→:1157`), so the winner is a member id for group requests — exactly what choke-point lists contain (R5).

**Explicit exclusions (v2 — stated, not implied):**
1. **Ultimate-path `request_completed` (`handler.go:1060-1065`, `"ultimate_model": true`)** — NOT a race delivered-success; the ultimate model is not in the raced list (recording it would be a永久 no-op at best) and ultimate switching is a declared non-goal. Never record there.
2. **Stream client-write-fail early-return (`handler.go:1183-1195`)** — `streamResult` failure logs `[STREAM] Client write failed`, cancels the others (`cancelAllExcept` `:1192`) and returns at `:1195` WITHOUT reaching a success publish: nothing is recorded. Not-delivered ⇒ correctly excluded.

**Anthropic lane** — inside `finalizeAnthropicSuccess` (`handler_anthropic.go:1618`, after `h.store.Add`, beside the `request_completed` publish):

```go
if arc.conf.StickyLastSuccessEnabled && arc.conversationKey != "" && arc.servedModel != "" {
    h.stickyEngine.RecordSuccess(arc.conversationKey, arc.servedModel)
}
```

**Never record on** `race_winner_selected` (first-byte, R6) or `race_deadline_pick` (best-partial, R6). `finalizeAnthropicSuccess` is the delivered-success funnel: `Status="completed"` `:1614` is set nowhere else, and `success==true` paths that never reach finalize (client disconnect `:525`, headers-sent partials `:955/:1049/:1464/:1495`) are correctly excluded (R10).

### 4.3 Failure invalidation hooks (flag-gated, matching §8.2)

**OpenAI** — inside the attempt-failure arm, right after `req.MarkFailed(err)` (`race_coordinator.go:1159`):

```go
if c.cfg.StickyLastSuccessEnabled {
    c.sticky.InvalidateOnFailure(c.conversationKey, req.modelID)
}
```

Requires: 12th constructor param `sticky *stickyorder.Engine` (append after `liveFirstByteGate`, `race_coordinator.go:294-306`), field `sticky`, threaded from `h.stickyEngine` at the `handler.go:1157` call; the legacy `newRaceCoordinator` wrapper (`:276-277`, TEST-ONLY per R25) passes `nil` (nil-safe engine ⇒ unchanged test semantics). Both operands in scope (R7). **Cred-failover exhaustion (`:1780-1806`) needs NO separate hook** — every failover attempt is an `executeRequest` failure that already flowed through this site (R8).

**Anthropic** — in the walk failure arm (`handler_anthropic.go:477-478`, after the restore block `:462-467`):

```go
if arc.conf.StickyLastSuccessEnabled {
    h.stickyEngine.InvalidateOnFailure(arc.conversationKey, currentModel)
}
```

**Not** on the client-disconnect (`:402-405`) or headersSent (`:418-420`) `break loop` sites — those are BETWEEN-attempt loop-level aborts, not model failures (R9; and see §8.12 for the mid-attempt cancel case, which DOES invalidate via R22).

### 4.4 Anthropic served identity — the open item, resolved

Neither leader-suggested field is usable (R11): `arc.reqLog.Model` is the client-facing requested id (group id for groups); `arc.reqLog.RouteModel` records only the FIRST group pick. Resolution:

1. Add `servedModel string` to `anthropicRequestContext` (near `conversationKey`, `:1558-1559`).
2. **Set it at walk-TOP, immediately BEFORE the attempt** — inserted between the request-body update block and the attempt:

```go
arc.servedModel = currentModel                      // BEFORE the attempt — see ordering trap below
success := h.attemptAnthropicModel(w, arc, modelIndex, currentModel)   // :457
```

> **Ordering trap (adjudication finding):** setting `servedModel` in the `if success` arm (`:458`) is TOO LATE — `finalizeAnthropicSuccess` is called from INSIDE the `attemptAnthropicModel` call tree (sites `:999/…/:1929`), i.e., before the walk's success line executes. A post-success set would always be empty at record time. Pre-attempt set is correct: on delivered success the last pre-attempt set is exactly the serving model; on failure the stale value is never read (no finalize runs); marshal-failure `continue`s (`:436/:442/:452`) skip the set for never-attempted models; group re-entry overwrites it on the rebuilt walk.

The recorded value is the walk's concrete `currentModel` (a member id for groups) — keyspace-consistent with the choke-point lists and with the OpenAI lane.

**Keyspace asymmetry (document deliberately):** for a group request the sticky KEY is salted with the GROUP id (`rc.resolvedModel.ID`, R2) while the VALUE is a MEMBER id. Slot = `(group, principal, prefix) → member`. Coherent: the raced list under that key contains exactly those member ids.

### 4.5 Construction & DI (mirrors the credLB path — R18, R27)

`cmd/main.go`, adjacent to `:135`:

```go
stickyEngine := stickyorder.NewEngine(stickyorder.DefaultStickyTTL, stickyorder.DefaultSweepInterval)
```

- `proxy.NewHandler` gains an 8th positional arg (after `credLB`): `stickyEngine`; `Handler.stickyEngine *stickyorder.Engine` field (~`handler.go:116` region, set in `NewHandler` `:141` pattern). Constructor-only injection per the house contract (`handler.go:1128-1133`).
- **Churn note (R27):** exactly one production call site (`cmd/main.go:260`); 48 in-package test call sites measured in `pkg/proxy/*_test.go` (`= NewHandler(`/`:= NewHandler(` forms; reviewer's ~53 includes other call forms) — each needs the new trailing `nil` (or an engine, for the sticky wiring tests). Mechanical; do it in the same commit.
- Lifecycle: `if stickyEngine != nil { defer stickyEngine.Stop() }` adjacent to `defer credLB.Stop()` (`main.go:186-188`), preserving the documented LIFO discipline (`:182-185`).
- Nil engine (legacy/test paths) degrades to today's behavior exactly.

---

## 5. Config gate — `StickyLastSuccessEnabled` (json `sticky_last_success_enabled`), default OFF

### 5.1 Clone sites (five, mirroring `RaceRetryEnabled` — R13)

| Site | `RaceRetryEnabled` receipt | New |
|------|---------------------------|-----|
| Field | `config.go:80` | `StickyLastSuccessEnabled bool \`json:"sticky_last_success_enabled"\`` next to it |
| Interface | `:143` | `GetStickyLastSuccessEnabled() bool` |
| Default | `:236` | `StickyLastSuccessEnabled: false` |
| Env override | `:490-492` | `STICKY_LAST_SUCCESS_ENABLED` (`v == "true" \|\| v == "1"`) |
| Getter | `:748-752` | `func (m *Manager) GetStickyLastSuccessEnabled() bool` |

Plus: snapshot build `handler.go:49` + struct field `:74`; every `ManagerInterface` implementor gains the getter (DB `ConfigManager` `store.go:319-323` pattern, `MockConfigManager` `mock_store.go:149-153` pattern, test mocks). JSON backend `Manager.Save` persists the whole struct — zero changes there.

### 5.2 The DB ConfigManager boolean merge — exact mechanism and resolution (v2: B1 fix)

**Mechanism (R14, R15):** `handleConfig` PUT decodes into a FRESH `config.Config` (`ui/server.go:564-565`), zero-filling every absent field; `mergeConfig` (`store.go:86`) merges field families only when an `isXProvided` sibling-nonzero probe fires; for booleans, `false` is indistinguishable from "not sent".

**Rejected v1 hardening (the B1 defect, for the record):** folding `StickyLastSuccessEnabled` into the `isRaceRetryProvided` OR would let an API-only PUT of `{"sticky_last_success_enabled": true}` fire the ENTIRE family block with zero-filled incoming ⇒ silently set `RaceRetryEnabled=false` and `RaceMaxParallel=0` — live readers `race_coordinator.go:674` (0 ⇒ no parallel/second/fallback spawns EVER), `:447`, `:779` (R21). A test asserting only "sticky enabled" would pass while the clobber ships. **Never put sticky in the family OR.**

**Adopted resolution (reviewer shape):**

```go
// store.go mergeConfig — sticky OUT of isRaceRetryProvided entirely.
// (1) Inside the family block (family intent detected ⇒ FE proxy-Apply is
//     authoritative for set AND clear):
if isRaceRetryProvided(incoming) {
    result.RaceRetryEnabled = incoming.RaceRetryEnabled
    result.StickyLastSuccessEnabled = incoming.StickyLastSuccessEnabled   // NEW — rides the family merge
    result.RaceParallelOnIdle = incoming.RaceParallelOnIdle
    result.RaceMaxParallel = incoming.RaceMaxParallel
    result.RaceMaxBufferBytes = incoming.RaceMaxBufferBytes
}
// (2) Standalone true-only enable OUTSIDE the block — API-only PUT can turn
//     the flag ON; it can never fire the family block, so race siblings are
//     never touched by a sticky-only PUT:
if incoming.StickyLastSuccessEnabled {
    result.StickyLastSuccessEnabled = true
}
```

`isRaceRetryProvided` (`store.go:204-213`) stays UNTOUCHED. Resulting behavior matrix:

| PUT shape | sticky after merge | race siblings after merge |
|---|---|---|
| FE proxy-Apply (family non-zero, sticky key present — the MANDATORY §5.3 delta makes this true) | `= incoming` (set AND clear) | `= incoming` |
| `{"sticky_last_success_enabled": true}` alone (API) | `true` | **byte-unchanged** (asserted in §9) |
| `{"sticky_last_success_enabled": false}` alone (API) | dropped — value keeps prior | byte-unchanged |
| loop-detection-only PUT (`SettingsPage.tsx:291-294`) | untouched | untouched |

**Residual (documented, accepted):** API-only `false`-alone drops — IDENTICAL to today's `{"race_retry_enabled": false}`-alone semantics; the FE path (mandatory, §5.3) is the supported way to clear the flag.

### 5.3 FE — MANDATORY in this branch (leader decision, v2)

The reviewer's evidence makes the FE delta load-bearing: every FE proxy-Apply (`SettingsPage.tsx:190-211`) fires the family block (race siblings non-zero) — if the payload carries NO sticky key, the zero-filled incoming would silently reset sticky to `false` on every proxy-settings save. Therefore the FE delta is **in scope and required**:

1. `pkg/ui/frontend/src/types.ts` — `AppConfig` gains `sticky_last_success_enabled: boolean`.
2. `pkg/ui/frontend/src/components/SettingsPage.tsx` — state, load (`:170` region pattern `config.race_retry_enabled ?? false`), and the proxy-Apply payload key (inside the `:190-211` block) so the FE round-trip is complete.
3. `pkg/ui/frontend/src/components/config/ProxySettings.tsx` — props + pill toggle in the race-settings group (pattern `:235-245`).

**CRITICAL constraint:** FE source changes require **regenerated content-hashed bundles committed in the SAME commit** (`pkg/ui/static` embed discipline — stale hashes 404 the UI; pinned by `TestEmbedStatic_AllAssetReferencesResolve`, `pkg/ui/embed_regression_test.go:67`, R26). Build with `cd pkg/ui/frontend && npx vite build` (no `make`). FE tests run via **vitest FROM `pkg/ui/frontend`**; `tsc-baseline.txt` total must not regress.

---

## 6. Observability — log-only, no new event types

- House style `log.Printf` with bracketed prefix — new greppable prefix **`[STICKY]`** (siblings: `[LB]` `handler.go:566/:574`, `[RACE]` `race_coordinator.go:361/:1166`, `[grouplb]` `handler.go:598`). Key printed as 8-char prefix (precedent `:575`).
- Lines: reorder applied (key prefix + `from=` → `to=` full short lists), success recorded, invalidated (`reason=attempt_failure`).
- `race_started` payload already carries `{"models": c.models}` (`race_coordinator.go:360-362`); because the reorder runs BEFORE construction, the reordered list is naturally visible in the existing event — **no new event-bus types, no FE contract change** (the FE EventLog renders payloads generically). Justification: keeps the FE contract surface frozen (leader requirement) while order changes remain observable in logs + the existing event stream.

---

## 7. Invalidation semantics & TTL

### 7.1 TTL — `DefaultStickyTTL = 30m` sliding, refreshed on `RecordSuccess` ONLY

- Provider prompt caches live on the order of minutes (Anthropic default cache window ~5m; OpenAI comparable). A sticky slot that outlives the cache still helps — the same provider receives the prefix again and re-primes — but decay bounds how long a dead winner can be pinned.
- **30m** ≈ one cache window PLUS a realistic conversation-resumption gap, and is 48× shorter than the house 24h. Rejected: **5m** (drops affinity during ordinary think-time gaps; below most cache windows), **15m** (marginal staleness gain, loses longer resumptions), **24h** (house default is tuned for credential affinity where binding stability IS the product; for race ORDER a 24h-old winner is likely stale under fleet/config drift and over-suppresses racing diversity). Package const, NOT a config field (leader lock).
- **Refresh on `RecordSuccess` only, NOT on `Get`:** a `Get` hit means the recorded winner was merely promoted; if it then loses the race, the new winner's `RecordSuccess` overwrites the value and refreshes the slot — the slot lives exactly as long as the conversation keeps producing delivered successes. Get-refresh would immortalize a slot whose recorded model keeps losing. Sweep cadence 5m (house `DefaultSweepInterval`).

### 7.2 Failure invalidation — attempt-level, winner-matched

- `InvalidateOnFailure(key, modelID)` fires at EVERY failed attempt (§4.3 sites, flag-gated) but deletes ONLY when `modelID == recorded winner`; failures of any other model are no-ops. Rationale: a fallback's failure says nothing about the winner's health, and vice versa; winner-matching keeps the record a pure last-delivered-success signal.
- **"Fails an attempt" = `executeRequest` returns err for that model (OpenAI, `race_coordinator.go:1156-1159`) / walk attempt returns false (Anthropic, `:457-478` arm)** — the same events that surface as `race_request_failed` / walk failure. First-failure vs any-failure is moot under winner-matching: the first failed attempt of the winner deletes the slot; later ones no-op on an absent key.
- **Cred-failover exhaustion counts** — implicitly and correctly: each failover attempt of the winner is an attempt failure that already invalidated; an exhaustion-specific hook would be a redundant no-op (R8).
- **False-positive risk — accepted:** a single transient 429 unpins a good winner; the next request races unpromoted and, on success, re-pins. Cost of a false unpin = one request without promotion = **exactly today's behavior**. No debounce, no failure counters — that bookkeeping would outgrow the feature's value.
- **Non-coupling (explicit, leader lock 5c):** the reorder does NOT consult credential cooldown tables — they are credential-ID-keyed inside per-model state (`engine.go:695-698`, R20); sticky is conversation-keyed. Disjoint keyspaces by construction; failure-driven invalidation is the sole dead-winner correction path.
- **Residual (accepted, M1 ruling):** Anthropic truncated terminals (missing upstream `[DONE]`, scanner read error, client disconnect) are never RECORDED (the delivery gate skips them) but do NOT fire winner-matched invalidation — stale pins self-heal on the next failed attempt of the recorded winner.

### 7.3 grouplb interplay (documented interaction, no coupling)

The grouplb sticky member binding is fixed INSIDE `applyGroupRewrite` (`pickServableGroupMember`, `store_group_rewrite.go:61`) — executed at `handler.go:594` / `handler_anthropic.go:357` / re-entry `:1253` / `handler_groups.go:390`, all BEFORE any reorder point. The reorder then only permutes the already-expanded member list. **Known interaction:** sticky promotion may move one of the group-sticky member's own fallbacks ABOVE the group's sticky pick — index 0 is no longer the group pick — while grouplb's credential-affinity binding and `RouteModel` metering are untouched. Group-LB semantics are OUT OF SCOPE per the leader's ruling; only race ORDER changes. (Materially harmless on the OpenAI lane: index 0 and index 1 both race; the group pick remains in the raced set with its binding intact.)

---

## 8. Edge cases

1. **Empty conversationKey** (`leadingUserRun == ""`): orchestrator gate skips; record/invalidation sites gate on non-empty. Same gate as the LB engines (R2).
2. **Gate off**: orchestrator returns the input untouched; record sites skip; invalidation hooks skip (flag-gated, §4.3). Byte-identical to today **on the request path** (the only gate-off deltas are boot-time engine construction + janitor goroutine — outside request handling).
3. **nil engine** (tests/embeddings/legacy `NewHandler` calls): all engine methods no-op; orchestrator returns input.
4. **Winner not in the current list** (config changed between requests): `PromoteToFront` no-op — fresh equal copy, no log, no membership change.
5. **`len(list) <= 1`**: no-op.
6. **Duplicate model IDs** (constructible — self-referencing fallback chain `[A, A]`, or the same id as direct entry AND group-member chain, R16): move the **FIRST occurrence** only; later duplicates keep relative position. Membership and length preserved by construction.
7. **`modelTypeSecond`** (spawn TYPE `race_coordinator.go:387-395` resolving to `c.models[0]`, R17): reorder retargets it to the sticky winner — that IS the feature working (the winner occupies the main + second-attempt slots). No interference: the coordinator's list is frozen at construction (`:318`), and all reorder is pre-construction.
8. **Group re-entry**: OpenAI covered by loop-top placement (idempotent); Anthropic REQUIRES the explicit site (d) (R19). Backup-pool variant: both re-entry paths go through `ExcludeGroupMember` (`handler.go:1245`, `handler_groups.go:385`) which may reselect from the remaining pool — the rebuild still lands at the same choke points, so the reorder applies to the backup-pool list identically (asserted in §9).
9. **`servedModel` ordering trap**: pre-attempt set, never post-success (§4.4).
10. **JSON-backend caveat** (non-production): the JSON config backend implements no `GroupRewriter` ⇒ identity rewrite can leave group IDs in the list (`handler_groups.go:73-81`); `PromoteToFront` on a group id is a harmless no-op. Note-only.
11. **`race_started` visibility**: the reordered list flows into the existing event payload (R4) — intended observability, not a side effect.
12. **Client disconnect / cancellation (v2 reword)** — between attempts, disconnects do NOT invalidate (the walk's loop-level breaks `:402-405`/`:418-420` skip the failure arm). **Mid-attempt cancels DO invalidate**: a cancelled attempt surfaces as `ctx.Err()`/`context.Canceled` from the executor (`race_executor.go:638-639/:646-647`, R22) into the `MarkFailed` arm — e.g. `cancelAllExcept(winner)` on a client-write failure (`handler.go:1192`) marks the cancelled losers (possibly the recorded sticky winner) failed ⇒ transient unpin of a cancelled loser. **Accepted**: self-healing on the next delivered success; the un-promoted worst case is exactly today's behavior; adding cancel-classification to the failure funnel would couple sticky to executor internals for no user-visible gain. Test added (§9).
13. **Dead-code consumers (v2 note)**: `handleModelFailure` (`handler_finalize.go:46-75`) and the `newRaceCoordinator` 6-arg wrapper (`race_coordinator.go:276-277`) are TEST-ONLY / non-production surfaces (R25) — the sticky design neither hooks nor relies on them; production failure handling is coordinator-side (`:1156-1183`).

---

## 9. Test plan (skeleton)

**Unit — `pkg/stickyorder` (pure; no `config.Load`, no HOME dependency):**
- `PromoteToFront` table tests: no-op paths (len 0/1, empty target, absent target, already-front), first-occurrence-with-duplicates, membership+length invariants, **input never mutated**, **always-fresh-copy** (mutate result; input unchanged).
- Engine: record→get roundtrip; empty key/model no-ops; **nil-receiver no-ops for all four methods**; TTL expiry (short TTL + sweep); **sliding refresh on RecordSuccess**; **NO refresh on Get**; `InvalidateOnFailure` deletes only on winner-match; re-record after invalidation; `Stop` idempotent (+ double-Stop); janitor panic-recovery.
- Concurrency under `-race`: parallel RecordSuccess/Get/InvalidateOnFailure/Stop hammer (deterministic asserts).

**Wiring — `pkg/proxy` (isolate HOME — project hazard: `APPLY_ENV_OVERRIDES`/`PORT=9797` leakage rewrites `~/.config/llm-supervisor-proxy/config.json`; use `XDG_CONFIG_HOME=$(mktemp -d)` / `t.TempDir` quarantine per `.agents/tester/PACKS.md`; never depend on ambient HOME):**
- **Negative (gate off ⇒ request-path byte-identical)**: flag false ⇒ raced order unchanged at all sites, engine map empty, zero `[STICKY]` log lines (capture via `log` redirect); ALSO nil-engine variant: flag TRUE + `NewHandler` 8th arg `nil` ⇒ same identity (pins §8.3).
- Gate on, per site: (a) initial race reorder; (b) Anthropic initial walk reorder; (d) Anthropic re-entry reorder — **requires a runtime group write (PUT/POST `/fe/api/models`) per the project trap note; seed+boot alone is a false green**; **(c) OpenAI re-entry reorder assertion** — group re-entry rebuilds and the loop-top re-application must reorder the rebuilt list (same runtime-group-write trap); **backup-pool re-entry variant** — `ExcludeGroupMember` reselect path (both lanes) asserts reorder applies to the backup list.
- Record: stream + non-stream delivered-success paths record `winner.GetModelID()`; **first-byte-then-die and deadline-pick fixtures record NOTHING**; **ultimate-path completion (`handler.go:1060-1065` shape) records NOTHING**; **client-write-fail early-return (`:1183-1195` shape) records NOTHING**.
- `servedModel` ordering: a fixture where finalize runs from inside `attemptAnthropicModel` asserts `servedModel` is already set (pins the §4.4 trap).
- Invalidation: failed attempt of the recorded winner unpins (next request un-promoted); failure of a NON-winner leaves the pin; cred-failover exhaustion of the winner unpins; **cancelled-loser test (v2)**: mid-stream cancel of a non-serving model == recorded winner (`cancelAllExcept` shape) ⇒ slot invalidated, next request un-promoted, next delivered success re-pins (§8.12).
- Group interplay: promoted member vs group pick — reorder permutes only; `RouteModel`/binding semantics untouched.
- **Config merge (final assertions, v2)**:
  - `T-merge-1` `{sticky:true}`-alone PUT (pre-seed existing `RaceRetryEnabled=true`, `RaceMaxParallel=3`, `RaceMaxBufferBytes=1MB`, `RaceParallelOnIdle=true`): assert `StickyLastSuccessEnabled==true` AND `RaceRetryEnabled==true` AND `RaceMaxParallel==3` AND `RaceMaxBufferBytes==1MB` AND `RaceParallelOnIdle==true` — **siblings byte-unchanged** (the B1 clobber guard).
  - `T-merge-2` FE-shaped proxy payload (family non-zero + sticky key): sticky `true` persists; sticky `false` persists (clear works).
  - `T-merge-3` loop-detection-only PUT: sticky value untouched (both when prior true and prior false).
  - `T-merge-4` `{sticky:false}`-alone: value keeps prior (pins the documented residual).
  - Env `STICKY_LAST_SUCCESS_ENABLED=1` enables; default false; snapshot reaches `rc.conf`/`arc.conf`.
- Known-flake discipline: `pkg/proxy -race` deterministically red (not a regression signal); `pkg/credentiallb TOCTOU_CoolingReverify` ~1/10 plain flake — re-run isolated. Anti-vacuous: every ADDED `func Test…` name appears as a literal `--- PASS`; release-gate packs A/B union.

**FE (mandatory, §5.3):** vitest from `pkg/ui/frontend`; toggle round-trip test; `tsc-baseline.txt` total must not regress; `TestEmbedStatic_AllAssetReferencesResolve` green with the regenerated bundles committed in the SAME commit (R26).

**Churn:** 48 measured in-package `NewHandler` test call sites (+1 production) take the new 8th arg — mechanical, same commit (R27).

---

## 10. Trade-offs (surfaced to the user verbatim per the leader's instruction)

1. **Stickiness scope.** `conversationKey` is salted by top-level requested model ID + token ID (`key.go:48-50`) ⇒ stickiness is per **(requested model, principal, prefix)** — NOT global per prefix. Cross-chain learning does not happen: two different requested models (or two principals) with byte-identical prefixes maintain disjoint slots. **Accepted**: it matches "per requested model" semantics, prevents cross-principal pollution, and reuses the exact keyspace the LB engines already gate on.
2. **Hash content delta.** The existing hash covers the leading USER-message run ONLY — system-prompt content is EXCLUDED (`key.go:102-104`). Verbatim reuse (mandated) means two requests differing only in system prompt share a sticky slot. **Known consequence; the hash is NOT changed** (locked decision — a second hash is forbidden, and changing the shared hash would move LB affinity too).
3. **TTL choice.** 30m sliding (refresh on success) vs **5m** (drops during ordinary think-gaps; below most provider cache windows), **15m** (loses longer resumptions for marginal staleness gain), **24h** (house default tuned for credential affinity; too stale for race order, over-suppresses racing diversity). 30m ≈ cache window + resumption gap with bounded staleness (§7.1).
4. **Failure-invalidation granularity.** Attempt-level, winner-matched: a single transient 429 of the recorded winner unpins it ⇒ the next request races unpromoted; mid-attempt cancels of a recorded winner also unpin (§8.12). **Argued acceptable**: the cost of a false unpin is precisely today's behavior (un-promoted racing), and the next delivered success re-pins. Debounce/counters/cancel-classification rejected as complexity out of proportion to the feature.
5. **In-memory only.** Restart loses all stickiness (no rehydration, `grouplb/engine.go:106-109` house ruling). First request per conversation after restart races unpromoted; warm-up is one delivered success. **Accepted per house ruling.**

---

## 11. Risks / open questions

- 🟡 **FE embed-bundle discipline** — the mandatory FE delta requires regenerated content-hashed bundles in the SAME commit (`pkg/ui/static`; `TestEmbedStatic_AllAssetReferencesResolve` pins); forgetting the rebuild 404s the UI (R26).
- 🟡 **Residual minimal-PUT gap** — `{sticky_last_success_enabled: false}` alone via API drops silently (identical to `race_retry_enabled: false`-alone today; §5.2). Documented, accepted; the FE path is the supported clear.
- 🟢 **`[STICKY]` log volume** — only on actual moves/records/invalidations; bounded by request traffic; no per-request noise when no record exists.
- 🟢 **Racing diversity reduction within a pinned conversation** — intended (that is the latency/cost win); diversity returns on any winner failure, TTL expiry, or restart.
- 🟢 **`slices.Equal`** — stdlib, fine on go 1.26 (no new dependency).
- 🟢 **Test-handler churn** — 48+ mechanical 8th-arg additions (R27); no semantic change (nil-safe engine).
- **Explicit non-goals**: ultimate-model switching, imggen/decision lanes, `/v1/models` catalog (no membership change), any change to the conversation hash, any group-LB behavior change.
- **Open questions**: none blocking. Judgment calls the leader may still revisit: TTL const value (30m); optional explicit site-(c) call after `handler.go:1258` (loop-top coverage is the spec).
