// Package mirror copies a source directory tree into a destination,
// skipping files whose size and mtime already match.
package mirror

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Result reports the outcome of copying a single file.
type Result struct {
	Path   string
	Bytes  int64
	Copied bool
	Err    error
}

//nolint:unused
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

//nolint:unused
func copyFile(srcPath, dstPath string) Result {
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return Result{Err: err}
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return Result{Err: err}
	}
	defer func() { _ = srcFile.Close() }()

	tmpFile, err := os.CreateTemp(filepath.Dir(dstPath), ".backup-*")
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

	copied, err := io.Copy(tmpFile, srcFile)
	if err != nil {
		return Result{Err: err}
	}

	// Flush the file before renaming
	if err := tmpFile.Close(); err != nil {
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
