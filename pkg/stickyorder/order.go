package stickyorder

// PromoteToFront returns a NEW slice with the FIRST occurrence of target
// at index 0; every other element keeps its original relative order.
// The input is never mutated and membership/length are never changed.
//
// A fresh copy is returned on EVERY path — including the no-op paths
// (len(list) <= 1, empty target, target absent, target already at index
// 0). The race coordinator ALIASES its model list
// (raceCoordinator.models) and the request contexts alias their
// pre-rewrite arrays (modelListOriginal), so a uniform fresh-result
// contract removes all aliasing reasoning from the call sites. The cost
// is one small allocation per gated request — noise next to the race
// itself.
//
// Duplicate handling: only the FIRST occurrence moves; later duplicates
// keep their relative position. Duplicates are constructible (a
// self-referencing fallback chain [A, A], or the same id as a direct
// entry and a group-member chain), so this follows "move the id", not
// "make index 0 unique".
func PromoteToFront(list []string, target string) []string {
	out := make([]string, len(list))
	copy(out, list)

	if len(out) <= 1 || target == "" || out[0] == target {
		return out
	}
	for i := 1; i < len(out); i++ {
		if out[i] == target {
			// Shift [0, i) right by one, then place target at 0.
			// copy() has memmove semantics, so the overlapping ranges
			// are safe (forward copy, destination starts one later).
			copy(out[1:i+1], out[:i])
			out[0] = target
			return out
		}
	}
	return out
}
