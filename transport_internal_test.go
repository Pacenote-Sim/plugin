package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// The transport is tested in the package rather than beside it, because what is
// being tested is the pair — what one side encodes and the other decodes — and
// both halves are unexported on purpose. A host gets them through [ClientSet],
// which is go-plugin's business and not this contract's.

// fake is a plugin under the test's control.
type fake struct {
	settings []Setting
	settErr  error

	notify   func(context.Context, Event) (Usage, error)
	answer   func(context.Context, Request) (Response, error)
	lastEvt  chan Event
	lastReq  chan Request
	answered Response

	serve    func(context.Context, HTTPRequest) (HTTPResponse, error)
	lastServ chan HTTPRequest
	served   HTTPResponse
}

func (f *fake) ServeHTTP(ctx context.Context, r HTTPRequest) (HTTPResponse, error) {
	if f.lastServ != nil {
		f.lastServ <- r
	}
	if f.serve != nil {
		return f.serve(ctx, r)
	}
	return f.served, nil
}

// noRoutes implements [Plugin] and nothing else, for the case a manifest
// promises a route and the binary cannot serve one.
type noRoutes struct{}

func (noRoutes) Settings(context.Context) ([]Setting, error)  { return nil, nil }
func (noRoutes) Notify(context.Context, Event) (Usage, error) { return Usage{}, nil }

func (f *fake) Settings(context.Context) ([]Setting, error) { return f.settings, f.settErr }

func (f *fake) Notify(ctx context.Context, e Event) (Usage, error) {
	if f.lastEvt != nil {
		f.lastEvt <- e
	}
	if f.notify != nil {
		return f.notify(ctx, e)
	}
	return Usage{}, nil
}

func (f *fake) Answer(ctx context.Context, r Request) (Response, error) {
	if f.lastReq != nil {
		f.lastReq <- r
	}
	if f.answer != nil {
		return f.answer(ctx, r)
	}
	return f.answered, nil
}

// dial puts impl behind a real gRPC connection and hands back the [Plugin] the
// host would hold. It is a loopback socket rather than an in-memory pipe so
// that the encoding is exercised end to end, which is the whole point.
// peer is every side of a connection at once, which is what a test wants and
// what the host's single type has: it is told, asked and served through.
type peer interface {
	Plugin
	Answerer
	Server
}

func dial(t *testing.T, impl Plugin) peer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	registerServer(srv, impl, nil)
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
	require.True(t, ok, "the host's side is told, asked and served through")
	return p
}

// TestSettingsCrossTheWire covers the declaration a host reads at startup.
func TestSettingsCrossTheWire(t *testing.T) {
	t.Parallel()

	t.Run("a declaration arrives whole", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		want := []Setting{
			{Name: "api_key", Label: "Vendor key", Help: "Sealed by the core.", Kind: KindSecret, Required: true},
			{Name: "mode", Label: "Mode", Kind: KindChoice, Default: "fast", Choices: []Choice{{Value: "fast", Label: "Fast", Note: "cheap"}}},
		}
		got, err := dial(t, &fake{settings: want}).Settings(t.Context())
		r.NoError(err)
		r.Equal(want, got)
	})

	t.Run("a declaration the panel cannot render is refused at the boundary", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, err := dial(t, &fake{settings: []Setting{{Name: "x", Label: "X", Kind: "colour"}}}).Settings(t.Context())
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "cannot render")
	})

	t.Run("a plugin that declares nothing is not an error", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		got, err := dial(t, &fake{}).Settings(t.Context())
		r.NoError(err)
		r.Empty(got)
	})
}

// TestEventCrossesTheWire is the fire-and-forget half, and the place where the
// separation of facts from credentials is proved.
func TestEventCrossesTheWire(t *testing.T) {
	t.Parallel()

	t.Run("facts, settings and credentials all arrive", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		seen := make(chan Event, 1)
		f := &fake{
			lastEvt: seen,
			notify: func(_ context.Context, e Event) (Usage, error) {
				return Usage{Job: string(e.Kind), Model: "m", InputTokens: 100, OutputTokens: 20}, nil
			},
		}
		sent := Event{
			ID:      "e-1",
			Kind:    EventLapCompleted,
			At:      time.Now().UTC().Truncate(time.Second),
			Driver:  Driver{ID: 7, Slug: "ana", Name: "Ana"},
			Session: Session{StintID: "s-1", Sim: "iracing", Track: "Barcelona", TrackID: "barcelona gp", Car: "296", Type: SessionRace},
			Lap: &LapFacts{
				Number: 14, LapMs: 91240, Kind: LapClean, DeltaMs: 840, Reference: "your best lap",
				Corners: json.RawMessage(`[{"turn":4,"apex_pct":312,"apex_kmh":112,"ref_apex_kmh":121,` +
					`"deficit_kmh":9,"brake_at_apex":31,"throttle_lag":14,"pattern":"early_apex"}]`),
				Position:  &Position{ClassPos: 3, GapAheadMs: 1240},
				SpokenLap: "one minute 31.2 seconds",
			},
			Settings:     Values{"greeting": "Right"},
			Secrets:      Secrets{"api_key": NewSecret("sk-ant-zzz")},
			TokenCeiling: 800,
		}

		use, err := dial(t, f).Notify(t.Context(), sent)
		r.NoError(err)
		r.Equal(int64(120), use.Total())

		got := <-seen
		r.Equal(sent.ID, got.ID)
		r.Equal(sent.Kind, got.Kind)
		r.Equal(sent.Driver, got.Driver)
		r.Equal(sent.Session.TrackID, got.Session.TrackID)
		r.Equal(sent.Lap, got.Lap)
		r.Equal(sent.Settings, got.Settings)
		r.Equal(800, got.TokenCeiling)

		key, ok := got.Secrets.Get("api_key")
		r.True(ok, "the credential has to arrive, or the plugin cannot call anything")
		r.Equal("sk-ant-zzz", key.Value())
	})

	t.Run("a plugin reporting an impossible spend is refused", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		f := &fake{notify: func(context.Context, Event) (Usage, error) {
			return Usage{Job: "j", InputTokens: -5}, nil
		}}
		_, err := dial(t, f).Notify(t.Context(), Event{ID: "e", Kind: EventStintFinished, Stint: &StintFacts{}})
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "negative")
	})

	t.Run("an event the host should never have sent is refused by the plugin's side", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, err := dial(t, &fake{}).Notify(t.Context(), Event{ID: "e", Kind: EventLapCompleted})
		r.ErrorIs(err, ErrInvalid)
		r.ErrorContains(err, "no lap facts")
	})
}

// TestRequestCrossesTheWire is the waiting half: one plugin's question, put by
// the host, answered by another.
func TestRequestCrossesTheWire(t *testing.T) {
	t.Parallel()

	t.Run("an answer comes back with what it cost", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		f := &fake{answered: Response{
			Payload: json.RawMessage(`{"known":true,"name":"Ana Ruiz"}`),
			Usage:   Usage{Job: "drivers.lookup", Model: "none", InputTokens: 300, OutputTokens: 12},
		}}
		got, err := dial(t, f).Answer(t.Context(), Request{
			ID: "r-1", Kind: "drivers.lookup", From: "payments", Payload: json.RawMessage(`{"slug":"ana"}`),
		})
		r.NoError(err)
		r.JSONEq(`{"known":true,"name":"Ana Ruiz"}`, string(got.Payload))
		r.Equal(RequestKind("drivers.lookup"), got.Kind, "the kind is filled in from the request when the plugin leaves it out")
		r.Equal(int64(312), got.Usage.Total())
	})

	t.Run("the question arrives as it was asked, sender and all", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		seen := make(chan Request, 1)
		f := &fake{lastReq: seen, answered: Response{Payload: json.RawMessage(`true`)}}
		_, err := dial(t, f).Answer(t.Context(), Request{
			ID: "r-1", Kind: "drivers.lookup", From: "payments", Hops: 2,
			Payload: json.RawMessage(`{"slug":"ana"}`),
			Secrets: Secrets{"api_key": NewSecret("sk-ant-zzz")},
		})
		r.NoError(err)

		got := <-seen
		r.Equal("payments", got.From)
		r.Equal(2, got.Hops)
		r.Equal(`{"slug":"ana"}`, string(got.Payload), "not re-encoded on the way") //nolint:testifylint // byte equality is the point.
		key, ok := got.Secrets.Get("api_key")
		r.True(ok, "the credential reaches the answer path too")
		r.Equal("sk-ant-zzz", key.Value())
	})

	t.Run("a plugin that does not answer says so", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, err := dial(t, noRoutes{}).Answer(t.Context(), Request{ID: "r-1", Kind: "quiet.thing", From: "asker"})
		r.ErrorIs(err, ErrUnsupported)
		r.ErrorContains(err, "does not answer them")
	})
}

// TestSentinelsSurviveTheProcessBoundary is what lets a caller branch on a
// failure that happened in another process. Without it every outcome is "the
// plugin said something", and the fallback path cannot tell "nothing to say"
// from "it is broken".
func TestSentinelsSurviveTheProcessBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		give error
		want error
		text string
	}{
		{name: "nothing worth saying", give: ErrNoAnswer, want: ErrNoAnswer},
		{name: "not a job this plugin does", give: ErrUnsupported, want: ErrUnsupported},
		{name: "the operator has not filled it in", give: ErrNotConfigured, want: ErrNotConfigured},
		{name: "somebody's mistake", give: ErrInvalid, want: ErrInvalid},
		{name: "a question the manifest did not allow", give: ErrNotAllowed, want: ErrNotAllowed},
		{name: "the plugin that would answer is not there", give: ErrUnavailable, want: ErrUnavailable},
		{
			name: "the plugin's own words survive",
			give: errors.New("the vendor answered 429 and would not say when to retry"),
			text: "the vendor answered 429",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			f := &fake{answer: func(context.Context, Request) (Response, error) {
				return Response{}, tc.give
			}}
			_, err := dial(t, f).Answer(t.Context(), Request{ID: "r", Kind: "coach.cue", From: "host"})
			r.Error(err)
			if tc.want != nil {
				r.ErrorIs(err, tc.want)
			}
			if tc.text != "" {
				r.ErrorContains(err, tc.text)
			}
		})
	}
}

// TestDeadlineCrossesTheWire proves the deadline is the caller's and not the
// plugin's. A plugin that ignores it is not asked twice; the caller stops
// waiting and falls back.
func TestDeadlineCrossesTheWire(t *testing.T) {
	t.Parallel()

	t.Run("the caller stops waiting on a plugin that hangs", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		f := &fake{answer: func(ctx context.Context, _ Request) (Response, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return Response{Payload: json.RawMessage(`"too late"`)}, nil
		}}

		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := dial(t, f).Answer(ctx, Request{ID: "r", Kind: "coach.cue", From: "host"})
		r.ErrorIs(err, context.DeadlineExceeded)
		r.Less(time.Since(start), 2*time.Second)
	})

	t.Run("a well-behaved plugin sees the deadline on its own context", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		seen := make(chan time.Time, 1)
		f := &fake{answer: func(ctx context.Context, _ Request) (Response, error) {
			d, _ := ctx.Deadline()
			seen <- d
			return Response{Payload: json.RawMessage(`"in time"`)}, nil
		}}

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		_, err := dial(t, f).Answer(ctx, Request{ID: "r", Kind: "coach.cue", From: "host"})
		r.NoError(err)

		got := <-seen
		r.False(got.IsZero(), "a plugin with no deadline cannot decide how hard to try")
	})
}

// The host's side of a plugin connection has to satisfy [Server], because that
// is how the host decides whether a plugin serves: it type-asserts. A method
// renamed without this would compile, and every plugin route would answer "this
// plugin does not serve one".
var _ Server = (*client)(nil)

// TestARequestCrossesTheWire covers a plugin's own route, both directions.
func TestARequestCrossesTheWire(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	f := &fake{
		lastServ: make(chan HTTPRequest, 1),
		served: HTTPResponse{
			Status: 201,
			Header: http.Header{
				"Content-Type": {"text/html; charset=utf-8"},
				"Set-Cookie":   {"pacenote_p_results_a=1", "pacenote_p_results_b=2"},
				"X-Empty":      {},
			},
			Body:    []byte("<p>ok</p>"),
			SignIn:  "ana-lopez",
			SignOut: true,
			Usage:   Usage{Job: "webhook.verify", Model: "none", InputTokens: 3, OutputTokens: 4},
		},
	}
	host, ok := dial(t, f).(Server)
	r.True(ok, "the host's side of the connection does not serve")

	sent := HTTPRequest{
		Method: http.MethodPost,
		Path:   "/webhook",
		Query:  "a=1&b=2",
		Header: http.Header{
			"X-Signature": {"abc"},
			"Accept":      {"text/html", "text/plain"},
			"X-Dropped":   {}, // no values: the two sides must agree it is absent
		},
		Body:     []byte(`{"paid":true}`),
		Prefix:   "/plugin/results",
		BaseURL:  "https://pacenote.example.com",
		Settings: Values{"mode": "live"},
		Caller: Caller{
			DriverSlug: "ana-lopez",
			DriverName: "Ana López",
			AdminEmail: "ana@example.com",
			Remote:     "203.0.113.7",
		},
	}
	got, err := host.ServeHTTP(t.Context(), sent)
	r.NoError(err)

	// What the plugin was handed.
	arrived := <-f.lastServ
	r.Equal(sent.Method, arrived.Method)
	r.Equal(sent.Path, arrived.Path)
	r.Equal(sent.Query, arrived.Query)
	r.Equal(sent.Body, arrived.Body)
	r.Equal(sent.Prefix, arrived.Prefix)
	r.Equal(sent.BaseURL, arrived.BaseURL)
	r.Equal(sent.Settings, arrived.Settings)
	r.Equal(sent.Caller, arrived.Caller)
	r.Equal([]string{"text/html", "text/plain"}, arrived.Header.Values("Accept"),
		"a header with several values arrived as one")
	r.Empty(arrived.Header.Values("X-Dropped"),
		"a header with no values came back as something")

	// And what it answered.
	r.Equal(201, got.Status)
	r.Equal("<p>ok</p>", string(got.Body))
	r.Equal("ana-lopez", got.SignIn)
	r.True(got.SignOut)
	r.Equal([]string{"pacenote_p_results_a=1", "pacenote_p_results_b=2"}, got.Header.Values("Set-Cookie"))
	r.Equal(int64(3), got.Usage.InputTokens)
	r.Equal(int64(4), got.Usage.OutputTokens)
	r.Equal("webhook.verify", got.Usage.Job)
}

// A manifest that asked for a route and a binary that cannot serve one. The
// host has to be able to tell that apart from a page that failed.
func TestAPluginThatDeclaredARouteAndDoesNotServe(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	host, ok := dial(t, noRoutes{}).(Server)
	r.True(ok)

	_, err := host.ServeHTTP(t.Context(), HTTPRequest{Method: http.MethodGet, Path: "/"})
	// ErrUnsupported and not a transport code: the host branches on the
	// sentinel, and "this plugin does not serve" has to be distinguishable
	// from a route that failed.
	r.ErrorIs(err, ErrUnsupported)
}

// An error a plugin returns from its own route comes back as an error, not as
// a page.
func TestAPluginWhoseRouteFails(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	f := &fake{serve: func(context.Context, HTTPRequest) (HTTPResponse, error) {
		return HTTPResponse{}, errors.New("the provider is down")
	}}
	host, ok := dial(t, f).(Server)
	r.True(ok)

	_, err := host.ServeHTTP(t.Context(), HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.Error(err)
	r.Contains(err.Error(), "the provider is down")
}

// A cost a plugin reports for a page is checked on the way out, like every
// other cost.
func TestARouteThatReportsACostThatIsNotOne(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	f := &fake{served: HTTPResponse{Status: 200, Usage: Usage{InputTokens: -1}}}
	host, ok := dial(t, f).(Server)
	r.True(ok)

	_, err := host.ServeHTTP(t.Context(), HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.Error(err)
}

// A negative status is not a status. It is clamped rather than wrapping into a
// number nobody meant.
func TestARouteThatAnswersANegativeStatus(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	f := &fake{served: HTTPResponse{Status: -7, Body: []byte("x")}}
	host, ok := dial(t, f).(Server)
	r.True(ok)

	got, err := host.ServeHTTP(t.Context(), HTTPRequest{Method: http.MethodGet, Path: "/"})
	r.NoError(err)
	r.Equal(0, got.Status, "a negative status wrapped instead of being refused")
}

// The edges of the two error translations, which are what the host branches on
// and are only reachable here.
func TestTranslatingErrorsAtTheEdges(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.NoError(toStatus(nil))
	r.NoError(fromStatus(nil))

	// Something that is not a gRPC status travels as itself.
	plain := errors.New("not a status")
	r.Equal(plain, fromStatus(plain))

	// A cancellation arrives as context.Canceled, because that is what the
	// host's own handling is written against.
	r.ErrorIs(fromStatus(status.Error(codes.Canceled, "gone")), context.Canceled)
	r.ErrorIs(fromStatus(status.Error(codes.DeadlineExceeded, "slow")), context.DeadlineExceeded)
}

// A peer that leaves a document unset is not a peer that is broken. A plugin in
// another language sending nothing where JSON was expected has to round-trip.
func TestAPeerThatSendsNothingWhereADocumentWasExpected(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.Equal([]byte("{}"), orEmptyObject(nil))
	r.Equal([]byte("[]"), orEmptyArray(nil))
	r.Equal([]byte(`{"a":1}`), orEmptyObject([]byte(`{"a":1}`)))
	r.Equal([]byte(`[1]`), orEmptyArray([]byte(`[1]`)))
}

// The wire, measured.
//
// Every call a host makes crosses these, so an allocation here is an allocation
// per lap, per request and per page. They are benchmarks rather than a budget
// in a test: what they are for is noticing a change, not failing a build.

func BenchmarkAnEventCrossingTheWire(b *testing.B) {
	host := benchHost(b, &fake{})
	e := Event{
		ID:       "e1",
		Kind:     EventLapCompleted,
		Lap:      &LapFacts{Number: 12, LapMs: 95_400},
		Settings: Values{"mode": "live", "voice": "calm"},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := host.Notify(context.Background(), e); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkARequestCrossingTheWire(b *testing.B) {
	host := benchHost(b, &fake{answered: Response{Payload: json.RawMessage(`"box this lap"`)}})
	r := Request{
		ID:       "r1",
		Kind:     "coach.cue",
		From:     "host",
		Payload:  json.RawMessage(`{"lap":12,"lap_ms":95400}`),
		Settings: Values{"mode": "live"},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := host.Answer(context.Background(), r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAPageCrossingTheWire(b *testing.B) {
	f := &fake{served: HTTPResponse{Status: 200, Body: []byte("<p>ok</p>")}}
	host, ok := benchHost(b, f).(Server)
	if !ok {
		b.Fatal("the host's side does not serve")
	}
	r := HTTPRequest{
		Method:   "GET",
		Path:     "/standings",
		Header:   http.Header{"Accept": {"text/html"}, "Cookie": {"pacenote_p_results_a=1"}},
		Prefix:   "/plugin/results",
		BaseURL:  "https://pacenote.example.com",
		Settings: Values{"mode": "live"},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := host.ServeHTTP(context.Background(), r); err != nil {
			b.Fatal(err)
		}
	}
}

// The header conversion on its own, which is the part that is this module's
// own code rather than gRPC's.
func BenchmarkHeadersBothWays(b *testing.B) {
	h := http.Header{
		"Accept":       {"text/html", "text/plain"},
		"Cookie":       {"pacenote_p_results_a=1"},
		"X-Signature":  {"abcdef0123456789"},
		"Content-Type": {"application/json"},
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := headerFrom(headersTo(h)); len(got) != len(h) {
			b.Fatalf("round trip lost a header: %d != %d", len(got), len(h))
		}
	}
}

// benchHost is dial for a benchmark, which cannot take a *testing.T.
func benchHost(b *testing.B, impl Plugin) peer {
	b.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(b, err)

	srv := grpc.NewServer()
	registerServer(srv, impl, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(b, err)

	b.Cleanup(func() {
		require.NoError(b, conn.Close())
		srv.Stop()
		<-done
	})
	p, ok := newClient(conn, nil).(peer)
	if !ok {
		b.Fatal("the host's side does not implement every side")
	}
	return p
}
