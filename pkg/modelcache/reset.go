package modelcache

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reset-time parsing (wi-4d2c8e17 — OPTIONAL item, assessed not
// guessed). Two real message shapes observed in the operator's live
// logs:
//
//	absolute — "Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33"
//	relative — "Unavailable (reset after 62h 45m 6s)"
//
// Both name a reset INSTANT. When one can be parsed reliably the
// model-layer mark uses "until the stated reset" (clamped by the
// caller to a sane ceiling, e.g. 24h). When parsing fails the caller
// falls back to DefaultTTL.
//
// TIMEZONE: the absolute shape carries no zone. Upstreams emit UTC for
// these reset banners; we parse as UTC. That is a documented assumption
// (not a correctness claim for a non-UTC upstream). Relative shapes are
// zone-free by construction (now + duration).

// absoluteReset matches "reset at 2026-11-16 18:13:33" / "resets on
// 2026-11-16T18:13:33".
var absoluteReset = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:at|on)\s+(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2}:\d{2})`)

// relativeReset matches "reset after 62h 45m 6s", "reset in 5 hours",
// "reset after 2 days 3 hours". Each unit group is optional; at least
// one must be present for a match to be meaningful (guarded below).
var relativeReset = regexp.MustCompile(`(?i)reset(?:s)?\s+(?:in|after)\s+(?:(\d+)\s*d(?:ays?)?)?\s*(?:(\d+)\s*h(?:ours?|rs?)?)?\s*(?:(\d+)\s*m(?:in(?:utes?)?)?)?\s*(?:(\d+)\s*s(?:ec(?:onds?)?)?)?`)

// ParseReset extracts a future reset instant from msg. ok=false when no
// parseable instant exists or the parsed instant is not in the future.
func ParseReset(msg string, now time.Time) (time.Time, bool) {
	if m := absoluteReset.FindStringSubmatch(msg); m != nil {
		// "2006-01-02 15:04:05" — UTC by documented assumption.
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1]+" "+m[2], time.UTC); err == nil {
			if t.After(now) {
				return t, true
			}
		}
		return time.Time{}, false
	}
	if m := relativeReset.FindStringSubmatch(msg); m != nil {
		var d, h, min, sec int
		if m[1] != "" {
			d, _ = strconv.Atoi(m[1])
		}
		if m[2] != "" {
			h, _ = strconv.Atoi(m[2])
		}
		if m[3] != "" {
			min, _ = strconv.Atoi(m[3])
		}
		if m[4] != "" {
			sec, _ = strconv.Atoi(m[4])
		}
		if d == 0 && h == 0 && min == 0 && sec == 0 {
			return time.Time{}, false // matched the skeleton, no numbers
		}
		delta := time.Duration(d)*24*time.Hour +
			time.Duration(h)*time.Hour +
			time.Duration(min)*time.Minute +
			time.Duration(sec)*time.Second
		return now.Add(delta), true
	}
	return time.Time{}, false
}

// wallMarkers identify a MODEL/plan usage wall (not a per-minute
// throttle, and NOT a bare account-quota reset). Bounded, model/plan-
// scoped phrases only: a bare "…will reset after 4h39m9s" quota body is
// ACCOUNT-wide (it must stay credential-scoped via the 429/402 upstream
// classification) and must never mint a model mark (D3). Free-text noise
// must not flip a model off either.
var wallMarkers = []string{
	"usage limit",       // "Usage limit reached for 5 hour"
	"token plan",        // "Token Plan usage limit"
	"daily limit",       // "daily limit reached"
	"session limit",     // "session limit reached"
	"plan limit",        // "plan limit reached"
	"weekly limit",      // "weekly limit reached"
	"week limit",        // "week limit reached"
	"limit reached for", // "limit reached for 5 hour"
	"5 hour",            // "for 5 hour" (the observed 5-hour plan wall)
	"five hour",         // "for five hours"
}

// throttleMarkers identify a short-horizon rate limit that must stay
// upstream-scoped (a per-minute throttle is NOT a model wall).
var throttleMarkers = []string{
	"for this minute",
	"per minute",
	"requests per",
	"tokens per",
	" for this hour",
	"per hour",
}

// ModelWall classifies a message as a model-attributable usage wall.
// attributable reports the classification; when the message also names
// a reset instant, (until, hasReset) carry it (future-only). Callers
// apply DefaultTTL when attributable && !hasReset.
func ModelWall(msg string, now time.Time) (attributable bool, until time.Time, hasReset bool) {
	if msg == "" {
		return false, time.Time{}, false
	}
	lower := strings.ToLower(msg)
	for _, t := range throttleMarkers {
		if strings.Contains(lower, t) {
			return false, time.Time{}, false
		}
	}
	matched := false
	for _, w := range wallMarkers {
		if strings.Contains(lower, w) {
			matched = true
			break
		}
	}
	if !matched {
		return false, time.Time{}, false
	}
	if t, ok := ParseReset(msg, now); ok {
		return true, t, true
	}
	return true, time.Time{}, false
}
