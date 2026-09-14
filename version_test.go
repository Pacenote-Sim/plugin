package plugin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestCheckVersion covers the one compatibility rule: exact, or refuse.
//
// The message matters as much as the refusal. An operator reading it wrote
// neither side, so it has to name both versions and say what to do.
func TestCheckVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		built    int
		host     int
		wantOK   bool
		contains []string
	}{
		{
			name:   "the same version starts",
			built:  1,
			host:   1,
			wantOK: true,
		},
		{
			name:     "a plugin newer than the server refuses, and says which way round",
			built:    2,
			host:     1,
			contains: []string{"coach", "version 2", "version 1", "newer than the server", "upgrade the server"},
		},
		{
			name:     "a plugin older than the server refuses, and says which way round",
			built:    1,
			host:     3,
			contains: []string{"coach", "version 1", "version 3", "older than the server", "upgrade coach"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			err := plugin.CheckVersion("coach", tc.built, tc.host)
			if tc.wantOK {
				r.NoError(err)
				return
			}
			r.Error(err)
			r.ErrorIs(err, plugin.ErrInvalid, "a version mismatch is somebody's mistake, not a transient failure")

			var ve *plugin.VersionError
			r.ErrorAs(err, &ve)
			r.Equal(tc.built, ve.Built)
			r.Equal(tc.host, ve.Host)
			for _, want := range tc.contains {
				r.Contains(err.Error(), want)
			}
		})
	}
}

// TestVersionErrorWithoutName still reads as a sentence when the manifest was
// too broken to carry a name.
func TestVersionErrorWithoutName(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	err := plugin.CheckVersion("", 9, plugin.InterfaceVersion)
	r.ErrorContains(err, "That plugin was built against plugin interface version 9")
}
