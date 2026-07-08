// Package mirror provides functions to:
// - detect if a file needs to be backed up by checking if the file exists
// in the vault, if it has a different Size or a different mtime.
// - copy the file to the destination vault, preserving the mtime and mode.
package mirror

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const prefix string = ".bkp-"

// Result reports the outcome of copying a single file.
type Result struct {
	Path   string
	Bytes  int64
	Copied bool
	Err    error
}

// Task describes a single file to be copied from Src to Dst.
type Task struct {
	Src  string
	Dst  string
	Size int64
}

// Scan walks src and reports the number of regular files needing copy to
// dst and their total size in bytes.
func Scan(src, dst string) (tasks []Task, err error) {
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

		need, err := needsCopy(info, dstPath)
		if err != nil {
			return err
		}

		if need {
			tasks = append(tasks, Task{Src: path, Dst: dstPath, Size: info.Size()})
		}

		return nil
	})
	if err != nil {
		return tasks, fmt.Errorf("scan %s: %w", src, err)
	}

	return tasks, nil
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

func copyFile(srcPath, dstPath string) Result {
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

	copied, err := io.Copy(tmpFile, srcFile)
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
