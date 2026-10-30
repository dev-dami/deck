package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"

	"github.com/dev-dami/deck/internal/deck/media"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// A video is too big for one frame, so the phone uploads it in chunks
// (media.upload) and chat.send then names it. Uploads are held in memory,
// bounded: at most uploadsPerDevice per device, each at most
// wire.MaxVideoUploadBytes, dropped uploadTTL after their last chunk or
// once a message takes them. Nothing reaches the disk until the message is
// sent; Deck then keeps the video in its data directory (internal/deck/
// media).
const (
	uploadTTL        = 10 * time.Minute
	uploadsPerDevice = 2
)

type upload struct {
	dev, session string
	name, mime   string
	data         []byte // grows to size
	size         int
	at           time.Time // the last chunk
}

type uploads struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*upload // by upload id
}

func newUploads(now func() time.Time) *uploads {
	return &uploads{now: now, m: map[string]*upload{}}
}

// pruneLocked drops expired uploads; u.mu is held.
func (u *uploads) pruneLocked() {
	for id, up := range u.m {
		if u.now().Sub(up.at) > uploadTTL {
			delete(u.m, id)
		}
	}
}

func tooLarge(msg string) *wire.Error { return &wire.Error{Code: wire.CodeTooLarge, Msg: msg} }

// put takes one chunk for device dev.
func (u *uploads) put(dev string, p wire.MediaUploadParams) (wire.MediaUploadResult, *wire.Error) {
	if p.SessionID == "" {
		return wire.MediaUploadResult{}, invalid("session_id is required")
	}
	if base64.StdEncoding.DecodedLen(len(p.Data)) > wire.MaxUploadChunkBytes+3 {
		return wire.MediaUploadResult{}, tooLarge("a chunk may be at most 4 MB")
	}
	chunk, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil || len(chunk) == 0 {
		return wire.MediaUploadResult{}, invalid("a chunk is not valid base64")
	}
	if len(chunk) > wire.MaxUploadChunkBytes {
		return wire.MediaUploadResult{}, tooLarge("a chunk may be at most 4 MB")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked()
	if p.UploadID == "" {
		// The first chunk opens the upload: it says what the whole video
		// is, and its bytes must start like one.
		if !media.IsVideoType(p.Mime) {
			return wire.MediaUploadResult{}, invalid("only MP4, WebM or QuickTime videos can be uploaded")
		}
		if p.Size <= 0 || p.Offset != 0 || len(chunk) > p.Size {
			return wire.MediaUploadResult{}, invalid("a new upload starts at offset 0 and says its size")
		}
		if p.Size > wire.MaxVideoUploadBytes {
			return wire.MediaUploadResult{}, tooLarge("a video may be at most " + media.Size(wire.MaxVideoUploadBytes))
		}
		if media.Sniff(chunk[:min(len(chunk), 64)]) == "" {
			return wire.MediaUploadResult{}, invalid("that file is not an MP4, WebM or QuickTime video")
		}
		n := 0
		for _, up := range u.m {
			if up.dev == dev {
				n++
			}
		}
		if n >= uploadsPerDevice {
			return wire.MediaUploadResult{}, tooLarge("finish or send the videos already uploading first")
		}
		var b [12]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		data := make([]byte, 0, p.Size)
		u.m[id] = &upload{dev: dev, session: p.SessionID, name: p.Name, mime: p.Mime, size: p.Size, data: append(data, chunk...), at: u.now()}
		return wire.MediaUploadResult{UploadID: id, Received: len(chunk)}, nil
	}
	up := u.m[p.UploadID]
	if up == nil || up.dev != dev || up.session != p.SessionID {
		return wire.MediaUploadResult{}, &wire.Error{Code: wire.CodeNotFound, Msg: "no such upload; start it again"}
	}
	if p.Offset != len(up.data) || len(up.data)+len(chunk) > up.size {
		return wire.MediaUploadResult{}, invalid("that chunk does not fit the upload")
	}
	up.data = append(up.data, chunk...)
	up.at = u.now()
	return wire.MediaUploadResult{UploadID: p.UploadID, Received: len(up.data)}, nil
}

// take hands a complete upload of dev's to a message in session, and
// forgets it.
func (u *uploads) take(dev, session, id string) (*upload, *wire.Error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pruneLocked()
	up := u.m[id]
	if up == nil || up.dev != dev || up.session != session {
		return nil, &wire.Error{Code: wire.CodeNotFound, Msg: "that video upload is gone; attach it again"}
	}
	if len(up.data) != up.size {
		return nil, invalid("that video has not finished uploading")
	}
	delete(u.m, id)
	return up, nil
}
