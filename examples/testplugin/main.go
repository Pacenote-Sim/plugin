// Command testplugin is a plugin that exercises every part of the contract and
// does nothing useful.
//
// That is the point. A host needs something honest to test against: something
// that answers when it should, reports what it spent, is handed a credential
// and proves it received one without printing it, and — on request — crashes,
// hangs past its deadline, or writes rubbish to standard error. A real plugin
// cannot be asked to do those things on demand, and a fake one built inside the
// host's own tests proves only that the host agrees with itself.
//
// It is also the shortest complete example of writing a plugin, so it is worth
// reading before the first real one.
//
// # Making it misbehave
//
// Behaviour comes from behaviour.json beside the binary, read fresh on every
// call so a test can change its mind mid-flight. A missing file is a plugin
// that behaves.
//
//	{
//	  "crash_at": "answer",   // "start", "settings", "notify" or "answer"
//	  "exit_code": 3,         // what it exits with when it crashes
//	  "hang": "10s",          // how long to ignore the deadline for
//	  "leak_secret": true,    // print the credential to standard error
//	  "leak_database": true,  // print its own connection string to standard error
//	  "stderr": "...",        // print this to standard error at startup
//	  "spend": 1200           // tokens to report having spent on each call
//	}
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pacenote-sim/plugin"
)

// behaviourName is the file beside the binary that says how to misbehave.
const behaviourName = "behaviour.json"

// behaviour is what that file holds. Every field is a thing a host has to
// survive, and the list is the list of tests the host owes.
type behaviour struct {
	// CrashAt names the moment to exit without warning: "start", "settings",
	// "notify" or "answer".
	CrashAt string `json:"crash_at,omitempty"`
	// ExitCode is what it exits with. Zero means 1, because an unannounced
	// exit that reports success is a different test.
	ExitCode int `json:"exit_code,omitempty"`
	// Hang is how long to sleep inside a call, ignoring the deadline.
	Hang string `json:"hang,omitempty"`
	// LeakSecret prints the credential to standard error, so the host can
	// prove that what it captures is redacted before it is stored or logged.
	LeakSecret bool `json:"leak_secret,omitempty"`
	// LeakDatabase prints the plugin's own connection string, password and
	// all, so the host can be shown scrubbing it out of what it stores. A
	// plugin that fails to connect and prints the string it tried is not a
	// far-fetched thing for an author to write.
	LeakDatabase bool `json:"leak_database,omitempty"`
	// Stderr is printed at startup, to give the host's "here is its last
	// output" something to show.
	Stderr string `json:"stderr,omitempty"`
	// Spend is the tokens to report against every call, for the cap.
	Spend int64 `json:"spend,omitempty"`
}

// hangFor is how long to sleep, or zero.
func (b behaviour) hangFor() time.Duration {
	d, err := time.ParseDuration(b.Hang)
	if err != nil {
		return 0
	}
	return d
}

// The settings this plugin declares. They are deliberately one of every kind
// the panel can render, because the host's settings tests need one of each.
const (
	settingGreeting = "greeting"
	settingAPIKey   = "api_key"
	settingLoudness = "loudness"
	settingEnabled  = "enabled"
	settingBudget   = "budget"
)

// testPlugin implements the contract.
type testPlugin struct{ dir string }

// Settings declares the form the operator fills in.
func (p testPlugin) Settings(context.Context) ([]plugin.Setting, error) {
	if p.read().CrashAt == "settings" {
		p.crash()
	}
	return []plugin.Setting{
		{
			Name:        settingGreeting,
			Label:       "Greeting",
			Help:        "What every answer starts with. It exists so a test can tell one configuration from another.",
			Kind:        plugin.KindText,
			Default:     "Right",
			Placeholder: "Right",
		},
		{
			Name:     settingAPIKey,
			Label:    "Vendor key",
			Help:     "Stored sealed and handed to the plugin one call at a time. Nothing ever reads it back.",
			Kind:     plugin.KindSecret,
			Required: false,
		},
		{
			Name:  settingLoudness,
			Label: "Loudness",
			Help:  "How much is said. It changes nothing except the length of the answer.",
			Kind:  plugin.KindChoice,
			Choices: []plugin.Choice{
				{Value: "quiet", Label: "Quiet", Note: "one clause"},
				{Value: "normal", Label: "Normal"},
				{Value: "loud", Label: "Loud", Note: "everything it was told"},
			},
			Default: "normal",
		},
		{
			Name:    settingEnabled,
			Label:   "Answer at all",
			Help:    "Off means every request comes back as nothing to say, which is a fallback the caller has to handle.",
			Kind:    plugin.KindBool,
			Default: "true",
		},
		{
			Name:    settingBudget,
			Label:   "Tokens per answer",
			Help:    "What this plugin claims to have spent on each call, so the daily cap has something to count.",
			Kind:    plugin.KindNumber,
			Default: "0",
		},
	}, nil
}

// Notify is told something happened and says what it cost.
func (p testPlugin) Notify(ctx context.Context, e plugin.Event) (plugin.Usage, error) {
	b := p.read()
	if b.CrashAt == "notify" {
		p.crash()
	}
	p.leak(b, e.Secrets)
	if err := p.wait(ctx, b); err != nil {
		return plugin.Usage{}, err
	}

	// Written to standard error and not standard output: standard output is
	// the handshake, and one line on it breaks the connection.
	fmt.Fprintf(os.Stderr, "testplugin: %s for %s, settings %v, credentials for %v\n",
		e.Kind, e.Driver.Slug, e.Settings.Names(), e.Secrets.Names())
	for _, line := range factLines(e.Lap, e.Stint) {
		fmt.Fprintln(os.Stderr, "testplugin:   "+line)
	}

	return p.usage(string(e.Kind), e.Settings, e.Secrets), nil
}

// factLines is every derived fact this delivery carried that a coaching plugin
// would reach for, one line each, so that an end-to-end run can read what
// actually arrived rather than infer it from the host's own logs.
//
// It prints what it was given and nothing else. A corner it was not told about
// does not appear, which is the same discipline a real cue is held to.
func factLines(lap *plugin.LapFacts, stint *plugin.StintFacts) []string {
	var out []string
	if lap != nil {
		out = append(out, fmt.Sprintf("lap %d in %d ms, %d corner(s)", lap.Number, lap.LapMs, len(lap.Corners)))
		for _, c := range lap.Corners {
			out = append(out, fmt.Sprintf(
				"corner: turn %d at %d‰, apex %d km/h against %d km/h, %d km/h down, brake %d%% at apex, throttle lag %d‰, pattern %q",
				c.Turn, c.ApexPct, c.ApexKmh, c.ReferenceApexKmh, c.DeficitKmh, c.BrakeAtApex, c.ThrottleLag, c.Pattern))
		}
	}
	if stint == nil {
		return out
	}
	out = append(out, fmt.Sprintf("stint: %d laps, %d%% consistent", stint.Laps, stint.ConsistencyPct))
	if stint.Setup == nil {
		return append(out, "setup: none published")
	}
	out = append(out, fmt.Sprintf("setup: revision %d, %d tyre(s), %d other value(s), rear wing %s",
		stint.Setup.UpdateCount, len(stint.Setup.Tyres), len(stint.Setup.Values), wingOf(stint.Setup)))
	for _, t := range stint.Setup.Tyres {
		out = append(out, fmt.Sprintf(
			"setup tyre %s: cold %.1f kPa, hot %.1f kPa, tread %.1f/%.1f/%.1f °C inner-to-outer, %.1f/%.1f/%.1f%% left",
			t.Wheel, t.ColdKpa, t.HotKpa, t.TempInnerC, t.TempMiddleC, t.TempOuterC,
			t.TreadInnerPct, t.TreadMiddlePct, t.TreadOuterPct))
	}
	for _, v := range stint.Setup.Values {
		out = append(out, fmt.Sprintf("setup value %s/%s = %q (%g %s)", v.Group, v.Name, v.Text, v.Number, v.Unit))
	}
	return out
}

// wingOf is the rear wing setting as published, or a word saying the car has
// none rather than an empty string that reads like a bug.
func wingOf(s *plugin.CarSetup) string {
	if s.RearWing == nil {
		return "(none)"
	}
	return s.RearWing.Text
}

// Answer is asked for something and answers it.
func (p testPlugin) Answer(ctx context.Context, r plugin.Request) (plugin.Response, error) {
	b := p.read()
	if b.CrashAt == "answer" {
		p.crash()
	}
	p.leak(b, r.Secrets)
	if err := p.wait(ctx, b); err != nil {
		return plugin.Response{}, err
	}
	if !r.Settings.Bool(settingEnabled) {
		return plugin.Response{}, plugin.ErrNoAnswer
	}

	greeting := r.Settings.String(settingGreeting)
	use := p.usage(string(r.Kind), r.Settings, r.Secrets)

	switch r.Kind {
	case plugin.RequestSetup:
		return plugin.Response{
			Kind: r.Kind,
			Changes: []plugin.SetupChange{{
				Area:      "front suspension",
				Setting:   "anti-roll bar",
				Direction: plugin.DirectionSofter,
				Amount:    "one click",
				Why:       greeting + " — " + setupWhy(r.Stint),
			}},
			Usage:         use,
			PromptVersion: "testplugin/1",
		}, nil

	case plugin.RequestCueRace, plugin.RequestCueTraining, plugin.RequestDebrief:
		return plugin.Response{
			Kind:          r.Kind,
			Text:          p.line(greeting, r),
			Usage:         use,
			PromptVersion: "testplugin/1",
		}, nil

	default:
		return plugin.Response{}, plugin.ErrUnsupported
	}
}

// setupWhy is the reason a setup change carries, taken from the measurement
// that is actually in the facts.
//
// It prefers the tread temperatures of the car's own setup sheet, because the
// spread across one tyre is the canonical camber reading and is about the wheel
// being changed, and falls back to the four-corner spread of the stint when the
// simulator published no setup. Both are numbers the plugin was handed; neither
// is a symptom it asked the driver to pick off a dropdown.
func setupWhy(s *plugin.StintFacts) string {
	if s.Setup != nil {
		if t, ok := s.Setup.TyreAt(plugin.WheelLF); ok && t.TempInnerC > 0 && t.TempOuterC > 0 {
			return fmt.Sprintf("the left front ran %.0f degrees hotter on the inner edge than on the outer.",
				t.TempInnerC-t.TempOuterC)
		}
	}
	return fmt.Sprintf("the hottest tyre is %.0f degrees above the coldest.", s.Tyres.SpreadC)
}

// line is the sentence, built only out of facts it was given. Inventing a turn
// number here would be inventing one in a real plugin, and the example should
// not teach that.
// ServeHTTP answers a request made of this plugin's own route, which is how the
// host's HTTP path is exercised end to end.
//
// It echoes what it was given rather than rendering anything, because what the
// tests are about is the boundary: which headers arrived, which cookies did
// not, who the host said the caller was. A page would hide all of that.
func (p testPlugin) ServeHTTP(ctx context.Context, r plugin.HTTPRequest) (plugin.HTTPResponse, error) {
	b := p.read()
	if err := p.wait(ctx, b); err != nil {
		return plugin.HTTPResponse{}, err
	}
	p.leak(b, r.Secrets)

	switch r.Path {
	case "/crash":
		p.crash()
	case "/sign-in":
		// The whole point of the arrangement: this plugin decides who somebody
		// is, and the host turns that into a session.
		return plugin.HTTPResponse{
			Status: http.StatusSeeOther,
			Header: http.Header{"Location": {r.Prefix + "/"}},
			SignIn: r.Query,
			Usage:  p.usage("serve", r.Settings, r.Secrets),
		}, nil
	case "/sign-out":
		return plugin.HTTPResponse{Status: http.StatusOK, SignOut: true}, nil
	}

	echo, err := json.Marshal(map[string]any{
		"method":  r.Method,
		"path":    r.Path,
		"query":   r.Query,
		"cookie":  r.Header.Get("Cookie"),
		"body":    string(r.Body),
		"caller":  r.Caller,
		"prefix":  r.Prefix,
		"baseurl": r.BaseURL,
	})
	if err != nil {
		return plugin.HTTPResponse{}, err
	}
	return plugin.HTTPResponse{
		Status: http.StatusOK,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   echo,
		Usage:  p.usage("serve", r.Settings, r.Secrets),
	}, nil
}

func (p testPlugin) line(greeting string, r plugin.Request) string {
	var b strings.Builder
	b.WriteString(greeting)
	b.WriteString(", ")
	b.WriteString(r.Driver.Name)
	if r.Lap != nil {
		fmt.Fprintf(&b, ": lap %d, %s", r.Lap.Number, r.Lap.SpokenLap)
		if len(r.Lap.Corners) > 0 {
			fmt.Fprintf(&b, ", Turn %d cost you %d k m per hour of apex speed",
				r.Lap.Corners[0].Turn, r.Lap.Corners[0].DeficitKmh)
		}
	}
	if r.Stint != nil {
		fmt.Fprintf(&b, ": %d laps, %d per cent consistent", r.Stint.Laps, r.Stint.ConsistencyPct)
	}
	b.WriteString(".")
	if r.Settings.String(settingLoudness) == "loud" {
		fmt.Fprintf(&b, " Configured with %v; credentials for %v.", r.Settings.Names(), r.Secrets.Names())
	}
	return b.String()
}

// usage is what to report. A credential being present is what makes this plugin
// pretend it called a vendor.
func (p testPlugin) usage(job string, v plugin.Values, s plugin.Secrets) plugin.Usage {
	spend, ok := v.Int(settingBudget)
	if !ok {
		spend = 0
	}
	if b := p.read().Spend; b > 0 {
		spend = int(b)
	}
	if spend <= 0 {
		return plugin.Usage{}
	}
	model := "none"
	if _, ok := s.Get(settingAPIKey); ok {
		model = "testplugin-fast"
	}
	in := int64(spend) / 4
	return plugin.Usage{
		Job:          job,
		Model:        model,
		InputTokens:  in,
		OutputTokens: int64(spend) - in,
	}
}

// leak prints the credential, on purpose, when asked. It is how the host proves
// that what it captures from a plugin's standard error is scrubbed before it is
// logged or stored.
func (p testPlugin) leak(b behaviour, s plugin.Secrets) {
	if !b.LeakSecret {
		return
	}
	for _, name := range s.Names() {
		fmt.Fprintf(os.Stderr, "testplugin: leaking %s=%s\n", name, s[name].Value())
	}
}

// wait sleeps for the configured hang, ignoring the deadline, which is what a
// plugin that has wedged looks like from the host's side. It returns the
// context's error if the context gives up first, which is what a well-behaved
// plugin does.
func (p testPlugin) wait(ctx context.Context, b behaviour) error {
	d := b.hangFor()
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// crash exits without cleaning up, which is what the host has to survive.
func (p testPlugin) crash() {
	b := p.read()
	code := b.ExitCode
	if code == 0 {
		code = 1
	}
	fmt.Fprintf(os.Stderr, "testplugin: crashing at %s as instructed\n", b.CrashAt)
	os.Exit(code)
}

// read loads the behaviour file fresh. A missing file is a plugin that behaves.
func (p testPlugin) read() behaviour {
	var b behaviour
	raw, err := os.ReadFile(filepath.Join(p.dir, behaviourName))
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return b
	}
	_ = json.Unmarshal(raw, &b)
	return b
}

func main() {
	dir := "."
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	p := testPlugin{dir: dir}

	b := p.read()
	if b.Stderr != "" {
		fmt.Fprintln(os.Stderr, b.Stderr)
	}
	// Whether a database arrived is printed either way, so a test can tell the
	// difference between a plugin that was given one and a plugin that was not.
	// The string itself is only printed when asked for, because printing a
	// credential by default would make every other test's output a hazard.
	if dsn, err := plugin.DatabaseURL(); err == nil {
		fmt.Fprintln(os.Stderr, "testplugin: database yes")
		if b.LeakDatabase {
			fmt.Fprintln(os.Stderr, "testplugin: leaking database "+dsn)
		}
	} else {
		fmt.Fprintln(os.Stderr, "testplugin: database no")
	}
	if b.CrashAt == "start" {
		p.crash()
	}
	plugin.Serve(p)
}
