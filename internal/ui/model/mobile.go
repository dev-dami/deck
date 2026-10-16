package model

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/dev-dami/deck/internal/ui/dialog"
	"github.com/dev-dami/deck/pkg/remote"
)

var mobileAddressPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://\S+`)

func mobileCommandError(err error) error {
	fallback := "Mobile action failed. Run deck mobile connect --help for troubleshooting."
	var exit *exec.ExitError
	if !errors.As(err, &exit) || len(exit.Stderr) == 0 {
		return errors.New(fallback)
	}
	detail := ansi.Strip(string(exit.Stderr))
	// Unexpected credential-bearing output is hidden wholesale, including URL
	// fragments which could carry the pairing secret. URLs are never displayed.
	for _, marker := range []string{"psk", "token", "secret", "#p=", "pair_url"} {
		if strings.Contains(strings.ToLower(detail), marker) {
			return errors.New(fallback)
		}
	}
	detail = mobileAddressPattern.ReplaceAllString(detail, "[address omitted]")
	detail = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, detail)
	detail = strings.Join(strings.Fields(detail), " ")
	if detail == "" {
		return errors.New(fallback)
	}
	return errors.New(ansi.Truncate(detail, 240, "…"))
}

func isMobileSlash(content string) bool {
	_, _, ok := mobileSlashAction(content)
	return ok
}

func mobileSlashAction(content string) (string, bool, bool) {
	switch strings.TrimSpace(content) {
	case "/remote":
		return "start", true, true
	case "/link":
		return "link", false, true
	case "/mobile", "/mobile status":
		return "status", false, true
	case "/mobile pair":
		return "pair", false, true
	case "/mobile start":
		return "start", false, true
	case "/mobile lan":
		return "start", true, true
	case "/mobile stop":
		return "stop", false, true
	}
	return "", false, false
}

func mobileConnectArgs(project, action string, lan bool) []string {
	device, revoke := strings.CutPrefix(action, "revoke:")
	if revoke {
		action = "revoke"
	}
	args := []string{"mobile", "connect", "--json", "--project", project, "--action", action}
	if revoke {
		args = append(args, "--device", device)
	}
	if lan {
		args = append(args, "--lan")
	}
	return args
}

func mobileConnectCmd(owner *dialog.Mobile, request uint64, project, action string, lan bool) tea.Cmd {
	return mobileConnectCmdUsing(owner, request, project, action, lan, runMobileConnect)
}

func runMobileConnect(ctx context.Context, project string, args []string) ([]byte, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = project
	return cmd.Output()
}

func mobileConnectCmdUsing(owner *dialog.Mobile, request uint64, project, action string, lan bool, run func(context.Context, string, []string) ([]byte, error)) tea.Cmd {
	return func() tea.Msg {
		result := dialog.MobileResultMsg{Owner: owner, Request: request}
		timeout := 60 * time.Second
		if action == "start" || action == "link" {
			timeout = 150 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		data, err := run(ctx, project, mobileConnectArgs(project, action, lan))
		if err != nil {
			// Child stderr can include pairing credentials; never route it to notices/history.
			result.Err = mobileCommandError(err)
			if ctx.Err() != nil {
				result.Err = errors.New("Mobile action timed out. Press r to check status before trying again.")
			}
			return result
		}
		if json.Unmarshal(data, &result.Status) != nil {
			result.Err = errors.New("Deck returned an invalid mobile status. Check that this executable supports mobile connect.")
			return result
		}
		if result.Status.PairURL != "" {
			result.QR, err = remote.RenderQR(result.Status.PairURL)
			if err != nil {
				result.Err = errors.New("Could not render pairing QR. Try pairing again.")
			}
		}
		return result
	}
}

func (m *UI) openMobileDialog() tea.Cmd { return m.openMobileDialogAction("status") }

func (m *UI) openMobileDialogAction(action string) tea.Cmd {
	return m.openMobileDialogRequest(action, false)
}

func (m *UI) openMobileDialogRequest(action string, lan bool) tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MobileID) {
		m.dialog.BringToFront(dialog.MobileID)
		if action != "status" {
			return m.dialog.Dialog(dialog.MobileID).(*dialog.Mobile).Start(action, lan)
		}
		return nil
	}
	project := m.com.Workspace.WorkingDir()
	d := dialog.NewMobile(m.com, project, func(owner *dialog.Mobile, request uint64, action string, lan bool) tea.Cmd {
		return mobileConnectCmd(owner, request, project, action, lan)
	})
	m.dialog.OpenDialog(d)
	return d.Start(action, lan)
}
