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
	)
}
