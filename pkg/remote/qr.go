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

// QRRows encodes text as rows of "1" (dark) and "0" (light) modules, quiet
// zone included, for callers that draw the code themselves (the desktop
// app's phone-link page).
func QRRows(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	side := code.Size + 2*quietZone
	rows := make([]string, 0, side)
	for y := -quietZone; y < code.Size+quietZone; y++ {
		var sb strings.Builder
		for x := -quietZone; x < code.Size+quietZone; x++ {
			if x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y) {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		}
		rows = append(rows, sb.String())
	}
	return rows, nil
}
