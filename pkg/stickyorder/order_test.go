package stickyorder

import (
	"reflect"
	"testing"
)

// TestPromoteToFront_NoOps pins every no-op path: len<=1, empty target,
// absent target, target already at index 0. All of them return an
// equal-length copy.
func TestPromoteToFront_NoOps(t *testing.T) {
	cases := []struct {
		name   string
		list   []string
		target string
		want   []string
	}{
		{name: "nil list", list: nil, target: "a", want: []string{}},
		{name: "empty list", list: []string{}, target: "a", want: []string{}},
		{name: "single element match", list: []string{"a"}, target: "a", want: []string{"a"}},
		{name: "single element miss", list: []string{"a"}, target: "b", want: []string{"a"}},
		{name: "empty target", list: []string{"a", "b"}, target: "", want: []string{"a", "b"}},
		{name: "absent target", list: []string{"a", "b", "c"}, target: "z", want: []string{"a", "b", "c"}},
		{name: "already front", list: []string{"a", "b", "c"}, target: "a", want: []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before []string
			if tc.list != nil {
				before = append([]string(nil), tc.list...)
			}
			got := PromoteToFront(tc.list, tc.target)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("PromoteToFront(%v, %q) = %v, want %v", tc.list, tc.target, got, tc.want)
			}
			// Input never mutated.
			if before != nil && !reflect.DeepEqual(tc.list, before) {
				t.Fatalf("input mutated: %v -> %v", before, tc.list)
			}
			// Fresh copy on every path: distinct backing array, so
			// mutating the result cannot touch the input.
			if len(got) > 0 && len(tc.list) > 0 && &got[0] == &tc.list[0] {
				t.Fatalf("PromoteToFront returned the input slice (aliasing) for %s", tc.name)
			}
		})
	}
}

// TestPromoteToFront_MovesTarget pins the stable reorder: the target
// moves to index 0 and every other element keeps its relative order.
func TestPromoteToFront_MovesTarget(t *testing.T) {
	cases := []struct {
		name   string
		list   []string
		target string
		want   []string
	}{
		{
			name:   "middle element",
			list:   []string{"a", "b", "c", "d"},
			target: "c",
			want:   []string{"c", "a", "b", "d"},
		},
		{
			name:   "last element",
			list:   []string{"a", "b", "c"},
			target: "c",
			want:   []string{"c", "a", "b"},
		},
		{
			name:   "second element",
			list:   []string{"a", "b"},
			target: "b",
			want:   []string{"b", "a"},
		},
		{
			name:   "duplicates move first occurrence only",
			list:   []string{"a", "b", "a", "c"},
			target: "a",
			want:   []string{"a", "b", "a", "c"},
		},
		{
			name:   "duplicates middle first occurrence",
			list:   []string{"b", "a", "b", "a"},
			target: "a",
			want:   []string{"a", "b", "b", "a"},
		},
		{
			name:   "self-referencing chain [A,A]",
			list:   []string{"x", "a", "a"},
			target: "a",
			want:   []string{"a", "x", "a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.list...)
			got := PromoteToFront(tc.list, tc.target)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("PromoteToFront(%v, %q) = %v, want %v", tc.list, tc.target, got, tc.want)
			}
			// Membership + length invariants: a permutation of the input.
			if len(got) != len(tc.list) {
				t.Fatalf("length changed: %d -> %d", len(tc.list), len(got))
			}
			if !sameMultiset(got, tc.list) {
				t.Fatalf("membership changed: %v -> %v", tc.list, got)
			}
			// Input never mutated.
			if !reflect.DeepEqual(tc.list, before) {
				t.Fatalf("input mutated: %v -> %v", before, tc.list)
			}
		})
	}
}

// TestPromoteToFront_FreshCopyMutationIsolation pins the always-fresh-copy
// contract by mutating the result and checking the input.
func TestPromoteToFront_FreshCopyMutationIsolation(t *testing.T) {
	in := []string{"a", "b", "c"}
	got := PromoteToFront(in, "c")
	got[0] = "MUTATED"
	got[2] = "MUTATED"
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("mutating the result changed the input: %v", in)
	}
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}
