// Package plugin is the contract between a Pacenote server and a plugin.
//
// It holds the interface and nothing else: no host, no implementation, no
// database, no HTTP. A plugin author depends on this module and never on a
// server, which is the point — the same plugin runs on the open community
// edition and on the closed enterprise one, because both are only hosts
// implementing what is here.
//
// The module is licensed Apache-2.0 while the community server is GPL-3, and
// that is deliberate rather than an oversight. A plugin is a separate process
// talking to the server over a local channel, so it is not linked into the
// server and is not a derivative work of it; depending on an Apache-2.0
// interface module leaves a plugin author free to pick any licence, including a
// closed commercial one.
//
// # What a plugin can do
//
// Five things, which are the five a demanding plugin — a coach that turns
// telemetry into a sentence a driver hears mid-corner — actually needs:
//
//   - Be told something happened. [Event], fire and forget.
//   - Be asked for something, with the host waiting. [Request] and [Response].
//   - Be lent one of the operator's credentials for a call. [Secret].
//   - Report what a call cost. [Usage]. The daily cap belongs to the core.
//   - Declare what the operator must configure. [Setting].
//
// Nothing else is here yet. Serving an endpoint, adding a page to the panel and
// enriching data on the way in are in the plan and are not in version 1,
// because an extension point with no user is a guess.
//
// # The governing rule
//
// The plugin receives facts, never traces. Every number in [LapFacts] and
// [StintFacts] was calculated exactly before the plugin saw it — apex speeds,
// corner deficits, consistency, fuel per lap. A plugin narrates; it does not
// compute. That buys accuracy, because something that cannot do arithmetic
// cannot get it wrong, and it buys latency and cost, because a lap trace is
// three hundred samples and the facts are a dozen numbers.
//
// # Writing one
//
// A plugin is a program with two files: a binary and a [Manifest] beside it, in
// a directory named after the plugin, under the server's plugin directory.
//
//	pacenote-data/plugins/loudmouth/plugin.json
//	pacenote-data/plugins/loudmouth/loudmouth
//
// The manifest says what it is and what it was built against:
//
//	{
//	  "name": "loudmouth",
//	  "version": "1.0.0",
//	  "author": "Someone",
//	  "description": "Says something in the team channel when a driver sets a personal best.",
//	  "interface_version": 1,
//	  "capabilities": {
//	    "events": ["lap.completed"],
//	    "network": true,
//	    "reads_driver_data": true
//	  }
//	}
//
// The program implements [Plugin] and calls [Serve]:
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"net/http"
//		"strings"
//
//		"github.com/pacenote-sim/plugin"
//	)
//
//	type loudmouth struct{}
//
//	// Settings is what the operator fills in, rendered by the panel.
//	func (loudmouth) Settings(context.Context) ([]plugin.Setting, error) {
//		return []plugin.Setting{{
//			Name:     "webhook",
//			Label:    "Channel webhook",
//			Help:     "The address the message is posted to. Create it in your chat service's channel settings.",
//			Kind:     plugin.KindSecret,
//			Required: true,
//		}, {
//			Name:    "only_personal_bests",
//			Label:   "Only personal bests",
//			Help:    "Off means every clean lap, which is a lot of messages on a full grid.",
//			Kind:    plugin.KindBool,
//			Default: "true",
//		}}, nil
//	}
//
//	// Notify is told a lap was completed. It does not have to be quick: the
//	// server dispatched it and carried on.
//	func (loudmouth) Notify(ctx context.Context, e plugin.Event) (plugin.Usage, error) {
//		if e.Kind != plugin.EventLapCompleted {
//			return plugin.Usage{}, nil
//		}
//		if e.Settings.Bool("only_personal_bests") && !e.Lap.PersonalBest {
//			return plugin.Usage{}, nil
//		}
//		hook, ok := e.Secrets.Get("webhook")
//		if !ok {
//			return plugin.Usage{}, plugin.ErrNotConfigured
//		}
//		line := fmt.Sprintf("%s: %s at %s", e.Driver.Name, e.Lap.SpokenLap, e.Session.Track)
//		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.Value(), strings.NewReader(line))
//		if err != nil {
//			return plugin.Usage{}, err
//		}
//		res, err := http.DefaultClient.Do(req)
//		if err != nil {
//			return plugin.Usage{}, err
//		}
//		defer res.Body.Close()
//		// Nothing was bought from a vendor by the token, so nothing is metered.
//		return plugin.Usage{}, nil
//	}
//
//	// Answer is not something this plugin does, and its manifest says so.
//	func (loudmouth) Answer(context.Context, plugin.Request) (plugin.Response, error) {
//		return plugin.Response{}, plugin.ErrUnsupported
//	}
//
//	func main() { plugin.Serve(loudmouth{}) }
//
// Note what is not in that program: no configuration file, no place a
// credential is stored, no decision about how much of the operator's money to
// spend. The core owns all three.
//
// # Rules a plugin author has to know
//
// Standard output belongs to the handshake until [Serve] has taken it over.
// Write one line to it before that and the host cannot talk to the plugin, and
// the error will not say why. Once serving, both streams are yours: the host
// captures the last of what a plugin prints and shows it to the operator when
// something goes wrong. Standard error is still the right place to write.
//
// Deadlines are real. The host stops waiting when the deadline passes, whatever
// the plugin is doing, and reports the plugin as having missed it. Work that
// continues afterwards is work nobody reads.
//
// Credentials are lent, not given. [Secret] renders as "[redacted]" when
// printed, logged or encoded, so the usual accidents cannot leak one. Reading
// the value with [Secret.Value] and then storing it somewhere is not an
// accident, and it is outside what this contract permits.
//
// Every method may be called concurrently. Guard your own state.
//
// # Versioning
//
// [InterfaceVersion] is this contract's version. A host declares it, a plugin
// declares in its manifest the one it was built against, and they must match
// exactly. A mismatch refuses to start with a message naming both versions and
// what to do: there is no degraded mode, because a plugin quietly missing a
// fact is a coach telling a driver something wrong hours after anyone would
// connect it to an upgrade.
//
// # A worked example you can run
//
// examples/testplugin is a plugin that exercises all five capabilities and does
// nothing useful, which is exactly what a host needs to test against. It is
// worth reading before writing the first real one.
package plugin
