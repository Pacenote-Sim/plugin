package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// Plugin is the whole contract. Implement these three methods, call [Serve] in
// main, put a [Manifest] beside the binary, and the host can run it.
//
// Every method is called on its own goroutine and several may be in flight at
// once, so an implementation has to be safe for concurrent use. Every method is
// given a context with the host's deadline on it, and is expected to respect
// it: the host stops waiting when the deadline passes whatever the plugin does,
// so work that carries on afterwards is work nobody will read.
type Plugin interface {
	// Settings declares what the operator has to configure. The host asks once
	// when the plugin starts, stores the answer, and renders the form from it.
	//
	// It must not depend on configuration — a fresh installation has none — and
	// it must be stable: a setting that appears and disappears between calls is
	// a form that loses what the operator typed.
	Settings(ctx context.Context) ([]Setting, error)

	// Notify is told that something happened. The host does not wait for this
	// before carrying on with its own work, so taking a while here costs
	// nothing but the plugin's own timeliness.
	//
	// The returned [Usage] is what the event cost. Return the zero value when
	// it cost nothing. An error is logged against the plugin and nothing else:
	// there is no retry, because an event that matters enough to retry is a
	// request and should be one.
	Notify(ctx context.Context, e Event) (Usage, error)

	// Answer is asked for something, with the host waiting. Return [ErrNoAnswer]
	// when there is nothing worth saying — that is a normal outcome and the
	// caller has its own fallback — and [ErrNotConfigured] when the operator
	// has not filled something in.
	//
	// Missing the deadline is the one unforgivable failure: the caller is a
	// driver at speed, and an answer that arrives after the corner is worse
	// than no answer. Watch ctx.Done and give up.
	Answer(ctx context.Context, r Request) (Response, error)
}

// DispenseKey is the name the single plugin implementation is registered and
// dispensed under. There is one implementation per process on purpose: a
// process that serves two plugins cannot crash for one of them.
const DispenseKey = "pacenote"

// Handshake is what the host and the plugin exchange before either speaks. The
// magic cookie is not a secret and is not security; it is what makes a program
// started by mistake fail immediately and legibly instead of hanging.
//
// ProtocolVersion is [InterfaceVersion], so go-plugin refuses a mismatch as a
// backstop. The host checks the manifest first, because that check can name
// both versions and say what to do, and a handshake failure cannot.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  InterfaceVersion,
	MagicCookieKey:   MagicCookieKey,
	MagicCookieValue: MagicCookieValue,
}

// Serve runs impl as a plugin and blocks until the host closes the connection
// or the process is killed. It is the whole of a plugin's main:
//
//	func main() { plugin.Serve(&myPlugin{}) }
//
// Nothing else may be written to standard output. The handshake is on it, and a
// stray fmt.Println breaks the connection before the host can say why. Standard
// error is free, and the host captures it: it is what the panel shows when a
// plugin fails to start.
func Serve(impl Plugin) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         goplugin.PluginSet{DispenseKey: &grpcPlugin{impl: impl}},
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}

// ClientSet is the plugin set a host hands go-plugin. It is here rather than in
// the host so that both sides register the same thing from the same file.
func ClientSet() goplugin.PluginSet {
	return goplugin.PluginSet{DispenseKey: &grpcPlugin{}}
}

// grpcPlugin is the go-plugin adapter. It is gRPC and not net/rpc because a
// plugin written in another language is a thing the interface should not rule
// out, and net/rpc rules it out on its own.
type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	impl Plugin
}

// GRPCServer registers the plugin's side. It runs in the plugin's process.
func (p *grpcPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	registerServer(s, p.impl)
	return nil
}

// GRPCClient builds the host's side. It runs in the server's process, and what
// it returns is a [Plugin] that happens to be another process.
func (p *grpcPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return newClient(c), nil
}
