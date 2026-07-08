package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dustin/go-humanize"
	"github.com/kalmbach/bkp/internal/mirror"
)

// Catppuccin Mocha subset — https://catppuccin.com/palette
var (
	mochaText     = lipgloss.Color("#cdd6f4")
	mochaOverlay0 = lipgloss.Color("#6c7086")
	mochaMauve    = lipgloss.Color("#cba6f7")
	mochaPeach    = lipgloss.Color("#fab387")

	logoStyle    = lipgloss.NewStyle().Foreground(mochaMauve).Bold(true)
	titleStyle   = lipgloss.NewStyle().Foreground(mochaMauve).Bold(true)
	boldStyle    = lipgloss.NewStyle().Foreground(mochaText).Bold(true)
	faintStyle   = lipgloss.NewStyle().Foreground(mochaOverlay0)
	warningStyle = lipgloss.NewStyle().Foreground(mochaPeach)
)

const logoArt = "▐▛███▜▌\n▝▜█████▛▘\n  ▘▘ ▝▝"

func renderHeader(title string) string {
	logo := "\n" + logoStyle.Render(logoArt)
	info := "\n" + titleStyle.Render(title) + "\n" + faintStyle.Render("v"+version)
	return lipgloss.JoinHorizontal(lipgloss.Top, logo, "  ", info)
}

type bkpPhase int

const (
	phaseSweeping bkpPhase = iota
	phaseScanning
	phaseScanned
	phaseError
)

type sweepDoneMsg struct {
	removed int
	err     error
}

type scanDoneMsg struct {
	tasks []mirror.Task
	err   error
}

type model struct {
	src string
	dst string

	phase bkpPhase
	tasks []mirror.Task
	err   error
}

func sweepCmd(dst string) tea.Cmd {
	return func() tea.Msg {
		removed, err := mirror.Sweep(dst)
		return sweepDoneMsg{removed: removed, err: err}
	}
}

func scanCmd(src, dst string) tea.Cmd {
	return func() tea.Msg {
		tasks, err := mirror.Scan(src, dst)
		return scanDoneMsg{tasks: tasks, err: err}
	}
}

func newModel(src, dst string) model {
	return model{src: src, dst: dst}
}

func (m model) Init() tea.Cmd {
	return sweepCmd(m.dst)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q":
			return m, tea.Quit
		}
	case sweepDoneMsg:
		if msg.err != nil {
			m.phase, m.err = phaseError, msg.err
			return m, nil
		}

		m.phase = phaseScanning
		return m, scanCmd(m.src, m.dst)
	case scanDoneMsg:
		if msg.err != nil {
			m.phase, m.err = phaseError, msg.err
			return m, nil
		}

		m.phase, m.tasks = phaseScanned, msg.tasks
	}

	return m, nil
}

func (m model) View() tea.View {
	var s strings.Builder
	title := "BACKUP"

	s.WriteString(renderHeader(title) + "\n\n")
	s.WriteString(boldStyle.Render("Src Dir: ") + m.src + "\n")
	s.WriteString(boldStyle.Render("Dst Dir: ") + m.dst + "\n")
	s.WriteString("\n")
	switch m.phase {
	case phaseSweeping:
		s.WriteString(warningStyle.Render("Removing orphaned tempfiles...") + "\n")

	case phaseScanning:
		s.WriteString(warningStyle.Render("Scanning...") + "\n")

	case phaseScanned:
		files := fmt.Sprintf("%d", len(m.tasks))

		// byte total is a sum of file sizes, always non-negative
		var bytes int64
		for _, t := range m.tasks {
			bytes += t.Size
		}

		size := humanize.Bytes(uint64(bytes))

		s.WriteString(boldStyle.Render(files) + " files to copy.\n")
		s.WriteString(boldStyle.Render(size) + " total\n")

	case phaseError:
		s.WriteString(warningStyle.Render("Scan failed: ") + warningStyle.Render(m.err.Error()) + "\n")
	}

	s.WriteString("\n" + faintStyle.Render("q/esc - quit"))
	return tea.NewView(s.String())
}
