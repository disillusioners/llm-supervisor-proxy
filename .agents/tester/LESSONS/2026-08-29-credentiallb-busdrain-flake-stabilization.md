# 2026-08-29 — credentiallb poll-timing flake stabilized (TestStoreEngine_BusDrainForwardsCredentialsChanged)

## Symptom
`pkg/store/database` credentiallb poll-timing test failed intermittently: 2/~13 historical runs (~15%), then 6× clean. Not reproducible in-session (100 plain + 50 race + 30 race×15 ≈ 180 invocations, all PASS).

## Root cause (test-side race window, NOT production)
- `store_test.go:609` used `time.Sleep(20ms)` as a "barrier" between `seedModelCreds` (publishes `model.credentials.changed` via bus, store.go:2010) and the test's manual publish (:612).
- The sleep does NOT guarantee the drain goroutine (store.go:690-714) consumed the AddModel event. If the drain processes it AFTER `RebindFromStore("bd", …)` (:588), the engine resets to `[cA, cB]` via `drainCredentialsChanged` DB re-read (store.go:707-714) — making the :591 sanity check a coin flip on a 2-cred weighted pick.
- The post-publish 2s deadline is also scheduler-sensitive under GC/load (each fresh-key pick costs a `GetModel` DB read).

## Fix (test-only, +35/-4, commit c9559cf)
Sentinel subscriber on the bus (Subscribe order = Publish delivery order, bus.go:131-149) → deterministic `select`-wait for the AddModel event delivery + 50ms drain grace, applied both pre- and post-manual-publish. Probabilistic cC-pick poll (P(miss) ≈ 1e-9 over 50 picks) now runs against a converged engine.

## Verification
20/20 plain, 20/20 `-race`, full `pkg/store/database` package ok (1.049s). Quarantined `TestStoreEngine_CloseLifecycle` did not fire.

## Pattern
Fixed sleeps are not synchronization barriers against goroutine consumers. When a test must wait for an event-bus consumer to act, subscribe a sentinel in the same bus and select on it — subscription order guarantees the consumer's channel was buffered first.
