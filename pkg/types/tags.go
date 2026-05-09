package types

import (
	"sort"
	"strings"
)

// BulkTagAction enumerates the supported bulk tag mutations on a tag slice.
// Used by both the API handler (POST /vms/bulk_tag) and the CLI
// (`vmsmith vm tag <add|remove|set>`) so the merge semantics stay in lockstep.
type BulkTagAction string

const (
	BulkTagActionAdd    BulkTagAction = "add"
	BulkTagActionRemove BulkTagAction = "remove"
	BulkTagActionSet    BulkTagAction = "set"
)

// IsValid reports whether action is one of the supported BulkTagAction values.
func (a BulkTagAction) IsValid() bool {
	switch a {
	case BulkTagActionAdd, BulkTagActionRemove, BulkTagActionSet:
		return true
	}
	return false
}

// MergeTags returns the post-action tag list. `current` is the existing tag
// slice on the VM; `requested` is the already-normalized (lowercased) tag set
// from the request. Output is alphabetically sorted so persisted tags stay in
// a canonical order regardless of how they got mutated.
//
//   - BulkTagActionAdd: union of current ∪ requested (case-insensitive de-dup).
//   - BulkTagActionRemove: current minus any tag whose lowercase form matches
//     a requested tag.
//   - BulkTagActionSet: a fresh copy of requested.
func MergeTags(action BulkTagAction, current, requested []string) []string {
	switch action {
	case BulkTagActionSet:
		out := make([]string, len(requested))
		copy(out, requested)
		return out
	case BulkTagActionAdd:
		out := make([]string, 0, len(current)+len(requested))
		seen := make(map[string]struct{}, len(current)+len(requested))
		for _, t := range current {
			key := strings.ToLower(t)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, t)
		}
		for _, t := range requested {
			key := strings.ToLower(t)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, t)
		}
		sort.Strings(out)
		return out
	case BulkTagActionRemove:
		drop := make(map[string]struct{}, len(requested))
		for _, t := range requested {
			drop[strings.ToLower(t)] = struct{}{}
		}
		out := make([]string, 0, len(current))
		for _, t := range current {
			if _, ok := drop[strings.ToLower(t)]; ok {
				continue
			}
			out = append(out, t)
		}
		return out
	default:
		return append([]string(nil), current...)
	}
}

// TagsEqualSet reports whether a and b contain the same case-insensitive tag
// values regardless of order. Used by mutating tag flows to skip a redundant
// store write when the proposed change is observably a no-op.
func TagsEqualSet(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	setA := make(map[string]struct{}, len(a))
	for _, t := range a {
		setA[strings.ToLower(t)] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, t := range b {
		setB[strings.ToLower(t)] = struct{}{}
	}
	if len(setA) != len(setB) {
		return false
	}
	for k := range setA {
		if _, ok := setB[k]; !ok {
			return false
		}
	}
	return true
}
