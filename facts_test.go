package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/plugin"
)

// TestCarSetupLookups covers the two accessors a plugin writes its first line
// of setup advice with. Both answer "was I told?" as well as "what was it",
// because a setup sheet that does not carry a setting is the normal case and a
// plugin that reads a zero as a measurement is inventing one.
func TestCarSetupLookups(t *testing.T) {
	t.Parallel()

	setup := plugin.CarSetup{
		Tyres: []plugin.SetupTyre{
			{Wheel: plugin.WheelLF, ColdKpa: 165, HotKpa: 178.5, TempInnerC: 96, TempOuterC: 84},
			{Wheel: plugin.WheelRF, ColdKpa: 165, HotKpa: 176},
		},
		Values: []plugin.SetupValue{
			{Group: "Chassis/LeftFront", Name: "Camber", Text: "-2.5 deg", Number: -2.5, Unit: "deg"},
			{Group: "Chassis/Rear", Name: "ArbSize", Text: "Medium"},
		},
	}

	t.Run("a wheel the sheet carries comes back whole", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		tyre, ok := setup.TyreAt(plugin.WheelLF)
		r.True(ok)
		r.InDelta(178.5, tyre.HotKpa, 0)
		r.InDelta(12, tyre.TempInnerC-tyre.TempOuterC, 0,
			"the spread across the tread is the camber reading and must survive the lookup")
	})

	t.Run("a wheel the sheet does not carry is absent, not zero", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		tyre, ok := setup.TyreAt(plugin.WheelRR)
		r.False(ok)
		r.Zero(tyre)
	})

	t.Run("a named setting comes back with its number and unit", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		v, ok := setup.Value("Camber")
		r.True(ok)
		r.InDelta(-2.5, v.Number, 0)
		r.Equal("deg", v.Unit)
	})

	t.Run("a setting with no number keeps its text", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		v, ok := setup.Value("ArbSize")
		r.True(ok)
		r.Equal("Medium", v.Text)
		r.Zero(v.Number, "a word has no number, and zero must not be read as one")
	})

	t.Run("a setting the sheet does not carry is absent", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		_, ok := setup.Value("TrackBar")
		r.False(ok)
	})

	t.Run("an empty setup answers nothing rather than panicking", func(t *testing.T) {
		t.Parallel()
		r := require.New(t)

		var empty plugin.CarSetup
		_, tyreOK := empty.TyreAt(plugin.WheelLF)
		_, valueOK := empty.Value("Camber")
		r.False(tyreOK)
		r.False(valueOK)
	})
}

// TestFactsJSON pins the two additions against the JSON a plugin decodes them
// from. The facts cross the process boundary as JSON inside the transport, so a
// tag that changes here changes what every already-built plugin reads.
func TestFactsJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "a corner carries the speed it lost, never a time",
			value: plugin.Corner{
				Turn: 4, ApexPct: 312, ApexKmh: 96, ReferenceApexKmh: 104, DeficitKmh: 8,
				BrakeAtApex: 31, ThrottleLag: 14, Pattern: plugin.PatternEarlyApex,
			},
			want: `{"turn":4,"apex_pct":312,"apex_kmh":96,"ref_apex_kmh":104,"deficit_kmh":8,` +
				`"brake_at_apex":31,"throttle_lag":14,"pattern":"early_apex"}`,
		},
		{
			name:  "a corner with nothing conclusive omits the pattern",
			value: plugin.Corner{Turn: 1, ApexPct: 40, ApexKmh: 120, DeficitKmh: 4},
			want:  `{"turn":1,"apex_pct":40,"apex_kmh":120,"deficit_kmh":4}`,
		},
		{
			name: "a tyre carries its tread inner to outer",
			value: plugin.SetupTyre{
				Wheel: plugin.WheelLF, ColdKpa: 165, HotKpa: 178.5,
				TempInnerC: 96, TempMiddleC: 90, TempOuterC: 84,
				TreadInnerPct: 97, TreadMiddlePct: 98, TreadOuterPct: 99,
			},
			want: `{"wheel":"lf","cold_kpa":165,"hot_kpa":178.5,"temp_inner_c":96,"temp_middle_c":90,` +
				`"temp_outer_c":84,"tread_inner_pct":97,"tread_middle_pct":98,"tread_outer_pct":99}`,
		},
		{
			name:  "a stint with no setup says nothing about one",
			value: plugin.StintFacts{Laps: 3},
			want: `{"laps":3,"clean_laps":0,"incidents":0,"best_lap_ms":0,"avg_lap_ms":0,` +
				`"consistency_pct":0,"top_speed_kmh":0,"fuel":{"used_l":0,"remaining_l":0},` +
				`"tyres":{"lf":0,"rf":0,"lr":0,"rr":0,"spread_c":0},` +
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
