package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// asked is a question as the server would put it: the kind spelled with this
// plugin's name, a sender, and this plugin's own settings beside it.
func asked(kind, payload string) plugin.Request {
	return plugin.Request{
		ID: "r1", Kind: plugin.RequestKind(kind), From: "payments",
		Payload:  json.RawMessage(payload),
		Settings: plugin.Values{settingRoster: "ana-ruiz: Ana Ruiz\nmarc-soler: Marc Soler\n\nbroken line\n  just-a-slug:  "},
	}
}

func TestLookup(t *testing.T) {
	t.Parallel()

	t.Run("a driver on the list", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		res, err := drivers{}.Answer(t.Context(), asked("drivers.lookup", `{"slug":"ana-ruiz"}`))
		r.NoError(err)
		r.JSONEq(`{"slug":"ana-ruiz","known":true,"name":"Ana Ruiz"}`, string(res.Payload))
		r.Equal(plugin.RequestKind("drivers.lookup"), res.Kind)
		r.Zero(res.Usage.Total(), "nothing was bought from anybody")
	})

	t.Run("a driver nobody knows is an answer, not an error", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		res, err := drivers{}.Answer(t.Context(), asked("drivers.lookup", `{"slug":"nobody"}`))
		r.NoError(err)
		r.JSONEq(`{"slug":"nobody","known":false}`, string(res.Payload))
	})

	t.Run("a question with no slug is the asker's mistake", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := drivers{}.Answer(t.Context(), asked("drivers.lookup", `{}`))
		r.ErrorIs(err, plugin.ErrInvalid)
	})

	t.Run("a kind this plugin does not answer", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		_, err := drivers{}.Answer(t.Context(), asked("drivers.delete", `{}`))
		r.ErrorIs(err, plugin.ErrUnsupported)
	})
}

func TestListReadsTheRosterForgivingly(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	res, err := drivers{}.Answer(t.Context(), asked("drivers.list", ""))
	r.NoError(err)
	// The broken line is skipped, the blank one too, and a slug with no name
	// is its own name.
	r.JSONEq(`{"drivers":[{"slug":"ana-ruiz","name":"Ana Ruiz"},{"slug":"marc-soler","name":"Marc Soler"},{"slug":"just-a-slug","name":"just-a-slug"}]}`,
		string(res.Payload))

	empty, err := drivers{}.Answer(t.Context(), plugin.Request{ID: "r", Kind: "drivers.list", From: "x"})
	r.NoError(err)
	r.JSONEq(`{"drivers":[]}`, string(empty.Payload), "an empty roster is an empty list, not null")
}

// The manifest beside this binary is what the server reads; it has to agree
// with what the code answers.
func TestTheManifestMatchesWhatThisAnswers(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	raw, err := os.ReadFile("plugin.json")
	r.NoError(err)
	var m plugin.Manifest
	r.NoError(json.Unmarshal(raw, &m))
	r.NoError(m.Validate())
	r.Equal(plugin.InterfaceVersion, m.InterfaceVersion)
	r.True(m.Capabilities.Answers("drivers.lookup"))
	r.True(m.Capabilities.Answers("drivers.list"))
	r.Empty(m.Capabilities.Asks, "it answers; it asks nobody")

	var p any = drivers{}
	_, ok := p.(plugin.Answerer)
	r.True(ok)

	settings, err := drivers{}.Settings(t.Context())
	r.NoError(err)
	r.NoError(plugin.ValidateSettings(settings))
	use, err := drivers{}.Notify(t.Context(), plugin.Event{Kind: plugin.EventLapCompleted})
	r.NoError(err)
	r.Zero(use.Total())
}
