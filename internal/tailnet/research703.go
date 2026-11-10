//go:build research

package tailnet

import (
	"context"
	"errors"
	"net"

	"tailscale.com/envknob"
	"tailscale.com/tsnet"
)

// ResearchDialer runs the embedded node inside the calling process, for variant 4 of the #703 daemon prototype: one
// process owns the tailnet identity and the database connection, with no helper socket between them. It takes the
// same owner lock as the helper, so the helper must be stopped first (connect off). Built only with -tags research.
func ResearchDialer(ctx context.Context, c Config) (func(ctx context.Context, network, address string) (net.Conn, error), func(), error) {
	if err := rejectAmbient(); err != nil {
		return nil, nil, err
	}
	release, err := acquire(c)
	if err != nil {
		return nil, nil, err
	}
	envknob.SetNoLogsNoSupport()
	srv := &tsnet.Server{Dir: c.Dir, Hostname: c.Hostname, ControlURL: c.ControlURL, Ephemeral: false,
		UserLogf: func(string, ...any) {}, Logf: func(string, ...any) {}}
	if err := srv.Start(); err != nil {
		release()
		return nil, nil, errors.New("embedded node could not start")
	}
	if _, err := srv.Up(ctx); err != nil {
		_ = srv.Close()
		release()
		return nil, nil, errors.New("embedded node did not reach Running")
	}
	dial := func(ctx context.Context, _, address string) (net.Conn, error) { return srv.Dial(ctx, "tcp", address) }
	return dial, func() { _ = srv.Close(); release() }, nil
}
