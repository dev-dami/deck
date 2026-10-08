//go:build !windows && !darwin && !linux

package clipimg

// noImageCode is unused on this platform because no clipboard tool is known.
const noImageCode = 1

// clipboardInvocation reports no tool: this OS has no clipboard image support.
func clipboardInvocation(path string) (invocation, bool) {
	return invocation{}, false
}

func noImage(code int) bool {
	return code != 0
}
