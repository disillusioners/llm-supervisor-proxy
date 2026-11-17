package decision

// The attempt-suppression seam of the decision lane — the ONLY place
// on this lane that reads a health layer.
//
// THE DOCTRINE (verbatim — pinned by the source-literal test):
//
//	The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it
//	may suppress an ATTEMPT, never a PICK or a CHAIN.
//
// Three layers, and they NEVER read each other's state:
//
//	GROUP   — policy facts only (pools, weights, order). The PICK
//	          reads this layer; it carries no failure state at all
//	          (see the policy file).
//	MODEL   — model-attributable failures. Read by the member loop's
//	          per-member attempt decision. Its CHAIN reader is
//	          ABSENT on this lane — the chain clause is vacuous for
//	          decision models (no equivalent concept on this lane;
//	          deliberately not built). RECORDED AS ABSENT, not faked.
//	UPSTREAM — credential failures. Read by the per-credential
//	          selection only.
//
// The layers are the process-wide instances (one answer about one
// credential); the fence is LAYER separation, not instance
// separation. The layer packages have no internal imports, so this
// package may read them — the policy file may NOT (the pick must
// stay policy-only).

import (
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
)

// suppressionSeam holds the decision lane's health-layer handles and
// implements the attempt-vs-skip decision, the limit-class marks and
// the win-clear.
//
// Every method is nil-safe: a lane built without the health layers
// never suppresses (and never panics) — the demotion path still runs.
type suppressionSeam struct {
	upstream *skipcache.Cache  // UPSTREAM layer (credential failures)
	model    *modelcache.Cache // MODEL layer (model-attributable failures)
	ttlFn    func() time.Duration
}

// defaultSuppressionTTL is the fallback mark window when the TTL
// seam is absent: the shared upstream layer's own default. A missing
// TTL must never disable the demotion path (fail SOFT, never panic,
// never skip the mark).
func (s *suppressionSeam) effectiveTTL() time.Duration {
	if s == nil || s.ttlFn == nil {
		return skipcache.DefaultTTL
	}
	ttl := s.ttlFn()
	if ttl <= 0 {
		return skipcache.DefaultTTL
	}
	return ttl
}

// credentialMarked reports whether the UPSTREAM layer holds a mark
// for this (credential, wire-model) or (provider, wire-model) pair —
// the credential-attributable health facts.
func (s *suppressionSeam) credentialMarked(credID, provider, wireModel string) bool {
	if s == nil || s.upstream == nil {
		return false
	}
	if credID != "" && s.upstream.Marked(skipcache.ScopeCredential, credID, wireModel) {
		return true
	}
	if provider != "" && s.upstream.Marked(skipcache.ScopeProvider, strings.ToLower(provider), wireModel) {
		return true
	}
	return false
}

// credentialScopeMarked reports whether the UPSTREAM layer holds a
// mark for this (credential, wire-model) pair — the CREDENTIAL-scope
// record of a limited/unhealthy credential.
//
// This is the reader the D-C precondition consults (ADR-15's D-C:
// "every credential cooling or marked"). The provider scope is
// deliberately NOT consulted there: the D-C state is about THIS
// member's own credentials.
func (s *suppressionSeam) credentialScopeMarked(credID, wireModel string) bool {
	if s == nil || s.upstream == nil {
		return false
	}
	if credID == "" || wireModel == "" {
		return false
	}
	return s.upstream.Marked(skipcache.ScopeCredential, credID, wireModel)
}

// modelMarked reports whether the MODEL layer holds a mark for this
// model row — the model-attributable health fact.
//
// The MODEL layer's CHAIN reader is ABSENT on this lane (the chain
// clause is vacuous for decision models): this method only answers
// "is this MODEL limited", never "should this model walk its chain".
func (s *suppressionSeam) modelMarked(modelID string) bool {
	if s == nil || s.model == nil {
		return false
	}
	return s.model.Marked(modelID)
}

// markLimit writes the limit-class marks for a failed attempt: the
// UPSTREAM layer (credential + provider scopes) and the MODEL layer.
//
// ONLY limit-class failures write a mark. A 5xx, an upstream 401/403
// and the client-attributable 4xx classes write NOTHING (the health
// layers must never become a failure oracle).
func (s *suppressionSeam) markLimit(modelID, credID, provider, wireModel string) {
	if s == nil {
		return
	}
	ttl := s.effectiveTTL()
	if s.upstream != nil {
		if credID != "" {
			s.upstream.Mark(skipcache.ScopeCredential, credID, wireModel, ttl)
		}
		if provider != "" {
			s.upstream.Mark(skipcache.ScopeProvider, strings.ToLower(provider), wireModel, ttl)
		}
	}
	if s.model != nil && modelID != "" {
		s.model.Mark(modelID, ttl)
	}
}

// clearWinner clears the WINNER's marks — the winner's identity only
// (member id + credential id + provider + wire model). The failed
// head's marks SURVIVE: a member that failed this request stays
// demoted.
func (s *suppressionSeam) clearWinner(modelID, credID, provider, wireModel string) {
	if s == nil {
		return
	}
	if s.upstream != nil {
		if credID != "" {
			s.upstream.Clear(skipcache.ScopeCredential, credID, wireModel)
		}
		if provider != "" {
			s.upstream.Clear(skipcache.ScopeProvider, strings.ToLower(provider), wireModel)
		}
	}
	if s.model != nil && modelID != "" {
		s.model.Clear(modelID)
	}
}

// SetUpstreamSkipCache wires the process-wide UPSTREAM layer into the
// decision lane. Nil-safe.
func (h *Handler) SetUpstreamSkipCache(cache *skipcache.Cache) {
	if h == nil {
		return
	}
	h.suppression.upstream = cache
}

// SetModelSkipCache wires the process-wide MODEL layer into the
// decision lane. Nil-safe.
func (h *Handler) SetModelSkipCache(cache *modelcache.Cache) {
	if h == nil {
		return
	}
	h.suppression.model = cache
}

// SetSkipCacheTTL injects the config-owned mark window. Nil-safe: an
// absent seam falls back to the shared layer's default window and
// the demotion path still runs.
func (h *Handler) SetSkipCacheTTL(fn func() time.Duration) {
	if h == nil {
		return
	}
	if fn == nil {
		return
	}
	h.suppression.ttlFn = fn
}
