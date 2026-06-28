package types

import (
	"sort"
	"strings"
)

// Template list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering.
const (
	TemplateSortID          = "id"
	TemplateSortName        = "name"
	TemplateSortCreatedAt   = "created_at"
	TemplateSortCPUs        = "cpus"
	TemplateSortRAMMB       = "ram_mb"
	TemplateSortDiskGB      = "disk_gb"
	TemplateSortImage       = "image"
	TemplateSortDefaultUser = "default_user"
	TemplateSortOSType      = "os_type"
	TemplateSortOSVariant   = "os_variant"
	TemplateSortDescription = "description"
)

// IsValidTemplateSort reports whether s is an accepted template list sort
// field. Used by the API and CLI parsers to reject unknown values uniformly.
func IsValidTemplateSort(s string) bool {
	switch s {
	case TemplateSortID, TemplateSortName, TemplateSortCreatedAt,
		TemplateSortCPUs, TemplateSortRAMMB, TemplateSortDiskGB,
		TemplateSortImage, TemplateSortDefaultUser, TemplateSortOSType,
		TemplateSortOSVariant, TemplateSortDescription:
		return true
	}
	return false
}

// SortTemplates sorts the given templates in place by the requested field
// and order. All comparators tiebreak on `id` so paginated requests return
// the same set across backends — `storage.Manager.ListTemplates` iterates
// bbolt key order (which is by ID) but tests may seed templates with
// equal-timestamp inputs that would otherwise shuffle.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see internal/api.parseTemplateSort).
func SortTemplates(templates []*VMTemplate, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(templates, func(i, j int) bool {
		ai, aj := templates[i], templates[j]
		var less bool
		switch sortField {
		case TemplateSortName:
			ni, nj := strings.ToLower(ai.Name), strings.ToLower(aj.Name)
			if ni != nj {
				less = ni < nj
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortCPUs:
			if ai.CPUs != aj.CPUs {
				less = ai.CPUs < aj.CPUs
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortRAMMB:
			if ai.RAMMB != aj.RAMMB {
				less = ai.RAMMB < aj.RAMMB
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortDiskGB:
			if ai.DiskGB != aj.DiskGB {
				less = ai.DiskGB < aj.DiskGB
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortDefaultUser:
			// Case-insensitive compare mirrors the case-insensitive
			// `?default_user=` exact-match filter contract on the
			// template list so the filter and sort agree on the same
			// column. Diverges from the VM list `default_user` axis
			// (5.4.91) which collapses empty → "root": templates store
			// an empty `default_user` as "use the image's built-in
			// user" (e.g. cloud-init's `cloud-user`/`ec2-user`/
			// `ubuntu`), NOT root. So an empty stored value here means
			// "deferred to the image" and sinks to the tail of asc /
			// head of desc, mirroring the nil-trailing semantics on
			// every other nullable sort axis (ip, guest_ip,
			// last_fired_at, last_delivery_at, actor, template `image`,
			// VM `image`).
			aiU, ajU := strings.ToLower(ai.DefaultUser), strings.ToLower(aj.DefaultUser)
			switch {
			case aiU == "" && ajU == "":
				less = ai.ID < aj.ID
			case aiU == "":
				less = false
			case ajU == "":
				less = true
			case aiU != ajU:
				less = aiU < ajU
			default:
				less = ai.ID < aj.ID
			}
		case TemplateSortOSType:
			// Case-insensitive compare on the template's *effective* OS
			// family via VMTemplate.ResolvedOSType (5.4.102). Symmetric
			// sort counterpart to the case-insensitive `?os_type=`
			// exact-match filter on the same column so the same OS-family
			// cohort can be both filtered and sorted on the same column.
			// Diverges from the nil-trailing convention on `image` /
			// `default_user` because this column has a documented default:
			// an empty stored `OSType` resolves to `linux` via
			// VMTemplate.ResolvedOSType (mirrors VMSpec.ResolvedOSType and
			// the `?os_type=linux` empty-means-linux filter contract) so
			// empty templates collate with explicit-linux templates rather
			// than sinking to the tail. The closed-and-total classification
			// guarantees every template resolves to exactly one of `linux`
			// < `windows`, mirroring the VM list `os_type` axis (5.4.100).
			aiOS := strings.ToLower(string(ai.ResolvedOSType()))
			ajOS := strings.ToLower(string(aj.ResolvedOSType()))
			if aiOS != ajOS {
				less = aiOS < ajOS
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortOSVariant:
			// 5.4.115 — case-insensitive compare on the template's
			// `OSVariant` field. Symmetric sort counterpart to the
			// case-insensitive `?os_variant=` exact-match filter on the
			// same column so the same Windows-edition cohort can be both
			// filtered and sorted on the same column — mirrors the VM list
			// `os_variant` sort axis (5.4.103). Unlike `os_type` (5.4.102)
			// `os_variant` has NO documented default — an empty stored
			// value means "operator did not specify an edition", typically
			// because the template provisions a Linux guest (where the
			// field is genuinely absent / not applicable). So empty
			// templates sink to the tail of asc / head of desc, mirroring
			// the nil-trailing semantics on `image` / `default_user` and
			// the VM list `os_variant` axis. Alphabetical Windows edition
			// ordering: windows-10 < windows-11 < windows-server-2019 <
			// windows-server-2022 < windows-server-2025.
			aiV, ajV := strings.ToLower(strings.TrimSpace(ai.OSVariant)), strings.ToLower(strings.TrimSpace(aj.OSVariant))
			switch {
			case aiV == "" && ajV == "":
				less = ai.ID < aj.ID
			case aiV == "":
				less = false
			case ajV == "":
				less = true
			case aiV != ajV:
				less = aiV < ajV
			default:
				less = ai.ID < aj.ID
			}
		case TemplateSortDescription:
			// 5.4.119 — case-insensitive compare on the template's
			// `Description` field. Symmetric sort counterpart to the
			// case-insensitive haystack used by the existing `?search=`
			// filter so the description-based query surface is filtered
			// (substring) and sorted (alphabetical) on the same semantics
			// — mirrors the image list `description` axis (5.4.118) one
			// resource over. Templates with an empty `Description` (the
			// common case — most templates get no description) sink to the
			// tail of asc / head of desc, mirroring the nil-trailing
			// semantics on every other nullable string axis (`image` /
			// `default_user` / `os_variant` and the image axes
			// `source_vm` / `description`) rather than collapsing to a
			// default like the documented-default axes (`os_type` →
			// `linux`, VM `firmware` → `bios`) — there is no documented
			// default for description because the field is genuinely
			// "operator did not bother to write one".
			aiD, ajD := strings.ToLower(ai.Description), strings.ToLower(aj.Description)
			switch {
			case aiD == "" && ajD == "":
				less = ai.ID < aj.ID
			case aiD == "":
				less = false
			case ajD == "":
				less = true
			case aiD != ajD:
				less = aiD < ajD
			default:
				less = ai.ID < aj.ID
			}
		case TemplateSortImage:
			// Case-insensitive compare mirrors the case-insensitive
			// `?image=` exact-match filter contract so the filter and sort
			// agree on the same column. Templates with an empty `Image`
			// sink to the tail of asc / head of desc — mirrors the
			// nil-trailing semantics on every other nullable sort axis
			// (ip, guest_ip, last_fired_at, last_delivery_at, actor) and
			// the VM list `image` axis (5.4.88).
			aiImg, ajImg := strings.ToLower(ai.Image), strings.ToLower(aj.Image)
			switch {
			case aiImg == "" && ajImg == "":
				less = ai.ID < aj.ID
			case aiImg == "":
				less = false
			case ajImg == "":
				less = true
			case aiImg != ajImg:
				less = aiImg < ajImg
			default:
				less = ai.ID < aj.ID
			}
		default: // TemplateSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
