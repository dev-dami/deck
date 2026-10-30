package attachments

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/message"
)

// DefaultLabel is the label an image or a video starts with, as the desktop
// and the phone give it: an image pasted from the clipboard (paste_N.png)
// is "Screenshot 1", a file its name without the extension, else "Image 1"
// or "Video 1"; the number is the first one the attached labels do not use
// yet. The agent sees the label next to the attachment.
func DefaultLabel(a message.Attachment, attached []message.Attachment) string {
	taken := make([]string, 0, len(attached))
	for _, x := range attached {
		taken = append(taken, x.Label)
	}
	stem := "Image"
	if a.IsVideo() {
		stem = "Video"
	}
	name := filepath.Base(a.FileName)
	if strings.HasPrefix(name, "paste_") {
		if a.IsImage() {
			stem = "Screenshot"
		}
	} else if base := agent.CleanLabel(strings.TrimSuffix(name, filepath.Ext(name))); base != "" && base != "." {
		return base
	}
	n := 1
	for slices.Contains(taken, fmt.Sprintf("%s %d", stem, n)) {
		n++
	}
	return fmt.Sprintf("%s %d", stem, n)
}

// chipName is what a chip shows: an image's or a video's label (a video
// sent as frames says so), else the file's name.
func chipName(a message.Attachment) string {
	if a.Label == "" {
		return filepath.Base(a.FileName)
	}
	if a.Frames {
		return a.Label + " (frames)"
	}
	return a.Label
}
