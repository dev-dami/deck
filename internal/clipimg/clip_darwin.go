//go:build darwin

package clipimg

// darwinScript writes the clipboard PNG data to the file named by the first
// argument. It fails when the clipboard holds no PNG data.
const darwinScript = `on run argv
	set f to POSIX file (item 1 of argv)
	set fh to open for access f with write permission
	try
		set eof fh to 0
		write (the clipboard as «class PNGf») to fh
		close access fh
	on error errMsg number errNum
		try
			close access fh
		end try
		error errMsg number errNum
	end try
end run`

// noImageCode is the exit code a failed pngpaste or osascript run returns.
const noImageCode = 1

// clipboardInvocation prefers pngpaste when installed and falls back to
// osascript, which ships with macOS.
func clipboardInvocation(path string) (invocation, bool) {
	if _, err := lookPath("pngpaste"); err == nil {
		return invocation{name: "pngpaste", args: []string{path}, dest: path}, true
	}
	if _, err := lookPath("osascript"); err == nil {
		return invocation{
			name: "osascript",
			args: []string{"-e", darwinScript, path},
			dest: path,
		}, true
	}
	return invocation{}, false
}

// noImage reports whether a failed run means "no image". Both tools exit
// non-zero only when the clipboard has no PNG data, so any failure maps to it.
func noImage(code int) bool {
	return code != 0
}
