package plugin_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestEventValidate covers what the host refuses to dispatch, so that a plugin
// author can trust the shape of what arrives.
func TestEventValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		event   plugin.Event
		wantErr string
	}{
		{
			name:  "a lap event with lap facts is accepted",
			event: plugin.Event{ID: "e1", Kind: plugin.EventLapCompleted, Lap: &plugin.LapFacts{Number: 14}},
		},
		{
			name:  "a stint event with stint facts is accepted",
			event: plugin.Event{ID: "e1", Kind: plugin.EventStintFinished, Stint: &plugin.StintFacts{Laps: 12}},
		},
		{
			name:    "an event with no id cannot be deduplicated",
			event:   plugin.Event{Kind: plugin.EventLapCompleted, Lap: &plugin.LapFacts{}},
			wantErr: "cannot be deduplicated",
		},
		{
			name:    "an event this version does not carry is refused",
			event:   plugin.Event{ID: "e1", Kind: "driver.paired"},
			wantErr: "is not an event this interface version carries",
		},
		{
			name:    "a lap event with no lap facts says nothing",
			event:   plugin.Event{ID: "e1", Kind: plugin.EventLapCompleted},
			wantErr: "with no lap facts",
		},
		{
			name:    "a stint event with no stint facts says nothing",
			event:   plugin.Event{ID: "e1", Kind: plugin.EventStintFinished},
			wantErr: "with no stint facts",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			err := tc.event.Validate()
			if tc.wantErr == "" {
				r.NoError(err)
				return
			}
			r.ErrorIs(err, plugin.ErrInvalid)
			r.ErrorContains(err, tc.wantErr)
		})
	}
}

// TestRequestValidate covers what a plugin refuses to answer. A request is one
// plugin asking another through the host, so the shape the host is trusted to
// produce — an id, a kind spelled for a plugin, a sender — is what is checked.
func TestRequestValidate(t *testing.T) {
	t.Parallel()

	good := plugin.Request{ID: "r1", Kind: "drivers.lookup", From: "payments", Payload: json.RawMessage(`{"slug":"ana"}`)}

	cases := []struct {
		name    string
		mutate  func(*plugin.Request)
		wantErr string
	}{
		{name: "a question with a sender, a kind and a payload is accepted", mutate: func(*plugin.Request) {}},
		{name: "a question with no payload is still a question", mutate: func(r *plugin.Request) { r.Payload = nil }},
		{name: "as many hops as are allowed is still allowed", mutate: func(r *plugin.Request) { r.Hops = plugin.MaxHops }},
		{
			name:    "a request with no id cannot be traced",
			mutate:  func(r *plugin.Request) { r.ID = "" },
			wantErr: "cannot be traced",
		},
		{
			name:    "a kind with no plugin in it is refused",
			mutate:  func(r *plugin.Request) { r.Kind = "lookup" },
			wantErr: "spell it <plugin>.<what>",
		},
		{
			name:    "a kind spelled with capitals is refused",
			mutate:  func(r *plugin.Request) { r.Kind = "Drivers.Lookup" },
			wantErr: "is not a request kind",
		},
		{
			name:    "a request from nobody is refused",
			mutate:  func(r *plugin.Request) { r.From = "" },
			wantErr: "from nobody",
		},
		{
			name:    "a question that has gone round in a circle is refused",
			mutate:  func(r *plugin.Request) { r.Hops = plugin.MaxHops + 1 },
			wantErr: "gone round in a circle",
		},
		{
			name:    "a payload that is not JSON is refused",
			mutate:  func(r *plugin.Request) { r.Payload = json.RawMessage(`{not json`) },
			wantErr: "payload is not JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			req := good
			tc.mutate(&req)
			err := req.Validate()
			if tc.wantErr == "" {
				r.NoError(err)
				return
			}
			r.ErrorIs(err, plugin.ErrInvalid)
			r.ErrorContains(err, tc.wantErr)
		})
	}
}

// TestResponseValidate covers what an asker refuses to use. The important case
// is the empty one: an asker has to be able to tell "nothing to say" from "here
// is nothing".
func TestResponseValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		response plugin.Response
		wantErr  error
		errText  string
	}{
		{
			name:     "a payload is an answer",
			response: plugin.Response{Kind: "drivers.lookup", Payload: json.RawMessage(`{"known":true}`)},
		},
		{
			name:     "an empty answer is no answer",
			response: plugin.Response{Kind: "drivers.lookup"},
			wantErr:  plugin.ErrNoAnswer,
		},
		{
			name:     "an answer that is not JSON is refused",
			response: plugin.Response{Kind: "drivers.lookup", Payload: json.RawMessage(`yes`)},
			wantErr:  plugin.ErrInvalid,
			errText:  "answer is not JSON",
		},
		{
			name:     "spending tokens against no job is refused",
			response: plugin.Response{Kind: "coach.cue", Payload: json.RawMessage(`"x"`), Usage: plugin.Usage{InputTokens: 10}},
			wantErr:  plugin.ErrInvalid,
			errText:  "no job to record them against",
		},
		{
			name:     "spending a negative number of tokens is refused",
			response: plugin.Response{Kind: "coach.cue", Payload: json.RawMessage(`"x"`), Usage: plugin.Usage{Job: "coach.cue", InputTokens: -1}},
			wantErr:  plugin.ErrInvalid,
			errText:  "negative number of tokens",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			err := tc.response.Validate()
			if tc.wantErr == nil {
				r.NoError(err)
				return
			}
			r.ErrorIs(err, tc.wantErr)
			if tc.errText != "" {
				r.ErrorContains(err, tc.errText)
			}
		})
	}
}

// TestUsage covers the arithmetic the cap is enforced on.
func TestUsage(t *testing.T) {
	t.Parallel()

	t.Run("the total is what the operator paid for, in and out", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		u := plugin.Usage{Job: "coach.cue", InputTokens: 300, OutputTokens: 40}
		r.Equal(int64(340), u.Total())
		r.True(u.Spent())
		r.NoError(u.Validate())
	})

	t.Run("a call that cost nothing is valid and reports nothing", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		var u plugin.Usage
		r.False(u.Spent())
		r.NoError(u.Validate())
	})
}

// TestKinds checks the vocabularies. Events and setting kinds are closed: they
// are what the host sends and what the panel renders, so a typo has to be a
// refusal. Request kinds are open — any plugin may define one — but spelled one
// way, so that the kind says which plugin answers it.
func TestKinds(t *testing.T) {
	t.Parallel()

	t.Run("events are closed", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		r.True(plugin.EventLapCompleted.Valid())
		r.True(plugin.EventStintFinished.Valid())
		r.False(plugin.EventKind("lap.complete").Valid())
	})

	t.Run("request kinds are open, and name their plugin", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		for _, k := range []plugin.RequestKind{"drivers.lookup", "coach.cue.race", "a-b.c_d", "x9.y"} {
			r.True(k.Valid(), k)
		}
		r.Equal("drivers", plugin.RequestKind("drivers.lookup").Plugin())
		r.Equal("coach", plugin.RequestKind("coach.cue.race").Plugin(), "the plugin is the part before the first dot")

		for _, k := range []plugin.RequestKind{"", "cue", "Drivers.lookup", ".lookup", "drivers.", "drivers lookup", "drivers/lookup", "drivers.look up", "drivers.Look"} {
			r.False(k.Valid(), "%q should not be a kind", k)
		}
		r.Empty(plugin.RequestKind("cue").Plugin(), "a kind with no dot names nobody")
	})

	t.Run("setting kinds are closed", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		for _, k := range []plugin.Kind{plugin.KindText, plugin.KindSecret, plugin.KindNumber, plugin.KindBool, plugin.KindChoice} {
			r.True(k.Valid(), k)
		}
		r.False(plugin.Kind("colour").Valid())
	})
}

// TestDeadlineIsCarriedOnTheRequest is the small promise that a plugin choosing
// how hard to try does not have to interrogate its context.
func TestDeadlineIsCarriedOnTheRequest(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	deadline := time.Now().Add(2 * time.Second)
	req := plugin.Request{ID: "r1", Kind: "coach.cue", From: "host", Deadline: deadline}
	r.NoError(req.Validate())
	r.WithinDuration(deadline, req.Deadline, 0)
}

// Outside an answer there is no question being answered, so the depth is zero.
func TestHopsOutsideAnAnswerIsZero(t *testing.T) {
	t.Parallel()
	require.Zero(t, plugin.Hops(t.Context()))
}
