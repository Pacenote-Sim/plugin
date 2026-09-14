package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// The go-plugin adapter, which is the one piece both processes register from
// the same file. A mismatch here is a plugin that starts and then cannot be
// dispensed, which is a worse failure than one that will not start at all.
func TestTheAdapterBothSidesRegister(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	// What a host hands go-plugin.
	set := ClientSet()
	r.Len(set, 1)
	r.Contains(set, DispenseKey, "the host dispenses under a different key than the plugin serves")

	// The plugin's half, registered on a real gRPC server.
	srv := grpc.NewServer()
	r.NoError((&grpcPlugin{impl: &fake{}}).GRPCServer(nil, srv))
	r.Contains(srv.GetServiceInfo(), "pacenote.plugin.v1.Plugin")

	// The host's half, over a real connection. It is not dialled: what is
	// asserted is the type the host is handed, not that anything answers.
	conn, err := grpc.NewClient("passthrough:///unused",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	r.NoError(err)
	t.Cleanup(func() { _ = conn.Close() })

	got, err := (&grpcPlugin{}).GRPCClient(context.Background(), nil, conn)
	r.NoError(err)
	_, isPlugin := got.(Plugin)
	r.True(isPlugin, "the host was handed something that is not a Plugin")
	_, isServer := got.(Server)
	r.True(isServer, "the host cannot ask it to serve a route")
}

// The handshake is what the host matches on, and both sides read it from here.
func TestTheHandshakeIsTheInterfaceVersion(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.Equal(uint(InterfaceVersion), Handshake.ProtocolVersion)
	r.Equal(MagicCookieKey, Handshake.MagicCookieKey)
	r.Equal(MagicCookieValue, Handshake.MagicCookieValue)
}
