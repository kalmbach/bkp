package crumbs

import (
	"testing"
)

func TestTruncate(t *testing.T) {
	const testPath = "/path/to/filename.test"

	tests := []struct {
		name  string
		path  string
		width int
		want  string
	}{
		{name: "path shorter than width", path: testPath, width: 50, want: "/path/to/filename.test"},
		{name: "basename larger than width", path: testPath, width: 10, want: "...me.test"},
		{name: "basename shorter than width", path: testPath, width: 20, want: "/pat...filename.test"},
		{name: "width is less than minimal", path: "/path", width: 2, want: "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truncate(tt.path, tt.width); got != tt.want {
				t.Errorf("truncate(%s, %d) = '%s', want '%s'", tt.path, tt.width, got, tt.want)
			}
		})
	}
}
