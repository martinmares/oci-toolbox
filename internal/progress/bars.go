package progress

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

const barWidth = 50

var (
	styleSrcLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))  // cyan-blue
	styleDstLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")) // pink
	styleBytes    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))            // grey
	styleDone     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))  // green
	styleError    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")) // red
	styleStatus   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // amber
)

// updateMsg carries a progress update for one lane (0=src, 1=dst).
type updateMsg struct {
	lane     int
	complete int64
	total    int64
	err      error
}

type doneMsg struct{ lane int }
type statusMsg struct{ text string }

// model is the bubbletea model — must be value-copyable (no mutexes).
type model struct {
	bars     [2]progress.Model
	complete [2]int64
	total    [2]int64
	percent  [2]float64
	done     [2]bool
	errs     [2]error
	labels   [2]string
	status   string
	numBars  int // 1 = only src bar, 2 = src + dst bars
}

func newModel(srcLabel, dstLabel string, numBars int) model {
	srcBar := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(barWidth),
		progress.WithoutPercentage(),
	)
	dstBar := progress.New(
		progress.WithGradient("#FF5FA2", "#ED8EFF"),
		progress.WithWidth(barWidth),
		progress.WithoutPercentage(),
	)
	return model{
		bars:    [2]progress.Model{srcBar, dstBar},
		labels:  [2]string{srcLabel, dstLabel},
		numBars: numBars,
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case updateMsg:
		m.complete[msg.lane] = msg.complete
		m.total[msg.lane] = msg.total
		if msg.err != nil {
			m.errs[msg.lane] = msg.err
		}
		var pct float64
		if msg.total > 0 {
			pct = float64(msg.complete) / float64(msg.total)
		}
		m.percent[msg.lane] = pct
		return m, nil

	case doneMsg:
		m.done[msg.lane] = true
		m.complete[msg.lane] = m.total[msg.lane]
		m.percent[msg.lane] = 1.0
		allDone := m.done[0] && (m.numBars < 2 || m.done[1])
		if allDone {
			return m, tea.Quit
		}
		return m, nil

	case statusMsg:
		m.status = msg.text
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var sb strings.Builder
	for i, label := range m.labels[:m.numBars] {
		labelStyle := styleSrcLabel
		if i == 1 {
			labelStyle = styleDstLabel
		}

		sb.WriteString(labelStyle.Render(fmt.Sprintf("%-12s", label)))
		sb.WriteString(" ")

		switch {
		case m.errs[i] != nil:
			sb.WriteString(styleError.Render("ERROR: " + m.errs[i].Error()))
		case m.done[i]:
			sb.WriteString(m.bars[i].ViewAs(1.0))
			sb.WriteString("  ")
			sb.WriteString(styleDone.Render("✓ " + fmtBytes(m.complete[i])))
		default:
			sb.WriteString(m.bars[i].ViewAs(m.percent[i]))
			sb.WriteString("  ")
			sb.WriteString(styleBytes.Render(fmtBytes(m.complete[i]) + " / " + fmtBytes(m.total[i])))
		}
		sb.WriteString("\n")
	}
	if m.status != "" {
		sb.WriteString(styleStatus.Render(m.status))
		sb.WriteString("\n")
	}
	return sb.String()
}

// UI manages the progress display.
type UI struct {
	program *tea.Program
}

// New creates a two-bar progress UI (pull + push).
func New(srcLabel, dstLabel string) *UI {
	m := newModel(srcLabel, dstLabel, 2)
	p := tea.NewProgram(m)
	return &UI{program: p}
}

// NewSingle creates a single-bar progress UI (pull only).
func NewSingle(srcLabel string) *UI {
	m := newModel(srcLabel, "", 1)
	p := tea.NewProgram(m)
	return &UI{program: p}
}

// Run starts the TUI event loop in a background goroutine.
func (u *UI) Run() {
	go func() { u.program.Run() }() //nolint:errcheck
}

// SrcChannel returns a channel for source progress updates (close when done).
func (u *UI) SrcChannel() chan<- v1.Update {
	return u.makeChannel(0)
}

// DstChannel returns a channel for destination progress updates (close when done).
func (u *UI) DstChannel() chan<- v1.Update {
	return u.makeChannel(1)
}

func (u *UI) makeChannel(lane int) chan<- v1.Update {
	ch := make(chan v1.Update, 128)
	go func() {
		for upd := range ch {
			if upd.Error != nil {
				if upd.Error == io.EOF {
					continue
				}
				u.program.Send(updateMsg{lane: lane, err: upd.Error})
				continue
			}
			u.program.Send(updateMsg{
				lane:     lane,
				complete: upd.Complete,
				total:    upd.Total,
			})
		}
		u.program.Send(doneMsg{lane: lane})
	}()
	return ch
}

// Wait blocks until both bars are finished and the TUI exits.
func (u *UI) Wait() {
	u.program.Wait()
}

// SetStatus updates the status line rendered below the progress bars.
func (u *UI) SetStatus(text string) {
	u.program.Send(statusMsg{text: text})
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
