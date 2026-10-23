// Package mirror provides functions to:
// - detect if a file needs to be backed up by checking if the file exists
// in the vault, if it has a different Size or a different mtime.
// - copy the file to the destination vault, preserving the mtime and mode.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

const prefix string = ".bkp-"

var (
	// ErrNotDir is returned when a path is not a directory.
	ErrNotDir = errors.New("is not a directory")

	// ErrSameDir is returned when src and dst are the same directory.
	ErrSameDir = errors.New("src and dst are the same directory")

	// ErrDstContainedBySrc is returned when dst is a subdir of src.
	ErrDstContainedBySrc = errors.New("dst directory is contained by src directory")

	// ErrSrcContainedByDst is returned when src is a subdir of dst.
	ErrSrcContainedByDst = errors.New("src directory is contained by dst directory")
)

// Result reports the outcome of copying a single file.
type Result struct {
	Path   string
	Bytes  int64
	Copied bool
	Err    error
}

// Task describes a single file to be copied from Src to Dst.
type Task struct {
	Src      string
	Dst      string
	Size     int64
	Progress *atomic.Int64
	Done     bool
}

// ScanResult describes the total number of files to be copied and
// the total number of bytes scanned.
type ScanResult struct {
	Tasks []Task
	Files int64
	Bytes int64
}

type progressWriter struct {
	w   io.Writer
	n   *atomic.Int64
	ctx context.Context
}

func (pw progressWriter) Write(b []byte) (int, error) {
	select {
	case <-pw.ctx.Done():
		return 0, fmt.Errorf("copy cancelled: %w", pw.ctx.Err())

	default:
		n, err := pw.w.Write(b)
		pw.n.Add(int64(n))

		if err != nil {
			return n, fmt.Errorf("write: %w", err)
		}

		return n, nil
	}
}

// Copy mirrors task.Src to task.Dst via an atomic temp-file rename,
// preserving mode and mtime, and reports bytes written through task.Progress.
func Copy(ctx context.Context, task Task) Result {
	return copyFile(ctx, task.Src, task.Dst, task.Progress)
}

// Scan walks src and reports the number of regular files needing copy to
// dst and their total size in bytes.
func Scan(src, dst string) (r ScanResult, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel %s: %w", path, err)
		}

		dstPath := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("info %s: %w", path, err)
		}

		r.Files++
		r.Bytes += info.Size()

		need, err := needsCopy(info, dstPath)
		if err != nil {
			return err
		}

		if need {
			r.Tasks = append(r.Tasks, Task{Src: path, Dst: dstPath, Size: info.Size(), Done: false})
		}

		return nil
	})
	if err != nil {
		return r, fmt.Errorf("scan %s: %w", src, err)
	}

	return r, nil
}

// Sweep removes orphaned .bkp-* tmpfiles left in dst by copies that were
// interrupted before their rename (e.g. power loss).
func Sweep(dst string) (removed int, err error) {
	root, err := os.OpenRoot(dst)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil // no vault yet, nothing to sweep
		}

		return 0, fmt.Errorf("open root %s: %w", dst, err)
	}

	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		if !strings.HasPrefix(d.Name(), prefix) {
			return nil
		}

		// root-scoped, symlink-safe
		if err := root.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		removed++

		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("sweep %s: %w", dst, err)
	}

	return removed, nil
}

// CheckPath makes sure path is an eligible directory.
func CheckPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		//PathError is self describing
		//nolint:wrapcheck
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("%s: %w", path, ErrNotDir)
	}

	return nil
}

// CanonicalPath resolves symlinks and returns absolute path.
func CanonicalPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("canonicalization of path: %w", err)
	}

	normalizedPath, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return "", fmt.Errorf("canonicalization of path: %w", err)
	}

	return normalizedPath, nil
}

func isContained(a, b string) (bool, error) {
	check, err := filepath.Rel(a, b)
	if err != nil {
		return false, fmt.Errorf("checking path relation: %w", err)
	}

	return check != ".." && !strings.HasPrefix(check, ".."+string(filepath.Separator)), nil
}

// ContainmentCheck makes sure neither directory contains the other
// to avoid circular references.
func ContainmentCheck(src, dst string) error {
	if src == dst {
		return ErrSameDir
	}

	contained, err := isContained(src, dst)
	if err != nil {
		return err
	}

	if contained {
		return fmt.Errorf("containment check: %w", ErrDstContainedBySrc)
	}

	contained, err = isContained(dst, src)
	if err != nil {
		return err
	}

	if contained {
		return fmt.Errorf("containment check: %w", ErrSrcContainedByDst)
	}

	return nil
}

func needsCopy(srcInfo fs.FileInfo, dstPath string) (bool, error) {
	if dstInfo, err := os.Stat(dstPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("stat %s: %w", dstPath, err)
	} else if srcInfo.Size() != dstInfo.Size() {
		return true, nil
	} else if !srcInfo.ModTime().Equal(dstInfo.ModTime()) {
		return true, nil
	}

	return false, nil
}

func copyFile(
	ctx context.Context,
	srcPath,
	dstPath string,
	progress *atomic.Int64,
) Result {
	if ctx.Err() != nil {
		return Result{Err: ctx.Err()}
	}

	if progress == nil {
		progress = new(atomic.Int64)
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return Result{Err: err}
	}

	//nolint:gosec // srcPath is the user-provided source tree to mirror
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return Result{Err: err}
	}
	defer func() { _ = srcFile.Close() }()

	srcDir := filepath.Dir(srcPath)
	dirInfo, err := os.Stat(srcDir)
	if err != nil {
		return Result{Err: err}
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), dirInfo.Mode().Perm()); err != nil {
		return Result{Err: err}
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(dstPath), prefix+"*")
	if err != nil {
		return Result{Err: err}
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpFile.Name())
		}
	}()
	defer func() { _ = tmpFile.Close() }()

	copied, err := io.Copy(progressWriter{tmpFile, progress, ctx}, srcFile)
	if err != nil {
		return Result{Err: err}
	}

	if err := os.Chmod(tmpFile.Name(), srcInfo.Mode()); err != nil {
		return Result{Err: err}
	}

	modTime := srcInfo.ModTime()
	if err := os.Chtimes(tmpFile.Name(), modTime, modTime); err != nil {
		return Result{Err: err}
	}

	if err := os.Rename(tmpFile.Name(), dstPath); err != nil {
		return Result{Err: err}
	}

	success = true
	return Result{Path: dstPath, Bytes: copied, Copied: true}
}
