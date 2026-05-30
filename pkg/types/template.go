package types

import (
	"strings"
	"time"
)

// VMTemplate stores reusable VM defaults that can be applied when creating new VMs.
type VMTemplate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	CPUs        int      `json:"cpus,omitempty"`
	RAMMB       int      `json:"ram_mb,omitempty"`
	DiskGB      int      `json:"disk_gb,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DefaultUser string   `json:"default_user,omitempty"`

	// OSType pins the guest OS family (linux|windows) that VMs created from
	// this template inherit when their own VMSpec.OSType is empty. Empty
	// stored value resolves to OSTypeLinux via ResolvedOSType — mirrors
	// VMSpec.ResolvedOSType.
	OSType OSType `json:"os_type,omitempty"`

	// OSVariant pairs with OSType for Windows templates (windows-10/11,
	// windows-server-2019/2022/2025). Advisory metadata; the create-time VM
	// validator enforces the known list when a derived VM specifies it.
	OSVariant string `json:"os_variant,omitempty"`

	Networks  []NetworkAttachment `json:"networks,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// ResolvedOSType mirrors VMSpec.ResolvedOSType so a list filter can treat
// templates with an empty stored OSType as Linux. Matching is
// case-insensitive and whitespace is trimmed so a value coming back from
// an old client or a hand-edited bbolt record still resolves cleanly.
func (t VMTemplate) ResolvedOSType() OSType {
	if OSType(strings.ToLower(strings.TrimSpace(string(t.OSType)))) == OSTypeWindows {
		return OSTypeWindows
	}
	return OSTypeLinux
}

// IsWindows reports whether this template targets a Windows guest.
func (t VMTemplate) IsWindows() bool {
	return t.ResolvedOSType() == OSTypeWindows
}

// TemplateUpdateSpec defines the fields that may be edited on an existing
// template via PATCH /api/v1/templates/{id}. PATCH semantics mirror the
// VM convention: an empty Description means "no change", and a nil Tags
// slice means "no change". Passing a non-nil but empty Tags slice
// (`"tags": []`) clears the tag set.
type TemplateUpdateSpec struct {
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
