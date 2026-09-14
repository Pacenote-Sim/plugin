package plugin

import (
	"fmt"
	"log/slog"
	"sort"
)

// Redacted is what a [Secret] renders as anywhere it can escape: a log line, an
// error, a formatted string, a JSON document. It is a fixed word rather than a
// length or a prefix, because "the first four characters of the key" is still
// four characters of the key.
const Redacted = "[redacted]"

// Secret is one of the operator's credentials, lent to a plugin for the length
// of one call.
//
// The value is unexported and there is exactly one way to read it, [Secret.Value].
// Every other way a string usually escapes a program is closed: printing it
// gives [Redacted], logging it gives [Redacted], encoding it as JSON gives
// [Redacted]. A plugin author who logs the whole request they were handed —
// which is the first thing anybody does when a call misbehaves — cannot leak
// the operator's key by accident, and that is the entire reason this is a type
// and not a string.
//
// The core holds the credential. A plugin is given it, uses it, and forgets it:
// storing one on disk or in a package variable is outside what this contract
// allows, and the operator's key is not the plugin's to keep.
type Secret struct{ v string }

// NewSecret wraps a credential. Plugin authors do not usually call this — the
// host fills [Request.Secrets] and [Event.Secrets] — but a test that fakes a
// call needs it.
func NewSecret(v string) Secret { return Secret{v: v} }

// Value is the credential itself, and the only way to get it. Pass the result
// straight to whatever needs it; do not hold it in a field, and do not put it
// anywhere a later line might print.
func (s Secret) Value() string { return s.v }

// Empty reports whether there is no credential here. It is the check to make
// before a call the operator has not configured a key for.
func (s Secret) Empty() bool { return s.v == "" }

// String is [Redacted]. This is what makes fmt.Sprintf("%v", secret) safe.
func (s Secret) String() string { return Redacted }

// GoString is [Redacted], which covers the %#v that a debugging session reaches
// for when %v did not say enough.
func (s Secret) GoString() string { return Redacted }

// LogValue is [Redacted], so slog renders it that way whether it is logged on
// its own or reached through a struct.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalJSON is [Redacted]. Secrets are not carried inside any JSON document
// this contract defines — they travel in their own field — so this exists for
// the plugin author who encodes a request into their own log or cache.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// UnmarshalJSON always fails. A [Secret] that came out of a JSON document would
// be one that was written into a JSON document, which is the thing this type
// exists to prevent.
func (s *Secret) UnmarshalJSON([]byte) error {
	return fmt.Errorf("%w: a credential is never read from JSON", ErrInvalid)
}

// Secrets are the credentials for one call, keyed by the name of the setting
// that holds them.
//
// A plugin receives the secret settings it declared itself and nothing else.
// There is no key here belonging to another plugin, and no way to ask for one.
type Secrets map[string]Secret

// Get is the credential stored for the named setting. The second result is
// false when the operator has not set one, which a plugin should treat as "this
// feature is off" rather than as a failure.
func (s Secrets) Get(name string) (Secret, bool) {
	v, ok := s[name]
	if !ok || v.Empty() {
		return Secret{}, false
	}
	return v, true
}

// Names lists the settings a credential was supplied for, in order. It names
// settings, never values, so it is safe to log.
func (s Secrets) Names() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Values is what the host hands the transport: the raw credentials, keyed the
// same way. It is unexported behaviour made explicit — the one place in this
// module that reads a secret in bulk — so that it is greppable rather than
// scattered.
func (s Secrets) raw() map[string]string {
	if len(s) == 0 {
		return nil
	}
	out := make(map[string]string, len(s))
	for k, v := range s {
		out[k] = v.Value()
	}
	return out
}

// secretsFrom rebuilds [Secrets] from the transport's map.
func secretsFrom(m map[string]string) Secrets {
	if len(m) == 0 {
		return nil
	}
	out := make(Secrets, len(m))
	for k, v := range m {
		out[k] = Secret{v: v}
	}
	return out
}
