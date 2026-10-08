//go:build linux

package clipimg

import "os"

// noImageCode is the exit code wl-paste or xclip returns when the clipboard
// has no image/png target.
const noImageCode = 1

// clipboardInvocation picks wl-paste under Wayland and xclip under X11. If
// only one of the two is installed, that one is used.
func clipboardInvocation(path string) (invocation, bool) {
	wl := invocation{
		name:     "wl-paste",
		args:     []string{"--type", "image/png"},
		dest:     path,
		toStdout: true,
	}
	xc := invocation{
		name:     "xclip",
		args:     []string{"-selection", "clipboard", "-t", "image/png", "-o"},
		dest:     path,
		toStdout: true,
	}
	hasWL := hasTool("wl-paste")
	hasXC := hasTool("xclip")

	if os.Getenv("WAYLAND_DISPLAY") != "" && hasWL {
		return wl, true
	}
	if hasXC {
		return xc, true
	}
	if hasWL {
		return wl, true
	}
	return invocation{}, false
}

func hasTool(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

// noImage reports whether a failed run means "no image". Any non-zero exit
// from wl-paste or xclip means the clipboard has no PNG data.
func noImage(code int) bool {
	return code != 0
}
