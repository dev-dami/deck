// Package media checks and keeps the videos people attach to a message, and
// turns a video into still frames for a model that cannot watch one.
//
// A video is recognised by its bytes, never by its name alone: an MP4 or
// QuickTime file starts with an ftyp box, a WebM file with an EBML header
// whose doctype is webm. Deck keeps each video under its data directory
// (Dir), one folder per session, removed with the session.
//
// Frames come from ffmpeg when it is on PATH (FFmpeg). Deck never needs it
// otherwise: without it a video can still go to a model that takes video,
// and the clients say that sending frames needs ffmpeg.
package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Size limits of a video. MaxVideoBytes is what a client on the same
// machine as the engine (the desktop, the terminal) may attach; the phone,
// which uploads in chunks over the remote connection, may attach up to
// MaxRemoteVideoBytes.
const (
	MaxVideoBytes       = 50 << 20
	MaxRemoteVideoBytes = 25 << 20
)

// Frame defaults: at most MaxFrames frames, one per second of video,
// scaled so that neither side is over MaxFrameSide pixels.
const (
	MaxFrames    = 8
	MaxFrameSide = 1280
)

// ErrNoFFmpeg is Frames without ffmpeg on PATH.
var ErrNoFFmpeg = errors.New("sending a video as frames needs ffmpeg, which was not found on PATH")

// Types are the video MIME types Deck accepts, by extension.
var Types = map[string]string{".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime"}

// Ext is the file extension Deck stores a video of the MIME type under.
func Ext(mime string) string {
	switch mime {
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	}
	return ".mp4"
}

// IsVideoType reports whether mime is a video type Deck accepts.
func IsVideoType(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "video/mp4", "video/webm", "video/quicktime":
		return true
	}
	return false
}

// Sniff names the video type of a file from its first bytes (at least 64
// of them help): "video/mp4", "video/quicktime" or "video/webm", or "" when
// they are none of those.
func Sniff(head []byte) string {
	// ISO base media (MP4, QuickTime): a box of type ftyp first, whose
	// major brand tells QuickTime ("qt  ") from the MP4 family.
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if string(head[8:12]) == "qt  " {
			return "video/quicktime"
		}
		return "video/mp4"
	}
	// Older QuickTime files can open with another top-level atom.
	if len(head) >= 8 {
		switch string(head[4:8]) {
		case "moov", "mdat", "wide", "free", "skip", "pnot":
			return "video/quicktime"
		}
	}
	// WebM: an EBML header that names the webm doctype.
	if len(head) >= 4 && bytes.Equal(head[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		n := min(len(head), 64)
		if bytes.Contains(head[:n], []byte("webm")) {
			return "video/webm"
		}
	}
	return ""
}

// Check validates a video file: a regular file of 1 to max bytes whose
// content is a video type Deck accepts. It returns that type; a declared
// type is not trusted, the bytes decide.
func Check(path string, max int64) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("video: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("video %s: not a regular file", filepath.Base(path))
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("video %s: empty file", filepath.Base(path))
	}
	if fi.Size() > max {
		return "", fmt.Errorf("video %s: %s is over the %s limit", filepath.Base(path), Size(fi.Size()), Size(max))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("video: %w", err)
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	mime := Sniff(head[:n])
	if mime == "" {
		return "", fmt.Errorf("video %s: not an MP4, WebM or QuickTime video", filepath.Base(path))
	}
	return mime, nil
}

// CheckBytes is Check for a video held in memory.
func CheckBytes(name string, data []byte, max int64) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("video %s: empty", name)
	}
	if int64(len(data)) > max {
		return "", fmt.Errorf("video %s: %s is over the %s limit", name, Size(int64(len(data))), Size(max))
	}
	mime := Sniff(data[:min(len(data), 64)])
	if mime == "" {
		return "", fmt.Errorf("video %s: not an MP4, WebM or QuickTime video", name)
	}
	return mime, nil
}

// Size is n bytes for a person: "25 MB".
func Size(n int64) string {
	if n >= 1<<20 {
		return strconv.FormatInt((n+(1<<19))>>20, 10) + " MB"
	}
	return strconv.FormatInt((n+512)>>10, 10) + " KB"
}

// Dir is where Deck keeps the media attached in a session: a folder of its
// own under Deck's data directory (the project's .deck, which git ignores),
// so agents can open it with their file tools but it is never part of the
// project. It is "" for an id that names no folder.
func Dir(dataDir, sessionID string) string {
	id := filepath.Base(sessionID)
	if dataDir == "" || sessionID == "" || id == "." || id == ".." || id == string(filepath.Separator) {
		return ""
	}
	return filepath.Join(dataDir, "media", id)
}

// Remove deletes a session's media folder, with everything in it.
func Remove(dataDir, sessionID string) error {
	dir := Dir(dataDir, sessionID)
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

// Store puts a video into dir under a fresh name with the extension of its
// type: from src when it is set, else from data. It returns the new path.
func Store(dir, src string, data []byte, mime string) (string, error) {
	if dir == "" {
		return "", errors.New("video: no folder to keep it in")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, newName()+Ext(mime))
	if src == "" {
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return "", err
		}
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

func newName() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// ffmpeg is looked up once; tests replace lookFFmpeg and clear ffLooked.
var (
	lookFFmpeg = func() (string, error) { return exec.LookPath("ffmpeg") }
	ffMu       sync.Mutex
	ffLooked   bool
	ffPath     string
)

// FFmpeg is the ffmpeg on PATH, or "" when there is none. It is looked up
// once per process.
func FFmpeg() string {
	ffMu.Lock()
	defer ffMu.Unlock()
	if !ffLooked {
		ffLooked = true
		if p, err := lookFFmpeg(); err == nil {
			ffPath = p
		}
	}
	return ffPath
}

// Frame is one still taken from a video: a JPEG file and where in the video
// it was taken.
type Frame struct {
	Path string
	At   time.Duration
}

// Label is the frame's label: the video's, then the time ("Video 1 · 0:03").
func (f Frame) Label(video string) string { return video + " · " + Clock(f.At) }

// Clock is a time into a video as m:ss, or h:mm:ss from an hour.
func Clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// FrameTimes are n evenly spaced times in a video of length d: the middle
// of each of n equal spans, so none is the black first or last frame.
func FrameTimes(d time.Duration, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = d * time.Duration(2*i+1) / time.Duration(2*n)
	}
	return out
}

// FrameCount is how many frames a video of length d gives: one per second,
// at least one and at most MaxFrames.
func FrameCount(d time.Duration) int {
	return max(1, min(MaxFrames, int((d+time.Second/2)/time.Second)))
}

var durationRe = regexp.MustCompile(`Duration: (\d+):(\d{2}):(\d{2}(?:\.\d+)?)`)

// duration reads a video's length from what `ffmpeg -i` prints.
func duration(ctx context.Context, ff, path string) (time.Duration, error) {
	out, _ := exec.CommandContext(ctx, ff, "-hide_banner", "-nostdin", "-i", path).CombinedOutput() // exits 1: no output named
	m := durationRe.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("video %s: ffmpeg could not read its length", filepath.Base(path))
	}
	h, _ := strconv.Atoi(string(m[1]))
	mi, _ := strconv.Atoi(string(m[2]))
	s, _ := strconv.ParseFloat(string(m[3]), 64)
	return time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(s*float64(time.Second)), nil
}

// Frames takes up to MaxFrames evenly spaced frames from the video with
// ffmpeg, each a JPEG in dir no larger than MaxFrameSide on either side,
// in order. It fails with ErrNoFFmpeg when ffmpeg is not on PATH.
func Frames(ctx context.Context, video, dir string) ([]Frame, error) {
	ff := FFmpeg()
	if ff == "" {
		return nil, ErrNoFFmpeg
	}
	d, err := duration(ctx, ff, video)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	base := newName()
	scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", MaxFrameSide, MaxFrameSide)
	var out []Frame
	for i, at := range FrameTimes(d, FrameCount(d)) {
		p := filepath.Join(dir, fmt.Sprintf("%s-frame%d.jpg", base, i+1))
		cmd := exec.CommandContext(ctx, ff, "-hide_banner", "-nostdin", "-v", "error",
			"-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), "-i", video,
			"-frames:v", "1", "-vf", scale, "-q:v", "3", "-y", p)
		if msg, err := cmd.CombinedOutput(); err != nil {
			for _, f := range out {
				os.Remove(f.Path)
			}
			return nil, fmt.Errorf("video %s: ffmpeg: %v %s", filepath.Base(video), err, strings.TrimSpace(string(msg)))
		}
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			continue // past the last frame of a variable-rate video
		}
		out = append(out, Frame{Path: p, At: at})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("video %s: ffmpeg took no frames", filepath.Base(video))
	}
	return out, nil
}
