package message

import (
	"slices"
	"strings"
)

type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
	// Label is what the person called an image or a video ("Image 1",
	// "login page error"); the agent sees it next to the attachment.
	Label string
	// Frames sends a video as still frames taken from it, for a model that
	// cannot watch video.
	Frames bool
}

func (a Attachment) IsText() bool     { return strings.HasPrefix(a.MimeType, "text/") }
func (a Attachment) IsImage() bool    { return strings.HasPrefix(a.MimeType, "image/") }
func (a Attachment) IsVideo() bool    { return strings.HasPrefix(a.MimeType, "video/") }
func (a Attachment) IsMarkdown() bool { return a.MimeType == "text/markdown" }

// ContainsTextAttachment returns true if any of the attachments is a text attachment.
func ContainsTextAttachment(attachments []Attachment) bool {
	return slices.ContainsFunc(attachments, func(a Attachment) bool {
		return a.IsText()
	})
}
