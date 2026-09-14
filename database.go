package plugin

import (
	"fmt"
	"os"
)

// EnvDatabaseURL names the variable a plugin's own database arrives in.
//
// A plugin that declared [Capabilities.Database] is started with this set and
// with nothing else from the server's environment. The connection string it
// carries is not the server's: it names a role created for this plugin alone,
// which owns one schema and may read the core_read views and nothing else. A
// plugin cannot read another plugin's tables, and cannot write the server's.
const EnvDatabaseURL = "PACENOTE_DATABASE_URL"

// MigrationsDir is the directory a plugin keeps its schema in, beside its
// manifest. The host applies every .sql file in it, in the order their names
// sort, once each.
//
// There are no down migrations. Uninstalling a plugin drops its schema whole,
// which is the only rollback that is ever actually correct — a down migration
// that has to guess how to un-split a column is a way to lose data slowly.
const MigrationsDir = "migrations"

// DatabaseURL is the connection string for this plugin's own database, or
// [ErrNoDatabase] if it declared none.
//
// It returns a string rather than an open pool so that a plugin may use
// whatever it likes — pgx, database/sql, sqlc, an ORM — and so that this module,
// which every plugin depends on including closed commercial ones, needs no
// database driver of its own.
//
// Call it once at startup and keep the pool. It is stable for the life of the
// process; a host that changes it restarts the plugin.
func DatabaseURL() (string, error) {
	v := os.Getenv(EnvDatabaseURL)
	if v == "" {
		return "", fmt.Errorf("%w: %s is not set, which is a plugin that did not declare a database in its manifest, or a host too old to provide one",
			ErrNoDatabase, EnvDatabaseURL)
	}
	return v, nil
}
