package progress

import (
	"strings"
	"testing"
)

func TestSnapshotUsesStatusVerbatim(t *testing.T) {
	ui := newUI("pull: src", "push: dst", 2)
	ui.status = "phase: pushing destination image"

	lines := ui.snapshotLinesLocked()
	if got := lines[len(lines)-1]; got != ui.status {
		t.Fatalf("status line = %q, want %q", got, ui.status)
	}
	if strings.Count(lines[len(lines)-1], "phase:") != 1 {
		t.Fatalf("status line should contain one phase prefix: %q", lines[len(lines)-1])
	}
}

func TestLabelWidthIsBounded(t *testing.T) {
	ui := newUI("pull: short", "push: very-long-registry.example.com/team/application/component:2026.06.01", 2)

	if got := ui.labelWidth(); got != maxLabelWidth {
		t.Fatalf("labelWidth = %d, want %d", got, maxLabelWidth)
	}
}

func TestFormatLaneIncludesProgressbarTiming(t *testing.T) {
	ui := newUI("pull: src", "push: dst", 2)
	ui.update(updateMsg{lane: 0, complete: 20, total: 100})

	line := ui.formatLane(0)
	for _, want := range []string{"pull: src", "20%", "[", ":"} {
		if !strings.Contains(line, want) {
			t.Fatalf("progress line = %q, want substring %q", line, want)
		}
	}
}

func TestFormatLaneShowsNoBytesTransferredForEmptyCompletedLane(t *testing.T) {
	ui := newUI("pull: src", "push: dst", 2)
	ui.finishLane(1)

	line := ui.formatLane(1)
	if strings.Contains(line, "0%") {
		t.Fatalf("progress line = %q, should not show stale 0%% bar", line)
	}
	if !strings.Contains(line, "push: dst") || !strings.Contains(line, "no bytes transferred") {
		t.Fatalf("progress line = %q, want no-bytes-transferred message", line)
	}
}
