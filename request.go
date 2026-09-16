package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RequestKind names what is being asked for.
//
// It is an open vocabulary. A plugin that answers declares its kinds in its
// manifest, and each is spelled "<its name>.<what>" — "drivers.lookup" is
// answered by the plugin called drivers — so the kind says who answers it, two
// plugins cannot collide, and neither the host nor this contract has to know
// what any of them mean.
type RequestKind string

// Plugin is the name of the plugin that answers this kind: the part before the
// first dot, or empty when there is none.
func (k RequestKind) Plugin() string {
	name, _, ok := strings.Cut(string(k), ".")
	if !ok {
		return ""
	}
	return name
}

// Valid reports whether k is spelled the way a kind has to be: a plugin name, a
// dot, and a word — lowercase letters, digits, underscores and hyphens on both
// sides, and something on both sides.
func (k RequestKind) Valid() bool {
	name, what, ok := strings.Cut(string(k), ".")
	return ok && validName(name) && validWord(what)
}

// validWord is the part of a kind after the dot: the same alphabet as a plugin
// name, and dots allowed so a plugin can group its kinds.
func validWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// MaxHops is the most plugins a question may pass through. A asking B is one
// hop; B asking C while it answers is two. A plugin that asks itself, or two
// that ask each other, run into this rather than into the host's memory.
const MaxHops = 3

// Request is one plugin asking another for something, through the host, with
// the asker waiting.
//
// The host puts it. It stamps From — the asker's word is never taken for who
// it is — checks the asker declared the target, checks the target declared the
// kind, applies the target's daily cap and the deadline, and hands over the
// payload without reading it. What is in the payload is between the two
// plugins, the way what is in a corner document is between a client and the
// plugin that reads it.
type Request struct {
	// ID is unique to this call, for the plugin's own logging and cache.
	ID string `json:"id"`
	// Kind is what is wanted, spelled "<this plugin's name>.<what>".
	Kind RequestKind `json:"kind"`
	// From is the plugin that asked, as the host knows it. It is the host's
	// word and cannot be set by the asker.
	From string `json:"from"`
	// Hops is how many plugins this question has already passed through,
	// counting the asker. A plugin answering it that asks something else in
	// turn does not have to add to it: the host counts.
	Hops int `json:"hops,omitempty"`
	// Deadline is when the host stops waiting. It is also the deadline on the
	// context, repeated here so a plugin deciding how hard to try has the
	// number without asking the context for it.
	Deadline time.Time `json:"deadline"`
	// Payload is the question, in whatever shape this kind takes. It is a
	// document and not a struct so that this contract does not have to know.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Settings and Secrets are the answering plugin's own configuration, on
	// the same terms as every other call.
	Settings Values  `json:"-"`
	Secrets  Secrets `json:"-"`
	// TokenCeiling is the most this call may spend, or zero for no ceiling
	// beyond the operator's daily cap.
	TokenCeiling int `json:"token_ceiling,omitempty"`
}

// Validate refuses a request a plugin cannot answer.
func (r Request) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("%w: a request with no id cannot be traced", ErrInvalid)
	case !r.Kind.Valid():
		return fmt.Errorf("%w: %q is not a request kind — spell it <plugin>.<what>", ErrInvalid, r.Kind)
	case r.From == "":
		return fmt.Errorf("%w: a request from nobody cannot be answered", ErrInvalid)
	case r.Hops > MaxHops:
		return fmt.Errorf("%w: a request that has passed through %d plugins has gone round in a circle", ErrInvalid, r.Hops)
	case len(r.Payload) > 0 && !json.Valid(r.Payload):
		return fmt.Errorf("%w: the payload is not JSON", ErrInvalid)
	default:
		return nil
	}
}

// Response is what the answering plugin gives back.
type Response struct {
	// Kind echoes the request, so a caller holding several in flight can tell
	// them apart without keeping the map.
	Kind RequestKind `json:"kind"`
	// Payload is the answer, in whatever shape this kind takes. Empty is not
	// an answer — return [ErrNoAnswer] instead — because a caller must be able
	// to tell "nothing to say" from "here is nothing".
	Payload json.RawMessage `json:"payload,omitempty"`
	// Usage is what the call cost. A plugin that leaves this zero is telling
	// the core it spent nothing, and the core will believe it.
	Usage Usage `json:"usage"`
}

// Validate refuses a response the caller cannot use.
func (r Response) Validate() error {
	if err := r.Usage.Validate(); err != nil {
		return err
	}
	if len(r.Payload) == 0 {
		return ErrNoAnswer
	}
	if !json.Valid(r.Payload) {
		return fmt.Errorf("%w: the answer is not JSON", ErrInvalid)
	}
	return nil
}

// hopsKey carries the depth of the question a plugin is answering, so that a
// question it asks in turn is counted from there rather than from zero.
type hopsKey struct{}

func withHops(ctx context.Context, hops int) context.Context {
	return context.WithValue(ctx, hopsKey{}, hops)
}

// Hops is how many plugins the question being answered in ctx has passed
// through, or zero outside an answer. A plugin does not need it to ask — the
// host counts — but one that wants to know how deep it is may read it.
func Hops(ctx context.Context) int {
	if n, ok := ctx.Value(hopsKey{}).(int); ok {
		return n
	}
	return 0
}
