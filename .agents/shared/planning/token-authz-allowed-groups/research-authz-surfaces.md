# AUTH COMMISSION — Authorization Surface Research (READ-ONLY)

**Basis:** worktree `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`, branch `feature/token-authz-allowed-groups`, base `a3f0335`.
**Method:** every line number below was read/grepped INSIDE that worktree (the main checkout `/home/nea/Code/opensource-projects/llm-supervisor-proxy` is a STALE tree — see "Drift" §7 — and must NOT be used for line numbers).
**No source file was modified.** This file is the only artifact written.

---

## 1. Every site in the repo that evaluates token scope

**The "only 4 sites" claim is CONFIRMED.** `IsModelAllowed` has exactly 4 non-test call sites, and there is **no equivalent scope check anywhere else** in the repo.

| # | File:line (a3f0335) | Lane / arm | Exact string checked | Source of that string |
|---|---|---|---|---|
| 1 | `pkg/decision/handler.go:343` (guard `:342`) | Decision **MODEL** arm (Step 7) | `modelID` | `modelID := req.Model` (`pkg/decision/handler.go:282`) — the raw client-requested string; `req` parsed from the body at `:263` |
| 2 | `pkg/imggen/handler.go:268` (guard `:267`) | Image-gen arm | `modelID` | client-requested body model (imggen lane; `modelID` bound from the parsed request before `:267`) |
| 3 | `pkg/proxy/handler.go:839` | Chat (OpenAI) arm | `modelID` = `rc.reqLog.Model` (`:835`) | client-requested string; `rc.reqLog.Model` set at `pkg/proxy/handler_functions.go:132` from `requestBody["model"]` (`:108`) |
| 4 | `pkg/proxy/handler_anthropic.go:514` | Anthropic arm | `reqLog.Model` | client-requested string; set at `pkg/proxy/handler_anthropic.go:186` from `anthropicReq.Model` |

**Guard shapes differ (matters for the fix):**
- `:839` (chat) — unconditional call: `if !authToken.IsModelAllowed(modelID)`; the wildcard is handled *inside* `IsModelAllowed` (`len==0 ⇒ true`). A second, separate arm at `pkg/proxy/handler.go:857` (`else if len(authToken.AllowedModels) > 0`) is the **unknown-model fail-closed** arm (model not in DB + token restricted ⇒ 403).
- `:514` (anthropic) — unconditional call: `if authToken != nil && !authToken.IsModelAllowed(reqLog.Model)`. The unknown-model fail-closed arm is **unreachable** on this lane by construction (comment `:510-513`: the boundary 404 gate already rejected unknown/non-internal models).
- `:343` (decision) and `:268` (imggen) — **double-guarded**: `if authToken != nil && len(authToken.AllowedModels) > 0 { if !authToken.IsModelAllowed(...) }`. The `len>0` guard is redundant with `IsModelAllowed`'s own wildcard rule but is the shape the fix must preserve ("empty allowed_models = wildcard").

**Complete enumeration of `.AllowedModels` field reads in non-test code** (grep `\.AllowedModels`, 55 hits, all accounted for):
- `pkg/auth/token.go:116`, `:119` — the wildcard rule + membership loop (the single source of truth).
- `pkg/auth/store.go:137`, `:179`, `:246` — hydration via `ScanAllowedModels` (ValidateToken / ListTokens / GetTokenByID).
- `pkg/decision/handler.go:342`; `pkg/imggen/handler.go:267`; `pkg/proxy/handler.go:857` — the three `len>0` guards above.
- `pkg/ui/server.go:2593`, `:2596` (PATCH keep/set), `:2634` (token response), `:2682-2683` (create), `:2698` (create response) — **admin write/read surfaces only, never a decision**.

**Negative findings (important for the commission):**
- **`pkg/mcp` performs NO scope check.** `pkg/mcp/auth.go:48-57` validates the bearer token and injects it into the request context, but nothing downstream ever reads `AllowedModels`. A restricted token therefore reaches every MCP tool.
- **`pkg/ui` never evaluates scope** — it only writes/reads the field.
- `pkg/auth/store.go` never evaluates scope (storage only).

**There are only THREE 403-envelope writers** even though there are four call sites — the Anthropic lane reuses the chat writer:
- `pkg/proxy/handler.go:525-535` `sendModelNotAllowedError` (chat **and** Anthropic: called at `handler.go:854`, `:874`, `handler_anthropic.go:530`).
- `pkg/decision/handler.go:784-794` `sendModelNotAllowedError`.
- `pkg/imggen/handler.go:713-723` `sendModelNotAllowedError`.

---

## 2. Decision lane control flow at a3f0335 (`pkg/decision/handler.go`, 916 lines)

`HandleDecision` = `:205-916`.

| Step | Lines | Content |
|---|---|---|
| Step 1 | `:208-212` | POST-only ⇒ 405 `http.Error` (plain text, **not** the OpenAI envelope) |
| Step 2 | `:214-245` | 16 MB `MaxBytesReader` ⇒ 413 |
| **Step 3 — AUTH** | **`:247-257`** | `authToken, ok := h.authenticate(r)` (`:248`); `!ok` ⇒ event `auth_failed` (`:250-254`) + `h.sendAuthError(w)` (`:255`) ⇒ **401** |
| Step 4 | `:259-282` | typed parse (`:263`); `req.Model == ""` ⇒ 400 "model field is required" (`:279`); `modelID := req.Model` (`:282`) |
| Step 5 | `:284-298` | typed validators ⇒ 400 `openAIError` |
| Step 6 | `:300-338` | `safeResolveModel` (`:305`) ⇒ 404 (`:313-314`); then the **kind gate** |
| **Step 7 — ALLOW-LIST** | **`:340-354`** | `if authToken != nil && len(authToken.AllowedModels) > 0 {` (`:342`) → `if !authToken.IsModelAllowed(modelID)` (`:343`) ⇒ event `model_not_allowed` (`:344-350`) → `h.sendModelNotAllowedError(w, modelID)` (`:351`) ⇒ **403** |
| Step 8 | `:356-371` | credential resolution ⇒ 502 |
| Step 9 | `:373-389` | provider lookup ⇒ 502 |

### The MODEL arm vs the GROUP arm — the defect, exact lines

```
:317    if !model.IsDecision() {
:318-322      // comment: the GROUP branch … the gate is EXTENDED, never widened blindly
:323        if model.IsGroup() && h.groupIsDecisionLane(model.ID) {
:324            h.serveDecisionGroup(w, r, model, authToken, &req, rawBody, startTime)
:325            return                      ← RETURNS BEFORE STEP 7
:326        }
:327        log.Printf("[decision] rejecting decision-route post to non-decision kind …")
:328-333      h.publishEvent(…, "outcome": decisionMisrouteCode, …)
:334-336      h.openAIErrorWithCode(w, http.StatusBadRequest,
                                     fmt.Sprintf("decision models are served by /v1/decisions only (got kind %q)", model.Kind),
                                     decisionMisrouteCode)      ← 400 decision_misroute
:337        return
:338    }
```

**CONFIRMED:** the decision **GROUP arm** is `pkg/decision/handler.go:317-326` and it `return`s at `:325`, i.e. **before** the Step-7 allow-list at `:340-354`. A decision group request therefore performs **no token-scope evaluation at all**. The report's cited ranges (`:317-326` vs `:340-354`) are **exact — no drift**.

### `groupIsDecisionLane` (fail-closed entry gate)
- `pkg/decision/handler.go:855-862`: `lane, ok := h.groupLane(groupID)`; `!ok` ⇒ **loud log** (`:858`) + `return false` ⇒ fail-closed (the caller then takes the 400 `decision_misroute` path). Returns `lane == models.GroupLaneDecision` (`:861`).
- `groupLane` (`:836-845`): nil-safe; requires the OPTIONAL `groupLaneSeam` (`:143-146`); absent seam ⇒ `("", false)` ⇒ fail-closed.
- Seam asserted implemented by the cache decorator: `pkg/decision/handler.go:153` (`var _ groupLaneSeam = (*modelscache.CachedModelsConfig)(nil)`).

### Second, independent lane gate inside the group arm
`pkg/decision/group.go:101-114`: `lane, ok := h.groupLane(groupID)`; `!ok || lane != models.GroupLaneDecision` ⇒ loud log (`:103`), event `outcome: decision_misroute` (`:104-109`), `openAIErrorWithCode(w, 400, "decision models are served by /v1/decisions only (group %q is not a decision group)", decision_misrouteCode)` (`:110-112`) ⇒ **400**.

### 400 `decision_misroute` error-precedence
`decisionMisrouteCode = "decision_misroute"` — `pkg/decision/handler.go:73`.
Precedence, in order:
1. **401** (Step 3, `:255`) — auth first, always.
2. **404** (`:313`) — unknown model, before the kind gate.
3. **400 `decision_misroute`** (`:334-336`) — model resolved but is not a decision kind (chat/img-gen model, unknown kind) **and** is not a decision-lane group.
4. **400 `decision_misroute`** (group.go:110-112) — group whose lane is not decision (or unresolvable).
5. **403** (`:351`) — scope denial — **only reachable on the MODEL arm** (the GROUP arm returns at `:325` before it).
6. 502 / other upstream statuses after.

---

## 3. Decision-group candidate list construction (`pkg/decision/group.go` 723 lines + `pkg/decision/policy.go` 166 lines)

### `serveDecisionGroup` — `pkg/decision/group.go:96-307`
- `:96` signature — takes `authToken *auth.AuthToken` **as a parameter**.
- `:101-114` — lane gate (fail-closed, see above).
- **`:118` — `policy := buildGroupPolicy(group, h.currentModelRows(), lane)`** ← **THE candidate-list construction site**.
- **`:119` — `order := policy.order()`** ← **THE attempt-order construction site** (weighted head → defaults → backups).
- `:120-123` — empty order ⇒ `exhaustDecisionGroup(w, groupID, 0, 0, 0, startTime)` ⇒ 502.
- `:125-140` — per-request bookkeeping: `triedMembers`, `triedPairs`, `granted` (the per-member latch).
- `:142-244` — **the member loop** (`for idx := range order`): per candidate → per-credential rotation loop (`:152-240`) → `attemptDecisionMember` (`:205`).
- `:246-276` — the OQ-1 "suppression never removes the last attempt" lift (`:251-275`).
- `:278-291` — terminal: fail-fast (`failFastDecisionGroup`) or exhaustion (`exhaustDecisionGroup`).
- `:296` — `h.commitDecisionWinner(w, groupID, winner, authToken)` (single client write).

### `buildGroupPolicy` — `pkg/decision/policy.go:58-96` (the candidate list itself)
```
:58  func buildGroupPolicy(group *models.ModelConfig, rows []models.ModelConfig, lane models.GroupLane) groupPolicy
:63      policy := groupPolicy{GroupID: group.ID, Lane: lane}
:64-67   byID := index of the live model rows (claim-blind — the member set)
:68      for _, member := range group.Members {          ← configured membership (models.ModelConfig.Members)
:69-71       if member.ModelID == "" { continue }
:72-75       row, ok := byID[member.ModelID]; if !ok { continue }   // vanished member — unservable
:76-78       if !row.Enabled { continue }                            // inactive member — never a candidate
:79-84       if memberLane, ok := models.MemberLane(row.Kind); !ok || memberLane != lane { continue }  // lane homogeneity
:85-88       weight := member.Weight; if weight <= 0 { weight = 1 }
:89-92       pool := models.PoolDefault; if member.BackupOnly { pool = models.PoolBackup }
:93           policy.Members = append(policy.Members, policyMember{ID: member.ModelID, Weight: weight, Pool: pool})
:94      }
```
`policy.Members` (built at `policy.go:93`) **is** the candidate list. `group.Members` is `models.ModelConfig.Members []GroupMember` (`pkg/models/config.go:415`); `GroupMember` carries `ModelID`, `Weight`, `BackupOnly` (`pkg/models/group.go:38-47`).

### `groupPolicy.order()` — `pkg/decision/policy.go:110-136` (the attempt order)
- `:114-122` — split into `defaults` / `backups` by pool.
- `:124-126` — **weighted head**: `head := defaults[pickWeighted(defaults)]` (fresh draw every request — a decision request carries no conversation).
- `:127-131` — the remaining default-pool members, in configured order.
- `:133-134` — **the backup pool last** ("a RECOURSE: it follows every default member").
- `pickWeighted` — `pkg/decision/policy.go:141-166` (cumulative prefix sums + binary search).

### ⇒ The construction-time exclusion seam (answer to "WHERE")
There are exactly **two** construction points, and the correct one is the **policy build**, not the loop:
1. **PRIMARY:** `pkg/decision/policy.go:58-96` `buildGroupPolicy` — filter the membership while `policy.Members` is assembled (`:93`), i.e. **before** `order()` runs. The function is pure policy (its own header comment `policy.go:15-18` pins the doctrine: *"it may suppress an ATTEMPT, never a PICK or a CHAIN"* — a scope exclusion is neither a suppression nor a mark, it is a **candidate-eligibility** decision, so it belongs here, upstream of `order()`).
2. **ALTERNATIVE (equivalent, later):** `pkg/decision/group.go:118-119` — filter `policy.Members` between `buildGroupPolicy` and `policy.order()`. This keeps `policy.go` token-free (policy.go imports **only** `math/rand` + `pkg/models` — `policy.go:20-24`; it does **not** import `pkg/auth`, and the file header explicitly forbids importing the suppression seam, `policy.go:12-13`). Injecting a token into `buildGroupPolicy` would require adding the `pkg/auth` import to the policy file.
3. **DO NOT** place the exclusion in the member loop (`group.go:142-244`) — that is skip-at-attempt-time, which the commission forbids.

**Empty-candidate-list semantics:** if the scope filter empties the candidate list, `group.go:120-123` currently fires `exhaustDecisionGroup` ⇒ **502 `decision_all_members_failed`** — a *fallback* failure masquerading as a permission failure. The commission's "permission beats resilience ⇒ clean 403" requires a **new, earlier arm** between the lane gate (`:101-114`) and the empty-order check (`:120-123`), e.g. `if len(candidates)==0 && token is restricted ⇒ 403 access_denied`. The same trap exists on the chat/anthropic lanes (see §5).

---

## 4. Error response shapes (exact JSON)

Envelope builders: `pkg/models/errors.go:51-59` (`NewOpenAIError`) and `:63-72` (`NewAnthropicError`); `ErrorDetails` has `omitempty` on `type` and `code` (`pkg/models/errors.go:43-47`).

| Lane | Status | Writer (file:line) | Exact body |
|---|---|---|---|
| **401** decision | 401 | `pkg/decision/handler.go:777-782` | `{"error":{"type":"authentication_error","message":"Invalid or expired API key"}}` |
| **401** chat | 401 | `pkg/proxy/handler.go:514-522` | identical to above |
| **401** anthropic | 401 | `pkg/proxy/handler.go:514-522` (reused; called `handler_anthropic.go:418`) | identical to above |
| **401** imggen | 401 | `pkg/imggen/handler.go:703-708` | identical to above |
| **403** decision | 403 | `pkg/decision/handler.go:784-794` | `{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<id>"}}` |
| **403** chat + anthropic | 403 | `pkg/proxy/handler.go:525-535` | identical shape (called `:854`, `:874`, `handler_anthropic.go:530`) |
| **403** imggen | 403 | `pkg/imggen/handler.go:713-723` | identical shape |
| **400** decision misroute | 400 | `pkg/decision/handler.go:334-336` → `openAIErrorWithCode` (`:770-775`) | `{"error":{"type":"server_error","code":"decision_misroute","message":"decision models are served by /v1/decisions only (got kind \"…\")"}}` |
| **400** generic decision | 400 | `pkg/decision/handler.go:760-765` `openAIError` | `{"error":{"type":"server_error","message":"<msg>"}}` (no `code` key) |
| **400** anthropic misroute | 400 | `pkg/proxy/handler_anthropic.go:301-305`, `:313-317` | `{"type":"error","error":{"type":"invalid_request_error","code":"decision_misroute","message":"decision models are served by /v1/decisions only"}}` (Anthropic shape has a root `"type":"error"`) |

**Note the 403 shape is NOT the OpenAI envelope** — it is a hand-rolled `map[string]string` with `type/message/model` and **no `code` key**. The commission's new 403 (group/chain scope denial) must pick one of the two existing conventions deliberately; the three writers are byte-identical to each other, so a 4th writer should copy `pkg/proxy/handler.go:525-535` verbatim.

### "Fallback exhausted / all members failed" today

**Decision group — 502 `decision_all_members_failed`** (`pkg/decision/group.go:345-358`):
```
:347  func (h *Handler) exhaustDecisionGroup(w http.ResponseWriter, groupID string, attempted, skipped, membersAttempted int, startTime time.Time) {
:348      log.Printf("[decision] group=%s exhausted: %d attempted, %d skipped, %d members attempted", …)
:349-354  h.publishEvent(eventTypeDecision, withDecisionAccounting({… "outcome":"all_members_failed", "model":groupID, "upstream_status":0 …}, attempted, skipped, membersAttempted))
:355-357  h.openAIErrorWithCode(w, http.StatusBadGateway,
                                 fmt.Sprintf("all decision group members failed (%d attempted)", attempted),
                                 decisionAllMembersFailedCode)
:358  }
```
- Status: **502**. Code const: `decisionAllMembersFailedCode = "decision_all_members_failed"` (`pkg/decision/group.go:57`).
- Body: `{"error":{"type":"server_error","code":"decision_all_members_failed","message":"all decision group members failed (<N> attempted)"}}`.
- **DRIFT REPORTED:** the brief cites "pkg/decision/group.go:347-372" for this envelope. At a3f0335 the envelope function is **`:345-358`** (doc comment `:345-346`, body `:347-358`); `:360-378` is `withDecisionAccounting` (the counter builder) and `:380-400` is `failFastDecisionGroup`. The **status (502) and the code string are CONFIRMED**; only the **line span is drifted** (`:347-372` → `:345-358`).
- The fail-fast variant (`group.go:380-400`) relays the **upstream** status/body verbatim (`:395-399`) — status defaults to 502 (`:383-386`).

**Chat lane — "all models failed"** (`pkg/proxy/handler.go:1158-1161` → `handleRaceFailure` `:1291-1340`): `h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "all_models_failed")` (`:1161`); the envelope is built from `coordinator.GetFinalErrorInfo()` (`:1318`) — **status/message/type/code are upstream-derived**, not a fixed 502 (e.g. `"All models rate limited"`, `pkg/proxy/race_coordinator.go:1532`, `:1654`). Sent via `h.sendError(w, errInfo.HTTPStatus, errInfo.Message, errInfo.ErrorType, errInfo.ErrorCode)` (`:1338`) ⇒ `{"error":{"type":…,"code":…,"message":…}}`; if headers were already sent (streaming) ⇒ SSE error instead (`:1335-1337`).

**Anthropic lane — "all models failed"** (`pkg/proxy/handler_anthropic.go:769-786`): if a stored upstream error exists ⇒ that status + translated body (`:775-779`); else `h.sendAnthropicError(w, "api_error", "All models failed after retries", http.StatusBadGateway)` (`:781`) ⇒ **502**, Anthropic envelope `{"type":"error","error":{"type":"api_error","message":"All models failed after retries"}}`; streaming ⇒ SSE error (`:783-786`).

---

## 5. Identity-vs-resolution semantics (what string is checked)

**The rule in code:** every scope check evaluates the **CLIENT-REQUESTED STRING**, never the resolved id.

| Site | Checked expression | Origin of the string | Resolved id used? |
|---|---|---|---|
| decision `:343` | `modelID` | `req.Model` — raw body (`handler.go:282`, parsed `:263`) | NO (resolution at `:305` is used only for the kind/lane gate) |
| imggen `:268` | `modelID` | client-requested body model | NO |
| chat `:839` | `modelID` = `rc.reqLog.Model` (`:835`) | `requestBody["model"]` (`handler_functions.go:108`) → `reqLog.Model` (`:132`) | NO — the resolved model is only used as a *gate* (`rc.resolvedModel != nil` at `:834`) and for logging (`:840`) |
| anthropic `:514` | `reqLog.Model` | `anthropicReq.Model` (`handler_anthropic.go:186`) | NO — `resolvedModel` used only in the log line (`:516`) |

**How a GROUP request differs: the entry string IS the group id.**
- Chat: the client posts `"model": "<group-id>"` ⇒ `rc.reqLog.Model == "<group-id>"` ⇒ the check at `:839` tests the **group id** against `allowed_models`. The members actually served are **never** checked. A token listing only the group id reaches every member of that group — **the same class of scope escape as the decision group arm**.
- Anthropic: identical — `reqLog.Model` is the group id; the check at `:514` tests the group id; the members (materialised by the group rewrite at `handler_anthropic.go:472`) are never checked.
- Decision: the group id is `modelID` (`handler.go:282`); the MODEL-arm check never runs for it (§2).
- **Consequence:** under the current code, `allowed_models = ["<group-id>"]` ⇒ *implicit* reach to every member of that group. Under the commission's EXPLICIT rule, the group must be listed in `allowed_groups` for its members to be reachable — i.e. this is a **behaviour change for existing tokens**, and it must be called out explicitly in the ADR.

### The chain/member surfaces the commission must also exclude (the other scope escapes)
Both text lanes build the candidate list **before** the allow-list runs, and the allow-list only ever inspects the **head** string:

- **Chat list construction:** `pkg/proxy/handler_functions.go:196-206` — `modelList = [resolvedModel.ID] + resolvedModel.FallbackChain` (`:201-202`). The chain entries are appended **here**, at construction, with no token involvement.
- **Anthropic list construction:** `pkg/proxy/handler_anthropic.go:326-336` — identical shape (`:331-332`).
- **Group rewrite (group → member + member chain):** `pkg/store/database/store_group_rewrite.go:55-113` `RewriteGroupEntries` — `out = append(out, memberRow.ID)` (`:101`) `out = append(out, memberRow.FallbackChain...)` (`:102`). Called via the shared helper `applyGroupRewrite` (`pkg/proxy/handler_groups.go:68-94`; chat call `pkg/proxy/handler.go:783`, Anthropic call `pkg/proxy/handler_anthropic.go:472`). **The chat and Anthropic lanes share this one helper (parity by construction).**
- **Chain advance (chat race):** `pkg/proxy/race_coordinator.go:1899-1912` `fallbackAdvanceTargetLocked` ⇒ returns `c.models[i+1]`; consumed by `spawn(modelTypeFallback, …)` at `race_coordinator.go:436-458` (`:454-458`).
- **Hedge spawn:** `pkg/proxy/race_coordinator.go:421-435` (`modelTypeSecond` ⇒ `modelID = c.models[0]` — a **credential-level** hedge on the same model, not a new model ⇒ **no new scope surface**).
- **Cred-failover spawn:** `race_coordinator.go:459-485` (same model, `triggerInfo.modelID`).
- **Re-entry (Anthropic):** `pkg/proxy/handler_anthropic.go:728-765` — the group re-entry rebuilds the member list; the tried-set harvest is at `:748-753` (`arc.modelList` harvested into `arc.groupTriedMembers`), then `h.tryGroupReentryAnthropic(arc)` (`:754`). The re-offered member comes from the group engine — **a re-entry can therefore re-offer a member that a scope filter excluded**, unless the exclusion is applied to the re-offer path too.

⇒ **The construction-time exclusion for the text lanes has exactly two candidate seams:** (a) the two `modelList` builders (`handler_functions.go:200-202`, `handler_anthropic.go:330-332`) — but they run **before** authentication (auth happens later: chat `handler.go:705-723`, anthropic `handler_anthropic.go:403-422`), so a token-scoped filter cannot live there without reordering; and (b) the **post-auth** rewrite site — chat `pkg/proxy/handler.go:783` and anthropic `pkg/proxy/handler_anthropic.go:472` — which run **after** auth and **before** every `modelList` consumer (the comments at `handler.go:772-782` and `handler_anthropic.go:463-472` say so explicitly). **(b) is the correct seam for both text lanes** (it is also where the group-member + chain entries are materialised, `store_group_rewrite.go:101-102`).

---

## 6. `pkg/decision/group.go` — zero allow-list checks (Q6)

**CONFIRMED by grep** (`AllowedModels|IsModelAllowed|Scope|scope|ermission|allow|Allow|token|Token` over `pkg/decision/group.go`, whole file, 723 lines):

- `pkg/decision/group.go:96` — `authToken *auth.AuthToken` **parameter** of `serveDecisionGroup`.
- `pkg/decision/group.go:296` — `h.commitDecisionWinner(w, groupID, winner, authToken)`.
- `pkg/decision/group.go:312` — `authToken *auth.AuthToken` **parameter** of `commitDecisionWinner`.
- `pkg/decision/group.go:341` — `h.meterOnSuccess(authToken, winner.memberID, winner.provider, winner.typed)` — the **only** consumer (metering: `pkg/decision/handler.go:670-694`, token id at `:689-690`).
- No `AllowedModels`, no `IsModelAllowed`, no scope/permission concept anywhere in the file.

⇒ **`authToken` is threaded into `serveDecisionGroup` ONLY for metering.** It is available at the top of the group arm (`group.go:96`), so the fix has the token in hand at the construction point (`group.go:118-119`) with **no plumbing change required**.

**Also confirmed (the same escape on the text lanes):** `pkg/decision/group.go` has zero scope checks, but so does the **group rewrite** `pkg/store/database/store_group_rewrite.go:55-113` — it has **no token parameter at all** (`RewriteGroupEntries(modelList []string, conversationKey string)`), and neither does the `models.GroupRewriter` interface (`pkg/models/group_rewrite.go:82-91`). A token-scoped filter at that seam therefore **requires extending the interface** (or filtering the list in `pkg/proxy` after the rewrite returns) — an interface change that touches `pkg/store/database` + `pkg/modelscache` (the decorator forward) + `pkg/proxy`.

---

## 7. Drift report (every place the cited line number differs from a3f0335)

| Cited | Actual at a3f0335 | Verdict |
|---|---|---|
| `pkg/decision/handler.go:317-326` (group arm) | `:317-326` | **EXACT — no drift** |
| `pkg/decision/handler.go:340-354` (Step-7 allow-list) | `:340-354` | **EXACT — no drift** |
| `pkg/decision/handler.go:343` (model-arm check) | `:343` | **EXACT — no drift** |
| `pkg/imggen/handler.go:268` | `:268` | **EXACT — no drift** |
| `pkg/proxy/handler.go:839` (chat check) | `:839` | **EXACT — no drift** |
| `pkg/proxy/handler_anthropic.go:514` | `:514` | **EXACT — no drift** |
| `pkg/auth/token.go:63-65` (malformed JSON ⇒ `[]`) | `:63-65` | **EXACT — no drift** |
| `pkg/auth/token.go:114-125` (`IsModelAllowed`) | `:114-125` | **EXACT — no drift** |
| `pkg/proxy/handler_functions.go:132` (`reqLog.Model` from body) | `:132` (source `:108`) | **EXACT — no drift** |
| `pkg/decision/group.go:347-372` (502 `decision_all_members_failed`) | **`:345-358`** (`exhaustDecisionGroup`); `:360-378` = `withDecisionAccounting`; `:380-400` = `failFastDecisionGroup` | **DRIFTED — span wrong; status (502) and code string CONFIRMED** |
| "anthropic checks after rewrite at `:472`" | `:472` is the `applyGroupRewrite` **call site**; the **check** is at `:514` | **PARTIAL — the cited line is the rewrite, not the check; the check is at `:514`** |

**Repository-wide drift hazard (IMPORTANT):** the main checkout `/home/nea/Code/opensource-projects/llm-supervisor-proxy` is a **stale tree** — e.g. its `pkg/auth/token.go` has `UltimateModelEnabled` + json tags and `IsModelAllowed` at **`:133`/`:135`** (not `:114`/`:116`), and `pkg/auth/store.go:23` still carries the `ultimateModelEnabled, ultimateModelID` parameters that the worktree's `pkg/auth/store.go:23` does **not** have. Any line number taken from the main checkout is **invalid for a3f0335**. All line numbers in this document were read inside the worktree.

---

## 8. Open questions / items the commission must decide (not answerable from code alone)

1. **Empty-candidate-list → 403 vs 502.** `group.go:120-123` currently maps an empty order to `exhaustDecisionGroup` (502). The commission wants a **clean 403** when the list is empty *because of scope* (not because the group is unservable). The two cases are indistinguishable at `:120` unless the filter records "scope-excluded" separately — the plan must specify how the two are told apart.
2. **Group-grant semantics for the TEXT lanes.** `allowed_groups` must expand to `members(group)` — but the text lane's group membership is per-host CONFIG (`models.members_json`) and the *chain* of each member is a further hop. Does granting a group cover the members' **chain targets**? The commission says "chains do NOT inherit reach from their parent member" ⇒ the chain targets of an allowed member must be listed explicitly. Confirm this reading before implementation.
3. **Interface change or proxy-side filter for the text lanes.** `models.GroupRewriter` (`pkg/models/group_rewrite.go:82-91`) has no token parameter; the group rewrite seam is shared by chat + Anthropic. Either extend the interface (touches `pkg/store/database` + `pkg/modelscache` decorator forward) or filter the returned list inside `pkg/proxy` (both call sites: `handler.go:783`, `handler_anthropic.go:472`).
4. **Migration for the new `allowed_groups` column** (TEXT, JSON array) + the `ScanAllowedGroups` fail-closed twin of `ScanAllowedModels` (`pkg/auth/token.go:40-68`), plus `pkg/auth/store.go` (create/update/list/get) and `pkg/ui/server.go` (`:2497`, `:2504`, `:2564`, `:2593-2599`, `:2634`, `:2682-2683`, `:2698`).
5. **The MCP lane has no scope check at all** (`pkg/mcp/auth.go:48-57`) — is it in the commission's scope? If not, record it as an explicit non-goal so the ADR does not silently imply coverage.
6. **Backward-compatibility blast radius.** Today `allowed_models = ["<group-id>"]` implies member reach on all three lanes. Under EXPLICIT reach that becomes a 403. Is there a migration/compat mode, or is it a clean break? (The FE admin surface `pkg/ui/server.go` would need to show group grants.)
