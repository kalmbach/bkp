package mirror

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSweep(t *testing.T) {
	dstPath := t.TempDir()
	f, err := os.CreateTemp(dstPath, prefix+"*")
	if err != nil {
		t.Fatal(err)
	}

	removed, err := Sweep(dstPath)
	if err != nil {
		t.Fatal(err)
	}

	if removed != 1 {
		t.Errorf("Sweep(): %d, want: 1", removed)
	}

	_, err = os.Stat(f.Name())
	if err == nil {
		t.Errorf("Stat(%s): nil, wanted error", f.Name())
	}
}

func TestScan(t *testing.T) {
	testData := []byte("hello world!")

	srcPath := t.TempDir()
	dstPath := t.TempDir()

	wantedFiles := 0
	wantedBytes := 0
	for x := range 10 {
		f, err := os.CreateTemp(srcPath, "*.bkp")
		if err != nil {
			t.Fatal(err)
		}

		writtenBytes, err := f.Write(testData)
		if err != nil {
			t.Fatal(err)
		}

		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		if x%2 == 0 {
			// copy file so scan sees it and skips it
			dst := filepath.Join(dstPath, filepath.Base(f.Name()))
			if err := os.WriteFile(dst, testData, 0o644); err != nil {
				t.Fatal(err)
			}

			info, err := os.Stat(f.Name())
			if err != nil {
				t.Fatal(err)
			}

			if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		} else {
			wantedBytes += writtenBytes
			wantedFiles++
		}
	}

	scan, err := Scan(srcPath, dstPath)
	if err != nil {
		t.Fatal(err)
	}

	scannedFiles := len(scan.Tasks)
	if scannedFiles != wantedFiles {
		t.Errorf("scan(){files} = %d; want %d", scannedFiles, wantedFiles)
	}

	var scannedBytes int64
	for _, t := range scan.Tasks {
		scannedBytes += t.Size
	}

	if scannedBytes != int64(wantedBytes) {
		t.Errorf("scan(){bytes} = %d; want %d", scannedBytes, wantedBytes)
	}
}

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
		task := Task{Src: srcPath, Dst: dstPath}
		ctx := context.Background()
		result := copyFile(ctx, task.Src, task.Dst, task.Progress)
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
			if strings.HasPrefix(entry.Name(), ".bkp") {
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

		task := Task{Src: srcPath, Dst: filepath.Join(dstDir, fileName)}
		ctx := context.Background()
		result := copyFile(ctx, task.Src, task.Dst, task.Progress)
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

		task := Task{Src: srcPath, Dst: dstPath}
		ctx := context.Background()
		result := copyFile(ctx, task.Src, task.Dst, task.Progress)
		if result.Err == nil {
			t.Error("copyFile() expected error renaming onto a directory")
		}

		entries, err := os.ReadDir(dstDir)
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".bkp") {
				t.Errorf("copyFile() left temp file %s", entry.Name())
			}
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		dstPath := filepath.Join(t.TempDir(), fileName)
		task := Task{Src: srcPath, Dst: dstPath}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result := copyFile(ctx, task.Src, task.Dst, task.Progress)
		if !errors.Is(result.Err, context.Canceled) {
			t.Errorf("copyFile() returned %v, want %v", result.Err, context.Canceled)
		}

		if result.Copied {
			t.Errorf("copyFile() returned copied = %t, want %t", true, false)
		}

		_, err := os.Stat(dstPath)
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Stat(%s) returned %v, want %v", dstPath, err, os.ErrNotExist)
		}
	})
}

type countingWriter struct {
	buf    bytes.Buffer
	writes int
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	cw.writes++
	return cw.buf.Write(p) //nolint:wrapcheck // test fake, passes the buffer through as is
}

func TestProgressWriter(t *testing.T) {
	data := []byte("hello world!")

	t.Run("live context writes through", func(t *testing.T) {
		inner := &countingWriter{}
		progress := new(atomic.Int64)
		pw := progressWriter{w: inner, n: progress, ctx: context.Background()}

		n, err := pw.Write(data)
		if err != nil {
			t.Fatalf("Write() returned error = %v", err)
		}

		if n != len(data) {
			t.Errorf("Write() returned n = %d, want %d", n, len(data))
		}

		if got := inner.buf.String(); got != string(data) {
			t.Errorf("inner writer got %q, want %q", got, data)
		}

		if got := progress.Load(); got != int64(len(data)) {
			t.Errorf("progress = %d, want %d", got, len(data))
		}
	})

	t.Run("cancelled context does not write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		inner := &countingWriter{}
		progress := new(atomic.Int64)
		pw := progressWriter{w: inner, n: progress, ctx: ctx}

		n, err := pw.Write(data)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Write() returned error = %v, want %v", err, context.Canceled)
		}

		if n != 0 {
			t.Errorf("Write() returned n = %d, want 0", n)
		}

		if inner.writes != 0 {
			t.Errorf("inner writer called %d times, want 0", inner.writes)
		}

		if got := progress.Load(); got != 0 {
			t.Errorf("progress = %d, want 0", got)
		}
	})

	t.Run("cancels between writes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		inner := &countingWriter{}
		progress := new(atomic.Int64)
		pw := progressWriter{w: inner, n: progress, ctx: ctx}

		if _, err := pw.Write(data); err != nil {
			t.Fatalf("first Write() returned error = %v", err)
		}

		cancel()

		if _, err := pw.Write(data); !errors.Is(err, context.Canceled) {
			t.Errorf("second Write() returned error = %v, want %v", err, context.Canceled)
		}

		if inner.writes != 1 {
			t.Errorf("inner writer called %d times, want 1", inner.writes)
		}

		if got := progress.Load(); got != int64(len(data)) {
			t.Errorf("progress = %d, want %d", got, len(data))
		}
	})
}

func TestCheckPath(t *testing.T) {
	tempDir := t.TempDir()
	testData := []byte("hello world!")
	testFile := filepath.Join(tempDir, "testfile")
	missingFile := filepath.Join(tempDir, "missing")

	if err := os.WriteFile(testFile, testData, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		dir  string
		want error
	}{
		{name: "existing dir", dir: tempDir, want: nil},
		{name: "missing", dir: missingFile, want: fs.ErrNotExist},
		{name: "regular file", dir: testFile, want: ErrNotDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPath(tt.dir)
			if !errors.Is(err, tt.want) {
				t.Errorf("CheckPath(%q) = %v; want %v", tt.dir, err, tt.want)
			}
		})
	}
}

func TestContainmentCheck(t *testing.T) {
	tempDir := t.TempDir()
	tempSiblingA := filepath.Join(tempDir, "subdirA")
	tempSiblingB := filepath.Join(tempDir, "subdirB")

	tests := []struct {
		name string
		src  string
		dst  string
		want error
	}{
		{name: "siblings", src: tempSiblingA, dst: tempSiblingB, want: nil},
		{name: "same dir", src: tempDir, dst: tempDir, want: ErrSameDir},
		{name: "dst is child of src", src: tempDir, dst: tempSiblingA, want: ErrDstContainedBySrc},
		{name: "src is child of dst", src: tempSiblingA, dst: tempDir, want: ErrSrcContainedByDst},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ContainmentCheck(tt.src, tt.dst)
			if !errors.Is(err, tt.want) {
				t.Errorf("ContainmentCheck(%q, %q) = %v; want %v", tt.src, tt.dst, err, tt.want)
			}
		})
	}
}

func TestIsContained(t *testing.T) {
	tempDir := t.TempDir()
	tempSiblingA := filepath.Join(tempDir, "subdir")
	tempSiblingB := filepath.Join(tempDir, "subdirB")
	tempSiblingC := filepath.Join(tempDir, "..cache")

	tests := []struct {
		name string
		src  string
		dst  string
		want bool
	}{
		{name: "siblings", src: tempSiblingA, dst: tempSiblingB, want: false},
		{name: "same dir", src: tempDir, dst: tempDir, want: true},
		{name: "dst is child of src", src: tempDir, dst: tempSiblingA, want: true},
		{name: "src is child of dst", src: tempSiblingA, dst: tempDir, want: false},
		{name: "..prefix", src: tempDir, dst: tempSiblingC, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := isContained(tt.src, tt.dst)
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("isContained(%q, %q) = %v; want %v", tt.src, tt.dst, got, tt.want)
			}
		})
	}
}

func TestCanonicalPath(t *testing.T) {
	tempDir := t.TempDir()
	realDir := filepath.Join(tempDir, "real")
	link := filepath.Join(tempDir, "link")
	missing := filepath.Join(tempDir, "missing")

	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	canonical, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(realDir)

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr error
	}{
		{name: "real dir", path: realDir, want: canonical},
		{name: "symlink", path: link, want: canonical},
		{name: "relative", path: ".", want: canonical},
		{name: "missing", path: missing, want: "", wantErr: fs.ErrNotExist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalPath(tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("CanonicalPath(%q) = %v; want %v", tt.path, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("CanonicalPath(%q) = %v; want %v", tt.path, got, tt.want)
			}
		})
	}
}
