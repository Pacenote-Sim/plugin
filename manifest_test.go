package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestParseManifest covers every way a manifest can be wrong. A malformed
// manifest has to fail at the point the author or the operator can still fix
// it, which means here and not three hours into a session.
func TestParseManifest(t *testing.T) {
	t.Parallel()

	const good = `{
		"name": "testplugin",
		"version": "1.0.0",
		"author": "Pacenote",
		"description": "Does nothing, honestly.",
		"interface_version": 1,
		"capabilities": {"events": ["lap.completed"]}
	}`

	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{name: "a complete manifest is accepted", json: good},
		{
			name: "everything declared at once is accepted",
			json: `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"binary":"x-plugin",
				"capabilities":{"events":["lap.completed","stint.finished"],"requests":["x.cue","x.setup"],"asks":["drivers"],
				"network":true,"writes_files":true,"reads_driver_data":true}}`,
		},
		{name: "a truncated document is refused", json: `{"name": "x"`, wantErr: "is not readable"},
		{name: "something that is not JSON at all is refused", json: "name = x", wantErr: "is not readable"},
		{
			name:    "a misspelled field is refused rather than ignored",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilitys":{"events":["lap.completed"]}}`,
			wantErr: "is not readable",
		},
		{
			name:    "two documents in one file are refused",
			json:    good + good,
			wantErr: "more than one document",
		},
		{
			name:    "a name nothing can key on is refused",
			json:    `{"name":"Test Plugin","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"events":["lap.completed"]}}`,
			wantErr: "is not a plugin name",
		},
		{
			name:    "no version is refused",
			json:    `{"name":"x","author":"a","description":"d","interface_version":1,"capabilities":{"events":["lap.completed"]}}`,
			wantErr: "declares no version",
		},
		{
			name:    "no author is refused, because installing it is trusting one",
			json:    `{"name":"x","version":"1","description":"d","interface_version":1,"capabilities":{"events":["lap.completed"]}}`,
			wantErr: "declares no author",
		},
		{
			name:    "no description is refused",
			json:    `{"name":"x","version":"1","author":"a","interface_version":1,"capabilities":{"events":["lap.completed"]}}`,
			wantErr: "describes itself in no words",
		},
		{
			name:    "no interface version is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","capabilities":{"events":["lap.completed"]}}`,
			wantErr: "declares no plugin interface version",
		},
		{
			name:    "a binary that is a path is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"binary":"../../bin/sh","capabilities":{"events":["lap.completed"]}}`,
			wantErr: "must be a file name beside the manifest",
		},
		{
			name:    "an event this server does not carry is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"events":["driver.paired"]}}`,
			wantErr: "is not an event this server carries",
		},
		{
			name:    "a request kind that is not spelled like one is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"requests":["tell joke"]}}`,
			wantErr: "is not a request kind",
		},
		{
			name:    "a request kind spelled for another plugin is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"requests":["drivers.lookup"]}}`,
			wantErr: "a plugin's kinds are spelled <its name>.<what>",
		},
		{
			name:    "asking something that is not a plugin name is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"events":["lap.completed"],"asks":["Bad Name"]}}`,
			wantErr: "is not a plugin name",
		},
		{
			name:    "a plugin that asks itself is a loop, not a capability",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"events":["lap.completed"],"asks":["x"]}}`,
			wantErr: "asks itself",
		},
		{
			name:    "a plugin nothing would ever call is refused",
			json:    `{"name":"x","version":"1","author":"a","description":"d","interface_version":1,"capabilities":{"network":true}}`,
			wantErr: "nothing would ever call it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			m, err := plugin.ParseManifest([]byte(tc.json))
			if tc.wantErr == "" {
				r.NoError(err)
				r.NotEmpty(m.Name)
				return
			}
			r.ErrorIs(err, plugin.ErrInvalid)
			r.ErrorContains(err, tc.wantErr)
		})
	}
}

// TestLoadManifest covers reading one off disk, which is what the host does.
func TestLoadManifest(t *testing.T) {
	t.Parallel()

	t.Run("a manifest in a directory is read", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		dir := t.TempDir()
		r.NoError(os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte(`{
			"name":"testplugin","version":"1.0.0","author":"Pacenote","description":"d",
			"interface_version":1,"capabilities":{"events":["lap.completed"]}}`), 0o600))

		m, err := plugin.LoadManifest(dir)
		r.NoError(err)
		r.Equal("testplugin", m.Name)
		r.Equal(1, m.InterfaceVersion)
		r.True(m.Capabilities.Wants(plugin.EventLapCompleted))
		r.False(m.Capabilities.Wants(plugin.EventStintFinished))
	})

	t.Run("a directory with no manifest is not a plugin", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, err := plugin.LoadManifest(t.TempDir())
		r.ErrorIs(err, plugin.ErrInvalid)
		r.ErrorContains(err, "could not be read")
	})

	t.Run("the path is named when the contents are wrong", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		dir := t.TempDir()
		r.NoError(os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte(`{`), 0o600))

		_, err := plugin.LoadManifest(dir)
		r.ErrorContains(err, plugin.ManifestName)
	})
}

// TestCapabilities covers what the operator is shown and what the host asks.
func TestCapabilities(t *testing.T) {
	t.Parallel()

	t.Run("a declared request is answered and nothing else is", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		c := plugin.Capabilities{Requests: []plugin.RequestKind{"x.setup"}}
		r.True(c.Answers("x.setup"))
		r.False(c.Answers("x.cue"))
	})

	t.Run("a declared target may be asked and nothing else may", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		c := plugin.Capabilities{Asks: []string{"drivers"}}
		r.True(c.MayAsk("drivers"))
		r.False(c.MayAsk("payments"))
		r.False(plugin.Capabilities{}.MayAsk("drivers"))
	})

	t.Run("what the operator reads names every declaration", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		lines := plugin.Capabilities{
			Events:          []plugin.EventKind{plugin.EventLapCompleted},
			Requests:        []plugin.RequestKind{"x.cue"},
			Asks:            []string{"drivers", "results"},
			Network:         true,
			WritesFiles:     true,
			ReadsDriverData: true,
		}.Describe()
		r.Len(lines, 6)
		r.Contains(lines[0], "lap.completed")
		r.Contains(lines[1], "x.cue")
		r.Equal("Asks drivers, results for information.", lines[2])
		r.Contains(lines[3], "outside this machine")
	})

	t.Run("a plugin that declares nothing says so", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		r.Equal([]string{"Declares nothing."}, plugin.Capabilities{}.Describe())
	})
}

// TestExecutable covers where the host looks for the binary.
func TestExecutable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		manifest plugin.Manifest
		want     string
	}{
		{
			name:     "the binary is named",
			manifest: plugin.Manifest{Name: "coach", Binary: "pacenote-coach"},
			want:     "pacenote-coach",
		},
		{
			name:     "no binary means the plugin's own name",
			manifest: plugin.Manifest{Name: "coach"},
			want:     "coach",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			got := tc.manifest.Executable(filepath.Join("plugins", "coach"))
			r.Equal(filepath.Join("plugins", "coach"), filepath.Dir(got))
			// The extension is the platform's, so the name is compared
			// without one rather than the test asserting it runs on Linux.
			base := filepath.Base(got)
			r.Equal(tc.want, base[:len(tc.want)])
		})
	}
}

// Serves is how a host decides whether to mount a plugin at all, before it
// asks the plugin anything.
func TestWhetherAPluginAskedForARoute(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.False(plugin.Capabilities{}.Serves())
	r.True(plugin.Capabilities{HTTP: &plugin.HTTPCapability{
		Routes: []plugin.Route{{Path: "/", Access: plugin.AccessAdmin}},
	}}.Serves())
}

// What the panel prints about a plugin that keeps tables and serves pages —
// the two capabilities a plugin has to ask for explicitly.
func TestWhatThePanelSaysAboutAPluginThatKeepsTablesAndServes(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	lines := strings.Join(plugin.Capabilities{
		Database: true,
		HTTP: &plugin.HTTPCapability{Routes: []plugin.Route{
			{Path: "/webhook", Access: plugin.AccessPublic},
		}},
	}.Describe(), "\n")

	r.Contains(lines, "Keeps tables of its own")
	r.Contains(lines, "/webhook")
}

// A route table a host will not carry makes the whole manifest invalid, rather
// than being dropped quietly.
func TestAManifestWhoseRoutesAreNotRoutes(t *testing.T) {
	t.Parallel()

	err := plugin.Capabilities{HTTP: &plugin.HTTPCapability{
		Routes: []plugin.Route{{Path: "no-leading-slash", Access: plugin.AccessAdmin}},
	}}.Validate()
	require.ErrorIs(t, err, plugin.ErrInvalid)
}
