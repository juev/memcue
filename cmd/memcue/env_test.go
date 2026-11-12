package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain drops the backend the machine running the suite is configured for. A machine in server mode exports
// MEMCUE_SERVER (or MEMCUE_POSTGRES_DSN) and its tailnet settings, often from the harness's own environment;
// inherited, every command a test runs in process or as a built binary goes to that backend instead of the test's
// MEMCUE_FACTS (#734). A test that wants a backend sets it itself.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if inheritedBackend(name) {
			_ = os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}

func inheritedBackend(name string) bool {
	return name == "MEMCUE_SERVER" || name == "MEMCUE_POSTGRES_DSN" || strings.HasPrefix(name, "MEMCUE_TAILSCALE_")
}
