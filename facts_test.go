package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestFactsJSON pins the facts against the JSON a plugin decodes them from. The
// facts cross the process boundary as JSON inside the transport, so a tag that
// changes here changes what every already-built plugin reads.
//
// The two documents — the corner analysis and the car setup — are pinned as
// passing through untouched. That is the whole contract for them: what the
// client sent is what the plugin gets, byte for byte, and nothing in between
// has an opinion about it.
func TestFactsJSON(t *testing.T) {
	t.Parallel()

	corners := json.RawMessage(`[{"turn":4,"apex_pct":312,"apex_kmh":96,"ref_apex_kmh":104,"deficit_kmh":8,` +
		`"brake_at_apex":31,"throttle_lag":14,"pattern":"early_apex"}]`)
	setup := json.RawMessage(`{"update_count":4,"tyres":[{"wheel":"lf","cold_kpa":165,"hot_kpa":178.5}],` +
		`"values":[{"name":"Camber","text":"-2.5 deg","number":-2.5,"unit":"deg"}]}`)

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "a lap carries its corner analysis exactly as it was stored",
			value: plugin.LapFacts{
				Number: 14, LapMs: 91240, Kind: plugin.LapClean, Corners: corners,
			},
			want: `{"number":14,"lap_ms":91240,"kind":"clean",` +
				`"corners":` + string(corners) + `,` +
				`"started_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name:  "a lap with no corner analysis says nothing about corners",
			value: plugin.LapFacts{Number: 1, LapMs: 95000, Kind: plugin.LapOut},
			want:  `{"number":1,"lap_ms":95000,"kind":"out","started_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name: "a stint carries its setup exactly as it was stored",
			value: plugin.StintFacts{
				Laps: 3, Setup: setup,
			},
			want: `{"laps":3,"clean_laps":0,"incidents":0,"best_lap_ms":0,"avg_lap_ms":0,` +
				`"consistency_pct":0,"top_speed_kmh":0,"fuel":{"used_l":0,"remaining_l":0},` +
				`"tyres":{"lf":0,"rf":0,"lr":0,"rr":0},` +
				`"setup":` + string(setup) + `,` +
				`"conditions":{"skies":0,"wetness":0,"wind_kmh":0,"humidity":0,"track_temp_c":0,"air_temp_c":0},` +
				`"finished_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name:  "a stint with no setup says nothing about one",
			value: plugin.StintFacts{Laps: 3},
			want: `{"laps":3,"clean_laps":0,"incidents":0,"best_lap_ms":0,"avg_lap_ms":0,` +
				`"consistency_pct":0,"top_speed_kmh":0,"fuel":{"used_l":0,"remaining_l":0},` +
				`"tyres":{"lf":0,"rf":0,"lr":0,"rr":0},` +
				`"conditions":{"skies":0,"wetness":0,"wind_kmh":0,"humidity":0,"track_temp_c":0,"air_temp_c":0},` +
				`"finished_at":"0001-01-01T00:00:00Z"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)

			got, err := json.Marshal(tc.value)
			r.NoError(err)
			r.JSONEq(tc.want, string(got))
			r.Equal(tc.want, string(got), "the key order is the declaration order and is part of the shape")
		})
	}
}

// TestDocumentsRoundTrip is the other direction: a plugin decoding the facts
// gets the documents back as the bytes that were sent, not a re-encoding of
// them. A document that had been re-encoded on the way would still be valid
// JSON and would still decode, which is exactly why it has to be pinned.
func TestDocumentsRoundTrip(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	corners := json.RawMessage(`[{"turn":3,"apex_pct":600,"apex_kmh":60,"ref_apex_kmh":80,"deficit_kmh":20}]`)
	setup := json.RawMessage(`{"tyres":[{"wheel":"lf","cold_kpa":165}]}`)

	encoded, err := json.Marshal(plugin.Event{
		ID:   "e-1",
		Kind: plugin.EventLapCompleted,
		Lap:  &plugin.LapFacts{Number: 7, Corners: corners},
		Stint: &plugin.StintFacts{
			Laps: 2, Setup: setup,
		},
	})
	r.NoError(err)

	var got plugin.Event
	r.NoError(json.Unmarshal(encoded, &got))
	r.Equal(string(corners), string(got.Lap.Corners))
	r.Equal(string(setup), string(got.Stint.Setup))
}
