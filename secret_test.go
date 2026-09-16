package plugin_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// printed formats through an interface, which is how a credential actually
// reaches a log line: inside a struct, behind a %v, with nobody having thought
// about it. Calling String directly would be testing the method rather than the
// hazard.
func printed(format string, v any) string { return fmt.Sprintf(format, v) }

// TestSecretNeverRenders is the test that matters most in this package. A
// credential leaks through whichever printing path nobody thought about, so
// every path a string can take out of a program is checked here, against a
// value distinctive enough that finding it is unambiguous.
func TestSecretNeverRenders(t *testing.T) {
	t.Parallel()

	const value = "sk-ant-thisisthekeynobodymayever-see"

	cases := []struct {
		name string
		show func(plugin.Secret) string
		// gone is a path where the credential does not appear at all rather
		// than appearing redacted, which is the stronger answer and the one
		// the contract's own documents take: secrets are never a field of a
		// JSON body.
		gone bool
	}{
		{name: "printed with the default verb", show: func(s plugin.Secret) string { return printed("%v", s) }},
		{name: "printed as a string", show: func(s plugin.Secret) string { return printed("%s", s) }},
		{name: "printed quoted", show: func(s plugin.Secret) string { return printed("%q", s) }},
		{name: "printed for a Go programmer", show: func(s plugin.Secret) string { return printed("%#v", s) }},
		{name: "stringified", show: func(s plugin.Secret) string { return s.String() }},
		{name: "encoded as JSON", show: func(s plugin.Secret) string {
			b, err := json.Marshal(s)
			if err != nil {
				return err.Error()
			}
			return string(b)
		}},
		{name: "inside a struct encoded as JSON", show: func(s plugin.Secret) string {
			b, err := json.Marshal(struct {
				Key plugin.Secret `json:"key"`
			}{s})
			if err != nil {
				return err.Error()
			}
			return string(b)
		}},
		{name: "inside a request encoded as JSON", show: func(s plugin.Secret) string {
			b, err := json.Marshal(plugin.Request{
				ID:      "r1",
				Kind:    "coach.cue",
				From:    "host",
				Secrets: plugin.Secrets{"api_key": s},
			})
			if err != nil {
				return err.Error()
			}
			return string(b)
		}, gone: true},
		{name: "logged on its own", show: func(s plugin.Secret) string {
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("called", slog.Any("key", s))
			return buf.String()
		}},
		{name: "logged inside a group", show: func(s plugin.Secret) string {
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).
				With(slog.Group("plugin", slog.Any("key", s))).
				Error("that call failed")
			return buf.String()
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			got := tc.show(plugin.NewSecret(value))
			r.NotContains(got, value, "the credential escaped")
			r.NotContains(got, "sk-ant", "part of the credential escaped")
			if tc.gone {
				r.NotContains(got, plugin.Redacted, "a secret is not a field of this document at all")
				return
			}
			r.Contains(got, plugin.Redacted)
		})
	}
}

// TestSecretValue checks the one way out, and the checks around it.
func TestSecretValue(t *testing.T) {
	t.Parallel()

	t.Run("the value is readable exactly once, deliberately", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		s := plugin.NewSecret("hunter2")
		r.Equal("hunter2", s.Value())
		r.False(s.Empty())
	})

	t.Run("an empty secret says so", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		r.True(plugin.Secret{}.Empty())
		r.Equal(plugin.Redacted, plugin.Secret{}.String())
	})

	t.Run("a secret is never read back out of JSON", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		var s plugin.Secret
		err := json.Unmarshal([]byte(`"hunter2"`), &s)
		r.ErrorIs(err, plugin.ErrInvalid)
		r.True(s.Empty())
	})
}

// TestSecrets covers the map a plugin is handed.
func TestSecrets(t *testing.T) {
	t.Parallel()

	t.Run("a set credential is found", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		s := plugin.Secrets{"api_key": plugin.NewSecret("k")}
		got, ok := s.Get("api_key")
		r.True(ok)
		r.Equal("k", got.Value())
	})

	t.Run("an empty credential reads as absent", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		s := plugin.Secrets{"api_key": plugin.NewSecret("")}
		_, ok := s.Get("api_key")
		r.False(ok, "an operator who has not filled it in is the same as not having one")
	})

	t.Run("a credential nobody set is absent", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, ok := plugin.Secrets(nil).Get("api_key")
		r.False(ok)
	})

	t.Run("names are settings and never values, in order", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		s := plugin.Secrets{
			"webhook": plugin.NewSecret("https://example.invalid/zzz"),
			"api_key": plugin.NewSecret("sk-ant-zzz"),
		}
		names := s.Names()
		r.Equal([]string{"api_key", "webhook"}, names)
		r.NotContains(strings.Join(names, " "), "zzz", "the names must carry no part of a value")
	})
}

// Every way a credential could be printed by accident.
//
// The redaction is spread over four methods and a struct tag, and any one of
// them going missing is silent: the value still works, and the only sign is a
// key in somebody's log. So this asserts the property rather than the methods —
// if a fifth way to format a value appears, this is where it gets added.
func TestACredentialCannotBePrintedByAccident(t *testing.T) {
	t.Parallel()

	const key = "sk-ant-thekeynobodymayeversee"
	one := plugin.NewSecret(key)
	many := plugin.Secrets{"api_key": one}

	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("a plugin logged what it was given",
		slog.Any("secrets", many), slog.Any("one", one))

	asJSON := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return string(b)
	}

	// fmt.Sprintf("%v", one) is what the linters want written as one.String().
	// It is deliberate: what is being tested is what fmt does with a secret,
	// because fmt is what a careless caller reaches for.
	//nolint:gocritic,staticcheck // redundantSprint/S1025: the redundancy is the test.
	for name, out := range map[string]string{
		"%v on a secret":    fmt.Sprintf("%v", one),
		"%s on a secret":    fmt.Sprintf("%s", one),
		"%#v on a secret":   fmt.Sprintf("%#v", one),
		"%v on the map":     fmt.Sprintf("%v", many),
		"%+v on the map":    fmt.Sprintf("%+v", many),
		"%#v on the map":    fmt.Sprintf("%#v", many),
		"json, the map":     asJSON(many),
		"json, a request":   asJSON(plugin.Request{ID: "r1", Kind: "voice.speak", From: "host", Payload: json.RawMessage(`"x"`), Secrets: many}),
		"json, an event":    asJSON(plugin.Event{Kind: plugin.EventLapCompleted, Secrets: many}),
		"json, an http req": asJSON(plugin.HTTPRequest{Method: "GET", Path: "/", Secrets: many}),
		"slog":              logged.String(),
	} {
		require.NotContains(t, out, key, "a credential reached %s", name)
	}

	// And the value is still reachable by the one method that is meant to.
	require.Equal(t, key, one.Value())
}
