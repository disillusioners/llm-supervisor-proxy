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

	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
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
func (h *Handler) applyGroupRewrite(conf *ConfigSnapshot, modelList []string, conversationKey string) (newList []string, pick *groupPickInfo, failGroupID string) {
	if conf == nil || conf.ModelsConfig == nil {
		return modelList, nil, ""
	}
	gr, ok := conf.ModelsConfig.(models.GroupRewriter)
	if !ok {
		return modelList, nil, ""
	}
	res := gr.RewriteGroupEntries(modelList, conversationKey)
	if res.FailedGroupID != "" {
		return nil, nil, res.FailedGroupID
	}
	if !res.AnyGroup {
		return modelList, nil, ""
	}
	pool := res.MemberPool
	if pool == "" {
		pool = models.PoolDefault // back-compat: groups created before D5 rewrite ⇒ no pool tag ⇒ default.
	}
	return res.ModelList, &groupPickInfo{
		GroupID:      res.GroupID,
		MemberID:     res.MemberID,
		NewlyBound:   res.MemberNewlyBound,
		Pool:         pool,
		BindingStale: res.BindingStale,
	}, ""
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
// sequential loop. Same contract as tryGroupReentry (max ONE
// re-entry per request, pre-first-byte enforced by the
// headersSent guard at the loop top), but operating on the
// anthropicRequestContext. The rebuild is performed by the SAME
// shared helper (parity by construction).
func (h *Handler) tryGroupReentryAnthropic(arc *anthropicRequestContext) bool {
	gr, ok := arc.conf.ModelsConfig.(models.GroupRewriter)
	if !ok {
		return false
	}
	replacement, pool, avail := gr.ExcludeGroupMember(arc.groupPick.GroupID, arc.conversationKey, arc.groupPick.MemberID)
	if !avail || replacement == "" {
		return false
	}
	arc.groupReentered = true
	newList, pick, failGroup := h.applyGroupRewrite(&arc.conf, arc.modelListOriginal, arc.conversationKey)
	if failGroup != "" || pick == nil {
		log.Printf("[grouplb] anthropic request %s: re-entry rebuild failed for group %s — treating as real failure", arc.reqID, arc.groupPick.GroupID)
		return false
	}
	arc.modelList = newList
	arc.groupPick = pick
	if pick.MemberID != arc.reqLog.Model {
		arc.reqLog.RouteModel = pick.MemberID
	} else {
		arc.reqLog.RouteModel = ""
	}
	if pool == models.PoolBackup {
		log.Printf("[grouplb] anthropic request %s: terminal member failure — group %s pool=backup reselect member=%s → re-entering once",
			arc.reqID, pick.GroupID, pick.MemberID)
	} else {
		log.Printf("[grouplb] anthropic request %s: terminal member failure — group %s reselect member=%s → re-entering once",
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
// the backend supports it; otherwise claims-aware. Used by the
// per-attempt re-fetch sites in race_executor and the Anthropic
// sequential loop.
func directGetModel(cfg *ConfigSnapshot, modelID string) *models.ModelConfig {
	if cfg == nil || cfg.ModelsConfig == nil {
		return nil
	}
	if ng, ok := cfg.ModelsConfig.(directModelGetter); ok {
		return ng.GetModelNative(modelID)
	}
	return cfg.ModelsConfig.GetModel(modelID)
}
