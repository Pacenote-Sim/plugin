package plugin

import (
	"fmt"
	"strings"
	"time"
)

// RequestKind names something the host wants back.
type RequestKind string

// The requests version 1 carries: the four jobs a coaching plugin does, and one
// a voice plugin does.
const (
	// RequestCueRace is one spoken line about position and the gaps either
	// side, under twelve words, wanted inside two seconds.
	RequestCueRace RequestKind = "cue.race"
	// RequestCueTraining is one spoken line about the corner the driver lost
	// the most in and one fix, under eighteen words, wanted inside two seconds.
	RequestCueTraining RequestKind = "cue.training"
	// RequestDebrief is a few hundred written words after a session, wanted
	// inside thirty.
	RequestDebrief RequestKind = "debrief"
	// RequestSetup is setup advice: a list of changes with a reason each.
	RequestSetup RequestKind = "setup"
	// RequestSpeak turns one line into audio a driver hears. It is the only
	// request whose answer is bytes rather than language, and it is the one
	// with the least room: the line was written because a corner is coming.
	//
	// The text is in [Request.Text] and the answer in [Response.Audio]. A
	// plugin answering this holds the credential for whatever service it uses;
	// the server relays what comes back and knows nothing about the vendor.
	RequestSpeak RequestKind = "speak"
)

// Valid reports whether k is a request this version defines.
func (k RequestKind) Valid() bool {
	switch k {
	case RequestCueRace, RequestCueTraining, RequestDebrief, RequestSetup, RequestSpeak:
		return true
	default:
		return false
	}
}

// Request is the host asking a plugin for something and waiting.
//
// It is the other half of [Event] and the reason this interface is not just a
// notification bus: coaching returns a line the server stores and the client
// speaks, and setup advice returns changes the server files against a car and a
// circuit.
//
// The deadline is real. It is on the context the plugin is called with, and a
// plugin that misses it is skipped, reported, and the caller told there was no
// answer so it can use its own fallback. Nothing is queued for a retry: a cue
// that arrives after the corner is worse than no cue.
type Request struct {
	// ID is unique to this call, for the plugin's own logging and cache.
	ID string `json:"id"`
	// Kind is what is wanted.
	Kind RequestKind `json:"kind"`
	// Deadline is when the host stops waiting. It is also the deadline on the
	// context, and it is repeated here so that a plugin deciding between a
	// fast model and a good one has the number without having to ask the
	// context for it.
	Deadline time.Time `json:"deadline"`
	// Driver and Session are who and where.
	Driver  Driver  `json:"driver"`
	Session Session `json:"session"`
	// Lap is the lap in question, for the two cue jobs.
	Lap *LapFacts `json:"lap,omitempty"`
	// Stint is the stint in question, for a debrief and for setup advice.
	Stint *StintFacts `json:"stint,omitempty"`
	// Symptom is what the car was doing, for [RequestSetup]. It is derived
	// from the telemetry rather than asked of the driver — steering angle
	// against lateral acceleration says whether the car turns as much as it is
	// asked to — but a driver may override it, because they felt it and we did
	// not. Empty means the plugin should derive its own from the facts.
	Symptom string `json:"symptom,omitempty"`
	// Text is what to say, for [RequestSpeak]. It is the line a coach already
	// wrote and a validator already passed, so a plugin speaking it neither
	// edits it nor decides whether it should be said.
	//
	// Voice is which voice to use, when the caller has a preference. It is the
	// plugin's own identifier for one — a service's voice id — and an empty
	// string is the operator's configured default.
	Text  string `json:"text,omitempty"`
	Voice string `json:"voice,omitempty"`

	// Settings, Secrets and TokenCeiling are as on [Event].
	Settings     Values  `json:"-"`
	Secrets      Secrets `json:"-"`
	TokenCeiling int     `json:"token_ceiling,omitempty"`
}

// Validate refuses a request a plugin cannot answer.
func (r Request) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("%w: a request with no id cannot be traced", ErrInvalid)
	case !r.Kind.Valid():
		return fmt.Errorf("%w: %q is not a request this interface version carries", ErrInvalid, r.Kind)
	case (r.Kind == RequestCueRace || r.Kind == RequestCueTraining) && r.Lap == nil:
		return fmt.Errorf("%w: a %s with no lap facts has nothing to say", ErrInvalid, r.Kind)
	case r.Kind == RequestSpeak && strings.TrimSpace(r.Text) == "":
		return fmt.Errorf("%w: a speak request carries no text, so there is nothing to say", ErrInvalid)
	case (r.Kind == RequestDebrief || r.Kind == RequestSetup) && r.Stint == nil:
		return fmt.Errorf("%w: a %s with no stint facts has nothing to say", ErrInvalid, r.Kind)
	default:
		return nil
	}
}

// Direction is which way a setup change goes.
type Direction string

// The directions a change can take. They are named rather than signed numbers
// because "stiffer" and "more" are what an operator reads in the panel, and a
// plugin that returns +1 is asking a human to remember a convention.
const (
	DirectionMore     Direction = "more"
	DirectionLess     Direction = "less"
	DirectionStiffer  Direction = "stiffer"
	DirectionSofter   Direction = "softer"
	DirectionHigher   Direction = "higher"
	DirectionLower    Direction = "lower"
	DirectionForward  Direction = "forward"
	DirectionRearward Direction = "rearward"
)

// SetupChange is one thing to change on the car.
type SetupChange struct {
	// Area is the part of the car — "front suspension", "differential".
	Area string `json:"area"`
	// Setting is the control on the setup sheet, spelled the way the
	// simulator spells it, so a driver can find it.
	Setting string `json:"setting"`
	// Direction is which way to move it.
	Direction Direction `json:"direction"`
	// Amount is how far, in the units the sheet uses — "one click", "2 mm".
	// It may be empty when the direction is the whole advice.
	Amount string `json:"amount,omitempty"`
	// Why is one line of reason, tied to a fact the plugin was given. A change
	// with no why is a guess with a confident tone.
	Why string `json:"why"`
}

// Response is what a plugin gives back.
type Response struct {
	// Kind echoes the request, so a caller holding several in flight can tell
	// them apart without keeping the map.
	Kind RequestKind `json:"kind"`
	// Text is the answer for the jobs whose answer is language: a cue, a
	// debrief. It is checked by the caller against the rules for that job
	// before anything is spoken, because a prompt is a request and a validator
	// is a guarantee.
	Text string `json:"text,omitempty"`
	// Changes are the answer for [RequestSetup].
	Changes []SetupChange `json:"changes,omitempty"`
	// Audio is the answer for [RequestSpeak], and AudioType its media type —
	// "audio/wav" or "audio/mpeg". The server relays both to the client without
	// decoding either, so a plugin may answer in whatever its service produces.
	Audio     []byte `json:"audio,omitempty"`
	AudioType string `json:"audio_type,omitempty"`
	// Usage is what the call cost. A plugin that leaves this zero is telling
	// the core it spent nothing, and the core will believe it.
	Usage Usage `json:"usage"`
	// PromptVersion is the version of the prompt asset that produced this,
	// recorded so a change in output can be traced to a change in prompt. It
	// is free text and it may be empty.
	PromptVersion string `json:"prompt_version,omitempty"`
}

// Validate refuses a response the caller cannot use. An answer with no content
// at all is [ErrNoAnswer] rather than an empty success: a caller that speaks
// what it is given must be able to tell "nothing to say" from "here is
// nothing".
func (r Response) Validate() error {
	if err := r.Usage.Validate(); err != nil {
		return err
	}
	if r.Text == "" && len(r.Changes) == 0 && len(r.Audio) == 0 {
		return ErrNoAnswer
	}
	if len(r.Audio) > 0 && r.AudioType == "" {
		return fmt.Errorf("%w: audio came back with no media type, so nothing can play it", ErrInvalid)
	}
	for i, c := range r.Changes {
		switch {
		case c.Area == "" || c.Setting == "":
			return fmt.Errorf("%w: change %d names no setting to change", ErrInvalid, i+1)
		case c.Why == "":
			return fmt.Errorf("%w: change %d to the %s gives no reason", ErrInvalid, i+1, c.Setting)
		}
	}
	return nil
}
