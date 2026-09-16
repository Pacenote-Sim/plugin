package plugin_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if a test leaves a goroutine behind. The transport
// starts a gRPC server and a connection per test, and the shutdown path is the
// thing most likely to strand one.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// gRPC's own connection bookkeeping outlives ClientConn.Close by a few
		// scheduler turns. It is the library's goroutine, not ours, and it is
		// the standard exception for tests that dial one.
		goleak.IgnoreTopFunction("google.golang.org/grpc.(*ClientConn).WithStateChangeHandler"),
		// go-plugin's in-process test connection runs a broker on both sides,
		// and the broker's bookkeeping — the knock listener behind Accept, the
		// timeout it keeps for a stream, gRPC's callback serialiser under it —
		// outlives Stop and Close by design. They are the library's goroutines,
		// the same ones the server's own plugin tests ignore.
		goleak.IgnoreAnyFunction("github.com/hashicorp/go-plugin.(*GRPCBroker).listenForKnocks"),
		goleak.IgnoreAnyFunction("github.com/hashicorp/go-plugin.(*GRPCBroker).timeoutWait"),
		goleak.IgnoreAnyFunction("github.com/hashicorp/go-plugin.(*GRPCBroker).Run"),
		goleak.IgnoreAnyFunction("google.golang.org/grpc/internal/grpcsync.(*CallbackSerializer).run"),
	)
}
