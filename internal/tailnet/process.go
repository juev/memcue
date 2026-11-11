package tailnet

import (
	"runtime"

	"tailscale.com/envknob"
	"tailscale.com/net/netns"
)

// prepareProcess sets the process-wide tailscale knobs an embedded node needs before tsnet starts it.
//
// On Linux a process without CAP_NET_ADMIN cannot mark its sockets with SO_MARK, so tailscale's netns binds every
// socket to the default-route interface with SO_BINDTODEVICE instead. A connection that the host then routes elsewhere
// never completes. On loffe the control server resolves to an address of loffe itself, and Docker's DNAT sends that
// connection to a container on a bridge, which a socket bound to eth0 cannot reach: neither memcue server nor the
// helper could log in (#726). The embedded node creates no TUN device and changes no route, so its own traffic has no
// tailnet route to bypass, and it dials as any other process does.
func prepareProcess() {
	envknob.SetNoLogsNoSupport()
	if runtime.GOOS == "linux" {
		netns.SetEnabled(false)
	}
}
