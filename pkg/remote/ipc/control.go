package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

// The control channel lets the CLI ask the running daemon to do what only the
// daemon's memory can: arm a pairing slot, report status. It is one JSON
// request per line, answered in order.

type controlRequest struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

type controlResponse struct {
	OK     bool            `json:"ok"`
	Err    string          `json:"err,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// ControlHandler runs one command. Its error text is shown to the person at
// the terminal, so write it for them.
type ControlHandler func(ctx context.Context, cmd string, args json.RawMessage) (any, error)

// ServeControl answers requests on conn until it closes or ctx ends.
func ServeControl(ctx context.Context, conn io.ReadWriter, h ControlHandler) error {
	sc := scanner(conn)
	var wmu sync.Mutex
	for ctx.Err() == nil && sc.Scan() {
		var req controlRequest
		resp := controlResponse{}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp.Err = "malformed request"
		} else if res, err := h(ctx, req.Cmd, req.Args); err != nil {
			resp.Err = err.Error()
		} else if raw, err := json.Marshal(res); err != nil {
			resp.Err = "the result could not be encoded"
		} else {
			resp.OK, resp.Result = true, raw
		}
		raw, _ := json.Marshal(resp)
		wmu.Lock()
		_, err := conn.Write(append(raw, '\n'))
		wmu.Unlock()
		if err != nil {
			return err
		}
	}
	return sc.Err()
}

// ControlClient is the CLI's side.
type ControlClient struct {
	conn net.Conn
	mu   sync.Mutex
	read func() ([]byte, error)
}

// DialControl connects to the running daemon as a control client.
func DialControl(ctx context.Context, e Endpoint) (*ControlClient, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", e.Addr)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(hello{V: 1, Secret: e.Secret, Role: RoleControl})
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		conn.Close()
		return nil, err
	}
	sc := scanner(conn)
	return &ControlClient{conn: conn, read: func() ([]byte, error) {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		return append([]byte(nil), sc.Bytes()...), nil
	}}, nil
}

// Call runs cmd and decodes its result into out (which may be nil).
func (c *ControlClient) Call(ctx context.Context, cmd string, args, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var raw json.RawMessage
	if args != nil {
		var err error
		if raw, err = json.Marshal(args); err != nil {
			return err
		}
	}
	line, _ := json.Marshal(controlRequest{Cmd: cmd, Args: raw})
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer stop()
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err
	}
	resp, err := c.read()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	var r controlResponse
	if err := json.Unmarshal(resp, &r); err != nil {
		return err
	}
	if !r.OK {
		return errors.New(r.Err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Result, out)
}

// Close ends the connection.
func (c *ControlClient) Close() error { return c.conn.Close() }
