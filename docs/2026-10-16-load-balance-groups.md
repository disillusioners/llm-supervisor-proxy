# Load-balance groups (`group` model kind) — Operator Runbook

> **Status:** release-pending P4 (2026-10-16). Feature ships via the load-balance-groups branch on user-authorized release (no push without explicit instruction).
>
> **Companion docs:**
> - `CHANGELOG.md` (Unreleased — load-balance groups section) — release-level surface.
> - `.agents/shared/planning/load-balance-groups/plan.md` — design rationale, gate rulings, G-rules.
> - `pkg/proxy/handler_groups_equivalence_test.go::TestAm29_Equivalence_AllFiveCases` — the canonical day-1 equivalent-behavior contract.

---

## 1. What this feature does

A **group** is a 4th kind of model (`kind: "group"`), in addition to
the existing `chat`, `image-gen`, and `decision` kinds. A group is a
**virtual model** that load-balances a single client-facing ID across
multiple proxy model IDs (each member is itself a real proxy model
with its own credential LB, fallback chain, and provider plumbing).

The client names a model (either the group's own ID, e.g.
`coding[coding]`, or the **claimed ID** of an existing chat model via
`shadow_model_id`) and receives a normal chat response. The proxy
runs `pkg/grouplb` (a lazy, sticky weighted picker) to decide which
member serves each request, then dispatches the request through the
member's own native pipeline — the member's credential LB, fallback
chain, and ultimate-model switching all run unchanged. To the client,
the experience is identical to talking to a single model.

---

## 2. Day-1 onboarding — self-shadow group

The simplest pattern: shadow-claim the existing chat model's ID. The
client keeps using "coding"; the proxy silently routes through the
group. No token changes, no client-facing config.

Via the dashboard:

1. **Models tab → Models list.** Find the chat model you want to
   group (e.g. `coding`).
2. **Click "Wrap as group".** This opens Group Models tab with the
   form pre-filled:
   - `id = "coding[coding]"` (canonical encoding — see §6 note
     in the plan; do NOT use the literal `id = "coding"`).
   - `shadow_model_id = "coding"` (claim the existing chat ID).
   - `members = [{model_id: "coding", weight: 1}]` (single member
     on day-1).
   - `internal = true`, `exclude_from_ultimate_switching = true`.
3. **Submit.** The form 400s if any G-rule fails (see §4) — fix
   the violation and re-submit.

Via the API (programmatic creation):

```bash
curl -X POST http://localhost:8080/fe/api/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "coding[coding]",
    "name": "Coding (Self-Shadow Group)",
    "enabled": true,
    "kind": "group",
    "internal": true,
    "exclude_from_ultimate_switching": true,
    "shadow_model_id": "coding",
    "members": [{"model_id": "coding", "weight": 1}]
  }'
```

After the group is created, `/v1/models` no longer lists `coding` as a
standalone row — it now resolves to the group. The client API stays
unchanged.

---

## 3. Multi-member groups — weighted distribution

To distribute a single client ID across multiple real models,
declare a group with N members and weights:

```json
{
  "id": "g-multi",
  "name": "Multi Backend",
  "enabled": true,
  "kind": "group",
  "internal": true,
  "exclude_from_ultimate_switching": true,
  "members": [
    { "model_id": "m-a", "weight": 3 },
    { "model_id": "m-b", "weight": 2 },
    { "model_id": "m-c", "weight": 1 }
  ]
}
```

The first request of a conversation weighted-randomly picks ONE
member (3/2/1 ⇒ ~50%/~33%/~17%). Subsequent requests pin to that
member for up to **24 hours of inactivity** (sliding-idle TTL).
Pinning key is the SHA-256 group-salted conversation hash (group
ID + tokenID + firstUserMessage); different tokens and different
conversations get independent picks.

If the pinned member fails with a terminal upstream failure
(5xx, terminal connection error, etc. — NOT a credential 429
which is absorbed by the member's own credential LB), the group
**reselects exactly once** and the request re-enters the loop
**pre-first-byte** (a stream that has begun streaming cannot
switch). A second terminal failure surfaces as a normal
`group_unservable` 502.

---

## 4. The G-rules (validation)

The validator enforces 8 rules (G1–G8) at three sites: the JSON
write path (PkgModelsConfig.Validate), the DB write path
(validateBeforeWrite), and the FE POST/PUT layer. Violating any of
them 400s the write.

| # | Rule | Why |
|---|------|-----|
| G1 | `kind=group ⇒ internal=true` | Dispatch predicates key on Internal; groups route through the same modelList fan-out as direct chat. |
| G2 | `internal=true ⇒ credential_id == 0` | Groups carry no credentials. Members have their own. |
| G3 | `kind=group ⇒ 1 ≤ len(members) ≤ 16` | Lazy engine needs at least one member; UI clamps to 16. |
| G4 | every `member.model_id` resolves to an enabled chat row | Stale members cause G-violations on subsequent writes (UI catches it on submit). |
| G5 | members must be text-kind, NOT group | No group-of-groups (USER-DECIDED). |
| G6 | no `fallback_chain`, no `secondary_upstream_model`, no `peak_hour_enabled`, no `release_stream_chunk_deadline` | Rejected for groups (v2 candidates). |
| G7 | `exclude_from_ultimate_switching == true` (write-gate) | Ultimate-model switching is a single-model feature; groups present a moving target — the silent stop is documented (§3.6.4). |
| G8 | the group ID != any other enabled group's shadow_model_id, AND shadow_model_id != group's own id | One enabled group at a time per claimed ID; the group cannot claim itself. |

A group whose every member is currently disabled (or deleted) is
**inert from the listing view** (the Am28 stale-member defense
excludes it from sticky pick via `pickServableGroupMember`'s
`memberInRefs` check); a direct request for the group's ID
returns `group_unservable` 502 with the normal envelope.

---

## 5. Migration guidance

The release ships migration **`031_add_model_groups`** which adds
two nullable columns to the `models` table:

- `members_json` TEXT — JSON `[{model_id, weight}, ...]`
- `shadow_model_id` TEXT — the claimed chat-model ID, or NULL

The migration is **non-destructive**: no existing column is
altered. Apply with the standard migration pipeline:

```bash
# proxy automatically runs the migrations on boot if the DB is
# older than the schema-version table; otherwise run manually:
sqlite3 ~/.config/llm-supervisor-proxy/config.db < \
  pkg/store/database/migrations/sqlite/031_add_model_groups.up.sql

# PostgreSQL equivalent:
psql $DATABASE_URL -f \
  pkg/store/database/migrations/postgres/031_add_model_groups.up.sql
```

After migration, restart the proxy. There is **no data backfill** —
groups are an additive subset; pre-existing rows continue to load
unchanged. The grouplb engine is also lazy (cold-start binds only
on first request to each group), so the proxy boots with zero
group state until a group row is exercised.

---

## 6. Restart cold-start (USER-DECIDED)

Sticky bindings are **in-memory only** (USER-DECIDED — DB
persistence of affinity was rejected for v1 minimalism). After a
process restart:

- The grouplb engine starts with **no bindings** for any group.
- The first request of each in-flight conversation fresh-picks
  a member (weighted random). The conversation may route to a
  DIFFERENT member than before the restart.
- One startup log line per process: `[grouplb] restart cold-start:
  in-flight conversations re-rolling`.

This is documented behavior, not engineered around. It bounds the
operational surface (no stale-affinity bugs from missed clears, no
DB write amplification on every model CRUD), at the cost of one
surprise pin-re-roll per restart.

---

## 7. 24h sliding idle TTL

A conversation that has not been seen for **24 hours** triggers a
fresh pick on the next request (the binding expires; the engine
returns `newlyBound=false` from the cache-hit path so observability
events don't double-fire). The first request of a brand-new
conversation always picks.

The TTL is user-decided; the engine default is 24h, settable via
`engine.SetTTL(time.Duration)` in code if a deployment needs
something tighter (no env override today — config-time choice).

---

## 8. Observability — bus events

| Event | Fires | Payload |
|-------|-------|---------|
| `model_member_selected` | first request of a conversation (newlyBound=true) — sticky pick creates a fresh binding | `{group_id, member_id, upstream_provider, upstream_model}` |
| `model_member_failover` | group-level reselect after a terminal member failure (Am24 re-entry — pre-first-byte, max ONE) | `{group_id, member_id, replacement}` |
| `model_group_binding_dropped` | a binding is dropped (member removal / explicit drop) | `{group_id, conversation_key, dropped_member}` |

The EventLog UI tab surfaces all three. The pre-existing
`config_store_unavailable`, `auth_failed`, and `request_failed`
events flow through unchanged.

---

## 9. Failure surface — `group_unservable`

A group is unservable when:

- It has zero enabled members (all members disabled / deleted) at
  the time the sticky pick runs.
- The sticky pick returns the SOONEST-expiring member when every
  configured member is in cooldown (a >F.4 ruling); the request
  proceeds but a `model_member_failover` event may be absent in
  this case (no re-entry happens).
- G-rule violations at write time (which surface as 400, not 502).

The unservable surface is a normal `group_unservable` 502 envelope.
No panic, no silent passthrough — `pkg/proxy/handler.go:585-600`
and the Anthropic mirror at `handler_anthropic.go:447`.

---

## 10. Disabling & deletion

- **Disabling a group:** set `enabled=false`. The group refuses to
  be picked; clients requesting the group's ID receive a normal
  "model not found" envelope. The shadow claim is dropped —
  if the group claimed "coding", "coding" reappears in
  `/v1/models` immediately.
- **Disabling a single member:** the Am28 defense excludes it
  from sticky pick on the next request; the binding expires
  within the 24h TTL and is replaced transparently.
- **Deleting a group:** the FE POST `/fe/api/models/{id}` DELETE
  drops the row, the grouplb engine forgets its state, and the
  members (if they were not also claimed by another group) remain
  servable through their direct IDs.
- **Deleting a model that's a group member:** blocked by `Am27`
  (`DeleteModel` scans all groups for the member ID and returns
  "model X is a member of group Y; remove it from the group first"
  — direct DELETE on the chat model is refused). The credential
  side is NOT scanned (groups carry no credentials).

---

## 11. Client-facing wire changes: NONE

This feature ships no new endpoint, no new field, no new auth
surface. Clients name an existing chat-model ID and the proxy
quietly routes through the group. This is the §1 non-goal
"client-facing wire changes" guarantee — verified by the
`Am29` equivalence suite (`pkg/proxy/handler_groups_equivalence_test.go`).

If a future feature needs to expose group identity in the response
(the OpenAI wire does NOT carry a "this was a group" flag), it
should be added as an OPTIONAL response header, not an
unconditional field — wire-shape compatibility is the contract.
