# Lessons — sticky-binding-ttl-1h gate (2026-11-17)

Worktree gate over an UNCOMMITTED diff (feature/sticky-binding-ttl-1h @ base a2952a9). Full report: `RESULTS/2026-11-17-sticky-binding-ttl-1h-verification.md`.

## 1. pkg/grouplb test determinism: weight ≤ 0 does NOT pin a member — it kills the selector
Trying to make "member A the deterministic default pick" via `B weight 0` is impossible: `newWeightedSelector` returns nil for ANY weight ≤ 0, and a nil weighted selector collapses the whole group to `ErrNoMembers`. The working pattern (used by the (c)-probe): default pool = EXACTLY ONE member (A, weight 1) → every default pick is trivially deterministic; declare B `BackupOnly: true` (mirrors the backup-pool acceptance test) so B can never be freshly picked into the default map yet stays visible to `groupState.members`; plant the binding→B via the existing `InjectPreconditionStateForTest` seam; force expiry by real sleep past `createdAt+ttl` (no clock seam exists in this engine).

## 2. Sabotage "no delete" must model the SEMANTIC, not the line — count-mirror self-healing
Removing ONLY the `delete(st.bindings, conversationKey)` line (keeping `st.bindingsCount--`) left `TestEngine_BindingExpiresDespiteContinuousHits_DeletesEntry` GREEN — not because the test is vacuous, but because the `getOrSelect` rebind path does an unconditional `bindingsCount++` (delete-then-insert rebalance), so the corrupted count bounces 0→1 and `want 1` still holds. The FAITHFUL ignore-only sabotage (skip the delete AND its mirror decrement — row left behind, count intact) fails exactly on `abs_ttl_acceptance_test.go:58: bindingsCount=3 want 1`. Two takeaways: (a) when proving a deletion test bites, sabotage the invariant pair (row + mirror) together; (b) test hardening follow-up — assert the map directly (len(map) / stale-row-absence) so count-mirror bugs cannot evade (🟢 finding).

## 3. Feature-scope sha for byte-identity proofs in a shared tree
Mid-gate, a concurrent tidier agent appended to TRACKED `.agents/tidier/notes.md`, shifting the full `git diff | sha256sum`. Identity proofs over a shared worktree/checkout should anchor on `git diff -- . ':(exclude).agents'` (feature scope) with the full-diff sha reported for transparency. Revert methodology for an UNCOMMITTED feature diff: NEVER `git checkout --`/`git stash`/`git reset` (destroys the feature) — cp the file to /tmp first, restore after, verify file sha == backup, then the scoped diff sha.

## 4. pkg/store/database -race pack is at ~27% headroom
176.407s under -race vs the 240s internal cap (~4.9× amplification of 36.4s plain). Any test addition there can trip the internal timeout under the race detector. Split the store race pack (group/credential vs models/config) BEFORE adding tests — mirrors the 2026-11-08 pkg/proxy 110s→240s note (that one got a cap raise for release-gate slices; this one should get a SPLIT, the package keeps growing).

## 5. Wave discipline for gates that mutate the tree
Sabotage/mutation packs MUST be serialized after ALL read-only packs (a concurrent test run would hit sabotaged code → false reds). Three-wave dispatch worked cleanly: wave 1 acceptance∥Pack A∥Pack B, wave 2 race∥race, wave 3 sabotage+probe (sole tree mutator). Race packs after plain packs also warms GOCACHE, reducing pack-wall variance.
