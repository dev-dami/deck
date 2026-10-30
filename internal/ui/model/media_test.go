package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/workspace"
)

// A 1×1 PNG, and the start of an MP4 file.
var (
	tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	mp4Head = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41")
)

// TestAttachedImagesGoWithTheirLabels covers images attached by path: each
// lands labelled (its file name; a clipboard paste is "Screenshot N"), and
// the message carries the labels to the engine, which puts them in the
// agent's prompt next to each image.
func TestAttachedImagesGoWithTheirLabels(t *testing.T) {
	t.Parallel()
	u, ws := newLeadUI(t, "s1")
	dir := t.TempDir()
	shot := filepath.Join(dir, "login error.png")
	require.NoError(t, os.WriteFile(shot, tinyPNG, 0o600))

	att, ok := u.handleFilePathPaste(shot)().(message.Attachment)
	require.True(t, ok)
	u.Update(att)
	u.Update(message.Attachment{FilePath: "paste_1.png", FileName: "paste_1.png", MimeType: "image/png", Content: tinyPNG})
	list := u.attachments.List()
	require.Len(t, list, 2)
	require.Equal(t, "login error", list[0].Label)
	require.Equal(t, "Screenshot 1", list[1].Label)

	batch, ok := u.sendMessageInternal("compare Image 1 and Image 2", false, list...)().(tea.BatchMsg)
	require.True(t, ok)
	batch[len(batch)-1]()
	require.Equal(t, []string{"compare Image 1 and Image 2"}, ws.runPrompts)
	require.Len(t, ws.runAttachments, 1)
	var labels []string
	for _, a := range ws.runAttachments[0] {
		labels = append(labels, a.Label)
	}
	require.Equal(t, []string{"login error", "Screenshot 1"}, labels)
}

// TestAttachAVideoByPath covers a video pasted as a path: checked by its
// bytes and sent from where it is; to a lead that cannot watch video it
// goes as frames where ffmpeg is installed.
func TestAttachAVideoByPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clip := filepath.Join(dir, "crash.mp4")
	require.NoError(t, os.WriteFile(clip, mp4Head, 0o600))
	fake := filepath.Join(dir, "fake.mp4")
	require.NoError(t, os.WriteFile(fake, []byte("not a video"), 0o600))

	watcher := workspace.Lead{Kind: "opencode", Model: "google/gemini-3-pro", Video: true}
	att, ok := videoAttachment(clip, watcher)().(message.Attachment)
	require.True(t, ok)
	require.Equal(t, message.Attachment{FilePath: clip, FileName: "crash.mp4", MimeType: "video/mp4"}, att)

	_, refused := videoAttachment(fake, watcher)().(util.InfoMsg)
	require.True(t, refused, "the bytes decide, not the name")

	blind := workspace.Lead{Kind: "claude", Slug: "claude-opus-5-5"}
	msg := videoAttachment(clip, blind)()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		require.Contains(t, msg.(util.InfoMsg).Msg, "claude-opus-5-5 can't watch video")
		return
	}
	att, ok = msg.(message.Attachment)
	require.True(t, ok)
	require.True(t, att.Frames, "a lead that cannot watch video gets frames")
}
