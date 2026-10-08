package codex

import "encoding/json"

// Hand-written subset of the codex app-server protocol (codex-cli 0.160.1).
// Reference schemas live in ./schema. Field names follow the wire (camelCase).

// Client -> server request methods used by this adapter.
const (
	methodInitialize    = "initialize"
	methodThreadStart   = "thread/start"
	methodThreadResume  = "thread/resume"
	methodTurnStart     = "turn/start"
	methodTurnInterrupt = "turn/interrupt"
	methodTurnSteer     = "turn/steer"
	methodModelList     = "model/list"

	notifyInitialized = "initialized"
)

// Server -> client notifications handled by translate.go.
const (
	noteTurnStarted      = "turn/started"
	noteTurnCompleted    = "turn/completed"
	noteItemStarted      = "item/started"
	noteItemCompleted    = "item/completed"
	noteAgentDelta       = "item/agentMessage/delta"
	noteReasoningText    = "item/reasoning/textDelta"
	noteReasoningSummary = "item/reasoning/summaryTextDelta"
	noteCommandOutput    = "item/commandExecution/outputDelta"
	noteFilePatchUpdated = "item/fileChange/patchUpdated"
	noteTokenUsage       = "thread/tokenUsage/updated"
	noteError            = "error"
)

// Server -> client requests.
const (
	reqCommandApproval     = "item/commandExecution/requestApproval"
	reqFileChangeApproval  = "item/fileChange/requestApproval"
	reqPermissionsApproval = "item/permissions/requestApproval"
	reqUserInput           = "item/tool/requestUserInput"
	reqElicitation         = "mcpServer/elicitation/request"
)

// Approval decision values (CommandExecutionApprovalDecision / FileChangeApprovalDecision).
const (
	decisionAccept           = "accept"
	decisionAcceptForSession = "acceptForSession"
	decisionDecline          = "decline"
	decisionCancel           = "cancel" // declines and interrupts the turn
)

// Defaults for the thread's approval/sandbox policy.
const (
	DefaultApprovalPolicy = "on-request"      // AskForApproval
	DefaultSandbox        = "workspace-write" // SandboxMode
)

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type initializeParams struct {
	ClientInfo clientInfo `json:"clientInfo"`
}

type threadStartParams struct {
	Cwd            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	// Config is per-thread config overrides (same dotted keys as `codex -c`).
	Config map[string]any `json:"config,omitempty"`
	// DeveloperInstructions are extra developer-role instructions for the thread.
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
}

type threadResumeParams struct {
	ThreadID       string `json:"threadId"`
	Cwd            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	// Config is per-thread config overrides (same dotted keys as `codex -c`).
	Config map[string]any `json:"config,omitempty"`
	// DeveloperInstructions are extra developer-role instructions for the thread.
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
}

// threadResponse is the result of thread/start and thread/resume.
type threadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// userInput is one UserInput item: {"type":"text","text":...,"text_elements":[]}
// or {"type":"localImage","path":<absolute file path>}.
type userInput struct {
	Type         string // "text" | "localImage"
	Text         string
	TextElements []any
	Path         string // localImage
}

func (u userInput) MarshalJSON() ([]byte, error) {
	if u.Type == "localImage" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}{u.Type, u.Path})
	}
	te := u.TextElements
	if te == nil {
		te = []any{}
	}
	return json.Marshal(struct {
		Type         string `json:"type"`
		Text         string `json:"text"`
		TextElements []any  `json:"text_elements"`
	}{u.Type, u.Text, te})
}

// turnSteerParams is TurnSteerParams: add input to the running turn.
// ExpectedTurnId must match the active turn or the request fails.
type turnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []userInput `json:"input"`
}

// sandboxPolicy is the turn-level SandboxPolicy (a tagged union; only the
// tag is needed for the three modes Deck uses): readOnly | workspaceWrite |
// dangerFullAccess.
type sandboxPolicy struct {
	Type string `json:"type"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	// Per-turn overrides; each applies "for this turn and subsequent turns".
	// Omitted when unchanged.
	ApprovalPolicy string         `json:"approvalPolicy,omitempty"`
	SandboxPolicy  *sandboxPolicy `json:"sandboxPolicy,omitempty"`
	Model          string         `json:"model,omitempty"`
}

type modelListParams struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// modelListResponse is ModelListResponse (subset of Model).
type modelListResponse struct {
	Data []struct {
		ID          string `json:"id"`
		Model       string `json:"model"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
		Hidden      bool   `json:"hidden"`
		IsDefault   bool   `json:"isDefault"`
	} `json:"data"`
	NextCursor string `json:"nextCursor"`
}

type turnStartResponse struct {
	Turn turn `json:"turn"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails,omitempty"`
}

type turn struct {
	ID     string     `json:"id"`
	Status string     `json:"status"` // completed | interrupted | failed | inProgress
	Error  *turnError `json:"error,omitempty"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     turn   `json:"turn"`
}

type patchKind struct {
	Type     string `json:"type"` // add | delete | update
	MovePath string `json:"move_path,omitempty"`
}

type fileUpdateChange struct {
	Path string    `json:"path"`
	Kind patchKind `json:"kind"`
	Diff string    `json:"diff"`
}

// threadItem is the union of ThreadItem variants; only the fields Deck reads.
type threadItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text,omitempty"` // agentMessage, plan
	// content is [UserInput] for userMessage and [string] for reasoning.
	Content          json.RawMessage    `json:"content,omitempty"`
	Summary          []string           `json:"summary,omitempty"` // reasoning
	Command          string             `json:"command,omitempty"`
	Cwd              string             `json:"cwd,omitempty"`
	Status           string             `json:"status,omitempty"` // inProgress | completed | failed | declined
	AggregatedOutput *string            `json:"aggregatedOutput,omitempty"`
	ExitCode         *int               `json:"exitCode,omitempty"`
	Changes          []fileUpdateChange `json:"changes,omitempty"`
	CommandActions   []commandAction    `json:"commandActions,omitempty"`

	// mcpToolCall / dynamicToolCall / webSearch
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Query     string          `json:"query,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
}

// commandAction is the parsed form of a command (CommandAction); Command is the
// inner command with shell wrappers removed.
type commandAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type itemNotification struct {
	Item     threadItem `json:"item"`
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
}

type deltaNotification struct {
	Delta  string `json:"delta"`
	ItemID string `json:"itemId"`
}

type patchUpdatedNotification struct {
	Changes []fileUpdateChange `json:"changes"`
	ItemID  string             `json:"itemId"`
}

type tokenBreakdown struct {
	CachedInputTokens     int `json:"cachedInputTokens"`
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	ReasoningOutputTokens int `json:"reasoningOutputTokens"`
	TotalTokens           int `json:"totalTokens"`
}

type tokenUsageNotification struct {
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Last  tokenBreakdown `json:"last"`
		Total tokenBreakdown `json:"total"`
		// ModelContextWindow is null when the model's window is unknown.
		ModelContextWindow *int `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

type errorNotification struct {
	Error     turnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
}

// commandApprovalParams is CommandExecutionRequestApprovalParams (subset).
type commandApprovalParams struct {
	ItemID     string `json:"itemId"`
	ApprovalID string `json:"approvalId,omitempty"`
	Command    string `json:"command,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Kind       string `json:"kind,omitempty"` // command | writeStdin

	CommandActions []commandAction `json:"commandActions,omitempty"`
}

// fileChangeApprovalParams is FileChangeRequestApprovalParams (subset).
type fileChangeApprovalParams struct {
	ItemID    string `json:"itemId"`
	Reason    string `json:"reason,omitempty"`
	GrantRoot string `json:"grantRoot,omitempty"`
}

type permissionsApprovalParams struct {
	ItemID      string          `json:"itemId"`
	Cwd         string          `json:"cwd,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Permissions json.RawMessage `json:"permissions"`
}

type decisionResponse struct {
	Decision string `json:"decision"`
}

type permissionsResponse struct {
	Permissions json.RawMessage `json:"permissions"`
	Scope       string          `json:"scope"` // turn | session
}
