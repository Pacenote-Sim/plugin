package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

// The second channel: a plugin asking the host, over go-plugin's broker on the
// same connection the host uses to call the plugin. These run both sides in one
// process on go-plugin's own test connection, which is a real multiplexed
// transport and not a shortcut — the broker is exactly what is being tested.

// askingFake is a plugin that asks: it keeps the host it was connected to and
// answers questions by asking one of its own when told to.
type askingFake struct {
	fake
	mu   sync.Mutex
	host Host
	// relay, when set, is what Answer asks the host in turn.
	relay string
}

func (a *askingFake) Connected(h Host) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.host = h
}

func (a *askingFake) connected() Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host
}

func (a *askingFake) Answer(ctx context.Context, r Request) (Response, error) {
	if a.relay == "" {
		return a.fake.Answer(ctx, r)
	}
	out, err := a.connected().Ask(ctx, a.relay, r.Payload)
	if err != nil {
		return Response{}, err
	}
	return Response{Payload: out}, nil
}

// recordingHost is the server's side under the test's control.
type recordingHost struct {
	mu     sync.Mutex
	asks   []asked
	answer func(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, error)
}

type asked struct {
	kind    string
	payload string
	hops    int
}

func (h *recordingHost) Ask(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	h.mu.Lock()
	h.asks = append(h.asks, asked{kind: kind, payload: string(payload), hops: Hops(ctx)})
	h.mu.Unlock()
	return h.answer(ctx, kind, payload)
}

func (h *recordingHost) seen() []asked {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]asked(nil), h.asks...)
}

// echoHost answers every question with what it was asked.
func echoHost() *recordingHost {
	return &recordingHost{answer: func(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"kind":"` + kind + `","got":` + string(orEmptyObject(payload)) + `}`), nil
	}}
}

// brokered puts impl behind go-plugin's own in-process connection, broker
// included, and hands back the host's side.
func brokered(t *testing.T, impl Plugin) Plugin {
	t.Helper()
	client, server := goplugin.TestPluginGRPCConn(t, true, map[string]goplugin.Plugin{
		DispenseKey: &grpcPlugin{impl: impl},
	})
	// The order go-plugin's own tests use: the server first, then the client.
	// The other way round races the shutdown the client asks the server for.
	t.Cleanup(func() {
		server.Stop()
		_ = client.Close()
	})
	raw, err := client.Dispense(DispenseKey)
	require.NoError(t, err)
	p, ok := raw.(Plugin)
	require.True(t, ok)
	return p
}

func TestAPluginAsksTheHostThroughTheBroker(t *testing.T) {
	t.Parallel()

	t.Run("an asker is connected before its settings are read, and its question reaches the host", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		asker := &askingFake{}
		host := echoHost()
		p := brokered(t, asker)
		detach, ok := Attach(p, host)
		t.Cleanup(detach)
		r.True(ok, "a real connection can be reached back from")

		_, err := p.Settings(t.Context())
		r.NoError(err)
		r.NotNil(asker.connected(), "Connected runs inside the first Settings call")

		out, err := asker.connected().Ask(t.Context(), "drivers.lookup", json.RawMessage(`{"slug":"ana"}`))
		r.NoError(err)
		r.JSONEq(`{"kind":"drivers.lookup","got":{"slug":"ana"}}`, string(out))

		seen := host.seen()
		r.Len(seen, 1)
		r.Equal("drivers.lookup", seen[0].kind)
		r.Equal(`{"slug":"ana"}`, seen[0].payload, "the payload is not re-encoded on the way") //nolint:testifylint // byte equality is the point.
		r.Zero(seen[0].hops, "a question asked outside an answer starts at zero")
	})

	t.Run("the depth of the question being answered travels with the next question", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		asker := &askingFake{relay: "drivers.lookup"}
		host := echoHost()
		p := brokered(t, asker)
		detach, ok := Attach(p, host)
		t.Cleanup(detach)
		r.True(ok)
		_, err := p.Settings(t.Context())
		r.NoError(err)

		answerer, ok := p.(Answerer)
		r.True(ok)
		res, err := answerer.Answer(t.Context(), Request{
			ID: "r-1", Kind: "relay.it", From: "somebody", Hops: 2, Payload: json.RawMessage(`{"slug":"ana"}`),
		})
		r.NoError(err)
		r.JSONEq(`{"kind":"drivers.lookup","got":{"slug":"ana"}}`, string(res.Payload))

		seen := host.seen()
		r.Len(seen, 1)
		r.Equal(2, seen[0].hops, "the host is told how deep the question already was, so it can count")
	})

	t.Run("the host's refusal reaches the asker as the error it is", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		asker := &askingFake{}
		host := &recordingHost{answer: func(_ context.Context, kind string, _ json.RawMessage) (json.RawMessage, error) {
			switch kind {
			case "secret.thing":
				return nil, ErrNotAllowed
			case "gone.thing":
				return nil, ErrUnavailable
			default:
				return nil, errors.New("the drivers plugin fell over")
			}
		}}
		p := brokered(t, asker)
		detach, ok := Attach(p, host)
		t.Cleanup(detach)
		r.True(ok)
		_, err := p.Settings(t.Context())
		r.NoError(err)

		_, err = asker.connected().Ask(t.Context(), "secret.thing", nil)
		r.ErrorIs(err, ErrNotAllowed)
		_, err = asker.connected().Ask(t.Context(), "gone.thing", nil)
		r.ErrorIs(err, ErrUnavailable)
		_, err = asker.connected().Ask(t.Context(), "other.thing", nil)
		r.ErrorContains(err, "fell over", "the other plugin's own words survive")
	})

	t.Run("a plugin that does not ask is not connected, whatever the host offered", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		p := brokered(t, &fake{})
		detach, ok := Attach(p, echoHost())
		t.Cleanup(detach)
		r.True(ok)
		_, err := p.Settings(t.Context())
		r.NoError(err, "a host offering a channel to a plugin that does not want one is not an error")
	})

	t.Run("a host that offers nothing leaves an asker with nobody to ask", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		asker := &askingFake{}
		p := brokered(t, asker)
		_, err := p.Settings(t.Context())
		r.NoError(err)
		r.Nil(asker.connected())
	})

	t.Run("what cannot be reached back from says so", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		detach, ok := Attach(&fake{}, echoHost())
		r.False(ok, "a fake in a test is not a connection")
		detach()

		detach, ok = Attach(brokered(t, &fake{}), nil)
		r.False(ok, "and nothing to attach is nothing attached")
		detach()
		detach()
	})
}

// The depth helpers on their own: zero outside an answer, whatever was put in.
func TestHopsTravelInTheContext(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.Zero(Hops(t.Context()))
	r.Equal(2, Hops(withHops(t.Context(), 2)))
}
