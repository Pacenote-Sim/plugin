package plugin

import "fmt"

// InterfaceVersion is the version of this contract. A host declares it, a
// plugin declares the one it was built against in its manifest, and the two
// must match exactly.
//
// Exactly, not "at least": a plugin built against a newer contract may expect
// facts an older host does not send, and one built against an older contract
// may ignore a field that now carries the meaning. Both are a coach saying
// something wrong to a driver at speed, hours after anyone would connect it to
// an upgrade. There is no degraded mode here on purpose.
//
// Version 2: the corner analysis and the car setup cross as the documents the
// client sent rather than as typed fields, and the fuel per lap and the tyre
// spread are no longer worked out by the host.
//
// Version 3: plugins ask each other. A request is an open kind spelled with the
// answering plugin's name and an opaque payload; Answer is optional; a plugin
// that asks is handed the host through a second channel on the same connection.
// The host itself no longer asks anything.
const InterfaceVersion = 3

// MagicCookieKey and MagicCookieValue are the handshake go-plugin performs
// before either side speaks. They are not security — anything that can run the
// plugin can read them out of this file — they are the check that stops a
// program started by mistake from being talked to as though it were a plugin.
const (
	MagicCookieKey   = "PACENOTE_PLUGIN"
	MagicCookieValue = "pacenote-plugin-v1-handshake"
)

// VersionError is a plugin and a host that were built against different
// contracts. It names both versions and what to do, because the operator
// reading it did not write either side.
type VersionError struct {
	// Plugin is the name from the manifest, for the message.
	Plugin string
	// Built is the interface version the plugin declared.
	Built int
	// Host is the interface version this host implements.
	Host int
}

// Error is the sentence an operator sees.
func (e *VersionError) Error() string {
	switch {
	case e.Built > e.Host:
		return fmt.Sprintf(
			"%s was built against plugin interface version %d and this server speaks version %d. "+
				"The plugin is newer than the server: upgrade the server, or install the build of %s for interface version %d.",
			e.name(), e.Built, e.Host, e.name(), e.Host)
	default:
		return fmt.Sprintf(
			"%s was built against plugin interface version %d and this server speaks version %d. "+
				"The plugin is older than the server: upgrade %s to a build for interface version %d.",
			e.name(), e.Built, e.Host, e.name(), e.Host)
	}
}

// Is reports that this is an [ErrInvalid], so a caller that only wants to know
// "is this the operator's problem or ours" does not have to unwrap it.
func (e *VersionError) Is(target error) bool { return target == ErrInvalid }

func (e *VersionError) name() string {
	if e.Plugin == "" {
		return "That plugin"
	}
	return e.Plugin
}

// CheckVersion compares what a plugin was built against with what a host
// speaks. A nil result is a plugin that may start.
func CheckVersion(name string, built, host int) error {
	if built == host {
		return nil
	}
	return &VersionError{Plugin: name, Built: built, Host: host}
}
