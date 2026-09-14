package plugin

import "errors"

// The failures a host and a plugin both need to recognise. They are values
// rather than strings so that a caller can branch on them across the process
// boundary: the transport carries the sentinel in the error's text and rebuilds
// it on the other side.
var (
	// ErrInvalid is a malformed manifest, an unusable setting declaration, or a
	// value that does not fit the setting it was given for. It is the plugin
	// author's mistake or the operator's, never a transient failure, so a
	// caller must not retry it.
	ErrInvalid = errors.New("plugin: invalid")

	// ErrUnsupported is a plugin being asked for something it never said it
	// does — an event it did not declare, a request kind it does not answer.
	// The host avoids this by reading the manifest first; a plugin returns it
	// when the host asks anyway.
	ErrUnsupported = errors.New("plugin: not supported by this plugin")

	// ErrNoAnswer is a plugin declining to say anything. It is a normal
	// outcome and not a failure: a coach with nothing worth saying says
	// nothing, and the caller uses its own fallback. Returning an empty
	// response would be a lie the caller might speak aloud.
	ErrNoAnswer = errors.New("plugin: no answer")

	// ErrNotConfigured is a plugin that cannot work until the operator fills
	// something in — most often a credential. The caller falls back; the panel
	// shows the plugin as needing attention rather than as broken.
	ErrNotConfigured = errors.New("plugin: not configured")

	// ErrNoDatabase is [DatabaseURL] on a plugin that declared none. It is a
	// programming mistake rather than a runtime condition — a plugin that asks
	// for a database it did not declare — so a plugin should treat it as fatal
	// at startup rather than carrying on without one.
	ErrNoDatabase = errors.New("plugin: no database")
)
