package agent

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

const (
	MaxOpImages          = 4
	MaxOpImageBytes      = 8 << 20
	MaxOpImageTotalBytes = 16 << 20
	MaxOpImagePixels     = 16_777_216
)

// OpResult travels through Deck's executor and broker. Binary content has
// its own limits; it must never pass through text truncation/redaction.
type OpResult struct {
	Text    string    `json:"text,omitempty"`
	Images  []OpImage `json:"images,omitempty"`
	IsError bool      `json:"is_error,omitempty"`
}

// OpImage is bytes internally, base64 on JSON wire, and an image content
// block at the MCP boundary. Local paths are not model-visible images.
type OpImage struct {
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

func TextResult(text string, isError bool) OpResult {
	return OpResult{Text: text, IsError: isError}
}

// ValidateImages checks metadata before an image is delivered to a CLI.
// DecodeConfig reads dimensions without allocating a decoded pixel buffer.
func (r OpResult) ValidateImages() error {
	if len(r.Images) > MaxOpImages {
		return fmt.Errorf("image result exceeds %d images", MaxOpImages)
	}
	total := 0
	for _, im := range r.Images {
		if im.MimeType != "image/png" && im.MimeType != "image/jpeg" {
			return fmt.Errorf("unsupported image MIME type")
		}
		if len(im.Data) == 0 || len(im.Data) > MaxOpImageBytes {
			return fmt.Errorf("image result exceeds byte limit or is empty")
		}
		total += len(im.Data)
		if total > MaxOpImageTotalBytes {
			return fmt.Errorf("image result exceeds total byte limit")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(im.Data))
		if err != nil || "image/"+format != im.MimeType {
			return fmt.Errorf("image bytes do not match MIME type")
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxOpImagePixels/cfg.Height {
			return fmt.Errorf("image result exceeds pixel limit")
		}
	}
	return nil
}
