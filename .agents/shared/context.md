# Project Context — llm-supervisor-proxy

_Last updated: 2026-08-28 (db-cache-layer Phase 1 landing)_

## Current State

- **Branch `feature/db-cache-layer`** (from `68dbe63`): DB Caching/Resilience Layer MVP landed as five sub-stage commits (1A→1B→1D→1C→1E).
  - 1A — additive strict read methods in `pkg/store/database` (`GetModelStrict`/`GetModelByNameStrict`/`GetCredentialStrict`/`GetModelsStrict`/`GetEnabledModelsStrict`/`GetCredentialsStrict` + `ResolveInternalConfigWithAffinityCached`); legacy signatures byte-identical.
  - 1B — new `pkg/modelscache` package: `CachedModelsConfig` + `CachedTokenStore` decorators (boot priming, 60s reconciler with abort-on-swap, negative caching, write-through/invalidate-only mutators, three-tier token cache, dead-default WARN tripwire).
  - 1D — fail-fast 503 `config_store_unavailable` at the two `pkg/proxy` boundary sites (nil model + unhealthy store; nil + healthy stays legit external passthrough).
  - 1C — `cmd/main.go` wiring: `CACHE_LAYER_ENABLED` env flag (default ON), wrap seams at models/token stores, teardown order `srv.Shutdown → StopModels → StopTokens → credLB.Stop → modelsMgr.Close → dbStore.Close`.
  - 1E — outage/contract tests, README env docs, planning-doc corrections (N1 hex-string key, Reviewer-🟡 rewording, cosmetics).
- Motivation: 2026-08-27 incident (18s PG outage → conflated-nil GetModel → silent external misroute to localhost:4001). The layer makes the proxy survive ≥1h DB outages with zero misroutes, zero silent 401s, zero silent-empty model lists (asserted by `pkg/modelscache/outage_test.go`).

## Rollback

- `CACHE_LAYER_ENABLED=false` + restart = pre-cache behavior (no rebuild). Boot-time priming failure keeps today's `log.Fatalf` posture.

## Phase 2 (deferred, NOT in this release)

Usage replay ring, cache metrics, `[]byte` key-wipe hardening, store-side credential-mutator event publishes, env-configurable TTLs, reconciler backoff. See `.agents/shared/planning/db-cache-layer/decisions.md` [LEADER-2].

## Standing notes

- Decrypted credentials now live in process memory for the cache's lifetime (accepted residual per arch §5): never logged, never metric-labeled, never disk-written.
- Unconfigured model names during a DB outage now 503 (`config_store_unavailable`) instead of silently passing through — intentional.
- The gzipmw pre-auth buffering risk (CWE-409/770) and other baseline debt are unchanged by this layer.
