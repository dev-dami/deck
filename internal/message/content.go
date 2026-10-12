package message

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
)

type MessageRole string

const (
	Assistant MessageRole = "assistant"
	User      MessageRole = "user"
	System    MessageRole = "system"
	Tool      MessageRole = "tool"
)

// mediaLoadFailedPlaceholder is the text substituted for image data that
// cannot be decoded during session replay.
const mediaLoadFailedPlaceholder = "[Image data could not be loaded]"

type FinishReason string

const (
	FinishReasonEndTurn   FinishReason = "end_turn"
	FinishReasonMaxTokens FinishReason = "max_tokens"
	FinishReasonToolUse   FinishReason = "tool_use"
	FinishReasonCanceled  FinishReason = "canceled"
	FinishReasonError     FinishReason = "error"
	// FinishReasonContentFilter is a provider safety/refusal stop
	// (Anthropic stop_reason=refusal, OpenAI content_filter, etc.).
	// The TUI renders this as a REFUSED banner rather than a silent
	// empty turn.
	FinishReasonContentFilter FinishReason = "content_filter"

	// Should never happen
	FinishReasonUnknown FinishReason = "unknown"
)

type ContentPart interface {
	isPart()
}

type ReasoningContent struct {
	Thinking         string `json:"thinking"`
	Signature        string `json:"signature"`
	ThoughtSignature string `json:"thought_signature"` // Used for google
	ToolID           string `json:"tool_id"`           // Used for openrouter google models
	StartedAt        int64  `json:"started_at,omitempty"`
	FinishedAt       int64  `json:"finished_at,omitempty"`
}

func (tc ReasoningContent) String() string {
	return tc.Thinking
}
func (ReasoningContent) isPart() {}

type TextContent struct {
	Text string `json:"text"`
	// Hidden marks generated user continuations that remain in model history.
	Hidden bool `json:"hidden,omitempty"`
}

func (tc TextContent) String() string {
	return tc.Text
}

func (TextContent) isPart() {}

type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (iuc ImageURLContent) String() string {
	return iuc.URL
}

func (ImageURLContent) isPart() {}

type BinaryContent struct {
	Path     string
	MIMEType string
	Data     []byte
}

func (bc BinaryContent) String(p catwalk.InferenceProvider) string {
	base64Encoded := base64.StdEncoding.EncodeToString(bc.Data)
	if p == catwalk.InferenceProviderOpenAI {
		return "data:" + bc.MIMEType + ";base64," + base64Encoded
	}
	return base64Encoded
}

func (BinaryContent) isPart() {}

type ToolCall struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Input            string `json:"input"`
	ProviderExecuted bool   `json:"provider_executed"`
	Finished         bool   `json:"finished"`
}

func (ToolCall) isPart() {}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	Data       string `json:"data"`
	MIMEType   string `json:"mime_type"`
	Metadata   string `json:"metadata"`
	IsError    bool   `json:"is_error"`
}

func (ToolResult) isPart() {}

type Finish struct {
	Reason  FinishReason `json:"reason"`
	Time    int64        `json:"time"`
	Message string       `json:"message,omitempty"`
	Details string       `json:"details,omitempty"`
}

func (Finish) isPart() {}

// ShellCommand stores a bang-mode shell command and its output as a
// distinct content part so it can be reconstructed on session restore.
type ShellCommand struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func (ShellCommand) isPart() {}

// HasShellCommand reports whether the message contains any ShellCommand parts.
func (m *Message) HasShellCommand() bool {
	for _, part := range m.Parts {
		if _, ok := part.(ShellCommand); ok {
			return true
		}
	}
	return false
}

// ShellCommands returns all ShellCommand parts from the message.
func (m *Message) ShellCommands() []ShellCommand {
	var cmds []ShellCommand
	for _, part := range m.Parts {
		if sc, ok := part.(ShellCommand); ok {
			cmds = append(cmds, sc)
		}
	}
	return cmds
}

type Message struct {
	ID               string
	Role             MessageRole
	SessionID        string
	Parts            []ContentPart
	Model            string
	Provider         string
	CreatedAt        int64
	UpdatedAt        int64
	IsSummaryMessage bool
	// PrismModelID and PrismModelName identify the model that actually
	// served the turn, as reported by the Hyper Prism model router
	// headers. Empty when the turn was not routed through Prism.
	PrismModelID   string
	PrismModelName string
	// PrismHypercreditSavings and PrismDollarSavings are the savings
	// from routing through Prism, as reported by its savings trailers.
	// Nil when not reported. When both are present the hypercredit
	// figure is the one shown.
	PrismHypercreditSavings *float64
	PrismDollarSavings      *float64
}

func (m *Message) Content() TextContent {
	for _, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			return c
		}
	}
	return TextContent{}
}

func (m *Message) ReasoningContent() ReasoningContent {
	for _, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			return c
		}
	}
	return ReasoningContent{}
}

func (m *Message) ImageURLContent() []ImageURLContent {
	imageURLContents := make([]ImageURLContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ImageURLContent); ok {
			imageURLContents = append(imageURLContents, c)
		}
	}
	return imageURLContents
}

func (m *Message) BinaryContent() []BinaryContent {
	binaryContents := make([]BinaryContent, 0)
	for _, part := range m.Parts {
		if c, ok := part.(BinaryContent); ok {
			binaryContents = append(binaryContents, c)
		}
	}
	return binaryContents
}

func (m *Message) ToolCalls() []ToolCall {
	toolCalls := make([]ToolCall, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			toolCalls = append(toolCalls, c)
		}
	}
	return toolCalls
}

// HasToolCalls reports whether the message has a tool call, without building
// the list ToolCalls returns.
func (m *Message) HasToolCalls() bool {
	for _, part := range m.Parts {
		if _, ok := part.(ToolCall); ok {
			return true
		}
	}
	return false
}

func (m *Message) ToolResults() []ToolResult {
	toolResults := make([]ToolResult, 0)
	for _, part := range m.Parts {
		if c, ok := part.(ToolResult); ok {
			toolResults = append(toolResults, c)
		}
	}
	return toolResults
}

func (m *Message) IsFinished() bool {
	for _, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			return true
		}
	}
	return false
}

func (m *Message) FinishPart() *Finish {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return &c
		}
	}
	return nil
}

func (m *Message) FinishReason() FinishReason {
	for _, part := range m.Parts {
		if c, ok := part.(Finish); ok {
			return c.Reason
		}
	}
	return ""
}

// IsErrorLike reports whether the message finished with an error-style
// banner (a real error or a provider safety refusal). The TUI renders
// both through the same banner path.
func (m *Message) IsErrorLike() bool {
	switch m.FinishReason() {
	case FinishReasonError, FinishReasonContentFilter:
		return true
	}
	return false
}

func (m *Message) IsThinking() bool {
	if m.ReasoningContent().Thinking != "" && m.Content().Text == "" && !m.IsFinished() {
		return true
	}
	return false
}

func (m *Message) AppendContent(delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			m.Parts[i] = TextContent{Text: c.Text + delta, Hidden: c.Hidden}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, TextContent{Text: delta})
	}
}

func (m *Message) AppendReasoningContent(delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:   c.Thinking + delta,
				Signature:  c.Signature,
				StartedAt:  c.StartedAt,
				FinishedAt: c.FinishedAt,
			}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, ReasoningContent{
			Thinking:  delta,
			StartedAt: time.Now().Unix(),
		})
	}
}

// StreamBuffer holds a streamed text so that appending a delta costs the
// delta's length rather than a copy of all the text so far, which is what
// concatenating per delta (AppendContent) costs: quadratic over a long reply.
// The strings it returns share its storage, which it only ever appends to,
// so they stay valid. The zero value is ready to use; do not copy one in use.
type StreamBuffer struct{ b strings.Builder }

// Append returns cur+delta. cur is normally what the previous Append
// returned; when it is anything else the buffer starts over from it.
func (s *StreamBuffer) Append(cur, delta string) string {
	// Equal strings sharing storage compare in constant time.
	if s.b.Len() != len(cur) || s.b.String() != cur {
		s.b.Reset()
		s.b.Grow(len(cur) + len(delta))
		s.b.WriteString(cur)
	}
	s.b.WriteString(delta)
	return s.b.String()
}

// AppendContentBuf is AppendContent for a stream of deltas, with buf holding
// the text so far (see StreamBuffer). Use one buf per message.
func (m *Message) AppendContentBuf(buf *StreamBuffer, delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(TextContent); ok {
			var text string
			if !found {
				text = buf.Append(c.Text, delta)
			} else {
				text = c.Text + delta
			}
			m.Parts[i] = TextContent{Text: text, Hidden: c.Hidden}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, TextContent{Text: buf.Append("", delta)})
	}
}

// AppendReasoningContentBuf is AppendReasoningContent for a stream of deltas,
// with buf holding the reasoning so far (see StreamBuffer).
func (m *Message) AppendReasoningContentBuf(buf *StreamBuffer, delta string) {
	found := false
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			var text string
			if !found {
				text = buf.Append(c.Thinking, delta)
			} else {
				text = c.Thinking + delta
			}
			m.Parts[i] = ReasoningContent{
				Thinking:   text,
				Signature:  c.Signature,
				StartedAt:  c.StartedAt,
				FinishedAt: c.FinishedAt,
			}
			found = true
		}
	}
	if !found {
		m.Parts = append(m.Parts, ReasoningContent{
			Thinking:  buf.Append("", delta),
			StartedAt: time.Now().Unix(),
		})
	}
}

func (m *Message) AppendThoughtSignature(signature string, toolCallID string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:         c.Thinking,
				ThoughtSignature: c.ThoughtSignature + signature,
				ToolID:           toolCallID,
				Signature:        c.Signature,
				StartedAt:        c.StartedAt,
				FinishedAt:       c.FinishedAt,
			}
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{ThoughtSignature: signature})
}

func (m *Message) AppendReasoningSignature(signature string) {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			m.Parts[i] = ReasoningContent{
				Thinking:   c.Thinking,
				Signature:  c.Signature + signature,
				StartedAt:  c.StartedAt,
				FinishedAt: c.FinishedAt,
			}
			return
		}
	}
	m.Parts = append(m.Parts, ReasoningContent{Signature: signature})
}

func (m *Message) FinishThinking() {
	for i, part := range m.Parts {
		if c, ok := part.(ReasoningContent); ok {
			if c.FinishedAt == 0 {
				m.Parts[i] = ReasoningContent{
					Thinking:   c.Thinking,
					Signature:  c.Signature,
					StartedAt:  c.StartedAt,
					FinishedAt: time.Now().Unix(),
				}
			}
			return
		}
	}
}

func (m *Message) ThinkingDuration() time.Duration {
	reasoning := m.ReasoningContent()
	if reasoning.StartedAt == 0 {
		return 0
	}

	endTime := reasoning.FinishedAt
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return time.Duration(endTime-reasoning.StartedAt) * time.Second
}

func (m *Message) FinishToolCall(toolCallID string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input,
					Finished: true,
				}
				return
			}
		}
	}
}

func (m *Message) AppendToolCallInput(toolCallID string, inputDelta string) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == toolCallID {
				m.Parts[i] = ToolCall{
					ID:       c.ID,
					Name:     c.Name,
					Input:    c.Input + inputDelta,
					Finished: c.Finished,
				}
				return
			}
		}
	}
}

func (m *Message) AddToolCall(tc ToolCall) {
	for i, part := range m.Parts {
		if c, ok := part.(ToolCall); ok {
			if c.ID == tc.ID {
				m.Parts[i] = tc
				return
			}
		}
	}
	m.Parts = append(m.Parts, tc)
}

func (m *Message) SetToolCalls(tc []ToolCall) {
	// remove any existing tool call part it could have multiple
	parts := make([]ContentPart, 0)
	for _, part := range m.Parts {
		if _, ok := part.(ToolCall); ok {
			continue
		}
		parts = append(parts, part)
	}
	m.Parts = parts
	for _, toolCall := range tc {
		m.Parts = append(m.Parts, toolCall)
	}
}

func (m *Message) AddToolResult(tr ToolResult) {
	m.Parts = append(m.Parts, tr)
}

func (m *Message) SetToolResults(tr []ToolResult) {
	for _, toolResult := range tr {
		m.Parts = append(m.Parts, toolResult)
	}
}

// Clone returns a deep copy of the message with an independent Parts slice.
// This prevents race conditions when the message is modified concurrently.
func (m *Message) Clone() Message {
	clone := *m
	clone.Parts = make([]ContentPart, len(m.Parts))
	copy(clone.Parts, m.Parts)
	return clone
}

// ResetStreamedContent removes all parts that were added during streaming
// (text, reasoning, tool calls, finish) so the message is ready for a
// retry. Non-streamed parts (images, binary attachments, tool results,
// shell commands) are preserved.
func (m *Message) ResetStreamedContent() {
	kept := m.Parts[:0]
	for _, part := range m.Parts {
		switch part.(type) {
		case TextContent, ReasoningContent, ToolCall, Finish:
			// Drop streamed parts.
		default:
			kept = append(kept, part)
		}
	}
	m.Parts = kept
}

func (m *Message) AddFinish(reason FinishReason, message, details string) {
	// remove any existing finish part
	for i, part := range m.Parts {
		if _, ok := part.(Finish); ok {
			m.Parts = slices.Delete(m.Parts, i, i+1)
			break
		}
	}
	m.Parts = append(m.Parts, Finish{Reason: reason, Time: time.Now().Unix(), Message: message, Details: details})
}

func (m *Message) AddImageURL(url, detail string) {
	m.Parts = append(m.Parts, ImageURLContent{URL: url, Detail: detail})
}

func (m *Message) AddBinary(mimeType string, data []byte) {
	m.Parts = append(m.Parts, BinaryContent{MIMEType: mimeType, Data: data})
}

func PromptWithTextAttachments(prompt string, attachments []Attachment) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	addedAttachments := false
	for _, content := range attachments {
		if !content.IsText() {
			continue
		}
		if !addedAttachments {
			sb.WriteString("\n<system_info>The files below have been attached by the user, consider them in your response</system_info>\n")
			addedAttachments = true
		}
		if content.FilePath != "" {
			fmt.Fprintf(&sb, "<file path='%s'>\n", content.FilePath)
		} else {
			sb.WriteString("<file>\n")
		}
		sb.WriteString("\n")
		sb.Write(content.Content)
		sb.WriteString("\n</file>\n")
	}
	return sb.String()
}
