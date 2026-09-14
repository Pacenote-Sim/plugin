package plugin_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestValidateSettings covers the declaration a plugin makes. Every case here
// is a mistake a plugin author can make once and never see again, because the
// panel would render something the operator cannot use.
func TestValidateSettings(t *testing.T) {
	t.Parallel()

	good := plugin.Setting{Name: "api_key", Label: "Vendor key", Kind: plugin.KindSecret}

	cases := []struct {
		name     string
		settings []plugin.Setting
		wantErr  string
	}{
		{
			name:     "a plain declaration is accepted",
			settings: []plugin.Setting{good},
		},
		{
			name: "every kind is accepted",
			settings: []plugin.Setting{
				{Name: "a", Label: "A", Kind: plugin.KindText, Default: "x"},
				{Name: "b", Label: "B", Kind: plugin.KindNumber, Default: "12"},
				{Name: "c", Label: "C", Kind: plugin.KindBool, Default: "true"},
				{Name: "d", Label: "D", Kind: plugin.KindChoice, Default: "one", Choices: []plugin.Choice{{Value: "one", Label: "One"}}},
				good,
			},
		},
		{
			name:     "a name the panel cannot key on is refused",
			settings: []plugin.Setting{{Name: "API Key", Label: "Vendor key", Kind: plugin.KindText}},
			wantErr:  "is not a settings name",
		},
		{
			name:     "a nameless field is refused",
			settings: []plugin.Setting{{Name: "api_key", Kind: plugin.KindText}},
			wantErr:  "has no label",
		},
		{
			name:     "a kind nothing can render is refused",
			settings: []plugin.Setting{{Name: "api_key", Label: "Vendor key", Kind: "colour"}},
			wantErr:  "which the panel cannot render",
		},
		{
			name:     "a credential with a default is refused",
			settings: []plugin.Setting{{Name: "api_key", Label: "Vendor key", Kind: plugin.KindSecret, Default: "sk-ant-oops"}},
			wantErr:  "carries a default",
		},
		{
			name:     "a choice of nothing is refused",
			settings: []plugin.Setting{{Name: "mode", Label: "Mode", Kind: plugin.KindChoice}},
			wantErr:  "offers a choice of nothing",
		},
		{
			name:     "choices on a kind that cannot show them are refused",
			settings: []plugin.Setting{{Name: "mode", Label: "Mode", Kind: plugin.KindText, Choices: []plugin.Choice{{Value: "a", Label: "A"}}}},
			wantErr:  "carries choices, which nothing will show",
		},
		{
			name:     "the same choice twice is refused",
			settings: []plugin.Setting{{Name: "mode", Label: "Mode", Kind: plugin.KindChoice, Choices: []plugin.Choice{{Value: "a", Label: "A"}, {Value: "a", Label: "Again"}}}},
			wantErr:  "twice",
		},
		{
			name:     "a default that is not a choice is refused",
			settings: []plugin.Setting{{Name: "mode", Label: "Mode", Kind: plugin.KindChoice, Default: "z", Choices: []plugin.Choice{{Value: "a", Label: "A"}}}},
			wantErr:  "is not one it accepts",
		},
		{
			name:     "a number setting with a word for a default is refused",
			settings: []plugin.Setting{{Name: "budget", Label: "Budget", Kind: plugin.KindNumber, Default: "lots"}},
			wantErr:  "is not one it accepts",
		},
		{
			name:     "the same setting declared twice is refused",
			settings: []plugin.Setting{good, good},
			wantErr:  "declared twice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			err := plugin.ValidateSettings(tc.settings)
			if tc.wantErr == "" {
				r.NoError(err)
				return
			}
			r.ErrorIs(err, plugin.ErrInvalid)
			r.ErrorContains(err, tc.wantErr)
		})
	}
}

// TestValidateValues covers the operator's answers being checked against the
// declaration, which is what the panel does on save and what the host does
// before every call.
func TestValidateValues(t *testing.T) {
	t.Parallel()

	decl := []plugin.Setting{
		{Name: "endpoint", Label: "Endpoint", Kind: plugin.KindText, Required: true},
		{Name: "greeting", Label: "Greeting", Kind: plugin.KindText, Default: "Right"},
		{Name: "budget", Label: "Budget", Kind: plugin.KindNumber, Default: "0"},
		{Name: "enabled", Label: "Enabled", Kind: plugin.KindBool, Default: "true"},
		{Name: "mode", Label: "Mode", Kind: plugin.KindChoice, Choices: []plugin.Choice{{Value: "fast", Label: "Fast"}}},
		{Name: "api_key", Label: "Vendor key", Kind: plugin.KindSecret},
	}

	cases := []struct {
		name    string
		in      plugin.Values
		want    plugin.Values
		wantErr error
		errText string
	}{
		{
			name: "defaults fill in what the operator left alone",
			in:   plugin.Values{"endpoint": "https://example.invalid"},
			want: plugin.Values{"endpoint": "https://example.invalid", "greeting": "Right", "budget": "0", "enabled": "true"},
		},
		{
			name: "what the operator set wins over the default",
			in:   plugin.Values{"endpoint": "https://example.invalid", "greeting": "Listen", "mode": "fast"},
			want: plugin.Values{"endpoint": "https://example.invalid", "greeting": "Listen", "budget": "0", "enabled": "true", "mode": "fast"},
		},
		{
			name:    "a required setting left empty is not a call worth making",
			in:      plugin.Values{},
			wantErr: plugin.ErrNotConfigured,
			errText: "Endpoint has to be filled in",
		},
		{
			name:    "a number that is not one is refused",
			in:      plugin.Values{"endpoint": "https://example.invalid", "budget": "plenty"},
			wantErr: plugin.ErrInvalid,
			errText: "is not a whole number",
		},
		{
			name:    "a choice nobody offered is refused",
			in:      plugin.Values{"endpoint": "https://example.invalid", "mode": "slow"},
			wantErr: plugin.ErrInvalid,
			errText: "is not one of the choices",
		},
		{
			name:    "a switch in a third position is refused",
			in:      plugin.Values{"endpoint": "https://example.invalid", "enabled": "maybe"},
			wantErr: plugin.ErrInvalid,
			errText: `is not "true" or "false"`,
		},
		{
			name: "a credential never travels with the settings",
			in:   plugin.Values{"endpoint": "https://example.invalid", "api_key": "sk-ant-oops"},
			want: plugin.Values{"endpoint": "https://example.invalid", "greeting": "Right", "budget": "0", "enabled": "true"},
		},
		{
			name: "a value for a setting that no longer exists is dropped, not refused",
			in:   plugin.Values{"endpoint": "https://example.invalid", "removed_last_version": "x"},
			want: plugin.Values{"endpoint": "https://example.invalid", "greeting": "Right", "budget": "0", "enabled": "true"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			got, err := plugin.ValidateValues(decl, tc.in)
			if tc.wantErr != nil {
				r.ErrorIs(err, tc.wantErr)
				r.ErrorContains(err, tc.errText)
				r.Nil(got)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

// TestValuesAccessors covers reading a value back the way a plugin does.
func TestValuesAccessors(t *testing.T) {
	t.Parallel()

	v := plugin.Values{"greeting": "Right", "budget": "1200", "enabled": "true", "off": "false"}

	t.Run("a string is itself", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		r.Equal("Right", v.String("greeting"))
		r.Empty(v.String("missing"))
	})

	t.Run("a number reports whether there was one", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		n, ok := v.Int("budget")
		r.True(ok)
		r.Equal(1200, n)

		_, ok = v.Int("greeting")
		r.False(ok)

		_, ok = v.Int("missing")
		r.False(ok)
	})

	t.Run("a switch has two positions", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)
		r.True(v.Bool("enabled"))
		r.False(v.Bool("off"))
		r.False(v.Bool("missing"))
	})
}

// Names is what a plugin iterates when it wants its own settings in a stable
// order, so it is sorted and not map order.
func TestTheSettingsThatHaveAValue(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	r.Empty(plugin.Values(nil).Names())
	r.Equal([]string{"api_base", "mode", "voice"},
		plugin.Values{"voice": "x", "api_base": "y", "mode": "z"}.Names(),
		"the order is not the same twice running")
}

// ValidName is exported so the host applies the same rule in places the plugin
// never sees — the URL it is mounted at, the PostgreSQL role it owns.
func TestWhatIsAValidName(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	for _, ok := range []string{"results", "r", "my-plugin", "my_plugin", "a1", "a-b_c9"} {
		r.True(plugin.ValidName(ok), "%q was refused", ok)
	}
	for _, bad := range []string{
		"", "1results", "-results", "_results", "Results", "my plugin",
		"my.plugin", "my/plugin", "my:plugin", strings.Repeat("a", 65),
	} {
		r.False(plugin.ValidName(bad), "%q was accepted", bad)
	}
	r.True(plugin.ValidName(strings.Repeat("a", 64)), "64 is the limit, not one under it")
}

// A choice list the panel could not render. Both are the plugin author's
// mistake and both are caught before an operator sees a blank dropdown.
func TestAChoiceListThePanelCouldNotRender(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	base := func(choices []plugin.Choice) plugin.Setting {
		return plugin.Setting{Name: "mode", Label: "Mode", Kind: plugin.KindChoice, Choices: choices}
	}
	r.NoError(base([]plugin.Choice{{Value: "live", Label: "Live"}}).Validate())
	r.ErrorIs(base([]plugin.Choice{{Value: "", Label: "Live"}}).Validate(), plugin.ErrInvalid)
	r.ErrorIs(base([]plugin.Choice{{Value: "live", Label: "   "}}).Validate(), plugin.ErrInvalid)
}
