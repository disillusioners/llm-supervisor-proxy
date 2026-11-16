# Credential Groups — SEAM RESEARCH (read-only, worktree wt-cred-groups @ 2b987bf)

Commission: management-only bundling of same-provider credentials referenced by models.
At model-resolution time the model's credential list is FLATTENED (groups expand in place,
dedup keeps first occurrence). Credential selection (rotation/cooldowns/skip-cache/weights/
sticky) must NEVER know groups exist.

---

## (a) THE SEAM — exact location and why

### The invariant that decides the seam
`credentiallb.Engine.GetOrSelect` (pkg/credentiallb/engine.go:225-348) consumes ONLY
engine-internal state: `st.credentials` (a `[]models.CredentialRef` snapshot) and
`st.bindings`. It never reads a ModelConfig. That state is populated EXCLUSIVELY by
`Engine.OnModelChanged(modelID, refs []models.CredentialRef)` (engine.go:366) and
`Engine.RebindFromStore(modelID, refs)` (engine.go:402). Therefore the engine's entire
view of a model's credential list is whatever those callers hand it — flatten at the
hand-off and the engine is group-blind by construction.

### The five engine-binding sites (all in pkg/store/database/store.go)
| Site | Line | Code |
|---|---|---|
| Startup rebind | store.go:789 | `m.engine.RebindFromStore(mc.ID, mc.Credentials)` inside `NewModelsManager` walk over `GetModels()` |
| Bus-drain refresh | store.go:867 (nil case :864) | `m.engine.OnModelChanged(modelID, mc.Credentials)` in `drainCredentialsChanged` |
| AddModel | store.go:1632 | `m.engine.OnModelChanged(model.ID, model.Credentials)` (+ `publishCredentialsChanged` :1633) |
| UpdateModel | store.go:1768 | `m.engine.OnModelChanged(model.ID, model.Credentials)` (+ publish :1769) |
| RemoveModel | store.go:1834 | `m.engine.OnModelChanged(modelID, nil)` — no change needed (nil is group-free) |

### The two resolver variants (len-branch reads of modelConfig.Credentials)
- `ModelsManager.ResolveInternalConfigWithAffinity` — store.go:2543. Branch reads:
  `len(modelConfig.Credentials) == 0` :2567 (legacy degrade), `== 1` :2588 (E-3 fast path,
  NO engine call), `>= 2` :2607 (`m.engine.GetOrSelect(modelID, conversationKey)`).
  Model fetched by ROW IDENTITY `GetModelNative` :2558 (claims bypass — P0 shadowfix).
- `ModelsManager.ResolveInternalConfigWithAffinityCached` — store.go:2679. Same branching
  against the CACHED row: `== 0` :2720, `== 1` :2729 (fast path :2730 returns
  `Credentials[0].CredentialID`), `>= 2` :2737 GetOrSelect. This is the PRODUCTION hot
  path: `CachedModelsConfig.ResolveInternalConfigWithAffinity` (pkg/modelscache/models.go:680)
  passes the cached row + a credLookup closure to `c.inner.ResolveInternalConfigWithAffinityCached`
  at models.go:737. `CACHE_LAYER_ENABLED` defaults ON (cmd/main.go:170).

### THE seam, stated once
ONE helper in pkg/store/database (e.g. `flattenedCredentialRefs(model) []models.CredentialRef`
— expands `credential_groups` refs in place via a group→member-credential-IDs lookup, dedup
keep-first), applied at:
1. the five binding sites above (pass flattened refs instead of `model.Credentials`),
2. the top of `ResolveInternalConfigWithAffinity` (store.go:2543) — branch + fast-path reads
   use the flattened slice,
3. the top of `ResolveInternalConfigWithAffinityCached` (store.go:2679) — same.
That is ~8 call sites, all in ONE file (store.go), all inside ModelsManager. Nothing in
pkg/proxy, pkg/credentiallb, pkg/stickyorder, pkg/skipcache, pkg/grouplb, or pkg/modelscache
changes for resolution.

### Why there and not elsewhere
- **Not in pkg/proxy**: the proxy never sees `[]CredentialRef`. Every runtime consumer
  (race_executor.go:491/:516, handler_anthropic.go:783, internal_handler.go:224, imggen via
  cmd/main.go wiring, decision via cmd/main.go wiring) receives ONE `models.ResolvedCredential`
  (pkg/models/config.go:488-495: Provider/APIKey/BaseURL/InternalModel/CredentialID/NewlyBound).
  There is nothing downstream to flatten.
- **Not at scan/read time** (scanModels store.go:932/941, GetModel:1028, GetModels:1223):
  the FE round-trips raw refs — GET /fe/api/models → PUT persists whatever the GET returned
  (ui/server.go:1249 AddModel / :1703 UpdateModel take the posted row verbatim). Flattening
  reads would silently REWRITE models' group references to expanded member lists on the next
  FE save, destroying the grouping. It would also put flat rows into the decorator cache
  (mutators cache the caller's copy: models.go:812/:845 `deepCopyModelConfig`).
- **Not in the decorator**: CachedModelsConfig caches raw rows and delegates resolution to
  `ResolveInternalConfigWithAffinityCached` (models.go:737) — flattening inside the store's
  two resolver variants covers production with zero decorator resolution changes. Only NEW
  manager methods (group CRUD etc.) need decorator forwards — see (c).
- The **E-3 trap** that forces the seam to precede the len-branches: a model whose raw list
  is a SINGLE group ref (expanding to N credentials) must not take the `== 1` fast path
  (store.go:2588/:2729) — that path resolves `Credentials[0].CredentialID` as a credential
  row and would look up the group's ID in the credentials table → miss → ok=false → 502.
  Flattening must happen before the branch.

---

## (b) COMPLETE entry-point list for credential resolution

All production resolution converges on the two ModelsManager resolver variants → ONE seam
covers them. Inventory:

1. **OpenAI chat race, primary path** — pkg/proxy/race_executor.go:516
   `cfg.ModelsConfig.ResolveInternalConfigWithAffinity(req.modelID, req.conversationKey)`.
2. **OpenAI chat, secondary-upstream hedge** — pkg/proxy/race_executor.go:491 (same call;
   inherits the primary credential, overrides InternalModel).
3. **OpenAI chat, credFailover reselect** — pkg/proxy/race_executor.go:477
   `resolveSpecificInternalCredential(cfg, req.modelID, req.reselectedCredentialID)` —
   resolves a SPECIFIC credential ID produced by `engine.ExcludeAndReselectForClass`
   (race_coordinator.go:1928). ID-based, already group-blind; no hook needed.
4. **Anthropic /v1/messages passthrough-vs-internal dispatch** — pkg/proxy/handler_anthropic.go:783
   `arc.conf.ModelsConfig.ResolveInternalConfigWithAffinity(modelConfig.ID, arc.conversationKey)`;
   skip-cache preference reorder at :788 (`preferUnmarkedUpstreamCredential`).
5. **InternalHandler (Anthropic internal branch + group member pipeline)** —
   pkg/proxy/internal_handler.go:224 `h.resolver.ResolveInternalConfigWithAffinity(h.config.ID,
   conversationKey)`; rate-limit/quota hook calls `h.credEngine.ExcludeAndReselectForClass`
   internal_handler.go:268. `h.resolver` is ModelsConfigInterface; production wires the
   decorator (or raw manager when cache layer off) — handler_anthropic.go:1086
   `internalHandler.SetCredentialFailover(h.credEngine, h.bus)`.
6. **Provider detection per attempt** — pkg/proxy/normalizers/config.go:27
   `cfg.ResolveInternalConfig(modelID)` (legacy 5-tuple; decorator serves it cache-side at
   modelscache/models.go:750; store-side single-cred fast path store.go:2444).
7. **Image generation** — pkg/imggen/handler.go:753-803 `SetCredentialResolver` /
   `NewCredentialResolverFromConfig`; cmd/main.go:238-251 wires it to the SAME
   `ResolveInternalConfigWithAffinity` seam ("single source of truth", handler.go:728-729
   comment; stable key per BE-A3/Am18).
8. **Decision models** — pkg/decision/handler.go:299 `h.safeResolveCredential(modelID)`;
   cmd/main.go wires to `ResolveInternalConfigWithAffinity(modelID, modelID)` (comment at
   decision/handler.go:295-298).
9. **UI Test-Model button (chat)** — pkg/ui/server.go:2053-2078 `handleTestModel`:
   `cred := s.modelsConfig.GetCredential(req.CredentialID)` — the FE posts an explicit
   credential ID (ModelForm.tsx:391 "Test Connection targets the primary credential only").
   NOT a resolution seam, BUT: if the FE's displayed credential list is the RAW refs, a
   group ref can be posted here → GetCredential(groupID) → 404. Flattened FE display or a
   validation gate required.
10. **UI Test buttons (decision/imggen)** — pkg/ui/handlers_decision.go:188-193 and
    pkg/ui/handlers_imggen.go:287-292: BOTH read `model.Credentials[0].CredentialID` directly
    ("FIRST credential, exact-ID lookup" convention). A group ref at position 0 → 404
    "Credential not found". Decision/imggen models are strictly single-credential semantics
    today — recommend validation FORBIDS group refs on non-chat kinds, which makes 9/10 moot
    for those kinds.
11. **ALTERNATE LANE — JSON-file twin** — pkg/models/config.go:1189-1241
    `ModelsConfig.ResolveInternalConfig` (in-memory `mc.Credentials.GetCredential`) and
    :1253 `ResolveInternalConfigWithAffinity` (no engine; degrades to single-credential).
    Legacy/test backend only (production is `database.InitializeAll` → ModelsManager). If
    groups must behave identically under the JSON twin, it needs its own flatten; otherwise
    document it as DB-only.

Verdict: **ONE seam covers all runtime paths (1-8)** because they all funnel into the two
resolver variants + the five binding sites. Items 9-10 are FE-display/validation concerns,
not resolution hooks. Item 11 is a separate legacy implementation.

---

## (c) DECORATOR-FORWARD ENUMERATION (pkg/modelscache)

### Interfaces today
- `models.ModelsConfigInterface` — pkg/models/config.go:446-477, 18 methods:
  GetModels, GetEnabledModels, GetModel, GetModelByName, GetTruncateParams, GetFallbackChain,
  AddModel, UpdateModel, RemoveModel, Save, Validate, GetCredential, GetCredentials,
  AddCredential, UpdateCredential, RemoveCredential, ResolveInternalConfig,
  ResolveInternalConfigWithAffinity.
- `modelscache.strictSource` — pkg/modelscache/strict.go:19-34: the 18 + GetModelStrict,
  GetModelByNameStrict, GetCredentialStrict, GetModelsStrict, GetEnabledModelsStrict,
  GetCredentialsStrict, ResolveInternalConfigWithAffinityCached. Compile-time assertion lives
  in outage_test.go (note at strict.go:16-18).
- Decorator methods (pkg/modelscache/models.go): reads :453/:506/:533/:542/:549/:563/:580/:620;
  fills :656 (fillModelByDirectID), snapshot :179/:199, reconciler :317/:347 (60s);
  resolution :680/:750; mutators AddModel :804, UpdateModel :838, RemoveModel :880,
  AddCredential :911, UpdateCredential :939, RemoveCredential :972; passthroughs Save :983,
  Validate :986; group-lane forwards RewriteGroupEntries :1043, ExcludeGroupMember :1056,
  ClearGroupMemberCooldown :1079, GetModelNative :1139; health/stop :277/:294.
- CachedTokenStore (tokens.go) — irrelevant to credential groups (auth tokens, not provider
  credentials).

### THE KNOWN TRAP (verbatim project knowledge)
A new method added to the concrete `*database.ModelsManager` but NOT to
`ModelsConfigInterface`/`strictSource` is invisible behind the decorator: production binds
`modelsConfig = wrappedModels` (cmd/main.go:187) when CACHE_LAYER_ENABLED=ON (default), and
an interface assertion at a call site would silently no-op or panic-free degrade — while
raw-manager tests (which construct ModelsManager directly) pass green.

### Methods credential-groups would ADD — each needs (i) interface addition,
(ii) decorator forward, (iii) decorator-through test, (iv) cache strategy:
1. `AddCredentialGroup` / `UpdateCredentialGroup` / `RemoveCredentialGroup` — CRUD.
   Decorator forward mirrors AddModel's shape (models.go:804-826): delegate to inner, then
   update decorator-side group state if cached. NOTE: do NOT inherit AddModel's
   `model.Internal = true` stamp (models.go:808/:841) — groups are not model rows.
2. `GetCredentialGroup(id)` / `GetCredentialGroups()` (+ `*Strict` twins if FE needs
   error-propagation parity with GetCredentialStrict strict.go:25).
3. **"Used by N models" reverse lookup** — e.g. `ModelsUsingCredentialGroup(groupID)
   []string`. Precedent for the pattern: RemoveCredential's in-use scan walks ALL models'
   refs (store.go:2384-2392). Needed for FE display + delete-guard.
4. **credential→group-membership lookup** — e.g. `GroupsContainingCredential(credID)`.
   Needed to EXTEND RemoveCredential's in-use guard: today it scans raw model refs only
   (store.go:2384-2392); a credential referenced only THROUGH a group would pass the guard
   and be deleted, leaving a dangling group member. The guard must also consult group
   memberships (store-layer, where the tables live).
5. Optional membership mutators (or fold into UpdateCredentialGroup).
6. Optional FE-preview accessor `FlattenedCredentialRefs(modelID)` (display-only).

### Cache invalidation mechanics the group writes must ride
- Decorator mutators swap cache synchronously (AddModel :804-826; UpdateModel :838-871
  handles renames + shadow-claim eviction `evictClaimEntriesLocked` :864-866;
  RemoveModel :880+). Boot/reconcile snapshots via `strictSnapshot` :179 + `reconcileOnce` :347.
- **Engine-side staleness (the subtle one)**: group membership edits change the FLATTENED
  list of every model referencing the group, but the engine-binding sites only fire on MODEL
  writes. A group edit MUST fan-out `engine.OnModelChanged(modelID, flattenedRefs)` to every
  referencing model (the reverse lookup from #3 is exactly the fan-out list), or rotation/
  cooldown state runs on a stale list until the next model write / restart. Precedent for
  write-time fan-out: `rebuildClaimsLocked` on every model write (store.go:901-906) and the
  decorator's claim eviction on group writes (models.go:864-866). Fan-out belongs in the
  ModelsManager group-write methods (store layer — the engine is not visible to the decorator).

### Decorator-through test pattern
fakeStrictSource implements the full interface in pkg/modelscache/models_test.go:236/:327/:334
(`ResolveInternalConfigWithAffinityCached` :236, `ResolveInternalConfig` :327,
`ResolveInternalConfigWithAffinity` :334) — new forwards get a fake method + an assertion that
the decorator actually called through (the models_test.go:1179 pattern asserts exactly one
delegation). Also ui/handlers_models_write_stamp_test.go:6 references the decorator's
write-stamp assertions — new mutators should carry equivalent pins.

---

## (d) AST-GUARD PRECEDENT (for a "credential-selection never sees CredentialGroup" pin)

Three in-repo mechanisms, strongest last:

1. **Raw-text regex guard** — pkg/decision/handler_test.go:40-63
   (`TestPackage_ImportGuard_NoProxyImports`) and :65-88 (`NoImggenImports`): compile a
   regex `github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")` and scan every .go
   file in the package dir. Weakness (project POK): raw text — a quoted path inside a
   COMMENT false-fails.
2. **Makefile denylist layers** — Makefile:51-67: `make test` runs three `grep -rnE` gates
   before `go test ./...`: pkg/imggen ↛ pkg/proxy (:53-57), pkg/decision ↛ pkg/proxy
   (:58-62), pkg/decision ↛ pkg/imggen (:63-67). The "2 layers" structure (Makefile + Go
   test) is the project convention (POK: decision-import-guard-denylist).
3. **go/parser import-block guard (comment-aware)** — pkg/proxy/cache_layer_separation_test.go:722-738
   `parsedImportPaths` (parser.ParseFile + parser.ImportsOnly + strconv.Unquote) and
   :752-784 `TestCacheLayer_Structural_PackageLevelNoCacheImports`: walks ENTIRE package dirs
   (`../store/database`, `../grouplb`, `../modelscache` — :757), skips _test.go (:766), and
   fails on any non-test file importing `pkg/skipcache` / `pkg/modelcache` (:753-756).
   Comment-awareness is itself pinned (:786+ `TestCacheLayer_Structural_ImportParserIsCommentAware`).
   **This is the direct template** for "pkg/credentiallb + pkg/stickyorder + pkg/skipcache
   must not import the package hosting CredentialGroup types": swap the forbidden-import map
   and the walked dir list.
4. **Full AST semantic walk** — pkg/proxy/translator/drift_trap_test.go:81-155:
   parser.ParseFile(AllErrors) + ast.Inspect over `*ast.CompositeLit` with type resolution
   (typeIsTranslatorReasoningDetail :155, ast.SelectorExpr/Ident matching). Template for a
   deeper pin ("no CredentialGroup-typed value constructed/flown inside selection code")
   if import-level is deemed too weak.
5. Also relevant: pkg/imggen's guard (imggen/handler.go:733-734: "The package owns no
   ModelsConfigInterface import — the production wiring lives in cmd/main.go (the import
   guard at task 1.8.2 audits this)") — the pattern of keeping a consumer package
   dependency-free by wiring at the composition root.

Placement note: if CredentialGroup types live in pkg/models (alongside CredentialRef) a
pure import-guard cannot work (everyone imports pkg/models); the guard then needs either a
SEPARATE package for group types (e.g. pkg/credentialgroups — imported only by
pkg/store/database + pkg/ui + pkg/models-free selection packages stay clean) or the
drift_trap-style AST walk. The cache_layer_separation guard dodged this by putting the
forbidden cache packages in their own packages — the same trick applies.

---

## (e) SURPRISES / LEAK SURFACES / TRAPS

1. **E-3 fast-path misfire** (already argued in (a)): raw single group ref → `== 1` branch
   resolves the GROUP's id against the credentials table → ok=false. This is the bug the
   seam placement prevents; a model with `[{group: "g"}]` must resolve to N credentials.
2. **Flatten-at-read destroys FE round-trip**: GetModel/GetModels/scanModels flattening
   would be persisted back by PUT /fe/api/models (server.go:1703 takes the posted row), and
   the decorator caches read results (mutators + snapshots). Raw-in reads, flatten-in
   resolve/bind is the only safe split.
3. **Engine staleness on group edits**: engine state updates ONLY via the 5 binding sites
   (all model-write-driven). Group membership changes need an explicit fan-out
   (`OnModelChanged` per referencing model) or selection runs stale until restart.
4. **RemoveCredential in-use guard hole**: scans raw model refs only (store.go:2384-2392);
   credentials reachable only via a group escape the guard → dangling member. Must extend
   to group memberships (store layer).
5. **Write-time validation today is group-hostile by default**: every raw ref must exist in
   the credentials table (store.go:1945-1947) — a group ref fails immediately. The validator
   (store.go:1930-1977: empty-id :1942, existence :1945, weight>0 :1948, dup :1951,
   16-cap :1956, provider-match :1960-1977) needs a group-ref branch, and the same-provider
   invariant should move to (or be duplicated at) the GROUP level (management-only bundling
   of same-provider credentials). UI-side twins: server.go:1017/:1511/:1863/:2018
   (`validateCredentials`) + FE — split-brain discipline precedent pkg/models/group.go:1-16
   (field rules via shared ValidateKindRules seam config.go:214 + collection-aware exported
   validators + parity tests kind_rules_parity_test.go).
6. **FE test-button leak**: decision/imggen handlers read `Credentials[0]` raw
   (handlers_decision.go:193, handlers_imggen.go:292); chat button posts an FE-chosen id
   (server.go:2078). Group ref at index 0 → 404. Mitigate via validation gate (groups are
   chat-kind-only refs) and/or flattened FE display.
7. **Deep-copy paths will silently drop a new field**: `deepCopyModelConfig`
   (pkg/modelscache — used in AddModel :812 / UpdateModel :845) and `BuildModelCopy`
   (pkg/models/copy.go:102-130 — credential refs copied verbatim :129-130, group rows not
   copyable :112-114) both enumerate fields explicitly. A new `credential_groups` field must
   be added to both or copy/clone semantics silently lose it.
8. **Cap semantics**: `MaxCredentialRefs = 16` (config.go:84) is enforced on RAW refs
   (store.go:1956). A flattened expansion can exceed 16; the engine has no cap but weights
   must be >0 (selector newWeightedSelector selector.go:28; validation :1948). Plan must
   decide: validate the EXPANDED length ≤ 16 at write time (bounds engine state + selector)
   — recommended — or raise the cap for flattened views only.
9. **Weight/position semantics on expansion**: `CredentialRef{CredentialID, Weight, Position}`
   (config.go:331-335). Groups carry member weights (GroupMember{ModelID, Weight, BackupOnly}
   group.go:42-46 is the MODEL-group precedent; credential-group members need their own
   weight shape). Expansion must synthesize valid weights (>0) and a deterministic order
   (dedup keep-first is order-sensitive — expansion order must be stable: refs in listed
   order, group members in their stored order).
10. **JSON twin lane is a second implementation**: config.go:1189/:1253 — no engine,
    single-credential only. Production never uses it (DB-only since remove-external-upstream);
    if the plan requires parity there, it's a second flatten site; otherwise document as
    DB-only feature.
11. **Group-blindness is already structural downstream**: stickyorder keys by conversation →
    model ID only (blueprint; engine.go contract); skipcache marks (credential, provider,
    model) identity post-resolution (race_executor.go:527-528 reorder; handler_anthropic.go:788);
    `preferUnmarkedUpstreamCredential` and the win-clear operate on ResolvedCredential. Two
    models sharing a group credential already share marks — existing semantics, unchanged.
12. **POK flag re config.db plaintext keys** (context, not seam): stored credentials are
    plaintext when ENCRYPTION_KEY unset — credential-group work touches no new secret
    surface (groups hold refs, not keys).
13. **Position field is write-ordered metadata** (config.go:334) — DB stores refs as a JSON
    column (`credentials_json` per comments at store.go:2613/:2736 and copy.go:5-9); the
    credential-groups table/column follows the same no-join-table, JSON-inline convention
    (copy.go:5-9 documents the models table shape; credentials live in a separate `credentials`
    table — store.go:2395-2398 DELETE). Group storage decision: sibling JSON column vs
    dedicated table — either way the flatten lookup must be in-memory cheap (the resolver
    variant is ZERO-DB by contract, store.go:2669-2671 — the group→members map must be
    cached in the manager or supplied via the decorator's credLookup-style closure).
14. **Claims shadow-index interplay**: GetModel is claims-first (store.go:1028) but the
    resolver path uses GetModelNative (store.go:2558) — flattening must use the row passed
    to it (Native in the raw variant, cached row in the Cached variant), never re-resolve
    through claims, or the self-shadow group shape (coding-group[coding], P0 2026-10-19)
    resurfaces.

---

## Appendix — key file:line index
- ModelConfig struct: pkg/models/config.go:338-415; Credentials field :357; CredentialRef :331-335;
  MaxCredentialRefs :84; PrimaryCredentialID :437-442; KindGroup :132; Members :403; ShadowModelID :414
- CredentialConfig: pkg/models/credential.go:84-89; interface :92-98; validProviders :15-60
- GroupMember + validation precedent: pkg/models/group.go:42-46, validateGroupRules :60-94,
  ValidateGroupMembers :96+
- Copy semantics: pkg/models/copy.go:102-130
- ModelsManager: struct ~store.go:690-728; NewModelsManager :765-814; Engine() :736;
  GroupEngine() :743; GetModel :1028 / GetModelNative :1050; GetModels :1223; AddModel :1549;
  UpdateModel :1690; RemoveModel :1788; Validate :1848; validateModelAgainstCredentials :1930-1977;
  validateBeforeWrite :2080; validateGroupWrite :2103; credential CRUD :2113/:2156/:2204/:2258/
  :2302/:2332/:2380 (in-use scan :2384-2392; OnCredentialDeleted :2416); publishCredentialsChanged :2425;
  ResolveInternalConfig :2444; resolveWithCredential :2480; ResolvedCredential alias :2517;
  ResolveInternalConfigWithAffinity :2543; ResolveInternalConfigWithAffinityCached :2679
- credentiallb.Engine: GetOrSelect :225-348; OnModelChanged :366; RebindFromStore :402;
  applyRefsLocked :424; OnCredentialDeleted :435; ExcludeAndReselectForClass :657; selector selector.go:28
- modelscache: strictSource strict.go:19-34; decorator reads/fills models.go:453-656; resolver :680/:737/:750;
  mutators :804/:838/:880/:911/:939/:972; group forwards :1043/:1056/:1079/:1139; reconciler :317/:347
- proxy call sites: race_executor.go:477/:491/:516/:527-528; handler_anthropic.go:783/:788/:877;
  internal_handler.go:224/:268; handler.go:214/:248/:925; normalizers/config.go:27
- UI: server.go:1017/:1511/:1863/:2018 validateCredentials mirrors; :2053-2078 handleTestModel;
  handlers_decision.go:188-193; handlers_imggen.go:287-292
- Guards: decision/handler_test.go:40-88; Makefile:51-67; cache_layer_separation_test.go:722-789;
  translator/drift_trap_test.go:81-155
- Wiring: cmd/main.go:138 (credLB), :145 (stickyEngine), :170 (CACHE_LAYER_ENABLED default ON),
  :176-190 (decorator wrap), :238-251 (imggen resolvers), :242 (skipcache, NOT manager-wired)
