package transcript

import (
	"strings"
	"testing"
)

// The working directory is the first one an entry names; a line that is not JSON or names none is skipped.
func TestCWD(t *testing.T) {
	in := `not json
{"type":"summary"}
{"type":"user","cwd":"/src/app","message":{"content":"Собери проект."}}
{"type":"assistant","cwd":"/src/other","message":{"content":"Сборка зелёная."}}`
	if got, err := CWD(strings.NewReader(in)); err != nil || got != "/src/app" {
		t.Errorf("got %q %v, want /src/app", got, err)
	}
	if got, err := CWD(strings.NewReader(`{"type":"user"}`)); err != nil || got != "" {
		t.Errorf("no cwd: got %q %v", got, err)
	}
}
