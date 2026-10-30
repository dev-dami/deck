package workspace

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	deckagent "github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/media"
	"github.com/dev-dami/deck/internal/message"
)

// dataDir is Deck's data directory (the project's .deck), where the
// thread map lives; "" when the workspace keeps nothing on disk.
func (w *DeckWorkspace) dataDir() string {
	if w.path == "" {
		return ""
	}
	return filepath.Dir(w.path)
}

// mediaDir is where the session's videos and their frames are kept.
func (w *DeckWorkspace) mediaDir(sessionID string) string {
	return media.Dir(w.dataDir(), sessionID)
}

// attachMedia turns a message's images and videos into what the agents
// are sent and the parts the message keeps, in the order they were
// attached. An image goes as it is, with its label. A video is checked
// by its bytes and kept in the session's media folder: the agents get its
// path, the message a part without the bytes. A video sent as frames
// (Attachment.Frames) becomes up to media.MaxFrames JPEG stills taken with
// ffmpeg, each labelled with the video's label and its time ("Video 1 ·
// 0:03"), and the video itself is not kept. Text attachments are skipped:
// the caller sends them inline.
func (w *DeckWorkspace) attachMedia(ctx context.Context, sessionID string, attachments []message.Attachment) ([]deckagent.Image, []message.ContentPart, error) {
	var images []deckagent.Image
	var parts []message.ContentPart
	videos := 0
	for _, a := range attachments {
		label := deckagent.CleanLabel(a.Label)
		switch {
		case a.IsImage():
			p := a.FilePath
			if p == "" {
				f, err := os.CreateTemp("", "deck-*"+filepath.Ext(a.FileName))
				if err != nil {
					continue
				}
				_, _ = f.Write(a.Content)
				f.Close()
				p = f.Name()
			}
			images = append(images, deckagent.Image{Path: p, MediaType: a.MimeType, Label: label})
			parts = append(parts, message.BinaryContent{Path: a.FileName, MIMEType: a.MimeType, Data: a.Content, Label: label})
		case a.IsVideo():
			videos++
			var mime string
			var err error
			if a.FilePath != "" {
				mime, err = media.Check(a.FilePath, media.MaxVideoBytes)
			} else {
				mime, err = media.CheckBytes(cmp.Or(a.FileName, "video"), a.Content, media.MaxVideoBytes)
			}
			if err != nil {
				return nil, nil, err
			}
			dir := w.mediaDir(sessionID)
			stored, err := media.Store(dir, a.FilePath, a.Content, mime)
			if err != nil {
				return nil, nil, fmt.Errorf("video: %w", err)
			}
			if !a.Frames {
				images = append(images, deckagent.Image{Path: stored, MediaType: mime, Label: label})
				parts = append(parts, message.BinaryContent{Path: a.FileName, MIMEType: mime, Label: label})
				continue
			}
			frames, err := media.Frames(ctx, stored, dir)
			_ = os.Remove(stored)
			if err != nil {
				return nil, nil, err
			}
			name := cmp.Or(label, fmt.Sprintf("Video %d", videos))
			base := strings.TrimSuffix(cmp.Or(a.FileName, "video"), filepath.Ext(a.FileName))
			for _, f := range frames {
				data, err := os.ReadFile(f.Path)
				if err != nil {
					return nil, nil, fmt.Errorf("video: %w", err)
				}
				fl := f.Label(name)
				images = append(images, deckagent.Image{Path: f.Path, MediaType: "image/jpeg", Label: fl})
				file := base + "-" + strings.ReplaceAll(media.Clock(f.At), ":", ".") + ".jpg"
				parts = append(parts, message.BinaryContent{Path: file, MIMEType: "image/jpeg", Data: data, Label: fl})
			}
		}
	}
	return images, parts, nil
}
