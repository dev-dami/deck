package server

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dev-dami/deck/pkg/remote/wire"
)

// mp4 is the start of an MP4 file: an ftyp box of brand isom.
var mp4 = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x02\x00isomiso2avc1mp41")

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// TestVideoUploadAndSend covers a video from the phone: uploaded in chunks
// (media.upload), then named by chat.send with its label, and taken once.
func TestVideoUploadAndSend(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	c, welcome := r.welcome(tok, nil)
	require.Contains(t, welcome.Welcome.Server.Features, "video")

	video := append(append([]byte(nil), mp4...), bytes.Repeat([]byte{7}, 100)...)
	first := unmarshal[wire.MediaUploadResult](t, okRes(t, c.call(wire.MMediaUpload, wire.MediaUploadParams{
		SessionID: "s1", Name: "C:\\clips\\crash.mp4", Mime: "video/mp4", Size: len(video), Data: b64(video[:60]),
	})))
	require.NotEmpty(t, first.UploadID)
	require.Equal(t, 60, first.Received)

	// A chunk that does not continue the upload is refused.
	res := c.call(wire.MMediaUpload, wire.MediaUploadParams{SessionID: "s1", UploadID: first.UploadID, Offset: 10, Data: b64(video[60:])})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	// Sending before it is complete is refused, and leaves it.
	img := wire.Image{Name: "crash.mp4", Mime: "video/mp4", UploadID: first.UploadID, Label: "the crash"}
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "look", Images: []wire.Image{img}})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))

	last := unmarshal[wire.MediaUploadResult](t, okRes(t, c.call(wire.MMediaUpload, wire.MediaUploadParams{
		SessionID: "s1", UploadID: first.UploadID, Offset: 60, Data: b64(video[60:]),
	})))
	require.Equal(t, len(video), last.Received)

	png := []byte{0x89, 'P', 'N', 'G'}
	okRes(t, c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "look", Images: []wire.Image{
		{Name: "shot.png", Mime: "image/png", Data: b64(png), Label: "Screenshot 1"},
		img,
	}}))
	sent := r.be.Sent()
	require.Len(t, sent, 1)
	require.Len(t, sent[0].Images, 2)
	require.Equal(t, "Screenshot 1", sent[0].Images[0].Label)
	v := sent[0].Images[1]
	require.Equal(t, "the crash", v.Label)
	require.Equal(t, "video/mp4", v.Mime)
	require.Equal(t, "crash.mp4", v.Name)
	require.Equal(t, video, v.Data)

	// The upload went with the message.
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Text: "again", Images: []wire.Image{img}})
	require.Equal(t, wire.CodeNotFound, errCode(t, res))
}

func TestVideoUploadLimits(t *testing.T) {
	r := newRig(t)
	tok, _ := r.token("Pixel")
	c, _ := r.welcome(tok, nil)
	start := func(p wire.MediaUploadParams) wire.Frame {
		p.SessionID = "s1"
		return c.call(wire.MMediaUpload, p)
	}
	// The bytes decide, not the type the phone claims.
	res := start(wire.MediaUploadParams{Mime: "video/mp4", Size: 20, Data: b64([]byte("#!/bin/sh echo hi"))})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	res = start(wire.MediaUploadParams{Mime: "application/x-sh", Size: len(mp4), Data: b64(mp4)})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	res = start(wire.MediaUploadParams{Mime: "video/mp4", Size: wire.MaxVideoUploadBytes + 1, Data: b64(mp4)})
	require.Equal(t, wire.CodeTooLarge, errCode(t, res))
	res = start(wire.MediaUploadParams{Mime: "video/mp4", Size: wire.MaxUploadChunkBytes * 2, Data: b64(make([]byte, wire.MaxUploadChunkBytes+1))})
	require.Equal(t, wire.CodeTooLarge, errCode(t, res))
	// Two uploads at a time per device.
	for range uploadsPerDevice {
		okRes(t, start(wire.MediaUploadParams{Mime: "video/mp4", Size: 1000, Data: b64(mp4)}))
	}
	res = start(wire.MediaUploadParams{Mime: "video/mp4", Size: 1000, Data: b64(mp4)})
	require.Equal(t, wire.CodeTooLarge, errCode(t, res))
	// They expire.
	r.clk.advance(uploadTTL + 1)
	okRes(t, start(wire.MediaUploadParams{Mime: "video/mp4", Size: 1000, Data: b64(mp4)}))

	// A video is never sent inline, nor is an image sent as frames.
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Images: []wire.Image{{Name: "a.mp4", Mime: "video/mp4", Data: b64(mp4)}}})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
	res = c.call(wire.MChatSend, wire.ChatSendParams{SessionID: "s1", Images: []wire.Image{{Name: "a.png", Mime: "image/png", Data: b64([]byte("x")), Frames: true}}})
	require.Equal(t, wire.CodeInvalid, errCode(t, res))
}
