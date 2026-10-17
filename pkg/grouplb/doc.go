// Package grouplb is the load-balance-groups sticky weighted picker
// over MEMBER MODEL IDs (plan §3.5, Round-2 membership pivot).
//
// It is a mechanical clone of pkg/credentiallb reduced to a sticky
// weighted picker over model IDs with ZERO credential or provider
// awareness:
//
//   - Membership is []MemberRef{ModelID, Weight, Position} — each
//     ModelID is a real pkg/models row ID (a chat-kind model). A
//     member's multi-credential fan-out is that member's OWN
//     credential LB (pkg/credentiallb), inherited natively; this
//     package knows nothing about credentials.
//   - Bindings and cooldowns are keyed by the MEMBER MODEL ID (a
//     429-class terminal failure on member A never cools member B
//     that happens to share a credential — plan R2, evaporated
//     concern).
//   - key.go is NOT forked: ComputeConversationKey is IMPORTED from
//     pkg/credentiallb (Am31). The caller passes
//     ComputeConversationKey(groupID, tokenID, firstUserMessage) —
//     the group ID salts the key, so the pick space is per-group
//     (two groups sharing a member keep distinct binding spaces, and
//     direct member requests use the member-salted space).
//
// Affinity is IN-MEMORY ONLY (USER-DECIDED, plan Q4): bindings are
// lost on restart (cold start — a single startup log line documents
// it; in-flight conversations re-roll to a fresh member on their next
// request). There is deliberately NO persistence machinery.
//
// Per-group state is LAZILY instantiated on first GetOrSelect (Am15):
// there is NO boot RebindFromStore walk. The owning ModelsManager
// seeds a group's engine state on first rewrite (and refreshes it on
// every group write via OnGroupChanged, which also filters bindings
// whose member was removed — the survivor filter).
//
// What carries verbatim from the credentiallb clone: prefix-sum
// weighted selector (O(log k) pick, weight<=0 ⇒ nil selector); E-1
// two-level locking with optimistic read + write-upgrade re-verify;
// E-3 n==1 fast path (day-1 single-member groups get zero engine
// overhead); 24h sliding idle TTL + 5-minute janitor + 60s default
// cooldown (USER-DECIDED, Q3); ExcludeAndReselect semantics
// (rebind-only-if-bound-to-failed, all-cooling ⇒ soonest-expiry
// fall-through); testhooks.go cloned 1:1 (all 7 seams, Am14) so race
// tests are deterministic and never sleep.
//
// Import purity (Am31 anti-pattern pin): this package must NEVER
// import credential/provider-aware packages (pkg/models credential
// types, pkg/providers, pkg/credentiallb beyond key.go helpers). A
// test guard greps the package source for violations.
package grouplb
