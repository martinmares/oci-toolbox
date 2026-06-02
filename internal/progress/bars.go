package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

const (
	defaultBarWidth = 24
	minBarWidth     = 10
	minLabelWidth   = 12
	maxLabelWidth   = 42
)

// updateMsg carries a progress update for one lane (0=src, 1=dst).
type updateMsg struct {
	lane     int
	complete int64
	total    int64
	err      error
}

type laneState struct {
	complete int64
	total    int64
	percent  float64
	done     bool
	err      error
}

// UI manages the progress display.
type UI struct {
	mu          sync.Mutex
	out         *os.File
	interactive bool
	labels      [2]string
	lane        [2]laneState
	bars        [2]*progressbar.ProgressBar
	status      string
	numBars     int
	rendered    int
	closed      bool
	lastStatus  string
	wg          sync.WaitGroup
}

// New creates a two-bar progress UI (pull + push).
func New(srcLabel, dstLabel string) *UI {
	return newUI(srcLabel, dstLabel, 2)
}

// NewSingle creates a single-bar progress UI (pull only).
func NewSingle(srcLabel string) *UI {
	return newUI(srcLabel, "", 1)
}

func newUI(srcLabel, dstLabel string, numBars int) *UI {
	out := os.Stderr
	interactive := false
	if out != nil {
		interactive = isatty.IsTerminal(out.Fd()) || isatty.IsCygwinTerminal(out.Fd())
	}
	return &UI{
		out:         out,
		interactive: interactive,
		labels:      [2]string{srcLabel, dstLabel},
		bars:        [2]*progressbar.ProgressBar{newLaneBar(srcLabel), newLaneBar(dstLabel)},
		numBars:     numBars,
	}
}

func newLaneBar(label string) *progressbar.ProgressBar {
	bar := progressbar.NewOptions64(
		1,
		progressbar.OptionSetDescription(label),
		progressbar.OptionSetWriter(io.Discard),
		progressbar.OptionSetWidth(defaultBarWidth),
		progressbar.OptionSetTheme(progressbar.ThemeASCII),
		progressbar.OptionShowBytes(true),
		progressbar.OptionShowTotalBytes(true),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetRenderBlankState(true),
	)
	_ = bar.RenderBlank()
	return bar
}

// Run is kept for API compatibility; rendering happens on updates.
func (u *UI) Run() {}

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
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		for upd := range ch {
			if upd.Error != nil {
				if upd.Error == io.EOF {
					continue
				}
				u.update(updateMsg{lane: lane, err: upd.Error})
				continue
			}
			u.update(updateMsg{
				lane:     lane,
				complete: upd.Complete,
				total:    upd.Total,
			})
		}
		u.finishLane(lane)
	}()
	return ch
}

func (u *UI) update(msg updateMsg) {
	u.mu.Lock()
	defer u.mu.Unlock()

	state := &u.lane[msg.lane]
	state.complete = msg.complete
	state.total = msg.total
	if msg.total > 0 {
		state.percent = float64(msg.complete) / float64(msg.total)
	} else {
		state.percent = 0
	}
	if msg.err != nil {
		state.err = msg.err
	}
	u.syncBarLocked(msg.lane)
	u.renderLocked()
}

func (u *UI) finishLane(lane int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	state := &u.lane[lane]
	state.done = true
	state.complete = state.total
	state.percent = 1.0
	u.syncBarLocked(lane)
	u.renderLocked()
}

func (u *UI) syncBarLocked(lane int) {
	bar := u.bars[lane]
	if bar == nil {
		return
	}
	state := u.lane[lane]
	if state.total > 0 && bar.GetMax64() != state.total {
		bar.ChangeMax64(state.total)
	}
	switch {
	case state.err != nil:
		_ = bar.Exit()
	case state.done && state.total > 0:
		_ = bar.Finish()
	case state.complete > 0:
		_ = bar.Set64(state.complete)
	default:
		_ = bar.RenderBlank()
	}
}

func (u *UI) renderLocked() {
	if u.out == nil || u.closed {
		return
	}

	lines := u.snapshotLinesLocked()
	if !u.interactive {
		if u.status != "" && u.status != u.lastStatus {
			fmt.Fprintln(u.out, u.status)
			u.lastStatus = u.status
		}
		return
	}

	if u.rendered > 0 {
		fmt.Fprintf(u.out, "\r\x1b[%dA", u.rendered)
	}
	for i, line := range lines {
		fmt.Fprint(u.out, "\x1b[2K\r")
		fmt.Fprint(u.out, line)
		if i < len(lines)-1 {
			fmt.Fprint(u.out, "\n")
		}
	}
	if u.rendered > len(lines) {
		fmt.Fprint(u.out, "\x1b[J")
	}
	fmt.Fprint(u.out, "\n")
	u.rendered = len(lines)
}

func (u *UI) snapshotLinesLocked() []string {
	lines := make([]string, 0, u.numBars+1)
	for i := 0; i < u.numBars; i++ {
		lines = append(lines, u.formatLane(i))
	}
	if u.status != "" {
		lines = append(lines, u.status)
	}
	return lines
}

func (u *UI) formatLane(i int) string {
	state := u.lane[i]
	label := truncateMiddle(u.labels[i], u.labelWidth())
	barText := strings.TrimSpace(u.bars[i].String())
	if barText == "" {
		barText = fmt.Sprintf("%-*s %s %s / %s", u.labelWidth(), label, renderBar(state.percent, u.barWidth()), fmtBytes(state.complete), fmtBytes(state.total))
	}

	switch {
	case state.err != nil:
		return fmt.Sprintf("%s ERROR: %s", barText, state.err.Error())
	case state.done && state.total == 0:
		return fmt.Sprintf("%s ✓ %s", barText, fmtBytes(state.complete))
	default:
		return barText
	}
}

func (u *UI) labelWidth() int {
	width := minLabelWidth
	for i := 0; i < u.numBars; i++ {
		if l := len(u.labels[i]); l > width {
			width = l
		}
	}
	if width > maxLabelWidth {
		return maxLabelWidth
	}
	return width
}

func (u *UI) barWidth() int {
	return defaultBarWidth
}

// Wait blocks until all progress channels are closed and the final snapshot is flushed.
func (u *UI) Wait() {
	u.wg.Wait()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
}

// SetStatus updates the status line rendered below the progress bars.
func (u *UI) SetStatus(text string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status = text
	u.renderLocked()
}

func renderBar(pct float64, width int) string {
	if width < minBarWidth {
		width = minBarWidth
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}

	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	if pct >= 1 {
		filled = width
	}
	var sb strings.Builder
	sb.Grow(width + 2)
	sb.WriteByte('[')
	for i := 0; i < width; i++ {
		switch {
		case i < filled-1:
			sb.WriteByte('=')
		case i == filled-1 && pct < 1:
			sb.WriteByte('>')
		default:
			sb.WriteByte(' ')
		}
	}
	sb.WriteByte(']')
	return sb.String()
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

func truncateMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	if width <= 3 {
		return s[:width-1] + "…"
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return s[:left] + "…" + s[len(s)-right:]
}

type PhaseTimer struct {
	startedAt time.Time
	current   phaseTiming
	entries   []phaseTiming
}

type phaseTiming struct {
	name     string
	started  time.Time
	duration time.Duration
}

func NewPhaseTimer() *PhaseTimer {
	now := time.Now()
	return &PhaseTimer{startedAt: now}
}

func (t *PhaseTimer) Start(name string) {
	if t == nil || name == "" {
		return
	}
	t.FinishCurrent()
	t.current = phaseTiming{name: name, started: time.Now()}
}

func (t *PhaseTimer) FinishCurrent() {
	if t == nil || t.current.name == "" {
		return
	}
	t.current.duration = time.Since(t.current.started).Round(time.Millisecond)
	t.entries = append(t.entries, t.current)
	t.current = phaseTiming{}
}

func (t *PhaseTimer) Summary() string {
	if t == nil {
		return ""
	}
	t.FinishCurrent()
	if len(t.entries) == 0 {
		return ""
	}

	var width int
	for _, entry := range t.entries {
		if len(entry.name) > width {
			width = len(entry.name)
		}
	}

	var sb strings.Builder
	sb.WriteString("\nphase timings:\n")
	for _, entry := range t.entries {
		sb.WriteString(fmt.Sprintf("  %-*s  %s\n", width, entry.name, fmtDuration(entry.duration)))
	}
	wall := time.Since(t.startedAt).Round(time.Millisecond)
	sb.WriteString(fmt.Sprintf("  %-*s  %s\n", width, "total", fmtDuration(wall)))
	return sb.String()
}

func fmtDuration(d time.Duration) string {
	switch {
	case d > 0 && d < time.Millisecond:
		return "<1ms"
	default:
		return d.String()
	}
}
