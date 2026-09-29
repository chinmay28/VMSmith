package vm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// seedVMDir builds a small VM-shaped directory with a nested file.
func seedVMDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "vm-1")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"disk.qcow2":    bytes.Repeat([]byte("q"), 300_000),
		"cidata.iso":    []byte("iso"),
		"sub/extra.bin": []byte("x"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func exdevRename(oldpath, newpath string) error {
	if strings.HasSuffix(oldpath, ".partial") {
		return os.Rename(oldpath, newpath) // staging rename happens on dst's fs
	}
	return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
}

func assertVMDir(t *testing.T, dir string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "disk.qcow2"))
	if err != nil || len(got) != 300_000 {
		t.Fatalf("disk.qcow2 in %s: len=%d err=%v", dir, len(got), err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub", "extra.bin")); err != nil || string(b) != "x" {
		t.Fatalf("sub/extra.bin in %s: %q %v", dir, b, err)
	}
	info, err := os.Stat(filepath.Join(dir, "cidata.iso"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("cidata.iso mode: %v %v", info, err)
	}
}

func TestRelocateSameFilesystemRenames(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	dst := filepath.Join(root, "target", "vm-1")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}

	var last float64
	rel, err := newDirRelocator().Relocate(src, dst, func(p float64) { last = p })
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if !rel.renamed || last != 100 {
		t.Fatalf("renamed=%v last=%v, want rename + 100%%", rel.renamed, last)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still exists after rename: %v", err)
	}
	assertVMDir(t, dst)

	if err := rel.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	assertVMDir(t, src)
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst still exists after rollback")
	}
}

func TestRelocateCrossFilesystemCopiesThenCommit(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	dst := filepath.Join(root, "target", "vm-1")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}

	r := newDirRelocator()
	r.rename = exdevRename
	r.chunkSize = 100_000
	var reports []float64
	rel, err := r.Relocate(src, dst, func(p float64) { reports = append(reports, p) })
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if rel.renamed {
		t.Fatal("expected copy path")
	}
	assertVMDir(t, dst)
	assertVMDir(t, src) // source untouched until Commit
	if _, err := os.Stat(dst + ".partial"); !os.IsNotExist(err) {
		t.Fatal("staging dir left behind")
	}
	if len(reports) < 3 || reports[len(reports)-1] != 100 {
		t.Fatalf("progress reports = %v, want several ending at 100", reports)
	}
	for i := 1; i < len(reports); i++ {
		if reports[i] < reports[i-1] {
			t.Fatalf("progress not monotonic: %v", reports)
		}
	}

	if err := rel.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source still exists after commit")
	}
}

func TestRelocateCrossFilesystemRollbackRemovesCopy(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	dst := filepath.Join(root, "vm-1-moved")

	r := newDirRelocator()
	r.rename = exdevRename
	rel, err := r.Relocate(src, dst, nil)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if err := rel.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("copy still exists after rollback")
	}
	assertVMDir(t, src)
}

func TestRelocateRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	dst := filepath.Join(root, "exists")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := newDirRelocator().Relocate(src, dst, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already exists", err)
	}
	assertVMDir(t, src)
}

func TestRelocateRejectsSymlinkOnCopyPath(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	r := newDirRelocator()
	r.rename = exdevRename
	dst := filepath.Join(root, "moved")
	if _, err := r.Relocate(src, dst, nil); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("dst created despite failure")
	}
	if _, err := os.Stat(dst + ".partial"); !os.IsNotExist(err) {
		t.Fatal("staging dir left behind after failure")
	}
	assertVMDir(t, src)
}

func TestDirAllocatedBytesAndSameFilesystem(t *testing.T) {
	root := t.TempDir()
	src := seedVMDir(t, root)
	n, err := dirAllocatedBytes(src)
	if err != nil || n == 0 {
		t.Fatalf("dirAllocatedBytes = %d, %v", n, err)
	}
	if !sameFilesystem(src, root) {
		t.Fatal("sameFilesystem(src, root) = false")
	}
	if sameFilesystem(src, filepath.Join(root, "missing")) {
		t.Fatal("sameFilesystem with missing path = true")
	}
	if free, err := freeBytes(root); err != nil || free == 0 {
		t.Fatalf("freeBytes = %d, %v", free, err)
	}
}
