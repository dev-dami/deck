package claude

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// maxImageBytes is the largest image file accepted (the API's per-image limit).
const maxImageBytes = 5 << 20

// contentPart is one block of a stream-json user message: text, or an image
// {"type":"image","source":{"type":"base64","media_type":...,"data":...}}.
type contentPart struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// normalizeMediaType lower-cases a media type, drops parameters and maps the
// common "image/jpg" misspelling.
func normalizeMediaType(mt string) string {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if mt == "image/jpg" {
		mt = "image/jpeg"
	}
	return mt
}

// loadImage reads img.Path and returns the image block for it. The media type
// is detected from the content (http.DetectContentType); a declared
// Image.MediaType must be one of png/jpeg/gif/webp, and if the content is a
// different supported type the content wins, since the API rejects a block
// whose bytes do not match its media type. Files over 5 MB are refused.
func loadImage(img agent.Image) (contentPart, error) {
	if img.Path == "" {
		return contentPart{}, errors.New("image has no path")
	}
	declared := normalizeMediaType(img.MediaType)
	if declared != "" && !imageTypes[declared] {
		return contentPart{}, fmt.Errorf("image %s: unsupported media type %q (want png, jpeg, gif or webp)", img.Path, img.MediaType)
	}
	fi, err := os.Stat(img.Path)
	if err != nil {
		return contentPart{}, fmt.Errorf("image: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return contentPart{}, fmt.Errorf("image %s: not a regular file", img.Path)
	}
	if fi.Size() > maxImageBytes {
		return contentPart{}, fmt.Errorf("image %s: %d bytes exceeds the 5 MB limit", img.Path, fi.Size())
	}
	data, err := os.ReadFile(img.Path)
	if err != nil {
		return contentPart{}, fmt.Errorf("image: %w", err)
	}
	if len(data) > maxImageBytes {
		return contentPart{}, fmt.Errorf("image %s: %d bytes exceeds the 5 MB limit", img.Path, len(data))
	}
	if len(data) == 0 {
		return contentPart{}, fmt.Errorf("image %s: empty file", img.Path)
	}
	sniffed := normalizeMediaType(http.DetectContentType(data))
	if !imageTypes[sniffed] {
		return contentPart{}, fmt.Errorf("image %s: content is %s, not png, jpeg, gif or webp", img.Path, sniffed)
	}
	// declared (if any) is supported; the sniffed type is what the bytes are.
	return contentPart{Type: "image", Source: &imageSource{
		Type: "base64", MediaType: sniffed, Data: base64.StdEncoding.EncodeToString(data),
	}}, nil
}

// buildUserMessage builds the stream-json user message: image blocks first,
// then the text. It returns the wire form and an echo form (image data elided)
// that is safe to keep as Event.Raw. An empty prompt with images sends the
// images alone (the API refuses empty text blocks).
func buildUserMessage(prompt string, images []agent.Image) (wire, echo []byte, err error) {
	parts := make([]contentPart, 0, len(images)+1)
	elided := make([]contentPart, 0, len(images)+1)
	for _, img := range images {
		p, err := loadImage(img)
		if err != nil {
			return nil, nil, err
		}
		parts = append(parts, p)
		e := p
		src := *p.Source
		src.Data = fmt.Sprintf("<%d base64 bytes elided>", len(src.Data))
		e.Source = &src
		elided = append(elided, e)
	}
	if prompt != "" || len(images) == 0 {
		t := contentPart{Type: "text", Text: prompt}
		parts = append(parts, t)
		elided = append(elided, t)
	}
	wire, err = marshalUser(parts)
	if err != nil {
		return nil, nil, err
	}
	echo, err = marshalUser(elided)
	return wire, echo, err
}

type userMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string        `json:"role"`
		Content []contentPart `json:"content"`
	} `json:"message"`
}

func marshalUser(parts []contentPart) ([]byte, error) {
	var um userMessage
	um.Type = "user"
	um.Message.Role = "user"
	um.Message.Content = parts
	return jsonMarshal(um)
}
