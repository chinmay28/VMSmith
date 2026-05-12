package types

import "strings"

// ImageMatchesSearch reports whether the given image matches the (already
// normalised, lowercase) search query. The match is a case-insensitive
// substring scan over the image's name, description, and tags — the fields an
// operator would type into a "find this image" box. An empty query matches
// everything; callers should short-circuit before calling.
//
// Matching against ID is intentionally excluded — IDs are opaque
// `img-<unix-nano>` strings that no operator types from memory; including
// them produces noisy false positives when the substring happens to look
// numeric. Mirrors the contract of VMMatchesSearch (2.2.13).
func ImageMatchesSearch(img *Image, query string) bool {
	if img == nil {
		return false
	}
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(img.Name), query) {
		return true
	}
	if img.Description != "" && strings.Contains(strings.ToLower(img.Description), query) {
		return true
	}
	for _, tag := range img.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
