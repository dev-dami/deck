package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// bs is a backslash, so the JSON escapes below are written as the engine
// would receive them.
const bs = "\x5c"

func TestMaskTyped(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"nested", `{"op":"browser.type","args":{"ref":"e1","text":"s3cret \"q\" \\ end"}}`, `{"op":"browser.type","args":{"ref":"e1","text":"•••"}}`},
		{"flat, any spacing", `{"op": "browser.type", "text" : "pw", "ref": "e2"}`, `{"op":"browser.type","text":"•••","ref":"e2"}`},
		{"escaped key", `{"op":"browser.type","args":{"element_ref":"e1","te` + bs + `u0078t":"hunter2"}}`, `{"op":"browser.type","args":{"element_ref":"e1","text":"•••"}}`},
		{"escaped op name", `{"op":"browser` + bs + `u002etype","args":{"text":"hunter2"}}`, `{"op":"browser` + bs + `u002etype","args":{"text":"•••"}}`},
		{"padded op name", `{"op":" browser.type ","args":{"text":"hunter2"}}`, `{"op":" browser.type ","args":{"text":"•••"}}`},
		{"repeated key", `{"op":"browser.type","args":{"text":"a","text":"b"}}`, `{"op":"browser.type","args":{"text":"•••","text":"•••"}}`},
		// Other ops keep their "text" argument; so does anything that is not a call.
		{"other op", `{"op":"thread.send","args":{"to":"all","text":"hi"}}`, `{"op":"thread.send","args":{"to":"all","text":"hi"}}`},
		{"select keeps its option", `{"op":"browser.select","args":{"text":"Chile"}}`, `{"op":"browser.select","args":{"text":"Chile"}}`},
		{"click", `{"op":"browser.click","args":{"element_ref":"e1"}}`, `{"op":"browser.click","args":{"element_ref":"e1"}}`},
		{"empty", ``, ``},
	} {
		if got := string(MaskTyped(json.RawMessage(c.in))); got != c.want {
			t.Errorf("%s: MaskTyped(%s) = %s, want %s", c.name, c.in, got, c.want)
		}
	}
}

func TestMaskTypedNeverLeaks(t *testing.T) {
	// Whatever shape a browser.type call arrives in, the secret is not in what is kept.
	for _, in := range []string{
		`{"op":"browser.type","args":{"text":"SECRET"}}`,
		`{"op":"browser.type","text":"SECRET"}`,
		`{"op":"browser.type","args":{"te` + bs + `u0078t":"SECRET"}}`,
		`{"o` + bs + `u0070":"browser.type","args":{"text":"SECRET"}}`,
		`{"op":"browser.type","args":{"text":"SECRET"},"text":"SECRET"}`,
	} {
		if got := string(MaskTyped(json.RawMessage(in))); strings.Contains(got, "SECRET") {
			t.Errorf("MaskTyped(%s) = %s keeps the secret", in, got)
		}
	}
}
