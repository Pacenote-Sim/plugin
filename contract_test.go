package plugin_test

import (
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

// TestRequestValidate covers the same for the waiting half.
func TestRequestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		request plugin.Request
		wantErr string
	}{
		{
			name:    "a race cue with a lap is accepted",
			request: plugin.Request{ID: "r1", Kind: plugin.RequestCueRace, Lap: &plugin.LapFacts{}},
		},
		{
			name:    "setup advice with a stint is accepted",
			request: plugin.Request{ID: "r1", Kind: plugin.RequestSetup, Stint: &plugin.StintFacts{}},
		},
		{
			name:    "a request with no id cannot be traced",
			request: plugin.Request{Kind: plugin.RequestCueRace, Lap: &plugin.LapFacts{}},
			wantErr: "cannot be traced",
		},
		{
			name:    "a request this version does not carry is refused",
			request: plugin.Request{ID: "r1", Kind: "tell.joke"},
			wantErr: "is not a request this interface version carries",
		},
		{
			name:    "a cue with no lap has nothing to say",
			request: plugin.Request{ID: "r1", Kind: plugin.RequestCueTraining},
			wantErr: "with no lap facts",
		},
		{
			name:    "a debrief with no stint has nothing to say",
			request: plugin.Request{ID: "r1", Kind: plugin.RequestDebrief},
			wantErr: "with no stint facts",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			err := tc.request.Validate()
			if tc.wantErr == "" {
				r.NoError(err)
				return
			}
			r.ErrorIs(err, plugin.ErrInvalid)
			r.ErrorContains(err, tc.wantErr)
		})
	}
}

// TestResponseValidate covers what a caller refuses to use. The important case
// is the empty one: a caller that speaks what it is given has to be able to
// tell "nothing to say" from "here is nothing".
func TestResponseValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		response plugin.Response
		wantErr  error
		errText  string
	}{
		{
			name:     "a line is an answer",
			response: plugin.Response{Kind: plugin.RequestCueRace, Text: "Turn 4, more entry speed."},
		},
		{
			name: "a change is an answer",
			response: plugin.Response{
				Kind:    plugin.RequestSetup,
				Changes: []plugin.SetupChange{{Area: "front", Setting: "anti-roll bar", Direction: plugin.DirectionSofter, Why: "the front is hotter"}},
			},
		},
		{
			name:     "an empty answer is no answer",
			response: plugin.Response{Kind: plugin.RequestCueRace},
			wantErr:  plugin.ErrNoAnswer,
		},
		{
			name: "a change with no setting is refused",
			response: plugin.Response{
				Kind:    plugin.RequestSetup,
				Changes: []plugin.SetupChange{{Area: "front", Direction: plugin.DirectionSofter, Why: "because"}},
			},
			wantErr: plugin.ErrInvalid,
			errText: "names no setting to change",
		},
		{
			name: "a change with no reason is a guess with a confident tone",
			response: plugin.Response{
				Kind:    plugin.RequestSetup,
				Changes: []plugin.SetupChange{{Area: "front", Setting: "anti-roll bar", Direction: plugin.DirectionSofter}},
			},
			wantErr: plugin.ErrInvalid,
			errText: "gives no reason",
		},
		{
			name:     "spending tokens against no job is refused",
			response: plugin.Response{Kind: plugin.RequestCueRace, Text: "x", Usage: plugin.Usage{InputTokens: 10}},
			wantErr:  plugin.ErrInvalid,
			errText:  "no job to record them against",
		},
		{
			name:     "spending a negative number of tokens is refused",
			response: plugin.Response{Kind: plugin.RequestCueRace, Text: "x", Usage: plugin.Usage{Job: "cue.race", InputTokens: -1}},
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

		u := plugin.Usage{Job: "cue.race", InputTokens: 300, OutputTokens: 40}
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

// TestKindsAreClosed checks that the vocabularies refuse anything they do not
// define. They are the vocabularies a manifest and a prompt are written
// against, so a typo has to be a refusal and not a silent no-op.
func TestKindsAreClosed(t *testing.T) {
	t.Parallel()

	t.Run("events", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		r.True(plugin.EventLapCompleted.Valid())
		r.True(plugin.EventStintFinished.Valid())
		r.False(plugin.EventKind("lap.complete").Valid())
	})

	t.Run("requests", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		for _, k := range []plugin.RequestKind{plugin.RequestCueRace, plugin.RequestCueTraining, plugin.RequestDebrief, plugin.RequestSetup} {
			r.True(k.Valid(), k)
		}
		r.False(plugin.RequestKind("cue").Valid())
	})

	t.Run("setting kinds", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		for _, k := range []plugin.Kind{plugin.KindText, plugin.KindSecret, plugin.KindNumber, plugin.KindBool, plugin.KindChoice} {
			r.True(k.Valid(), k)
		}
		r.False(plugin.Kind("colour").Valid())
	})
}

// TestDeadlineIsCarriedOnTheRequest is the small promise that a plugin choosing
// between a fast model and a good one does not have to interrogate its context.
func TestDeadlineIsCarriedOnTheRequest(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	deadline := time.Now().Add(2 * time.Second)
	req := plugin.Request{ID: "r1", Kind: plugin.RequestCueRace, Deadline: deadline, Lap: &plugin.LapFacts{}}
	r.NoError(req.Validate())
	r.WithinDuration(deadline, req.Deadline, 0)
}

// A speak request with nothing to say, and an answer with audio nothing can
// play. Both are caught on the wire rather than at the speaker.
func TestTheTwoWaysASpokenLineGoesWrong(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.ErrorIs(plugin.Request{ID: "r1", Kind: plugin.RequestSpeak, Text: "   "}.Validate(), plugin.ErrInvalid)
	r.NoError(plugin.Request{ID: "r1", Kind: plugin.RequestSpeak, Text: "box this lap"}.Validate())

	r.ErrorIs(plugin.Response{Audio: []byte{0x01}}.Validate(), plugin.ErrInvalid)
	r.NoError(plugin.Response{Audio: []byte{0x01}, AudioType: "audio/wav"}.Validate())
}
