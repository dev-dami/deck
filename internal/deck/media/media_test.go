package media

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mp4Head is the start of an MP4 file: an ftyp box of brand isom.
var mp4Head = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41")

func TestSniff(t *testing.T) {
	webm := append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x42, 0x82, 0x84}, []byte("webm\x42\x87")...)
	mkv := append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x88}, []byte("matroska")...)
	for name, tc := range map[string]struct {
		head []byte
		want string
	}{
		"mp4":       {mp4Head, "video/mp4"},
		"quicktime": {[]byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00qt  "), "video/quicktime"},
		"old mov":   {[]byte("\x00\x00\x00\x08wide\x00\x00\x00\x00mdat"), "video/quicktime"},
		"webm":      {webm, "video/webm"},
		"matroska":  {mkv, ""},
		"png":       {[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), ""},
		"text":      {[]byte("this is not a video at all"), ""},
		"short":     {[]byte("ftyp"), ""},
	} {
		if got := Sniff(tc.head); got != tc.want {
			t.Errorf("%s: Sniff = %q, want %q", name, got, tc.want)
		}
	}
}

func TestCheckSniffsAndLimits(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("clip.mp4", append(append([]byte(nil), mp4Head...), make([]byte, 1000)...))
	if mime, err := Check(good, MaxVideoBytes); err != nil || mime != "video/mp4" {
		t.Errorf("Check(mp4) = %q, %v", mime, err)
	}
	// The name says video, the bytes do not: refused.
	fake := write("fake.mp4", []byte("#!/bin/sh\necho not a video\n"))
	if _, err := Check(fake, MaxVideoBytes); err == nil || !strings.Contains(err.Error(), "not an MP4") {
		t.Errorf("Check(fake) = %v", err)
	}
	// The name says nothing, the bytes say MP4: accepted.
	noExt := write("recording", append(append([]byte(nil), mp4Head...), 0))
	if mime, err := Check(noExt, MaxVideoBytes); err != nil || mime != "video/mp4" {
		t.Errorf("Check(no extension) = %q, %v", mime, err)
	}
	if _, err := Check(good, 500); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("over the limit: %v", err)
	}
	if _, err := Check(write("empty.mp4", nil), MaxVideoBytes); err == nil {
		t.Error("an empty file is refused")
	}
	if _, err := Check(dir, MaxVideoBytes); err == nil {
		t.Error("a directory is refused")
	}
	if _, err := CheckBytes("x", make([]byte, MaxRemoteVideoBytes+1), MaxRemoteVideoBytes); err == nil {
		t.Error("CheckBytes keeps the limit")
	}
	if mime, err := CheckBytes("x", mp4Head, MaxRemoteVideoBytes); err != nil || mime != "video/mp4" {
		t.Errorf("CheckBytes(mp4) = %q, %v", mime, err)
	}
}

func TestStoreKeepsVideosUnderTheDataDir(t *testing.T) {
	data := t.TempDir()
	dir := Dir(data, "../../escape")
	if dir != filepath.Join(data, "media", "escape") {
		t.Fatalf("Dir = %s", dir)
	}
	p, err := Store(dir, "", mp4Head, "video/mp4")
	if err != nil || filepath.Dir(p) != dir || filepath.Ext(p) != ".mp4" {
		t.Fatalf("Store = %s, %v", p, err)
	}
	src := filepath.Join(t.TempDir(), "in.webm")
	_ = os.WriteFile(src, []byte("webm bytes"), 0o600)
	q, err := Store(dir, src, nil, "video/webm")
	if err != nil || filepath.Ext(q) != ".webm" || q == p {
		t.Fatalf("Store(src) = %s, %v", q, err)
	}
	if b, _ := os.ReadFile(q); string(b) != "webm bytes" {
		t.Errorf("copied %q", b)
	}
	if fi, _ := os.Stat(q); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	// An id that names no folder never reaches the whole media folder.
	for _, id := range []string{"", ".", "..", "/"} {
		if d := Dir(data, id); d != "" {
			t.Errorf("Dir(%q) = %s", id, d)
		}
		if err := Remove(data, id); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(q); err != nil {
		t.Fatal("a bad id removed media")
	}
	if err := Remove(data, "../../escape"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Remove keeps the folder")
	}
}

func TestFrameTimesAndLabels(t *testing.T) {
	if n := FrameCount(3 * time.Second); n != 3 {
		t.Errorf("3s: %d frames", n)
	}
	if n := FrameCount(200 * time.Millisecond); n != 1 {
		t.Errorf("a short clip: %d frames", n)
	}
	if n := FrameCount(10 * time.Minute); n != MaxFrames {
		t.Errorf("a long video: %d frames", n)
	}
	got := FrameTimes(8*time.Second, 4)
	want := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second, 7 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FrameTimes = %v", got)
		}
	}
	if l := (Frame{At: 3 * time.Second}).Label("Video 1"); l != "Video 1 · 0:03" {
		t.Errorf("label %q", l)
	}
	if c := Clock(time.Hour + 2*time.Minute + 5*time.Second); c != "1:02:05" {
		t.Errorf("clock %q", c)
	}
}

func TestFramesWithFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	gen := exec.Command(ff, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=3:size=1920x1080:rate=10",
		"-pix_fmt", "yuv420p", "-y", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	if mime, err := Check(video, MaxVideoBytes); err != nil || mime != "video/mp4" {
		t.Fatalf("Check(real mp4) = %q, %v", mime, err)
	}
	frames, err := Frames(context.Background(), video, filepath.Join(dir, "frames"))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("frames = %+v", frames)
	}
	for i, f := range frames {
		if f.At != time.Duration(2*i+1)*time.Second/2 {
			t.Errorf("frame %d at %v", i, f.At)
		}
		r, err := os.Open(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(r)
		r.Close()
		if err != nil || cfg.Width != MaxFrameSide || cfg.Height != 720 {
			t.Errorf("frame %d: %dx%d (%v), want scaled to 1280x720", i, cfg.Width, cfg.Height, err)
		}
	}
}

func TestFramesWithoutFFmpeg(t *testing.T) {
	old := lookFFmpeg
	t.Cleanup(func() { lookFFmpeg, ffLooked, ffPath = old, false, "" })
	lookFFmpeg = func() (string, error) { return "", exec.ErrNotFound }
	ffLooked, ffPath = false, ""
	if FFmpeg() != "" {
		t.Fatal("FFmpeg found one")
	}
	if _, err := Frames(context.Background(), "x.mp4", t.TempDir()); err != ErrNoFFmpeg {
		t.Errorf("Frames = %v, want ErrNoFFmpeg", err)
	}
}
