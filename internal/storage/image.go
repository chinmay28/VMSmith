package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Manager handles image creation, export, and management.
type Manager struct {
	store *store.Store
	cfg   *config.Config
}

// NewManager creates a new storage manager.
func NewManager(cfg *config.Config, store *store.Store) *Manager {
	return &Manager{store: store, cfg: cfg}
}

// CreateImageOptions carries optional metadata for CreateImage. Description is
// a free-form string (max 1024 chars enforced at the API layer); Tags are
// already-normalized values produced by validate.NormalizeTags.
type CreateImageOptions struct {
	Description string
	Tags        []string
}

// CreateImage creates a flattened qcow2 image from a VM's disk and persists
// the optional description / tags alongside the standard image metadata.
func (m *Manager) CreateImage(vmDiskPath, name, sourceVM string, opts CreateImageOptions) (*types.Image, error) {
	imagePath := filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")

	// Flatten the overlay into a standalone image (no backing file)
	cmd := exec.Command("qemu-img", "convert",
		"-f", "qcow2",
		"-O", "qcow2",
		"-c", // compress
		vmDiskPath,
		imagePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("qemu-img convert: %s: %w", string(out), err)
	}

	info, err := os.Stat(imagePath)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	img := &types.Image{
		ID:          fmt.Sprintf("img-%d", now.UnixNano()),
		Name:        name,
		Path:        imagePath,
		SizeBytes:   info.Size(),
		Format:      "qcow2",
		SourceVM:    sourceVM,
		Description: strings.TrimSpace(opts.Description),
		Tags:        opts.Tags,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := m.store.PutImage(img); err != nil {
		return nil, err
	}

	return img, nil
}

// ListImages returns all available images.
func (m *Manager) ListImages() ([]*types.Image, error) {
	return m.store.ListImages()
}

// FilterImagesByTag returns only images whose tag list contains tag
// (case-insensitive). The returned slice is a fresh allocation so callers can
// safely retain the input slice unchanged.
func FilterImagesByTag(imgs []*types.Image, tag string) []*types.Image {
	out := make([]*types.Image, 0, len(imgs))
	for _, img := range imgs {
		if img == nil {
			continue
		}
		for _, t := range img.Tags {
			if strings.EqualFold(t, tag) {
				out = append(out, img)
				break
			}
		}
	}
	return out
}

// CreateTemplate stores a reusable VM template.
func (m *Manager) CreateTemplate(tpl *types.VMTemplate) (*types.VMTemplate, error) {
	if tpl == nil {
		return nil, fmt.Errorf("template is required")
	}

	now := time.Now()
	tpl.Name = strings.TrimSpace(tpl.Name)
	tpl.Image = strings.TrimSpace(tpl.Image)
	tpl.Description = strings.TrimSpace(tpl.Description)
	tpl.DefaultUser = strings.TrimSpace(tpl.DefaultUser)
	if tpl.ID == "" {
		tpl.ID = fmt.Sprintf("tmpl-%d", now.UnixNano())
	}
	if tpl.CreatedAt.IsZero() {
		tpl.CreatedAt = now
	}
	if tpl.UpdatedAt.IsZero() {
		tpl.UpdatedAt = now
	}
	if err := m.store.PutTemplate(tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

// ListTemplates returns all available VM templates.
func (m *Manager) ListTemplates() ([]*types.VMTemplate, error) {
	return m.store.ListTemplates()
}

// GetTemplate retrieves a VM template by ID.
func (m *Manager) GetTemplate(id string) (*types.VMTemplate, error) {
	return m.store.GetTemplate(id)
}

// DeleteTemplate removes a VM template from metadata storage.
func (m *Manager) DeleteTemplate(id string) error {
	if _, err := m.store.GetTemplate(id); err != nil {
		return err
	}
	return m.store.DeleteTemplate(id)
}

// UpdateTemplate applies a partial patch to an existing template. An empty
// patch.Description leaves the description untouched; a nil patch.Tags leaves
// the tag set untouched, while a non-nil empty slice clears tags. UpdatedAt
// is bumped only when at least one field actually changed.
func (m *Manager) UpdateTemplate(id string, patch types.TemplateUpdateSpec) (*types.VMTemplate, error) {
	tpl, err := m.store.GetTemplate(id)
	if err != nil {
		return nil, err
	}

	changed := false
	if trimmed := strings.TrimSpace(patch.Description); trimmed != "" && trimmed != tpl.Description {
		tpl.Description = trimmed
		changed = true
	}
	if patch.Tags != nil && !stringSlicesEqual(patch.Tags, tpl.Tags) {
		// Caller passed an explicit slice (including []) — replace.
		tpl.Tags = append([]string(nil), patch.Tags...)
		changed = true
	}
	if changed {
		tpl.UpdatedAt = time.Now()
		if err := m.store.PutTemplate(tpl); err != nil {
			return nil, err
		}
	}
	return tpl, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DeleteImage removes an image from disk and metadata.
func (m *Manager) DeleteImage(id string) error {
	img, err := m.store.GetImage(id)
	if err != nil {
		return err
	}

	os.Remove(img.Path)
	return m.store.DeleteImage(id)
}

// GetImage retrieves image metadata.
func (m *Manager) GetImage(id string) (*types.Image, error) {
	return m.store.GetImage(id)
}

// UpdateImage applies a metadata patch to an existing image. An empty
// Description is treated as "no change". A nil Tags slice means "no change";
// a non-nil slice (including an empty one) replaces the current tag set.
// Tags are not re-validated here — callers should normalize first.
func (m *Manager) UpdateImage(id string, patch types.ImageUpdateSpec) (*types.Image, error) {
	img, err := m.store.GetImage(id)
	if err != nil {
		return nil, err
	}

	changed := false
	if desc := strings.TrimSpace(patch.Description); desc != "" && desc != img.Description {
		img.Description = desc
		changed = true
	}
	if patch.Tags != nil {
		img.Tags = patch.Tags
		changed = true
	}

	if changed {
		img.UpdatedAt = time.Now()
		if err := m.store.PutImage(img); err != nil {
			return nil, err
		}
	}
	return img, nil
}

// ImagePath returns the filesystem path for a named image.
func (m *Manager) ImagePath(name string) string {
	return filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")
}

// ImportImage saves an uploaded file into the images directory and registers
// it with the supplied metadata.
func (m *Manager) ImportImage(name string, src []byte, opts CreateImageOptions) (*types.Image, error) {
	if err := os.MkdirAll(m.cfg.Storage.ImagesDir, 0o755); err != nil {
		return nil, err
	}

	imagePath := filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")
	if err := os.WriteFile(imagePath, src, 0o644); err != nil {
		return nil, fmt.Errorf("writing image file: %w", err)
	}

	now := time.Now()
	img := &types.Image{
		ID:          fmt.Sprintf("img-%d", now.UnixNano()),
		Name:        name,
		Path:        imagePath,
		SizeBytes:   int64(len(src)),
		Format:      "qcow2",
		Description: strings.TrimSpace(opts.Description),
		Tags:        opts.Tags,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := m.store.PutImage(img); err != nil {
		os.Remove(imagePath)
		return nil, err
	}

	return img, nil
}
