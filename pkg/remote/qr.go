package remote

import (
	"strings"

	"rsc.io/qr"
)

const quietZone = 4

// RenderQR draws text as a terminal QR code from half-block characters,
// two rows of modules per line. Colours are forced to black on white:
// scanners need dark modules on a light field whatever the terminal theme.
func RenderQR(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	size := code.Size
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		return code.Black(x, y)
	}
	var sb strings.Builder
	for y := -quietZone; y < size+quietZone; y += 2 {
		sb.WriteString("\x1b[30;47m")
		for x := -quietZone; x < size+quietZone; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				sb.WriteRune('█')
			case top:
				sb.WriteRune('▀')
			case bottom:
				sb.WriteRune('▄')
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteString("\x1b[0m\n")
	}
	return sb.String(), nil
}
