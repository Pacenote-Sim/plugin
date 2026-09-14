package plugin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is what a setting holds, which is what the panel renders it as.
type Kind string

// The five kinds. There are five rather than fifteen because every one of them
// is a control an operator has to understand without documentation, and because
// a kind the panel cannot draw is a setting nobody can fill in.
const (
	// KindText is one line of text.
	KindText Kind = "text"
	// KindSecret is a credential. It is written once and never read back: the
	// core seals it with the data key, the panel shows that there is one, and
	// the value reaches the plugin only in [Request.Secrets] or
	// [Event.Secrets], at call time.
	KindSecret Kind = "secret"
	// KindNumber is a whole number.
	KindNumber Kind = "number"
	// KindBool is a switch. Its value is "true" or "false".
	KindBool Kind = "bool"
	// KindChoice is one of [Setting.Choices].
	KindChoice Kind = "choice"
)

// Valid reports whether k is one of the five kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindText, KindSecret, KindNumber, KindBool, KindChoice:
		return true
	default:
		return false
	}
}

// Choice is one option of a [KindChoice] setting.
type Choice struct {
	// Value is what is stored. It is stable: renaming a label is cosmetic,
	// renaming a value orphans what the operator already chose.
	Value string `json:"value"`
	// Label is what the operator reads.
	Label string `json:"label"`
	// Note is the half-line under the label, for the difference between two
	// options that the labels alone do not make obvious. It may be empty.
	Note string `json:"note,omitempty"`
}

// Setting is one thing the operator has to fill in, declared by the plugin and
// rendered by the panel.
//
// A plugin declares these rather than reading a configuration file of its own,
// so that an operator configures every plugin in the same place as everything
// else, and so that a credential is sealed by the core rather than left in a
// file beside a binary.
type Setting struct {
	// Name is the key the value is stored under and the key the plugin reads
	// it back by. It is lowercase letters, digits and underscores, and it is
	// permanent: changing it loses whatever the operator had set.
	Name string `json:"name"`
	// Label is the field's name in the panel, in sentence case, without a
	// trailing colon.
	Label string `json:"label"`
	// Help is the line under the field. It is what an operator who has never
	// seen this plugin needs in order to answer, and it is worth writing
	// properly: it is the only documentation most of them will read.
	Help string `json:"help,omitempty"`
	// Kind is what it holds.
	Kind Kind `json:"kind"`
	// Required refuses an empty value. A plugin with an unfilled required
	// setting is shown as needing attention and is not called.
	Required bool `json:"required,omitempty"`
	// Default is the value used when the operator has set none. It must be
	// empty for [KindSecret]: a default credential is not a thing.
	Default string `json:"default,omitempty"`
	// Choices are the options of a [KindChoice] setting, in the order the
	// panel shows them. Ignored for every other kind.
	Choices []Choice `json:"choices,omitempty"`
	// Placeholder is the grey text in an empty field, for the shape of the
	// answer rather than an example that someone will paste verbatim.
	Placeholder string `json:"placeholder,omitempty"`
}

// Validate reports whether this declaration can be rendered and stored.
func (s Setting) Validate() error {
	switch {
	case !validName(s.Name):
		return fmt.Errorf("%w: %q is not a settings name — use lowercase letters, digits and underscores", ErrInvalid, s.Name)
	case strings.TrimSpace(s.Label) == "":
		return fmt.Errorf("%w: the %q setting has no label, so the panel has nothing to call it", ErrInvalid, s.Name)
	case !s.Kind.Valid():
		return fmt.Errorf("%w: the %q setting is of kind %q, which the panel cannot render", ErrInvalid, s.Name, s.Kind)
	case s.Kind == KindSecret && s.Default != "":
		return fmt.Errorf("%w: the %q setting is a credential and carries a default, which is never right", ErrInvalid, s.Name)
	case s.Kind == KindChoice && len(s.Choices) == 0:
		return fmt.Errorf("%w: the %q setting offers a choice of nothing", ErrInvalid, s.Name)
	}
	if s.Kind != KindChoice && len(s.Choices) > 0 {
		return fmt.Errorf("%w: the %q setting is of kind %q and carries choices, which nothing will show", ErrInvalid, s.Name, s.Kind)
	}
	seen := make(map[string]bool, len(s.Choices))
	for _, c := range s.Choices {
		switch {
		case c.Value == "":
			return fmt.Errorf("%w: the %q setting has a choice with no value", ErrInvalid, s.Name)
		case strings.TrimSpace(c.Label) == "":
			return fmt.Errorf("%w: the %q choice of the %q setting has no label", ErrInvalid, c.Value, s.Name)
		case seen[c.Value]:
			return fmt.Errorf("%w: the %q setting offers %q twice", ErrInvalid, s.Name, c.Value)
		}
		seen[c.Value] = true
	}
	if s.Default != "" {
		if err := s.check(s.Default); err != nil {
			return fmt.Errorf("%w: the default of the %q setting is not one it accepts: %w", ErrInvalid, s.Name, err)
		}
	}
	return nil
}

// check reports whether v is a value this setting accepts. It does not judge an
// empty value: [ValidateValues] does that, because whether empty is allowed is
// [Setting.Required] and not the kind.
func (s Setting) check(v string) error {
	switch s.Kind {
	case KindNumber:
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("%w: %q is not a whole number", ErrInvalid, v)
		}
	case KindBool:
		if v != "true" && v != "false" {
			return fmt.Errorf(`%w: %q is not "true" or "false"`, ErrInvalid, v)
		}
	case KindChoice:
		for _, c := range s.Choices {
			if c.Value == v {
				return nil
			}
		}
		return fmt.Errorf("%w: %q is not one of the choices", ErrInvalid, v)
	case KindText, KindSecret:
	}
	return nil
}

// ValidateSettings checks a whole declaration: every setting on its own, and no
// two of them sharing a name.
func ValidateSettings(settings []Setting) error {
	seen := make(map[string]bool, len(settings))
	for i := range settings {
		if err := settings[i].Validate(); err != nil {
			return err
		}
		if seen[settings[i].Name] {
			return fmt.Errorf("%w: the %q setting is declared twice", ErrInvalid, settings[i].Name)
		}
		seen[settings[i].Name] = true
	}
	return nil
}

// Values are the operator's answers, keyed by [Setting.Name].
//
// Secrets are not here. A [KindSecret] setting's value reaches the plugin as a
// [Secret] in [Request.Secrets] or [Event.Secrets], and what appears here for
// such a setting is nothing at all — which is what lets a plugin log its whole
// settings map while debugging.
type Values map[string]string

// String is the value of the named setting, or empty when it has none.
func (v Values) String(name string) string { return v[name] }

// Int is the value of a [KindNumber] setting. The second result is false when
// there is no value or it is not a number, which for a validated setting means
// the operator left it empty and the plugin declared no default.
func (v Values) Int(name string) (int, bool) {
	n, err := strconv.Atoi(v[name])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Bool is the value of a [KindBool] setting. Anything that is not "true" is
// false, because a switch has no third position.
func (v Values) Bool(name string) bool { return v[name] == "true" }

// Names lists the settings that have a value, in order.
func (v Values) Names() []string {
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidateValues checks the operator's answers against the declaration and
// fills in the defaults. The result is what a plugin is given: every declared
// non-secret setting that has a value, and nothing that was not declared.
//
// A value for a setting the plugin does not declare is dropped rather than
// refused. That is what makes an upgrade that removes a setting survivable: the
// row stays in the database until the operator saves the form again, and
// nothing breaks in the meantime.
func ValidateValues(settings []Setting, in Values) (Values, error) {
	out := make(Values, len(settings))
	for i := range settings {
		s := &settings[i]
		v, ok := in[s.Name]
		if !ok || v == "" {
			v = s.Default
		}
		if v == "" {
			if s.Required {
				return nil, fmt.Errorf("%w: %s has to be filled in", ErrNotConfigured, s.Label)
			}
			continue
		}
		if s.Kind == KindSecret {
			// A credential does not travel with the settings. It reaches the
			// plugin as a Secret at call time, and putting it here as well
			// would undo the point of the type.
			continue
		}
		if err := s.check(v); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, s.Label, err)
		}
		out[s.Name] = v
	}
	return out, nil
}

// ValidName reports whether s is a name this contract accepts — for a plugin,
// for a setting, and for anything else the host has to put in a URL, a column
// or a role. Lowercase letters, digits, underscores and hyphens, starting with
// a letter.
//
// It is exported because the host has to apply the same rule in places the
// plugin never sees: the address a plugin is mounted at, and the name of the
// PostgreSQL role it owns. Two spellings of one rule is one spelling too many.
func ValidName(s string) bool { return validName(s) }

// validName is the rule itself, narrow so that a name is usable as a key, a
// directory and a column value without anything having to quote it.
func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9', c == '_', c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
