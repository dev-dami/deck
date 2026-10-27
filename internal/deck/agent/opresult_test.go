package agent

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func testOpPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestOpResultImageValidation(t *testing.T) {
	valid := testOpPNG(t, 2, 2)
	for _, tc := range []struct {
		name   string
		images []OpImage
		bad    bool
	}{
		{"text", nil, false},
		{"png", []OpImage{{MimeType: "image/png", Data: valid}}, false},
		{"wrong MIME", []OpImage{{MimeType: "image/jpeg", Data: valid}}, true},
		{"unsupported", []OpImage{{MimeType: "image/svg+xml", Data: valid}}, true},
		{"empty", []OpImage{{MimeType: "image/png"}}, true},
		{"corrupt", []OpImage{{MimeType: "image/png", Data: []byte("not a PNG")}}, true},
		{"too many", make([]OpImage, MaxOpImages+1), true},
		{"byte cap", []OpImage{{MimeType: "image/png", Data: make([]byte, MaxOpImageBytes+1)}}, true},
		{"pixel cap", []OpImage{{MimeType: "image/png", Data: testOpPNG(t, 4097, 4096)}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (OpResult{Images: tc.images}).ValidateImages(); (err != nil) != tc.bad {
				t.Fatalf("validation error = %v, want bad=%v", err, tc.bad)
			}
		})
	}
}

func TestOpResultJSONPreservesImageBytes(t *testing.T) {
	want := OpResult{Text: "tab t1", Images: []OpImage{{MimeType: "image/png", Data: testOpPNG(t, 3, 2)}}}
	wire, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got OpResult
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != want.Text || len(got.Images) != 1 || !bytes.Equal(got.Images[0].Data, want.Images[0].Data) {
		t.Fatal("image bytes changed over JSON transport")
	}
	if err := got.ValidateImages(); err != nil {
		t.Fatal(err)
	}
}
