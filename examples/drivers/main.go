// drivers is an example plugin that other plugins ask. It knows which drivers
// are on this team — a list the operator types into its settings — and answers
// two questions about them.
//
// It is here to be the other half of examples/payments, which asks it whether
// the driver a payment names is one of the team's. Nothing below is clever; the
// thing worth reading is the shape. A plugin that answers declares its kinds in
// its manifest, spelled with its own name, and implements [plugin.Answerer].
// The payload of a question and of an answer are this plugin's to define, and
// they are documented here because the server does not know them:
//
//	drivers.lookup   {"slug": "ana-ruiz"}   →  {"slug": "ana-ruiz", "known": true, "name": "Ana Ruiz"}
//	drivers.list     (no payload)           →  {"drivers": [{"slug": ..., "name": ...}, ...]}
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pacenote-sim/plugin"
)

// settingRoster is the operator's list: one driver per line, "slug: Name".
const settingRoster = "roster"

func main() { plugin.Serve(drivers{}) }

type drivers struct{}

func (d drivers) Settings(context.Context) ([]plugin.Setting, error) {
	return []plugin.Setting{{
		Name:  settingRoster,
		Label: "Drivers on this team",
		Help:  "One per line, as slug: name — for example ana-ruiz: Ana Ruiz. Other plugins ask this one whether a driver is on the list.",
		Kind:  plugin.KindText,
	}}, nil
}

// Notify is told about laps and stints and does nothing with them. It is here
// because every plugin has it; this one exists to be asked.
func (d drivers) Notify(context.Context, plugin.Event) (plugin.Usage, error) {
	return plugin.Usage{}, nil
}

// driver is one line of the roster.
type driver struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Answer is what other plugins reach. The kind arrives as "drivers.<what>", and
// only the part after the dot is this plugin's to interpret.
func (d drivers) Answer(_ context.Context, r plugin.Request) (plugin.Response, error) {
	roster := parseRoster(r.Settings.String(settingRoster))

	switch strings.TrimPrefix(string(r.Kind), r.Kind.Plugin()+".") {
	case "lookup":
		var q struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(r.Payload, &q); err != nil || q.Slug == "" {
			return plugin.Response{}, fmt.Errorf("%w: a lookup names a slug: {\"slug\": \"...\"}", plugin.ErrInvalid)
		}
		answer := map[string]any{"slug": q.Slug, "known": false}
		for _, dr := range roster {
			if dr.Slug == q.Slug {
				answer["known"] = true
				answer["name"] = dr.Name
			}
		}
		return respond(r.Kind, answer)
	case "list":
		return respond(r.Kind, map[string]any{"drivers": roster})
	default:
		return plugin.Response{}, plugin.ErrUnsupported
	}
}

// respond encodes an answer. Nothing here calls a vendor, so nothing is metered.
func respond(kind plugin.RequestKind, answer any) (plugin.Response, error) {
	out, err := json.Marshal(answer)
	if err != nil {
		return plugin.Response{}, err
	}
	return plugin.Response{Kind: kind, Payload: out}, nil
}

// parseRoster reads the operator's list. A line it cannot read is skipped
// rather than refused: the operator sees the effect on the plugin's page and a
// half-typed line should not take the whole list down.
func parseRoster(text string) []driver {
	out := []driver{}
	for line := range strings.SplitSeq(text, "\n") {
		slug, name, ok := strings.Cut(line, ":")
		slug, name = strings.TrimSpace(slug), strings.TrimSpace(name)
		if !ok || slug == "" {
			continue
		}
		if name == "" {
			name = slug
		}
		out = append(out, driver{Slug: slug, Name: name})
	}
	return out
}
