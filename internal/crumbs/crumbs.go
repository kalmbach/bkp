// Package crumbs provides a function to truncate a path string to fit a fixed width.
package crumbs

import (
	"path/filepath"

	"charm.land/lipgloss/v2"
)

const minWidth = 3

// Truncate keeps the head of the path and the basename at the tail,
// ellipsis in the middle.
func Truncate(path string, width int) string {
	if width <= minWidth {
		return "..."
	}

	if lipgloss.Width(path) <= width {
		return path
	}

	base := filepath.Base(path)
	if lipgloss.Width(base) >= width-3 {
		r := []rune(base)
		return "..." + string(r[len(r)-(width-3):])
	}

	head := width - 3 - lipgloss.Width(base)
	r := []rune(path)

	return string(r[:head]) + "..." + base
}
