package tailnet

import (
	"encoding/json"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

// A control server with SSH rules in its ACL sends an SSH policy in every netmap (#697). The pinned tailscale.com
// must decode it with the standard library of the pinned Go: tsnet v1.98.6 tagged SSHAction.SessionDuration with
// `format:nano`, which Go 1.27's encoding/json refuses, so the node never left NeedsLogin after approval.
func TestNetmapWithSSHPolicyDecodes(t *testing.T) {
	in := []byte(`{"SSHPolicy":{"rules":[{"principals":[{"any":true}],"sshUsers":{"*":"="},"action":{"accept":true,"sessionDuration":3600000000000}}]}}`)
	var m tailcfg.MapResponse
	if err := json.Unmarshal(in, &m); err != nil {
		t.Fatalf("netmap with an SSH policy does not decode: %v", err)
	}
	if m.SSHPolicy == nil || len(m.SSHPolicy.Rules) != 1 || m.SSHPolicy.Rules[0].Action == nil {
		t.Fatalf("SSH policy lost: %+v", m.SSHPolicy)
	}
	if got := m.SSHPolicy.Rules[0].Action.SessionDuration; got != time.Hour {
		t.Fatalf("session duration %v, want 1h", got)
	}
	if _, err := json.Marshal(m); err != nil {
		t.Fatalf("netmap with an SSH policy does not encode: %v", err)
	}
}
