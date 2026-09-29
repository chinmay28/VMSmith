package vm

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// Disk placement: a VM's directory (system disk + provisioning ISO) lives
// at <location>/<vm-id>/, where <location> is storage.base_dir (the
// "default" location) or one of storage.disk_locations. MoveDisk relocates
// that directory for a stopped VM and repoints everything that references
// the old path: the persistent domain XML, every snapshot's embedded
// domain definition, and the bbolt record.

type diskMoveProgressKey struct{}

// WithDiskMoveProgress returns a context carrying a disk-move progress
// callback (0-100), mirroring WithCloneProgress so the API layer can stream
// cross-filesystem copy progress without widening the Manager interface.
func WithDiskMoveProgress(ctx context.Context, fn func(percent float64)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, diskMoveProgressKey{}, fn)
}

func diskMoveProgressFromContext(ctx context.Context) func(percent float64) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(diskMoveProgressKey{}).(func(percent float64))
	return fn
}

// storedDiskLocationName is the form persisted in VMSpec.DiskLocation:
// the canonical name (or absolute path for an ad-hoc location), with the
// default location stored as "" so records created before locations
// existed and new default-placed VMs look alike.
func storedDiskLocationName(name string) string {
	name = config.NormalizeDiskLocationName(name)
	if name == config.DefaultDiskLocation {
		return ""
	}
	return name
}

// ResolveDiskLocation validates a location — a configured name or an
// absolute directory path under storage.disk_location_roots — and checks
// its directory exists. A path is resolved through symlinks and re-checked
// so a link cannot smuggle disks into a denied tree; the resolved path is
// what gets stored. Typed errors: invalid_disk_location (unknown name or
// disallowed path) and disk_location_unavailable (directory missing — e.g.
// the drive is not mounted).
func ResolveDiskLocation(cfg config.StorageConfig, name string) (config.DiskLocation, error) {
	loc, err := cfg.ResolveDiskLocation(name)
	if err != nil {
		return config.DiskLocation{}, types.NewAPIError("invalid_disk_location", err.Error())
	}
	if info, err := os.Stat(loc.Path); err != nil || !info.IsDir() {
		return config.DiskLocation{}, types.NewAPIError("disk_location_unavailable",
			fmt.Sprintf("disk location %q directory %s is not available; create it (and mount the drive) first", loc.Name, loc.Path))
	}
	if !config.IsDiskLocationPath(loc.Name) {
		return loc, nil
	}
	real, err := filepath.EvalSymlinks(loc.Path)
	if err != nil {
		return config.DiskLocation{}, types.NewAPIError("disk_location_unavailable",
			fmt.Sprintf("resolving disk location %s: %v", loc.Path, err))
	}
	if real == loc.Path {
		return loc, nil
	}
	resolved, err := cfg.ResolveDiskLocation(real)
	if err != nil {
		return config.DiskLocation{}, types.NewAPIError("invalid_disk_location",
			fmt.Sprintf("%s resolves to %s: %v", loc.Path, real, err))
	}
	return resolved, nil
}

// MoveDisk relocates a stopped VM's disk directory to another configured
// storage location. Typed errors: invalid_disk_location,
// disk_location_unavailable, disk_location_unchanged, vm_running,
// insufficient_storage, disk_move_conflict.
func (m *LibvirtManager) MoveDisk(ctx context.Context, id string, location string) (*types.VM, error) {
	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}
	loc, err := ResolveDiskLocation(m.cfg.Storage, location)
	if err != nil {
		return nil, err
	}

	srcDir := filepath.Clean(filepath.Dir(storedVM.DiskPath))
	dstDir := filepath.Join(loc.Path, id)
	if srcDir == dstDir {
		return nil, types.NewAPIError("disk_location_unchanged",
			fmt.Sprintf("vm disk already lives in location %q", loc.Name))
	}
	if strings.HasPrefix(dstDir, srcDir+"/") {
		return nil, types.NewAPIError("invalid_disk_location",
			fmt.Sprintf("location %s is inside the vm's own directory %s", loc.Path, srcDir))
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()
	if state, _, err := dom.GetState(); err != nil {
		return nil, fmt.Errorf("getting domain state: %w", err)
	} else if state != libvirt.DOMAIN_SHUTOFF {
		return nil, types.NewAPIError("vm_running", "vm must be stopped before its disk can be moved")
	}

	if _, err := os.Lstat(dstDir); err == nil {
		return nil, types.NewAPIError("disk_move_conflict",
			fmt.Sprintf("%s already exists; remove the leftover directory before moving", dstDir))
	}
	if !sameFilesystem(srcDir, loc.Path) {
		need, err := dirAllocatedBytes(srcDir)
		if err != nil {
			return nil, fmt.Errorf("sizing %s: %w", srcDir, err)
		}
		free, err := freeBytes(loc.Path)
		if err != nil {
			return nil, fmt.Errorf("checking free space on %s: %w", loc.Path, err)
		}
		if need > free {
			return nil, types.NewAPIError("insufficient_storage",
				fmt.Sprintf("location %q has %d bytes free but the vm needs %d", loc.Name, free, need))
		}
	}

	logger.Info("daemon", "moving vm disk", "vm", storedVM.Name, "vm_id", id,
		"from", srcDir, "to", dstDir, "location", loc.Name)

	relocation, err := newDirRelocator().Relocate(srcDir, dstDir, diskMoveProgressFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("relocating vm directory: %w", err)
	}

	// A cross-filesystem copy can take minutes; if the VM was started in
	// the meantime it is writing to the source and the copy is stale.
	if state, _, err := dom.GetState(); err != nil || state != libvirt.DOMAIN_SHUTOFF {
		m.rollbackRelocation(relocation, storedVM.Name)
		return nil, types.NewAPIError("vm_running", "vm was started while its disk was being moved; the move was rolled back")
	}

	moved := *storedVM
	moved.DiskPath = filepath.Join(dstDir, filepath.Base(storedVM.DiskPath))
	moved.Spec.DiskLocation = storedDiskLocationName(loc.Name)
	moved.UpdatedAt = time.Now()

	// Repoint libvirt, newest dependency first; unwind in reverse on failure.
	if err := m.redefineDomain(dom, &moved, moved.Spec); err != nil {
		m.rollbackRelocation(relocation, storedVM.Name)
		return nil, err
	}
	rewritten, err := rewriteSnapshotsDiskDir(dom, srcDir, dstDir)
	if err != nil {
		_, _ = rewriteSnapshotsDiskDir(dom, dstDir, srcDir)
		m.restoreDomain(dom, storedVM)
		m.rollbackRelocation(relocation, storedVM.Name)
		return nil, fmt.Errorf("repointing snapshots: %w", err)
	}
	if err := m.store.PutVM(&moved); err != nil {
		if rewritten > 0 {
			_, _ = rewriteSnapshotsDiskDir(dom, dstDir, srcDir)
		}
		m.restoreDomain(dom, storedVM)
		m.rollbackRelocation(relocation, storedVM.Name)
		return nil, fmt.Errorf("persisting vm: %w", err)
	}

	if err := relocation.Commit(); err != nil {
		// The move itself succeeded; only the old copy lingers.
		logger.Warn("daemon", "vm disk moved but the old directory could not be removed",
			"vm", storedVM.Name, "path", srcDir, "error", err.Error())
	}
	logger.Info("daemon", "vm disk moved", "vm", storedVM.Name, "vm_id", id,
		"location", loc.Name, "disk_path", moved.DiskPath)
	return &moved, nil
}

func (m *LibvirtManager) restoreDomain(dom *libvirt.Domain, original *types.VM) {
	if err := m.redefineDomain(dom, original, original.Spec); err != nil {
		logger.Error("daemon", "failed to restore domain definition after aborted disk move",
			"vm", original.Name, "error", err.Error())
	}
}

func (m *LibvirtManager) rollbackRelocation(r *dirRelocation, vmName string) {
	if err := r.Rollback(); err != nil {
		logger.Error("daemon", "failed to roll back vm directory after aborted disk move",
			"vm", vmName, "src", r.src, "dst", r.dst, "error", err.Error())
	}
}

// rewriteSnapshotsDiskDir redefines every snapshot of dom whose XML
// references oldDir so it references newDir instead. Internal qcow2
// snapshots travel with the disk file, but libvirt keeps a copy of the
// domain definition in each snapshot's metadata — reverting to a snapshot
// that still names the old path would point the VM at a file that no
// longer exists. Returns how many snapshots were rewritten.
func rewriteSnapshotsDiskDir(dom *libvirt.Domain, oldDir, newDir string) (int, error) {
	snaps, err := dom.ListAllSnapshots(libvirt.DOMAIN_SNAPSHOT_LIST_TOPOLOGICAL)
	if err != nil {
		return 0, fmt.Errorf("listing snapshots: %w", err)
	}
	defer func() {
		for i := range snaps {
			snaps[i].Free()
		}
	}()

	rewritten := 0
	for i := range snaps {
		raw, err := snaps[i].GetXMLDesc(libvirt.DOMAIN_SNAPSHOT_XML_SECURE)
		if err != nil {
			return rewritten, fmt.Errorf("dumping snapshot xml: %w", err)
		}
		updated := rewriteDiskDirInXML(raw, oldDir, newDir)
		if updated == raw {
			continue
		}
		flags := libvirt.DOMAIN_SNAPSHOT_CREATE_REDEFINE
		if current, err := snaps[i].IsCurrent(0); err == nil && current {
			flags |= libvirt.DOMAIN_SNAPSHOT_CREATE_CURRENT
		}
		if _, err := dom.CreateSnapshotXML(updated, flags); err != nil {
			return rewritten, fmt.Errorf("redefining snapshot: %w", err)
		}
		rewritten++
	}
	return rewritten, nil
}

// rewriteDiskDirInXML replaces references to files under oldDir with the
// same files under newDir. Matching on "<dir>/" (in raw and XML-escaped
// form) keeps sibling directories that merely share a prefix — vm-1 vs
// vm-10 — untouched.
func rewriteDiskDirInXML(doc, oldDir, newDir string) string {
	oldPrefix := filepath.Clean(oldDir) + "/"
	newPrefix := filepath.Clean(newDir) + "/"
	doc = strings.ReplaceAll(doc, oldPrefix, newPrefix)
	if escOld, escNew := xmlEscape(oldPrefix), xmlEscape(newPrefix); escOld != oldPrefix {
		doc = strings.ReplaceAll(doc, escOld, escNew)
	}
	return doc
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
