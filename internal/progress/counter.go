package progress

import (
	"fmt"
	"io"
	"time"

	"github.com/schollz/progressbar/v3"
)

// Counter renders progress for a known number of discrete operations.
type Counter struct {
	bar *progressbar.ProgressBar
}

func NewCounter(total int, description string, writer io.Writer) *Counter {
	if writer == nil {
		writer = io.Discard
	}
	bar := progressbar.NewOptions(
		total,
		progressbar.OptionSetDescription(truncateMiddle(description, maxLabelWidth)),
		progressbar.OptionSetWriter(writer),
		progressbar.OptionSetWidth(defaultBarWidth),
		progressbar.OptionSetTheme(progressbar.ThemeASCII),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("images"),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprintln(writer)
		}),
	)
	_ = bar.RenderBlank()
	return &Counter{bar: bar}
}

func (c *Counter) Describe(description string) {
	if c == nil {
		return
	}
	c.bar.Describe(truncateMiddle(description, maxLabelWidth))
}

func (c *Counter) Advance() error {
	if c == nil {
		return nil
	}
	return c.bar.Add(1)
}

func (c *Counter) Finish() error {
	if c == nil {
		return nil
	}
	return c.bar.Finish()
}

func (c *Counter) Exit() error {
	if c == nil || c.bar.IsFinished() {
		return nil
	}
	return c.bar.Exit()
}
