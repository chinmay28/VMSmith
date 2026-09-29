package vm

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// dirRelocator moves a VM directory to a new parent directory. On the same
// filesystem it is a single atomic rename; across filesystems (EXDEV) it
// copies every file into a staging directory next to the destination,
// fsyncs, and renames the staging directory into place — so dst is either
// absent or complete, never half-written.
//
// The source is never deleted by Relocate: the caller finalises with
// Commit (removes the source copy, if any) once every dependent record
// (libvirt domain, snapshots, bbolt) points at dst, or undoes the move
// with Rollback.
type dirRelocator struct {
	// rename is os.Rename in production; tests swap it to force the
	// cross-filesystem copy path.
	rename func(oldpath, newpath string) error
	// chunkSize bounds each copy step so progress can be reported.
	chunkSize int64
}

func newDirRelocator() *dirRelocator {
	return &dirRelocator{rename: os.Rename, chunkSize: 64 << 20}
}

// dirRelocation is the handle for an in-flight move returned by Relocate.
type dirRelocation struct {
	src, dst string
	// renamed is true when the move was a same-filesystem rename (the
	// source no longer exists); false when dst is a copy and src is intact.
	renamed bool
	rename  func(oldpath, newpath string) error
}

// Relocate moves src to dst. dst must not exist; its parent must. progress,
// when non-nil, receives completion percentages (0-100) during a copy.
func (r *dirRelocator) Relocate(src, dst string, progress func(percent float64)) (*dirRelocation, error) {
	if _, err := os.Lstat(dst); err == nil {
		return nil, fmt.Errorf("destination %s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("checking destination %s: %w", dst, err)
	}

	err := r.rename(src, dst)
	if err == nil {
		if progress != nil {
			progress(100)
		}
		return &dirRelocation{src: src, dst: dst, renamed: true, rename: r.rename}, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return nil, fmt.Errorf("moving %s to %s: %w", src, dst, err)
	}

	staging := dst + ".partial"
	_ = os.RemoveAll(staging) // leftover from an interrupted earlier attempt
	if err := r.copyTree(src, staging, progress); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	if err := r.rename(staging, dst); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("finalising copy into %s: %w", dst, err)
	}
	syncDir(filepath.Dir(dst))
	return &dirRelocation{src: src, dst: dst, renamed: false, rename: r.rename}, nil
}

// Commit removes the source copy left behind by a cross-filesystem move.
// It is a no-op after a same-filesystem rename.
func (d *dirRelocation) Commit() error {
	if d.renamed {
		return nil
	}
	return os.RemoveAll(d.src)
}

// Rollback undoes the move, restoring src and removing dst.
func (d *dirRelocation) Rollback() error {
	if d.renamed {
		return d.rename(d.dst, d.src)
	}
	return os.RemoveAll(d.dst)
}

// copyTree copies the regular files and directories under src into a new
// directory dst, preserving permission bits. Anything else (symlinks,
// devices, sockets) is rejected rather than silently skipped: a VM dir only
// ever holds regular files, so an unexpected entry means the move is not
// safe to perform blindly.
func (r *dirRelocator) copyTree(src, dst string, progress func(percent float64)) error {
	var total int64
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
		case info.Mode().IsRegular():
			total += info.Size()
		default:
			return fmt.Errorf("refusing to move %s: not a regular file or directory", path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	var copied int64
	report := func() {
		if progress == nil {
			return
		}
		if total == 0 {
			progress(100)
			return
		}
		progress(float64(copied) * 100 / float64(total))
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return r.copyFile(path, target, info.Mode().Perm(), func(n int64) {
			copied += n
			report()
		})
	})
}

// copyFile copies one regular file in chunks, fsyncing before close. Each
// chunk goes through (*os.File).ReadFrom with an *io.LimitedReader, which
// lets the runtime use copy_file_range on Linux.
func (r *dirRelocator) copyFile(src, dst string, perm os.FileMode, onChunk func(n int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	for {
		n, err := out.ReadFrom(&io.LimitedReader{R: in, N: r.chunkSize})
		if err != nil {
			out.Close()
			return fmt.Errorf("copying %s: %w", src, err)
		}
		if n == 0 {
			break
		}
		onChunk(n)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return fmt.Errorf("syncing %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	// OpenFile's perm is filtered by the umask; restore the source bits so
	// libvirt-qemu keeps the same access it had before the move.
	return os.Chmod(dst, perm)
}

// syncDir fsyncs a directory so a rename into it is durable. Best effort.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

// dirAllocatedBytes returns the on-disk footprint of every regular file
// under dir (allocated blocks, not apparent size, so sparse qcow2 files are
// not over-counted when checking destination free space).
func dirAllocatedBytes(dir string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			total += uint64(st.Blocks) * 512
		} else {
			total += uint64(info.Size())
		}
		return nil
	})
	return total, err
}

// sameFilesystem reports whether two existing paths live on the same device.
func sameFilesystem(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	as, ok1 := ai.Sys().(*syscall.Stat_t)
	bs, ok2 := bi.Sys().(*syscall.Stat_t)
	return ok1 && ok2 && as.Dev == bs.Dev
}

// freeBytes returns the space available to unprivileged writers on the
// filesystem holding path.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
