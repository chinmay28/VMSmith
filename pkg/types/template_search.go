package types

import "strings"

// TemplateMatchesSearch reports whether the given template matches the
// (already normalised, lowercase) search query. The match is a
// case-insensitive substring scan over the template's name, description, and
// tags — the fields an operator would type into a "find this template" box.
// An empty query matches everything; callers should short-circuit before
// calling.
//
// Matching against ID, image, default_user, and the network attachment
// fields is intentionally excluded. ID is an opaque `tmpl-<unix-nano>`
// string no operator types from memory; image / default_user / networks
// describe the template's effect, not the template itself, and including
// them produces noisy false positives (e.g. an `image=rocky9.qcow2`
// template matching every search containing "rocky"). Mirrors the contract
// of VMMatchesSearch (2.2.13), ImageMatchesSearch (5.4.9), and
// SnapshotMatchesSearch (5.4.10).
func TemplateMatchesSearch(tpl *VMTemplate, query string) bool {
	if tpl == nil {
		return false
	}
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(tpl.Name), query) {
		return true
	}
	if tpl.Description != "" && strings.Contains(strings.ToLower(tpl.Description), query) {
		return true
	}
	for _, tag := range tpl.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
