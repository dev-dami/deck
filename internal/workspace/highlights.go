package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dev-dami/deck/internal/deck/highlights"
	"github.com/dev-dami/deck/internal/message"
	"github.com/dev-dami/deck/internal/pubsub"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
	"google.golang.org/protobuf/proto"
)

type HighlightsDesk interface {
	Highlights(context.Context, string) (*pb.HighlightsResponse, error)
	HighlightAction(context.Context, *pb.HighlightActionRequest) (*pb.HighlightItem, error)
}

func (w *DeckWorkspace) highlightStore() *highlights.Store {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.highlights == nil {
		dir := ""
		if w.path != "" {
			dir = filepath.Join(filepath.Dir(w.path), "highlights")
		}
		w.highlights = highlights.New(dir, func(c highlights.Change) {
			change := DeckChange{SessionID: c.SessionID, Highlight: c.Item, Summary: c.Summary}
			if c.Item != nil {
				change.Kind = ChangeHighlight
			} else {
				change.Kind = ChangeSummary
			}
			w.changes().PublishMustDeliver(context.Background(), pubsub.UpdatedEvent, change)
		})
	}
	return w.highlights
}

func (w *DeckWorkspace) Highlights(ctx context.Context, sid string) (*pb.HighlightsResponse, error) {
	if err := w.topSession(ctx, sid); err != nil {
		return nil, err
	}
	return w.highlightStore().Snapshot(sid)
}
func (w *DeckWorkspace) HighlightAction(ctx context.Context, r *pb.HighlightActionRequest) (*pb.HighlightItem, error) {
	if r == nil {
		return nil, highlights.ErrInvalid
	}
	if err := w.topSession(ctx, r.SessionId); err != nil {
		return nil, err
	}
	r = proto.Clone(r).(*pb.HighlightActionRequest)
	author, err := w.highlightSource(ctx, r.SessionId, "", r.Source, r.Action == "pin")
	if err != nil {
		return nil, err
	}
	if r.Source != nil && r.Source.TurnId != "" {
		return nil, highlights.ErrInvalid
	}
	return w.highlightStore().Action(highlights.Actor{User: true}, r, author)
}

func (w *DeckWorkspace) DeleteSession(ctx context.Context, sid string) error {
	if err := w.topSession(ctx, sid); err != nil {
		return err
	}
	return w.highlightStore().Delete(sid, func() error { return w.AppWorkspace.DeleteSession(ctx, sid) })
}

// highlightSource checks primary refs before persistence. Session IDs in
// messages and engine-thread IDs in tasks are deliberately not interchangeable.
func (w *DeckWorkspace) highlightSource(ctx context.Context, sid, thread string, source *pb.HighlightSource, pin bool) (string, error) {
	if source == nil {
		if pin {
			return "", highlights.ErrInvalid
		}
		return "", nil
	}
	if source.MessageId != "" && source.TaskId != "" {
		return "", highlights.ErrInvalid
	}
	if source.MessageId != "" {
		msg, err := w.app.Messages.Get(ctx, source.MessageId)
		if err != nil || msg.SessionID != sid {
			return "", ErrNotFound
		}
		if pin && msg.Role != message.Assistant {
			return "", highlights.ErrInvalid
		}
		if msg.Model != "" {
			return msg.Model, nil
		}
		if msg.Provider != "" {
			return msg.Provider, nil
		}
		return "agent", nil
	}
	if pin {
		return "", highlights.ErrInvalid
	}
	if source.TaskId != "" {
		if thread == "" {
			w.mu.Lock()
			thread = w.threads[sid]
			w.mu.Unlock()
		}
		if thread == "" {
			return "", ErrNotFound
		}
		if _, ok := w.mgr().Task(thread, source.TaskId); !ok {
			return "", ErrNotFound
		}
	}
	return "", nil
}

func (w *DeckWorkspace) runHighlights(ctx context.Context, thread, agent, turn, op string, raw json.RawMessage) (string, error) {
	sid := w.sessionOf(thread)
	if sid == "" {
		return "", ErrNotFound
	}
	if err := w.topSession(ctx, sid); err != nil {
		return "", err
	}
	actor := highlights.Actor{Agent: agent, Turn: turn}
	encode := func(value any, err error) (string, error) {
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(value)
		return string(data), err
	}
	if op == "highlight.list" {
		return encode(w.highlightStore().Snapshot(sid))
	}
	if op == "summary.update" {
		var args struct {
			Text   string              `json:"text"`
			Source *pb.HighlightSource `json:"source"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", highlights.ErrInvalid
		}
		if _, err := w.highlightSource(ctx, sid, thread, args.Source, false); err != nil {
			return "", err
		}
		return encode(w.highlightStore().Summary(sid, actor, args.Text, args.Source))
	}
	action := strings.TrimPrefix(op, "highlight.")
	switch action {
	case "add", "update", "resolve", "dismiss":
	default:
		return "", highlights.ErrInvalid
	}
	var req pb.HighlightActionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", fmt.Errorf("highlight arguments: %w", err)
	}
	req.SessionId = sid
	req.Action = action
	if _, err := w.highlightSource(ctx, sid, thread, req.Source, false); err != nil {
		return "", err
	}
	return encode(w.highlightStore().Action(actor, &req, ""))
}

func (w *DeckWorkspace) bindHighlightTurn(sid, agent, turn, msg string) {
	if turn == "" {
		return
	}
	if err := w.highlightStore().BindTurn(sid, agent, turn, msg); err != nil {
		// Source enrichment must not interrupt projection of the conversation.
		// A later explicit update can still carry a validated primary reference.
		w.publish(DeckChange{Kind: ChangeNotice, SessionID: sid, Text: "Could not link a Highlight to its source: " + err.Error()})
	}
}
