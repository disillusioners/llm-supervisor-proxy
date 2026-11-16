# Cache Layer Separation (wi-4d2c8e17)

Branch: `fix/cache-layer-separation` · base `24c51ef` (v193 tree) ·
design record for the reviewer.

## Problem

One skip cache decided too much. A limit mark could change *which*
group member was picked (observed regression: a mark on the sole
default member flipped the group into backup mode), and single-model
lists never hedged. Outcome: traffic left the intended head, chain-only
models were bypassed, and client-attributable 400s could spin to the
deadline.

## Ruling 1 — semantics

- **R1** The cache must not decide the group pick. The pick is POLICY
  (pools / weights / sticky); a mark never changes the head.
- **R2** A mark suppresses only the ATTEMPT (zero-cost, no upstream
  call); the walk proceeds INTO the member's chain.
- **R3** A member is judged failed only after its own resilience
  (hedge + chain) is consumed — then the next group member.
- **R4** Chain-only, INACTIVE models remain reachable and servable;
  validation allows them (no cleanup).
- **R5/F1** A single-model list still gets a hedge (credential-level
  second-upstream attempt).

## Ruling 2 — three layers, no cross-reads

| Layer    | Question                                | Written by                              | Read by                         | TTL |
|----------|-----------------------------------------|-----------------------------------------|---------------------------------|-----|
| GROUP    | is this member servable in this group?  | group policy facts only (membership, pools, grouplb rotation) | the group pick ONLY | policy lifetime / engine rotation |
| MODEL    | is this MODEL limited/unavailable?      | model-attributable walls (`pkg/modelcache`) | model/chain attempt suppression ONLY | reset instant (clamp 1m..24h) or fixed `model_skip_cache_ttl` (15m) |
| UPSTREAM | is this remote/credential working?      | credential/provider failures (`pkg/skipcache`) | attempt path: credential RE-ORDERING (prefer an unmarked sibling; all-marked still attempts, soonest-expiring first) + win-clear ONLY | 60s / quota 15m (as before) |

**Structural invariant.** The group pick cannot read another layer —
NO non-test file in `pkg/store/database`, `pkg/grouplb` or
`pkg/modelscache` imports `pkg/skipcache` or `pkg/modelcache` (pinned by
`TestCacheLayer_Structural_PackageLevelNoCacheImports`), and the data
manager holds no cache reference at all. The v193
four-phase sweep survives, but its ordering key is now the **grouplb
rotation cooldown only**; upstream/model marks never demote or filter.

Doctrine (kept in `pkg/skipcache` and extended): *"The cache is an
optimization, not the truth: it may reorder candidates, never exclude
them — and it may suppress an ATTEMPT, never a PICK or a CHAIN."*

### Package layout

- `pkg/skipcache` — UPSTREAM layer (unchanged API; doc re-scoped).
- `pkg/modelcache` — MODEL layer: `Cache{Mark, MarkUntil, Marked, Until,
  Clear}` + reset parser (`ParseReset`, `ModelWall`).
- `pkg/grouplb` — GROUP layer (existing engine: membership, pools,
  weights, bindings, rotation cooldown).

### Write sites

- Upstream: `race_coordinator.markUpstreamSkipLocked` and
  `handler_anthropic.markAnthropicUpstreamSkip` (limit-class only).
- Model: same two sites, when the failed body classifies as a
  model-attributable wall (`modelcache.ModelWall`).

### Read sites

- Model suppression (zero-cost): `race_executor.executeInternalRequest`
  (`ModelSkipCache.Marked(req.modelID)`) and the Anthropic walk
  (`ModelSkipCache.Marked(currentModel)`). A synthetic plain error keeps
  it rotation-ineligible but advance-worthy, so the chain is entered.
- Upstream: `preferUnmarkedUpstreamCredential` in the attempt path
  (`race_executor.executeInternalRequest`) reorders credential
  preference away from a marked `(credential|provider, model)` pair to
  an unmarked sibling; if every credential is marked it prefers the
  soonest-expiring mark and STILL attempts (never excludes). Plus
  `clearWinnerUpstreamSkip`. This is the real reader (D1) — it is not
  "credential selection as before".

### Win-clear (per layer)

A proven WIN clears: UPSTREAM rows for the winning credential/provider,
MODEL row for the winning model, and the GROUP *rotation* row for the
winning member (proven service; never a health mark).

### Acknowledged design notes

- **Group-layer rotation row** is the single acknowledged *health-derived*
  artifact that lives inside the group layer: it is post-R3 rotation
  bookkeeping written only by `ExcludeGroupMember` on a genuine
  end-to-end member failure, and it is cleared for the proven winner as
  policy restoration. It is deliberately retained (not re-litigated) —
  it is not a skip-cache health mark.
- **Model layer keyed by ROW identity.** Two rows that share one
  upstream plan rediscover the same wall independently (each mints its
  own row-level mark). The per-row cost is accepted — row identity is
  the stable execution key and avoids wire-name/row-ID mismatches.
- **Upstream reader is an ordering input only.** It reorders credential
  preference; it never excludes an attempt and never touches the group
  pick or chain membership.

## Adjacent fixes (in scope)

- **F2** Client-attributable head failure (400/413/415/422/cancel) with
  a chain: the all-failed terminal now also fires on the
  `chainEndPublished` latch, so the coordinator resolves promptly and
  `GetFinalErrorInfo` passes the 4xx through (`ErrorTypeInvalidRequest`)
  instead of 502/“Request timeout”.
- **F3** A content-less 200 (finish + `[DONE]` only) is not a usable
  win: `streamBuffer.HasModelContent` gates the winner predicate, and a
  completed attempt with no content is treated as a failure so the chain
  advances (no hang).
- **LABELS** Chain hops emit `model_failed` / `model_fallback_attempted`
  / `model_fallback_advanced` / `model_fallback_exhausted` with
  model-only payloads (no `group_id`/`member_id`). The genuine member
  hop (handler re-entry) emits `group_member_failed` /
  `group_fallback_*`. Shape chosen: the coordinator distinguishes the
  head from chain targets via `groupPick.MemberID`; group events are
  published by the two handler re-entry sites — no member list needed.

## Reset-TTL assessment (OPTIONAL item)

Observed shapes:
- Absolute: `Usage limit reached for 5 hour. Your limit will reset at
  2026-11-16 18:13:33`.
- Relative: `Unavailable (reset after 62h 45m 6s)` and the real 9router
  wire body `… Your quota will reset after 4h39m9s.`
  (`pkg/imggen/ninerouter.go:290`).

`modelcache.ParseReset` handles `reset at/on YYYY-MM-DD HH:MM:SS` (UTC,
documented assumption) and `reset in/after [Nd][Nh][Nm][Ns]` (compact or
spaced). `ModelWall` filters throttles (`for this minute`, `per minute`,
`requests/tokens per …`) so a short rate limit stays upstream-scoped.
On a successful parse the model mark runs to the reset instant clamped
to [1m, 24h]; otherwise the fixed `model_skip_cache_ttl` (default 15m)
applies. Provider coverage: the parser is provider-agnostic over the
error message; the two documented shapes + 9router are covered. A body
that names a reset in an unrecognised format falls back to the fixed
TTL (safe).

## Test map (RED→GREEN)

`pkg/proxy/cache_layer_separation_test.go`:
`TestCacheLayer_T_A_MarkedHeadStillPicked_ChainServes`,
`…T_B_MarkDoesNotFlipToBackup`, `…T_C_ChainBeforeMemberAdvance`,
`…T_D_InactiveChainTargetServable`, `…T_E_SingleModelGetsHedge`,
`…T_F_Client400_ReturnsPromptly`, `…T_G_Empty200DoesNotWin`,
`…GroupPick_StableUnderUpstreamMarks`, `…WinClear_PerLayer`,
`…Structural_NoCacheImportsInGroupPick`,
`…Structural_ModelSuppressionKeyedByRow`.
`pkg/modelcache/modelcache_test.go` (cache + reset parser).
`pkg/proxy/group_fallback_events_test.go` (model_* label contract).
`pkg/store/database/store_group_skipcache_test.go` (pick stability under
upstream marks).
