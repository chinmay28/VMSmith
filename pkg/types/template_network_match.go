package types

import "strings"

// TemplateMatchesNetwork reports whether the given template is attached to a
// network whose name equals the (already normalised, lowercase) query. The
// match is a case-insensitive exact-match (any-of) over the names of the
// template's additional network attachments (networks[].name) — the
// user-friendly labels an operator assigns ("data-net", "storage-net"). An
// empty query matches everything; callers should short-circuit before calling.
//
// Mirrors VMMatchesNetwork (5.4.36): the implicit primary NAT network is not
// represented in a template's networks list (it is the default every VM
// carries), so a `?network=` query only ever scopes to the explicitly-attached
// extra networks operators name and group by.
func TemplateMatchesNetwork(tpl *VMTemplate, network string) bool {
	if tpl == nil {
		return false
	}
	if network == "" {
		return true
	}
	for _, attachment := range tpl.Networks {
		if strings.EqualFold(attachment.Name, network) {
			return true
		}
	}
	return false
}
