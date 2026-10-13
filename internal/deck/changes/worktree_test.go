package changes

import (
	"reflect"
	"testing"
)

func TestSplitRaw(t *testing.T) {
	out := ":100644 100644 bdc955b 8352675 M\x00bin\x00" +
		":000000 100644 0000000 8ba3a16 A\x00new\x00" +
		":100644 100644 9405325 d5e10a2 R090\x00x.txt\x00y.txt\x00" +
		"\x00diff --git a/bin b/bin\nGIT binary patch\n"
	files, patch := splitRaw(out)
	if want := []string{"bin", "new", "y.txt"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	if patch != "diff --git a/bin b/bin\nGIT binary patch\n" {
		t.Fatalf("patch = %q", patch)
	}
	if files, patch := splitRaw(""); files != nil || patch != "" {
		t.Fatalf("empty: %q %q", files, patch)
	}
}
