# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> Operator-facing details (configuration, behavior caveats, affinity limitations, latency expectations) live in the [README](README.md) — see `### Model credentials (multi-credential load balancing)`.

## [Unreleased]

### Added

- **Per-model multi-credential load balancing.** Models may now declare an ordered, weighted `credentials[]` array (up to **16** same-provider entries) on the model record; the first entry is the primary, `position` is server-managed (0-based slice index), and per-credential `weight` (must be `> 0`) drives both initial selection and failover rotation. The first request of a conversation picks one credential by weighted random; every subsequent request of that conversation is pinned to the same credential via conversation-sticky affinity — a token-salted, conversation-stable key with a **24h sliding idle TTL** (refreshed on each use). The engine is in-memory only and lives in `pkg/credentiallb`.
- **Rate-limit credential failover.** On HTTP 429, the proxy now retries against the next healthy same-provider credential **before** falling through to the next model in the fallback chain. Per-credential cooldown is seeded from the upstream's `Retry-After` header (default **60s**); on cooldown expiry the credential transparently rejoins the weighted distribution. Failover is observable via the `model_credential_failover` event.
- **Database schema: `models.credentials_json` (migration `028_add_model_credentials`).** Adds an ordered, weighted JSON array per model. The migration is **non-destructive**: the legacy `credential_id` column is retained as a derived shadow (back-filled from `credentials_json[0]`) and is **deprecated** — removal is tracked for migration 029+. Existing single-credential models continue to behave **byte-identically** to pre-feature behavior (the engine's fast path skips affinity bookkeeping when `len(credentials) == 1`).
- **Web UI: MultiCredentialEditor.** The model editor now exposes the `credentials[]` array with add/remove/reorder and per-credential weight editing. The **test-connection** endpoint continues to accept a single `credential_id` (it tests against the primary).
