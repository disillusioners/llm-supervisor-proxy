# LESSONS — decision-groups live E2E on the primary (2026-11-17)

Reusable operating gotchas from the first live E2E of the decision lane on llm.ensem.dev PRIMARY (image v200
`3cf3cb8`). Full evidence: `.agents/tester/RESULTS/2026-11-17-deploy-e2e-decision-groups.md`.

## 1. The decision lane's proof trail lives ONLY on the SSE event stream

`kubectl logs` carries **no** decision-lane per-attempt ordering (verified: 0 hits for the E2E's own ids across
5000 stdout lines). The pick → attempt → fail → advance → serve trail is published on the event bus.

- `GET /fe/api/events` is a **live SSE stream** that **hangs a naive GET forever**. Start it BEFORE the request:
  ```bash
  curl -sN --max-time 120 "$BASE/fe/api/events" > /tmp/e2e/events.log 2>&1 &   # -s AND -N
  ```
- **Use `curl -sN`, not `curl -N`.** Without `-s` the progress meter writes onto the SSE frame's line and a naive
  `startswith("data:")` parse **silently drops frames** — in this run it dropped the single most important frame
  (`upstream_transport_error`). Re-parse with `json.JSONDecoder().raw_decode`, not line splitting.
- **The stream REPLAYS a recent-event backlog** to each new subscriber (a fresh capture started at a timestamp
  *before* it was opened). Attribute events to a request by **timestamp vs the response's `Date` header**, never by
  capture order — other lanes share the same stream.
- The event envelope is `{"type":"decision","timestamp":<unix>,"data":{…}}`. The useful outcomes are
  `member_attempted`, `member_suppressed` (+`reason`), `upstream_transport_error`, `success` (+`member`,
  `provider`, `input_tokens`, `output_tokens`), `all_members_failed`.
- **`attempted` / `skipped` / `members_attempted` are counter keys INSIDE terminal/failure payloads**, not events.
  On a SUCCESS no payload carries them — derive them from the event sequence (count `member_attempted` =
  attempted, count `member_suppressed` = skipped, distinct members = members_attempted).

## 2. The admin surface is `/fe/api/models?kind=…` — the obvious paths 404

`GET /fe/api/groups` → **404**. `GET /fe/api/decision-models` → **404**. `GET /fe/api/models/{id}` → **405**
(no single-model GET; verify created rows via the list endpoint). A reviewer expecting `/fe/api/groups` will
wrongly conclude the feature is missing.

## 3. Delete ordering: a model that is a group member cannot be deleted

`DELETE /fe/api/models/{id}` → **409** while the row is a member of any group. Order: `PUT` the group (remove the
member) → `DELETE` the model → `DELETE` its credential.

## 4. Token plaintext is write-only — plan to create and delete a test token

`GET /fe/api/tokens` returns metadata only; `GET /fe/api/tokens/{id}` → **405**; the store matches on a SHA-256
hash. The plaintext is returned **exactly once**, at creation. A live E2E must therefore create a scoped token
and delete it afterwards (both `DELETE` → 204). The shell tool does not persist state between invocations — hold
the plaintext in a `chmod 600` file, read it into a variable per call, and **shred** it plus the creation-response
file at the end.

## 5. `allowed_models` is NOT a containment control on the group path

`pkg/decision/handler.go:317-326` returns into `serveDecisionGroup` **before** the allow-list check at
`:340-354`; `pkg/decision/group.go` has no `AllowedModels` reference at all. A token scoped to a narrow list is
still served for a decision-GROUP request; only the direct decision-MODEL path 403s. (Anomaly `c630644d`.)
Never use token scoping to bound a group E2E's blast radius.

## 6. Every `/v1/decisions` call is a real paid upstream call

Budget them explicitly. The house budget is **≤3 calls per provider, ≤6 total**; a full E2E (1 failover + 2
normal-selection + 1 final-state call) fits in **4**. A member pointed at an unresolvable RFC-2606 `.invalid`
host costs nothing (DNS failure ⇒ transport error ⇒ not limit-class ⇒ no mark, no suppression ⇒ `skipped = 0`).

## 7. The failover staging that actually makes the head deterministic

The head is a **fresh weighted pick from the DEFAULT pool** (no sticky binding), so "first in the list" is not
the head. Make the throwaway the **SOLE default-pool member** and put the live models in `backup_only: true` —
then the head IS the throwaway, deterministically. A live member appearing as the head means the staging did not
apply: stop and re-verify rather than record a "failover". (`backup_only` is the tier field; there is no `tier`
or `enabled` field on a member, and no top-level group `weight`/`pools` field.)

## 8. The metering row is the winner's member id

`GET /fe/api/usage/models?view=hourly` → the row's `model_id` must be the **served member's** id, never the
group id and never the failed member. Verified live: the group id and the throwaway id have **no** usage rows.
There is no request-id in a usage row — correlate via `model_id` + `hour_bucket` (UTC hour).

## 9. A concurrent lane may share the host

Another commission ran its own E2E on the same pod during this run. Use distinctive row ids
(`<feature>-e2e-<thing>`), snapshot the id sets before and after, and diff — the `/v1/models` diff was empty and
the credential diff was 0, which is what proves no cross-talk.
