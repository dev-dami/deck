package dialog

import "github.com/dev-dami/deck/internal/deck/gh"

// GitAction is an action from one of the git dialogs (or a git command in
// the palette). The UI handles them all in one place.
type GitAction interface {
	gitAction()
}

// GitTab is a tab of the git panel.
type GitTab int

// The git panel's tabs, in order.
const (
	GitTabBranch GitTab = iota
	GitTabPRs
	GitTabRecent
	gitTabCount
)

// Git actions.
type (
	// ActionGitOpen opens the git panel on Tab; Commit starts it on the
	// commit message input.
	ActionGitOpen struct {
		Tab    GitTab
		Commit bool
	}
	// ActionGitRefresh reloads the git panel.
	ActionGitRefresh struct{}
	// ActionGitClosePanel closes the git panel. It also hides the
	// "recently pushed" nudge for the branch it was about.
	ActionGitClosePanel struct{}
	// ActionGitCommitAll stages everything and commits it.
	ActionGitCommitAll struct{ Message string }
	// ActionGitPush pushes the current branch.
	ActionGitPush struct{}
	// ActionGitFetch fetches from the remotes.
	ActionGitFetch struct{}
	// ActionGitCheckout checks out Branch, then opens the create pull
	// request form for it when ThenCreatePR is set.
	ActionGitCheckout struct {
		Branch       string
		ThenCreatePR bool
	}
	// ActionGitCreatePR opens the create pull request form for the current
	// branch.
	ActionGitCreatePR struct{}
	// ActionGitSubmitPR pushes the branch if it needs it and opens the pull
	// request.
	ActionGitSubmitPR struct {
		Title, Base, Body string
		Draft             bool
	}
	// ActionGitViewPR opens pull request Number; 0 is the current branch's.
	ActionGitViewPR struct{ Number int }
	// ActionGitPRRefresh reloads pull request Number.
	ActionGitPRRefresh struct{ Number int }
	// ActionGitPRRerun re-runs the failed jobs of the workflow runs.
	ActionGitPRRerun struct {
		Number int
		RunIDs []string
	}
	// ActionGitPRLog shows the failed log of a check.
	ActionGitPRLog struct {
		Number int
		Check  gh.Check
	}
	// ActionGitPRHandoff puts a check's failed log in the composer for an
	// agent. Log is fetched when empty.
	ActionGitPRHandoff struct {
		Number int
		Check  gh.Check
		Log    string
	}
	// ActionGitPRAskMerge asks how (and whether) to merge pull request
	// Number.
	ActionGitPRAskMerge struct {
		Number int
		Title  string
	}
	// ActionGitPRMerge merges pull request Number by Method.
	ActionGitPRMerge struct {
		Number int
		Method string
	}
	// ActionGitPRCheckout checks out pull request Number's branch.
	ActionGitPRCheckout struct{ Number int }
	// ActionGitPROpenWeb opens pull request Number in the browser.
	ActionGitPROpenWeb struct{ Number int }
)

func (ActionGitOpen) gitAction()       {}
func (ActionGitRefresh) gitAction()    {}
func (ActionGitClosePanel) gitAction() {}
func (ActionGitCommitAll) gitAction()  {}
func (ActionGitPush) gitAction()       {}
func (ActionGitFetch) gitAction()      {}
func (ActionGitCheckout) gitAction()   {}
func (ActionGitCreatePR) gitAction()   {}
func (ActionGitSubmitPR) gitAction()   {}
func (ActionGitViewPR) gitAction()     {}
func (ActionGitPRRefresh) gitAction()  {}
func (ActionGitPRRerun) gitAction()    {}
func (ActionGitPRLog) gitAction()      {}
func (ActionGitPRHandoff) gitAction()  {}
func (ActionGitPRAskMerge) gitAction() {}
func (ActionGitPRMerge) gitAction()    {}
func (ActionGitPRCheckout) gitAction() {}
func (ActionGitPROpenWeb) gitAction()  {}
