package workspace

import (
	"context"
	"errors"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/gitutil"
	"github.com/dev-dami/deck/internal/history"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/proto"
	"github.com/dev-dami/deck/internal/question"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/shell"
	"github.com/dev-dami/deck/internal/skills"
)

// AppWorkspace implements the Workspace interface by delegating
// directly to an in-process [app.App] instance. This is the default
// mode when the client/server architecture is not enabled.
type AppWorkspace struct {
	app   *app.App
	store *config.ConfigStore
}

// NewAppWorkspace creates a new AppWorkspace wrapping the given app
// and config store.
func NewAppWorkspace(a *app.App, store *config.ConfigStore) *AppWorkspace {
	return &AppWorkspace{
		app:   a,
		store: store,
	}
}

// -- Sessions --

func (w *AppWorkspace) CreateSession(ctx context.Context, title string) (session.Session, error) {
	return w.app.Sessions.Create(ctx, title)
}

func (w *AppWorkspace) GetSession(ctx context.Context, sessionID string) (session.Session, error) {
	return w.app.Sessions.Get(ctx, sessionID)
}

func (w *AppWorkspace) ListSessions(ctx context.Context) ([]session.Session, error) {
	return w.app.Sessions.List(ctx)
}

func (w *AppWorkspace) SaveSession(ctx context.Context, sess session.Session) (session.Session, error) {
	return w.app.Sessions.Save(ctx, sess)
}

func (w *AppWorkspace) DeleteSession(ctx context.Context, sessionID string) error {
	return w.app.Sessions.Delete(ctx, sessionID)
}

func (w *AppWorkspace) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return w.app.Sessions.CreateAgentToolSessionID(messageID, toolCallID)
}

func (w *AppWorkspace) ParseAgentToolSessionID(sessionID string) (string, string, bool) {
	return w.app.Sessions.ParseAgentToolSessionID(sessionID)
}

// SetCurrentSession reports the active session to herdr so the pane
// can persist a resumable reference. Multi-client presence tracking
// is irrelevant in single-client local mode, but herdr still needs
// to know which session is live to support agent resume.
func (w *AppWorkspace) SetCurrentSession(ctx context.Context, sessionID string) error {
	w.app.ReportCurrentSession(sessionID)
	return nil
}

// -- Messages --

func (w *AppWorkspace) ListMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	// Drain any debounced updates so the caller observes the latest
	// in-memory state. message.Service buffers streaming deltas and a
	// cold List would otherwise miss them at session-switch time.
	if err := w.app.Messages.FlushAll(ctx); err != nil {
		return nil, err
	}
	return w.app.Messages.List(ctx, sessionID)
}

func (w *AppWorkspace) ListUserMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	return w.app.Messages.ListUserMessages(ctx, sessionID)
}

func (w *AppWorkspace) ListAllUserMessages(ctx context.Context) ([]message.Message, error) {
	return w.app.Messages.ListAllUserMessages(ctx)
}

// -- Agent --

// The agent methods below are the base for DeckWorkspace, which drives
// the agent CLIs; on its own the AppWorkspace has no agent.

var errNoAgent = errors.New("no agent: Deck's engine drives the agents")

func (w *AppWorkspace) AgentRun(context.Context, string, string, ...message.Attachment) error {
	return errNoAgent
}

func (w *AppWorkspace) AgentRunShellCommand(ctx context.Context, sessionID, command string, termWidth int, onProgress func(string), isFirstMessage bool) (proto.ShellCommandResponse, error) {
	var persist shell.PersistFunc
	if sessionID != "" {
		persist = func(cmd, output string, exitCode int) error {
			return shell.PersistOutput(ctx, w.app.Messages, sessionID, cmd, output, exitCode, w.store.Config().Options.DataDirectory)
		}
	}

	opts := shell.RunOptions{
		Command:   command,
		Cwd:       w.store.WorkingDir(),
		TermWidth: termWidth,
	}

	var result shell.CaptureResult
	var err error

	if onProgress != nil {
		result, err = shell.RunAndCaptureStream(ctx, opts, onProgress)
	} else {
		result, err = shell.RunAndPersist(ctx, opts, persist)
	}

	if err != nil && onProgress == nil {
		return proto.ShellCommandResponse{}, err
	}

	// Persist if we used the streaming path (persist wasn't called by RunAndPersist).
	if onProgress != nil && persist != nil {
		if persistErr := persist(command, result.Output, result.ExitCode); persistErr != nil {
			slog.Error("Failed to persist shell command output", "error", persistErr, "command", command)
		}
	}

	_ = isFirstMessage

	return proto.ShellCommandResponse{
		Output:   result.Output,
		ExitCode: result.ExitCode,
	}, nil
}

func (w *AppWorkspace) AgentCancel(string)                                 {}
func (w *AppWorkspace) AgentIsBusy() bool                                  { return false }
func (w *AppWorkspace) AgentIsSessionBusy(string) bool                     { return false }
func (w *AppWorkspace) AgentModel() AgentModel                             { return AgentModel{} }
func (w *AppWorkspace) AgentIsReady() bool                                 { return false }
func (w *AppWorkspace) AgentReadyErr() error                               { return ErrAgentNotInitialized }
func (w *AppWorkspace) AgentQueuedPrompts(string) int                      { return 0 }
func (w *AppWorkspace) AgentQueuedPromptsList(string) []string             { return nil }
func (w *AppWorkspace) AgentClearQueue(string)                             {}
func (w *AppWorkspace) AgentSetMain(string) error                          { return errNoAgent }
func (w *AppWorkspace) AgentSummarize(context.Context, string) error       { return errNoAgent }
func (w *AppWorkspace) UpdateAgentModel(context.Context) error             { return nil }
func (w *AppWorkspace) InitCoderAgent(context.Context) error               { return nil }
func (w *AppWorkspace) InitCoderAgentNonInteractive(context.Context) error { return nil }
func (w *AppWorkspace) GetDefaultSmallModel(string) config.SelectedModel {
	return config.SelectedModel{}
}

// -- Permissions --

func (w *AppWorkspace) PermissionGrant(perm permission.PermissionRequest) bool {
	return w.app.Permissions.Grant(perm)
}

func (w *AppWorkspace) PermissionGrantPersistent(perm permission.PermissionRequest) bool {
	return w.app.Permissions.GrantPersistent(perm)
}

func (w *AppWorkspace) PermissionDeny(perm permission.PermissionRequest) bool {
	return w.app.Permissions.Deny(perm)
}

func (w *AppWorkspace) PermissionSkipRequests() bool {
	return w.app.Permissions.SkipRequests()
}

func (w *AppWorkspace) PermissionSetSkipRequests(skip bool) {
	w.app.Permissions.SetSkipRequests(skip)
}

// -- Questions --

func (w *AppWorkspace) QuestionAnswer(responses []question.Answer) bool {
	return w.app.Questions.Answer(responses)
}

func (w *AppWorkspace) QuestionCancel() bool {
	return w.app.Questions.Cancel()
}

// -- FileTracker --

func (w *AppWorkspace) FileTrackerRecordRead(ctx context.Context, sessionID, path string) {
	w.app.FileTracker.RecordRead(ctx, sessionID, path)
}

func (w *AppWorkspace) FileTrackerLastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return w.app.FileTracker.LastReadTime(ctx, sessionID, path)
}

func (w *AppWorkspace) FileTrackerListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return w.app.FileTracker.ListReadFiles(ctx, sessionID)
}

// -- History --

func (w *AppWorkspace) ListSessionHistory(ctx context.Context, sessionID string) ([]history.File, error) {
	return w.app.History.ListBySession(ctx, sessionID)
}

// -- LSP --

// -- Config (read-only) --

func (w *AppWorkspace) Config() *config.Config {
	return w.store.Config()
}

func (w *AppWorkspace) WorkingDir() string {
	return w.store.WorkingDir()
}

func (w *AppWorkspace) GitBranch(context.Context) (string, error) {
	return gitutil.CurrentBranch(w.store.WorkingDir()), nil
}

func (w *AppWorkspace) Resolver() config.VariableResolver {
	return w.store.Resolver()
}

// -- Config mutations --

func (w *AppWorkspace) UpdatePreferredModel(scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error {
	return w.store.UpdatePreferredModel(scope, modelType, model)
}

func (w *AppWorkspace) SetCompactMode(scope config.Scope, enabled bool) error {
	return w.store.SetCompactMode(scope, enabled)
}

func (w *AppWorkspace) SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error {
	if err := w.store.SetProviderAPIKey(scope, providerID, apiKey); err != nil {
		return err
	}
	w.store.SignalAuthComplete(providerID)
	return nil
}

func (w *AppWorkspace) SetConfigField(scope config.Scope, key string, value any) error {
	return w.store.SetConfigField(scope, key, value)
}

func (w *AppWorkspace) SetConfigFields(scope config.Scope, fields map[string]any) error {
	return w.store.SetConfigFields(scope, fields)
}

func (w *AppWorkspace) RemoveConfigField(scope config.Scope, key string) error {
	return w.store.RemoveConfigField(scope, key)
}

func (w *AppWorkspace) RefreshOAuthToken(ctx context.Context, scope config.Scope, providerID string) error {
	return w.store.RefreshOAuthToken(ctx, scope, providerID)
}

// -- Project lifecycle --

func (w *AppWorkspace) ProjectNeedsInitialization() (bool, error) {
	return config.ProjectNeedsInitialization(w.store)
}

func (w *AppWorkspace) MarkProjectInitialized() error {
	return config.MarkProjectInitialized(w.store)
}

func (w *AppWorkspace) InitializePrompt() (string, error) {
	return "", nil
}

func (w *AppWorkspace) ListSkills(_ context.Context) ([]skills.CatalogEntry, error) {
	mgr := w.app.Skills
	return skills.Catalog(mgr.ActiveSkills(), mgr.ResolvedPaths(), mgr.WorkingDir()), nil
}

func (w *AppWorkspace) ReadSkill(_ context.Context, skillID string) ([]byte, skills.SkillReadResult, error) {
	mgr := w.app.Skills
	return skills.ReadContent(mgr.ActiveSkills(), mgr.ResolvedPaths(), mgr.WorkingDir(), skillID)
}

// -- MCP operations --

// -- Lifecycle --

func (w *AppWorkspace) Subscribe(program *tea.Program) {
	w.app.Subscribe(program)
}

func (w *AppWorkspace) Shutdown() {
	w.app.Shutdown()
}

// App returns the underlying app.App instance.
func (w *AppWorkspace) App() *app.App {
	return w.app
}

// Store returns the underlying config store.
func (w *AppWorkspace) Store() *config.ConfigStore {
	return w.store
}

// Compile-time check that AppWorkspace implements Workspace.
var _ Workspace = (*AppWorkspace)(nil)
