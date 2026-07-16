package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dustin/go-humanize"
	"github.com/kalmbach/bkp/internal/crumbs"
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

func renderHeader() string {
	l := "\n" + logoStyle.Render(logoArt)
	t := "\n" + titleStyle.Render("bkp")
	v := "\n" + faintStyle.Render("v"+version)
	return lipgloss.JoinHorizontal(lipgloss.Top, l, " ", t+v)
}

type bkpPhase int

const (
	phaseSweeping bkpPhase = iota
	phaseScanning
	phaseScanned
	phaseCopying
	phaseQuitting
	phaseError
)

const tickInterval = 100 * time.Millisecond

type tickMsg struct{}

type sweepDoneMsg struct {
	removed int
	err     error
}

type scanDoneMsg struct {
	tasks []mirror.Task
	bytes int64
	files int64
	err   error
}

type copyDoneMsg struct {
	path   string
	bytes  int64
	copied bool
	err    error
}

type model struct {
	src string
	dst string

	scanned bool
	files   int64
	bytes   int64

	phase   bkpPhase
	tasks   []mirror.Task
	current int
	err     error

	bar      progress.Model
	phaseBar progress.Model
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func sweepCmd(dst string) tea.Cmd {
	return func() tea.Msg {
		removed, err := mirror.Sweep(dst)
		return sweepDoneMsg{removed: removed, err: err}
	}
}

func scanCmd(src, dst string) tea.Cmd {
	return func() tea.Msg {
		scan, err := mirror.Scan(src, dst)
		return scanDoneMsg{
			tasks: scan.Tasks,
			files: scan.Files,
			bytes: scan.Bytes,
			err:   err,
		}
	}
}

func copyCmd(task mirror.Task) tea.Cmd {
	return func() tea.Msg {
		r := mirror.Copy(task)
		return copyDoneMsg{path: r.Path, bytes: r.Bytes, copied: r.Copied, err: r.Err}
	}
}

func newModel(src, dst string) model {
	bar := progress.New(
		progress.WithColors(mochaMauve, mochaPeach),
		progress.WithWidth(40),
	)

	phaseBar := progress.New(
		progress.WithColors(mochaMauve, mochaPeach),
		progress.WithWidth(20),
	)

	return model{src: src, dst: dst, scanned: false, bar: bar, phaseBar: phaseBar}
}

func (m model) Init() tea.Cmd {
	return sweepCmd(m.dst)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q":
			m.phase = phaseQuitting
			return m, tea.Quit
		case "space":
			if m.phase == phaseScanned {
				if len(m.tasks) == 0 {
					break
				}

				m.phase = phaseCopying
				m.tasks[m.current].Progress = new(atomic.Int64)

				return m, tea.Batch(copyCmd(m.tasks[m.current]), tickCmd())
			}

			if m.phase == phaseCopying {
				m.phase = phaseScanned
			}
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

		m.current = 0
		m.phase = phaseScanned
		m.scanned = true
		m.tasks, m.files, m.bytes = msg.tasks, msg.files, msg.bytes
	case tickMsg:
		if m.phase != phaseCopying {
			return m, nil
		}
		return m, tickCmd()
	case copyDoneMsg:
		if msg.err != nil {
			m.phase, m.err = phaseError, msg.err
			return m, nil
		}

		m.tasks[m.current].Done = msg.copied
		m.current++

		if m.phase == phaseCopying {
			if m.current >= len(m.tasks) {
				m.phase = phaseScanning
				return m, scanCmd(m.src, m.dst)
			}
			m.tasks[m.current].Progress = new(atomic.Int64)
			return m, copyCmd(m.tasks[m.current])
		}

		return m, nil
	}

	return m, nil
}

func (m model) PhaseView(task mirror.Task) string {
	var s strings.Builder

	fraction := float64(1)
	if task.Progress != nil && task.Size > 0 {
		fraction = float64(task.Progress.Load()) / float64(task.Size)
	}

	s.WriteString(
		warningStyle.Render(
			fmt.Sprintf("copying %s: ", crumbs.Truncate(task.Dst, 60)),
		) + m.phaseBar.ViewAs(fraction) + "\n",
	)
	return s.String()
}

func (m model) BackupView() string {
	var s strings.Builder

	// byte total is a sum of file sizes, always non-negative
	var bytes, files int64
	for _, t := range m.tasks {
		if !t.Done {
			files++
			bytes += t.Size
		}
	}

	if files > 0 {
		var percentage int
		var fraction float64
		if m.bytes > 0 {
			percentage = int((m.bytes - bytes) * 100 / m.bytes)
			fraction = float64(m.bytes-bytes) / float64(m.bytes)
		} else {
			percentage = int((m.files - files) * 100 / m.files)
			fraction = float64(m.files-files) / float64(m.files)
		}

		size := humanize.Bytes(uint64(bytes))

		s.WriteString(boldStyle.Render("home is") + titleStyle.Render(fmt.Sprintf(" %d%% ", percentage)) + boldStyle.Render("backed up") + "\n")
		s.WriteString(boldStyle.Render(fmt.Sprintf("%d (%s) files to copy.", files, size)) + "\n")

		if m.phase == phaseCopying {
			s.WriteString("\n" + m.bar.ViewAs(fraction) + "\n")
		}
	} else {
		s.WriteString(boldStyle.Render("home is backed up.\n"))
	}

	return s.String()
}

func (m model) View() tea.View {
	var s strings.Builder
	s.WriteString(renderHeader() + "\n\n")
	s.WriteString(boldStyle.Render("Vault: ") + m.dst + "\n")
	s.WriteString("\n")
	switch m.phase {
	case phaseSweeping:
		s.WriteString(warningStyle.Render("Removing orphaned tempfiles...") + "\n")

	case phaseScanning:
		s.WriteString(warningStyle.Render("Scanning...") + "\n")

	case phaseScanned:
		s.WriteString(m.BackupView())

	case phaseCopying:
		s.WriteString(m.BackupView() + "\n")
		s.WriteString(m.PhaseView(m.tasks[m.current]) + "\n")

	case phaseQuitting:
		if m.scanned {
			s.WriteString(m.BackupView() + "\n")
		}

	case phaseError:
		s.WriteString(warningStyle.Render("Scan failed: ") + warningStyle.Render(m.err.Error()) + "\n")
	}

	if m.phase != phaseQuitting {
		s.WriteString("\n" + faintStyle.Render("space - start/stop, q/esc - quit"))
	}

	return tea.NewView(s.String())
}
