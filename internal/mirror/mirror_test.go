package mirror

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNeedsCopy(t *testing.T) {
	fileName := "test"
	fileData := "hello world!"

	srcFilePath := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(srcFilePath, []byte(fileData), 0o644); err != nil {
		t.Fatal(err)
	}

	srcInfo, err := os.Stat(srcFilePath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		dstPath    string
		dstContent []byte
		dstMtime   time.Time
		want       bool
	}{
		{name: "does not exist", dstPath: filepath.Join(t.TempDir(), "no"), dstContent: nil, want: true},
		{name: "different size", dstPath: filepath.Join(t.TempDir(), fileName), dstContent: []byte("hi"), want: true},
		{name: "different mod time", dstPath: filepath.Join(t.TempDir(), fileName), dstContent: []byte(fileData), dstMtime: srcInfo.ModTime().Add(-time.Hour), want: true},
		{name: "identical", dstPath: filepath.Join(t.TempDir(), fileName), dstContent: []byte(fileData), dstMtime: srcInfo.ModTime(), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dstContent != nil {
				if err := os.WriteFile(tt.dstPath, tt.dstContent, srcInfo.Mode()); err != nil {
					t.Fatal(err)
				}

				if err := os.Chtimes(tt.dstPath, time.Now(), tt.dstMtime); err != nil {
					t.Fatal(err)
				}
			}

			got, err := needsCopy(srcInfo, tt.dstPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("needsCopy() = %t; want %t", got, tt.want)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	fileName := "test"
	fileData := "hello world!"
	filePerm := fs.FileMode(0o644)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "test_file.tmp")
	if err := os.WriteFile(srcPath, []byte(fileData), filePerm); err != nil {
		t.Fatal(err)
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("copied", func(t *testing.T) {
		dstPath := filepath.Join(t.TempDir(), fileName)
		result := copyFile(srcPath, dstPath)
		if result.Err != nil {
			t.Errorf("copyFile() returned error = %v", result.Err)
		}

		if !result.Copied {
			t.Errorf("copyFile() returned copied = %t, want %t", false, true)
		}

		if result.Path != dstPath {
			t.Errorf("copyFile() returned path = %s, want %s", result.Path, dstPath)
		}

		if result.Bytes != srcInfo.Size() {
			t.Errorf("copyFile() copied bytes = %v, want %v", result.Bytes, srcInfo.Size())
		}

		dstInfo, err := os.Stat(dstPath)
		if err != nil {
			t.Fatal(err)
		}

		if dstInfo.Size() != srcInfo.Size() {
			t.Errorf("copyFile() copied %v bytes, want %v", dstInfo.Size(), srcInfo.Size())
		}

		if !dstInfo.ModTime().Equal(srcInfo.ModTime()) {
			t.Errorf("copyFile() set mod time %v, want %v", dstInfo.ModTime(), srcInfo.ModTime())
		}

		if dstInfo.Mode() != srcInfo.Mode() {
			t.Errorf("copyFile() set mode %v, want %v", dstInfo.Mode(), srcInfo.Mode())
		}

		entries, err := os.ReadDir(filepath.Dir(dstPath))
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".backup") {
				t.Errorf("copyFile() left temp file %s", entry.Name())
			}
		}
	})

	t.Run("report error", func(t *testing.T) {
		dstDir := t.TempDir()
		if err := os.Chmod(dstDir, 0o500); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(dstDir, 0o700) }()

		result := copyFile(srcPath, filepath.Join(dstDir, fileName))
		if result.Err == nil {
			t.Errorf("copyFile() expected error writing read only directory")
		}

		if !errors.Is(result.Err, os.ErrPermission) {
			t.Errorf("copyFile() returned error = %v, expected %v", result.Err, os.ErrPermission)
		}
	})

	t.Run("cleanup on error", func(t *testing.T) {
		dstDir := t.TempDir()
		dstPath := filepath.Join(dstDir, fileName)
		if err := os.Mkdir(dstPath, 0o755); err != nil {
			t.Fatal(err)
		}

		result := copyFile(srcPath, dstPath)
		if result.Err == nil {
			t.Error("copyFile() expected error renaming onto a directory")
		}

		entries, err := os.ReadDir(dstDir)
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".backup") {
				t.Errorf("copyFile() left temp file %s", entry.Name())
			}
		}
	})
}
