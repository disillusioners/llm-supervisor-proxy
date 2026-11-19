# 2026-11-19 — Probe C live-acceptance lessons (AUTH COMMISSION v203, both hosts)

Reusable knowledge from the live acceptance probe of the deployed v203 authz commission. Recipe distilled into skill `live-deploy-acceptance-probe` (`55e48bb9-8083-49ff-8cb5-de1880856fd9`). Full report: `RESULTS/2026-11-19-probe-c-live-acceptance-v203.md`.

## L1 — Discover the request shape BEFORE probing; a wrong body masquerades as a wrong status
`POST /v1/decisions` requires `questions[]` (a predicate needs `type` + `name` + `instructions`) alongside `model` + `input` — `pkg/decision/types.go` `DecisionRequest{model,input,questions}`, `pkg/decision/validators.go`. A bare `{"model":...}` fails validation ⇒ 400-validation, not 403-authz — which would have been misread as "the scope gate did not fire". Derive the minimal valid body from source, and read the existing harness (`~/.config/llmproxy-smoke/probe.sh`) first — it already carries the working shapes.

## L2 — The public catalog hides decision-kind ids
`GET /v1/models` (200, 27 ids) contains **no** decision-kind model or group. Decision ids live only in the pod-direct admin API (`GET /fe/api/models`). Kinds must be confirmed from source semantics, not guessed: `kind:"group"` + unanimous decision-kind members ⇒ decision lane (`pkg/models/group_lane.go` `UnanimousGroupLane`/`DeriveGroupLane`). A group with **no `shadow_model_id`** is only reachable by naming the group id directly — which is precisely the bypass vector P1/P1d exercise.

## L3 — An absent target is UNTESTABLE — never PASS, never a bypass failure
The backup host did not carry `dg-e2e-decision-group` (admin rows primary 37 / daoduc 36; sole diff). The request returns **404 unknown model**, which precedes the scope gate, so the authorization arm is structurally unreachable there. Rule: report it as an ENVIRONMENT FACT with the admin row counts as evidence, and **run the identical request on the peer host as a positive control** to pin the expected gate shape. Local absence must never be reported as a pass, and never as a bypass failure either.

## L4 — Proof of "rejected pre-upstream" comes from source, not from the status code
Before claiming a 401/403/400/404 cost zero upstream calls, cite the gate order in source: decision-lane scope gate `pkg/decision/handler.go:359` (step 7), decision group arm ARM#1 `pkg/decision/group.go:124/:139`, chat-lane scope gate `pkg/proxy/handler.go:847` — all precede credential resolution, candidate construction, and any upstream attempt.

## L5 — Wildcard credential: no local value ⇒ transient mint + immediate revoke
All three mode-600 files in `~/.config/llmproxy-smoke/` are narrow (`smoke-primary-agentic`→`[agentic-group]`, `smoke-primary`/`smoke-15m`→`[coding-group,quick-group]`); the in-store wildcards (`Ha`,`tuyen`,`kha`) expose **no values** from the list endpoint. Recipe: pod-direct `POST /fe/api/tokens` with body `{"name":...,"expires_at":...}` and **both grant axes omitted** ⇒ NULL ⇒ wildcard (conjunctive rule), response 201; use once; `DELETE /fe/api/tokens/<id>` ⇒ **204**; re-list to prove the row is gone. Always add a short `expires_at` as a fail-safe belt. The value never touches argv, stdout, or the evidence file.

## L6 — Parallel probe workers need distinct local port-forward ports
Two workers on one host collided on the suggested 18443/28443; the daoduc worker moved to 18444 and the primary cross-check to 18445. Convention: **one distinct local port per concurrent worker per host**, and record the PID so teardown is scoped (`ss -ltnp` + `pgrep -af 'kubectl.*port-forward'` as the free-and-clean proof).

## L7 — Envelope shapes differ by gate; do not read the difference as a defect
The 403 envelope carries exactly three keys (`type`/`message`/`model`) and **no `code`** — this is what the commission pinned. The 400 misroute envelope carries `code:"decision_misroute"` with `type:"server_error"` (`openAIErrorWithCode`). Both are correct; the 403's missing `code` is the assertion that matters.

## L8 — Edge behaviour is asymmetric between the two hosts
Primary `/` → 302 to `https://door.ensem.dev/login/oauth/authorize` (oauth2-proxy); backup `/` → 302 to `/ui/` (plain nginx, no OAuth on the backup edge). Do not read the backup's missing OAuth door as a misconfiguration.

## L9 — Upstream-call budget is the real cost lever
The whole probe set cost **3** real upstream calls (2 chat completions + 1 decision) on primary and **0** on daoduc, because every authorization/validation rejection happens before candidate construction. Design probes so each assertion is a pre-upstream rejection wherever possible; state the count explicitly in the report.
