package validate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

var VMNameRe = regexp.MustCompile(`^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]{0,62}[a-zA-Z0-9])$`)
var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]*$`)

func NormalizeTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(strings.ToLower(tag))
		if trimmed == "" {
			return nil, types.NewAPIError("invalid_spec", "tags cannot contain empty values")
		}
		if len(trimmed) > 32 {
			return nil, types.NewAPIError("invalid_spec", "tags must be 1-32 characters")
		}
		if !tagRe.MatchString(trimmed) {
			return nil, types.NewAPIError("invalid_spec", "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens")
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func ValidateOptionalVMResourceValue(value, min, max int, field string) error {
	if value == 0 {
		return nil
	}
	if value < min || value > max {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("%s must be between %d and %d", field, min, max))
	}
	return nil
}

func ValidateTemplateRequest(name, image string, cpus, ramMB, diskGB int) error {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return types.NewAPIError("invalid_name", "template name is required")
	}
	if !VMNameRe.MatchString(trimmedName) {
		return types.NewAPIError("invalid_name", "template name must be 1-64 characters and contain only letters, numbers, and hyphens")
	}
	if strings.TrimSpace(image) == "" {
		return types.NewAPIError("invalid_image", "image is required")
	}
	if err := ValidateOptionalVMResourceValue(cpus, 1, 128, "cpus"); err != nil {
		return err
	}
	if err := ValidateOptionalVMResourceValue(ramMB, 128, 1024*1024, "ram_mb"); err != nil {
		return err
	}
	if err := ValidateOptionalVMResourceValue(diskGB, 1, 1024*10, "disk_gb"); err != nil {
		return err
	}
	return nil
}

func ValidateUniqueTemplateName(name string, templates []*types.VMTemplate) error {
	trimmed := strings.TrimSpace(name)
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tpl.Name), trimmed) {
			return types.NewAPIError("invalid_name", fmt.Sprintf("template name %q already exists", trimmed))
		}
	}
	return nil
}
