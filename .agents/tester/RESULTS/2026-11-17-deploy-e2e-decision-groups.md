# LIVE E2E — decision-groups — llm-supervisor-proxy PRIMARY (llm.ensem.dev) — image v200 `3cf3cb8`

**Date**: 2026-11-17 · **Host**: PRIMARY (llm.ensem.dev, RKE2) · **Namespace**: `llmproxy`
**Pod**: `llm-supervisor-proxy-689b45cd67-bx5lq` · **Image**: `docker.io/disillusioners/llm-supervisor-proxy:3cf3cb8`
**imageID**: `sha256:c11dc5c9bc5c50486e0d66cf04674fc2ad120a49cf37a31369d10a2fe52da70a` — byte-exact match to the v200 digest
**Deployment**: generation **40**, revision 34 · **Restarts: 0 before → 0 after**, `startedAt` unchanged
**Runbook**: `.agents/shared/planning/decision-groups/phase3-plan.md` § Deploy runbook step 7 (E1–E10)
**Gate (pre-merge)**: `RESULTS/decision-groups-gate.md` · **Gate (post-merge)**: `RESULTS/decision-groups-gate-post-merge.md`

---

## 0. Verdict

# 🟢 E2E VERIFIED — the failover path was EXERCISED and proven live

The feature's whole point is proven live: **a real decision group, serving a real decision request, with member
selection AND failover observed.** The deterministic staging worked — the throwaway was the policy head, it
genuinely failed, the lane advanced, and a **live member served the request with HTTP 200**. The end state is the
user's: the group exists with exactly the 2 live decision models in the default pool, and the live credentials
are **provably untouched**.

| Corpus completion bar | Result |
|---|---|
| the primary E2E shows a real decision group serving from the user's 2 live decision models | ✅ 4 real requests, 4 × HTTP 200 |
| with the throwaway-member failover observed | ✅ **observed** — `member_attempted(dg-e2e-throwaway)` → `upstream_transport_error` → `member_attempted(decision)` → `success` |
| the group is left in place with exactly the 2 live members | ✅ group kept, 2 members, both default pool |
| the live credentials are provably untouched (id-set + metadata diff) | ✅ 21 → 21, id diff empty, **metadata diffs = 0** |

---

## 1. The primary's 2 live decision models (E1)

`GET /fe/api/models?kind=decision` → **2 rows**, both `enabled=true`, `internal=true`. `provider` is not a model
field — it was derived by joining `/fe/api/credentials` on `credentials[].credential_id`.

| id | name | kind | provider | internal_model | internal_base_url | credentials |
|---|---|---|---|---|---|---|
| `decision` | Decision | decision | **openai** (`openai-kha`) | `gpt-6-luna` | `https://api.openai.com` | 1 |
| `decision-openrouter` | Decision-OpenRouter | decision | **openrouter** (`openrouter`) | `jev-1.13` | `https://openrouter.ai/api` | 1 |

No credential value was printed at any point.

## 2. Group configuration — BEFORE (staged) and AFTER (restored)

**Staged** (`POST /fe/api/models` → 201) — throwaway = **sole default-pool member**, both live models backup-only:

```json
{"id":"dg-e2e-decision-group","name":"DG E2E Decision Group","kind":"group","enabled":true,"internal":true,
 "fallback_chain":[],
 "members":[{"model_id":"dg-e2e-throwaway","weight":10},
            {"model_id":"decision","weight":10,"backup_only":true},
            {"model_id":"decision-openrouter","weight":10,"backup_only":true}]}
```

**Restored** (`PUT /fe/api/models/dg-e2e-decision-group` → 200) — staging fully undone, weights unchanged:

```json
{"members":[{"model_id":"decision","weight":10},{"model_id":"decision-openrouter","weight":10}]}
```

`backup_only` is **absent on both** after the restore ⇒ both are back in the **default pool**. The group was
**kept** (corpus `E8b` / W6: the created group is the user's desired end state — **never delete the group**).

## 3. The failover proof — the full decision-event sequence (E5/E6/E7)

Captured from the live SSE stream (`GET /fe/api/events`) while the request was in flight. **Verbatim, in order:**

```
data: {"type":"decision","timestamp":1794916691,"data":{"duration_ms":6,"member":"dg-e2e-throwaway","model":"dg-e2e-decision-group","outcome":"member_attempted","provider":"openai","upstream_status":0}}

data: {"type":"decision","timestamp":1794916691,"data":{"duration_ms":132,"member":"dg-e2e-throwaway","model":"dg-e2e-decision-group","outcome":"upstream_transport_error","provider":"openai","upstream_status":0}}

data: {"type":"decision","timestamp":1794916691,"data":{"duration_ms":132,"member":"decision","model":"dg-e2e-decision-group","outcome":"member_attempted","provider":"openai","upstream_status":0}}

data: {"type":"decision","timestamp":1794916691,"data":{"duration_ms":770,"input_tokens":181,"member":"decision","model":"dg-e2e-decision-group","outcome":"success","output_tokens":181,"provider":"openai","upstream_status":200}}
```

**Response** (HTTP **200**):
```json
{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"needs_refund","probability":0.7}],
 "usage":{"input_tokens":181,"output_tokens":0,"total_tokens":181,
          "input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},
          "output_tokens_details":{"reasoning_tokens":0}}}
```

**Assertion by assertion (corpus E7):**

| # | Expectation | Observed | ✓ |
|---|---|---|---|
| a | status 200 | **200** | ✅ |
| b | served by a **live backup member** (never the throwaway) | `decision` (openai) | ✅ |
| c | the FIRST `member_attempted` is the **throwaway** (deterministic policy head) | `dg-e2e-throwaway` | ✅ |
| d | the throwaway **genuinely failed before any commit** | `outcome:"upstream_transport_error"`, `upstream_status:0` (the RFC-2606 `.invalid` host never resolves); **no `success` event for the throwaway** | ✅ |
| e | the lane **advanced** to the next member | `member_attempted` → `decision` → `success` | ✅ |
| f | `skipped == 0` (a transport error is not limit-class ⇒ marks nothing) | **0 `member_suppressed` events** | ✅ |
| g | `attempted` / `members_attempted` | **2 / 2** (derived: 2 `member_attempted` events, 2 distinct members) | ✅ |
| h | exactly one client-facing write | one request, one `success` event, one 200 response | ✅ |
| i | the **metered model id is the WINNER's member id** — not the group id, not the throwaway | `{"model_id":"decision","model_name":"Decision","hour_bucket":"2026-11-17T11","request_count":1,"prompt_tokens":181,...}` — **no row exists for `dg-e2e-decision-group` or `dg-e2e-throwaway`** | ✅ |

The counters `attempted` / `skipped` / `members_attempted` are **counter keys inside terminal/failure event
payloads**, not separate events; on a successful request no payload carries them, so they were **derived from
the event sequence** as the shapes recon established.

**Pod stdout does NOT carry the decision lane's per-attempt ordering.** `kubectl logs --tail=2000 | grep -i decision`
returned exactly **1** line, and it was this E2E's own probe; `grep -ci dg-e2e` over the last 5000 stdout lines
returned **0**. The SSE event stream is the **only** source for the pick→attempt→fail→advance→serve trail.

## 4. Which member served each request

| Step | Status | Serving member | provider | upstream | Response model | Usage |
|---|---|---|---|---|---|---|
| E5 (staged) | 200 | **`decision`** | openai | 200 | `gpt-6-luna` (prob 0.7) | 181/0/181 |
| normal #1 | 200 | **`decision-openrouter`** | openrouter | 200 | `typesafe/jev-1.13-20260917` (prob 0.76) | 299/22/321 |
| normal #2 | 200 | **`decision`** | openai | 200 | `gpt-6-luna` (prob 0.7) | 181/0/181 |
| E10 (cleaned group) | 200 | **`decision`** | openai | 200 | `gpt-6-luna` (prob 0.7) | 181/0/181 |

With both members restored to the default pool the **fresh weighted pick** rotated across the two live members
(openrouter then openai) — the rotation evidence the commission required.

**Upstream-call budget**: **4 paid decision calls total — 3 openai / 1 openrouter**, within the corpus's
`live_e2e_test.go` budget (≤3 per provider, ≤6 total). The throwaway's attempt cost **nothing** (DNS failure).

## 5. Cleanup proof (E8/E8b/E9)

```
PUT    /fe/api/models/dg-e2e-decision-group  -> 200   (restore: 2 live members, default pool)
DELETE /fe/api/models/dg-e2e-throwaway        -> 204
DELETE /fe/api/credentials/dg-e2e-cred        -> 204
```

Post-cleanup reads:
- `GET /fe/api/models?kind=group` → the group exists with **exactly 2 members**
  (`{"model_id":"decision","weight":10}`, `{"model_id":"decision-openrouter","weight":10}`), `backup_only` absent
  on both. Group ids: `agentic-group, coding-group, dg-e2e-decision-group, quick-group, title, vision-group`.
- `GET /fe/api/models?kind=decision` → **2 rows** again (`decision`, `decision-openrouter`).
- `GET /fe/api/credentials` → `dg-e2e-cred` **absent**; 21 rows.
- **E9 diff (ids + metadata only — provider / base_url / attached models):**
  ```
  before rows: 21 | after rows: 21
  ids only in BEFORE: none
  ids only in AFTER : none
  => metadata diffs on shared ids: 0
  ```
  Live decision credentials unchanged: `openai-kha` (openai) → attached `decision`; `openrouter` (openrouter) →
  attached `decision-openrouter` + `title-openrouter`.

**Ordering constraint discovered**: `DELETE /fe/api/models/{id}` returns **409** if the row is a member of a
group — the throwaway must leave the group *before* it is deleted. The sequence above respects that.

## 6. Every live row created and removed

| row | type | created | removed | end state |
|---|---|---|---|---|
| `dg-e2e-cred` | credential (openai, fake placeholder key) | `POST /fe/api/credentials` → 201 | `DELETE` → 204 | **absent** |
| `dg-e2e-throwaway` | model (`kind=decision`, `internal`, `.invalid` base URL) | `POST /fe/api/models` → 201 | `DELETE` → 204 | **absent** |
| `dg-e2e-decision-group` | group | `POST /fe/api/models` → 201 | **KEPT** — members restored via `PUT` → 200 | present, 2 live default-pool members |
| `dg-e2e-decision` | token (allowed_models: group + throwaway + both live models + both canaries) | `POST /fe/api/tokens` → 201 | `DELETE` → 204 | **absent** |
| `dg-e2e-authz-probe` | token (transient, authz probes only; `allowed_models:["quick-group"]`) | `POST /fe/api/tokens` → 201 | `DELETE` → 204 | **absent** |

`GET /fe/api/tokens` after cleanup → **5 rows** (the original count); both `dg-e2e-*` names **absent**.

**Untouched (verified)**: `decision`, `decision-openrouter`, `openai-kha`, `openrouter`, every other model, group
and token, all auth/affinity settings, and **no live member's weight**. No kubectl mutations (only `get`,
`logs`, `version`, `config current-context`). No source edits, commits, pushes or deploys.

**Redaction**: the token plaintext was never printed. Because the shell tool does not persist state across
invocations it was held in a `chmod 600` file, read into a variable per call, and **shredded** at the end; the
token-creation response files were shredded; a final re-scan of the scratch dir found **0** files containing an
`api_key` or `token` value field. The throwaway credential used only the fake placeholder
`sk-e2e-throwaway-not-a-real-key`.

## 7. FE live check

`GET /ui` → 307 → `Location: /ui/` → `GET /ui/` → 200 (548 B SPA index) referencing
`/ui/assets/index-DdLeb6Xb.js` and `/ui/assets/index-Diz8jSFK.css`; the entry lazily loads
`./SettingsPage-BDCXaBm2.js`.

| asset | bytes | sha256 | "Decision Model" | "Decision Group" | "credential-groups" |
|---|---|---|---|---|---|
| `index-DdLeb6Xb.js` | 313 576 | `7eb3eead201b0efce…` | 0 | 0 | 1 (`/fe/api/credential-groups` fetch path) |
| `SettingsPage-BDCXaBm2.js` | 212 111 | `1ed73b461af8a974…` | **10** | **3** | **9** |
| `index-Diz8jSFK.css` | 52 837 | `a14e8d1c97d05d69…` | 0 | 0 | 0 |

The decision-lane markers live in the **SettingsPage chunk** — `"Add Decision Model"`, `"Edit Decision Model"`,
`"Decision Models"`, `"Decision Groups"`, `"Add Decision Group"`, `data-testid="decision-container-tab-models"`,
`decision-container-tab-group`, `credential-groups-status/-add/-empty/-list`. The asset set matches the
post-merge gate's byte-identity proof (`SettingsPage-BDCXaBm2.js`, `index-DdLeb6Xb.js`, `index-Diz8jSFK.css`,
`index.html`) — **the live pod serves the bundle the gate proved reproducible**.

**Admin decision-lane data**: `?kind=decision` → 2 rows; `?kind=group` → our group's members `decision` +
`decision-openrouter`, both resolving to `kind=decision` ⇒ the group is entirely decision-lane.

## 8. Non-regression

```
GET /v1/models  ->  before: 27 ids    after: 27 ids
diff  models_before.txt models_after.txt  ->  (EMPTY)
cmp   models_before.txt models_after.txt  ->  BYTE-IDENTICAL
```

**Chat canaries** (`POST /v1/chat/completions`):
- unauthenticated → **401** `{"error":{"type":"authentication_error","message":"Invalid or expired API key"}}`
  for both groups — expected and correct: `/v1/*` requires Bearer/X-API-Key (only `/v1/models` is public by
  design). The runbook's canary step did not specify a header; the authenticated form below is the meaningful one.
- with the bearer token (which lists both groups) → **`coding-group`: 200** (content `pong`, 203/18/221) and
  **`agentic-group`: 200** (content `Pong! I'm here and ready to help…`, 13/147/160).

**A concurrent lane was active on this host during the run** (usage rows `model_id=credgrp-verify-1794910766`
and heavy unrelated traffic in the shared event stream). Our ids were distinctive; the `/v1/models` diff was
empty and the credential diff was 0 — **no cross-talk**.

---

## 9. Findings

### 9.1 🔴 The decision-GROUP path bypasses the token `allowed_models` check (defect in the shipped v200)

Verified against the **shipped source at the live SHA** (`git show origin/master:pkg/decision/handler.go`,
`origin/master` = `3cf3cb8` = the running image):

- `handler.go:317-326` — the group branch `if model.IsGroup() && h.groupIsDecisionLane(model.ID) {
  h.serveDecisionGroup(...); return }` **returns at `:325`**, i.e. **before** the allow-list check.
- `handler.go:340-354` — the allow-list gate (`len(authToken.AllowedModels) > 0` → `IsModelAllowed` → **403**)
  is reached **only after** the group branch has already returned.

Free empirical corroboration (probe token `allowed_models=["quick-group"]`, no upstream call, no mutation):

| probe | target | result | meaning |
|---|---|---|---|
| A | decision **model** `decision` (not allowed) | **403** `{"error":{"message":"model not allowed for this token","model":"decision","type":"access_denied"}}` | the allow-list **is** enforced on the model path |
| B | **group** `coding-group` (not allowed) | **400 `decision_misroute`**, not 403 | the handler dispatches the group branch **before** the allow-list ⇒ a group id never reaches it |
| C | the decision-lane group | **NOT fired** — it would have been a 5th paid upstream call, over the runbook's ≤4/≤3 budget | the decision-lane-group half is proven from the shipped source at the live SHA + probe B's structural evidence, **not** by a direct live 403-test |

**Therefore the absence of a 403 in this run is NOT proof of scoping** — the run's token listed the group
anyway, and a group request *cannot* be 403'd by that list. **A token whose `allowed_models` omits a decision
group is not constrained from using it.** Filed as anomaly `c630644d`.

### 9.2 🟢 RESOLVED — credential `api_key` IS server-masked on every read path (no defect)

Two workers initially disagreed (one: "server-masked `sk-…***`"; one: "plaintext, unmasked"). A dedicated
read-only check settled it:

| check | result |
|---|---|
| `GET /fe/api/credentials` | **200**, JSON array of **21** objects, fields exactly `{id, provider, api_key, base_url}` |
| values matching a "real long secret" shape (`sk-`/`sk-proj-`/≥24 alnum, no mask marker) | **0** |
| values containing a mask marker (`***`) | **21** — uniformly **11 chars** (`first 8 + ***`), suffix length 0 in 21/21 |
| short / placeholder / empty values | **0** |
| `GET /fe/api/credentials/{id}` (detail) | **masked identically** |
| `GET /fe/api/tokens` | **no secret material at all** — fields exactly `{id, name, created_at, created_by, allowed_models}`; the plaintext is returned **once, at creation** by design (`// Show once!`) |

**Why the "plaintext" sighting happened — and it is NOT a store read.** `pkg/ui/server.go:2775` (POST create) and
`:2878` (PUT update) assign `APIKey: <body value>` **unmasked** — the handler echoes back exactly what the
caller submitted. The E2E worker had just created `dg-e2e-cred` with a placeholder key and read the **response
body**, so it saw its own placeholder verbatim; its scrubber then reported "plaintext". Full plaintext is never
returned by any read path.

Two lesser observations (no action taken, neither a regression):
- **The create/update response echoes the submitted key unmasked**, and a `PUT` that omits `api_key` substitutes
  and echoes the **existing stored key** unmasked (`:2869`). That echo is of the caller's own input (or of the
  key it just replaced) — low severity, but worth knowing before scripting against it.
- **Masking discloses the first 8 characters** of every live key to an unauthenticated local caller (the admin
  API is unauthenticated inside the binary; OAuth is at the ingress, and a port-forward bypasses it). For
  `sk-proj-…`/`sk-ant-…` keys those 8 chars are a pure vendor prefix with no entropy; for keys with substantive
  leading entropy it is a partial disclosure.
- Related standing posture (not introduced by this release): the primary's store keeps provider keys in
  **plaintext** because `INTERNAL_ENCRYPTION_KEY` is unset — the documented fallback in the crypto posture.


### 9.3 🟡 The decision lane does not log to stdout — the SSE stream is the only evidence source

`kubectl logs` carries no decision-lane per-attempt ordering. Any future E2E must capture the event stream.

### 9.4 🟡 `/fe/api/events` REPLAYS a recent-event backlog to each new subscriber

A fresh capture began at ts `11:59:37`, before it was opened. Per-request attribution must be done by matching
event **timestamps** to the response's `Date` header — **not** by capture order.

### 9.5 🟡 `curl -N` without `-s` can SILENTLY drop an SSE frame

curl's progress meter wrote onto the same line as one frame, so a naive `startswith("data:")` parse dropped the
`upstream_transport_error` event — the single most important frame in the proof. Re-parsed with
`json.JSONDecoder().raw_decode` → 4 events. **Use `curl -sN` for SSE capture.**

---

## 10. Operational notes for the next live E2E (reusable)

1. `GET /fe/api/events` **hangs a naive GET** — start `curl -sN … &` BEFORE the request, kill it after.
2. `curl -sN`, not `curl -N` — the progress meter corrupts the SSE stream.
3. The SSE stream carries a **backlog** — attribute by timestamp, not order.
4. The admin surface for groups/decision-models is **`/fe/api/models?kind=group|decision`**; `/fe/api/groups`
   and `/fe/api/decision-models` both **404**.
5. **No single-model GET** (`/fe/api/models/{id}` → 405) — verify created rows through the list endpoint.
6. `DELETE /fe/api/models/{id}` → **409** while the row is a member of a group — leave the group first.
7. Token plaintext is returned **once, at creation**, and is **never** retrievable — a test token must be
   created and later deleted.
8. The **decision lane never writes per-attempt ordering to stdout**.
9. `allowed_models` is **not** enforced on the group path (§9.1) — never use it as a containment control.
10. **Every** `/v1/decisions` request is a real paid upstream call — budget them explicitly.

---

## 11. Deliverables

- This file: `.agents/tester/RESULTS/2026-11-17-deploy-e2e-decision-groups.md` — **NEW, needs a follow-up commit**.
- `.agents/tester/LESSONS/2026-11-17-decision-groups-live-e2e-gotchas.md` — **NEW, needs a follow-up commit**.
- `PACKS.md` — 2026-11-17 live-E2E note.
- `QUARANTINE.md` — **no change**: the E2E discovered no new flake.
- No source file was modified; no commit, push or deploy was performed by the tester.
