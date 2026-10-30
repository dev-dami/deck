package rpc

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dev-dami/deck/internal/deck/media"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// mp4 is the start of an MP4 file: an ftyp box of brand isom.
var mp4 = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41")

// TestSendLabelsAndVideo covers what Send takes besides an image's bytes:
// the label of each attachment, a video as bytes or as a local path, and a
// video to send as frames. A video is told by its bytes.
func TestSendLabelsAndVideo(t *testing.T) {
	fake, _, c, _ := rig(t)
	ctx := ctxT(t)
	png := []byte{0x89, 'P', 'N', 'G', 0, 1}
	clip := filepath.Join(t.TempDir(), "clip.mov")
	if err := os.WriteFile(clip, mp4, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Text: "compare", Images: []*deckv1.Image{
		{Name: "a.png", Mime: "image/png", Data: png, Label: "  login   page error "},
		{Name: "clip.mov", Mime: "video/quicktime", Path: clip, Label: "the crash"},
		{Name: "b.mp4", Mime: "video/mp4", Data: mp4, Label: "Video 2", Frames: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || len(sent[0].Images) != 3 {
		t.Fatalf("sent = %+v", sent)
	}
	ims := sent[0].Images
	if ims[0].Label != "login page error" || ims[0].Path != "" {
		t.Errorf("image = %+v", ims[0])
	}
	// The bytes say MP4 whatever the name and type claimed.
	if ims[1].Path != clip || ims[1].Mime != "video/mp4" || len(ims[1].Data) != 0 || ims[1].Label != "the crash" {
		t.Errorf("video by path = %+v", ims[1])
	}
	if !ims[2].Frames || ims[2].Mime != "video/mp4" || len(ims[2].Data) != len(mp4) {
		t.Errorf("video as frames = %+v", ims[2])
	}

	notVideo := filepath.Join(t.TempDir(), "x.mp4")
	_ = os.WriteFile(notVideo, []byte("#!/bin/sh\n"), 0o600)
	for name, im := range map[string]*deckv1.Image{
		"fake video bytes": {Name: "x.mp4", Mime: "video/mp4", Data: []byte("not a video")},
		"fake video file":  {Name: "x.mp4", Mime: "video/mp4", Path: notVideo},
		"relative path":    {Name: "x.mp4", Mime: "video/mp4", Path: "x.mp4"},
		"image by path":    {Name: "a.png", Mime: "image/png", Path: clip},
		"other type":       {Name: "a.mkv", Mime: "video/x-matroska", Data: mp4},
	} {
		_, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Images: []*deckv1.Image{im}})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	big := filepath.Join(t.TempDir(), "big.mp4")
	f, _ := os.Create(big)
	_, _ = f.Write(mp4)
	_ = f.Truncate(media.MaxVideoBytes + 1)
	f.Close()
	if _, err := c.Send(ctx, &deckv1.SendRequest{SessionId: "s1", Images: []*deckv1.Image{{Name: "big.mp4", Mime: "video/mp4", Path: big}}}); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("a video over the limit: %v", err)
	}
}
