# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> Operator-facing details (configuration, behavior caveats, affinity limitations, latency expectations) live in the [README](README.md) — see `### Model credentials (multi-credential load balancing)`.

## [Unreleased]

### Added

- Decision model kind added (kind=decision); OpenAI Decision API + TypeSafe (Jev) providers; new /v1/decisions route; new Decision Models dashboard tab
- **Per-model multi-credential load balancing.** Models may now declare an ordered, weighted `credentials[]` array (up to **16** same-provider entries) on the model record; the first entry is the primary, `position` is server-managed (0-based slice index), and per-credential `weight` (must be `> 0`) drives both initial selection and failover rotation. The first request of a conversation picks one credential by weighted random; every subsequent request of that conversation is pinned to the same credential via conversation-sticky affinity — a token-salted, conversation-stable key with a **24h sliding idle TTL** (refreshed on each use). The engine is in-memory only and lives in `pkg/credentiallb`.
- **Rate-limit credential failover.** On HTTP 429, the proxy now retries against the next healthy same-provider credential **before** falling through to the next model in the fallback chain. Per-credential cooldown is seeded from the upstream's `Retry-After` header (default **60s**); on cooldown expiry the credential transparently rejoins the weighted distribution. Failover is observable via the `model_credential_failover` event.
- **Database schema: `models.credentials_json` (migration `028_add_model_credentials`).** Adds an ordered, weighted JSON array per model. The migration is **non-destructive**: the legacy `credential_id` column is retained as a derived shadow (back-filled from `credentials_json[0]`) and is **deprecated** — removal is tracked for migration 029+. Existing single-credential models continue to behave **byte-identically** to pre-feature behavior (the engine's fast path skips affinity bookkeeping when `len(credentials) == 1`).
- **Web UI: MultiCredentialEditor.** The model editor now exposes the `credentials[]` array with add/remove/reorder and per-credential weight editing. The **test-connection** endpoint continues to accept a single `credential_id` (it tests against the primary).

### Added — load-balance groups (kind=group)

- **`group` model kind — virtual models that load-balance across multiple (provider, model) targets.** A new 4th model kind that lets the proxy distribute a chat conversation across many real models behind a stable client-facing identity. Members are existing proxy model IDs (not raw provider strings); the group's election flows through `pkg/grouplb` (a single-mechanism, weighted, sticky picker keyed by a group-salted conversation hash) and the served member inherits its native credential-LB, fallback-chain, and retry pipeline unchanged.
- **ID-space shadowing.** A group may *claim* one existing chat model's ID via `shadow_model_id`. While the claim is active, requests for the claimed ID route to the group; `/v1/models` hides the claimed row. Clearing the shadow or disabling the group drops the claim and the original model reappears. The auth allow-list evaluates the client-requested string (fail-closed), so a token may list the group's own ID, the claimed ID, or neither.
- **Sticky routing — in-memory, 24h sliding TTL, restart cold-starts.** The first request of a conversation weighted-randomly picks one member; subsequent requests pin to that member. Bindings live in the grouplb engine and are **not persisted** across restarts (USER-DECIDED, in-memory only); a restart fresh-picks per in-flight conversation and emits a startup log line. Day-1 single-member groups use the E-3 fast path with zero engine overhead.
- **Day-1 no-op guarantee.** A system with NO group rows behaves identically to the pre-feature baseline: no engine initialization, no group events on the bus, `/v1/models` listing unchanged, metering byte-identical. The named equivalence suite (`pkg/proxy/handler_groups_equivalence_test.go::TestAm29_Equivalence_AllFiveCases`) pins five cases — zero-groups, manual `coding[coding]` routing, response-shape masking, single-row metering, and the latency envelope.
- **Web UI: Group Models tab.** 11th dashboard tab (`data-tab-id="group_models"`, glyph 🔀); CRUD mirroring the Decision Models tab; a chat-kind member picker for the member editor (members are model IDs, not raw strings); a clearable `shadow_model_id` dropdown populated from the chat-kind models; an "ID = member ID" pre-fill wrap affordance with the canonical `${id}[${id}]` shape (form pre-fill convenience only — no auto-create, no bootstrap, no seed). Cache quadruple-delete discipline across the 9 mutation sites in `useApi.ts`.
- **Database schema: `models.members_json` + `models.shadow_model_id` (migration `031_add_model_groups`).** Adds a `[{model_id, weight}]` JSON column and a TEXT shadow column. **Non-destructive**: no existing column is altered; `kind=group` rows are an additive subset.
- **New package: `pkg/grouplb`** (engine + selector + binding + events). Clone of `pkg/credentiallb` reduced to weighted sticky pick over model IDs, no credential awareness, lazy per-group state seeded exactly once per process lifetime (Am15).
- **Q-surface resolution snapshot.** Q1 ID-vs-Name shadowing resolved in ID space (§3.4); Q3 sticky TTL fixed at 24h in-memory (USER-DECIDED); Q4 restart cold-start documented as log-line behavior, not engineered around; Q7 group fallback chains via the served member's native `fallback_chain` (no separate field); Q8 G-rule uniqueness (no two enabled groups may claim the same shadow); Q17 force-ultimate onto a group fails fast with the normal error envelope (documented residual). All adopted Qs (Q1–Q21) recorded in `.agents/shared/planning/load-balance-groups/decisions.md`.

### Operator notes (load-balance groups)

- Day-1 onboarding a chat model (e.g. "coding") behind its own self-shadow group: in the dashboard, open Models → pick "coding" → "Wrap as group" → submit. The group row is `coding[coding]` and the claim is automatic — no token changes, no client-side config.
- Migration: apply `031_add_model_groups` on the existing DB. No data backfill; the migration only adds nullable columns. Restart after applying.
- Restart behavior: sticky bindings live in-memory; a process restart fresh-picks per in-flight conversation. Log line: `[grouplb] restart cold-start: in-flight conversations re-rolling` (one-shot, at boot).
- 24h sliding TTL: a conversation that has not been seen for 24h is re-picked on the next request. The first request of a new conversation always picks.

See `docs/2026-10-16-load-balance-groups.md` for the full operator runbook.
