package model

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/charmbracelet/x/editor"
	xstrings "github.com/charmbracelet/x/exp/strings"
	"github.com/dev-dami/deck/internal/notify"

	"github.com/dev-dami/deck/internal/app"
	"github.com/dev-dami/deck/internal/clipboard"
	"github.com/dev-dami/deck/internal/commands"
	"github.com/dev-dami/deck/internal/config"
	"github.com/dev-dami/deck/internal/deck/media"
	"github.com/dev-dami/deck/internal/deck/peer"
	decks "github.com/dev-dami/deck/internal/deck/session"
	"github.com/dev-dami/deck/internal/fsext"
	"github.com/dev-dami/deck/internal/history"
	"github.com/dev-dami/deck/internal/home"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/permission"
	"github.com/dev-dami/deck/internal/pubsub"
	"github.com/dev-dami/deck/internal/question"
	"github.com/dev-dami/deck/internal/session"
	"github.com/dev-dami/deck/internal/skills"
	"github.com/dev-dami/deck/internal/ui/attachments"
	"github.com/dev-dami/deck/internal/ui/chat"
	"github.com/dev-dami/deck/internal/ui/common"
	"github.com/dev-dami/deck/internal/ui/completions"
	"github.com/dev-dami/deck/internal/ui/dialog"
	fimage "github.com/dev-dami/deck/internal/ui/image"
	"github.com/dev-dami/deck/internal/ui/logo"
	"github.com/dev-dami/deck/internal/ui/motion"
	"github.com/dev-dami/deck/internal/ui/notice"
	"github.com/dev-dami/deck/internal/ui/notification"
	"github.com/dev-dami/deck/internal/ui/styles"
	"github.com/dev-dami/deck/internal/ui/tone"
	"github.com/dev-dami/deck/internal/ui/util"
	"github.com/dev-dami/deck/internal/ui/yolo"
	"github.com/dev-dami/deck/internal/version"
	"github.com/dev-dami/deck/internal/workspace"
)

// Compact mode breakpoints.
const (
	compactModeWidthBreakpoint  = 120
	compactModeHeightBreakpoint = 30
)

// If pasted text has more than 10 newlines, treat it as a file attachment.
const pasteLinesThreshold = 10

// If pasted text has more than 1000 columns, treat it as a file attachment.
const pasteColsThreshold = 1000

// Session details panel max height.
const sessionDetailsMaxHeight = 20

// gitBranchPollInterval is how often the workspace's checked-out branch is
// re-read. A checkout emits no event Deck can subscribe to, so the branch
// has to be polled to stay current.
const gitBranchPollInterval = 5 * time.Second

// gitBranchFetchTimeout bounds one branch read. Locally this is a file
// read; in client/server mode it is a request to the server.
const gitBranchFetchTimeout = 10 * time.Second

// TextareaMaxHeight is the maximum height of the prompt textarea.
const TextareaMaxHeight = 15

// editorHeightMargin is the vertical margin added to the textarea height to
// account for the attachments row (top) and bottom margin.
const editorHeightMargin = 2

// TextareaMinHeight is the minimum height of the prompt textarea.
const TextareaMinHeight = 3

// uiFocusState represents the current focus state of the UI.
type uiFocusState uint8

// Possible uiFocusState values.
const (
	uiFocusNone uiFocusState = iota
	uiFocusEditor
	uiFocusMain
	uiFocusSidebar
)

type uiState uint8

// Possible uiState values.
const (
	uiOnboarding uiState = iota
	uiInitialize
	uiLanding
	uiChat
)

type uiInputMode uint8

const (
	uiInputModeCode uiInputMode = iota
	uiInputModePlan
)

type openEditorMsg struct {
	Text string
}

type shellResultMsg struct {
	PendingID string // ID of the pending ShellItem to update.
	Command   string
	Output    string
	ExitCode  int
}

// shellStreamMsg carries incremental output from a streaming shell command.
type shellStreamMsg struct {
	PendingID string
	Chunk     string
	streamCh  <-chan string // unexported; used to continue draining
}

type (
	// cancelTimerExpiredMsg is sent when the cancel timer expires.
	cancelTimerExpiredMsg struct{}
	// userCommandsLoadedMsg is sent when user commands are loaded.
	userCommandsLoadedMsg struct {
		Commands []commands.CustomCommand
	}
	// sendMessageMsg is sent to send a message (the project init prompt).
	sendMessageMsg struct {
		Content     string
		Attachments []message.Attachment
	}

	// copyChatHighlightMsg is sent to copy the current chat highlight to clipboard.
	copyChatHighlightMsg struct{}

	// sessionFilesUpdatesMsg is sent when the files for this session have been updated
	sessionFilesUpdatesMsg struct {
		sessionFiles []SessionFile
	}

	// gitBranchUpdatedMsg carries the workspace's checked-out branch. branch
	// is empty when the workspace is not a Git repository or HEAD is
	// detached.
	gitBranchUpdatedMsg struct {
		branch string
	}

	// gitBranchPollMsg is sent by the git branch poll timer.
	gitBranchPollMsg struct{}
)

// UI represents the main user interface model.
type UI struct {
	com          *common.Common
	session      *session.Session
	sessionFiles []SessionFile

	// keeps track of read files while we don't have a session id
	sessionFileReads []string

	// initialSessionID is set when loading a specific session on startup.
	initialSessionID string
	// continueLastSession is set to continue the most recent session on startup.
	continueLastSession bool

	lastUserMessageTime int64

	// The width and height of the terminal in cells.
	width  int
	height int
	layout uiLayout

	isTransparent bool

	// mouseEnabled controls whether Bubble Tea mouse reporting is active.
	// When false, the terminal emulator (or tmux) handles text selection,
	// copy/paste, right-click, and scrolling instead of Deck.
	mouseEnabled bool

	// themeKey identifies the currently applied theme so applyTheme can
	// skip the expensive style rebuild when switching to a provider that
	// resolves to the same theme.
	themeKey string

	// userThemeSelected records that the user explicitly chose a theme
	// during this session. It guards against provider-driven theme swaps
	// discarding that choice, even in client/server mode where the
	// config round-trip may not reflect the selection immediately.
	userThemeSelected bool

	focus uiFocusState
	state uiState
	mode  uiInputMode

	// Frame memoization (see framecache.go). scrollOnlyUpdate is set by
	// handlers that change nothing but the chat scroll position; frameDirty
	// overrides it when a layout change happens in the same update.
	frames           *frameCache
	scrollOnlyUpdate bool
	frameDirty       bool
	frameSkipPut     bool
	frameGCArmed     bool
	// planReadySessionID holds the session whose plan run emitted the
	// plan-ready marker but has not been confirmed for execution yet. It
	// lets the user reopen the handoff prompt after dismissing it.
	planReadySessionID string
	// modeSwitching is true while the async agent-model update kicked off
	// by setInputMode is still in flight; sending is blocked meanwhile.
	modeSwitching bool

	// cycleYolo is true while YOLO was enabled by the Shift+Tab input-mode
	// cycle, which is the only case where the cycle may disable it again.
	cycleYolo bool

	keyMap KeyMap
	keyenh tea.KeyboardEnhancementsMsg

	dialog *dialog.Overlay
	status *Status

	// isCanceling tracks whether the user has pressed escape once to cancel.
	isCanceling bool

	// bangMode tracks whether the editor is in bang (!) shell mode.
	bangMode     bool
	bangWasEmpty bool // true when bang prompt became empty on last keystroke

	// pendingBangCommand holds a shell command that was issued before
	// the session finished loading. The loadSessionMsg handler creates
	// the pending UI item and starts execution once the chat list is
	// stable, eliminating races between session load and shell output.
	pendingBangCommand string

	// bangCancel cancels a running bang-mode shell command. Nil when no
	// bang command is in progress. Set by runShellCommand, cleared by
	// shellResultMsg. Checked by isAgentBusy and cancelAgent so that
	// Escape works for bang commands the same way it does for agent runs.
	bangCancel context.CancelFunc

	header *header

	// yoloFX is the shockwave that plays when yolo is switched on;
	// yoloTicking is true while its frame tick is on its way.
	yoloFX      *yolo.Anim
	yoloTicking bool

	// workTicking is true while the working line's frame tick is on its
	// way; workSeen is when each busy agent was first seen at work.
	workTicking bool
	workSeen    map[string]time.Time

	// canvas is the screen buffer View draws each frame into, kept
	// between frames while the terminal size holds.
	canvas uv.ScreenBuffer
	// frameBuf is the scratch buffer View trims the rendered canvas into,
	// kept between frames so only the final string is allocated.
	frameBuf []byte
	// frameMemo shares reads between the parts of the frame View is
	// drawing; nil outside of it. See frameMemo.
	frameMemo *frameMemo
	// stylesGen changes whenever the active styles do (refreshStyles), so
	// text memoized across frames can tell a theme change.
	stylesGen uint64

	// chatWarmPending asks the end of the current update to start warming
	// the chat's message cache (see Chat.Warm), after a theme change
	// dropped it.
	chatWarmPending bool

	// sendProgressBar instructs the TUI to send progress bar updates to the
	// terminal.
	sendProgressBar    bool
	progressBarEnabled bool

	// turnOutcome is the result of the last agent turn reported to the
	// terminal as program status (done or error) until the user has seen
	// it, or empty.
	turnOutcome tea.ProgramState

	// caps hold different terminal capabilities that we query for.
	caps common.Capabilities

	// Editor components
	textarea textarea.Model

	// textareaMouseSelecting tracks whether a mouse selection gesture is
	// currently in progress within the textarea (left button held after a
	// click inside the textarea region).
	textareaMouseSelecting bool

	// Active inline editor replaces the textarea when non-nil.
	activeInline dialog.InlineEditor
	// inlineCursor stores the cursor from the last inline editor
	// Draw call, used by the cursor positioning logic below.
	inlineCursor *tea.Cursor

	// Attachment list
	attachments *attachments.Attachments

	readyPlaceholder   string
	workingPlaceholder string

	// Completions state
	completions              *completions.Completions
	completionsOpen          bool
	completionsStartIndex    int
	completionsQuery         string
	completionsPositionStart image.Point // x,y where user typed the sigil
	// completionsSigil is what opened the popup: "@" (or "$") for agents,
	// their models, profiles and teammates, "#" for files.
	completionsSigil string
	// completionsFiles is set while the popup lists files: after "#", or
	// after "@" and something path-like ("@src/").
	completionsFiles bool
	// profiles are the named agents (deck.json), as last read for the @
	// popup, the Agent picker or /profiles.
	profiles []workspace.Profile

	// lead is leadSession's lead as last read (lead.go); the editor's last
	// row shows it. leadGen drops reads that raced a pick.
	lead          workspace.Lead
	leadSession   string
	leadCheckedAt time.Time
	leadInFlight  bool
	leadGen       uint64
	// leadPicking holds reads off while a pick is being applied.
	leadPicking bool
	// pendingLead is the Agent picker's pick made before the session
	// exists: the first message starts it.
	pendingLead *dialog.ActionSelectAgentModel

	// Chat components
	chat *Chat

	// onboarding state
	onboarding struct {
		yesInitializeSelected bool
	}

	// skills
	skillStates []*skills.SkillState

	// sidebarLogo keeps a cached version of the sidebar sidebarLogo.
	sidebarLogo string

	// stripState is the sidebar's strip of panes; use strip() (strip.go).
	stripState *paneStrip

	// Notification state
	notifyBackend       notification.Backend
	notifyWindowFocused bool
	// custom commands
	customCommands []commands.CustomCommand

	// teamSnap is the peer link as last read, for the Team pane; nil until
	// the first read. teamWatching is set while a watch is out.
	teamSnap     *peer.Snapshot
	teamWatching bool
	// shared is the thread a teammate shares that the person follows in
	// the Shared pane; sharedGen counts what was followed, so a read for a
	// thread left behind is dropped.
	shared    *sharedView
	sharedGen int
	// teamSeen is the newest chat message or capsule request already
	// told; newer ones become notices.
	teamSeen uint64

	// capsules is the session's capsules for the Capsules pane, as last
	// read; capsulesLoading is set while a read is out.
	capsules        []decks.CapsuleInfo
	capsulesLoading bool
	capsulesAt      time.Time

	// forceCompactMode tracks whether compact mode is forced by user toggle
	forceCompactMode bool

	// isCompact tracks whether we're currently in compact layout mode (either
	// by user toggle or auto-switch based on window size)
	isCompact bool

	// detailsOpen tracks whether the details panel is open (in compact mode)
	detailsOpen bool

	// pills state
	pillsExpanded      bool
	pillsAutoExpanded  bool
	focusedPillSection pillSection
	// promptQueue / promptQueueItems mirror the session's queued prompts.
	// They are event-driven with a TTL backstop, fetched off-thread by
	// dispatchPromptQueueRefresh (see workspace_cache.go); promptQueue is
	// always len(promptQueueItems).
	promptQueue          int
	promptQueueItems     []string
	promptQueueCheckedAt time.Time
	promptQueueInFlight  bool
	// promptQueueGen is bumped by every queue state transition; an
	// in-flight fetch captures it at dispatch and its result is discarded
	// if the generation has moved on (see workspace_cache.go).
	promptQueueGen uint64
	// agentBusyCache / yoloCache memoize the workspace busy and permission
	// probes (synchronous HTTP round-trips in client/server mode). Reads
	// never probe; refreshes happen off-thread (see workspace_cache.go).
	agentBusyCache    ttlCache
	yoloCache         ttlCache
	busyFetchInFlight bool
	// agentReady / agentModel memoize the coordinator readiness and
	// selected model (AgentIsReady/AgentModel are synchronous HTTP GETs in
	// client/server mode, and modelInfo renders them every frame). Seeded
	// once at construction and refreshed by the same off-thread probe as
	// agentBusyCache.
	agentReady bool
	agentModel workspace.AgentModel
	// busyFetchGen is bumped by every busy/permission state transition;
	// like promptQueueGen it lets a stale in-flight probe result be
	// discarded and re-fetched instead of clobbering newer state.
	busyFetchGen uint64
	pillsView    string

	// Todo spinner
	todoSpinner    spinner.Model
	todoIsSpinning bool

	// preThemeStyles stores the styles before a theme preview so we can revert.
	preThemeStyles *styles.Styles

	// mouse highlighting related state
	lastClickTime time.Time
	hoverX        int
	hoverY        int

	// gitBranch is the workspace's checked-out branch as of the last poll,
	// empty when there is none to show. Reading it costs a file read
	// locally and a request in client/server mode, so renders take it from
	// here rather than asking the workspace per frame.
	gitBranch string

	// git is the git/GitHub state behind the header badge, the sidebar
	// block and the PR nudge (see git.go).
	git gitState

	// notices is the notification history and the toasts on screen (see
	// notices.go); jobs are Deck's own operations in flight (jobs.go).
	notices noticeState
	jobs    jobState

	// Prompt history for up/down navigation through previous messages.
	promptHistory struct {
		messages []string
		index    int
		draft    string
	}
}

// New creates a new instance of the [UI] model.
func New(com *common.Common, initialSessionID string, continueLast bool) *UI {
	// Editor components
	ta := textarea.New()
	ta.SetStyles(com.Styles.Editor.Textarea)
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)
	ta.DynamicHeight = true
	ta.MinHeight = TextareaMinHeight
	ta.MaxHeight = TextareaMaxHeight
	// Keep "ctrl+a" for line-start (the textarea default); bind select-all
	// to "ctrl+shift+a" instead (line-start is also available via "home").
	ta.KeyMap.LineStart = key.NewBinding(
		key.WithKeys("home", "ctrl+a"),
		key.WithHelp("home", "line start"),
	)
	ta.KeyMap.SelectAll = key.NewBinding(
		key.WithKeys("ctrl+shift+a"),
		key.WithHelp("ctrl+shift+a", "select all"),
	)
	// Copying is handled by deck's keymap (Editor.CopySelection) so it can
	// use deck's clipboard backend and user feedback; disable the
	// textarea's built-in copy binding.
	ta.KeyMap.CopySelection = key.NewBinding()
	ta.Focus()

	ch := NewChat(com, com.Config().Options.TUI.Scrollbar)

	keyMap := DefaultKeyMap()

	// Completions component
	comp := completions.New(
		com.Styles.Completions.Normal,
		com.Styles.Completions.Focused,
		com.Styles.Completions.Match,
	)

	todoSpinner := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(com.Styles.Pills.TodoSpinner),
	)

	// Attachments component
	attachments := attachments.New(
		attachments.NewRenderer(com.Styles.Attachments),
		attachments.Keymap{
			DeleteMode: keyMap.Editor.AttachmentDeleteMode,
			DeleteAll:  keyMap.Editor.DeleteAllAttachments,
			Escape:     keyMap.Editor.Escape,
		},
	)

	tone.SetLight(!com.Styles.IsDark)
	if editor, ok := com.Workspace.(workspace.SettingsEditor); ok {
		motion.Set(editor.DeckSettings().Motion)
	}
	header := newHeader(com)

	ui := &UI{
		com:                 com,
		dialog:              dialog.NewOverlay(),
		keyMap:              keyMap,
		textarea:            ta,
		chat:                ch,
		header:              header,
		yoloFX:              yolo.New(com.Styles.IsDark),
		completions:         comp,
		attachments:         attachments,
		todoSpinner:         todoSpinner,
		frames:              newFrameCache(frameCacheTTL, frameCacheMaxEntries),
		notifyBackend:       notification.NoopBackend{},
		notifyWindowFocused: true,
		initialSessionID:    initialSessionID,
		continueLastSession: continueLast,
		skillStates:         skills.GetLatestStates(),
	}

	status := NewStatus(com, ui)
	status.working = ui.workingLine

	// Seed the active theme key from the large model provider so the
	// first model selection can correctly skip a redundant theme swap.
	if cfg := com.Config(); cfg != nil {
		ui.themeKey = styles.ThemeKeyForProvider(cfg.Models[config.SelectedModelTypeLarge].Provider)
		ui.userThemeSelected = common.ThemeNameFromConfig(cfg) != ""
	}

	// Seed the yolo cache once at construction; afterwards it is kept
	// fresh by write-through toggles and off-thread refreshes so Update
	// and View never probe the workspace synchronously.
	yolo := com.Workspace.PermissionSkipRequests()
	ui.yoloCache.set(yolo)

	// Seed the memoized agent ready/model state the same way so the first
	// frame renders the model info; the busy probe keeps it fresh
	// afterwards.
	if com.Workspace.AgentIsReady() {
		ui.agentReady = true
		ui.agentModel = com.Workspace.AgentModel()
	}
	ui.mode = uiInputModeCode
	ui.setEditorPrompt(yolo)
	ui.randomizePlaceholders()
	ui.textarea.Placeholder = ui.readyPlaceholder
	ui.status = status

	// Initialize compact mode from config
	ui.forceCompactMode = com.Config().Options.TUI.CompactMode
	// Restore the sidebar's panes as the user left them.
	ui.stripState = newPaneStrip(com.Config().Options.TUI.SidebarLayout)

	// set onboarding state defaults
	ui.onboarding.yesInitializeSelected = true

	desiredState := uiLanding
	desiredFocus := uiFocusEditor
	// Deck's agents are the user's own CLIs, already logged in: there is no
	// provider to set up and no project file to write first.

	// set initial state
	ui.setState(desiredState, desiredFocus)

	opts := com.Config().Options

	// disable indeterminate progress bar
	ui.progressBarEnabled = opts.Progress == nil || *opts.Progress
	// enable transparent mode
	ui.isTransparent = opts.TUI.IsTransparent()
	// enable mouse support (default on)
	ui.mouseEnabled = opts.TUI.Mouse == nil || *opts.TUI.Mouse

	return ui
}

// Init initializes the UI model.
func (m *UI) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.state == uiLanding {
		cmds = append(cmds, m.header.startLogo(time.Now()))
	}
	if m.state == uiOnboarding {
		if cmd := m.openModelsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// load the user commands async
	cmds = append(cmds, m.loadCustomCommands())
	// Follow the peer link for the Team pane.
	if cmd := m.watchTeam(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// load initial session if specified
	initialSession := m.loadInitialSession()
	if initialSession != nil {
		cmds = append(cmds, initialSession)
	}
	// loadSessionMsg reloads history for whichever session arrives, so
	// doing it here too would be discarded — and with no session set yet
	// it reads every user message in the database, which the pending
	// session load then queues behind on the single connection.
	if initialSession == nil {
		cmds = append(cmds, m.loadPromptHistory())
	}
	// Prime the ChatGPT model catalog: a signed-in OpenAI provider
	// whose catalog is missing (the fetch at login failed, or the
	// credentials predate it) refills lazily, so the models dialog shows
	// the subscription section as soon as it is opened.
	cmds = append(cmds, m.updateAgentModelCmd(func() tea.Msg {
		_ = m.com.Workspace.UpdateAgentModel(context.TODO())
		return nil
	}))
	// Prime the memoized busy/permission state off-thread.
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// The branch is shown from the first frame on, so read it now and keep
	// polling for checkouts made outside Deck.
	cmds = append(cmds, m.fetchGitBranch(), m.gitBranchTicker())
	// The Usage pane reads today's tokens off-thread, then every so often.
	cmds = append(cmds, m.loadUsagePane())
	return tea.Batch(cmds...)
}

// loadInitialSession loads the initial session if one was specified on startup.
func (m *UI) loadInitialSession() tea.Cmd {
	switch {
	case m.state != uiLanding:
		// Only load if we're in landing state (i.e., fully configured)
		return nil
	case m.initialSessionID != "":
		return m.loadSession(m.initialSessionID)
	case m.continueLastSession:
		return func() tea.Msg {
			sessions, err := m.com.Workspace.ListSessions(context.Background())
			if err != nil || len(sessions) == 0 {
				return nil
			}
			return m.loadSession(sessions[0].ID)()
		}
	default:
		return nil
	}
}

// sendNotification returns a command that sends a notification if allowed by policy.
func (m *UI) sendNotification(n notification.Notification) tea.Cmd {
	if !m.shouldSendNotification() {
		return nil
	}

	return m.notifyBackend.Send(n)
}

// selectNotificationBackend chooses the appropriate notification backend based
// on terminal capabilities, environment, and user configuration. This is a pure
// function that should be called once during initialization or when capabilities
// change.
func selectNotificationBackend(caps common.Capabilities, cfg *config.Config) notification.Backend {
	// Check for explicit user preference first.
	if cfg != nil && cfg.Options != nil && cfg.Options.Notifications != "" {
		switch cfg.Options.Notifications {
		case "native":
			if !notification.NativeSupported {
				slog.Debug("Native notifications unavailable on this platform; using OSC backend", "osc99_supported", caps.OSC99Notifications)
				return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
			}
			slog.Debug("Using native backend (user preference)")
			return notification.NewNativeBackend(notification.Icon)
		case "osc":
			slog.Debug("Using OSC backend (user preference)", "osc99_supported", caps.OSC99Notifications)
			return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
		case "bell":
			slog.Debug("Using bell backend (user preference)")
			return notification.NewBellBackend()
		case "disabled":
			slog.Debug("Notifications disabled (user preference)")
			return notification.NoopBackend{}
		case "auto":
			// Fall through to auto-detection below.
		default:
			slog.Warn("Unknown notification style, using auto", "style", cfg.Options.Notifications)
		}
	}

	// Auto-detect based on environment and capabilities.
	_, isSSH := caps.Env.LookupEnv("SSH_TTY")

	// SSH sessions use terminal-based notifications (OSC 99 or 777).
	if isSSH {
		slog.Debug("Selected OSCBackend for SSH session", "osc99_supported", caps.OSC99Notifications)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Local sessions: prefer OSC on macOS because the native backend (beeep)
	// uses terminal-notifier or AppleScript, which is slow and doesn't display
	// icons properly. Also prefer OSC where native notifications are unavailable
	// (illumos/solaris). OSC 99 provides a polished experience with icon support.
	if runtime.GOOS == "darwin" || !notification.NativeSupported {
		slog.Debug("Selected OSCBackend for local session", "osc99_supported", caps.OSC99Notifications, "native_supported", notification.NativeSupported)
		return notification.NewOSCBackend(notification.Icon, caps.OSC99Notifications)
	}

	// Non-macOS local sessions use native OS notifications if focus events are supported.
	// Without focus events, we can't suppress notifications when focused, so
	// we disable them entirely to avoid spamming the user.
	if caps.ReportFocusEvents {
		slog.Debug("Selected NativeBackend for local session")
		return notification.NewNativeBackend(notification.Icon)
	}

	slog.Debug("Selected NoopBackend (focus events not supported)")
	return notification.NoopBackend{}
}

func (m *UI) updateNotificationBackend() {
	cfg := m.com.Config()
	m.notifyBackend = selectNotificationBackend(m.caps, cfg)
}

// shouldSendNotification returns true if notifications should be sent based on
// current state. Focus reporting must be supported, window must not be
// focused, and notifications must not be disabled in config.
func (m *UI) shouldSendNotification() bool {
	cfg := m.com.Config()
	if cfg != nil && cfg.Options != nil && cfg.Options.Notifications == "disabled" {
		return false
	}
	return m.caps.ReportFocusEvents && !m.notifyWindowFocused
}

// setState changes the UI state and focus.
func (m *UI) setState(state uiState, focus uiFocusState) {
	if state == uiLanding {
		// Always turn off compact mode when going to landing
		m.isCompact = false
	}
	m.state = state
	m.focus = focus
	// Changing the state may change layout, so update it.
	m.updateLayoutAndSize()
}

// loadCustomCommands loads the custom commands asynchronously.
func (m *UI) loadCustomCommands() tea.Cmd {
	return func() tea.Msg {
		customCommands, err := commands.LoadCustomCommands(m.com.Config())
		if err != nil {
			slog.Error("Failed to load custom commands", "error", err)
		}
		// Append user-invocable skills as commands.
		skillEntries, err := m.com.Workspace.ListSkills(context.Background())
		if err != nil {
			slog.Error("Failed to load skill commands", "error", err)
		}
		customCommands = append(customCommands, commands.FromSkillCatalog(skillEntries)...)
		return userCommandsLoadedMsg{Commands: customCommands}
	}
}

// applyChatScroll scrolls the chat by lines and, if the selection is then
// outside the viewport, moves it to the nearest visible edge. The selection
// is moved rather than scrolled to so a large coalesced delta is applied in
// full instead of being rewound to the selected item.
func (m *UI) applyChatScroll(lines int) {
	m.chat.ScrollBy(lines)
	if m.chat.SelectedItemInView() {
		return
	}
	if lines > 0 && m.chat.AtBottom() {
		m.chat.SelectLast()
		return
	}
	m.chat.SelectNearestInView(lines < 0)
}

// Update handles updates to the UI model, and keeps the yolo shockwave's
// frames coming while it plays.
func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if t, ok := msg.(yoloTickMsg); ok {
		if m.yoloFX.Advance(time.Time(t)) {
			return m, yoloTick()
		}
		m.yoloTicking = false
		return m, nil
	}
	if _, ok := msg.(workTickMsg); ok {
		if len(m.busyAgents()) > 0 {
			return m, workTick()
		}
		m.workTicking = false
		return m, nil
	}
	if t, ok := msg.(toastTickMsg); ok {
		return m, m.handleToastTick(t)
	}
	model, cmd := m.update(msg)
	if c := m.scheduleToastTick(time.Now()); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if !m.yoloTicking && m.yoloFX.Advance(time.Now()) {
		m.yoloTicking = true
		cmd = tea.Batch(cmd, yoloTick())
	}
	if !m.workTicking && len(m.busyAgents()) > 0 {
		m.workTicking = true
		cmd = tea.Batch(cmd, workTick())
	}
	return model, cmd
}

// yoloTickMsg asks for the yolo shockwave's next frame.
type yoloTickMsg time.Time

func yoloTick() tea.Cmd {
	return tea.Tick(time.Second/30, func(t time.Time) tea.Msg { return yoloTickMsg(t) })
}

func (m *UI) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	m.beginFrameUpdate()
	// Update terminal capabilities
	m.caps.Update(msg)
	switch msg := msg.(type) {
	case tea.EnvMsg:
		// Is this Windows Terminal?
		if !m.sendProgressBar {
			m.sendProgressBar = slices.Contains(msg, "WT_SESSION")
		}
		cmds = append(cmds, common.QueryCmd(uv.Environ(msg)))
	case tea.ModeReportMsg:
		m.updateNotificationBackend()
	case uv.UnknownOscEvent:
		m.updateNotificationBackend()
	case tea.FocusMsg:
		if m.windowBlurred() {
			// Catch up on the git reads skipped while blurred.
			cmds = append(cmds, m.fetchGitBranch(), m.gitPoll())
		}
		m.notifyWindowFocused = true
		m.turnOutcome = ""
	case tea.BlurMsg:
		m.notifyWindowFocused = false
	case dialog.CollapseInlineMsg:
		m.focusActiveInline(uiFocusMain)
	case pubsub.Event[notify.Notification]:
		if cmd := m.handleAgentNotification(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[notify.RunComplete]:
		if cmd := m.handlePlanHandoff(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case workspace.LimitsChangedMsg:
		// An agent's plan limits moved: Deck's mark shows them.
		m.header.invalidateMark()
	case themeSwitchedMsg:
		cmds = append(cmds, m.handleThemeSwitched(msg))
	case themeCreatedMsg:
		cmds = append(cmds, m.handleThemeCreated(msg))
	case themeRenamedMsg:
		cmds = append(cmds, m.handleThemeRenamed(msg))
	case themeDeletedMsg:
		cmds = append(cmds, m.handleThemeDeleted(msg))
	case busyStateMsg:
		cmds = append(cmds, m.applyBusyState(msg)...)
	case teamSnapMsg:
		if cmd := m.applyTeamSnap(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case sharedMirrorMsg:
		if cmd := m.applySharedMirror(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case sharedRetryMsg:
		if cmd := m.applySharedRetry(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case capsulesMsg:
		m.applyCapsules(msg)
	case capsuleShareMsg:
		cmds = append(cmds, m.shareCapsule(msg))
	case capsuleSharedMsg:
		cmds = append(cmds, m.applyCapsuleShared(msg))
	case teamChatSentMsg:
		if cmd := m.applyTeamChatSent(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case focusPaneMsg:
		cmds = append(cmds, m.focusOrOpenPane(msg.id))
	case promptQueueMsg:
		cmds = append(cmds, m.applyPromptQueue(msg)...)
	case leadMsg:
		cmds = append(cmds, m.applyLead(msg))
	case leadPickedMsg:
		// The pick is in: read the lead as the engine has it.
		m.leadPicking = false
		m.leadCheckedAt = time.Time{}
		cmds = append(cmds, util.CmdHandler(msg.info))
	case agentModelChangedMsg:
		// The coordinator model changed (selection, thinking, reasoning):
		// re-fetch the memoized ready/model state off-thread.
		m.invalidateBusyCaches()
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case agentRunSubmittedMsg:
		m.turnOutcome = ""
		// A first message makes the lead: read it again.
		m.leadCheckedAt = time.Time{}
		// A prompt was just accepted (run started or enqueued): fetch the
		// authoritative busy/queue state to confirm the optimistic values
		// sendMessage wrote.
		m.invalidateBusyCaches()
		m.invalidatePromptQueue()
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case loadSessionMsg:
		if m.forceCompactMode {
			m.isCompact = true
		}
		// Plan mode is scoped to the session it was enabled in: switching
		// to another session falls back to code mode and drops any pending
		// plan handoff. (Loading the session that was just created for the
		// first plan-mode prompt is not a switch; the IDs match then.)
		if m.session == nil || m.session.ID != msg.session.ID {
			if cmd := m.resetPlanModeState(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		m.setState(uiChat, m.focus)
		m.session = msg.session
		m.strip().scroll = map[paneID]int{}
		m.sessionFiles = msg.files
		// Session switch: the memoized busy state and queued prompts
		// belong to the previous session. Drop them and re-fetch
		// off-thread so the queue pill and esc behavior track the new
		// session instead of a stale one.
		m.invalidateBusyCaches()
		m.invalidatePromptQueue()
		m.promptQueue = 0
		m.promptQueueItems = nil
		m.promptQueueCheckedAt = time.Time{}
		if cmd := m.dispatchBusyRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		msgs := msg.messages
		if cmd := m.setSessionMessagesNested(msgs, msg.nested); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Render the transcript into the cache a batch at a time so the
		// first scroll does not render it all at once.
		if cmd := m.chat.Warm(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.restoreModelFromSession(msgs); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.autoExpandPillsIfReasonable(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// If a bang command was issued before the session finished
		// loading, start it now that the chat list is stable.
		if m.pendingBangCommand != "" {
			cmds = append(cmds, m.runShellCommandInternal(m.pendingBangCommand, true))
			m.pendingBangCommand = ""
		}
		if hasInProgressTodo(m.session.Todos) {
			// only start spinner if there is an in-progress todo
			if m.isAgentBusy() {
				m.todoIsSpinning = true
				cmds = append(cmds, m.todoSpinner.Tick)
			}
			m.updateLayoutAndSize()
		}
		// Reload prompt history for the new session.
		m.historyReset()
		cmds = append(cmds, m.loadPromptHistory())
		m.updateLayoutAndSize()

	case sessionFilesUpdatesMsg:
		m.sessionFiles = msg.sessionFiles

	case modeSwitchedMsg:
		m.modeSwitching = false
		cmds = append(cmds, m.applyModeSwitch(msg)...)

	case sendMessageMsg:
		cmds = append(cmds, m.sendMessage(msg.Content, msg.Attachments...))

	case userCommandsLoadedMsg:
		m.customCommands = msg.Commands
		dia := m.dialog.Dialog(dialog.CommandsID)
		if dia == nil {
			break
		}

		commands, ok := dia.(*dialog.Commands)
		if ok {
			commands.SetCustomCommands(m.customCommands)
		}

	case promptHistoryLoadedMsg:
		m.promptHistory.messages = msg.messages
		m.promptHistory.index = -1
		m.promptHistory.draft = ""

	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.DeletedEvent {
			if m.session != nil && m.session.ID == msg.Payload.ID {
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			break
		}
		if m.session != nil && msg.Payload.ID == m.session.ID {
			prevHasInProgress := hasInProgressTodo(m.session.Todos)
			prevPillsHeight := m.pillsAreaHeight()
			m.session = &msg.Payload
			m.header.invalidateMark() // the agents' context may have moved
			if !prevHasInProgress && hasInProgressTodo(m.session.Todos) {
				m.todoIsSpinning = true
				cmds = append(cmds, m.todoSpinner.Tick)
			}
			// The pills panel reserves vertical space that the chat area
			// must yield. Recompute the layout whenever that footprint
			// changes (todos appearing, the list growing, etc.) so the
			// box renders on first paint rather than waiting for a toggle.
			// When the footprint is unchanged we still re-render the pill
			// content so status changes (e.g. the in-progress spinner)
			// show up.
			if m.pillsAreaHeight() != prevPillsHeight {
				m.updateLayoutAndSize()
			} else {
				m.renderPills()
			}
			m.autoExpandPillsIfReasonable()
		}
	case pubsub.Event[message.Message]:
		// Check if this is a child session message for an agent tool.
		if m.session == nil {
			break
		}
		if msg.Payload.SessionID != m.session.ID {
			// This might be a child session message from an agent tool.
			if cmd := m.handleChildSessionMessage(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			break
		}
		switch msg.Type {
		case pubsub.CreatedEvent:
			cmds = append(cmds, m.appendSessionMessage(msg.Payload))
			// A new message is a run boundary — a user prompt starting
			// a turn or the agent replying/dequeueing. Drop the
			// memoized busy state and re-fetch it and the queue
			// off-thread. Per-chunk UpdatedEvents deliberately do NOT
			// trigger this: during streaming that would put workspace
			// probes on every token.
			m.invalidateBusyCaches()
			m.invalidatePromptQueue()
			if cmd := m.dispatchBusyRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		case pubsub.UpdatedEvent:
			cmds = append(cmds, m.updateSessionMessage(msg.Payload))
		case pubsub.DeletedEvent:
			m.chat.RemoveMessage(msg.Payload.ID)
		}
		// start the spinner if there is a new message
		if hasInProgressTodo(m.session.Todos) && m.isAgentBusy() && !m.todoIsSpinning {
			m.todoIsSpinning = true
			cmds = append(cmds, m.todoSpinner.Tick)
		}
		// stop the spinner if the agent is not busy anymore
		if m.todoIsSpinning && !m.isAgentBusy() {
			m.todoIsSpinning = false
		}
		// there is a number of things that could change the pills here so we want to re-render
		m.renderPills()
	case pubsub.Event[history.File]:
		cmds = append(cmds, m.handleFileEvent(msg.Payload))
	case pubsub.Event[skills.Event]:
		m.skillStates = msg.Payload.States
	case pubsub.Event[permission.PermissionRequest]:
		if cmd := m.openPermissionsDialog(msg.Payload); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.noticeFromPermission(msg.Payload)
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Deck is waiting...",
			Message: fmt.Sprintf("Permission required to execute \"%s\"", msg.Payload.ToolName),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[permission.PermissionNotification]:
		m.handlePermissionNotification(msg.Payload)
	case pubsub.Event[question.Request]:
		m.openBatchFormDialog(msg.Payload)
		m.chat.ScrollToBottom()
		if cmd := m.sendNotification(notification.Notification{
			Title:   "Deck is waiting...",
			Message: fmt.Sprintf("%d questions need your input", len(msg.Payload.Questions)),
		}); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case pubsub.Event[question.Notification]:
		m.handleQuestionNotification(msg.Payload)
	case cancelTimerExpiredMsg:
		m.isCanceling = false
	case tea.TerminalVersionMsg:
		termVersion := strings.ToLower(msg.Name)
		// Only enable progress bar for the following terminals.
		if !m.sendProgressBar {
			m.sendProgressBar = xstrings.ContainsAnyOf(termVersion, "ghostty", "iterm2", "rio")
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Suppress the chat's full-height scan during the resize so a drag
		// only reflows visible items; it settles (and recomputes) shortly
		// after the last resize event.
		if m.state == uiChat {
			cmds = append(cmds, m.chat.BeginResize())
		}
		m.updateLayoutAndSize()
		if m.state == uiChat && m.chat.Follow() {
			m.chat.ScrollToBottom()
		}
	case tea.KeyboardEnhancementsMsg:
		m.keyenh = msg
		if msg.SupportsKeyDisambiguation() {
			m.keyMap.Models.SetHelp("ctrl+m", "agent")
			m.keyMap.Editor.Newline.SetHelp("shift+enter", "newline")
		}
	case copyChatHighlightMsg:
		cmds = append(cmds, m.copyChatHighlight())
	case DelayedClickMsg:
		// Handle delayed single-click action (e.g., expansion).
		m.chat.HandleDelayedClick(msg)
	case agentModelChoicesMsg:
		if cmd := m.openAgentModels(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case usagePaneMsg:
		cmds = append(cmds, m.setUsagePane(msg))
		return m, tea.Batch(cmds...)
	case usagePaneTickMsg:
		cmds = append(cmds, m.usagePaneTick())
		return m, tea.Batch(cmds...)

	case logoTickMsg:
		if cmd := m.header.logoFrameAt(time.Time(msg)); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case tea.ClipboardMsg:
		// The terminal's answer to an OSC 52 read; only the Add a teammate
		// wizard asks for one (v, with no system clipboard), and it ignores
		// one it did not ask for.
		if cmd := m.handleDialogAction(m.dialog.UpdateDialog(dialog.PeerPairID, dialog.PairClipboardMsg{Content: msg.Content})); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case dialog.TargetedMsg:
		// Work a dialog started reports back to it even when another
		// dialog is in front; it is dropped once that dialog closed.
		if cmd := m.handleDialogAction(m.dialog.UpdateDialog(msg.TargetDialog(), msg)); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case tea.MouseClickMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		// A click on a toast dismisses it.
		if msg.Button == uv.MouseLeft && m.clickToast(msg.X, msg.Y) {
			return m, tea.Batch(cmds...)
		}
		if m.state == uiLanding {
			if cmd := m.header.logoMouse(msg.X, msg.Y, true, time.Now()); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		// Route clicks to inline editors that support mouse interaction.
		if m.activeInline != nil {
			if selectable, ok := m.activeInline.(dialog.MouseSelectableEditor); ok &&
				selectable.HandleMouseDown(msg.X, msg.Y) {
				return m, tea.Batch(cmds...)
			}
			if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
				if done, handled := clickable.HandleMouseClick(msg.X, msg.Y); handled {
					if done {
						prev := m.activeInline
						m.activeInline = nil
						m.textarea.Focus()
						m.updateLayoutAndSize()
						if cod, ok := prev.(dialog.CmdOnDone); ok {
							if c := cod.PendingCmd(); c != nil {
								cmds = append(cmds, c)
							}
						}
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		if cmd := m.handleClickFocus(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

		// Check if the click landed on an attachment's remove button.
		// The attachment chips are rendered on the first row of the
		// editor layout area, above the textarea.
		if m.activeInline == nil && msg.Button == uv.MouseLeft && len(m.attachments.List()) > 0 && msg.Y == m.layout.editor.Min.Y {
			relX := msg.X - m.layout.editor.Min.X
			if m.attachments.HandleClick(relX) {
				return m, tea.Batch(cmds...)
			}
		}

		// Forward clicks within the textarea region to the textarea so it
		// can position the cursor and start a selection.
		if m.activeInline == nil {
			if handled, cmd := m.forwardMouseToTextarea(msg); handled {
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			if !image.Pt(msg.X, msg.Y).In(m.layout.sidebar) {
				if handled, cmd := m.chat.HandleMouseDown(x, y); handled {
					m.lastClickTime = time.Now()
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}

	case tea.MouseMotionMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}
		// Toasts hold still under the pointer. While they are up the
		// terminal reports every move (see View); a move with no button
		// held is only that, so it stops here unless something else
		// asked for hover.
		hoverChanged := m.hoverToast(msg.X, msg.Y)
		if m.notices.toasts.Len() > 0 && msg.Button == uv.MouseNone && m.activeInline == nil && m.state != uiLanding {
			if !hoverChanged {
				// A move that holds no new toast changes nothing on
				// screen; keep the memoized frame.
				m.markScrollOnly()
			}
			return m, tea.Batch(cmds...)
		}
		if m.state == uiLanding {
			if cmd := m.header.logoMouse(msg.X, msg.Y, false, time.Now()); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		// Track hover position for inline editors.
		if m.activeInline != nil {
			if selectable, ok := m.activeInline.(dialog.MouseSelectableEditor); ok &&
				selectable.HandleMouseDrag(msg.X, msg.Y) {
				return m, tea.Batch(cmds...)
			}
			if m.hoverX != msg.X || m.hoverY != msg.Y {
				m.hoverX = msg.X
				m.hoverY = msg.Y
				if clickable, ok := m.activeInline.(dialog.MouseClickableEditor); ok {
					clickable.SetHover(msg.X, msg.Y)
				}
			}
		}

		// While a mouse selection is in progress in the textarea, forward
		// motion events to it and skip chat drag handling.
		if m.activeInline == nil && m.textareaMouseSelecting {
			if handled, cmd := m.forwardMouseToTextarea(msg); handled {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			// Skip chat edge-scrolling when an inline editor is
			// active to prevent accidental scrolling while hovering
			// over question forms or other inline components.
			if m.activeInline == nil || m.focus != uiFocusEditor {
				if msg.Y <= 0 {
					m.chat.ScrollBy(-1)
					if !m.chat.SelectedItemInView() {
						m.chat.SelectPrev()
						m.chat.ScrollToSelected()
					}
				} else if msg.Y >= m.chat.Height()-1 {
					m.chat.ScrollBy(1)
					if !m.chat.SelectedItemInView() {
						m.chat.SelectNext()
						m.chat.ScrollToSelected()
					}
				}
			}

			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			m.chat.HandleMouseDrag(x, y)
		}

	case tea.MouseReleaseMsg:
		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		if m.activeInline != nil {
			if selectable, ok := m.activeInline.(dialog.MouseSelectableEditor); ok {
				if handled, cmd := selectable.HandleMouseRelease(msg.X, msg.Y); handled {
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
					return m, tea.Batch(cmds...)
				}
			}
		}

		// End any in-progress textarea mouse selection.
		if m.activeInline == nil && m.textareaMouseSelecting {
			m.textareaMouseSelecting = false
			if handled, cmd := m.forwardMouseToTextarea(msg); handled {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		switch m.state {
		case uiChat:
			x, y := msg.X, msg.Y
			// Adjust for chat area position
			x -= m.layout.main.Min.X
			y -= m.layout.main.Min.Y
			if m.chat.HandleMouseUp(x, y) && m.chat.HasHighlight() {
				cmds = append(cmds, tea.Tick(doubleClickThreshold, func(t time.Time) tea.Msg {
					if time.Since(m.lastClickTime) >= doubleClickThreshold {
						return copyChatHighlightMsg{}
					}
					return nil
				}))
			}
		}
	case common.CoalescedWheelMsg:
		// Route wheel events to active inline editor only when the
		// mouse is over the editor area, so scrolling over the chat
		// still scrolls the chat.
		if m.activeInline != nil && image.Pt(msg.Mouse.X, msg.Mouse.Y).In(m.layout.editor) {
			if we, ok := m.activeInline.(common.WheelScrollable); ok {
				we.HandleWheel(msg.DeltaX, msg.DeltaY)
				return m, tea.Batch(cmds...)
			}
		}

		// Pass mouse events to dialogs first if any are open.
		if m.dialog.HasDialogs() {
			m.dialog.Update(msg)
			return m, tea.Batch(cmds...)
		}

		// Otherwise handle mouse wheel for chat. Use the coalesced delta
		// directly as the line count. Terminals like Ghostty send DeltaY=3
		// per physical wheel tick (matching their native scrollback), while
		// others send DeltaY=1.
		switch m.state {
		case uiChat:
			// Over the sidebar the wheel scrolls its panes (strip_keys.go).
			if m.stripActive() && image.Pt(msg.Mouse.X, msg.Mouse.Y).In(m.layout.sidebar) {
				m.handleStripWheel(msg)
				break
			}
			if msg.DeltaX != 0 {
				m.chat.ScrollSelectedShellHorizontal(int(msg.DeltaX))
			}
			lines := int(msg.DeltaY)
			if lines == 0 {
				break
			}
			m.markScrollOnly()
			m.applyChatScroll(lines)
		}
	case frameGCMsg:
		m.handleFrameGC()
	case animTickMsg:
		if cmd := m.handleAnimTick(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case scrollbarHideMsg:
		if m.state == uiChat {
			m.chat.HideScrollbar(msg.seq)
		}
	case chatWarmMsg:
		// A resize has settled (or a session loaded, or the theme changed);
		// warm the message cache one batch at a time so the scrollbar
		// recompute never blocks the UI thread.
		if m.state == uiChat {
			cmd, done := m.chat.WarmStep(msg.seq)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if done {
				// Heights are cached now, so the final layout pass (scrollbar
				// reservation) is cheap.
				m.updateLayoutAndSize()
			} else {
				// Rendering off-screen messages into the cache changes
				// nothing on screen; keep the memoized frame.
				m.markScrollOnly()
			}
		}
	case stripSaveMsg:
		if cmd := m.handleStripSave(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case spinner.TickMsg:
		if m.dialog.HasDialogs() {
			// route to dialog
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.state == uiChat && m.hasSession() && hasInProgressTodo(m.session.Todos) && m.todoIsSpinning {
			var cmd tea.Cmd
			m.todoSpinner, cmd = m.todoSpinner.Update(msg)
			if cmd != nil {
				m.renderPills()
				cmds = append(cmds, cmd)
			}
		}

	case tea.KeyPressMsg:
		if m.state == uiLanding {
			m.header.anim.Calm(time.Now())
		}
		if cmd := m.handleKeyPressMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.PasteMsg:
		if m.activeInline != nil && m.focus == uiFocusEditor {
			if p, ok := m.activeInline.(dialog.PasteableEditor); ok {
				if cmd := p.HandlePaste(msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
		}
		if cmd := m.handlePasteMsg(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case openEditorMsg:
		prevHeight := m.textarea.Height()
		m.textarea.SetValue(msg.Text)
		m.textarea.MoveToEnd()
		m.syncBangModeFromTextarea()
		cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
	case shellStreamMsg:
		if item := m.chat.MessageItem(msg.PendingID); item != nil {
			if shellItem, ok := item.(*chat.ShellItem); ok {
				shellItem.AppendOutput(msg.Chunk)
				m.chat.ScrollToBottom()
			}
		}
		// Continue draining the stream channel.
		if msg.streamCh != nil {
			ch := msg.streamCh
			pid := msg.PendingID
			cmds = append(cmds, func() tea.Msg {
				chunk, ok := <-ch
				if !ok {
					return nil
				}
				return shellStreamMsg{PendingID: pid, Chunk: chunk, streamCh: ch}
			})
		}
	case shellResultMsg:
		// Clear the bang cancel func — command is done.
		if m.bangCancel != nil {
			m.bangCancel()
			m.bangCancel = nil
		}
		// Complete the pending shell item if it exists, otherwise create a new one.
		completed := false
		if msg.PendingID != "" {
			if item := m.chat.MessageItem(msg.PendingID); item != nil {
				if shellItem, ok := item.(*chat.ShellItem); ok {
					shellItem.Complete(msg.Output, msg.ExitCode)
					m.chat.ScrollToBottom()
					completed = true
				}
			}
		}
		if !completed {
			item := chat.NewShellItem(m.com.Styles, msg.Command, msg.Output, msg.ExitCode)
			m.chat.AppendMessages(item)
			m.chat.ScrollToBottom()
		}
		cmds = append(cmds, m.loadPromptHistory())
	case gitBranchUpdatedMsg:
		if msg.branch == m.gitBranch {
			// The 5s poll confirmed the branch; nothing to redraw.
			m.markScrollOnly()
		}
		m.gitBranch = msg.branch
	case gitBranchPollMsg:
		// The tick itself only starts reads; their results redraw if
		// they change anything.
		if cli := m.git.client; cli != nil && cli == m.gitClient() {
			m.markScrollOnly()
		}
		cmds = append(cmds, m.gitBranchTicker())
		if m.windowBlurred() {
			// Nobody is looking: skip the local branch and status reads
			// (FocusMsg catches them up), but keep asking GitHub, whose
			// finished checks ring the bell.
			m.gitClient()
			cmds = append(cmds, m.gitMaybePollPR(time.Now()))
		} else {
			cmds = append(cmds, m.fetchGitBranch(), m.gitPoll())
		}
	case gitMsg:
		cmds = append(cmds, m.handleGitMsg(msg))
	case util.InfoMsg:
		if msg.Type == util.InfoTypeError {
			slog.Error("Error reported", "error", msg.Msg)
		}
		m.status.SetInfoMsg(msg)
		m.noticeFromInfo(msg)
		ttl := msg.TTL
		if ttl <= 0 {
			ttl = DefaultStatusTTL
		}
		cmds = append(cmds, clearInfoMsgCmd(ttl))
	case notice.Msg:
		m.Notify(msg.Notice)
	case JobStartedMsg:
		m.jobStarted(msg.ID, msg.Label)
	case JobDoneMsg:
		m.jobDone(msg.ID, msg.Err)
	case app.UpdateAvailableMsg:
		text := fmt.Sprintf("Deck update available: v%s → v%s.", msg.CurrentVersion, msg.LatestVersion)
		if msg.IsDevelopment {
			text = fmt.Sprintf("This is a development version of Deck. The latest version is v%s.", msg.LatestVersion)
		}
		ttl := 10 * time.Second
		m.status.SetInfoMsg(util.InfoMsg{
			Type: util.InfoTypeUpdate,
			Msg:  text,
			TTL:  ttl,
		})
		m.Notify(notice.Notice{Level: notice.Info, Source: notice.SourceDeck, Body: text})
		cmds = append(cmds, clearInfoMsgCmd(ttl))
	case workspace.ConnectionEvent:
		cmds = append(cmds, m.handleConnectionEvent(msg)...)
	case util.ClearStatusMsg:
		m.status.ClearInfoMsg()
	case agentCompletionsMsg:
		m.refreshAgentCompletions()
	case profilesLoadedMsg:
		m.applyProfiles(msg)
	case completions.CompletionItemsLoadedMsg:
		if m.completionsOpen && m.completionsFiles {
			m.completions.SetItems(msg.Files)
			if m.completionsQuery != "" {
				m.completions.Filter(m.completionsQuery)
			}
		}
	case uv.KittyGraphicsEvent:
		if !bytes.HasPrefix(msg.Payload, []byte("OK")) {
			slog.Warn("Unexpected Kitty graphics response",
				"response", string(msg.Payload),
				"options", msg.Options)
		}
	default:
		if m.dialog.HasDialogs() {
			if cmd := m.handleDialogMsg(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// This logic gets triggered on any message type, but should it?
	prevPlaceholder := m.textarea.Placeholder
	switch m.focus {
	case uiFocusMain:
	case uiFocusEditor:
		// Textarea placeholder logic
		if m.bangMode {
			m.textarea.Placeholder = "Run a shell command"
		} else if m.isAgentBusy() {
			m.textarea.Placeholder = m.workingPlaceholder
		} else if m.mode == uiInputModePlan {
			m.textarea.Placeholder = "Let's plan"
		} else {
			m.textarea.Placeholder = m.readyPlaceholder
		}
		if !m.bangMode && m.mode != uiInputModePlan && m.yoloModeCached() {
			m.textarea.Placeholder = "Yolo: everything the agents ask is approved"
		}
	}
	if m.textarea.Placeholder != prevPlaceholder {
		m.invalidateFrames()
	}

	// TTL backstop: schedule an off-thread re-probe for any memoized
	// workspace state that has gone stale. Never does IO on this
	// goroutine.
	cmds = append(cmds, m.staleWorkspaceRefreshCmds()...)

	// at this point this can only handle [message.Attachment] message, and we
	// should return all cmds anyway.
	if m.attachments.Update(msg) {
		m.invalidateFrames()
	}
	// Any update may have put a spinner on screen (new message, tool update,
	// scroll, session load); make sure the clock is running. This is the
	// sole place the clock is armed so a tick never sits inside a caller's
	// tea.Sequence.
	if m.state == uiChat {
		if cmd := m.chat.EnsureAnimating(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.chatWarmPending {
		// A theme change dropped every message's render; re-render them
		// a batch at a time rather than all at once on the first scroll.
		m.chatWarmPending = false
		if cmd := m.chat.Warm(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.endFrameUpdate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleAnimTick advances every visible spinner by one frame. A tick that
// changed nothing visible is scroll-only so the frame cache survives; one
// that did keeps the view pinned to the bottom while following, since
// animated items can change height.
func (m *UI) handleAnimTick(msg animTickMsg) tea.Cmd {
	if m.state != uiChat {
		m.chat.stopAnimating(msg)
		m.markScrollOnly()
		return nil
	}
	changed, cmd := m.chat.Tick(msg)
	if !changed {
		m.markScrollOnly()
		return cmd
	}
	if m.chat.Follow() {
		m.chat.ScrollToBottom()
	}
	return cmd
}

// setSessionMessages sets the messages for the current session in the chat
func (m *UI) setSessionMessages(msgs []message.Message) tea.Cmd {
	return m.setSessionMessagesNested(msgs, nil)
}

// setSessionMessagesNested is setSessionMessages with the nested agent
// transcripts the load command already read (see loadNestedMessages),
// keyed by agent tool session ID. Transcripts missing from nested are
// read here.
func (m *UI) setSessionMessagesNested(msgs []message.Message, nested map[string][]message.Message) tea.Cmd {
	var cmds []tea.Cmd
	// Build tool result map to link tool calls with their results
	msgPtrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolResultMap := chat.BuildToolResultMap(msgPtrs)
	if len(msgPtrs) > 0 {
		m.lastUserMessageTime = msgPtrs[0].CreatedAt
	}

	// Add messages to chat with linked tool results
	items := make([]chat.MessageItem, 0, len(msgs)*2)
	for _, msg := range msgPtrs {
		switch msg.Role {
		case message.User:
			m.lastUserMessageTime = msg.CreatedAt
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir())...)
		case message.Assistant:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir())...)
			if chat.ShouldShowAssistantInfo(msg) {
				infoItem := chat.NewAssistantInfoItem(m.com.Styles, msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
				items = append(items, infoItem)
			}
		default:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir())...)
		}
	}

	// Load nested tool calls for agent/agentic_fetch tools.
	m.loadNestedToolCalls(items, nested)
	m.setMessagePlanFlags(items)

	// If the user switches between sessions while the agent is working we
	// want to make sure the animations are shown. Gate on the agent actually
	// being busy: a session that was killed mid-generation can persist an
	// assistant message with no Finish part, which still reports Spinning()
	// even though nothing is running. Allowing the clock for it here would
	// leave a ghost "working" spinner (and a second one alongside any tool
	// spinner) after the session is reloaded. Messages arriving for the
	// session re-enable the clock.
	m.chat.SetAnimationsAllowed(m.isAgentBusy())

	if cmd := m.chat.SetMessages(items...); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.chat.SelectLast()
	return tea.Sequence(cmds...)
}

// handleConnectionEvent reports the health of the client-server link and,
// once it recovers, reloads the open session. A reload is always needed
// after a degraded episode: events published while the stream was down are
// gone, and if the workspace itself was re-created any run died with it.
func (m *UI) handleConnectionEvent(msg workspace.ConnectionEvent) []tea.Cmd {
	info := util.InfoMsg{
		Type: util.InfoTypeWarn,
		Msg:  "Lost connection to the Deck server — reconnecting…",
		TTL:  30 * time.Second,
	}
	switch msg.State {
	case workspace.ConnectionDegraded:
		slog.Warn("Server connection degraded", "error", msg.Err, "stuck", msg.Stuck)
		if msg.Stuck {
			info.Type = util.InfoTypeError
			info.Msg = "Can't restore the connection to the Deck server. Restart Deck to recover."
			info.TTL = time.Minute
		}
	case workspace.ConnectionRecovered:
		info = util.InfoMsg{
			Type: util.InfoTypeSuccess,
			Msg:  "Reconnected to the Deck server.",
			TTL:  DefaultStatusTTL,
		}
	}
	m.status.SetInfoMsg(info)
	cmds := []tea.Cmd{clearInfoMsgCmd(info.TTL)}
	if msg.State == workspace.ConnectionRecovered {
		if m.session != nil {
			cmds = append(cmds, m.loadSession(m.session.ID))
		}
	}
	return cmds
}

// loadNestedToolCalls recursively loads nested tool calls for
// agent/agentic_fetch tools. Transcripts are taken from nested, keyed by
// agent tool session ID, when the load command already read them; only
// the ones it did not are read here, on the event loop.
func (m *UI) loadNestedToolCalls(items []chat.MessageItem, nested map[string][]message.Message) {
	for _, item := range items {
		nestedContainer, ok := item.(chat.NestedToolContainer)
		if !ok {
			continue
		}
		toolItem, ok := item.(chat.ToolMessageItem)
		if !ok {
			continue
		}

		tc := toolItem.ToolCall()
		messageID := toolItem.MessageID()

		// Get the agent tool session ID.
		agentSessionID := m.com.Workspace.CreateAgentToolSessionID(messageID, tc.ID)

		// Fetch nested messages.
		nestedMsgs, ok := nested[agentSessionID]
		if !ok {
			var err error
			nestedMsgs, err = m.com.Workspace.ListMessages(context.Background(), agentSessionID)
			if err != nil {
				continue
			}
		}
		if len(nestedMsgs) == 0 {
			continue
		}

		// Build tool result map for nested messages.
		nestedMsgPtrs := make([]*message.Message, len(nestedMsgs))
		for i := range nestedMsgs {
			nestedMsgPtrs[i] = &nestedMsgs[i]
		}
		nestedToolResultMap := chat.BuildToolResultMap(nestedMsgPtrs)

		// Extract nested tool items.
		var nestedTools []chat.ToolMessageItem
		for _, nestedMsg := range nestedMsgPtrs {
			nestedItems := chat.ExtractMessageItems(m.com.Styles, nestedMsg, nestedToolResultMap, m.com.Workspace.WorkingDir())
			for _, nestedItem := range nestedItems {
				if nestedToolItem, ok := nestedItem.(chat.ToolMessageItem); ok {
					// Mark nested tools as simple (compact) rendering.
					if simplifiable, ok := nestedToolItem.(chat.Compactable); ok {
						simplifiable.SetCompact(true)
					}
					nestedTools = append(nestedTools, nestedToolItem)
				}
			}
		}

		// Recursively load nested tool calls for any agent tools within.
		nestedMessageItems := make([]chat.MessageItem, len(nestedTools))
		for i, nt := range nestedTools {
			nestedMessageItems[i] = nt
		}
		m.loadNestedToolCalls(nestedMessageItems, nested)

		// Set nested tools on the parent.
		nestedContainer.SetNestedTools(nestedTools)
	}
}

// setMessagePlanFlags marks assistant message items as plan-agent output
// while the UI is in plan mode, so their streaming content renders as the
// open plan card. Finished non-plan messages ignore the flag, so reloading
// an old session in plan mode never grows spurious cards.
func (m *UI) setMessagePlanFlags(items []chat.MessageItem) {
	if m.mode != uiInputModePlan {
		return
	}
	for _, item := range items {
		if a, ok := item.(*chat.AssistantMessageItem); ok {
			a.SetPlanAgent(true)
		}
	}
}

// appendSessionMessage appends a new message to the current session in the chat
// if the message is a tool result it will update the corresponding tool call message
func (m *UI) appendSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	existing := m.chat.MessageItem(msg.ID)
	if existing != nil {
		// message already exists, skip
		return nil
	}

	switch msg.Role {
	case message.User:
		// Shell commands are rendered live via shellResultMsg; skip
		// the persisted duplicate.
		hasShellCmd := false
		for _, part := range msg.Parts {
			if _, ok := part.(message.ShellCommand); ok {
				hasShellCmd = true
				break
			}
		}
		if hasShellCmd {
			return nil
		}
		m.lastUserMessageTime = msg.CreatedAt
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir())
		m.chat.AppendMessages(items...)
		m.chat.ScrollToBottom()
	case message.Assistant:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir())
		m.setMessagePlanFlags(items)
		m.chat.AppendMessages(items...)
		if m.chat.Follow() {
			m.chat.ScrollToBottom()
		}
		if chat.ShouldShowAssistantInfo(&msg) {
			infoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(infoItem)
			if m.chat.Follow() {
				m.chat.ScrollToBottom()
			}
		}
	case message.Tool:
		for _, tr := range msg.ToolResults() {
			toolItem := m.chat.MessageItem(tr.ToolCallID)
			if toolItem == nil {
				// we should have an item!
				continue
			}
			if toolMsgItem, ok := toolItem.(chat.ToolMessageItem); ok {
				toolMsgItem.SetResult(&tr)
				if m.chat.Follow() {
					m.chat.ScrollToBottom()
				}
			}
		}
	}
	return tea.Sequence(cmds...)
}

func (m *UI) handleClickFocus(msg tea.MouseClickMsg) (cmd tea.Cmd) {
	switch {
	case m.state != uiChat:
		return nil
	case m.stripActive() && image.Pt(msg.X, msg.Y).In(m.layout.sidebar):
		return m.handleStripClick(msg)
	case m.focus != uiFocusEditor && image.Pt(msg.X, msg.Y).In(m.layout.editor):
		if m.activeInline != nil {
			m.focusActiveInline(uiFocusEditor)
		} else {
			m.focus = uiFocusEditor
			cmd = m.textarea.Focus()
			m.chat.Blur()
		}
		m.strip().chooser = nil
	case m.focus != uiFocusMain && image.Pt(msg.X, msg.Y).In(m.layout.main):
		if m.activeInline != nil {
			m.focusActiveInline(uiFocusMain)
		} else {
			m.focus = uiFocusMain
			m.textarea.Blur()
			m.chat.Focus()
		}
		m.strip().chooser = nil
	}
	return cmd
}

// focusActiveInline moves focus between an inline editor and the chat while
// preserving the editor and reconciling any collapsed layout.
func (m *UI) focusActiveInline(focus uiFocusState) {
	if m.activeInline == nil {
		return
	}

	m.focus = focus
	m.textarea.Blur()
	switch focus {
	case uiFocusEditor:
		m.activeInline.SetFocused(true)
		if m.chat != nil {
			m.chat.Blur()
		}
	case uiFocusMain:
		m.activeInline.SetFocused(false)
		if m.chat != nil {
			m.chat.Focus()
			m.chat.SetSelected(m.chat.Len() - 1)
		}
	}

	if m.status != nil && m.chat != nil {
		m.updateLayoutAndSize()
	}
}

// updateSessionMessage updates an existing message in the current session in
// the chat when an assistant message is updated it may include updated tool
// calls as well that is why we need to handle creating/updating each tool call
// message too.
func (m *UI) updateSessionMessage(msg message.Message) tea.Cmd {
	// A message update means work is active; the animation clock may have
	// been frozen by a non-busy session reload (ghost-spinner guard).
	m.chat.SetAnimationsAllowed(true)
	var cmds []tea.Cmd
	existingItem := m.chat.MessageItem(msg.ID)

	if existingItem != nil {
		if assistantItem, ok := existingItem.(*chat.AssistantMessageItem); ok {
			assistantItem.SetMessage(&msg)
			assistantItem.SetPlanAgent(m.mode == uiInputModePlan)
		}
	}

	shouldRenderAssistant := chat.ShouldRenderAssistantMessage(&msg)
	// If the message of the assistant does not have any response just tool
	// calls we need to remove it, but keep the info item per finished turn
	// renders so the footer (model/provider/duration) remains visible.
	if !shouldRenderAssistant && len(msg.ToolCalls()) > 0 && existingItem != nil {
		m.chat.RemoveMessage(msg.ID)
	}

	// The info item shows for every turn with a Prism-routed model, and
	// for the final turn of the prompt. It is removed again when the
	// turn no longer qualifies (e.g. a retry reset the stream).
	if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); chat.ShouldShowAssistantInfo(&msg) {
		if infoItem == nil {
			newInfoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(newInfoItem)
		}
	} else if infoItem != nil {
		m.chat.RemoveMessage(chat.AssistantInfoID(msg.ID))
	}

	var items []chat.MessageItem
	for _, tc := range msg.ToolCalls() {
		existingToolItem := m.chat.MessageItem(tc.ID)
		if toolItem, ok := existingToolItem.(chat.ToolMessageItem); ok {
			existingToolCall := toolItem.ToolCall()
			// only update if finished state changed or input changed
			// to avoid clearing the cache
			if (tc.Finished && !existingToolCall.Finished) || tc.Input != existingToolCall.Input {
				toolItem.SetToolCall(tc)
			}
		}
		if existingToolItem == nil {
			items = append(items, chat.NewToolMessageItem(m.com.Styles, msg.ID, tc, nil, false, m.com.Workspace.WorkingDir()))
		}
	}

	m.chat.AppendMessages(items...)
	if m.chat.Follow() {
		m.chat.ScrollToBottom()
		m.chat.SelectLast()
	}

	return tea.Sequence(cmds...)
}

// handleChildSessionMessage handles messages from child sessions (agent tools).
func (m *UI) handleChildSessionMessage(event pubsub.Event[message.Message]) tea.Cmd {
	var cmds []tea.Cmd

	// Only process messages with tool calls or results.
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	// Check if this is an agent tool session and parse it.
	childSessionID := event.Payload.SessionID
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}
	// Nested tool activity means the agent is running; the animation clock
	// may have been frozen by a non-busy session reload.
	m.chat.SetAnimationsAllowed(true)

	// Find the parent agent tool item.
	var agentItem chat.NestedToolContainer
	for i := 0; i < m.chat.Len(); i++ {
		item := m.chat.MessageItem(toolCallID)
		if item == nil {
			continue
		}
		if agent, ok := item.(chat.NestedToolContainer); ok {
			if toolMessageItem, ok := item.(chat.ToolMessageItem); ok {
				if toolMessageItem.ToolCall().ID == toolCallID {
					// Verify this agent belongs to the correct parent message.
					// We can't directly check parentMessageID on the item, so we trust the session parsing.
					agentItem = agent
					break
				}
			}
		}
	}

	if agentItem == nil {
		return nil
	}

	// Get existing nested tools.
	nestedTools := agentItem.NestedTools()

	// Update or create nested tool calls.
	for _, tc := range event.Payload.ToolCalls() {
		found := false
		for _, existingTool := range nestedTools {
			if existingTool.ToolCall().ID == tc.ID {
				existingTool.SetToolCall(tc)
				found = true
				break
			}
		}
		if !found {
			// Create a new nested tool item.
			nestedItem := chat.NewToolMessageItem(m.com.Styles, event.Payload.ID, tc, nil, false, m.com.Workspace.WorkingDir())
			if simplifiable, ok := nestedItem.(chat.Compactable); ok {
				simplifiable.SetCompact(true)
			}
			nestedTools = append(nestedTools, nestedItem)
		}
	}

	// Update nested tool results.
	for _, tr := range event.Payload.ToolResults() {
		for _, nestedTool := range nestedTools {
			if nestedTool.ToolCall().ID == tr.ToolCallID {
				nestedTool.SetResult(&tr)
				break
			}
		}
	}

	// Update the agent item with the new nested tools.
	agentItem.SetNestedTools(nestedTools)

	// Update the chat so it updates the index map for animations to work as expected
	m.chat.UpdateNestedToolIDs(toolCallID)

	if m.chat.Follow() {
		m.chat.ScrollToBottom()
		m.chat.SelectLast()
	}

	return tea.Sequence(cmds...)
}

func (m *UI) handleDialogMsg(msg tea.Msg) tea.Cmd {
	return m.handleDialogAction(m.dialog.Update(msg))
}

// handleDialogAction does what a dialog's action asks.
func (m *UI) handleDialogAction(action dialog.Action) tea.Cmd {
	var cmds []tea.Cmd
	if action == nil {
		return tea.Batch(cmds...)
	}

	isOnboarding := m.state == uiOnboarding

	switch msg := action.(type) {
	// Generic dialog messages
	case dialog.ActionClose:
		if m.dialog.ContainsDialog(dialog.FilePickerID) {
			defer fimage.ResetCache()
		}

		m.dialog.CloseFrontDialog()

		if isOnboarding {
			if cmd := m.openModelsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		if m.focus == uiFocusEditor {
			cmds = append(cmds, m.textarea.Focus())
		}
	case dialog.ActionCmd:
		if msg.Cmd != nil {
			cmds = append(cmds, msg.Cmd)
		}
	case dialog.ActionGitPane:
		cmds = append(cmds, m.handleGitPaneAction(msg))

	// Session dialog messages.
	case dialog.ActionSelectSession:
		m.dialog.CloseDialog(dialog.SessionsID)
		cmds = append(cmds, m.loadSession(msg.Session.ID))

	// Open dialog message.
	case dialog.ActionOpenDialog:
		m.dialog.CloseDialog(dialog.CommandsID)
		if cmd := m.openDialog(msg.DialogID); cmd != nil {
			cmds = append(cmds, cmd)
		}

	// Command dialog messages.
	case dialog.ActionToggleYoloMode:
		if m.mode == uiInputModePlan {
			// Same as Ctrl+Y in plan mode: YOLO only exists as YOLO
			// coding, so activating it leaves plan mode.
			if cmd := m.switchPlanToYolo(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else {
			m.toggleYoloMode()
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionRunNotice:
		if cmd := m.runNoticeAction(msg.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ActionClearNotices:
		m.clearNotices()
	case dialog.ActionSelectNotificationStyle:
		cfg := m.com.Config()
		if cfg != nil && cfg.Options != nil {
			cfg.Options.Notifications = msg.Style
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.notifications", msg.Style); err != nil {
				cmds = append(cmds, util.ReportError(err))
			} else {
				cmds = append(cmds, util.CmdHandler(util.NewInfoMsg("Notifications set to: "+msg.Style)))
			}
			// Reinitialize notification backend with new style.
			m.notifyBackend = selectNotificationBackend(m.caps, cfg)
		}
		m.dialog.CloseDialog(dialog.NotificationsID)
	case dialog.ActionNewSession:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
			break
		}
		if cmd := m.newSession(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSummarize:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, func() tea.Msg {
			err := m.com.Workspace.AgentSummarize(context.Background(), msg.SessionID)
			if err != nil {
				return util.ReportError(err)()
			}
			return nil
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleHelp:
		m.status.ToggleHelp()
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionShowProfiles:
		cmds = append(cmds, m.loadProfiles(true))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionExternalEditor:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
			break
		}
		editorValue := m.textarea.Value()
		if m.bangMode {
			editorValue = "!" + editorValue
		}
		cmds = append(cmds, m.openEditor(editorValue))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleCompactMode:
		cmds = append(cmds, m.toggleCompactMode())
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSidebarReopen:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.openStripReopen())
	case dialog.ActionSidebarReset:
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, m.resetStrip())
	case dialog.ActionTogglePills:
		if cmd := m.togglePillsExpanded(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleThinking:
		cmds = append(cmds, m.updateAgentModelCmd(func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}

			agentCfg, ok := cfg.Agents[config.AgentCoder]
			if !ok {
				return util.ReportError(errors.New("agent configuration not found"))()
			}

			currentModel := cfg.Models[agentCfg.Model]
			currentModel.Think = !currentModel.Think
			if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, agentCfg.Model, currentModel); err != nil {
				return util.ReportError(err)()
			}
			m.com.Workspace.UpdateAgentModel(context.TODO())
			status := "disabled"
			if currentModel.Think {
				status = "enabled"
			}
			return util.NewInfoMsg("Thinking mode " + status)
		}))
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionToggleTransparentBackground:
		cmds = append(cmds, func() tea.Msg {
			cfg := m.com.Config()
			if cfg == nil {
				return util.ReportError(errors.New("configuration not found"))()
			}

			isTransparent := cfg.Options != nil && cfg.Options.TUI.IsTransparent()
			newValue := !isTransparent
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.transparent", newValue); err != nil {
				return util.ReportError(err)()
			}
			m.isTransparent = newValue

			status := "disabled"
			if newValue {
				status = "enabled"
			}
			return util.NewInfoMsg("Transparent background " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionSwitchTheme:
		themeName := msg.Theme
		newStyles, err := styles.LoadTheme(themeName)
		if err != nil {
			cmds = append(cmds, util.ReportError(err))
			break
		}
		// Saved off the event loop; themeSwitchedMsg applies it.
		cmds = append(cmds, m.switchThemeCmd(themeName, newStyles))
	case dialog.ActionPreviewTheme:
		// Every cursor move lands here: the kept theme is copied
		// straight into the active styles.
		newStyles, err := styles.LoadThemeShared(msg.Theme)
		if err != nil {
			break
		}
		if m.preThemeStyles == nil {
			saved := m.com.Styles.Clone()
			m.preThemeStyles = &saved
		}
		m.previewTheme(newStyles)
	case dialog.ActionRevertThemePreview:
		if m.preThemeStyles != nil {
			m.applyTheme(*m.preThemeStyles)
			m.preThemeStyles = nil
		}
		m.dialog.CloseDialog(dialog.ThemeID)
	case dialog.ActionPreviewThemePalette:
		newStyles, err := styles.LoadPaletteTheme(msg.Base, msg.Palette)
		if err != nil {
			break
		}
		if m.preThemeStyles == nil {
			saved := m.com.Styles.Clone()
			m.preThemeStyles = &saved
		}
		m.previewTheme(&newStyles)
	case dialog.ActionSaveThemePalette:
		// The theme is stored under its own name; Base only identifies the
		// built-in palette its colors are derived from.
		themeName := msg.Name
		if themeName == "" {
			themeName = msg.Base
		}

		// Write the file before touching the UI so a failed save never
		// leaves colors applied that did not reach disk.
		savePath, err := styles.ThemePath(themeName)
		if err != nil {
			cmds = append(cmds, util.ReportError(err))
			break
		}
		tf := &styles.ThemeFile{Base: msg.Base, Palette: msg.Palette}
		if err := styles.SaveThemeFile(savePath, tf); err != nil {
			cmds = append(cmds, util.ReportError(err))
			break
		}

		// Only take over the whole UI when the saved theme is the active
		// one (or the implicit default). Otherwise the preview backup
		// stays intact so esc restores the user's real theme instead of
		// leaving the edited colors applied until restart.
		activeTheme := common.ThemeNameFromConfig(m.com.Config())
		isActive := strings.EqualFold(activeTheme, themeName)
		if activeTheme == "" {
			isActive = strings.EqualFold(themeName, "deck")
		}
		if isActive {
			newStyles, err := styles.LoadTheme(themeName)
			if err != nil {
				cmds = append(cmds, util.ReportError(err))
				break
			}
			m.applyTheme(newStyles)
			m.preThemeStyles = nil
		}
		cmds = append(cmds, util.ReportInfo("Theme saved"))
		m.dialog.CloseDialog(dialog.ThemeEditorID)
		if td, ok := m.dialog.Dialog(dialog.ThemeID).(*dialog.Theme); ok {
			td.RefreshThemes(themeName)
		}
	case dialog.ActionEditTheme:
		m.openThemeEditorDialog(msg.Name)
	case dialog.ActionRevertThemePalette:
		if m.preThemeStyles != nil {
			m.applyTheme(*m.preThemeStyles)
			m.preThemeStyles = nil
		}
		m.dialog.CloseDialog(dialog.ThemeEditorID)
	case dialog.ActionRevertOverriddenTheme:
		// Drop any user override layered on top of the built-in: the
		// shadowing theme file and the config palette entry.
		if path, err := styles.FindThemeFile(msg.Name); err == nil {
			if err := os.Remove(path); err != nil {
				cmds = append(cmds, util.ReportError(fmt.Errorf("revert theme: %w", err)))
				break
			}
		}
		// If the reverted theme is the active one, re-apply the pristine
		// built-in so the change is visible immediately.
		if strings.EqualFold(common.ThemeNameFromConfig(m.com.Config()), msg.Name) {
			if newStyles, err := styles.LoadTheme(msg.Name); err == nil {
				m.applyTheme(newStyles)
			}
		}
		cmds = append(cmds, util.ReportInfo("Reverted "+msg.Name+" to its built-in colors"))
		m.dialog.CloseDialog(dialog.ThemeID)
		m.openThemeDialog()
	case dialog.ActionCreateTheme:
		base := msg.Base
		if base == "" {
			base = "deck"
		}
		// Written off the event loop; themeCreatedMsg finishes up.
		cmds = append(cmds, m.createThemeCmd(msg.Name, base))
	case dialog.ActionRenameTheme:
		cfg := m.com.Config()
		wasActive := cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil && strings.EqualFold(cfg.Options.TUI.ActiveTheme, msg.OldName)
		// Renamed off the event loop; themeRenamedMsg finishes up.
		cmds = append(cmds, m.renameThemeCmd(msg.OldName, strings.ToLower(msg.NewName), wasActive))
	case dialog.ActionDeleteTheme:
		// If the deleted theme was active, the command resets to the
		// default theme; themeDeletedMsg applies it.
		wasActive := strings.EqualFold(common.ThemeNameFromConfig(m.com.Config()), msg.Name)
		cmds = append(cmds, m.deleteThemeCmd(msg.Name, wasActive))
	case dialog.ActionToggleMouseSupport:
		cfg := m.com.Config()
		if cfg == nil {
			cmds = append(cmds, util.ReportError(errors.New("configuration not found")))
			break
		}
		// Flip the field on the main update path so it never races with
		// View() reading m.mouseEnabled from a background command's
		// goroutine; only the (possibly slow) config write is deferred.
		mouseEnabled := cfg.Options == nil || cfg.Options.TUI.Mouse == nil || *cfg.Options.TUI.Mouse
		newValue := !mouseEnabled
		m.mouseEnabled = newValue
		cmds = append(cmds, func() tea.Msg {
			if err := m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.tui.mouse", newValue); err != nil {
				return util.ReportError(err)()
			}

			status := "disabled"
			if newValue {
				status = "enabled"
			}
			return util.NewInfoMsg("Mouse support " + status)
		})
		m.dialog.CloseDialog(dialog.CommandsID)
	case dialog.ActionQuit:
		cmds = append(cmds, tea.Quit)
	case dialog.ActionInitializeProject:
		if m.isAgentBusy() {
			cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before summarizing session..."))
			break
		}
		cmds = append(cmds, m.initializeProject())
		m.dialog.CloseDialog(dialog.CommandsID)

	case dialog.ActionSelectAgentModel:
		cmds = append(cmds, m.selectAgentModel(msg))
	case dialog.ActionSetDeckSettings:
		cmds = append(cmds, m.applyDeckSettings(msg))
	case dialog.ActionSetTUIOption:
		cmds = append(cmds, m.setTUIOption(msg))
	case dialog.ActionPermissionResponse:
		m.dialog.CloseDialog(dialog.PermissionsID)
		switch msg.Action {
		case dialog.PermissionAllow:
			m.com.Workspace.PermissionGrant(msg.Permission)
		case dialog.PermissionAllowForSession:
			m.com.Workspace.PermissionGrantPersistent(msg.Permission)
		case dialog.PermissionDeny:
			m.com.Workspace.PermissionDeny(msg.Permission)
		}

	case dialog.ActionFilePickerSelected:
		cmds = append(cmds, tea.Sequence(
			msg.Cmd(),
			func() tea.Msg {
				m.dialog.CloseDialog(dialog.FilePickerID)
				return nil
			},
			func() tea.Msg {
				fimage.ResetCache()
				return nil
			},
		))

	case dialog.ActionRunCustomCommand:
		if len(msg.Arguments) > 0 && msg.Args == nil {
			m.dialog.CloseFrontDialog()
			argsDialog := dialog.NewArguments(
				m.com,
				"Custom Command Arguments",
				"",
				msg.Arguments,
				msg, // Pass the action as the result
			)
			m.dialog.OpenDialog(argsDialog)
			break
		}
		content := msg.Content
		if msg.Args != nil {
			content = substituteArgs(content, msg.Args)
		}
		// If this is a skill command, format it using the skill's FormatInvocation method
		if msg.Skill != nil {
			content = msg.Skill.FormatInvocation()
		}
		cmds = append(cmds, m.sendMessage(content))
		m.dialog.CloseFrontDialog()
	case dialog.ActionAttachSkill:
		m.dialog.CloseFrontDialog()
		cmds = append(cmds, m.attachSkill(msg.ID, msg.Name))
	default:
		cmds = append(cmds, util.CmdHandler(msg))
	}

	return tea.Batch(cmds...)
}

// substituteArgs replaces $ARG_NAME placeholders in content with actual values.
func substituteArgs(content string, args map[string]string) string {
	for name, value := range args {
		placeholder := "$" + name
		content = strings.ReplaceAll(content, placeholder, value)
	}
	return content
}

// fetchGitBranch reads the workspace's checked-out branch off the render
// path. A failed read keeps whatever the last poll reported.
func (m *UI) fetchGitBranch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), gitBranchFetchTimeout)
		defer cancel()
		branch, err := m.com.Workspace.GitBranch(ctx)
		if err != nil {
			slog.Warn("Failed to read the git branch", "error", err)
			return nil
		}
		return gitBranchUpdatedMsg{branch: branch}
	}
}

// windowBlurred reports whether the terminal said its window lost focus.
// Terminals that do not report focus are always treated as focused.
func (m *UI) windowBlurred() bool {
	return m.caps.ReportFocusEvents && !m.notifyWindowFocused
}

// gitBranchTicker schedules the next git branch poll.
func (m *UI) gitBranchTicker() tea.Cmd {
	return tea.Tick(gitBranchPollInterval, func(time.Time) tea.Msg {
		return gitBranchPollMsg{}
	})
}

// restoreModelFromSession checks the last assistant message in the
// loaded session and, if it used a different provider/model than the
// current config, restores that model/provider provided it is still
// available. Returns a tea.Cmd that rebuilds the agent models if a
// switch was made, or nil if no switch was needed.
func (m *UI) restoreModelFromSession(msgs []message.Message) tea.Cmd {
	var lastAssistant *message.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.Assistant && !msgs[i].IsSummaryMessage {
			lastAssistant = &msgs[i]
			break
		}
	}
	if lastAssistant == nil || lastAssistant.Provider == "" || lastAssistant.Model == "" {
		return nil
	}

	cfg := m.com.Config()
	if cfg == nil {
		return nil
	}

	currentLarge := cfg.Models[config.SelectedModelTypeLarge]
	if currentLarge.Provider == lastAssistant.Provider && currentLarge.Model == lastAssistant.Model {
		return nil
	}

	if !cfg.IsModelAvailable(lastAssistant.Provider, lastAssistant.Model) {
		slog.Debug("Skipping model restoration: provider/model not available",
			"provider", lastAssistant.Provider,
			"model", lastAssistant.Model)
		return nil
	}

	selectedModel := config.SelectedModel{
		Provider: lastAssistant.Provider,
		Model:    lastAssistant.Model,
	}
	if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, config.SelectedModelTypeLarge, selectedModel); err != nil {
		slog.Error("Failed to restore model from session", "error", err)
		return nil
	}

	m.applyThemeForProvider(lastAssistant.Provider)

	if _, ok := cfg.Models[config.SelectedModelTypeSmall]; !ok {
		smallModel := m.com.Workspace.GetDefaultSmallModel(lastAssistant.Provider)
		if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, config.SelectedModelTypeSmall, smallModel); err != nil {
			slog.Error("Failed to set small model during session restore", "error", err)
		}
	}

	return m.updateAgentModelCmd(func() tea.Msg {
		if err := m.com.Workspace.UpdateAgentModel(context.TODO()); err != nil {
			return util.ReportError(err)
		}
		slog.Info("Restored model from session",
			"provider", lastAssistant.Provider,
			"model", lastAssistant.Model)
		return nil
	})
}

func (m *UI) handleKeyPressMsg(msg tea.KeyPressMsg) tea.Cmd {
	var cmds []tea.Cmd

	handleGlobalKeys := func(msg tea.KeyPressMsg) bool {
		switch {
		case key.Matches(msg, m.keyMap.Help):
			m.status.ToggleHelp()
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Commands):
			if cmd := m.openCommandsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Models):
			if cmd := m.openModelsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Sessions):
			if cmd := m.openSessionsDialog(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Settings):
			if cmd := m.openSettingsDialog(dialog.SettingsID); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return true
		case key.Matches(msg, m.keyMap.Git):
			cmds = append(cmds, m.openGitPane())
			return true
		case key.Matches(msg, m.keyMap.Notices):
			cmds = append(cmds, m.openNoticeHistory())
			return true
		case key.Matches(msg, m.keyMap.Usage):
			cmds = append(cmds, m.openUsageDialog())
			return true
		case key.Matches(msg, m.keyMap.Chat.Details) && m.isCompact:
			m.detailsOpen = !m.detailsOpen
			m.updateLayoutAndSize()
			return true
		case key.Matches(msg, m.keyMap.Chat.ToggleSidebar):
			if m.canToggleSidebar() {
				cmds = append(cmds, m.toggleCompactMode())
				return true
			}
		case key.Matches(msg, m.keyMap.SidebarColumnLeft):
			return m.moveStripColumn(-1)
		case key.Matches(msg, m.keyMap.SidebarColumnRight):
			return m.moveStripColumn(1)
		case key.Matches(msg, m.keyMap.Chat.EndFollow):
			if m.state == uiChat && m.hasSession() {
				if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Chat.TogglePills):
			if m.state == uiChat && m.hasSession() {
				if cmd := m.togglePillsExpanded(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Chat.PillLeft):
			if m.state == uiChat && m.hasSession() && m.pillsExpanded && m.focus != uiFocusEditor {
				if cmd := m.switchPillSection(-1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Chat.PillRight):
			if m.state == uiChat && m.hasSession() && m.pillsExpanded && m.focus != uiFocusEditor {
				if cmd := m.switchPillSection(1); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
		case key.Matches(msg, m.keyMap.Suspend):
			if m.isAgentBusy() {
				cmds = append(cmds, util.ReportWarn("Agent is busy, please wait..."))
				return true
			}
			cmds = append(cmds, tea.Suspend)
			return true
		case key.Matches(msg, m.keyMap.ToggleYolo):
			if m.mode == uiInputModePlan {
				// YOLO has no meaning while planning; activating it
				// switches straight to YOLO coding.
				if cmd := m.switchPlanToYolo(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				return true
			}
			yolo := m.toggleYoloMode()
			if yolo {
				cmds = append(cmds, util.CmdHandler(util.InfoMsg{Type: util.InfoTypeYolo, Msg: yoloModeBannerMsg}))
			} else {
				cmds = append(cmds, util.ReportInfo("Yolo mode disabled"))
			}
			return true
		}
		return false
	}

	if key.Matches(msg, m.keyMap.Quit) && !m.dialog.ContainsDialog(dialog.QuitID) {
		// Always handle quit keys first
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}

		return tea.Batch(cmds...)
	}

	// Route all messages to dialog if one is open.
	if m.dialog.HasDialogs() {
		return m.handleDialogMsg(msg)
	}

	// Tab always toggles focus between editor and chat, even when
	// an inline editor is active. This lets users collapse the
	// question form to view chat.
	if m.activeInline != nil && key.Matches(msg, m.keyMap.Tab) {
		if m.focus == uiFocusEditor {
			m.focusActiveInline(uiFocusMain)
		} else {
			m.focusActiveInline(uiFocusEditor)
		}
		return tea.Batch(cmds...)
	}

	// Route keys to active inline editor if one is showing.
	if m.activeInline != nil && m.focus == uiFocusEditor {
		if done, cmd := m.activeInline.HandleKey(msg); done {
			prev := m.activeInline
			m.activeInline = nil
			m.textarea.Focus()
			m.updateLayoutAndSize()
			if cmd != nil {
				cmds = append(cmds, cmd)
			} else if cod, ok := prev.(dialog.CmdOnDone); ok {
				if c := cod.PendingCmd(); c != nil {
					cmds = append(cmds, c)
				}
			}
		} else {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.activeInline.HeightChanged() {
				m.updateLayoutAndSize()
			}
		}
		return tea.Batch(cmds...)
	}

	// While the sidebar has focus its panes and the strip get keys first,
	// esc included (strip_keys.go).
	if m.focus == uiFocusSidebar && m.stripActive() {
		if handled, cmd := m.handleStripKey(msg); handled {
			cmds = append(cmds, cmd)
			return tea.Batch(cmds...)
		}
	}

	// Handle cancel key when agent is busy.
	if key.Matches(msg, m.keyMap.Chat.Cancel) {
		if m.isAgentBusy() {
			if cmd := m.cancelAgent(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return tea.Batch(cmds...)
		}
	}

	switch m.state {
	case uiOnboarding:
		return tea.Batch(cmds...)
	case uiInitialize:
		cmds = append(cmds, m.updateInitializeView(msg)...)
		return tea.Batch(cmds...)
	case uiChat, uiLanding:
		switch m.focus {
		case uiFocusEditor:
			// Handle completions if open.
			if m.completionsOpen {
				if msg, ok := m.completions.Update(msg); ok {
					switch msg := msg.(type) {
					case completions.SelectionMsg[completions.FileCompletionValue]:
						cmds = append(cmds, m.insertFileCompletion(msg.Value.Path))
						if !msg.KeepOpen {
							m.closeCompletions()
						}
					case completions.SelectionMsg[completions.AgentCompletionValue]:
						cmds = append(cmds, m.insertAgentCompletion(msg.Value))
						m.closeCompletions()
					case completions.ClosedMsg:
						m.completionsOpen = false
					}
					return tea.Batch(cmds...)
				}
			}

			if ok := m.attachments.Update(msg); ok {
				return tea.Batch(cmds...)
			}

			switch {
			case key.Matches(msg, m.keyMap.ShiftTab):
				if cmd := m.toggleInputMode(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.AddImage):
				if !m.currentModelSupportsImages() {
					break
				}
				if cmd := m.openFilesDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}

			case key.Matches(msg, m.keyMap.Editor.PasteImage):
				if !m.currentModelSupportsImages() {
					break
				}
				cmds = append(cmds, m.pasteImageFromClipboard)
			case key.Matches(msg, m.keyMap.Editor.PasteText):
				cmds = append(cmds, m.pasteTextFromClipboard)

			case key.Matches(msg, m.keyMap.Editor.SendMessage):
				if m.modeSwitching {
					cmds = append(cmds, util.ReportInfo("Switching input mode, one moment..."))
					break
				}
				prevHeight := m.textarea.Height()
				value := m.textarea.Value()
				if before, ok := strings.CutSuffix(value, "\\"); ok {
					// If the last character is a backslash, remove it and add a newline.
					m.textarea.SetValue(before)
					if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
						cmds = append(cmds, cmd)
					}
					break
				}

				// Otherwise, send the message
				m.textarea.Reset()
				if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
					cmds = append(cmds, cmd)
				}

				value = strings.TrimSpace(value)
				if value == "exit" || value == "quit" {
					return m.openQuitDialog()
				}

				if m.bangMode && value != "" {
					m.bangMode = false
					m.setEditorPrompt(m.yoloModeCached())
					m.randomizePlaceholders()
					m.historyReset()
					return tea.Batch(m.runShellCommand(value))
				}

				attachments := m.attachments.List()
				m.attachments.Reset()
				if len(value) == 0 && !message.ContainsTextAttachment(attachments) {
					// Enter on an empty editor while a ready plan is pending
					// reopens the dismissed handoff prompt.
					if m.mode == uiInputModePlan && m.hasSession() && m.planReadySessionID == m.session.ID {
						m.openPlanHandoff()
					}
					return nil
				}

				m.randomizePlaceholders()
				m.historyReset()

				return tea.Batch(m.sendMessage(value, attachments...), m.loadPromptHistory())
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
					break
				}
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Tab):
				if m.state != uiLanding {
					m.setState(m.state, uiFocusMain)
					m.textarea.Blur()
					m.chat.Focus()
					m.chat.SetSelected(m.chat.Len() - 1)
				}
			case key.Matches(msg, m.keyMap.Editor.OpenEditor):
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is working, please wait..."))
					break
				}
				editorValue := m.textarea.Value()
				if m.bangMode {
					editorValue = "!" + editorValue
				}
				cmds = append(cmds, m.openEditor(editorValue))
			case key.Matches(msg, m.keyMap.Editor.Newline):
				prevHeight := m.textarea.Height()
				m.textarea.InsertRune('\n')
				m.closeCompletions()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))
			case key.Matches(msg, m.keyMap.Editor.CopySelection):
				if m.textarea.HasSelection() {
					cmds = append(cmds, common.CopyToClipboardWithCallback(
						m.textarea.SelectedText(),
						"Selection copied to clipboard",
						nil,
					))
					m.textarea.ClearSelection()
				}
			case key.Matches(msg, m.keyMap.Editor.CutSelection):
				if m.textarea.HasSelection() {
					cmds = append(cmds, common.CopyToClipboardWithCallback(
						m.textarea.SelectedText(),
						"Selection cut to clipboard",
						nil,
					))
					m.textarea.DeleteSelection()
				}
			case key.Matches(msg, m.keyMap.Editor.HistoryPrev):
				cmd := m.handleHistoryUp(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.HistoryNext):
				cmd := m.handleHistoryDown(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.Escape):
				cmd := m.handleHistoryEscape(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Editor.Commands) && m.textarea.Value() == "":
				if cmd := m.openCommandsDialog(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			default:
				if handleGlobalKeys(msg) {
					// Handle global keys first before passing to textarea.
					break
				}

				// Bang mode: backspace on already-empty prompt exits.
				if m.bangMode && m.bangWasEmpty && msg.Code == tea.KeyBackspace {
					m.bangMode = false
					m.bangWasEmpty = false
					m.setEditorPrompt(m.yoloModeCached())
					break
				}

				// Check for a sigil before passing to textarea.
				curValue := m.textarea.Value()
				curIdx := len(curValue)

				// Trigger completions on @ (agents, profiles and
				// teammates; $ is the same) and # (files).
				if completionSigil(msg.String()) && !m.completionsOpen {
					// Only show if beginning of prompt or after whitespace.
					if curIdx == 0 || (curIdx > 0 && isWhitespace(curValue[curIdx-1])) {
						cmds = append(cmds, m.openCompletions(msg.String()))
					}
				}

				// remove the details if they are open when user starts typing
				if m.detailsOpen {
					m.detailsOpen = false
					m.updateLayoutAndSize()
				}

				prevHeight := m.textarea.Height()
				cmds = append(cmds, m.updateTextareaWithPrevHeight(msg, prevHeight))

				// Bang mode: enter when "!" is typed at the start of the
				// prompt, optionally preceded by whitespace (either on an
				// empty/whitespace-only prompt or prepended to existing text).
				// Exit on backspace clearing the last character.
				newVal := m.textarea.Value()
				trimmedNew := strings.TrimLeftFunc(newVal, unicode.IsSpace)
				trimmedCur := strings.TrimLeftFunc(curValue, unicode.IsSpace)
				if !m.bangMode && strings.HasPrefix(trimmedNew, "!") && !strings.HasPrefix(trimmedCur, "!") {
					m.bangMode = true
					m.bangWasEmpty = len(strings.TrimSpace(curValue)) == 0
					// Strip leading whitespace and the "!" from the textarea
					// while preserving the cursor position relative to the
					// command text.
					col := m.textarea.Column()
					line := m.textarea.Line()
					stripped := trimmedNew[1:]
					m.textarea.SetValue(stripped)
					m.textarea.SetCursorColumn(max(0, col-(len(newVal)-len(stripped))))
					_ = line // cursor line doesn't change; prefix removed
					m.setEditorPrompt(m.yoloModeCached())
				} else if m.bangMode && newVal == "" && curValue != "" {
					// Just cleared last character; mark empty, stay in bang mode.
					m.bangWasEmpty = true
				} else if m.bangMode && newVal != "" {
					m.bangWasEmpty = false
				}

				// Any text modification becomes the current draft.
				m.updateHistoryDraft(curValue)

				// After updating textarea, check if we need to filter completions.
				// Skip filtering on the initial @ keystroke since items are loading async.
				if m.completionsOpen && msg.String() != m.completionsSigil {
					newValue := m.textarea.Value()
					newIdx := len(newValue)

					// Close completions if cursor moved before start.
					if newIdx <= m.completionsStartIndex {
						m.closeCompletions()
					} else if msg.String() == "space" {
						// Close on space.
						m.closeCompletions()
					} else {
						// Extract current word and filter.
						word := m.textareaWord()
						if strings.HasPrefix(word, m.completionsSigil) {
							cmds = append(cmds, m.filterCompletions(word[1:]))
						} else if m.completionsOpen {
							m.closeCompletions()
						}
					}
				}
			}
		case uiFocusMain:
			switch {
			case key.Matches(msg, m.keyMap.ShiftTab):
				if cmd := m.toggleInputMode(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Tab):
				m.focus = uiFocusEditor
				cmds = append(cmds, m.textarea.Focus())
				m.chat.Blur()
			case key.Matches(msg, m.keyMap.Chat.FocusSidebar):
				if m.stripActive() {
					m.focusSidebar()
				}
			case key.Matches(msg, m.keyMap.Chat.NewSession):
				if !m.hasSession() {
					break
				}
				if m.isAgentBusy() {
					cmds = append(cmds, util.ReportWarn("Agent is busy, please wait before starting a new session..."))
					break
				}
				m.focus = uiFocusEditor
				if cmd := m.newSession(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case key.Matches(msg, m.keyMap.Chat.Expand):
				m.chat.ToggleExpandedSelectedItem()
			case key.Matches(msg, m.keyMap.Chat.Up):
				m.markScrollOnly()
				m.chat.ScrollBy(-1)
				if !m.chat.SelectedItemInView() {
					m.chat.SelectPrev()
					m.chat.ScrollToSelected()
				}
			case key.Matches(msg, m.keyMap.Chat.Down):
				m.markScrollOnly()
				m.chat.ScrollBy(1)
				if !m.chat.SelectedItemInView() {
					m.chat.SelectNext()
					m.chat.ScrollToSelected()
				}
			case key.Matches(msg, m.keyMap.Chat.UpOneItem):
				m.chat.SelectPrev()
				m.chat.ScrollToSelected()
			case key.Matches(msg, m.keyMap.Chat.DownOneItem):
				m.chat.SelectNext()
				m.chat.ScrollToSelected()
			case key.Matches(msg, m.keyMap.Chat.HalfPageUp):
				m.markScrollOnly()
				m.chat.ScrollBy(-m.chat.Height() / 2)
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.HalfPageDown):
				m.markScrollOnly()
				m.chat.ScrollBy(m.chat.Height() / 2)
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.PageUp):
				m.markScrollOnly()
				m.chat.ScrollBy(-m.chat.Height())
				m.chat.SelectFirstInView()
			case key.Matches(msg, m.keyMap.Chat.PageDown):
				m.markScrollOnly()
				m.chat.ScrollBy(m.chat.Height())
				m.chat.SelectLastInView()
			case key.Matches(msg, m.keyMap.Chat.Home):
				m.chat.ScrollToTop()
				m.chat.SelectFirst()
			case key.Matches(msg, m.keyMap.Chat.End):
				m.chat.ScrollToBottomAndSelectLast()
			default:
				if ok, cmd := m.chat.HandleKeyMsg(msg); ok {
					cmds = append(cmds, cmd)
				} else {
					handleGlobalKeys(msg)
				}
			}
		case uiFocusSidebar:
			// The strip and its panes had the key first (above); what is
			// left is moving focus away and the global keys.
			switch {
			case key.Matches(msg, m.keyMap.Tab):
				if cmd := m.leaveSidebar(uiFocusEditor); cmd != nil {
					cmds = append(cmds, cmd)
				}
			default:
				handleGlobalKeys(msg)
			}
		default:
			handleGlobalKeys(msg)
		}
	default:
		handleGlobalKeys(msg)
	}

	return tea.Sequence(cmds...)
}

// drawHeader draws the header section of the UI.
func (m *UI) drawHeader(scr uv.Screen, area uv.Rectangle) {
	m.header.drawHeader(
		scr,
		area,
		m.session,
		m.markLevels(),
		m.isCompact,
		m.detailsOpen,
		area.Dx(),
		m.gitBranch,
		m.gitHeaderBadge(),
		m.rosterOrNil(),
	)
}

// Draw implements [uv.Drawable] and draws the UI model.
func (m *UI) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	layout := m.generateLayout(area.Dx(), area.Dy())

	if m.layout != layout {
		m.layout = layout
		m.updateSize()
	} else if m.state == uiChat && m.hasSession() {
		// Re-render pills on every draw so the box appears even when
		// the layout footprint hasn't changed (e.g. todos arrived
		// while the panel was collapsed). updateSize already calls
		// renderPills, but only when the layout actually differs;
		// this catches the steady-state case.
		m.renderPills()
	}

	// Clear the screen first
	screen.Clear(scr)

	switch m.state {
	case uiOnboarding:
		m.drawHeader(scr, layout.header)

		// NOTE: Onboarding flow will be rendered as dialogs below, but
		// positioned at the bottom left of the screen.

	case uiInitialize:
		m.drawHeader(scr, layout.header)

		main := uv.NewStyledString(m.initializeView())
		main.Draw(scr, layout.main)

	case uiLanding:
		b := m.landingLayout()
		m.drawHeader(scr, b.header)
		uv.NewStyledString(b.textView).Draw(scr, b.text)
		// After the header, so the wordmark's canvas does not paint over it.
		if b.agentsView != "" {
			uv.NewStyledString(b.agentsView).Draw(scr, b.agents)
		}

		if m.activeInline != nil {
			m.activeInline.SetFocused(m.focus == uiFocusEditor)
			if collapsed, ok := m.collapsedInlineEditor(); ok {
				collapsed.DrawCollapsed(scr, layout.editor)
				m.inlineCursor = nil
			} else {
				m.inlineCursor = m.activeInline.Draw(scr, layout.editor)
			}
		} else {
			editor := uv.NewStyledString(m.renderEditorView(scr.Bounds().Dx()))
			editor.Draw(scr, layout.editor)
			m.inlineCursor = nil
		}

	case uiChat:
		if m.isCompact {
			m.drawHeader(scr, layout.header)
		} else {
			m.drawSidebar(scr, layout.sidebar)
		}

		m.chat.Draw(scr, layout.main)
		if layout.pills.Dy() > 0 && m.pillsView != "" {
			uv.NewStyledString(m.pillsView).Draw(scr, layout.pills)
		}

		if m.activeInline != nil {
			m.activeInline.SetFocused(m.focus == uiFocusEditor)
			if collapsed, ok := m.collapsedInlineEditor(); ok {
				collapsed.DrawCollapsed(scr, layout.editor)
				m.inlineCursor = nil
			} else {
				m.inlineCursor = m.activeInline.Draw(scr, layout.editor)
			}
		} else {
			editorWidth := scr.Bounds().Dx()
			if !m.isCompact {
				editorWidth -= layout.sidebar.Dx()
			}
			editor := uv.NewStyledString(m.renderEditorView(editorWidth))
			editor.Draw(scr, layout.editor)
			m.inlineCursor = nil
		}

		// Draw details overlay in compact mode when open
		if m.isCompact && m.detailsOpen {
			m.drawSessionDetails(scr, layout.sessionDetails)
		}
	}

	isOnboarding := m.state == uiOnboarding

	// Add status and help layer
	m.status.SetHideHelp(isOnboarding)
	m.status.SetMode(m.mode, m.yoloModeCached())
	// Keep the hints clear of the unread mark at the right.
	reserve := 0
	if !isOnboarding {
		reserve = m.noticeDotReserve(layout.status)
	}
	m.status.SetRightReserve(reserve)
	m.status.Draw(scr, layout.status)
	if !isOnboarding {
		m.drawNoticeDot(scr, layout.status)
	}

	// Draw completions popup if open
	if !isOnboarding && m.completionsOpen && m.completions.HasItems() {
		w, h := m.completions.Size()
		x := m.completionsPositionStart.X
		y := m.completionsPositionStart.Y - h

		screenW := area.Dx()
		if x+w > screenW {
			x = screenW - w
		}
		x = max(0, x)
		y = max(0, y+1) // Offset for attachments row

		completionsView := uv.NewStyledString(m.completions.Render())
		completionsView.Draw(scr, image.Rectangle{
			Min: image.Pt(x, y),
			Max: image.Pt(x+w, y+h),
		})
	}

	// Debugging rendering (visually see when the tui rerenders)
	if uiDebug() {
		debugView := lipgloss.NewStyle().Background(lipgloss.ANSIColor(rand.Intn(256))).Width(4).Height(2)
		debug := uv.NewStyledString(debugView.String())
		debug.Draw(scr, image.Rectangle{
			Min: image.Pt(4, 1),
			Max: image.Pt(8, 3),
		})
	}

	// Toasts sit over the main view, under the dialogs.
	m.drawToasts(scr, area)

	// This needs to come last to overlay on top of everything. We always pass
	// the full screen bounds because the dialogs will position themselves
	// accordingly.
	if m.dialog.HasDialogs() {
		return m.dialog.Draw(scr, scr.Bounds())
	}

	switch m.focus {
	case uiFocusEditor:
		if m.layout.editor.Dy() <= 0 {
			// Don't show cursor if editor is not visible
			return nil
		}
		if m.detailsOpen && m.isCompact {
			// Don't show cursor if details overlay is open
			return nil
		}

		if m.activeInline != nil {
			if cur := m.inlineCursor; cur != nil {
				cur.X++                        // Adjust for app margins
				cur.Y += m.layout.editor.Min.Y // Inline editor draws from area top
				return cur
			}
			return nil
		}

		if m.textarea.Focused() {
			cur := m.textarea.Cursor()
			cur.X++                            // Adjust for app margins
			cur.Y += m.layout.editor.Min.Y + 1 // Offset for attachments row
			return cur
		}
	}
	return nil
}

// uiDebug reports whether DECK_UI_DEBUG asks Draw to mark every redraw.
// Read once: the environment does not change under a running UI, and
// Draw runs on every frame.
var uiDebug = sync.OnceValue(func() bool { return os.Getenv("DECK_UI_DEBUG") == "true" })

// mouseMode determines the Bubble Tea mouse reporting mode to request for
// the current frame. When mouse support is disabled via configuration, no
// mouse mode is requested so the terminal emulator (or tmux) can handle
// text selection, copy/paste, and scrolling natively. Inline editors and
// hoverable dialogs need motion events even without a button pressed (e.g.
// for hover/drag), so they use MouseModeAllMotion; everything else only
// needs click/drag tracking via MouseModeCellMotion.
func mouseMode(enabled, wantsMotion bool) tea.MouseMode {
	switch {
	case !enabled:
		return tea.MouseModeNone
	case wantsMotion:
		return tea.MouseModeAllMotion
	default:
		return tea.MouseModeCellMotion
	}
}

// View renders the UI model's view.
func (m *UI) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if !m.isTransparent {
		v.BackgroundColor = m.com.Styles.Background
	}
	// The landing wordmark ripples under the pointer, so it needs every move.
	v.MouseMode = mouseMode(m.mouseEnabled, m.activeInline != nil || m.dialog.HandlesHover() || m.state == uiLanding || m.notices.toasts.Len() > 0)
	v.ReportFocus = m.caps.ReportFocusEvents
	v.WindowTitle = "deck " + home.Short(m.com.Workspace.WorkingDir())

	key, cacheable := m.currentFrameKey()
	fx := !m.yoloFX.Done(time.Now())
	if cacheable && !fx {
		if content, cursor, ok := m.frames.get(key); ok {
			v.Content = content
			v.Cursor = cursor
			m.applyProgressBar(&v)
			v.ProgramStatus = m.programStatus()
			return v
		}
	}

	// Reuse the canvas across frames: Draw clears it first, and a fresh
	// width*height buffer per frame is pure garbage at animation rates.
	if m.canvas.RenderBuffer == nil || m.canvas.Width() != m.width || m.canvas.Height() != m.height {
		m.canvas = uv.NewScreenBuffer(m.width, m.height)
	}
	canvas := m.canvas
	m.beginFrameMemo()
	v.Cursor = m.Draw(canvas, canvas.Bounds())
	m.endFrameMemo()

	m.frameBuf = trimFrameLines(m.frameBuf[:0], canvas.Render())
	content := string(m.frameBuf)
	if fx {
		v.Content = m.yoloFX.Apply(content, m.width, time.Now())
		m.applyProgressBar(&v)
		v.ProgramStatus = m.programStatus()
		return v
	}

	v.Content = content
	if cacheable {
		m.storeFrame(key, content, v.Cursor)
	}
	m.applyProgressBar(&v)
	v.ProgramStatus = m.programStatus()

	return v
}

// trimFrameLines appends s to dst with CRLF normalized to LF and the
// trailing spaces of every line trimmed. It is one pass into a buffer
// View keeps between frames: View runs it on the whole screen each frame,
// and splitting into a slice of lines to join again allocated twice.
func trimFrameLines(dst []byte, s string) []byte {
	for {
		i := strings.IndexByte(s, '\n')
		line := s
		if i >= 0 {
			line = strings.TrimSuffix(s[:i], "\r")
		}
		dst = append(dst, strings.TrimRight(line, " ")...)
		if i < 0 {
			return dst
		}
		dst = append(dst, '\n')
		s = s[i+1:]
	}
}

// applyProgressBar attaches the terminal progress bar while the agent is
// busy. Kept outside the frame cache so the randomized value stays fresh.
func (m *UI) applyProgressBar(v *tea.View) {
	if m.progressBarEnabled && m.sendProgressBar && m.isAgentBusy() {
		// HACK: use a random percentage to prevent ghostty from hiding it
		// after a timeout.
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, rand.Intn(100))
	}
}

// ShortHelp implements [help.KeyMap].
func (m *UI) ShortHelp() []key.Binding {
	var binds []key.Binding
	k := &m.keyMap

	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		if m.focus == uiFocusEditor {
			return m.activeInline.ShortHelp()
		}
		return []key.Binding{
			m.inlineFocusHelp(),
			k.Chat.UpDown,
			k.Chat.PageUp,
			k.Chat.PageDown,
		}
	}

	tab := k.Tab
	commands := k.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		commands.SetHelp("/ or ctrl+p", "commands")
	}

	switch m.state {
	case uiInitialize:
		binds = append(binds, k.Quit)
	case uiChat:
		// Show cancel binding if agent is busy.
		if m.isAgentBusy() {
			cancelBinding := k.Chat.Cancel
			if m.isCanceling {
				cancelBinding.SetHelp("esc", "press again to cancel")
			} else if m.promptQueue > 0 {
				cancelBinding.SetHelp("esc", "clear queue")
			}
			binds = append(binds, cancelBinding)
		}

		switch m.focus {
		case uiFocusEditor:
			tab.SetHelp("tab", "focus chat")
		default:
			tab.SetHelp("tab", "focus editor")
		}

		binds = append(
			binds,
			tab,
			k.ShiftTab,
			commands,
			k.Models,
		)

		if m.canToggleSidebar() {
			binds = append(binds, k.Chat.ToggleSidebar)
		}

		switch m.focus {
		case uiFocusEditor:
			binds = append(
				binds,
				k.Editor.Newline,
			)
		case uiFocusSidebar:
			binds = append(
				binds,
				k.Sidebar.Move,
				k.Sidebar.Close,
				k.Sidebar.Reopen,
				k.Sidebar.Back,
			)
		case uiFocusMain:
			binds = append(
				binds,
				k.Chat.UpDown,
				k.Chat.UpDownOneItem,
				k.Chat.PageUp,
				k.Chat.PageDown,
				k.Chat.Copy,
			)
			if m.pillsExpanded && hasIncompleteTodos(m.session.Todos) && m.promptQueue > 0 {
				binds = append(binds, k.Chat.PillLeft)
			}
		}
	default:
		// TODO: other states
		// if m.session == nil {
		// no session selected
		binds = append(binds,
			k.Tab,
			commands,
			k.ShiftTab,
			k.Models,
			k.Editor.Newline,
		)
	}

	binds = append(
		binds,
		k.Quit,
		k.Help,
	)

	return binds
}

// FullHelp implements [help.KeyMap].
func (m *UI) FullHelp() [][]key.Binding {
	// When an inline editor is active, show its help.
	if m.activeInline != nil {
		if m.focus == uiFocusEditor {
			return [][]key.Binding{m.activeInline.ShortHelp()}
		}
		return [][]key.Binding{
			{m.inlineFocusHelp()},
			{
				m.keyMap.Chat.UpDown,
				m.keyMap.Chat.UpDownOneItem,
				m.keyMap.Chat.PageUp,
				m.keyMap.Chat.PageDown,
			},
			{
				m.keyMap.Chat.HalfPageUp,
				m.keyMap.Chat.HalfPageDown,
				m.keyMap.Chat.Home,
				m.keyMap.Chat.End,
			},
		}
	}

	var binds [][]key.Binding
	k := &m.keyMap
	help := k.Help
	help.SetHelp("ctrl+g", "less")
	hasAttachments := len(m.attachments.List()) > 0
	hasSession := m.hasSession()
	commands := k.Commands
	if m.focus == uiFocusEditor && m.textarea.Value() == "" {
		commands.SetHelp("/ or ctrl+p", "commands")
	}

	switch m.state {
	case uiInitialize:
		binds = append(binds,
			[]key.Binding{
				k.Quit,
			})
	case uiChat:
		// Show cancel binding if agent is busy.
		if m.isAgentBusy() {
			cancelBinding := k.Chat.Cancel
			if m.isCanceling {
				cancelBinding.SetHelp("esc", "press again to cancel")
			} else if m.promptQueue > 0 {
				cancelBinding.SetHelp("esc", "clear queue")
			}
			binds = append(binds, []key.Binding{cancelBinding})
		}

		mainBinds := []key.Binding{}
		tab := k.Tab
		switch m.focus {
		case uiFocusEditor:
			tab.SetHelp("tab", "focus chat")
		default:
			tab.SetHelp("tab", "focus editor")
		}

		mainBinds = append(
			mainBinds,
			tab,
			k.ShiftTab,
			commands,
			k.Models,
			k.Sessions,
			k.ToggleYolo,
			k.Git,
			k.Usage,
		)
		if hasSession {
			mainBinds = append(mainBinds, k.Chat.NewSession, k.Chat.EndFollow)
		}
		if m.canToggleSidebar() {
			mainBinds = append(mainBinds, k.Chat.ToggleSidebar)
		}
		if m.stripActive() {
			mainBinds = append(mainBinds, k.SidebarColumnLeft)
		}

		binds = append(binds, mainBinds)

		switch m.focus {
		case uiFocusEditor:
			editorBinds := []key.Binding{
				k.Editor.Newline,
				k.Editor.MentionAgent,
				k.Editor.MentionFile,
				k.Editor.OpenEditor,
				k.Editor.PasteText,
				k.Editor.SelectAll,
				k.Editor.CopySelection,
				k.Editor.CutSelection,
			}
			if m.currentModelSupportsImages() {
				editorBinds = append(editorBinds, k.Editor.AddImage, k.Editor.PasteImage)
			}
			binds = append(binds, editorBinds)
			if hasAttachments {
				binds = append(
					binds,
					[]key.Binding{
						k.Editor.AttachmentDeleteMode,
						k.Editor.DeleteAllAttachments,
						k.Editor.Escape,
					},
				)
			}
		case uiFocusSidebar:
			binds = append(
				binds,
				[]key.Binding{
					k.Sidebar.Left,
					k.Sidebar.Right,
					k.Sidebar.Up,
					k.Sidebar.Down,
					k.Sidebar.PageUp,
					k.Sidebar.PageDown,
					k.Sidebar.Top,
					k.Sidebar.Bottom,
				},
				[]key.Binding{
					k.Sidebar.Close,
					k.Sidebar.Reopen,
					k.Sidebar.MovePane,
					k.Sidebar.MoveColumn,
					k.Sidebar.Resize,
					k.Sidebar.Back,
				},
			)
		case uiFocusMain:
			binds = append(
				binds,
				[]key.Binding{
					k.Chat.UpDown,
					k.Chat.UpDownOneItem,
					k.Chat.PageUp,
					k.Chat.PageDown,
				},
				[]key.Binding{
					k.Chat.HalfPageUp,
					k.Chat.HalfPageDown,
					k.Chat.Home,
					k.Chat.End,
					k.Chat.EndFollow,
					k.Chat.FocusSidebar,
				},
				[]key.Binding{
					k.Chat.Copy,
					k.Chat.ClearHighlight,
				},
			)
			if m.pillsExpanded && hasIncompleteTodos(m.session.Todos) && m.promptQueue > 0 {
				binds = append(binds, []key.Binding{k.Chat.PillLeft})
			}
		}
	default:
		if m.session == nil {
			// no session selected
			binds = append(
				binds,
				[]key.Binding{
					k.ShiftTab,
					commands,
					k.Models,
					k.Sessions,
					k.ToggleYolo,
				},
			)
			editorBinds := []key.Binding{
				k.Editor.Newline,
				k.Editor.MentionAgent,
				k.Editor.MentionFile,
				k.Editor.OpenEditor,
				k.Editor.PasteText,
				k.Editor.SelectAll,
				k.Editor.CopySelection,
				k.Editor.CutSelection,
			}
			if m.currentModelSupportsImages() {
				editorBinds = append(editorBinds, k.Editor.AddImage, k.Editor.PasteImage)
			}
			binds = append(binds, editorBinds)
			if hasAttachments {
				binds = append(
					binds,
					[]key.Binding{
						k.Editor.AttachmentDeleteMode,
						k.Editor.DeleteAllAttachments,
						k.Editor.Escape,
					},
				)
			}
		}
	}

	binds = append(
		binds,
		[]key.Binding{
			help,
			k.Quit,
		},
	)

	return binds
}

// inlineFocusHelp returns the Tab binding used to restore a blurred inline
// editor. Collapsible editors provide context-specific wording.
func (m *UI) inlineFocusHelp() key.Binding {
	tab := m.keyMap.Tab
	description := "focus editor"
	if collapsed, ok := m.activeInline.(dialog.CollapsibleInlineEditor); ok {
		description = collapsed.CollapsedHelp()
	}
	tab.SetHelp("tab", description)
	return tab
}

func (m *UI) currentModelSupportsImages() bool {
	if it, ok := m.com.Workspace.(workspace.ImageTaker); ok {
		return it.AcceptsImages()
	}
	cfg := m.com.Config()
	if cfg == nil {
		return false
	}
	agentCfg, ok := cfg.Agents[config.AgentCoder]
	if !ok {
		return false
	}
	model := cfg.GetModelByType(agentCfg.Model)
	return model != nil && model.SupportsImages
}

// toggleCompactMode toggles compact mode between uiChat and uiChatCompact states.
func (m *UI) toggleCompactMode() tea.Cmd {
	m.forceCompactMode = !m.forceCompactMode

	err := m.com.Workspace.SetCompactMode(config.ScopeGlobal, m.forceCompactMode)
	if err != nil {
		return util.ReportError(err)
	}

	var cmds []tea.Cmd
	if m.forceCompactMode && m.focus == uiFocusSidebar {
		// The sidebar is going away, so focus the editor again to keep key
		// events routed somewhere useful.
		m.strip().chooser = nil
		if m.activeInline != nil {
			m.focusActiveInline(uiFocusEditor)
		} else {
			m.focus = uiFocusEditor
			cmds = append(cmds, m.textarea.Focus())
		}
	}

	m.updateLayoutAndSize()

	return tea.Batch(cmds...)
}

// canToggleSidebar reports whether the sidebar can be shown right now, i.e.
// a chat session is active and the terminal is large enough for the full
// layout.
func (m *UI) canToggleSidebar() bool {
	return m.state == uiChat && m.hasSession() &&
		m.width >= compactModeWidthBreakpoint &&
		m.height >= compactModeHeightBreakpoint
}

// updateLayoutAndSize updates the layout and sizes of UI components.
func (m *UI) updateLayoutAndSize() {
	// Determine if we should be in compact mode
	if m.state == uiChat {
		if m.forceCompactMode {
			m.isCompact = true
		} else if m.width < compactModeWidthBreakpoint || m.height < compactModeHeightBreakpoint {
			m.isCompact = true
		} else {
			m.isCompact = false
		}
		// A window too small for the sidebar takes it away; keep the
		// keyboard somewhere that is still on screen.
		if m.isCompact && m.focus == uiFocusSidebar {
			m.strip().chooser = nil
			if m.activeInline != nil {
				m.focusActiveInline(uiFocusEditor)
			} else {
				m.focus = uiFocusEditor
				m.textarea.Focus()
			}
		}
	}

	// First pass sizes components from their current heights.
	previousInlineHeight := -1
	if m.activeInline != nil {
		previousInlineHeight = m.activeInline.Height(m.editorContentWidth())
	}
	m.layout = m.generateLayout(m.width, m.height)
	prevHeight := m.textarea.Height()
	m.updateSize()

	// SetWidth can change textarea or inline-editor height due to soft-wrap
	// recalculation. If that happens, reconcile once with the new height.
	inlineHeightChanged := m.activeInline != nil &&
		m.activeInline.Height(m.editorContentWidth()) != previousInlineHeight
	if m.textarea.Height() != prevHeight || inlineHeightChanged {
		m.layout = m.generateLayout(m.width, m.height)
		m.updateSize()
	}
}

// handleTextareaHeightChange checks whether the textarea height changed and,
// if so, recalculates the layout. When the chat is in follow mode it keeps
// the view scrolled to the bottom. The returned command, if non-nil, must be
// batched by the caller.
func (m *UI) handleTextareaHeightChange(prevHeight int) tea.Cmd {
	if m.textarea.Height() == prevHeight {
		return nil
	}
	m.updateLayoutAndSize()
	if m.state == uiChat && m.chat.Follow() {
		m.chat.ScrollToBottom()
	}
	return nil
}

// updateTextarea updates the textarea for msg and then reconciles layout if
// the textarea height changed as a result.
func (m *UI) updateTextarea(msg tea.Msg) tea.Cmd {
	return m.updateTextareaWithPrevHeight(msg, m.textarea.Height())
}

// forwardMouseToTextarea forwards a mouse event to the textarea with
// coordinates translated into the textarea's local space. It reports whether
// the event landed within the textarea's rendered region and was forwarded.
func (m *UI) forwardMouseToTextarea(msg tea.MouseMsg) (bool, tea.Cmd) {
	mouse := msg.Mouse()

	// The textarea is rendered inside layout.editor below the attachments
	// row. renderEditorView always reserves the first row for attachments
	// (an empty line when there are none), so the textarea always starts
	// one row below the editor top.
	const attachmentsRow = 1
	origin := image.Pt(m.layout.editor.Min.X, m.layout.editor.Min.Y+attachmentsRow)

	// The textarea occupies its own height starting at the origin.
	area := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(m.layout.editor.Dx(), m.textarea.Height()))}
	if !image.Pt(mouse.X, mouse.Y).In(area) {
		return false, nil
	}

	rel := tea.Mouse{
		X:      mouse.X - origin.X,
		Y:      mouse.Y - origin.Y,
		Button: mouse.Button,
		Mod:    mouse.Mod,
	}

	switch msg.(type) {
	case tea.MouseClickMsg:
		if rel.Button != uv.MouseLeft {
			return false, nil
		}
		m.textareaMouseSelecting = true
		m.textarea.BeginSelection(rel.X, rel.Y)
		return true, nil
	case tea.MouseMotionMsg:
		if !m.textareaMouseSelecting {
			return true, nil
		}
		m.textarea.ExtendSelection(rel.X, rel.Y)
		return true, nil
	case tea.MouseReleaseMsg:
		m.textarea.EndSelection()
		m.textareaMouseSelecting = false
		return true, nil
	default:
		return false, nil
	}
}

// updateTextareaWithPrevHeight is for cases when the height of the layout may
// have changed.
//
// Particularly, it's for cases where the textarea changes before
// textarea.Update is called (for example, SetValue, Reset, and InsertRune). We
// pass the height from before those changes took place so we can compare
// "before" vs "after" sizing and recalculate the layout if the textarea grew
// or shrank.
func (m *UI) updateTextareaWithPrevHeight(msg tea.Msg, prevHeight int) tea.Cmd {
	ta, cmd := m.textarea.Update(msg)
	m.textarea = ta
	return tea.Batch(cmd, m.handleTextareaHeightChange(prevHeight))
}

// updateSize updates the sizes of UI components based on the current layout.
func (m *UI) updateSize() {
	m.invalidateFrames()

	// Set status width
	m.status.SetWidth(m.layout.status.Dx())

	m.chat.SetSize(m.layout.main.Dx(), m.layout.main.Dy())
	m.textarea.MaxHeight = TextareaMaxHeight
	m.textarea.SetWidth(m.layout.editor.Dx())
	if resizable, ok := m.activeInline.(dialog.ResizableInlineEditor); ok {
		resizable.SetWidth(m.layout.editor.Dx())
	}
	m.renderPills()

	// Handle different app states
	switch m.state {
	case uiChat:
		if !m.isCompact {
			m.cacheSidebarLogo(m.layout.sidebar.Dx())
		}
	}
}

// generateLayout calculates the layout rectangles for all UI components based
// on the current UI state and terminal dimensions.
func (m *UI) generateLayout(w, h int) uiLayout {
	// The screen area we're working with
	area := image.Rect(0, 0, w, h)

	// The help height
	helpHeight := 1
	// The editor height: textarea height + margin for attachments and bottom spacing.
	// When an inline editor is active, use its height instead.
	editorHeight := m.textarea.Height() + editorHeightMargin
	if m.activeInline != nil {
		// The editor content width depends only on terminal width
		// and layout (not on editor height), so passing the current
		// frame's width to Height() keeps layout in sync with the
		// width Draw will use, preventing flicker during fast resize.
		editorWidth := m.editorContentWidth()
		if collapsed, ok := m.collapsedInlineEditor(); ok {
			editorHeight = collapsed.CollapsedHeight() + 1
		} else {
			editorHeight = m.activeInline.Height(editorWidth)
		}
	}
	// The sidebar width
	// The sidebar width: the classic column, wider on wide terminals so the
	// strip shows columns side by side (strip_draw.go). The app area is
	// the window less a cell of margin on each side.
	sidebarWidth := sidebarNarrowWidth
	if m.state == uiChat && !m.isCompact {
		sidebarWidth = m.sidebarWidth(w - 2)
	}
	// The header height
	// The wordmark and its tagline: twice the size when the window has the
	// room, so the landing page reads loud rather than empty.
	landingHeaderHeight := 7
	if h >= 34 && w >= 110 {
		landingHeaderHeight = 12
	}

	var helpKeyMap help.KeyMap = m
	if m.status != nil && m.status.ShowingAll() {
		for _, row := range helpKeyMap.FullHelp() {
			helpHeight = max(helpHeight, len(row))
		}
	}

	// Add app margins
	var appRect, helpRect image.Rectangle
	layout.Vertical(
		layout.Len(area.Dy()-helpHeight),
		layout.Fill(1),
	).Split(area).Assign(&appRect, &helpRect)
	appRect.Min.Y += 1
	appRect.Max.Y -= 1
	helpRect.Min.Y -= 1
	appRect.Min.X += 1
	appRect.Max.X -= 1

	if slices.Contains([]uiState{uiOnboarding, uiInitialize, uiLanding}, m.state) {
		// extra padding on left and right for these states
		appRect.Min.X += 1
		appRect.Max.X -= 1
	}

	uiLayout := uiLayout{
		area:   area,
		status: helpRect,
	}

	// Handle different app states
	switch m.state {
	case uiOnboarding, uiInitialize:
		// Layout
		//
		// header
		// ------
		// main
		// ------
		// help

		var headerRect, mainRect image.Rectangle
		layout.Vertical(
			layout.Len(landingHeaderHeight),
			layout.Fill(1),
		).Split(appRect).Assign(&headerRect, &mainRect)
		uiLayout.header = headerRect
		uiLayout.main = mainRect

	case uiLanding:
		// Layout
		//
		// header
		// ------
		// main
		// ------
		// editor
		// ------
		// help
		var headerRect, mainRect image.Rectangle
		layout.Vertical(
			layout.Len(landingHeaderHeight),
			layout.Fill(1),
		).Split(appRect).Assign(&headerRect, &mainRect)
		var editorRect image.Rectangle
		layout.Vertical(
			layout.Len(mainRect.Dy()-editorHeight),
			layout.Fill(1),
		).Split(mainRect).Assign(&mainRect, &editorRect)
		// Remove extra padding from editor (but keep it for header and main)
		editorRect.Min.X -= 1
		editorRect.Max.X += 1
		uiLayout.header = headerRect
		uiLayout.main = mainRect
		uiLayout.editor = editorRect

	case uiChat:
		if m.isCompact {
			// Layout
			//
			// compact-header
			// ------
			// main
			// ------
			// editor
			// ------
			// help
			const compactHeaderHeight = 1
			var headerRect, mainRect image.Rectangle
			layout.Vertical(
				layout.Len(compactHeaderHeight),
				layout.Fill(1),
			).Split(appRect).Assign(&headerRect, &mainRect)
			detailsHeight := min(sessionDetailsMaxHeight, area.Dy()-1) // One row for the header
			var sessionDetailsArea image.Rectangle
			layout.Vertical(
				layout.Len(detailsHeight),
				layout.Fill(1),
			).Split(appRect).Assign(&sessionDetailsArea, new(image.Rectangle))
			uiLayout.sessionDetails = sessionDetailsArea
			uiLayout.sessionDetails.Min.Y += compactHeaderHeight // adjust for header
			// Add one line gap between header and main content
			mainRect.Min.Y += 1
			var editorRect image.Rectangle
			layout.Vertical(
				layout.Len(mainRect.Dy()-editorHeight),
				layout.Fill(1),
			).Split(mainRect).Assign(&mainRect, &editorRect)
			mainRect.Max.X -= 1 // Add padding right
			uiLayout.header = headerRect
			pillsHeight := m.pillsAreaHeight()
			if pillsHeight > 0 {
				pillsHeight = min(pillsHeight, mainRect.Dy())
				var chatRect, pillsRect image.Rectangle
				layout.Vertical(
					layout.Len(mainRect.Dy()-pillsHeight),
					layout.Fill(1),
				).Split(mainRect).Assign(&chatRect, &pillsRect)
				uiLayout.main = chatRect
				uiLayout.pills = pillsRect
			} else {
				uiLayout.main = mainRect
			}
			// Add bottom margin to main
			uiLayout.main.Max.Y -= 1
			uiLayout.editor = editorRect
		} else {
			// Layout
			//
			// ------|---
			// main  |
			// ------| side
			// editor|
			// ----------
			// help

			var mainRect, sideRect image.Rectangle
			layout.Horizontal(
				layout.Len(appRect.Dx()-sidebarWidth),
				layout.Fill(1),
			).Split(appRect).Assign(&mainRect, &sideRect)
			// Add padding left
			sideRect.Min.X += 1
			var editorRect image.Rectangle
			layout.Vertical(
				layout.Len(mainRect.Dy()-editorHeight),
				layout.Fill(1),
			).Split(mainRect).Assign(&mainRect, &editorRect)
			mainRect.Max.X -= 1 // Add padding right
			uiLayout.sidebar = sideRect
			pillsHeight := m.pillsAreaHeight()
			if pillsHeight > 0 {
				pillsHeight = min(pillsHeight, mainRect.Dy())
				var chatRect, pillsRect image.Rectangle
				layout.Vertical(
					layout.Len(mainRect.Dy()-pillsHeight),
					layout.Fill(1),
				).Split(mainRect).Assign(&chatRect, &pillsRect)
				uiLayout.main = chatRect
				uiLayout.pills = pillsRect
			} else {
				uiLayout.main = mainRect
			}
			// Add bottom margin to main
			uiLayout.main.Max.Y -= 1
			uiLayout.editor = editorRect
		}
	}

	return uiLayout
}

// uiLayout defines the positioning of UI elements.
type uiLayout struct {
	// area is the overall available area.
	area uv.Rectangle

	// header is the header shown in special cases
	// e.x when the sidebar is collapsed
	// or when in the landing page
	// or in init/config
	header uv.Rectangle

	// main is the area for the main pane. (e.x chat, configure, landing)
	main uv.Rectangle

	// pills is the area for the pills panel.
	pills uv.Rectangle

	// editor is the area for the editor pane.
	editor uv.Rectangle

	// sidebar is the area for the sidebar.
	sidebar uv.Rectangle

	// status is the area for the status view.
	status uv.Rectangle

	// session details is the area for the session details overlay in compact mode.
	sessionDetails uv.Rectangle
}

func (m *UI) openEditor(value string) tea.Cmd {
	tmpfile, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return util.ReportError(err)
	}
	tmpPath := tmpfile.Name()
	defer tmpfile.Close() //nolint:errcheck
	if _, err := tmpfile.WriteString(value); err != nil {
		return util.ReportError(err)
	}
	cmd, err := editor.Command(
		"deck",
		tmpPath,
		editor.AtPosition(
			m.textarea.Line()+1,
			m.textarea.Column()+1,
		),
	)
	if err != nil {
		return util.ReportError(err)
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() {
			_ = os.Remove(tmpPath)
		}()

		if err != nil {
			return util.ReportError(err)
		}
		return editorFileMsg(tmpPath)
	})
}

// editorFileMsg reads the file the external editor was supposed to write and
// turns it into the message that replaces the composer text. An emptied buffer
// yields an empty Text, which clears the composer just like deleting every
// character in-app does.
func editorFileMsg(path string) tea.Msg {
	content, err := os.ReadFile(path)
	if err != nil {
		return util.ReportError(err)
	}
	return openEditorMsg{
		Text: strings.TrimSpace(string(content)),
	}
}

// setEditorPrompt configures the textarea prompt function based on whether
// plan, yolo, or bang mode is enabled.
func (m *UI) setEditorPrompt(yolo bool) {
	if m.bangMode {
		m.textarea.SetPromptFunc(4, m.bangPromptFunc)
		return
	}
	if m.mode == uiInputModePlan {
		m.textarea.SetPromptFunc(4, m.planPromptFunc)
		return
	}
	if yolo {
		m.textarea.SetPromptFunc(4, m.yoloPromptFunc)
		return
	}
	m.textarea.SetPromptFunc(4, m.normalPromptFunc)
}

// normalPromptFunc returns the normal editor prompt style ("> " on the
// first line, "::: " on subsequent lines).
func (m *UI) normalPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.Editor.PromptNormalIconFocused.Render()
		}
		return "::: "
	}
	if info.Focused {
		return t.Editor.PromptNormalFocused.Render()
	}
	return t.Editor.PromptNormalBlurred.Render()
}

// planPromptFunc marks planning with a badge beside the editor.
func (m *UI) planPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.Editor.PromptPlanIconFocused.Render()
		}
		return t.Editor.PromptPlanIconBlurred.Render()
	}
	if info.Focused {
		return t.Editor.PromptPlanDotsFocused.Render()
	}
	return t.Editor.PromptPlanDotsBlurred.Render()
}

// yoloPromptFunc returns the yolo mode editor prompt style with warning icon
// and colored dots.
func (m *UI) yoloPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.Editor.PromptYoloIconFocused.Render()
		} else {
			return t.Editor.PromptYoloIconBlurred.Render()
		}
	}
	if info.Focused {
		return t.Editor.PromptYoloDotsFocused.Render()
	}
	return t.Editor.PromptYoloDotsBlurred.Render()
}

// bangPromptFunc returns the bang mode editor prompt style with Turtle-colored
// icon and dots.
func (m *UI) bangPromptFunc(info textarea.PromptInfo) string {
	t := m.com.Styles
	if info.LineNumber == 0 {
		if info.Focused {
			return t.Editor.PromptBangIconFocused.Render()
		}
		return t.Editor.PromptBangIconBlurred.Render()
	}
	if info.Focused {
		return t.Editor.PromptBangDotsFocused.Render()
	}
	return t.Editor.PromptBangDotsBlurred.Render()
}

func (m *UI) toggleInputMode() tea.Cmd {
	if m.isAgentBusy() || m.modeSwitching {
		return util.ReportWarn("Agent is busy, please wait before switching input mode...")
	}
	if m.mode == uiInputModePlan {
		// Second step of the Shift+Tab cycle: plan -> YOLO. Enabling YOLO
		// here is the only case where the cycle may disable it again.
		if !m.com.Workspace.PermissionSkipRequests() {
			m.toggleYoloMode()
			m.cycleYolo = true
		}
		return m.setInputMode(uiInputModeCode)
	}
	// Only the cycle may turn YOLO back off: YOLO the user enabled himself
	// (Ctrl+Y, the command palette) survives entering plan mode.
	if m.com.Workspace.PermissionSkipRequests() && m.cycleYolo {
		m.toggleYoloMode()
		return util.ReportInfo("input mode: code")
	}
	return m.setInputMode(uiInputModePlan)
}

// switchPlanToYolo handles activating YOLO while in plan mode: YOLO is a
// coding concern, so instead of a "plan + yolo" state the UI switches
// straight to the coder with YOLO enabled. Activation is idempotent — YOLO
// carried into plan mode stays on, and the user ends up in full YOLO mode
// either way.
func (m *UI) switchPlanToYolo() tea.Cmd {
	if m.isAgentBusy() || m.modeSwitching {
		return util.ReportWarn("Agent is busy, please wait before switching input mode...")
	}
	if !m.com.Workspace.PermissionSkipRequests() {
		m.toggleYoloMode()
	}
	// Explicit activation pins YOLO: the Shift+Tab cycle must not disable
	// it on the next pass.
	m.cycleYolo = false
	return m.setInputMode(uiInputModeCode)
}

// Mode banner copy shown in the status bar after switching modes.
const (
	planModeBannerMsg = "Plan with Deck before generating any code."
	yoloModeBannerMsg = "Skip permission prompts. System level commands will be blocked."
)

func (m *UI) setInputMode(target uiInputMode) tea.Cmd {
	agentID := config.AgentPlan
	if target == uiInputModeCode {
		agentID = config.AgentCoder
	}

	// YOLO is orthogonal to the input mode, so report it alongside the mode
	// instead of treating a YOLO-enabled coder as plain "code".
	yolo := target == uiInputModeCode && m.com.Workspace.PermissionSkipRequests()

	// The agent switch is an HTTP round-trip in client/server mode, so it
	// runs off the update loop together with the model update. The mode and
	// editor prompt only change once the switch succeeds (applyModeSwitch),
	// so a failed switch never leaves the editor claiming a mode the
	// server's active agent does not match.
	m.modeSwitching = true
	return func() tea.Msg {
		err := m.com.Workspace.AgentSetMain(agentID)
		if err == nil {
			err = m.com.Workspace.UpdateAgentModel(context.Background())
		}
		return modeSwitchedMsg{
			mode: target,
			yolo: yolo,
			err:  err,
		}
	}
}

// applyModeSwitch finalizes an input-mode switch once the backend has
// settled. On error the previous mode is kept so the editor never claims a
// mode the server's active agent does not match.
func (m *UI) applyModeSwitch(msg modeSwitchedMsg) []tea.Cmd {
	if msg.err != nil {
		return []tea.Cmd{util.ReportError(msg.err)}
	}
	m.mode = msg.mode
	m.setEditorPrompt(m.yoloModeCached())
	var cmds []tea.Cmd
	if msg.continueSessionID != "" && m.session != nil && m.session.ID == msg.continueSessionID {
		cmds = append(cmds, m.sendMessageInternal("Implement the plan.", true))
	}
	switch {
	case msg.mode == uiInputModePlan:
		cmds = append(cmds, util.CmdHandler(util.InfoMsg{Type: util.InfoTypePlan, Msg: planModeBannerMsg}))
	case msg.yolo:
		cmds = append(cmds, util.CmdHandler(util.InfoMsg{Type: util.InfoTypeYolo, Msg: yoloModeBannerMsg}))
	default:
		cmds = append(cmds, util.ReportInfo("input mode: code"))
	}
	return cmds
}

// modeSwitchedMsg reports that the async agent switch started by
// setInputMode has finished (successfully or not).
type modeSwitchedMsg struct {
	continueSessionID string
	mode              uiInputMode
	yolo              bool
	err               error
}

// closeCompletions closes the completions popup and resets state.
func (m *UI) closeCompletions() {
	m.completionsOpen = false
	m.completionsFiles = false
	m.completionsQuery = ""
	m.completionsStartIndex = 0
	m.completions.Close()
}

// insertCompletionText replaces the @query in the textarea with the given text.
// Returns false if the replacement cannot be performed.
func (m *UI) insertCompletionText(text string) bool {
	value := m.textarea.Value()
	if m.completionsStartIndex > len(value) {
		return false
	}

	word := m.textareaWord()
	endIdx := min(m.completionsStartIndex+len(word), len(value))
	newValue := value[:m.completionsStartIndex] + text + value[endIdx:]
	m.textarea.SetValue(newValue)
	m.textarea.MoveToEnd()
	m.textarea.InsertRune(' ')
	return true
}

// agentCompletionsMsg says the agent CLIs' model lists are in, for the $
// popup.
type agentCompletionsMsg struct{}

// openAgentCompletions opens the $ popup on what is known at once, and asks
// the CLIs that have not listed their models yet.
func (m *UI) openAgentCompletions() tea.Cmd {
	m.completions.SetAgentItems(m.agentCompletionItems())
	picker, ok := m.com.Workspace.(workspace.AgentPicker)
	if !ok {
		return nil
	}
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = picker.AgentModelChoices(ctx, sessionID)
		return agentCompletionsMsg{}
	}
}

// agentCompletionItems is every agent with every model it offers, its
// current model first.
func (m *UI) agentCompletionItems() []completions.AgentItem {
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	var choices []workspace.ModelChoice
	if picker, ok := m.com.Workspace.(workspace.AgentPicker); ok {
		choices = picker.CachedAgentModelChoices(sessionID)
	}
	var cur, rest []completions.AgentItem
	for _, c := range choices {
		label := tone.Mark(c.Kind) + " " + c.Kind + " · " + c.Name
		effort := c.CurrentEffort
		if c.Current {
			label += "  (now)"
		} else {
			effort = ""
		}
		it := completions.AgentItem{Label: label, Value: completions.AgentCompletionValue{
			Kind: c.Kind, Model: c.ID, Effort: effort, Current: c.Current,
		}}
		if c.Current {
			cur = append(cur, it)
		} else {
			rest = append(rest, it)
		}
	}
	var lines []workspace.AgentLine
	if roster, ok := m.com.Workspace.(workspace.Roster); ok && m.session != nil {
		lines = roster.Roster(m.session.ID)
	}
	sigil := m.agentSigil()
	who := append(instanceItems(sigil, lines), peopleItems(sigil, lines, m.profiles, m.teammateNames())...)
	return append(append(cur, who...), rest...)
}

// insertAgentCompletion puts @agent (or $agent) in the message and, when
// another model was picked, switches the agent to it.
func (m *UI) insertAgentCompletion(v completions.AgentCompletionValue) tea.Cmd {
	prevHeight := m.textarea.Height()
	text := m.agentSigil() + v.Kind
	if v.Insert != "" {
		text = v.Insert
	}
	if !m.insertCompletionText(text) {
		return nil
	}
	if v.Insert != "" {
		return m.handleTextareaHeightChange(prevHeight)
	}
	cmds := []tea.Cmd{m.handleTextareaHeightChange(prevHeight)}
	if !v.Current {
		cmds = append(cmds, m.selectAgentModel(dialog.ActionSelectAgentModel{Kind: v.Kind, Model: v.Model, Effort: v.Effort}))
	}
	return tea.Batch(cmds...)
}

// insertFileCompletion inserts the selected file path into the textarea,
// replacing the @query, and adds the file as an attachment.
func (m *UI) insertFileCompletion(path string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(path) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	fileCmd := func() tea.Msg {
		if !m.currentModelSupportsImages() && common.IsImagePath(path) {
			return util.NewWarnMsg("The current model does not support image attachments")
		}

		absPath, _ := filepath.Abs(path)

		if m.hasSession() {
			// Skip attachment if file was already read and hasn't been modified.
			lastRead := m.com.Workspace.FileTrackerLastReadTime(context.Background(), m.session.ID, absPath)
			if !lastRead.IsZero() {
				if info, err := os.Stat(path); err == nil && !info.ModTime().After(lastRead) {
					return nil
				}
			}
		} else if slices.Contains(m.sessionFileReads, absPath) {
			return nil
		}

		m.sessionFileReads = append(m.sessionFileReads, absPath)

		// Add file as attachment.
		content, err := os.ReadFile(path)
		if err != nil {
			// If it fails, let the LLM handle it later.
			return nil
		}

		return message.Attachment{
			FilePath: path,
			FileName: filepath.Base(path),
			MimeType: mimeOf(content),
			Content:  content,
		}
	}
	return tea.Batch(heightCmd, fileCmd)
}

// completionsPosition returns the X and Y position for the completions popup.
func (m *UI) completionsPosition() image.Point {
	cur := m.textarea.Cursor()
	if cur == nil {
		return image.Point{
			X: m.layout.editor.Min.X,
			Y: m.layout.editor.Min.Y,
		}
	}
	return image.Point{
		X: cur.X + m.layout.editor.Min.X,
		Y: m.layout.editor.Min.Y + cur.Y,
	}
}

// textareaWord returns the current word at the cursor position.
func (m *UI) textareaWord() string {
	return m.textarea.Word()
}

// isWhitespace returns true if the byte is a whitespace character.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isAgentBusy returns true if the agent coordinator exists and is currently
// busy processing a request. It only reads the memoized state (it runs in
// per-message paths like the textarea placeholder, where a workspace probe
// would be an HTTP round-trip per keystroke in client/server mode); the
// value is refreshed off-thread, see workspace_cache.go.
func (m *UI) isAgentBusy() bool {
	if m.bangCancel != nil {
		return true
	}
	return m.agentBusyCache.val
}

// hasSession returns true if there is an active session with a valid ID.
func (m *UI) hasSession() bool {
	return m.session != nil && m.session.ID != ""
}

// CurrentSession returns the active session, or nil when there is none.
// It is safe to call after the TUI has exited.
func (m *UI) CurrentSession() *session.Session {
	return m.session
}

// mimeOf detects the MIME type of the given content.
func mimeOf(content []byte) string {
	mimeBufferSize := min(512, len(content))
	return http.DetectContentType(content[:mimeBufferSize])
}

var readyPlaceholders = [...]string{
	"Message your agents — @ picks who and which model, # adds a file",
	"Ask anything; @codex brings codex in, @all asks everyone",
	"What should the agents do?",
}

var workingPlaceholders = [...]string{
	"The agents are on it — type to queue, esc twice to stop",
	"Working — your next message waits its turn",
}

// randomizePlaceholders selects random placeholder text for the textarea's
// ready and working states.
func (m *UI) randomizePlaceholders() {
	m.workingPlaceholder = workingPlaceholders[rand.Intn(len(workingPlaceholders))]
	m.readyPlaceholder = readyPlaceholders[rand.Intn(len(readyPlaceholders))]
}

// renderEditorView renders the editor view with attachments if any.
func (m *UI) renderEditorView(width int) string {
	var attachmentsView string
	if len(m.attachments.List()) > 0 {
		attachmentsView = m.attachments.Render(width)
	} else {
		attachmentsView = m.gitNudgeLine(width)
	}
	return strings.Join([]string{
		attachmentsView,
		m.textarea.View(),
		m.leadLine(width), // the bottom margin: the thread's agent
	}, "\n")
}

// cacheSidebarLogo renders and caches the sidebar logo at the specified width.
func (m *UI) cacheSidebarLogo(width int) {
	m.sidebarLogo = renderLogo(m.com.Styles, true, width)
}

// applyThemeForProvider swaps the active theme to the one associated with
// the given provider, but only when that theme differs from the one
// already applied. Most providers share a single theme, so re-selecting a
// model from the same theme family would otherwise pay the full cost of
// invalidating the markdown renderer cache and re-rendering the entire
// transcript for no visible change.
// A theme explicitly selected in the config always wins, so provider
// changes never discard the user's choice.
func (m *UI) applyThemeForProvider(providerID string) {
	// A theme the user explicitly selected always wins over the
	// per-provider default, so provider or session changes never discard
	// their choice. The in-memory flag covers client/server mode, where
	// the config round-trip may not surface the selection right away.
	if m.userThemeSelected || common.ThemeNameFromConfig(m.com.Config()) != "" {
		return
	}
	key := styles.ThemeKeyForProvider(providerID)
	if key == m.themeKey {
		return
	}
	m.themeKey = key
	m.applyTheme(styles.ThemeForProvider(providerID))
}

// applyTheme replaces the active styles with the given theme, drops the
// shared style caches, and refreshes every component that caches style
// data.
func (m *UI) applyTheme(s styles.Styles) {
	*m.com.Styles = s
	common.InvalidateStyleCaches()
	m.refreshStyles()
	m.chat.InvalidateRenderCaches()
	m.chatWarmPending = true
}

// previewTheme applies the given styles for live preview inside an open
// theme dialog. The whole interface updates, but only the chat messages
// currently on screen re-render; the rest of the transcript keeps its
// cached output so browsing themes stays fast in large sessions. Off-screen
// messages follow along when the theme is actually applied.
// s is only read: it may be a theme LoadThemeShared keeps.
func (m *UI) previewTheme(s *styles.Styles) {
	*m.com.Styles = *s
	common.InvalidateStyleCaches()
	m.refreshStyles()
	m.chat.InvalidateVisibleRenderCaches()
}

// refreshStyles pushes the current *m.com.Styles into every subcomponent
// that copies or pre-renders style-dependent values at construction time.
// Callers are responsible for invalidating chat render caches.
func (m *UI) refreshStyles() {
	m.stylesGen++
	t := m.com.Styles
	tone.SetLight(!t.IsDark)
	m.header.refresh()
	if m.layout.sidebar.Dx() > 0 {
		m.cacheSidebarLogo(m.layout.sidebar.Dx())
	}
	m.textarea.SetStyles(t.Editor.Textarea)
	m.completions.SetStyles(t.Completions.Normal, t.Completions.Focused, t.Completions.Match)
	m.attachments.SetStyles(t.Attachments)
	m.todoSpinner.Style = t.Pills.TodoSpinner
	m.status.help.Styles = t.Help
	if d := m.dialog.Dialog(dialog.ThemeID); d != nil {
		if td, ok := d.(*dialog.Theme); ok {
			td.RefreshStyles()
		}
	}
}

// attachSkill reads a skill's content by ID and returns it as a markdown
// attachment to be added to the attachment toolbar. The user can then
// compose a message and send it with the skill attached.
// The name parameter is used as a fallback when the server does not
// return one.
func (m *UI) attachSkill(skillID, name string) tea.Cmd {
	return func() tea.Msg {
		content, result, err := m.com.Workspace.ReadSkill(context.Background(), skillID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		fileName := result.Name
		if fileName == "" {
			fileName = name
		}
		return message.Attachment{
			FilePath: fileName,
			FileName: fileName,
			MimeType: "text/markdown",
			Content:  content,
		}
	}
}

// openThemeNewDialog opens the new theme naming dialog. The new theme
// inherits its palette from the currently active theme.
func (m *UI) openThemeNewDialog() {
	if m.dialog.ContainsDialog(dialog.ThemeNewID) {
		m.dialog.BringToFront(dialog.ThemeNewID)
		return
	}
	base := common.ThemeNameFromConfig(m.com.Config())
	m.dialog.OpenDialog(dialog.NewThemeNew(m.com, base))
}

// openThemeDialog opens the theme picker dialog.
func (m *UI) openThemeDialog() {
	if m.dialog.ContainsDialog(dialog.ThemeID) {
		m.dialog.BringToFront(dialog.ThemeID)
		return
	}
	themeDialog := dialog.NewTheme(m.com)
	m.dialog.OpenDialog(themeDialog)
}

// openThemeEditorDialog opens the theme editor dialog for the given theme.
// An empty themeName edits the currently active theme.
func (m *UI) openThemeEditorDialog(themeName string) {
	if m.dialog.ContainsDialog(dialog.ThemeEditorID) {
		m.dialog.BringToFront(dialog.ThemeEditorID)
		return
	}
	themeDialog := dialog.NewThemeEditor(m.com, themeName)
	m.dialog.OpenDialog(themeDialog)
}

// ensureSession makes sure a session is active, creating one if none is. It
// returns a command that loads the freshly created session (nil when a session
// already existed) and an error if creation failed. It mutates UI state, so
// callers must run on the Update goroutine.
func (m *UI) ensureSession() (tea.Cmd, error) {
	if m.hasSession() {
		return nil, nil
	}
	newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
	if err != nil {
		return nil, err
	}
	if m.forceCompactMode {
		m.isCompact = true
	}
	var cmd tea.Cmd
	if newSession.ID != "" {
		m.session = &newSession
		cmd = m.loadSession(newSession.ID)
	}
	m.setState(uiChat, m.focus)
	return cmd, nil
}

// sendMessage sends a message with the given content and attachments.
func (m *UI) sendMessage(content string, attachments ...message.Attachment) tea.Cmd {
	if len(attachments) == 0 && isProfilesSlash(content) {
		return m.loadProfiles(true)
	}
	if len(attachments) == 0 && isMobileSlash(content) {
		action, lan, _ := mobileSlashAction(content)
		return m.openMobileDialogRequest(action, lan)
	}
	return m.sendMessageInternal(content, false, attachments...)
}

// sendMessageInternal can hide a generated continuation from the chat.
func (m *UI) sendMessageInternal(content string, hidden bool, attachments ...message.Attachment) tea.Cmd {
	if err := m.com.Workspace.AgentReadyErr(); err != nil {
		return util.ReportError(err)
	}

	// Start the turn timer.
	common.StartTurn()

	// Any new prompt supersedes a pending, unconfirmed plan.
	m.setPlanReadyPending("")

	var cmds []tea.Cmd
	// The agent picked before the session existed takes its first message.
	var pending *dialog.ActionSelectAgentModel
	picker, _ := m.com.Workspace.(workspace.AgentPicker)
	if !m.hasSession() && picker != nil {
		pending, m.pendingLead = m.pendingLead, nil
	}
	loadCmd, err := m.ensureSession()
	if err != nil {
		return util.ReportError(err)
	}
	if loadCmd != nil {
		cmds = append(cmds, loadCmd)
	}
	if pending != nil {
		m.leadGen++
		m.setLead(m.session.ID, pickedLead(*pending))
	}

	ctx := context.Background()
	cmds = append(cmds, func() tea.Msg {
		for _, path := range m.sessionFileReads {
			m.com.Workspace.FileTrackerRecordRead(ctx, m.session.ID, path)
		}
		return nil
	})

	// Capture session ID to avoid race with main goroutine updating m.session.
	sessionID := m.session.ID
	// Optimistically mark the agent busy: the prompt we are about to submit
	// either starts a run or is enqueued behind one. This keeps esc pressed
	// right after enter routing to cancelAgent instead of reading a stale
	// idle value; the authoritative state arrives via agentRunSubmittedMsg.
	// Bump the busy/queue generations so any probe started before this
	// optimistic write is discarded rather than reverting us to idle.
	m.agentBusyCache.set(true)
	m.busyFetchGen++
	m.invalidatePromptQueue()
	cmds = append(cmds, func() tea.Msg {
		// AgentRun is fire-and-forget: it returns once the prompt has
		// been accepted (HTTP 202) or synchronously with a validation
		// or transport error. Run failures and cancellation surface
		// through SSE-derived events, not this return value.
		runCtx := context.Background()
		if hidden {
			runCtx = message.WithHiddenUserMessage(runCtx)
		}
		var leadErr error
		if pending != nil {
			leadErr = picker.SetAgentModel(sessionID, cmp.Or(pending.Agent, pending.Kind), pending.Model, pending.Effort)
		}
		err := m.com.Workspace.AgentRun(runCtx, sessionID, content, attachments...)
		if err == nil {
			err = leadErr
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("%v", err),
			}
		}
		return agentRunSubmittedMsg{}
	})
	return tea.Batch(cmds...)
}

// runShellCommand executes a shell command server-side without triggering
// the LLM. The result is displayed as a tool-style item in the chat.
func (m *UI) runShellCommand(command string) tea.Cmd {
	return m.runShellCommandInternal(command, false)
}

// runShellCommandInternal is the shared implementation for bang-mode shell
// execution. isFirstMessage indicates the command is the first user message
// in a newly created session, which triggers title generation.
func (m *UI) runShellCommandInternal(command string, isFirstMessage bool) tea.Cmd {
	var cmds []tea.Cmd
	if !m.hasSession() {
		newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		if newSession.ID != "" {
			m.session = &newSession
			cmds = append(cmds, m.loadSession(newSession.ID))
		}
		m.setState(uiChat, m.focus)
		// Defer shell execution until loadSessionMsg fires so the chat
		// list is stable before we add items or start streaming.
		m.pendingBangCommand = command
		return tea.Batch(cmds...)
	}

	sessionID := m.session.ID
	contentWidth := min(m.layout.main.Dx()-2, 120)

	// Append a pending shell item immediately so the user sees feedback.
	pendingItem := chat.NewPendingShellItem(m.com.Styles, command)
	// Bang mode runs without the agent, so re-enable the animation clock
	// that a non-busy session reload may have frozen.
	m.chat.SetAnimationsAllowed(true)
	m.chat.AppendMessages(pendingItem)
	m.chat.ScrollToBottom()

	// Stream output via channel. The progress callback writes chunks
	// to streamCh; a reader cmd converts them to shellStreamMsg values.
	streamCh := make(chan string, 64)
	pendingID := pendingItem.ID()

	onProgress := func(chunk string) {
		select {
		case streamCh <- chunk:
		default:
			// Drop if UI can't keep up.
		}
	}

	// Reader cmd: drains streamCh into shellStreamMsg until closed.
	cmds = append(cmds, func() tea.Msg {
		chunk, ok := <-streamCh
		if !ok {
			return nil
		}
		return shellStreamMsg{PendingID: pendingID, Chunk: chunk, streamCh: streamCh}
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.bangCancel = cancel

	cmds = append(cmds, func() tea.Msg {
		resp, err := m.com.Workspace.AgentRunShellCommand(ctx, sessionID, command, contentWidth, onProgress, isFirstMessage)
		close(streamCh)
		if err != nil && !errors.Is(err, context.Canceled) {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("shell: %v", err),
			}
		}
		exitCode := resp.ExitCode
		if errors.Is(err, context.Canceled) {
			exitCode = 130 // conventional SIGINT exit code
		}
		return shellResultMsg{
			PendingID: pendingID,
			Command:   command,
			Output:    resp.Output,
			ExitCode:  exitCode,
		}
	})
	return tea.Batch(cmds...)
}

const cancelTimerDuration = 2 * time.Second

// cancelTimerCmd creates a command that expires the cancel timer.
func cancelTimerCmd() tea.Cmd {
	return tea.Tick(cancelTimerDuration, func(time.Time) tea.Msg {
		return cancelTimerExpiredMsg{}
	})
}

// cancelAgent handles the cancel key press. The first press sets isCanceling to true
// and starts a timer. The second press (before the timer expires) actually
// cancels the agent.
func (m *UI) cancelAgent() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	// Gate on the memoized ready state: esc is a hot key and AgentIsReady
	// is a synchronous HTTP round-trip in client/server mode.
	if !m.agentReady {
		return nil
	}

	if m.isCanceling {
		// Second escape press — actually cancel.
		m.isCanceling = false

		// Cancel a running bang command if one is in progress.
		if m.bangCancel != nil {
			m.bangCancel()
			m.bangCancel = nil
		}

		m.com.Workspace.AgentCancel(m.session.ID)
		// Stop the spinning todo indicator and drop the memoized busy
		// state the cancel just changed; the pill re-renders now from
		// last-known state and again when the off-thread refresh (and
		// the agent's own events) land.
		m.todoIsSpinning = false
		m.invalidateBusyCaches()
		m.renderPills()
		return m.dispatchBusyRefresh()
	}

	// Queued prompts pending: esc clears the queue. Decide from the cached
	// count (event-driven) instead of a synchronous workspace probe.
	if m.promptQueue > 0 {
		m.com.Workspace.AgentClearQueue(m.session.ID)
		m.promptQueue = 0
		m.promptQueueItems = nil
		m.promptQueueCheckedAt = time.Now()
		// Bump the queue generation so a fetch started before this clear
		// cannot land and repopulate the pill we just emptied.
		m.invalidatePromptQueue()
		m.updateLayoutAndSize()
		return nil
	}

	// First escape press - set canceling state and start timer.
	m.isCanceling = true
	return cancelTimerCmd()
}

// openDialog opens a dialog by its ID.
func (m *UI) openDialog(id string) tea.Cmd {
	var cmds []tea.Cmd
	switch id {
	case dialog.SessionsID:
		if cmd := m.openSessionsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.AgentModelsID:
		if cmd := m.openModelsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.CommandsID:
		if cmd := m.openCommandsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.NotificationsID:
		if cmd := m.openNotificationsDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.FilePickerID:
		if cmd := m.openFilesDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.ThemeID:
		m.openThemeDialog()
	case dialog.ThemeNewID:
		m.openThemeNewDialog()
	case dialog.ThemeEditorID:
		m.openThemeEditorDialog("")
	case dialog.QuitID:
		if cmd := m.openQuitDialog(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.SettingsID, dialog.SettingsUsageID, dialog.SettingsTeamID:
		if cmd := m.openSettingsDialog(id); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case dialog.PeerPairID:
		m.openPeerPairDialog()
	case dialog.MobileID:
		cmds = append(cmds, m.openMobileDialog())
	case dialog.MobilePairID:
		cmds = append(cmds, m.openMobileDialogAction("link"))
	case dialog.MobileStartID:
		cmds = append(cmds, m.openMobileDialogAction("start"))
	case dialog.MobileLANID:
		cmds = append(cmds, m.openMobileDialogRequest("start", true))
	case dialog.MobileStopID:
		cmds = append(cmds, m.openMobileDialogAction("stop"))
	case dialog.NoticeHistoryID:
		cmds = append(cmds, m.openNoticeHistory())
	case dialog.UsageID:
		cmds = append(cmds, m.openUsageDialog())
	default:
		// Unknown dialog
		break
	}
	return tea.Batch(cmds...)
}

// openQuitDialog opens the quit confirmation dialog.
func (m *UI) openQuitDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.QuitID) {
		// Bring to front
		m.dialog.BringToFront(dialog.QuitID)
		return nil
	}

	quitDialog := dialog.NewQuit(m.com)
	m.dialog.OpenDialog(quitDialog)
	return nil
}

// openModelsDialog opens the Agent picker (ctrl+l).
func (m *UI) openModelsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.AgentModelsID) {
		m.dialog.BringToFront(dialog.AgentModelsID)
		return nil
	}
	return m.fetchAgentModels()
}

// agentModelChoicesMsg carries the models the agent CLIs offer and the
// named agents, for the Agent picker.
type agentModelChoicesMsg struct {
	choices  []workspace.ModelChoice
	profiles []workspace.Profile
	err      error
}

// fetchAgentModels asks the agent CLIs for their models, and reads the
// named agents, off the UI loop.
func (m *UI) fetchAgentModels() tea.Cmd {
	picker, ok := m.com.Workspace.(workspace.AgentPicker)
	if !ok {
		return util.ReportWarn("Model switching is not available here")
	}
	profiles, _ := m.com.Workspace.(workspace.Profiles)
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	return tea.Batch(
		util.ReportInfo("Asking the agents for their models…"),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			choices, err := picker.AgentModelChoices(ctx, sessionID)
			msg := agentModelChoicesMsg{choices: choices, err: err}
			if profiles != nil {
				msg.profiles = profiles.ListProfiles()
			}
			return msg
		},
	)
}

// openAgentModels opens the Agent picker once the models are in, on the
// lead the editor shows.
func (m *UI) openAgentModels(msg agentModelChoicesMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	if len(msg.choices) == 0 {
		return util.ReportWarn("No agent CLI listed any models")
	}
	m.profiles = msg.profiles
	if m.dialog.ContainsDialog(dialog.AgentModelsID) {
		m.dialog.CloseDialog(dialog.AgentModelsID)
	}
	agents := pickerAgents(msg.choices, msg.profiles, m.shownLead())
	m.dialog.OpenDialog(dialog.NewAgentPicker(m.com, agents, msg.choices))
	return nil
}

// selectAgentModel makes the pick the session's lead (SetAgentModel). With
// no session yet it waits for the first message, which starts the session
// (sendMessageInternal).
func (m *UI) selectAgentModel(msg dialog.ActionSelectAgentModel) tea.Cmd {
	m.dialog.CloseDialog(dialog.AgentModelsID)
	picker, ok := m.com.Workspace.(workspace.AgentPicker)
	if !ok {
		return nil
	}
	who := cmp.Or(msg.Agent, msg.Kind)
	if !m.hasSession() {
		m.pendingLead = &msg
		return util.ReportInfo(who + " takes the first message")
	}
	m.pendingLead = nil
	sessionID := m.session.ID
	m.leadGen++
	m.leadPicking = true
	m.setLead(sessionID, pickedLead(msg))
	return func() tea.Msg {
		if err := picker.SetAgentModel(sessionID, who, msg.Model, msg.Effort); err != nil {
			return leadPickedMsg{info: util.NewErrorMsg(err)}
		}
		label := msg.Model
		if label == "" {
			label = "its default model"
		}
		if msg.Effort != "" {
			label += " · " + msg.Effort + " effort"
		}
		return leadPickedMsg{info: util.NewInfoMsg("Talking to " + who + " · " + label)}
	}
}

// openCommandsDialog opens the commands dialog.
func (m *UI) openCommandsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.CommandsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.CommandsID)
		return nil
	}

	var sessionID string
	hasSession := m.session != nil
	if hasSession {
		sessionID = m.session.ID
	}
	hasTodos := hasSession && hasIncompleteTodos(m.session.Todos)
	hasQueue := m.promptQueue > 0

	commands, err := dialog.NewCommands(m.com, sessionID, hasSession, hasTodos, hasQueue, m.customCommands)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(commands)

	return nil
}

// openReasoningDialog opens the reasoning effort dialog.
func (m *UI) openReasoningDialog() tea.Cmd {
	// Efforts are picked per model, in the model picker.
	return m.fetchAgentModels()
}

// openNotificationsDialog opens the notification style picker dialog.
func (m *UI) openNotificationsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.NotificationsID) {
		m.dialog.BringToFront(dialog.NotificationsID)
		return nil
	}

	notificationsDialog := dialog.NewNotifications(m.com)
	m.dialog.OpenDialog(notificationsDialog)
	return nil
}

// openSessionsDialog opens the sessions dialog. If the dialog is already open,
// it brings it to the front. Otherwise, it will list all the sessions and open
// the dialog.
func (m *UI) openSessionsDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.SessionsID) {
		// Bring to front
		m.dialog.BringToFront(dialog.SessionsID)
		return nil
	}

	selectedSessionID := ""
	if m.session != nil {
		selectedSessionID = m.session.ID
	}

	dialog, err := dialog.NewSessions(m.com, selectedSessionID)
	if err != nil {
		return util.ReportError(err)
	}

	m.dialog.OpenDialog(dialog)
	return nil
}

// openFilesDialog opens the file picker dialog.
func (m *UI) openFilesDialog() tea.Cmd {
	if !m.currentModelSupportsImages() {
		return util.ReportWarn("The current model does not support image attachments")
	}
	if m.dialog.ContainsDialog(dialog.FilePickerID) {
		// Bring to front
		m.dialog.BringToFront(dialog.FilePickerID)
		return nil
	}

	filePicker, cmd := dialog.NewFilePicker(m.com)
	filePicker.SetImageCapabilities(&m.caps)
	m.dialog.OpenDialog(filePicker)

	return cmd
}

// openPermissionsDialog opens the permissions dialog for a permission request.
func (m *UI) openPermissionsDialog(perm permission.PermissionRequest) tea.Cmd {
	// Close any existing permissions dialog first.
	m.dialog.CloseDialog(dialog.PermissionsID)

	// Get diff mode from config.
	var opts []dialog.PermissionsOption
	if diffMode := m.com.Config().Options.TUI.DiffMode; diffMode != "" {
		opts = append(opts, dialog.WithDiffMode(diffMode == "split"))
	}

	permDialog := dialog.NewPermissions(m.com, perm, opts...)
	m.dialog.OpenDialogWithGrace(permDialog)
	return nil
}

// openBatchFormDialog activates a tabbed multi-question form in
// the editor area. Single questions render without tabs or confirm.
func (m *UI) openBatchFormDialog(batch question.Request) {
	// Close any existing question form first to prevent stacking.
	if qf, ok := m.activeInline.(*dialog.QuestionForm); ok && qf != nil {
		m.activeInline = nil
	}

	form := dialog.NewQuestionForm(m.com.Styles, batch)
	form.OnAnswer = func(responses []question.Answer) {
		m.com.Workspace.QuestionAnswer(responses)
	}
	form.OnCancel = func() {
		m.com.Workspace.QuestionCancel()
	}
	m.activeInline = form
	m.textarea.Blur()
	m.focus = uiFocusEditor
	m.activeInline.SetFocused(true)
	m.updateLayoutAndSize()
}

// handleQuestionNotification dismisses an open question form when
// any client resolved the pending batch. Only one question can be
// pending at a time, so any notification means the current form
// is stale regardless of BatchID.
func (m *UI) handleQuestionNotification(_ question.Notification) {
	if _, ok := m.activeInline.(*dialog.QuestionForm); ok {
		m.activeInline = nil
		m.textarea.Focus()
		m.updateLayoutAndSize()
	}
}

// editorContentWidth returns the content width available to the
// editor area for the current state. It depends only on terminal
// width and layout (not on editor height), so it can be computed
// before the editor's height is known. This is the single source
// of truth for the inline editor width used by both layout sizing
// and Height() queries.
func (m *UI) editorContentWidth() int {
	width := m.width - 2 // appRect horizontal margins
	if m.state == uiChat && !m.isCompact {
		width -= m.sidebarWidth(m.width-2) - 2 // sidebar column
	}
	return width
}

// collapsedInlineEditor returns the active inline editor when it should use
// its compact representation while chat has focus.
func (m *UI) collapsedInlineEditor() (dialog.CollapsibleInlineEditor, bool) {
	if m.focus == uiFocusEditor {
		return nil, false
	}
	collapsible, ok := m.activeInline.(dialog.CollapsibleInlineEditor)
	if !ok || !collapsible.ShouldCollapse(m.editorContentWidth(), m.height) {
		return nil, false
	}
	return collapsible, true
}

// handlePermissionNotification updates tool items when permission state changes.
func (m *UI) handlePermissionNotification(notification permission.PermissionNotification) {
	if toolItem := m.chat.MessageItem(notification.ToolCallID); toolItem != nil {
		if permItem, ok := toolItem.(chat.ToolMessageItem); ok {
			if notification.Granted {
				permItem.SetStatus(chat.ToolStatusRunning)
			} else {
				permItem.SetStatus(chat.ToolStatusAwaitingPermission)
			}
		}
	}

	// If this notification reflects a final resolution (granted or denied),
	// dismiss any open permissions dialog whose tool call ID matches. This
	// covers the case where another client resolved the request remotely.
	if !notification.Granted && !notification.Denied {
		return
	}
	if d := m.dialog.Dialog(dialog.PermissionsID); d != nil {
		if perm, ok := d.(*dialog.Permissions); ok && perm.ToolCallID() == notification.ToolCallID {
			m.dialog.CloseDialog(dialog.PermissionsID)
		}
	}
}

// handlePlanHandoff checks whether a completed run in plan mode contained the
// plan-ready sentinel marker and, if so, opens the plan handoff dialog.
func (m *UI) handlePlanHandoff(rc notify.RunComplete) tea.Cmd {
	if m.mode != uiInputModePlan {
		return nil
	}
	if rc.Error != "" || rc.Cancelled {
		return nil
	}
	if m.session == nil || rc.SessionID != m.session.ID {
		return nil
	}
	if !common.PlanReadyMarkerPresent(rc.Text) {
		slog.Debug("Plan run completed without ready marker", "session_id", rc.SessionID)
		return nil
	}
	m.setPlanReadyPending(rc.SessionID)
	if _, ok := m.activeInline.(*dialog.PlanHandoffInline); ok {
		return nil
	}
	m.openPlanHandoff()
	return nil
}

// resetPlanModeState drops any pending plan handoff and, when plan mode is
// active, switches back to code mode. Used when the UI moves to a different
// session, since plan mode is scoped to the session it was enabled in.
func (m *UI) resetPlanModeState() tea.Cmd {
	m.setPlanReadyPending("")
	if _, ok := m.activeInline.(*dialog.PlanHandoffInline); ok {
		m.activeInline = nil
		m.textarea.Focus()
	}
	if m.mode != uiInputModePlan {
		return nil
	}
	// The backend rejects agent switches while a run is active (409). When
	// one is, keep the mode as-is: the server's active agent still matches
	// what the editor shows, and the next Shift+Tab lands back in code mode
	// once the run finishes.
	if m.isAgentBusy() {
		return nil
	}
	return m.setInputMode(uiInputModeCode)
}

// setPlanReadyPending records (or clears, with an empty ID) the session that
// has an unconfirmed ready plan.
func (m *UI) setPlanReadyPending(sessionID string) {
	m.planReadySessionID = sessionID
}

// openPlanHandoff replaces the textarea with the inline "switch to code"
// prompt. Dismissing it keeps the pending plan, so the prompt can be reopened
// by pressing enter on an empty editor while still in plan mode.
func (m *UI) openPlanHandoff() {
	inline := dialog.NewPlanHandoffInline(m.com)
	inline.OnConfirm = func(yolo bool) tea.Cmd {
		if m.com.Workspace.PermissionSkipRequests() != yolo {
			m.toggleYoloMode()
		}
		m.setPlanReadyPending("")
		sessionID := m.session.ID
		cmd := m.setInputMode(uiInputModeCode)
		return func() tea.Msg {
			result := cmd()
			if switched, ok := result.(modeSwitchedMsg); ok {
				switched.continueSessionID = sessionID
				return switched
			}
			return result
		}
	}
	inline.OnRequestChanges = func(feedback string) tea.Cmd {
		return m.sendMessage(feedback)
	}
	m.activeInline = inline
	m.textarea.Blur()
	m.focus = uiFocusEditor
	m.activeInline.SetFocused(true)
	if m.status != nil {
		m.updateLayoutAndSize()
	}
}

// handleAgentNotification translates domain agent events into desktop
// notifications using the UI notification backend.
func (m *UI) handleAgentNotification(n notify.Notification) tea.Cmd {
	var cmds []tea.Cmd
	m.noticeFromAgent(n)
	switch n.Type {
	case notify.TypeAgentFinished:
		common.StopTurn()
		m.turnOutcome = tea.ProgramStateDone
		cmds = append(cmds, m.sendNotification(notification.Notification{
			Title:   "deck",
			Message: fmt.Sprintf("Your agents are done in \"%s\"", n.SessionTitle),
		}))
	case notify.TypeAgentStarted:
		m.turnOutcome = ""
	case notify.TypeAgentError:
		m.turnOutcome = tea.ProgramStateError
		// Terminal edge like TypeAgentFinished; fall through to the
		// busy/queue refresh below.
	default:
		return nil
	}
	// TypeAgentFinished / TypeAgentError are the busy→idle edge: the agent
	// clears its active request before publishing precisely so observers
	// can re-probe. Drop the memoized busy state and re-fetch it and the
	// prompt queue off-thread.
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// newSession clears the current session state and prepares for a new session.
// The actual session creation happens when the user sends their first message.
// Returns a command to reload prompt history.
func (m *UI) newSession() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	planCmd := m.resetPlanModeState()
	m.session = nil
	m.strip().scroll = map[paneID]int{}
	m.sessionFiles = nil
	m.sessionFileReads = nil
	m.setState(uiLanding, uiFocusEditor)
	m.textarea.Focus()
	m.chat.Blur()
	m.chat.ClearMessages()
	m.pillsExpanded = false
	m.pillsAutoExpanded = false
	m.promptQueue = 0
	m.promptQueueItems = nil
	m.promptQueueCheckedAt = time.Now()
	m.invalidateBusyCaches()
	m.invalidatePromptQueue()
	m.pillsView = ""
	m.historyReset()
	return tea.Batch(
		planCmd,
		m.loadPromptHistory(),
		m.reportCurrentSession(""),
	)
}

// checkBangModeAfterPaste engages bang mode when pasted text starts with
// optional whitespace followed by "!". It strips the prefix and adjusts
// the cursor, mirroring the keypress bang-mode entry logic.
func (m *UI) checkBangModeAfterPaste() {
	if m.bangMode {
		return
	}
	val := m.textarea.Value()
	trimmed := strings.TrimLeftFunc(val, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "!") {
		return
	}
	m.bangMode = true
	m.bangWasEmpty = true
	stripped := trimmed[1:]
	m.textarea.SetValue(stripped)
	col := m.textarea.Column()
	m.textarea.SetCursorColumn(max(0, col-(len(val)-len(stripped))))
	m.setEditorPrompt(m.yoloModeCached())
}

// handlePasteMsg handles a paste message.
func (m *UI) handlePasteMsg(msg tea.PasteMsg) tea.Cmd {
	// Normalize \r\n before the textarea sanitizer sees it.
	msg.Content = strings.ReplaceAll(msg.Content, "\r\n", "\n")

	if m.dialog.HasDialogs() {
		return m.handleDialogMsg(msg)
	}

	if m.focus != uiFocusEditor {
		return nil
	}

	if hasPasteExceededThreshold(msg) {
		return func() tea.Msg {
			content := []byte(msg.Content)
			if int64(len(content)) > common.MaxAttachmentSize {
				return util.ReportWarn("Paste is too big (>5mb)")
			}
			name := fmt.Sprintf("paste_%d.txt", m.pasteIdx())
			mimeBufferSize := min(512, len(content))
			mimeType := http.DetectContentType(content[:mimeBufferSize])
			return message.Attachment{
				FileName: name,
				FilePath: name,
				MimeType: mimeType,
				Content:  content,
			}
		}
	}

	// Attempt to parse pasted content as file paths. If possible to parse,
	// all files exist and are valid, add as attachments.
	// Otherwise, paste as text.
	paths := fsext.ParsePastedFiles(msg.Content)
	allExistsAndValid := func() bool {
		if len(paths) == 0 {
			return false
		}
		for _, path := range paths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return false
			}
			if !common.IsImagePath(path) && !common.IsVideoPath(path) {
				return false
			}
		}
		return true
	}
	if !allExistsAndValid() {
		prevHeight := m.textarea.Height()
		cmd := m.updateTextareaWithPrevHeight(msg, prevHeight)
		m.checkBangModeAfterPaste()
		return cmd
	}
	if slices.ContainsFunc(paths, common.IsImagePath) && !m.currentModelSupportsImages() {
		return util.ReportWarn("The current model does not support image attachments")
	}

	var cmds []tea.Cmd
	for _, path := range paths {
		cmds = append(cmds, m.handleFilePathPaste(path))
	}
	return tea.Batch(cmds...)
}

func hasPasteExceededThreshold(msg tea.PasteMsg) bool {
	var (
		lineCount = 0
		colCount  = 0
	)
	for line := range strings.SplitSeq(msg.Content, "\n") {
		lineCount++
		colCount = max(colCount, len(line))

		if lineCount > pasteLinesThreshold || colCount > pasteColsThreshold {
			return true
		}
	}
	return false
}

// handleFilePathPaste handles a pasted file path.
func (m *UI) handleFilePathPaste(path string) tea.Cmd {
	if common.IsVideoPath(path) {
		return videoAttachment(path, m.shownLead())
	}
	return func() tea.Msg {
		fileInfo, err := os.Stat(path)
		if err != nil {
			return util.ReportError(err)
		}
		if fileInfo.IsDir() {
			return util.ReportWarn("Cannot attach a directory")
		}
		if fileInfo.Size() > common.MaxAttachmentSize {
			return util.ReportWarn("File is too big (>5mb)")
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return util.ReportError(err)
		}

		mimeBufferSize := min(512, len(content))
		mimeType := http.DetectContentType(content[:mimeBufferSize])
		fileName := filepath.Base(path)
		return message.Attachment{
			FilePath: path,
			FileName: fileName,
			MimeType: mimeType,
			Content:  content,
		}
	}
}

// videoAttachment attaches the video at path: checked by its bytes, at
// most media.MaxVideoBytes, and read by the engine from where it is. When
// the lead's model cannot watch video, it goes as still frames if the
// engine has ffmpeg; otherwise it is refused, saying why.
func videoAttachment(path string, lead workspace.Lead) tea.Cmd {
	return func() tea.Msg {
		mime, err := media.Check(path, media.MaxVideoBytes)
		if err != nil {
			return util.NewWarnMsg(err.Error())
		}
		att := message.Attachment{FilePath: path, FileName: filepath.Base(path), MimeType: mime}
		if !lead.Video {
			who := cmp.Or(lead.Slug, lead.Model, lead.Kind, "This agent")
			if media.FFmpeg() == "" {
				return util.NewWarnMsg(who + " can't watch video, and sending it as frames needs ffmpeg")
			}
			att.Frames = true
		}
		return att
	}
}

// pasteTextFromClipboard reads text from the system clipboard and returns a
// tea.PasteMsg so it flows through the same paste logic as bracketed paste.
func (m *UI) pasteTextFromClipboard() tea.Msg {
	textData, err := clipboard.Read(clipboard.FormatText)
	if err != nil || len(textData) == 0 {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  "Clipboard is empty or does not contain text",
		}
	}
	return tea.PasteMsg{Content: string(textData)}
}

// pasteImageFromClipboard reads image data from the system clipboard and
// creates an attachment. If no image data is found, it falls back to
// interpreting clipboard text as a file path.
func (m *UI) pasteImageFromClipboard() tea.Msg {
	if !m.currentModelSupportsImages() {
		return util.NewWarnMsg("The current model does not support image attachments")
	}
	imageData, err := clipboard.Read(clipboard.FormatImage)
	if int64(len(imageData)) > common.MaxAttachmentSize {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  "File too large, max 5MB",
		}
	}
	name := fmt.Sprintf("paste_%d.png", m.pasteIdx())
	if err == nil {
		return message.Attachment{
			FilePath: name,
			FileName: name,
			MimeType: mimeOf(imageData),
			Content:  imageData,
		}
	}

	textData, textErr := clipboard.Read(clipboard.FormatText)
	if textErr != nil || len(textData) == 0 {
		return nil // Clipboard is empty or does not contain an image
	}

	path := strings.TrimSpace(string(textData))
	path = strings.ReplaceAll(path, "\\ ", " ")
	if _, statErr := os.Stat(path); statErr != nil {
		return nil // Clipboard does not contain an image or valid file path
	}

	if !common.IsImagePath(path) {
		return util.NewInfoMsg("File type is not a supported image format")
	}

	fileInfo, statErr := os.Stat(path)
	if statErr != nil {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  fmt.Sprintf("Unable to read file: %v", statErr),
		}
	}
	if fileInfo.Size() > common.MaxAttachmentSize {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  "File too large, max 5MB",
		}
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		return util.InfoMsg{
			Type: util.InfoTypeError,
			Msg:  fmt.Sprintf("Unable to read file: %v", readErr),
		}
	}

	return message.Attachment{
		FilePath: path,
		FileName: filepath.Base(path),
		MimeType: mimeOf(content),
		Content:  content,
	}
}

var pasteRE = regexp.MustCompile(`paste_(\d+).txt`)

func (m *UI) pasteIdx() int {
	result := 0
	for _, at := range m.attachments.List() {
		found := pasteRE.FindStringSubmatch(at.FileName)
		if len(found) == 0 {
			continue
		}
		idx, err := strconv.Atoi(found[1])
		if err == nil {
			result = max(result, idx)
		}
	}
	return result + 1
}

// drawSessionDetails draws the session details in compact mode.
func (m *UI) drawSessionDetails(scr uv.Screen, area uv.Rectangle) {
	if m.session == nil {
		return
	}

	s := m.com.Styles

	width := area.Dx() - s.CompactDetails.View.GetHorizontalFrameSize()
	height := area.Dy() - s.CompactDetails.View.GetVerticalFrameSize()

	title := s.CompactDetails.Title.Width(width).MaxHeight(2).Render(m.session.Title)
	blocks := []string{
		title,
		"",
		m.modelInfo(width),
		"",
	}

	detailsHeader := lipgloss.JoinVertical(
		lipgloss.Left,
		blocks...,
	)

	version := s.CompactDetails.Version.Width(width).AlignHorizontal(lipgloss.Right).Render(version.Version)

	remainingHeight := height - lipgloss.Height(detailsHeader) - lipgloss.Height(version)

	const maxSectionWidth = 50
	sectionWidth := max(1, min(maxSectionWidth, width/4-2)) // account for spacing between sections
	maxItemsPerSection := remainingHeight - 3               // Account for section title and spacing

	skillsSection := m.skillsInfo(sectionWidth, maxItemsPerSection, false)
	filesSection := m.filesInfo(m.com.Workspace.WorkingDir(), sectionWidth, maxItemsPerSection, false)
	sections := lipgloss.JoinHorizontal(lipgloss.Top, filesSection, " ", skillsSection)
	uv.NewStyledString(
		s.CompactDetails.View.
			Width(area.Dx()).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Left,
					detailsHeader,
					sections,
					version,
				),
			),
	).Draw(scr, area)
}

func (m *UI) copyChatHighlight() tea.Cmd {
	text := m.chat.HighlightContent()
	return common.CopyToClipboardWithCallback(
		text,
		"Selected text copied to clipboard",
		func() tea.Msg {
			m.chat.ClearMouse()
			return nil
		},
	)
}

// renderLogo renders the Deck logo with the given styles and dimensions.
func renderLogo(t *styles.Styles, compact bool, width int) string {
	return logo.Render(t.Logo.GradCanvas, version.Version, compact, logo.Opts{
		TaglineColor: t.Logo.TaglineColor,
		VersionColor: t.Logo.VersionColor,
		Width:        width,
		Dark:         t.IsDark,
		Mark:         logo.PaletteFor(t),
	})
}
