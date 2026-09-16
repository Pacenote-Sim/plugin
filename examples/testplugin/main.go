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
// # What it answers
//
// Three kinds, spelled with whatever name the host installed it under:
//
//	<name>.echo    answers with what it was asked, who asked, and how deep
//	<name>.ask     asks whatever the payload names — {"kind": ..., "payload": ...} — and relays the answer
//	<name>.chain   asks itself the same question, until the host refuses; answers with how deep it got
//
// The last two are how a host proves its broker: one plugin reaching another
// through it, and a loop being stopped rather than run.
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
	"sync"
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

// The host this plugin may ask through, kept from Connected. It is a package
// variable because testPlugin is a value and the host is handed over once.
var (
	hostMu  sync.Mutex
	theHost plugin.Host
)

// Connected keeps the host. It runs once, before Settings is answered.
func (p testPlugin) Connected(h plugin.Host) {
	hostMu.Lock()
	defer hostMu.Unlock()
	theHost = h
}

func connectedHost() (plugin.Host, bool) {
	hostMu.Lock()
	defer hostMu.Unlock()
	return theHost, theHost != nil
}

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
//
// The corner analysis and the setup arrive as the documents the client sent.
// This plugin decodes them into shapes of its own rather than importing the
// protocol module, which is what a plugin with no other reason to depend on it
// would do; the fields it names are the ones it uses and the rest are ignored.
func factLines(lap *plugin.LapFacts, stint *plugin.StintFacts) []string {
	var out []string
	if lap != nil {
		corners := cornersOf(lap)
		out = append(out, fmt.Sprintf("lap %d in %d ms, %d corner(s)", lap.Number, lap.LapMs, len(corners)))
		for _, c := range corners {
			out = append(out, fmt.Sprintf(
				"corner: turn %d at %d‰, apex %d km/h against %d km/h, %d km/h down, brake %d%% at apex, throttle lag %d‰, pattern %q",
				c.Turn, c.ApexPct, c.ApexKmh, c.RefApexKmh, c.DeficitKmh, c.BrakeAtApex, c.ThrottleLag, c.Pattern))
		}
	}
	if stint == nil {
		return out
	}
	out = append(out, fmt.Sprintf("stint: %d laps, %d%% consistent", stint.Laps, stint.ConsistencyPct))
	s := setupOf(stint)
	if s == nil {
		return append(out, "setup: none published")
	}
	out = append(out, fmt.Sprintf("setup: revision %d, %d tyre(s), %d other value(s), rear wing %s",
		s.UpdateCount, len(s.Tyres), len(s.Values), wingOf(s)))
	for _, t := range s.Tyres {
		out = append(out, fmt.Sprintf(
			"setup tyre %s: cold %.1f kPa, hot %.1f kPa, tread %.1f/%.1f/%.1f °C inner-to-outer, %.1f/%.1f/%.1f%% left",
			t.Wheel, t.ColdKpa, t.HotKpa, t.TempInnerC, t.TempMiddleC, t.TempOuterC,
			t.TreadInnerPct, t.TreadMiddlePct, t.TreadOuterPct))
	}
	for _, v := range s.Values {
		out = append(out, fmt.Sprintf("setup value %s/%s = %q (%g %s)", v.Group, v.Name, v.Text, v.Number, v.Unit))
	}
	return out
}

// corner is as much of the wire's corner analysis as this plugin reads.
type corner struct {
	Turn        int    `json:"turn"`
	ApexPct     int    `json:"apex_pct"`
	ApexKmh     int    `json:"apex_kmh"`
	RefApexKmh  int    `json:"ref_apex_kmh"`
	DeficitKmh  int    `json:"deficit_kmh"`
	BrakeAtApex int    `json:"brake_at_apex"`
	ThrottleLag int    `json:"throttle_lag"`
	Pattern     string `json:"pattern"`
}

// setupSheet is as much of the wire's car setup as this plugin reads.
type setupSheet struct {
	UpdateCount int `json:"update_count"`
	Tyres       []struct {
		Wheel          string  `json:"wheel"`
		ColdKpa        float64 `json:"cold_kpa"`
		HotKpa         float64 `json:"hot_kpa"`
		TempInnerC     float64 `json:"temp_inner_c"`
		TempMiddleC    float64 `json:"temp_middle_c"`
		TempOuterC     float64 `json:"temp_outer_c"`
		TreadInnerPct  float64 `json:"tread_inner_pct"`
		TreadMiddlePct float64 `json:"tread_middle_pct"`
		TreadOuterPct  float64 `json:"tread_outer_pct"`
	} `json:"tyres"`
	RearWing *struct {
		Text string `json:"text"`
	} `json:"rear_wing"`
	Values []struct {
		Group  string  `json:"group"`
		Name   string  `json:"name"`
		Text   string  `json:"text"`
		Number float64 `json:"number"`
		Unit   string  `json:"unit"`
	} `json:"values"`
}

// cornersOf decodes the lap's corner document, or nothing. A document this
// plugin cannot read is treated as no corners rather than as an error, because
// the lap still happened and the rest of the facts are still good.
func cornersOf(lap *plugin.LapFacts) []corner {
	if lap == nil || len(lap.Corners) == 0 {
		return nil
	}
	var out []corner
	if err := json.Unmarshal(lap.Corners, &out); err != nil {
		return nil
	}
	return out
}

// setupOf decodes the stint's setup document, or nil when none was published.
func setupOf(stint *plugin.StintFacts) *setupSheet {
	if stint == nil || len(stint.Setup) == 0 {
		return nil
	}
	var out setupSheet
	if err := json.Unmarshal(stint.Setup, &out); err != nil {
		return nil
	}
	return &out
}

// wingOf is the rear wing setting as published, or a word saying the car has
// none rather than an empty string that reads like a bug.
func wingOf(s *setupSheet) string {
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

	// The kind is "<name>.<what>", and what this plugin was installed as is the
	// host's business: only the part after the dot is this plugin's.
	switch what := strings.TrimPrefix(string(r.Kind), r.Kind.Plugin()+"."); what {
	case "echo":
		return echo(greeting, r, use)
	case "ask":
		return relay(ctx, r, use)
	case "chain":
		return chain(ctx, r, use)
	default:
		return plugin.Response{}, plugin.ErrUnsupported
	}
}

// echo answers with what it was asked and what it was told about the asking,
// so a host's tests can read both back. The names of the settings and the
// credentials are in it — the values never are.
func echo(greeting string, r plugin.Request, use plugin.Usage) (plugin.Response, error) {
	var payload any
	if len(r.Payload) > 0 {
		payload = r.Payload
	}
	out, err := json.Marshal(map[string]any{
		"greeting":    greeting,
		"from":        r.From,
		"hops":        r.Hops,
		"echo":        payload,
		"settings":    r.Settings.Names(),
		"credentials": r.Secrets.Names(),
	})
	if err != nil {
		return plugin.Response{}, err
	}
	return plugin.Response{Kind: r.Kind, Payload: out, Usage: use}, nil
}

// relay asks whatever the payload names and answers with what came back — or
// with the error, which is the point: a host's test reads the refusal from the
// asking side.
func relay(ctx context.Context, r plugin.Request, use plugin.Usage) (plugin.Response, error) {
	var ask struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(r.Payload, &ask); err != nil {
		return plugin.Response{}, fmt.Errorf("%w: the payload does not say what to ask: %w", plugin.ErrInvalid, err)
	}
	host, ok := connectedHost()
	if !ok {
		return plugin.Response{}, fmt.Errorf("%w: this plugin was never connected to the host", plugin.ErrUnavailable)
	}
	out, err := host.Ask(ctx, ask.Kind, ask.Payload)
	if err != nil {
		return plugin.Response{}, err
	}
	return plugin.Response{Kind: r.Kind, Payload: out, Usage: use}, nil
}

// chain asks the same question on — of the partner the payload names, or of
// itself when it names none — and answers with how deep the host let it go. The
// host refusing is the expected end, not a failure.
func chain(ctx context.Context, r plugin.Request, use plugin.Usage) (plugin.Response, error) {
	host, ok := connectedHost()
	if !ok {
		return plugin.Response{}, fmt.Errorf("%w: this plugin was never connected to the host", plugin.ErrUnavailable)
	}
	var link struct {
		Partner string `json:"partner"`
	}
	_ = json.Unmarshal(r.Payload, &link)
	me := r.Kind.Plugin()
	next, onward := string(r.Kind), r.Payload
	if link.Partner != "" {
		next = link.Partner + ".chain"
		onward = json.RawMessage(`{"partner":"` + me + `"}`)
	}
	if out, err := host.Ask(ctx, next, onward); err == nil {
		// Somebody deeper answered: their depth is the deepest.
		return plugin.Response{Kind: r.Kind, Payload: out, Usage: use}, nil
	}
	out, err := json.Marshal(map[string]int{"reached": r.Hops})
	if err != nil {
		return plugin.Response{}, err
	}
	return plugin.Response{Kind: r.Kind, Payload: out, Usage: use}, nil
}

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
