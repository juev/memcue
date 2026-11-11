package tailnet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

// Listener is the embedded node of memcue server (#714): it accepts connections on one port of the tailnet and opens
// no port of the host (R13). It owns the identity directory of c for its life, as a helper does, so a helper cannot
// take the same identity.
type Listener struct {
	net.Listener
	server  *tsnet.Server
	client  *local.Client
	release func()
	once    sync.Once
	err     error
}

// Listen starts the node of c and listens on c.Port of the tailnet. With bootstrap the first login uses the auth key
// of c.KeyFile; otherwise the node logs the login URL through logf until someone approves it. Access is left to the
// tailnet ACL.
func Listen(c Config, bootstrap bool, logf func(string, ...any)) (*Listener, error) {
	if err := rejectAmbient(); err != nil {
		return nil, err
	}
	release, err := acquire(c)
	if err != nil {
		return nil, err
	}
	authKey := ""
	if bootstrap {
		if authKey, err = bootstrapKey(c.KeyFile); err != nil {
			release()
			return nil, err
		}
	}
	prepareProcess()
	srv := &tsnet.Server{Dir: c.Dir, Hostname: c.Hostname, ControlURL: c.ControlURL, AuthKey: authKey,
		UserLogf: logf, Logf: func(string, ...any) {}}
	if err := srv.Start(); err != nil {
		release()
		return nil, errors.New("embedded node could not start; inspect state permissions and configuration")
	}
	client, err := srv.LocalClient()
	if err != nil {
		_ = srv.Close()
		release()
		return nil, errHelper
	}
	ln, err := srv.Listen("tcp", ":"+c.Port)
	if err != nil {
		_ = srv.Close()
		release()
		return nil, errors.New("embedded node could not listen on the tailnet")
	}
	return &Listener{Listener: ln, server: srv, client: client, release: release}, nil
}

// Who names the tailnet node a request came from, or its address when the tailnet does not know it.
func (l *Listener) Who(r *http.Request) string {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w, err := l.client.WhoIs(ctx, r.RemoteAddr)
	if err != nil || w.Node == nil {
		return r.RemoteAddr
	}
	if w.Node.ComputedName != "" {
		return w.Node.ComputedName
	}
	return w.Node.Name
}

// Close stops listening, stops the node and releases its identity. http.Server closes it on shutdown, so a second
// Close only returns the first one's result.
func (l *Listener) Close() error {
	l.once.Do(func() {
		l.err = l.Listener.Close()
		_ = l.server.Close()
		l.release()
	})
	return l.err
}
