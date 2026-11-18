package proxy

// load-balance-groups (plan §3.6.1 Am21 + §4 Am26) — the shared,
// protocol-neutral pieces of the group pipeline:
//
//   - groupPickInfo: the per-request record of the group rewrite.
//   - applyGroupRewrite: the ONE helper BOTH protocol sites call
//     after their conversation-key derivation (Am21 parity by
//     construction; the parity test locks the two call sites' use).
//   - patchTopLevelModelField + sseModelMasker: the Am26 masking
//     machinery (byte-preserving top-level "model" patch; buffered
//     per complete SSE event — never per raw chunk, Am6).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/stickyorder"
)

// groupPickInfo records the (main) group rewrite outcome for the
// request: metering stamps (Am25), group-level failure semantics
// (Am24), and the Q15 member event key on these fields. The `Pool`
// field (group-backup-members / D5) carries the pool the served
// member was drawn from — empty before the rewrite succeeded,
// "default" / "backup" after. The model_member_selected event
// payload picks it up additively. `BindingStale` (D8) is true when
// the selection-time repair discarded a sticky binding whose member
// had left the group and re-picked — surfaced as the additive
// "binding" event field.
type groupPickInfo struct {
	GroupID    string
	MemberID   string
	NewlyBound bool // grouplb W-1 signal for model_member_selected (Q15)
	Pool       string
	// BindingStale marks a D8 selection-time repair: the
	// conversation's sticky binding pointed at a member that left
	// the group, so the binding was discarded and the member
	// re-picked.
	BindingStale bool
}

// membersOf returns the CURRENT member ids of a group, read from the
// IN-MEMORY models snapshot (Ruling 5 — NEVER a live DB read).
//
// The accessor is the OPTIONAL models.GroupMembershipReader seam: the
// modelscache decorator implements it with a NON-COPYING read of its
// in-memory snapshot (one small allocation — the member-id slice).
// GetModels() would answer the same question but DEEP-COPIES the whole
// model set (every row plus its chain / credential-ref / member slices)
// on every call — per-request garbage on the hot path.
//
// A backend without the seam (the structural test fakes, the JSON
// twin) falls back to the deep-copying snapshot: correct, slower.
//
// FRESHNESS: the decorator's snapshot is rebuilt on every group write
// (the write-through mutators) and on every reconciler tick, so a
// membership change takes effect on the next request without re-minting
// the token (ADR D12).
func (h *Handler) membersOf(conf *ConfigSnapshot) func(string) []string {
	return func(groupID string) []string {
		if conf == nil || conf.ModelsConfig == nil {
			return nil
		}
		if r, ok := conf.ModelsConfig.(models.GroupMembershipReader); ok {
			return r.GroupMembers(groupID)
		}
		for _, row := range conf.ModelsConfig.GetModels() {
			if row.ID != groupID || !row.IsGroup() {
				continue
			}
			out := make([]string, 0, len(row.Members))
			for _, m := range row.Members {
				out = append(out, m.ModelID)
			}
			return out
		}
		return nil
	}
}

// excludeGroupMemberScoped is the shared offer-walk helper (the chat
// and Anthropic re-entry walks both use it).
//
// It uses the SCOPED exclude when the backend provides it (the
// production modelscache decorator does) and falls back to the legacy
// exclude otherwise.
//
// THE FALLBACK'S REACH CHECK: the legacy exclude cannot scope its
// offer, so an out-of-reach replacement is refused — and it is NOT
// excluded in turn, because the legacy API's only "advance" mechanism
// is a cooldown write, and cooling a member that was never attempted
// would create cross-request state from a permission decision (Ruling
// 1). The fallback therefore ends the walk (the caller keeps its
// terminal availability path) instead of advancing.
func (h *Handler) excludeGroupMemberScoped(conf *ConfigSnapshot, reach models.ReachFilter, groupID, conversationKey, memberID string, cooldown time.Duration) models.GroupExcludeOutcome {
	if conf == nil || conf.ModelsConfig == nil {
		return models.GroupExcludeOutcome{}
	}
	if grs, ok := conf.ModelsConfig.(models.GroupRewriterScoped); ok {
		return grs.ExcludeGroupMemberScoped(groupID, conversationKey, memberID, cooldown, reach)
	}
	gr, ok := conf.ModelsConfig.(models.GroupRewriter)
	if !ok {
		return models.GroupExcludeOutcome{}
	}
	replacement, pool, avail := gr.ExcludeGroupMember(groupID, conversationKey, memberID, cooldown)
	if !avail || replacement == "" {
		return models.GroupExcludeOutcome{}
	}
	if reach != nil && !reach.Allows(replacement) {
		return models.GroupExcludeOutcome{}
	}
	return models.GroupExcludeOutcome{Replacement: replacement, Pool: pool, OK: true}
}

// applyGroupRewrite is the ONE shared rewrite entry point (Am21).
// Both protocol sites call it AFTER their conversation-key
// derivation — the sticky pick needs the request's key (group-salted
// for a direct-named group; computed post-auth where tokenID is
// known), and the rewrite must precede every modelList consumer.
//
// Behavior:
//   - No GroupRewriter backend (JSON twin, test stubs): identity —
//     the list is returned unchanged (groups resolve but are not
//     rewritten; production main.go always binds the DB manager).
//   - FailedGroupID non-empty (Am28 exhausted member set): the caller
//     must surface the normal resolve-failure envelope — never a
//     panic, never a silent passthrough.
//   - pick non-nil: the list was rewritten; the caller stamps its
//     context (modelList swap + original preserved for Am24 rebuild)
//     and the served-member metering record (Am25).
//   - pick.Pool (group-backup-members / D5) is "default" when the
//     pick came from the configured default sub-pool and "backup"
//     when the defaults had all been excluded/failed; the Q15
//     event site reads it for the additive payload field.
//
// THE REACH PARAMETER: reach is the token's scope (ADR D6/D7). It is
// handed to the SCOPED rewrite so the member PICK itself is
// scope-aware (an out-of-reach member is never offered and never
// cooled). A nil reach (wildcard / anonymous) keeps today's behaviour
// byte-for-byte.
func (h *Handler) applyGroupRewrite(conf *ConfigSnapshot, modelList []string, conversationKey string, reach models.ReachFilter) rewriteOutcome {
	if conf == nil || conf.ModelsConfig == nil {
		return rewriteOutcome{list: modelList}
	}
	gr, ok := conf.ModelsConfig.(models.GroupRewriter)
	if !ok {
		return rewriteOutcome{list: modelList}
	}
	var res models.GroupRewriteResult
	if grs, scoped := conf.ModelsConfig.(models.GroupRewriterScoped); scoped {
		res = grs.RewriteGroupEntriesScoped(modelList, conversationKey, reach)
	} else {
		if reach != nil {
			// LOUD ONCE per request: the backend cannot scope its pick,
			// so the request-layer list filter (applyReachFilter) is the
			// fail-closed backstop. The pick may be sub-optimal, but no
			// out-of-scope candidate can reach an upstream attempt.
			log.Printf("[authz] models backend lacks the scoped rewrite — permission filtering falls back to the request-layer list filter")
		}
		res = gr.RewriteGroupEntries(modelList, conversationKey)
	}
	if res.FailedGroupID != "" {
		// The group had no servable member. scopeExcluded distinguishes
		// the two reasons: reach (403 access_denied) vs availability
		// (502 group_unservable) — ADR D14.
		return rewriteOutcome{list: nil, pick: nil, failGroupID: res.FailedGroupID, scopeExcluded: res.ScopeExcluded}
	}
	if !res.AnyGroup {
		return rewriteOutcome{list: modelList}
	}
	// PD-12 / arch PD-7: a rewrite that produced NO group pick must
	// NOT mint a phantom pick. No GroupID ⇒ no group context, and
	// every downstream group path is gated on `rc.groupPick != nil`.
	// (The dropped fallback-position group leaves AnyGroup true with
	// GroupID empty — minting a pick there would blank the route model
	// of a perfectly healthy request and emit group events with an
	// empty id.)
	if res.GroupID == "" {
		return rewriteOutcome{list: res.ModelList}
	}
	// T3 pick/list consistency: the stamped pick must be present in the
	// list that will be executed. A divergence is a fail-closed
	// condition — never serve a list whose stamped pick is absent.
	if res.MemberID != "" && !containsModelID(res.ModelList, res.MemberID) {
		log.Printf("[authz] PICK/LIST DIVERGENCE group=%s member=%s — the stamped pick is absent from the execution list", res.GroupID, res.MemberID)
		return rewriteOutcome{list: nil, pick: nil, failGroupID: res.GroupID, scopeExcluded: true}
	}
	pool := res.MemberPool
	if pool == "" {
		pool = models.PoolDefault // back-compat: groups created before D5 rewrite ⇒ no pool tag ⇒ default.
	}
	return rewriteOutcome{
		list: res.ModelList,
		pick: &groupPickInfo{
			GroupID:      res.GroupID,
			MemberID:     res.MemberID,
			NewlyBound:   res.MemberNewlyBound,
			Pool:         pool,
			BindingStale: res.BindingStale,
		},
	}
}

// rewriteOutcome is the result of the shared rewrite: the rewritten
// list, the (optional) group pick, the failed group id and the
// SCOPE-vs-AVAILABILITY discriminator.
//
// The struct exists instead of a 4-value return so the "pick must be
// nil whenever there is no group" rule (PD-12 / arch PD-7) and the
// scope-exclusion flag are both explicit at the call site.
type rewriteOutcome struct {
	// list is the rewritten execution list (nil when the rewrite
	// failed — the caller must fail clean).
	list []string
	// pick is the group pick, or nil when no group was picked.
	pick *groupPickInfo
	// failGroupID is non-empty when a group entry had no servable
	// member (the caller surfaces the failure envelope).
	failGroupID string
	// scopeExcluded is true when the failure was caused by the token's
	// REACH (every candidate was out of reach), not by availability.
	// The caller must then answer 403 access_denied — NEVER the 502
	// group_unservable envelope (ADR D14 / PD-14).
	scopeExcluded bool
}

// containsModelID reports whether id is in list.
func containsModelID(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// applyReachFilter removes every candidate outside the token's reach.
//
//	scope == nil  (wildcard / anonymous) => the list is returned
//	             UNTOUCHED — same slice identity, ZERO allocation.
//	scope != nil  => the out-of-scope entries are removed, the
//	             relative order is preserved, and a dupe is collapsed
//	             onto its FIRST occurrence (the coordinator's index
//	             math must stay stable).
//
// The removed ids are named ONCE per call in the proxy log (ids only —
// NEVER in an HTTP response body: a response body must not disclose
// the existence of out-of-scope models).
//
// The caller decides what an EMPTY result means: an empty list at the
// coordinator is a contract violation (the constructor refuses it), so
// the empty case must be answered with 403/502 BEFORE construction.
func (h *Handler) applyReachFilter(scope *authz.Scope, list []string) []string {
	if scope == nil || len(list) == 0 {
		return list
	}
	// Fast path: nothing to remove ⇒ the list passes through untouched
	// (same slice identity, no allocation).
	dropped := false
	for _, id := range list {
		if !scope.Allows(id) {
			dropped = true
			break
		}
	}
	if !dropped {
		return list
	}
	out := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	excluded := make([]string, 0, len(list))
	for _, id := range list {
		if !scope.Allows(id) {
			excluded = append(excluded, id)
			continue
		}
		if seen[id] {
			continue // keep the first occurrence, drop the rest
		}
		seen[id] = true
		out = append(out, id)
	}
	log.Printf("[authz] dropped %d out-of-scope candidate(s) from the execution list: %s", len(excluded), strings.Join(excluded, ","))
	return out
}

// stickyActive is the ONE feature gate shared by every sticky
// gate/record/invalidate site: the per-request snapshot must exist, the
// config flag must be ON, and the conversation key must be non-empty
// (the SAME gate the LB engines use — no leading user run ⇒ no
// stickiness). Sites add their OWN local conjuncts on top (deadlinePick,
// writeErr, servedModel, …); none of them re-derive these three.
//
// Nil-receiver-safe: a nil snapshot is inactive.
func (s *ConfigSnapshot) stickyActive(key string) bool {
	return s != nil && s.StickyLastSuccessEnabled && key != ""
}

// applyStickyOrder is the sticky last-success ORDER orchestrator — one
// call per choke point (design §3.3). Gates, in order:
//
//  0. a non-nil per-request snapshot (defensive; the handler always
//     builds one),
//  1. the per-request snapshot flag (config.StickyLastSuccessEnabled),
//  2. a non-empty conversation key (the SAME gate the LB engines use —
//     no leading user run ⇒ no stickiness),
//  3. a recorded last-success winner for this key.
//
// (0)–(2) are the shared ConfigSnapshot.stickyActive predicate. A nil
// engine needs no separate gate: every stickyorder method is
// nil-receiver-safe, so Get on a nil engine is the ordinary "no record"
// miss (legacy/test embeddings degrade to today's behaviour).
//
// It NEVER computes a conversation hash — the key arrives as an
// argument from the request context (the existing affinity key,
// verbatim). Every path returns a slice the caller may assign back: the
// no-op paths return the input as-is, and the promotion path returns a
// fresh copy (stickyorder.PromoteToFront never aliases or mutates its
// input).
//
// An EMPTY reqID suppresses the promotion log line: the OpenAI loop
// passes one on a group-re-entry replay when that request ALREADY logged
// its promotion, so the same request cannot emit two "promoted" lines.
// The gates themselves never depend on reqID.
func (h *Handler) applyStickyOrder(conf *ConfigSnapshot, list []string, conversationKey, reqID string) []string {
	if !conf.stickyActive(conversationKey) {
		return list
	}
	last, ok := h.stickyEngine.Get(conversationKey)
	if !ok || last == "" {
		return list
	}
	out := stickyorder.PromoteToFront(list, last)
	if reqID != "" && !slices.Equal(out, list) {
		// Moved (target present and not already front). Full short
		// lists: model IDs are safe to log (never payload content).
		// The key prefix is length-guarded (production keys are
		// 64-hex; short keys must never panic a log line).
		log.Printf("[STICKY] request %s: promoted last-success model=%s to index 0 key=%s from=%v to=%v",
			reqID, last, stickyorder.KeyPrefix(conversationKey), list, out)
	}
	return out
}

// ─── Am26 masking ────────────────────────────────────────────────────────────

// patchTopLevelModelField returns body with the top-level "model"
// string value replaced by target — BYTE-PRESERVING: only the value's
// bytes change; key order, whitespace, and every other byte are
// untouched (a full unmarshal→marshal round-trip would alphabetize
// map keys and break the §4 byte-shape transparency goal).
//
// Fail-open: when the body has no top-level "model" key, the value is
// not a JSON string, or anything unexpected is encountered, the body
// is returned UNCHANGED (the caller logs; the stream/response is
// never dropped).
func patchTopLevelModelField(body []byte, target string) []byte {
	pos, ok := locateTopLevelModelValue(body)
	if !ok {
		return body
	}
	// Validate that the located value is a JSON string (the scanner
	// already ensures this, but re-check cheaply for safety).
	if pos.valueStart >= len(body) || body[pos.valueStart] != '"' {
		return body
	}
	// Marshal the target through encoding/json for correct escaping.
	enc, err := json.Marshal(target)
	if err != nil {
		return body
	}
	out := make([]byte, 0, len(body)+len(enc))
	out = append(out, body[:pos.valueStart]...)
	out = append(out, enc...)
	out = append(out, body[pos.valueEnd:]...)
	return out
}

// topLevelModelPos marks the byte range [valueStart, valueEnd) of a
// located top-level "model" value.
type topLevelModelPos struct {
	valueStart int
	valueEnd   int
}

// locateTopLevelModelValue scans a JSON object for the top-level
// "model" KEY and returns the byte range of its VALUE. Only depth-1
// keys of the root object match (a nested {"usage": {..., "model":
// ...}} must NOT be patched). Returns ok=false on any surprise.
func locateTopLevelModelValue(body []byte) (topLevelModelPos, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	// Expect the root object.
	tok, err := dec.Token()
	if err != nil {
		return topLevelModelPos{}, false
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return topLevelModelPos{}, false
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return topLevelModelPos{}, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return topLevelModelPos{}, false
		}
		// Offset of the VALUE = decoder's current input offset minus
		// the bytes already buffered for the value... json.Decoder
		// does not expose value offsets directly, so we locate the
		// value via a second pass anchored on the key position: for
		// string values we walk the raw bytes from the key end.
		if key != "model" {
			// Skip the value entirely (any JSON type).
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return topLevelModelPos{}, false
			}
			continue
		}
		// Found the top-level "model" key. Locate its value in the
		// RAW bytes: find the key occurrence at the decoder's
		// current position. The decoder has consumed up to the key
		// token; scan the raw body for the next string value start.
		keyIdx := lastKeyIndex(body, dec)
		if keyIdx < 0 {
			return topLevelModelPos{}, false
		}
		return stringValueRange(body, keyIdx)
	}
	return topLevelModelPos{}, false
}

// lastKeyIndex returns a raw-byte index INSIDE body just past the
// "model" key the decoder most recently consumed. The decoder's
// InputOffset() points past the key token (before the ':').
func lastKeyIndex(body []byte, dec *json.Decoder) int {
	off := int(dec.InputOffset())
	if off < 0 || off > len(body) {
		return -1
	}
	return off
}

// stringValueRange, given an index just past a key token, walks
// forward over the ':' + whitespace and returns the byte range of the
// following JSON string VALUE.
func stringValueRange(body []byte, from int) (topLevelModelPos, bool) {
	i := from
	// Skip whitespace + the colon.
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	if i >= len(body) || body[i] != ':' {
		return topLevelModelPos{}, false
	}
	i++
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	if i >= len(body) || body[i] != '"' {
		return topLevelModelPos{}, false
	}
	start := i
	// Walk the string honoring escapes.
	i++
	for i < len(body) {
		switch body[i] {
		case '\\':
			i += 2
			continue
		case '"':
			return topLevelModelPos{valueStart: start, valueEnd: i + 1}, true
		}
		i++
	}
	return topLevelModelPos{}, false
}

// sseModelMasker buffers and patches SSE output PER COMPLETE EVENT
// (Am6/Am26): a JSON object can straddle raw read-buffer chunk
// boundaries, so a per-chunk patch would fail open on straddled
// events and leak the upstream model name intermittently. The masker
// accumulates raw chunks, emits only COMPLETE events (patched) plus
// verbatim non-data lines, and holds the incomplete tail until its
// terminator arrives. A nil masker is a byte-transparent no-op (the
// non-group gate — zero cost, zero copies for existing traffic).
type sseModelMasker struct {
	target string
	pend   []byte
}

// newSSEModelMasker returns a masker rewriting top-level "model"
// fields of complete `data: {...}` SSE events to target. nil target
// ⇒ nil masker (transparent).
func newSSEModelMasker(target string) *sseModelMasker {
	if target == "" {
		return nil
	}
	return &sseModelMasker{target: target}
}

// process consumes one raw chunk and returns the bytes safe to write
// now: every COMPLETE event in the chunk (patched) and any verbatim
// pass-through bytes. Incomplete trailing bytes are buffered.
func (m *sseModelMasker) process(chunk []byte) []byte {
	if m == nil {
		return chunk
	}
	m.pend = append(m.pend, chunk...)
	var out []byte
	for {
		// An SSE event terminates with a blank line ("\n\n" —
		// possibly "\r\n\r\n" from some upstreams).
		idx, sepLen := findEventBoundary(m.pend)
		if idx < 0 {
			break // incomplete — keep buffering
		}
		event := m.pend[:idx+sepLen]
		m.pend = m.pend[idx+sepLen:]
		out = append(out, m.maskEvent(event)...)
	}
	return out
}

// flush returns any buffered incomplete tail verbatim (fail-open:
// trailing bytes without a terminator — e.g. a truncated final event
// — are written unchanged, never dropped).
func (m *sseModelMasker) flush() []byte {
	if m == nil || len(m.pend) == 0 {
		return nil
	}
	out := m.pend
	m.pend = nil
	return out
}

// maskEvent patches one COMPLETE SSE event. Events whose data payload
// is a JSON object with a top-level "model" string get it rewritten;
// everything else ("data: [DONE]", comments, event fences, non-JSON)
// passes through byte-identical.
func (m *sseModelMasker) maskEvent(event []byte) []byte {
	// Find the "data:" line within the event (SSE events may carry
	// field lines before/after it).
	dataStart := -1
	for i := 0; i+len("data:") <= len(event); i++ {
		if (i == 0 || event[i-1] == '\n') && bytes.Equal(event[i:i+len("data:")], []byte("data:")) {
			dataStart = i + len("data:")
			break
		}
	}
	if dataStart < 0 {
		return event // no data line (comment/fence) — verbatim
	}
	payload := event[dataStart:]
	trimmed := bytes.TrimLeft(payload, " ")
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		return event // [DONE] / non-JSON payload — verbatim
	}
	// The payload runs to the event terminator; strip it for the
	// patcher, patch, then re-append the original spacing + terminator
	// bytes (byte-preserving outside the patched value).
	body := bytes.TrimRight(trimmed, "\r\n")
	patched := patchTopLevelModelField(body, m.target)
	if len(patched) == len(body) && bytes.Equal(patched, body) {
		return event // unchanged (no top-level model / fail-open)
	}
	lead := len(payload) - len(trimmed)                             // spaces after "data:"
	termLen := len(payload) - len(bytes.TrimRight(payload, "\r\n")) // terminator bytes
	out := make([]byte, 0, len(event)+len(patched)-len(body))
	out = append(out, event[:dataStart]...)
	for i := 0; i < lead; i++ {
		out = append(out, ' ')
	}
	out = append(out, patched...)
	out = append(out, payload[len(payload)-termLen:]...)
	return out
}

// findEventBoundary locates the next SSE event terminator in b,
// returning the index of the terminator's START and its length
// ("\n\n" = 2, "\n\r\n" = 3 — CRLF terminator). -1 when no complete
// terminator exists. The event occupies b[:idx+sepLen]; the next
// event starts at b[idx+sepLen:].
func findEventBoundary(b []byte) (int, int) {
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		// "\n\n" — terminator starts at i, length 2.
		if i+1 < len(b) && b[i+1] == '\n' {
			return i, 2
		}
		// "\n\r\n" — CRLF terminator starts at i, length 3.
		if i+2 < len(b) && b[i+1] == '\r' && b[i+2] == '\n' {
			return i, 3
		}
		// A trailing single "\n" at buffer end could still grow
		// into a terminator — keep buffering.
		if i+1 == len(b) {
			return -1, 0
		}
	}
	return -1, 0
}

// groupMaskTarget returns the Am26 mask target for a request: the
// CLIENT-REQUESTED string (group ID or claimed ID — whichever the
// client sent), only when the resolved row is a group (group-kind
// gate; non-group traffic is byte-unchanged).
func groupMaskTarget(rc *requestContext) string {
	if rc == nil || rc.resolvedModel == nil || !rc.resolvedModel.IsGroup() {
		return ""
	}
	return rc.reqLog.Model
}

// groupFailMessage builds the resolve-failure message for an
// unservable group (Am28 / §3.8 defense-in-depth).
func groupFailMessage(groupID string) string {
	return fmt.Sprintf("group '%s' has no servable members", groupID)
}

// tryGroupReentryAnthropic — Am24 mirror for the /v1/messages
// sequential loop. Same contract as tryGroupReentry, but operating on
// the anthropicRequestContext. The rebuild is performed by the SAME
// shared helper (parity by construction).
//
// wi-7769a812 part 2: the max-ONE re-entry latch (groupReentered) is
// RETIRED — the caller's gate bounds the member iteration to N
// attempts for an N-member group (payloadCommitted pre-commit +
// groupAdvances cap + the groupTriedMembers set here refusing an
// already-tried replacement — F3: a TRUE within-request exclusion),
// and the member exclusion window handed to the engine is the
// CONFIG-OWNED skip-cache TTL (one source of truth with the upstream
// skip marks — which, per part 3 ordering-not-filtering, only DEMOTE
// candidates and never exclude them). Pre-commit is enforced by
// the CALLER's !arc.payloadCommitted gate (the loop-top guard +
// re-entry gate); pre-first-byte is inherited from the same flag
// (headersSent is non-committing — the lazy preamble fires on
// framing-only progress).
func (h *Handler) tryGroupReentryAnthropic(arc *anthropicRequestContext) bool {
	if _, ok := arc.conf.ModelsConfig.(models.GroupRewriter); !ok {
		return false
	}
	if arc.groupTriedMembers == nil {
		arc.groupTriedMembers = make(map[string]bool)
	}
	// F3 (wi-7769a812 part 3) — bounded offer walk: each refused offer
	// already failed THIS request, so excluding it genuinely advances
	// the ordering sweep's head. Mirrors the OpenAI-lane iterate.
	//
	// The offer is SCOPED (ADR D6): the walk never offers an
	// out-of-reach member, and an out-of-reach member is never cooled.
	replacement, pool, avail := "", "", false
	excludeTarget := arc.groupPick.MemberID
	for iter := 0; iter <= models.MaxGroupMembers; iter++ {
		outcome := h.excludeGroupMemberScoped(&arc.conf, arc.scope, arc.groupPick.GroupID, arc.conversationKey, excludeTarget, arc.conf.UpstreamSkipCacheTTL)
		if !outcome.OK || outcome.Replacement == "" {
			if outcome.ScopeOnly {
				// EVERY remaining candidate was out of reach: a SCOPE
				// exhaustion, not an availability failure. The verdict
				// is attempt-count-keyed at the terminal site.
				arc.scopeExhausted = true
				log.Printf("[authz] anthropic request %s: group %s exhausted ON SCOPE (every remaining member is outside this token's reach)", arc.reqID, arc.groupPick.GroupID)
			}
			return false
		}
		replacement, pool, avail = outcome.Replacement, outcome.Pool, true
		if !arc.groupTriedMembers[replacement] {
			break
		}
		log.Printf("[grouplb] anthropic request %s: pool re-offered already-tried member %s — excluding and advancing the ordering scan", arc.reqID, replacement)
		excludeTarget = replacement
	}
	if !avail || replacement == "" || arc.groupTriedMembers[replacement] {
		log.Printf("[grouplb] anthropic request %s: offer walk exhausted without a fresh member — group fallback exhausted", arc.reqID)
		return false
	}
	outcome := h.applyGroupRewrite(&arc.conf, arc.modelListOriginal, arc.conversationKey, arc.scope)
	if outcome.failGroupID != "" || outcome.pick == nil {
		if outcome.scopeExcluded {
			// SCOPE exhaustion at the rebuild. The verdict is
			// attempt-count-keyed (leader ruling): ZERO upstream
			// attempts ⇒ 403 access_denied; >= 1 attempt ⇒ the terminal
			// availability path is unchanged.
			arc.scopeExhausted = true
			log.Printf("[authz] anthropic request %s: group %s rebuild exhausted ON SCOPE — no member of this group is inside the token's reach", arc.reqID, arc.groupPick.GroupID)
			return false
		}
		log.Printf("[grouplb] anthropic request %s: re-entry rebuild failed for group %s — treating as real failure", arc.reqID, arc.groupPick.GroupID)
		return false
	}
	pick := outcome.pick
	// F3 post-rebuild guard — the tried-mark lands HERE (after the
	// guard, on the member actually served) so offer/rebuild agreement
	// passes and only divergence onto an already-attempted member is
	// refused (mirror of the OpenAI-lane guard).
	if arc.groupTriedMembers[pick.MemberID] {
		log.Printf("[grouplb] anthropic request %s: rebuild re-served already-tried member %s — group fallback exhausted", arc.reqID, pick.MemberID)
		return false
	}
	arc.groupTriedMembers[pick.MemberID] = true
	arc.groupAdvances++
	arc.modelList = outcome.list
	arc.groupPick = pick
	// Sticky last-success race ORDER (design site (d) — MANDATORY): the
	// Anthropic re-entry restarts the inner walk WITHOUT re-passing
	// HandleAnthropicMessages' group-rewrite preamble, so that file's
	// walk-start site never re-runs. Apply the orchestrator to the
	// rebuilt list here, after the ExcludeGroupMember reselect (this
	// covers the backup-pool reselect variant identically — both
	// rebuild paths land on this line).
	// C3 — THE REACH FILTER: the rebuild re-materialises the member
	// list, so the filter runs again here (BEFORE the sticky ordering
	// — sticky promotion must never promote an entry the filter would
	// remove).
	arc.modelList = h.applyReachFilter(arc.scope, arc.modelList)
	arc.modelList = h.applyStickyOrder(&arc.conf, arc.modelList, arc.conversationKey, arc.reqID)
	if pick.MemberID != arc.reqLog.Model {
		arc.reqLog.RouteModel = pick.MemberID
	} else {
		arc.reqLog.RouteModel = ""
	}
	if pool == models.PoolBackup {
		log.Printf("[grouplb] anthropic request %s: terminal member failure — group %s pool=backup reselect member=%s → advancing to next member in pool order (exhaustive fallback)",
			arc.reqID, pick.GroupID, pick.MemberID)
	} else {
		log.Printf("[grouplb] anthropic request %s: terminal member failure — group %s reselect member=%s → advancing to next member in pool order (exhaustive fallback)",
			arc.reqID, pick.GroupID, pick.MemberID)
	}
	// load-balance-groups / P4.5 (P2 deferred #1) — publish the
	// EventMemberFailover event for the Anthropic path; the OpenAI
	// path emits the same event at handler.go:1221. Symmetry
	// closes the observability gap (FE EventLog + downstream
	// failover dashboards).
	if h.bus != nil {
		payload := map[string]interface{}{
			"id":          arc.reqID,
			"group_id":    pick.GroupID,
			"member_id":   pick.MemberID,
			"replacement": replacement,
		}
		// group-backup-members / D5: the additive `pool` field
		// marks a BACKUP-pool serve ("backup"); ABSENT for a
		// default-pool serve (M3 contract — a "default" value is
		// never emitted). Mirrors the OpenAI path's emission at
		// handler.go — the same defect (the newlyBound-gated
		// model_member_selected emission never fires on the
		// re-entry) applies to both protocols.
		if pool == models.PoolBackup {
			payload["pool"] = models.PoolBackup
		}
		h.publishEvent(grouplb.EventMemberFailover, payload)
	}
	return true
}

// directModelGetter is the load-balance-groups Am22 L1 seam:
// per-attempt re-fetches inside the race executor and the
// Anthropic sequential loop MUST resolve the row for req.modelID
// by DIRECT ID (claims-bypassed) — otherwise a member picked by
// the Am21 rewrite gets re-routed to its claiming group (claims
// would redirect the member ID back to the group row, and the
// per-attempt "is this internal?" check would fire the wrong
// code path). The narrow interface lets a backend that has
// GetModelNative opt in; backends without it fall back to
// claims-aware (the JSON twin — which has no grouplb engine
// anyway — degrades safely).
type directModelGetter interface {
	GetModelNative(modelID string) *models.ModelConfig
}

// directGetModel returns the row for modelID bypassing claims when
// the backend supports it; otherwise claims-aware.
//
// EXECUTION-resolution call sites — every read of an ATTEMPT's own
// model row must land on the MEMBER row, never on the group row that
// claims it:
//   - race_executor.go:48  — the MiniMax interleaved-thinking gate
//     (raceIntProviderIsMiniMax)
//   - race_executor.go:114 — the per-attempt re-fetch (executeRequest)
//   - race_executor.go:208 — the reselected-credential check
//     (resolveSpecificInternalCredential)
//   - race_executor.go:353 — the secondary-upstream read
//     (executeInternalRequest)
//   - race_coordinator.go:1547 — the credential-failover ELIGIBILITY
//     gate (credFailoverEligibleLocked)
//   - race_coordinator.go:1668 — the credential-failover BUDGET
//     (modelBudget)
//   - handler_anthropic.go:521 — the Anthropic sequential attempt loop
//     (attemptAnthropicModel)
//
// The CLIENT-facing resolution (the handler's model lookup) stays
// claims-first; only these execution reads bypass claims.
func directGetModel(cfg *ConfigSnapshot, modelID string) *models.ModelConfig {
	if cfg == nil || cfg.ModelsConfig == nil {
		return nil
	}
	if ng, ok := cfg.ModelsConfig.(directModelGetter); ok {
		return ng.GetModelNative(modelID)
	}
	return cfg.ModelsConfig.GetModel(modelID)
}
