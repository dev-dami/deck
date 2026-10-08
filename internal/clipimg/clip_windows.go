//go:build windows

package clipimg

// winScript reads the clipboard image and writes it as PNG to the path in
// DECK_CLIP_PATH. Exit code 3 means the clipboard holds no image. The path is
// passed through the environment, never spliced into the script.
const winScript = `Add-Type -AssemblyName System.Windows.Forms; ` +
	`Add-Type -AssemblyName System.Drawing; ` +
	`$img = [System.Windows.Forms.Clipboard]::GetImage(); ` +
	`if ($img -eq $null) { exit 3 }; ` +
	`$img.Save($env:DECK_CLIP_PATH, [System.Drawing.Imaging.ImageFormat]::Png)`

// noImageCode is the exit code winScript uses for "no image".
const noImageCode = 3

// clipboardInvocation returns the PowerShell run that saves the clipboard image
// to path. The Clipboard API needs an STA thread, hence -STA.
func clipboardInvocation(path string) (invocation, bool) {
	if _, err := lookPath("powershell.exe"); err != nil {
		return invocation{}, false
	}
	return invocation{
		name: "powershell.exe",
		args: []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", winScript},
		env:  []string{"DECK_CLIP_PATH=" + path},
		dest: path,
	}, true
}

// noImage reports whether exit code means "no image on the clipboard".
func noImage(code int) bool {
	return code == noImageCode
}
