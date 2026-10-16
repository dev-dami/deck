// Package policy decides which approvals a phone may answer with its device
// key alone and which need a fresh biometric check (docs/mobile-contract.md
// §7). It errs toward "destructive": anything it cannot parse is destructive.
package policy

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// Tier is wire.TierNormal or wire.TierDestructive.
type Tier = string

// Classify returns the tier of an approval raised in a project rooted at
// root. An empty root means the root is unknown, which makes any path
// suspect.
func Classify(root string, a wire.Approval) Tier {
	switch a.Kind {
	case wire.ApprovalCommand:
		if a.CommandTruncated || strings.TrimSpace(a.Command) == "" {
			return wire.TierDestructive
		}
		return classifyCommand(a.Command)
	case wire.ApprovalEdit:
		if a.Path == "" || sensitivePath(a.Path) || !inside(root, a.Path) {
			return wire.TierDestructive
		}
		return wire.TierNormal
	}
	return wire.TierDestructive
}

// sensitivePath reports paths whose change or disclosure is never routine:
// environment files, keys, and git internals.
func sensitivePath(p string) bool {
	slash := strings.ReplaceAll(p, `\`, "/")
	for _, seg := range strings.Split(slash, "/") {
		l := strings.ToLower(seg)
		switch {
		case l == ".git", l == ".ssh", l == ".aws", l == ".gnupg":
			return true
		case l == ".env" || strings.HasPrefix(l, ".env."):
			return true
		case strings.HasSuffix(l, ".pem"), strings.HasSuffix(l, ".key"), strings.HasSuffix(l, ".p12"), strings.HasSuffix(l, ".pfx"):
			return true
		case strings.HasPrefix(l, "id_") && !strings.HasSuffix(l, ".pub"):
			return true
		}
	}
	return false
}

// inside reports whether p, resolved against root, stays within root. Without
// a root nothing is known to be inside.
func inside(root, p string) bool {
	if root == "" {
		return false
	}
	if !filepath.IsAbs(p) && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, `\`) {
		p = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../") && !path.IsAbs(rel)
}

var (
	// Shell constructs after which the real command cannot be read off the
	// text: substitution, evaluation, and decoded payloads.
	opaque = regexp.MustCompile("`|\\$\\(|\\beval\\b|\\bsource\\b|base64\\s+(-d|--decode)|<\\(|\\bxargs\\b|\\bfind\\b[^|;&]*-(exec|delete)")
	// A segment is one simple command of a pipeline or list.
	splitter = regexp.MustCompile(`&&|\|\||[;|&\n]`)

	rmRecursive = regexp.MustCompile(`^rm\s+(-\S*[rR]\S*|--recursive)\b|^rm\s+.*\s(-\S*[rR]\S*|--recursive)\b`)
	gitForce    = regexp.MustCompile(`^git\s+(\S+\s+)*push\b.*(\s--force\b|\s--force-with-lease\b|\s-f\b|\s--delete\b|\s\+\S)`)
	gitReset    = regexp.MustCompile(`^git\s+(\S+\s+)*reset\b.*--hard\b`)
	gitClean    = regexp.MustCompile(`^git\s+(\S+\s+)*clean\b.*\s-\S*[fdx]`)
	gitCheckout = regexp.MustCompile(`^git\s+(\S+\s+)*(checkout|restore)\b.*(\s--\s+\.|\s\.$)`)
	gitBranchD  = regexp.MustCompile(`^git\s+(\S+\s+)*branch\b.*\s-D\b`)
	sqlDrop     = regexp.MustCompile(`(?i)\b(drop|truncate)\s+(table|database|schema)\b|\bdelete\s+from\b`)
	mkfs        = regexp.MustCompile(`^(sudo\s+)?mkfs(\.\w+)?\b`)
	ddCmd       = regexp.MustCompile(`^(sudo\s+)?dd\b`)
	chmodR      = regexp.MustCompile(`^(sudo\s+)?(chmod|chown)\s+.*(-\S*R\S*|--recursive)\b`)
	sudo        = regexp.MustCompile(`^sudo\b`)
	pipeToShell = regexp.MustCompile(`(?i)\b(curl|wget|iwr|irm|invoke-webrequest)\b.*\|\s*(sudo\s+)?(sh|bash|zsh|iex|invoke-expression|python3?|node)\b`)
	psRemove    = regexp.MustCompile(`(?i)^(remove-item|rm|del|rd|rmdir)\b.*(-recurse|\s/s\b)`)
	// Redirects into system paths or raw disks; /dev/null and the std streams
	// are routine.
	redirectSys = regexp.MustCompile(`>\s*/(etc|boot|sys|proc)/|>\s*/dev/(sd|hd|vd|nvme|disk|mem|kmem|mapper)`)
	formatCmd   = regexp.MustCompile(`(?i)^(format|diskpart|fdisk|parted)\b`)
	npmPublish  = regexp.MustCompile(`^(npm|bun|pnpm|yarn|cargo|twine|gem)\s+(\S+\s+)*(publish|push)\b`)
)

func classifyCommand(cmd string) Tier {
	if opaque.MatchString(cmd) || pipeToShell.MatchString(cmd) || redirectSys.MatchString(cmd) || sqlDrop.MatchString(cmd) {
		return wire.TierDestructive
	}
	for _, seg := range splitter.Split(cmd, -1) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		for _, re := range []*regexp.Regexp{
			rmRecursive, gitForce, gitReset, gitClean, gitCheckout, gitBranchD, mkfs, ddCmd,
			chmodR, sudo, psRemove, formatCmd, npmPublish,
		} {
			if re.MatchString(seg) {
				return wire.TierDestructive
			}
		}
	}
	return wire.TierNormal
}
