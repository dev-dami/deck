package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/internal/message"
)

// mp4 is the start of an MP4 file: an ftyp box of brand isom.
var mp4 = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41")

// TestAttachMediaLabelsAndVideo covers what a message's images and videos
// become: labelled images, a video kept in the session's media folder
// under Deck's data dir (its part keeps no bytes), and a file that only
// claims to be a video refused.
func TestAttachMediaLabelsAndVideo(t *testing.T) {
	w := newPickerWorkspace(t, pickerCfg())
	data := t.TempDir()
	w.path = filepath.Join(data, "deck-threads.json")
	ctx := context.Background()

	images, parts, err := w.attachMedia(ctx, "s1", []message.Attachment{
		{FileName: "notes.txt", MimeType: "text/plain", Content: []byte("skipped: sent inline")},
		{FileName: "shot.png", MimeType: "image/png", Content: []byte("PNG"), Label: " login  page error "},
		{FileName: "crash.mp4", MimeType: "video/mp4", Content: mp4, Label: "the crash"},
	})
	require.NoError(t, err)
	require.Len(t, images, 2)
	require.Len(t, parts, 2)
	require.Equal(t, "login page error", images[0].Label)
	require.Equal(t, "image/png", images[0].MediaType)
	require.Equal(t, "login page error", parts[0].(message.BinaryContent).Label)

	v := images[1]
	require.True(t, v.IsVideo())
	require.Equal(t, "the crash", v.Label)
	require.Equal(t, filepath.Join(data, "media", "s1"), filepath.Dir(v.Path), "kept under the data dir, never the project")
	b, err := os.ReadFile(v.Path)
	require.NoError(t, err)
	require.Equal(t, mp4, b)
	vp := parts[1].(message.BinaryContent)
	require.Equal(t, "video/mp4", vp.MIMEType)
	require.Empty(t, vp.Data, "the message keeps no video bytes")
	require.Equal(t, "the crash", vp.Label)

	_, _, err = w.attachMedia(ctx, "s1", []message.Attachment{{FileName: "evil.mp4", MimeType: "video/mp4", Content: []byte("#!/bin/sh\n")}})
	require.ErrorContains(t, err, "not an MP4")

	// Deleting the session's media leaves nothing behind.
	require.NoError(t, os.RemoveAll(w.mediaDir("s1")))
}

// TestAttachMediaFrames covers a video sent as frames: labelled stills
// taken with ffmpeg, the video itself not kept.
func TestAttachMediaFrames(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	src := filepath.Join(t.TempDir(), "clip.webm")
	gen := exec.Command(ff, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10", "-c:v", "libvpx", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	w := newPickerWorkspace(t, pickerCfg())
	w.path = filepath.Join(t.TempDir(), "deck-threads.json")
	images, parts, err := w.attachMedia(context.Background(), "s1", []message.Attachment{
		{FilePath: src, FileName: "clip.webm", MimeType: "video/webm", Frames: true},
	})
	require.NoError(t, err)
	require.Len(t, images, 2)
	require.Equal(t, "Video 1 · 0:00", images[0].Label)
	require.Equal(t, "Video 1 · 0:01", images[1].Label)
	for i, im := range images {
		require.Equal(t, "image/jpeg", im.MediaType)
		p := parts[i].(message.BinaryContent)
		require.NotEmpty(t, p.Data)
		require.Equal(t, im.Label, p.Label)
	}
	entries, err := os.ReadDir(w.mediaDir("s1"))
	require.NoError(t, err)
	for _, e := range entries {
		require.True(t, strings.HasSuffix(e.Name(), ".jpg"), "only the frames are kept: %s", e.Name())
	}
	_, err = os.Stat(src)
	require.NoError(t, err, "the person's own file is left alone")
}
