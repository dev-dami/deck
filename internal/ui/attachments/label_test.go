package attachments

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
)

// TestAttachLabels covers the label an image or a video lands with, as the
// desktop and the phone give it, and the chip that shows it.
func TestAttachLabels(t *testing.T) {
	t.Parallel()
	m := New(newTestRenderer(), Keymap{})
	for _, a := range []message.Attachment{
		{FileName: "paste_1.png", MimeType: "image/png"},
		{FileName: "paste_2.png", MimeType: "image/png"},
		{FilePath: "/tmp/shots/login error.png", FileName: "login error.png", MimeType: "image/png"},
		{FilePath: "/tmp/crash.mov", FileName: "crash.mov", MimeType: "video/mp4", Frames: true},
		{FileName: "paste_3.txt", MimeType: "text/plain"},
		{FileName: "x.png", MimeType: "image/png", Label: "kept"},
	} {
		require.True(t, m.Update(a))
	}
	var labels []string
	for _, a := range m.List() {
		labels = append(labels, a.Label)
	}
	require.Equal(t, []string{"Screenshot 1", "Screenshot 2", "login error", "crash", "", "kept"}, labels)

	out := ansi.Strip(m.Render(200))
	require.Contains(t, out, "Screenshot 1")
	require.Contains(t, out, "crash (frames)")
	require.Contains(t, out, "paste_3.txt", "a text attachment keeps its name")
}
