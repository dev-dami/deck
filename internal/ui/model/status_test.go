package model

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/ui/attachments"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/stretchr/testify/require"
)

// leadingSpaces counts the spaces at the start of a stripped line.
func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// drawStatusLines draws the status bar into a screen buffer and returns the
// visible content of each row.
func drawStatusLines(t *testing.T, st *Status, w, h int) []string {
	t.Helper()
	scr := uv.NewScreenBuffer(w, h)
	st.Draw(scr, uv.Rect(0, 0, w, h))
	lines := strings.Split(ansi.Strip(scr.Render()), "\n")
	return lines
}

// TestStatusHelpClearsNoticeDot draws the status bar with unread notices
// at several widths: the hints must end at least a space before the mark.
func TestStatusHelpClearsNoticeDot(t *testing.T) {
	t.Parallel()

	for _, w := range []int{80, 120, 150, 170, 200} {
		u := newPrismTestUI()
		u.attachments = attachments.New(nil, attachments.Keymap{})
		u.status.helpKm = u
		for range 14 {
			u.notices.store.Push(notice.Notice{Title: "n"})
		}
		area := uv.Rect(0, 0, w, 1)
		scr := uv.NewScreenBuffer(w, 1)
		u.status.SetRightReserve(u.noticeDotReserve(area))
		u.status.Draw(scr, area)
		u.drawNoticeDot(scr, area)
		line := strings.TrimRight(ansi.Strip(scr.Render()), " ")
		require.True(t, strings.HasSuffix(line, "● 14 "+notice.HistoryKey), "w=%d: %q", w, line)
		dotAt := strings.Index(line, "●")
		before := strings.TrimRight(line[:dotAt], " ")
		require.NotEmpty(t, before, "w=%d: the hints are drawn", w)
		require.GreaterOrEqual(t, len(line[:dotAt])-len(before), 1, "w=%d: a space before the mark: %q", w, line)
		require.NotContains(t, before, "●", "w=%d", w)
	}
}

func TestStatusDrawExpandedHelpRowsAlignWithBadge(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	u.attachments = attachments.New(nil, attachments.Keymap{})
	st := u.status
	st.helpKm = u
	st.SetWidth(100)
	st.ToggleHelp()
	st.SetMode(uiInputModePlan, false)

	lines := drawStatusLines(t, st, 100, 6)
	require.True(t, strings.HasPrefix(lines[0], strings.Repeat(" ", badgeLeftInset)+" "+"PLAN MODE"),
		"the badge row must start with the badge inset and its padding: %q", lines[0])

	// Every subsequent help row must start at the same column as the hints
	// on the badge row: badge inset + badge width + separator + help padding.
	badge := st.modeBadge()
	wantHintsCol := badgeLeftInset + lipgloss.Width(badge) + 1 + u.com.Styles.Status.Help.GetPaddingLeft()
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		require.Equal(t, wantHintsCol, leadingSpaces(line),
			"expanded help row %d must align with the badge row hints", i+1)
	}
}

func TestStatusDrawExpandedHelpRowsAlignWithoutBadge(t *testing.T) {
	t.Parallel()

	u := newPrismTestUI()
	u.attachments = attachments.New(nil, attachments.Keymap{})
	st := u.status
	st.helpKm = u
	st.SetWidth(100)
	st.ToggleHelp()
	st.SetMode(uiInputModeCode, false)

	lines := drawStatusLines(t, st, 100, 6)
	wantCol := u.com.Styles.Status.Help.GetPaddingLeft()
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			break
		}
		require.Equal(t, wantCol, leadingSpaces(line),
			"expanded help row %d must align with the first row", i)
	}
}
