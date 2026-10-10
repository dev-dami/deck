// Package brand holds the providers' logos, as published, for the places Deck
// can draw an image (see internal/termimg). Text marks (✻, >_) live in the
// TUI's theme.
//
// The logo files come from lobe-icons 1.97.1 (MIT,
// github.com/lobehub/lobe-icons); the marks themselves are trademarks of
// Anthropic and OpenAI and are used only to say which CLI an agent runs.
package brand

import (
	"bytes"
	"embed"
	"image"
	"image/png"
	"sync"
)

//go:embed logos/*.png
var logos embed.FS

var (
	mu    sync.Mutex
	cache = map[string]image.Image{}
)

// Logo is the logo of an agent kind ("claude", "codex"), or false.
func Logo(kind string) (image.Image, bool) {
	mu.Lock()
	defer mu.Unlock()
	if img, ok := cache[kind]; ok {
		return img, img != nil
	}
	data, err := logos.ReadFile("logos/" + kind + ".png")
	var img image.Image
	if err == nil {
		img, _ = png.Decode(bytes.NewReader(data))
	}
	cache[kind] = img
	return img, img != nil
}
