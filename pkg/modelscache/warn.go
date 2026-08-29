package modelscache

import (
	"log"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// deadDefaultUpstreamURL is the development-default upstream literal
// (planner ruling D — literal match only; smarter detection is out of
// scope for MVP). Emitting a loud boot-time WARN when the global
// UpstreamURL is still this default while enabled models exist is
// incident recommendation 3's tripwire; removing the dead default
// itself is a separate workstream (Q7). The global URL travels in
// Options.UpstreamURL (models do not carry an upstream URL — the
// misroute target is the proxy-level external passthrough upstream).
const deadDefaultUpstreamURL = "http://localhost:4001"

// warnDeadDefaultUpstream emits one loud WARN per enabled model when
// the configured global upstream is still the development default
// (planner ruling E — boot-only, never per-read: the WARN is for
// operators deploying, not for runtime consumers at hundreds of
// requests per second). Idempotent by construction: invoked exactly
// once, from NewCachedModelsConfig. An empty upstreamURL skips the check (the
// wiring did not supply one — e.g. tests).
func warnDeadDefaultUpstream(upstreamURL string, enabled []models.ModelConfig) {
	if upstreamURL == "" || upstreamURL != deadDefaultUpstreamURL || len(enabled) == 0 {
		return
	}
	for i := range enabled {
		log.Printf("[WARN] UpstreamURL is the development default %s for model %s — please configure before production use", deadDefaultUpstreamURL, enabled[i].ID)
	}
}
