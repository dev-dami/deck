package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

// markTurnDone records a completed turn so a late turn/start response cannot
// make the run look busy again.
func (r *run) markTurnDone(id string) {
	r.mu.Lock()
	if r.doneTurns == nil {
		r.doneTurns = map[string]bool{}
	}
	r.doneTurns[id] = true
	r.mu.Unlock()
}

// mediaTypeByExt maps the image extensions Codex accepts to media types.
var mediaTypeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

var allowedMediaTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// imageInputs validates the images and turns them into localImage input items
// carrying absolute paths.
func imageInputs(images []agent.Image) ([]userInput, error) {
	var out []userInput
	for _, img := range images {
		if img.Path == "" {
			return nil, errors.New("codex: image has no path")
		}
		mt := strings.ToLower(img.MediaType)
		if mt == "" {
			mt = mediaTypeByExt[strings.ToLower(filepath.Ext(img.Path))]
		}
		if !allowedMediaTypes[mt] {
			return nil, fmt.Errorf("codex: unsupported image type for %s (want png, jpeg, gif or webp)", filepath.Base(img.Path))
		}
		abs, err := filepath.Abs(img.Path)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("codex: image: %w", err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("codex: image %s is not a regular file", filepath.Base(abs))
		}
		out = append(out, userInput{Type: "localImage", Path: abs})
	}
	return out, nil
}

// Steer injects the prompt (and images) into the running turn with turn/steer.
// When no turn is running, or the turn ended before the request landed, it
// starts a normal turn instead.
func (r *run) Steer(prompt string, images []agent.Image) error {
	tid := r.SessionID()
	if tid == "" {
		return errNoThread
	}
	imgs, err := imageInputs(images)
	if err != nil {
		return err
	}
	r.mu.Lock()
	turn := r.turnID
	r.mu.Unlock()
	if turn == "" {
		return r.SendWithImages(prompt, images)
	}

	input := append([]userInput(nil), imgs...)
	if prompt != "" || len(imgs) == 0 {
		input = append(input, userInput{Type: "text", Text: prompt, TextElements: []any{}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	err = r.client.Call(ctx, methodTurnSteer, turnSteerParams{ThreadID: tid, ExpectedTurnID: turn, Input: input}, nil)
	var rpcErr *RPCError
	if err == nil || !errors.As(err, &rpcErr) {
		return err
	}
	// The server refused: most likely the turn ended in the meantime. If it
	// did, the prompt is simply the next turn; otherwise report the refusal.
	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		r.mu.Lock()
		cur := r.turnID
		r.mu.Unlock()
		if cur != turn {
			return r.SendWithImages(prompt, images)
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-r.closing:
			return err
		}
	}
}

var (
	_ agent.ImageSender = (*run)(nil)
	_ agent.Steerer     = (*run)(nil)
)
