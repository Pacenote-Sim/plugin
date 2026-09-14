package plugin

import "fmt"

// Usage is what one call cost the operator.
//
// The plugin reports it; the core records it and enforces the daily cap. That
// direction is deliberate and it is the one thing in this contract worth being
// blunt about: **the cap belongs to the core**. A plugin deciding how much of
// somebody else's money to spend is backwards, so a plugin is told its ceiling
// for a call ([Request.TokenCeiling]), it reports what it actually used, and
// the decision to call it again is not its own.
//
// A plugin that spends nothing — one that posts to a chat room, one that
// answered from its own cache — returns the zero value, and the core records
// nothing.
type Usage struct {
	// Job is what the call was for. For a request it is the request kind; for
	// an event a plugin names its own work. It is what the operator sees when
	// they ask where the money went, so "cue.training" is useful and "call" is
	// not.
	Job string `json:"job,omitempty"`
	// Model is the model the tokens were spent on, when there was one. It is
	// free text because the plugin knows the vendor and the core does not.
	Model string `json:"model,omitempty"`
	// InputTokens and OutputTokens are counted the way the vendor counts them,
	// so that the figure in the panel matches the figure on the bill.
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	// Cached reports that the answer came from the plugin's own cache. It is
	// recorded because a cache that is not measured is a cache nobody can tell
	// is working.
	Cached bool `json:"cached,omitempty"`
}

// Total is what the daily cap is measured against: every token the operator
// paid for, in or out.
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Spent reports whether this call cost anything at all.
func (u Usage) Spent() bool { return u.Total() > 0 }

// Validate refuses a report the core cannot record. A negative count is the
// only way a plugin could spend the operator's allowance backwards, and a spend
// with no job named is a row in the panel that answers nothing.
func (u Usage) Validate() error {
	switch {
	case u.InputTokens < 0 || u.OutputTokens < 0:
		return fmt.Errorf("%w: a call cannot use a negative number of tokens", ErrInvalid)
	case u.Spent() && u.Job == "":
		return fmt.Errorf("%w: %d tokens were reported with no job to record them against", ErrInvalid, u.Total())
	default:
		return nil
	}
}
