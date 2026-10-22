package extract

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hungEndpoint accepts a request and never answers it: the client has to give up on its own.
func hungEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

func TestJevJudgeTimeouts(t *testing.T) {
	old := JevJudge
	t.Cleanup(func() { JevJudge = old })
	JevJudge.TriageTimeout = 0
	if JevJudgeTriageTimeout() != DefaultTriageTimeout {
		t.Errorf("default %v", JevJudgeTriageTimeout())
	}
	JevJudge.TriageTimeout = 750 * time.Millisecond
	if JevJudgeTriageTimeout() != 750*time.Millisecond {
		t.Errorf("configured %v", JevJudgeTriageTimeout())
	}
}

func TestJevJudgeSettings(t *testing.T) {
	old := JevJudge
	t.Cleanup(func() { JevJudge = old })
	JevJudge.URL, JevJudge.Model, JevJudge.Key = "u", "m", ""
	if JevJudgeOn() {
		t.Error("on without a key")
	}
	JevJudge.Key = "k"
	if !JevJudgeOn() {
		t.Error("off with all three")
	}
}
