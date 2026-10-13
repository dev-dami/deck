// Package workspace defines the Workspace interface used by all
// frontends (TUI, CLI) to interact with a running workspace. Two
// implementations exist: one wrapping a local app.App instance and one
// wrapping the HTTP client SDK.
package workspace

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/history"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/proto"
	"github.com/dev-dami/deck/internal/question"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/skills"
)

// Reasons the coder agent may be unavailable, returned by
// Workspace.AgentReadyErr so callers can tell a genuinely
// uninitialized agent apart from a lost server connection.
var (
	// ErrAgentNotInitialized means the workspace exists but its coder
	// agent has not been configured/initialized (e.g. no model set).
	ErrAgentNotInitialized = errors.New("coder agent is not initialized")
	// ErrServerUnreachable means the client could not reach the server
	// to determine the agent's status (server down, or the workspace was
	// torn down out from under the client).
	ErrServerUnreachable = errors.New("lost connection to the deck server")
	// ErrWorkspaceGone means the server is reachable but no longer knows
	// this client's workspace: it was torn down, or the server was
	// replaced underneath the client. The subscription loop re-registers
	// the workspace in the background when it sees this.
	ErrWorkspaceGone = errors.New("the server reset this workspace; reconnecting")
	// ErrStreamClosed means an established event stream ended.
	// Resubscribing usually succeeds immediately, but events published in
	// the meantime are lost for good, so the client treats it as a
	// degraded link that requires a resync.
	ErrStreamClosed = errors.New("the event stream closed; reconnecting")
)

// ConnectionState describes the health of the client-server link as
// reported by the [ClientWorkspace] subscription loop.
type ConnectionState int

const (
	// ConnectionDegraded means the event stream is down (or the workspace
	// was lost server-side) and the client is retrying or re-registering
	// in the background.
	ConnectionDegraded ConnectionState = iota
	// ConnectionRecovered means the event stream was re-established,
	// possibly against a re-created workspace.
	ConnectionRecovered
)

// ConnectionEvent is delivered to the TUI as a tea.Msg on degraded and
// recovered transitions of the client-server link. Local (in-process)
// workspaces never emit it.
type ConnectionEvent struct {
	State ConnectionState
	// Err is the most recent failure, set when State is
	// ConnectionDegraded.
	Err error
	// Stuck marks a degraded connection that has resisted repeated
	// recovery attempts. The loop keeps retrying regardless; the UI
	// should escalate from a transient notice to a persistent error.
	Stuck bool
}

// AgentModel holds the model information exposed to the UI.
type AgentModel struct {
	CatwalkCfg catwalk.Model
	ModelCfg   config.SelectedModel
}

// Workspace is the main abstraction consumed by the TUI and CLI. It
// groups every operation a frontend needs to perform against a running
// workspace, regardless of whether the workspace is in-process or
// remote.
type Workspace interface {
	// Sessions
	CreateSession(ctx context.Context, title string) (session.Session, error)
	GetSession(ctx context.Context, sessionID string) (session.Session, error)
	ListSessions(ctx context.Context) ([]session.Session, error)
	SaveSession(ctx context.Context, sess session.Session) (session.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	CreateAgentToolSessionID(messageID, toolCallID string) string
	ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool)
	// SetCurrentSession reports the session this client is currently
	// viewing. Empty sessionID clears the entry (e.g. landing screen).
	// In single-client local mode this is a no-op. In client/server
	// mode it informs the server's per-client presence map so other
	// observers can compute attached-client counts per session.
	SetCurrentSession(ctx context.Context, sessionID string) error

	// Messages
	ListMessages(ctx context.Context, sessionID string) ([]message.Message, error)
	ListUserMessages(ctx context.Context, sessionID string) ([]message.Message, error)
	ListAllUserMessages(ctx context.Context) ([]message.Message, error)

	// Agent
	AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error
	AgentRunShellCommand(ctx context.Context, sessionID, command string, termWidth int, onProgress func(string), isFirstMessage bool) (proto.ShellCommandResponse, error)
	AgentCancel(sessionID string)
	AgentIsBusy() bool
	AgentIsSessionBusy(sessionID string) bool
	AgentModel() AgentModel
	AgentIsReady() bool
	// AgentReadyErr reports nil when the coder agent is ready to accept
	// work, or a descriptive error otherwise: ErrAgentNotInitialized
	// when the agent simply isn't set up, or ErrServerUnreachable
	// (wrapped) when the client could not reach the server to find out.
	// It lets the UI show an actionable message instead of collapsing
	// both cases into "agent offline".
	AgentReadyErr() error
	AgentQueuedPrompts(sessionID string) int
	AgentQueuedPromptsList(sessionID string) []string
	AgentClearQueue(sessionID string)
	AgentSetMain(agentID string) error
	AgentSummarize(ctx context.Context, sessionID string) error
	UpdateAgentModel(ctx context.Context) error
	InitCoderAgent(ctx context.Context) error
	InitCoderAgentNonInteractive(ctx context.Context) error
	GetDefaultSmallModel(providerID string) config.SelectedModel

	// Permissions
	//
	// PermissionGrant, PermissionGrantPersistent, and PermissionDeny
	// return true if the call resolved the pending request and false if
	// it had already been resolved by another subscriber (or is no
	// longer pending). A false return is not an error; the modal can
	// still close locally because the resolution will arrive via the
	// PermissionNotification event stream regardless of which client
	// won the race.
	PermissionGrant(perm permission.PermissionRequest) bool
	PermissionGrantPersistent(perm permission.PermissionRequest) bool
	PermissionDeny(perm permission.PermissionRequest) bool
	PermissionSkipRequests() bool
	PermissionSetSkipRequests(skip bool)

	// Questions
	//
	// QuestionAnswer resolves the pending question with responses.
	QuestionAnswer(responses []question.Answer) bool

	// QuestionCancel cancels the pending question.
	QuestionCancel() bool

	// FileTracker
	FileTrackerRecordRead(ctx context.Context, sessionID, path string)
	FileTrackerLastReadTime(ctx context.Context, sessionID, path string) time.Time
	FileTrackerListReadFiles(ctx context.Context, sessionID string) ([]string, error)

	// History
	ListSessionHistory(ctx context.Context, sessionID string) ([]history.File, error)

	// Config (read-only data)
	Config() *config.Config
	WorkingDir() string
	GitBranch(ctx context.Context) (string, error)
	Resolver() config.VariableResolver

	// Config mutations (proxied to server in client mode)
	UpdatePreferredModel(scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error
	SetCompactMode(scope config.Scope, enabled bool) error
	SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error
	SetConfigField(scope config.Scope, key string, value any) error
	SetConfigFields(scope config.Scope, fields map[string]any) error
	RemoveConfigField(scope config.Scope, key string) error
	RefreshOAuthToken(ctx context.Context, scope config.Scope, providerID string) error

	// Project lifecycle
	ProjectNeedsInitialization() (bool, error)
	MarkProjectInitialized() error
	InitializePrompt() (string, error)
	ListSkills(ctx context.Context) ([]skills.CatalogEntry, error)
	ReadSkill(ctx context.Context, skillID string) ([]byte, skills.SkillReadResult, error)

	// Events
	Subscribe(program *tea.Program)
	Shutdown()
}
