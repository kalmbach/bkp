package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
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
	normalStyle  = lipgloss.NewStyle().Foreground(mochaText)
	faintStyle   = lipgloss.NewStyle().Foreground(mochaOverlay0)
	warningStyle = lipgloss.NewStyle().Foreground(mochaPeach)
)

const logoArt = "▐▛███▜▌\n▝▜█████▛▘\n  ▘▘ ▝▝"

func renderHeader(dst string) string {
	l := "\n" + logoStyle.Render(logoArt)
	t := "\n" + titleStyle.Render("bkp - v"+version)
	d := "\n" + faintStyle.Render("home -> "+dst)
	return lipgloss.JoinHorizontal(lipgloss.Top, l, " ", t+d)
}

type bkpPhase int

const (
	phaseSweeping bkpPhase = iota
	phaseScanning
	phaseScanned
	phaseCopying
	phaseStopping
	phaseQuitting
	phaseError
)

func (p bkpPhase) busy() bool {
	switch p {
	case phaseSweeping, phaseScanning, phaseCopying, phaseStopping, phaseQuitting:
		return true
	case phaseScanned, phaseError:
		return false
	}
	return false
}

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
	ctx     context.Context
	cancel  context.CancelFunc
	current int
	err     error

	bar  progress.Model
	spin spinner.Model
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

func copyCmd(ctx context.Context, task mirror.Task) tea.Cmd {
	return func() tea.Msg {
		r := mirror.Copy(ctx, task)
		return copyDoneMsg{path: r.Path, bytes: r.Bytes, copied: r.Copied, err: r.Err}
	}
}

func newModel(src, dst string) model {
	bar := progress.New(
		progress.WithColors(mochaPeach, mochaMauve),
		progress.WithWidth(40),
	)

	spin := spinner.New(
		spinner.WithSpinner(spinner.Pulse),
		spinner.WithStyle(warningStyle),
	)

	return model{
		src:     src,
		dst:     dst,
		scanned: false,
		bar:     bar,
		spin:    spin,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(sweepCmd(m.dst), m.spin.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// ignore keys while quitting
		if m.phase == phaseQuitting {
			return m, nil
		}

		switch msg.String() {
		case "esc", "q":
			if m.phase == phaseCopying || m.phase == phaseStopping {
				// cancel only if we are not stopping yet
				if m.phase == phaseCopying && m.cancel != nil {
					m.cancel()
				}

				m.phase = phaseQuitting

				// return nil here to wait for the go routines
				return m, nil
			}

			m.phase = phaseQuitting
			return m, tea.Quit
		case "space":
			if m.phase == phaseScanned {
				if len(m.tasks) == 0 {
					break
				}

				m.phase = phaseCopying
				m.ctx, m.cancel = context.WithCancel(context.Background())
				m.tasks[m.current].Progress = new(atomic.Int64)

				return m, tea.Batch(copyCmd(m.ctx, m.tasks[m.current]), tickCmd(), m.spin.Tick)
			}

			if m.phase == phaseCopying {
				m.phase = phaseStopping
				m.cancel()
			}
		}
	case spinner.TickMsg:
		if !m.phase.busy() {
			return m, nil
		}

		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case sweepDoneMsg:
		if msg.err != nil {
			m.phase, m.err = phaseError, msg.err
			return m, nil
		}

		m.phase = phaseScanning
		return m, tea.Batch(scanCmd(m.src, m.dst), m.spin.Tick)
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
		if m.phase == phaseQuitting {
			return m, tea.Quit
		}

		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				if m.phase == phaseStopping {
					m.phase = phaseScanned
				}
				return m, nil
			}

			// copy errored out, release context's resources
			m.cancel()

			m.phase = phaseError
			m.err = msg.err
			return m, nil
		}

		m.tasks[m.current].Done = msg.copied
		m.current++

		if m.current >= len(m.tasks) {
			// copy is done, release context's resources
			m.cancel()

			m.phase = phaseScanning
			return m, tea.Batch(scanCmd(m.src, m.dst), m.spin.Tick)
		}

		if m.phase == phaseCopying {
			m.tasks[m.current].Progress = new(atomic.Int64)
			return m, copyCmd(m.ctx, m.tasks[m.current])
		}

		if m.phase == phaseStopping {
			m.phase = phaseScanned
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
		m.spin.View() + " " + warningStyle.Render(
			fmt.Sprintf("%d%% %s", int(fraction*100), crumbs.Truncate(task.Dst, 40)),
		) + "\n",
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
		var fraction float64
		if m.bytes > 0 {
			fraction = float64(m.bytes-bytes) / float64(m.bytes)
		} else {
			fraction = float64(m.files-files) / float64(m.files)
		}

		size := humanize.Bytes(uint64(bytes))

		s.WriteString(m.bar.ViewAs(fraction) + "\n")
		s.WriteString(normalStyle.Render(fmt.Sprintf("%d (%s) files to sync.", files, size)) + "\n")
	} else {
		s.WriteString(normalStyle.Render("Nothing to sync.\n"))
	}

	return s.String()
}

func (m model) View() tea.View {
	var s strings.Builder
	s.WriteString(renderHeader(m.dst))
	s.WriteString("\n\n")
	switch m.phase {
	case phaseSweeping:
		s.WriteString(m.spin.View() + " " + warningStyle.Render("Removing orphaned tempfiles...") + "\n")

	case phaseScanning:
		s.WriteString(m.spin.View() + " " + warningStyle.Render("Scanning...") + "\n")

	case phaseScanned:
		s.WriteString(m.BackupView())

	case phaseCopying:
		s.WriteString(m.BackupView() + "\n")
		s.WriteString(m.PhaseView(m.tasks[m.current]) + "\n")

	case phaseStopping:
		s.WriteString(m.BackupView() + "\n")
		s.WriteString(m.spin.View() + " " + warningStyle.Render("Stopping...") + "\n")

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
