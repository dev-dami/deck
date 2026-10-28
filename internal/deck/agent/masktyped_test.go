package agent

import (
	"encoding/json"
	"testing"
)

func TestMaskTyped(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"op":"browser.type","args":{"ref":"e1","text":"s3cret \"q\" \\ end"}}`, `{"op":"browser.type","args":{"ref":"e1","text":"•••"}}`},
		{`{"op": "browser.type", "text" : "pw", "ref": "e2"}`, `{"op": "browser.type", "text" : "•••", "ref": "e2"}`},
		// Other ops keep their "text" argument; so does anything that is not a call.
		{`{"op":"thread.send","args":{"to":"all","text":"hi"}}`, `{"op":"thread.send","args":{"to":"all","text":"hi"}}`},
		{`{"op":"browser.click","args":{"ref":"e1"}}`, `{"op":"browser.click","args":{"ref":"e1"}}`},
		{``, ``},
	} {
		if got := string(MaskTyped(json.RawMessage(c.in))); got != c.want {
			t.Errorf("MaskTyped(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}
