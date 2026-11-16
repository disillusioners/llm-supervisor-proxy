package modelcache

import "time"

// Mark-window bounds — the SINGLE SOURCE of the model-benching window
// (tidier High-1: the classify→clamp→mark policy was previously
// restated in three unlinked sites — both protocol lanes' mark sites
// and config.Validate — and the D4 clamp was hand-mirrored to the two
// lanes separately). Endpoints are INCLUSIVE: a window of exactly
// MinMarkWindow or MaxMarkWindow is honored.
//
// Floor 1m: keeps a sub-minute window from flapping a model back in
// between two requests of a walled plan. Ceiling 24h: keeps a typo'd
// reset instant from benching a model for days.
const (
	MinMarkWindow = time.Minute
	MaxMarkWindow = 24 * time.Hour
)

// ApplyWall is the ONE owner of the model-wall mark policy: classify
// the message, then mark the model for the stated reset instant
// (clamped to [MinMarkWindow, MaxMarkWindow]) or, when no reset
// parses, for the FIXED window (fixedTTL <= 0 ⇒ DefaultTTL, then
// clamped). Both protocol lanes are thin callers; config.Validate
// reuses the exported bounds at Save time.
//
// Byte-equal to the two per-lane blocks it replaces (fix round #2
// extraction): the fixed-TTL fallback is applied BEFORE the clamp in
// both, and the reset window is clamped with the same inclusive
// bounds. Nil cache, empty key or empty msg: no-op (never marks).
func ApplyWall(c *Cache, key, msg string, fixedTTL time.Duration, now time.Time) {
	if c == nil || key == "" || msg == "" {
		return
	}
	attributable, until, hasReset := ModelWall(msg, now)
	if !attributable {
		return
	}
	if hasReset {
		window := until.Sub(now)
		if window < MinMarkWindow {
			window = MinMarkWindow
		}
		if window > MaxMarkWindow {
			window = MaxMarkWindow
		}
		c.MarkUntil(key, now.Add(window))
		return
	}
	if fixedTTL <= 0 {
		fixedTTL = DefaultTTL
	}
	if fixedTTL < MinMarkWindow {
		fixedTTL = MinMarkWindow
	}
	if fixedTTL > MaxMarkWindow {
		fixedTTL = MaxMarkWindow
	}
	c.Mark(key, fixedTTL)
}
