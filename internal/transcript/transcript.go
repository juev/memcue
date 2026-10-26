// Package transcript reads what memcue needs of a Claude Code session transcript (JSON lines): the working directory
// the session ran in, which names the project of the built-in memory notes migrate turns into facts. Nothing reads a
// session's text any more: the agent writes its facts itself (#494).
package transcript

import (
	"bufio"
	"encoding/json"
	"io"
)

// maxLine is the longest transcript line read: a line holds a whole tool result.
const maxLine = 64 << 20

// CWD returns the working directory of the first entry that names one, or "" when none does.
func CWD(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), maxLine)
	for sc.Scan() {
		var e struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.CWD != "" {
			return e.CWD, nil
		}
	}
	return "", sc.Err()
}
