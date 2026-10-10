// Package toolspec holds the inputs, permission requests and results of
// the coding tools agents call, as the chat and the permission dialog read them.
package toolspec

import (
	"github.com/dev-dami/deck/internal/session"
)

// AgenticFetchParams defines the parameters for the agentic fetch tool.
type AgenticFetchParams struct {
	URL    string `json:"url,omitempty" description:"The URL to fetch content from (optional - if not provided, the agent will search the web)"`
	Prompt string `json:"prompt" description:"The prompt describing what information to find or extract"`
}

// AgenticFetchPermissionsParams defines the permission parameters for the agentic fetch tool.
type AgenticFetchPermissionsParams struct {
	URL    string `json:"url,omitempty"`
	Prompt string `json:"prompt"`
}

// AgenticFetchToolName is the name of the agentic fetch tool.
const AgenticFetchToolName = "agentic_fetch"

const BashNoOutput = "no output"

type BashParams struct {
	Description         string `json:"description" description:"A brief description of what the command does, try to keep it under 30 characters or so"`
	Command             string `json:"command" description:"The command to execute"`
	WorkingDir          string `json:"working_dir,omitempty" description:"The working directory to execute the command in (defaults to current directory)"`
	RunInBackground     bool   `json:"run_in_background,omitempty" description:"Set to true (boolean) to run this command in the background. Use job_output to read the output later."`
	AutoBackgroundAfter int    `json:"auto_background_after,omitempty" description:"Seconds to wait before automatically moving the command to a background job (default: 60)"`
}

type BashPermissionsParams struct {
	Description         string `json:"description"`
	Command             string `json:"command"`
	WorkingDir          string `json:"working_dir"`
	RunInBackground     bool   `json:"run_in_background"`
	AutoBackgroundAfter int    `json:"auto_background_after"`
}

type BashResponseMetadata struct {
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
	Output           string `json:"output"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
}

const BashToolName = "bash"

type CallHierarchyParams struct {
	Symbol    string `json:"symbol" description:"The symbol name to show call hierarchy for"`
	Direction string `json:"direction" description:"Either 'incoming' (who calls this) or 'outgoing' (what does this call)"`
	Path      string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

const CallHierarchyToolName = "lsp_call_hierarchy"

// ChannelContextKey is the key for the channel that originated the turn.
const // ChannelContextKey is the key for the channel that originated the turn.
ChannelContextKey channelContextKey = "channel"

type DefinitionParams struct {
	Symbol string `json:"symbol" description:"The symbol name to find the definition of"`
	Path   string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

// DefinitionResponseMetadata carries structured data for the renderer.
type DefinitionResponseMetadata struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Content  string `json:"content"`
}

const DefinitionToolName = "lsp_definition"

type DiagnosticsParams struct {
	FilePath string `json:"file_path,omitempty" description:"The path to the file to get diagnostics for (leave empty for project diagnostics)"`
}

const DiagnosticsToolName = "lsp_diagnostics"

type DownloadParams struct {
	URL      string `json:"url" description:"The URL to download from"`
	FilePath string `json:"file_path" description:"The local file path where the downloaded content should be saved"`
	Timeout  int    `json:"timeout,omitempty" description:"Optional timeout in seconds (max 600)"`
}

type DownloadPermissionsParams struct {
	URL      string `json:"url"`
	FilePath string `json:"file_path"`
	Timeout  int    `json:"timeout,omitempty"`
}

const DownloadToolName = "download"

type EditParams struct {
	FilePath   string `json:"file_path" description:"The absolute path to the file to modify"`
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

type EditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type EditResponseMetadata struct {
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

const EditToolName = "edit"

type FailedEdit struct {
	Index int                `json:"index"`
	Error string             `json:"error"`
	Edit  MultiEditOperation `json:"edit"`
}

// FetchParams defines the parameters for the simple fetch tool.
type FetchParams struct {
	URL     string `json:"url" description:"The URL to fetch content from"`
	Format  string `json:"format" description:"The format to return the content in (text, markdown, or html)"`
	Timeout int    `json:"timeout,omitempty" description:"Optional timeout in seconds (max 120)"`
}

// FetchPermissionsParams defines the permission parameters for the simple fetch tool.
type FetchPermissionsParams struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	Timeout int    `json:"timeout,omitempty"`
}

const FetchToolName = "fetch"

type GlobParams struct {
	Pattern string `json:"pattern" description:"The glob pattern to match files against"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

const GlobToolName = "glob"

type GrepParams struct {
	Pattern     string `json:"pattern" description:"The regex pattern to search for in file contents"`
	Path        string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
	Include     string `json:"include,omitempty" description:"File pattern to include in the search (e.g. \"*.js\", \"*.{ts,tsx}\")"`
	LiteralText bool   `json:"literal_text,omitempty" description:"If true, the pattern will be treated as literal text with special regex characters escaped. Default is false."`
}

const GrepToolName = "grep"

type JobKillParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to terminate"`
}

type JobKillResponseMetadata struct {
	ShellID     string `json:"shell_id"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

const JobKillToolName = "job_kill"

type JobOutputParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to retrieve output from"`
	Wait    bool   `json:"wait" description:"If true, block until the background shell completes before returning output"`
}

type JobOutputResponseMetadata struct {
	ShellID          string `json:"shell_id"`
	Command          string `json:"command"`
	Description      string `json:"description"`
	Done             bool   `json:"done"`
	WorkingDirectory string `json:"working_directory"`
}

const JobOutputToolName = "job_output"

type LSPRestartParams struct {
	// Name is the optional name of a specific LSP client to restart.
	// If empty, all LSP clients will be restarted.
	Name string `json:"name,omitempty"`
}

const LSPRestartToolName = "lsp_restart"

type LSParams struct {
	Path   string   `json:"path,omitempty" description:"The path to the directory to list (defaults to current working directory)"`
	Ignore []string `json:"ignore,omitempty" description:"List of glob patterns to ignore"`
	Depth  int      `json:"depth,omitempty" description:"The maximum depth to traverse"`
}

type LSPermissionsParams struct {
	Path   string   `json:"path"`
	Ignore []string `json:"ignore"`
	Depth  int      `json:"depth"`
}

const LSToolName = "ls"

// LargeContentThreshold is the size threshold for saving content to a file.
const LargeContentThreshold = 50000 // 50KB

// MessageIDContextKey is the key for the message ID in the context.
const // MessageIDContextKey is the key for the message ID in the context.
MessageIDContextKey messageIDContextKey = "message_id"

// ModelNameContextKey is the key for the model name in the context.
const // ModelNameContextKey is the key for the model name in the context.
ModelNameContextKey modelNameKey = "model_name"

type MultiEditOperation struct {
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)."`
}

type MultiEditParams struct {
	FilePath string               `json:"file_path" description:"The absolute path to the file to modify"`
	Edits    []MultiEditOperation `json:"edits" description:"Array of edit operations to perform sequentially on the file"`
}

type MultiEditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type MultiEditResponseMetadata struct {
	Additions    int          `json:"additions"`
	Removals     int          `json:"removals"`
	OldContent   string       `json:"old_content,omitempty"`
	NewContent   string       `json:"new_content,omitempty"`
	EditsApplied int          `json:"edits_applied"`
	EditsFailed  []FailedEdit `json:"edits_failed,omitempty"`
}

const MultiEditToolName = "multiedit"

// QuestionChoice represents a selectable option.
type QuestionChoice struct {
	ID          string `json:"id" description:"Unique identifier for this choice"`
	Label       string `json:"label" description:"Display text for this choice"`
	Description string `json:"description,omitempty" description:"Optional description for this choice"`
}

// QuestionItem is a single question from the tool input.
type QuestionItem struct {
	Label       string           `json:"label,omitempty" description:"Short tab header label (3 words max)."`
	Type        string           `json:"type" description:"The type of question: yes_no, single_choice, multi_choice, or free_text"`
	Question    string           `json:"question" description:"The question text"`
	Description string           `json:"description" description:"Required markdown description shown below the question"`
	Choices     []QuestionChoice `json:"choices,omitempty" description:"List of choices"`
	Options     []QuestionChoice `json:"options,omitempty"` // alias for Choices
}

// QuestionParams defines the parameters for the question tool.
type QuestionParams struct {
	Questions          []QuestionItem `json:"questions" description:"List of questions to present. Single item = no tabs, multiple = tabbed form."`
	ConfirmTitle       string         `json:"confirm_title,omitempty" description:"Title for the confirmation tab. Required for multi-question batches."`
	ConfirmDescription string         `json:"confirm_description,omitempty" description:"Description for the confirmation tab. Required for multi-question batches."`
}

const QuestionToolName = "question"

type ReferencesParams struct {
	Symbol string `json:"symbol" description:"The symbol name to search for (e.g., function name, variable name, type name)"`
	Path   string `json:"path,omitempty" description:"The directory to search in. Use a directory/file to narrow down the symbol search. Defaults to the current working directory."`
}

const ReferencesToolName = "lsp_references"

type RenameParams struct {
	Symbol  string `json:"symbol" description:"The symbol name to rename"`
	NewName string `json:"new_name" description:"The new name for the symbol"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

const RenameToolName = "lsp_rename"

type ReplaceSymbolParams struct {
	Symbol      string `json:"symbol" description:"The symbol name to target (e.g., function name, method name, type name)"`
	FilePath    string `json:"file_path" description:"The path to the file containing the symbol"`
	Replacement string `json:"replacement,omitempty" description:"The replacement text. Required for 'replace' action. For 'add_before'/'add_after', the text to insert. Ignored for 'delete'."`
	Action      string `json:"action,omitempty" description:"Operation to perform: 'replace' (default, replace entire symbol), 'add_before' (insert before symbol), 'add_after' (insert after symbol), 'delete' (remove symbol entirely)"`
}

// ReplaceSymbolPermissionsParams carries diff data for the permission dialog.
type ReplaceSymbolPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

// ReplaceSymbolResponseMetadata carries diff data for the renderer.
type ReplaceSymbolResponseMetadata struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
	Action     string `json:"action"`
}

const ReplaceSymbolToolName = "lsp_replace_symbol"

// SessionIDContextKey is the key for the session ID in the context.
const // SessionIDContextKey is the key for the session ID in the context.
SessionIDContextKey sessionIDContextKey = "session_id"

type SourcegraphParams struct {
	Query         string `json:"query" description:"The Sourcegraph search query"`
	Count         int    `json:"count,omitempty" description:"Optional number of results to return (default: 10, max: 20)"`
	ContextWindow int    `json:"context_window,omitempty" description:"The context around the match to return (default: 10 lines)"`
	Timeout       int    `json:"timeout,omitempty" description:"Optional timeout in seconds (max 120)"`
}

const SourcegraphToolName = "sourcegraph"

// SupportsImagesContextKey is the key for the model's image support capability.
const // SupportsImagesContextKey is the key for the model's image support capability.
SupportsImagesContextKey supportsImagesKey = "supports_images"

type SymbolsParams struct {
	FilePath string `json:"file_path" description:"The path to the file to get symbols for"`
}

const SymbolsToolName = "lsp_symbols"

type TodoItem struct {
	Content    string `json:"content" description:"What needs to be done (imperative form)"`
	Status     string `json:"status" description:"Task status: pending, in_progress, or completed"`
	ActiveForm string `json:"active_form" description:"Present continuous form (e.g., 'Running tests')"`
}

type TodosParams struct {
	Todos []TodoItem `json:"todos" description:"The updated todo list"`
}

type TodosResponseMetadata struct {
	IsNew         bool           `json:"is_new"`
	Todos         []session.Todo `json:"todos"`
	JustCompleted []string       `json:"just_completed,omitempty"`
	JustStarted   string         `json:"just_started,omitempty"`
	Completed     int            `json:"completed"`
	Total         int            `json:"total"`
}

const TodosToolName = "todos"

type ViewParams struct {
	FilePath string `json:"file_path" description:"The path to the file to read"`
	Offset   int    `json:"offset,omitempty" description:"The line number to start reading from (0-based)"`
	Limit    int    `json:"limit,omitempty" description:"The number of lines to read (defaults to 200)"`
}

type ViewPermissionsParams struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

const ViewResourceSkill ViewResourceType = "skill"

type ViewResourceType string

type ViewResponseMetadata struct {
	FilePath            string           `json:"file_path"`
	Content             string           `json:"content"`
	ResourceType        ViewResourceType `json:"resource_type,omitempty"`
	ResourceName        string           `json:"resource_name,omitempty"`
	ResourceDescription string           `json:"resource_description,omitempty"`
}

const ViewToolName = "view"

// WebFetchParams defines the parameters for the web_fetch tool.
type WebFetchParams struct {
	URL string `json:"url" description:"The URL to fetch content from"`
}

// WebFetchToolName is the name of the web_fetch tool.
const WebFetchToolName = "web_fetch"

// WebSearchParams defines the parameters for the web_search tool.
type WebSearchParams struct {
	Query      string `json:"query" description:"The search query to find information on the web"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of results to return (default: 10, max: 20)"`
}

// WebSearchToolName is the name of the web_search tool for sub-agents.
const WebSearchToolName = "web_search"

type WriteParams struct {
	FilePath string `json:"file_path" description:"The path to the file to write"`
	Content  string `json:"content" description:"The content to write to the file"`
}

type WritePermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type WriteResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

const WriteToolName = "write"

type channelContextKey string

type messageIDContextKey string

type modelNameKey string

type sessionIDContextKey string

type supportsImagesKey string

// AgentParams is the input of the tool that hands a task to a sub-agent.
type AgentParams struct {
	Prompt string `json:"prompt" description:"The task for the agent to perform"`
}

// AgentToolName is the sub-agent tool.
const AgentToolName = "agent"
