package plugin

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/pacenote-sim/plugin/internal/pb"
)

// The other process is not trusted to speak JSON. Every payload that crosses
// the transport is decoded behind a check, in both directions, and a payload
// that does not decode is refused with ErrInvalid and a sentence naming which
// one — never a panic, never a zero value read as a fact. These tests hand each
// side something that is not JSON and read the refusal.

// garbage is a plugin process that answers every call with bytes that are not
// JSON, which is what a plugin built against something other than this contract
// looks like from the host's side.
type garbage struct{ pb.UnimplementedPluginServer }

func (garbage) Settings(context.Context, *pb.SettingsRequest) (*pb.SettingsResponse, error) {
	return &pb.SettingsResponse{SettingsJson: []byte("[")}, nil
}

func (garbage) Notify(context.Context, *pb.NotifyRequest) (*pb.NotifyResponse, error) {
	return &pb.NotifyResponse{UsageJson: []byte("{")}, nil
}

func (garbage) Answer(context.Context, *pb.AnswerRequest) (*pb.AnswerResponse, error) {
	return &pb.AnswerResponse{ResponseJson: []byte("{")}, nil
}

func (garbage) Serve(context.Context, *pb.ServeRequest) (*pb.ServeResponse, error) {
	return &pb.ServeResponse{Status: 200, UsageJson: []byte("{")}, nil
}

// unusable is a plugin process whose answers decode and then fail the check
// behind the decoding: a setting the panel cannot render, a spend that cannot
// be, an answer with nothing in it.
type unusable struct{ pb.UnimplementedPluginServer }

func (unusable) Settings(context.Context, *pb.SettingsRequest) (*pb.SettingsResponse, error) {
	return &pb.SettingsResponse{SettingsJson: []byte(`[{"name":"x","label":"X","kind":"colour"}]`)}, nil
}

func (unusable) Notify(context.Context, *pb.NotifyRequest) (*pb.NotifyResponse, error) {
	return &pb.NotifyResponse{UsageJson: []byte(`{"job":"j","input_tokens":-5}`)}, nil
}

func (unusable) Answer(context.Context, *pb.AnswerRequest) (*pb.AnswerResponse, error) {
	return &pb.AnswerResponse{ResponseJson: []byte(`{"kind":"x.y"}`)}, nil
}

// TestTheHostRefusesAnAnswerThatDecodesAndIsStillWrong is the check behind the
// decoding, on the host's side: readable is not the same as usable.
func TestTheHostRefusesAnAnswerThatDecodesAndIsStillWrong(t *testing.T) {
	t.Parallel()

	host := dialRaw(t, unusable{})
	lap := &LapFacts{Number: 1, LapMs: 90000, Kind: LapClean}

	t.Run("a setting the panel cannot render", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Settings(t.Context())
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "cannot render")
	})

	t.Run("a spend that cannot be", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Notify(t.Context(), Event{ID: "e", Kind: EventLapCompleted, Lap: lap})
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "negative")
	})

	t.Run("an answer with nothing in it is no answer", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Answer(t.Context(), Request{ID: "r", Kind: "x.y", From: "t"})
		r.ErrorIs(err, ErrNoAnswer)
	})
}

// dialRaw is dial for a server that speaks protobuf directly, so a test can put
// bytes on the wire that the adapter on this side would never have produced.
func dialRaw(t *testing.T, impl pb.PluginServer) peer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	pb.RegisterPluginServer(srv, impl)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, conn.Close())
		srv.Stop()
		<-done
	})
	p, ok := newClient(conn, nil).(peer)
	require.True(t, ok)
	return p
}

// TestTheHostRefusesAPluginThatDoesNotSpeakJSON is the host's side: every
// answer a plugin gives is decoded behind a check.
func TestTheHostRefusesAPluginThatDoesNotSpeakJSON(t *testing.T) {
	t.Parallel()

	host := dialRaw(t, garbage{})
	lap := &LapFacts{Number: 1, LapMs: 90000, Kind: LapClean}
	ask := Request{ID: "r", Kind: "x.y", From: "t"}

	t.Run("a settings declaration", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Settings(t.Context())
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "settings declaration is not readable")
	})

	t.Run("what an event cost", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Notify(t.Context(), Event{ID: "e", Kind: EventLapCompleted, Lap: lap})
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "cost is not readable")
	})

	t.Run("an answer", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := host.Answer(t.Context(), ask)
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "answer is not readable")
	})

	t.Run("what a page cost", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		srv, ok := host.(Server)
		r.True(ok, "the host's side of the transport serves")
		_, err := srv.ServeHTTP(t.Context(), HTTPRequest{Method: "GET", Path: "/"})
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "cost is not readable")
	})
}

// TestThePluginRefusesAHostThatDoesNotSpeakJSON is the plugin's side, called
// the way the gRPC server calls it: protobuf in, with payloads a real host
// would never send.
func TestThePluginRefusesAHostThatDoesNotSpeakJSON(t *testing.T) {
	t.Parallel()

	s := &server{impl: &fake{}}
	event := []byte(`{"id":"e","kind":"lap.completed","lap":{"number":1,"lap_ms":90000,"kind":"clean"}}`)
	request := []byte(`{"id":"r","kind":"x.y","from":"t"}`)

	t.Run("an event", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := s.Notify(t.Context(), &pb.NotifyRequest{EventJson: []byte("{")})
		r.ErrorIs(fromStatus(err), ErrInvalid)
		r.ErrorContains(err, "event is not readable")
	})

	t.Run("the settings beside an event", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := s.Notify(t.Context(), &pb.NotifyRequest{EventJson: event, ValuesJson: []byte("{")})
		r.ErrorIs(fromStatus(err), ErrInvalid)
		r.ErrorContains(err, "settings are not readable")
	})

	t.Run("a request", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := s.Answer(t.Context(), &pb.AnswerRequest{RequestJson: []byte("{")})
		r.ErrorIs(fromStatus(err), ErrInvalid)
		r.ErrorContains(err, "request is not readable")
	})

	t.Run("the settings beside a request", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := s.Answer(t.Context(), &pb.AnswerRequest{RequestJson: request, ValuesJson: []byte("{")})
		r.ErrorIs(fromStatus(err), ErrInvalid)
		r.ErrorContains(err, "settings are not readable")
	})

	t.Run("the settings beside a page request", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := s.Serve(t.Context(), &pb.ServeRequest{Method: "GET", Path: "/", ValuesJson: []byte("{")})
		r.ErrorIs(fromStatus(err), ErrInvalid)
		r.ErrorContains(err, "settings are not readable")
	})
}
