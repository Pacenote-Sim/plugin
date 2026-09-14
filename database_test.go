package plugin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// The connection string a host hands a plugin that declared a database.
//
// A plugin that did not declare one gets a named error rather than an empty
// string, because an empty connection string fails later and somewhere else.
//
// It is not parallel: t.Setenv and t.Parallel cannot both be used.
func TestTheDatabaseAPluginWasGiven(t *testing.T) {
	r := require.New(t)

	t.Setenv(plugin.EnvDatabaseURL, "")
	_, err := plugin.DatabaseURL()
	r.ErrorIs(err, plugin.ErrNoDatabase)

	t.Setenv(plugin.EnvDatabaseURL, "postgres://plugin_results_ab12@127.0.0.1:5432/pacenote")
	got, err := plugin.DatabaseURL()
	r.NoError(err)
	r.Equal("postgres://plugin_results_ab12@127.0.0.1:5432/pacenote", got)
}
