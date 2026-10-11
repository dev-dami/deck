package gh

import (
	"regexp"
	"slices"
)

// Buckets a check falls in, as `gh pr checks` names them.
const (
	BucketPass     = "pass"
	BucketFail     = "fail"
	BucketPending  = "pending"
	BucketSkipping = "skipping"
	BucketCancel   = "cancel"
)

// Check is one status check on a pull request.
type Check struct {
	Name     string `json:"name"`
	Workflow string `json:"workflow"`
	State    string `json:"state"`  // GitHub's own word: SUCCESS, FAILURE, IN_PROGRESS…
	Bucket   string `json:"bucket"` // one of the Bucket constants
	Link     string `json:"link"`
}

// Failed reports whether the check failed (a cancel counts).
func (c Check) Failed() bool { return c.Bucket == BucketFail || c.Bucket == BucketCancel }

// Pending reports whether the check has not finished.
func (c Check) Pending() bool { return c.Bucket == BucketPending }

// actionsLink matches an Actions job link: .../actions/runs/<run>/job/<job>.
var actionsLink = regexp.MustCompile(`/actions/runs/(\d+)(?:/job/(\d+))?`)

// RunID is the Actions workflow run the check belongs to, or "" when it is
// not an Actions check.
func (c Check) RunID() string {
	if m := actionsLink.FindStringSubmatch(c.Link); m != nil {
		return m[1]
	}
	return ""
}

// JobID is the Actions job the check is, or "".
func (c Check) JobID() string {
	if m := actionsLink.FindStringSubmatch(c.Link); m != nil {
		return m[2]
	}
	return ""
}

// FailedRunIDs is the distinct workflow runs that have a failed check.
func FailedRunIDs(cs []Check) []string {
	var ids []string
	for _, c := range cs {
		if id := c.RunID(); id != "" && c.Failed() && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// CheckSummary counts a pull request's checks.
type CheckSummary struct {
	Total, Passed, Failed, Pending int
}

// States a [CheckSummary] can be in.
const (
	ChecksNone    = "none"
	ChecksPending = "pending"
	ChecksFailed  = "failed"
	ChecksPassed  = "passed"
)

// Summarize counts checks. Skipped checks count as passed and cancelled ones
// as failed.
func Summarize(cs []Check) CheckSummary {
	s := CheckSummary{Total: len(cs)}
	for _, c := range cs {
		switch {
		case c.Pending():
			s.Pending++
		case c.Failed():
			s.Failed++
		default:
			s.Passed++
		}
	}
	return s
}

// Done is how many checks have finished.
func (s CheckSummary) Done() int { return s.Passed + s.Failed }

// State is where the checks stand as a whole: any still running makes them
// pending, then any failure makes them failed.
func (s CheckSummary) State() string {
	switch {
	case s.Total == 0:
		return ChecksNone
	case s.Pending > 0:
		return ChecksPending
	case s.Failed > 0:
		return ChecksFailed
	default:
		return ChecksPassed
	}
}
