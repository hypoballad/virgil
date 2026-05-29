package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestInputWidthCalculationIsConsistent(t *testing.T) {
	m := newInputTestModel()
	m.width = 120
	m.setInputSize(m.width)

	// frameWidth (2) + markerWidth (3) = 5
	expectedInputWidth := 115
	if got := m.input.Width(); got != expectedInputWidth {
		t.Fatalf("expected textarea width %d, got %d", expectedInputWidth, got)
	}

	// In view.go, inputPanelWidth is now m.width (120)
	// styleInput has padding 1 on each side, so inner width is 118.
	// marker (3) + textarea (115) = 118.
	// 118 fits in 118.

	// This test just ensures model.go stays consistent with our understanding.
}

func TestEmptyInputViewPadsPlaceholderLine(t *testing.T) {
	m := newInputTestModel()
	m.width = 100
	m.setInputSize(m.width)
	m.input.SetValue("")

	plainLines := strings.Split(ansi.Strip(m.inputView()), "\n")
	if len(plainLines) == 0 {
		t.Fatal("expected input view to contain at least one line")
	}
	firstLine := plainLines[0]
	if got, want := lipgloss.Width(firstLine), m.input.Width(); got != want {
		t.Fatalf("expected empty placeholder line width %d, got %d. line=%q", want, got, firstLine)
	}
	if !strings.HasPrefix(firstLine, "Message...") {
		t.Fatalf("expected placeholder at start of line, got %q", firstLine)
	}
	if !strings.HasSuffix(firstLine, " ") {
		t.Fatalf("expected placeholder line to be padded with trailing spaces, got %q", firstLine)
	}
}

func TestPasteNormalization(t *testing.T) {
	m := newInputTestModel()

	// Simulate paste with CRLF
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("line1\r\nline2"),
	}

	updated, _ := m.Update(pasteMsg)
	next := updated.(Model)

	val := next.input.Value()
	if strings.Contains(val, "\r") {
		t.Fatalf("value contains carriage return after paste: %q", val)
	}
	if !strings.Contains(val, "line1\nline2") {
		t.Fatalf("value does not contain expected newline: %q", val)
	}
}

func TestInputViewWidth(t *testing.T) {
	m := newInputTestModel()
	m.width = 100
	m.setInputSize(m.width)

	view := m.View()
	// The input part is at the end of the View result.
	// We want to make sure the last lines (input area) have the full width.
	lines := strings.Split(view, "\n")

	// The input line should be one of the last few lines.
	// Look for the line containing the marker.
	found := false
	for _, line := range lines {
		if strings.Contains(line, inputMarker) {
			found = true
			// Check the visual width of the line.
			// It should be equal to m.width (100).
			width := lipgloss.Width(line)
			if width != 100 {
				t.Fatalf("expected input line width 100, got %d. Line: %q", width, line)
			}
		}
	}
	if !found {
		t.Fatal("could not find input line in view")
	}
}
