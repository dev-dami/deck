package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Screenshots reach the agent as a file it can read as an image: the MCP
// result is text. They live in a private folder of the temp directory and
// are pruned as new ones arrive.
const (
	shotKeep = 40
	shotAge  = 30 * time.Minute
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ShotDir is the folder screenshots are written to.
func ShotDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("deck-browser-%d", os.Getuid()))
}

// SaveShot writes a screenshot of agent's to dir (private to the user) and
// returns its path. mime picks the extension; dir is pruned of old shots.
func SaveShot(dir, agent string, data []byte, mime string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	prune(dir, time.Now())
	ext := ".png"
	if strings.Contains(mime, "jpeg") {
		ext = ".jpg"
	}
	name := fmt.Sprintf("%s-%d%s", unsafeName.ReplaceAllString(agent, "_"), time.Now().UnixMilli(), ext)
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return path, f.Close()
}

// prune removes shots older than shotAge and the oldest beyond shotKeep.
func prune(dir string, now time.Time) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type shot struct {
		path string
		at   time.Time
	}
	var left []shot
	for _, e := range es {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if now.Sub(info.ModTime()) > shotAge {
			os.Remove(p)
			continue
		}
		left = append(left, shot{p, info.ModTime()})
	}
	if len(left) < shotKeep {
		return
	}
	sort.Slice(left, func(i, j int) bool { return left[i].at.Before(left[j].at) })
	for _, s := range left[:len(left)-shotKeep+1] {
		os.Remove(s.path)
	}
}
