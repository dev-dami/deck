package dialog

import "github.com/dev-dami/deck/internal/ui/styles"

// gitCommands is the git and GitHub entries of the command palette.
func gitCommands(t *styles.Styles) []*CommandItem {
	return []*CommandItem{
		NewCommandItem(t, "git", "Git", GitKey, ActionGitOpen{Tab: GitTabBranch}).
			WithAliases("branch", "github"),
		NewCommandItem(t, "git_create_pr", "Create Pull Request", "", ActionGitCreatePR{}).
			WithAliases("pr", "open pr", "github"),
		NewCommandItem(t, "git_prs", "Pull Requests", "", ActionGitOpen{Tab: GitTabPRs}).
			WithAliases("prs", "github"),
		NewCommandItem(t, "git_checks", "Checks for This Branch", "", ActionGitViewPR{}).
			WithAliases("ci", "pr checks", "actions"),
		NewCommandItem(t, "git_push", "Push", "", ActionGitPush{}).
			WithAliases("git push"),
		NewCommandItem(t, "git_commit", "Commit All…", "", ActionGitOpen{Tab: GitTabBranch, Commit: true}).
			WithAliases("git commit"),
	}
}
