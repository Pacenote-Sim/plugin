package plugin

import (
	"fmt"
	"time"
)

// EventKind names something that happened.
type EventKind string

// The events version 1 carries. Two, because two is what the first demanding
// plugin needed, and an event nothing consumes is a payload to maintain for
// nobody. More will be added when a plugin asks.
const (
	// EventLapCompleted is one lap, stored and reduced to facts. It arrives
	// once per lap per driver, which on a full grid is the highest rate
	// anything here runs at.
	EventLapCompleted EventKind = "lap.completed"
	// EventStintFinished is a stint closed by its final summary. It carries
	// the whole-stint facts a debrief is written from.
	EventStintFinished EventKind = "stint.finished"
)

// Valid reports whether k is an event this version defines.
func (k EventKind) Valid() bool {
	switch k {
	case EventLapCompleted, EventStintFinished:
		return true
	default:
		return false
	}
}

// Event is the host telling a plugin that something happened.
//
// It is fire and forget from the server's side: the host dispatches it, does
// not block on it, and carries on. A plugin that is slow, wedged or dead
// delays nothing and fails nothing — an upload must never wait on an
// integration. The plugin still answers, because the answer carries what the
// event cost, and a plugin that spends the operator's tokens on an event has to
// be metered like anything else.
//
// Handle an event that is not yours by returning the zero [Usage] and no error.
// The host only sends the kinds a plugin declared, but a manifest can be edited.
type Event struct {
	// ID is unique to this delivery. It is the key to deduplicate on: a host
	// that restarts mid-dispatch may send the same event twice, and a plugin
	// that posts to a chat room should not post twice.
	ID string `json:"id"`
	// Kind is what happened.
	Kind EventKind `json:"kind"`
	// At is when the host dispatched it, not when the lap was driven — that is
	// in the facts.
	At time.Time `json:"at"`
	// Driver and Session are who and where.
	Driver  Driver  `json:"driver"`
	Session Session `json:"session"`
	// Lap is set when Kind is [EventLapCompleted], and nil otherwise.
	Lap *LapFacts `json:"lap,omitempty"`
	// Stint is set when Kind is [EventStintFinished], and nil otherwise.
	Stint *StintFacts `json:"stint,omitempty"`

	// Settings are the operator's answers for this plugin, already validated
	// against what it declared and with the defaults filled in. Credentials are
	// not here; they are in [Event.Secrets].
	Settings Values `json:"-"`
	// Secrets are the credentials this plugin declared, lent for this call.
	Secrets Secrets `json:"-"`
	// TokenCeiling is the most this call may spend, or zero for no ceiling
	// beyond the operator's daily cap. It is the core rationing its own money:
	// a plugin refuses before it sends rather than truncating afterwards.
	TokenCeiling int `json:"token_ceiling,omitempty"`
}

// Validate refuses an event a plugin cannot act on. The host calls it before
// dispatching, so a plugin author can trust the shape of what arrives.
func (e Event) Validate() error {
	switch {
	case e.ID == "":
		return fmt.Errorf("%w: an event with no id cannot be deduplicated", ErrInvalid)
	case !e.Kind.Valid():
		return fmt.Errorf("%w: %q is not an event this interface version carries", ErrInvalid, e.Kind)
	case e.Kind == EventLapCompleted && e.Lap == nil:
		return fmt.Errorf("%w: a %s with no lap facts says nothing", ErrInvalid, e.Kind)
	case e.Kind == EventStintFinished && e.Stint == nil:
		return fmt.Errorf("%w: a %s with no stint facts says nothing", ErrInvalid, e.Kind)
	default:
		return nil
	}
}
