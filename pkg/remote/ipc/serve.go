package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/dev-dami/deck/pkg/remote"
)

// Noticer is implemented by a provider with a screen (the terminal UI): the
// daemon sends it short messages for the person looking at it.
type Noticer interface {
	Notice(text string)
}

// ServeConn runs the provider side: it answers the daemon's calls with b and
// forwards b's events until ctx ends or the connection closes. The caller
// owns closing conn.
func ServeConn(ctx context.Context, conn io.ReadWriter, b remote.Backend) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &writer{w: conn}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range b.Events(ctx) {
			raw, err := json.Marshal(ev.Payload)
			if err != nil {
				continue
			}
			if w.send(frame{T: "ev", Ev: &evBody{Name: ev.Name, SessionID: ev.SessionID, Scoped: ev.Scoped, Payload: raw}}) != nil {
				cancel()
				return
			}
		}
	}()

	sc := scanner(conn)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.T == "notice" && f.N != "" {
			if n, ok := b.(Noticer); ok {
				n.Notice(f.N)
			}
			continue
		}
		if f.T != "call" || f.A == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ret := frame{T: "ret", ID: f.ID}
			r, err := dispatch(ctx, b, f.M, *f.A)
			if err != nil {
				ret.E = toErrBody(err)
			} else if ret.R, err = json.Marshal(r); err != nil {
				ret.E = toErrBody(err)
			}
			_ = w.send(ret)
		}()
	}
	cancel()
	wg.Wait()
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

var errUnknownMethod = errors.New("ipc: unknown method")

func dispatch(ctx context.Context, b remote.Backend, m string, a args) (any, error) {
	switch m {
	case "info":
		i := b.Info()
		_, ui := b.(Noticer)
		return infoBody{Version: i.Version, Project: i.Project, Kinds: i.Kinds, Theme: i.Theme, UI: ui}, nil
	case "sessions":
		return b.Sessions(ctx)
	case "create_session":
		return b.CreateSession(ctx, a.Title)
	case "rename_session":
		return struct{}{}, b.RenameSession(ctx, a.ID, a.Title)
	case "delete_session":
		return struct{}{}, b.DeleteSession(ctx, a.ID)
	case "messages":
		msgs, more, next, err := b.Messages(ctx, a.SessionID, a.Before, a.Limit)
		return messagesBody{msgs, more, next}, err
	case "send":
		imgs := make([]remote.Image, len(a.Images))
		for i, im := range a.Images {
			imgs[i] = remote.Image{Name: im.Name, Mime: im.Mime, Data: im.Data}
		}
		return b.Send(ctx, a.SessionID, a.Text, imgs)
	case "edit":
		return struct{}{}, b.EditMessage(ctx, a.SessionID, a.MessageID, a.Text)
	case "cancel":
		return struct{}{}, b.Cancel(a.SessionID)
	case "interrupt":
		return struct{}{}, b.Interrupt(a.SessionID, a.Agent)
	case "continue":
		return struct{}{}, b.Continue(a.SessionID)
	case "queue":
		return b.Queue(a.SessionID)
	case "steer":
		return struct{}{}, b.Steer(a.SessionID, a.ID)
	case "cancel_queued":
		return struct{}{}, b.CancelQueued(a.SessionID, a.ID)
	case "roster":
		return b.Roster(a.SessionID)
	case "models":
		return b.Models(ctx, a.SessionID, a.Kind)
	case "set_model":
		return struct{}{}, b.SetModel(a.SessionID, a.Kind, a.Model, a.Effort)
	case "mode":
		return b.Mode(a.SessionID)
	case "set_mode":
		return struct{}{}, b.SetMode(a.SessionID, a.Mode)
	case "approvals":
		return b.Approvals(a.SessionID)
	case "answer":
		return b.Answer(a.SessionID, a.ID, a.Decision)
	case "changes":
		return b.Changes(ctx, a.SessionID)
	}
	return nil, errUnknownMethod
}
