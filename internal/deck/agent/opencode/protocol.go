package opencode

import "encoding/json"

// The subset of the Agent Client Protocol (protocol version 1) Deck uses.

const (
	methodInitialize      = "initialize"
	methodSessionNew      = "session/new"
	methodSessionResume   = "session/resume"
	methodSessionLoad     = "session/load"
	methodSessionDelete   = "session/delete"
	methodSessionPrompt   = "session/prompt"
	methodSessionSetOpt   = "session/set_config_option"
	notifySessionCancel   = "session/cancel"
	notifySessionUpdate   = "session/update"
	reqRequestPermission  = "session/request_permission"
	optModel              = "model"
	optEffort             = "effort"
	optMode               = "mode"
	effortDefault         = "default"
	modeBuild, modePlan   = "build", "plan"
	stopCancelled         = "cancelled"
	updAgentMessage       = "agent_message_chunk"
	updAgentThought       = "agent_thought_chunk"
	updToolCall           = "tool_call"
	updToolCallUpdate     = "tool_call_update"
	updUsage              = "usage_update"
	updConfigOptionUpdate = "config_option_update"
)

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type fsCaps struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type clientCaps struct {
	FS       fsCaps `json:"fs"`
	Terminal bool   `json:"terminal"`
}

type initializeParams struct {
	ProtocolVersion    int        `json:"protocolVersion"`
	ClientCapabilities clientCaps `json:"clientCapabilities"`
	ClientInfo         clientInfo `json:"clientInfo"`
}

type initializeResult struct {
	ProtocolVersion   int `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession        bool `json:"loadSession"`
		PromptCapabilities struct {
			Image bool `json:"image"`
		} `json:"promptCapabilities"`
		SessionCapabilities struct {
			Resume *struct{} `json:"resume"`
			Delete *struct{} `json:"delete"`
		} `json:"sessionCapabilities"`
	} `json:"agentCapabilities"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// mcpServer is a stdio MCP server for OpenCode to start in the session.
type mcpServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []envVar `json:"env"`
}

type sessionParams struct {
	SessionID  string      `json:"sessionId,omitempty"`
	Cwd        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type configOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value       string `json:"value"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"options"`
}

type sessionResult struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions"`
}

type setOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type setOptionResult struct {
	ConfigOptions []configOption `json:"configOptions"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		InputTokens      int `json:"inputTokens"`
		OutputTokens     int `json:"outputTokens"`
		CachedReadTokens int `json:"cachedReadTokens"`
	} `json:"usage"`
}

type cancelParams struct {
	SessionID string `json:"sessionId"`
}

// toolContent is one item of a tool call's content: text output, or a diff.
type toolContent struct {
	Type    string `json:"type"` // "content", "diff"
	Content *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Path    string  `json:"path"`
	OldText *string `json:"oldText"`
	NewText string  `json:"newText"`
}

type location struct {
	Path string `json:"path"`
}

// toolCall is a tool_call / tool_call_update, or the toolCall of a permission
// request. Fields left out of an update are unchanged.
type toolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`   // read, edit, delete, move, search, execute, think, fetch, other
	Status     string          `json:"status"` // pending, in_progress, completed, failed
	Locations  []location      `json:"locations"`
	RawInput   json.RawMessage `json:"rawInput"`
	Content    []toolContent   `json:"content"`
}

// update is the "update" of a session/update notification.
type update struct {
	SessionUpdate string `json:"sessionUpdate"`
	MessageID     string `json:"messageId"`
	Content       *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	toolCall
	Used int `json:"used"`
	Size int `json:"size"`
	Cost *struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"cost"`
	ConfigOptions []configOption `json:"configOptions"`
}

// UnmarshalJSON keeps "content" for both shapes: a message chunk's single
// content block, and a tool call's list.
func (u *update) UnmarshalJSON(b []byte) error {
	type plain update
	var p struct {
		plain
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*u = update(p.plain)
	u.Content, u.toolCall.Content = nil, nil
	if len(p.Content) > 0 && p.Content[0] == '[' {
		_ = json.Unmarshal(p.Content, &u.toolCall.Content)
	} else if len(p.Content) > 0 && p.Content[0] == '{' {
		_ = json.Unmarshal(p.Content, &u.Content)
	}
	return nil
}

type updateParams struct {
	SessionID string `json:"sessionId"`
	Update    update `json:"update"`
}

// decodeUpdate is the client's decode hook: a session/update's params (most
// of the stream) are decoded once, straight from the line, into Value, and
// never copied as raw params first.
func decodeUpdate(method string, params []byte) any {
	if method != notifySessionUpdate {
		return nil
	}
	p := new(updateParams)
	if json.Unmarshal(params, p) != nil {
		return nil
	}
	return p
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
	Name     string `json:"name"`
}

type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  toolCall           `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"` // "selected" or "cancelled"
	OptionID string `json:"optionId,omitempty"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}
