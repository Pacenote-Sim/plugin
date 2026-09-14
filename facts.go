package plugin

import "time"

// The facts a plugin is given.
//
// Every number here was calculated exactly, before the plugin saw it. That is
// the governing rule of this product's language features and it is built into
// the shape of this file: a plugin receives derived facts and never a raw
// trace. It buys accuracy, because something that cannot do arithmetic cannot
// get the arithmetic wrong; cost, because a lap trace is three hundred samples
// and the facts are a dozen numbers; and latency, because a cue has to arrive
// before the next corner.
//
// A plugin that wants the trace is asking for the wrong thing. Say so rather
// than adding a field.

// SessionType is what the driver was doing. The spellings match the ones the
// client and the server already use on the wire.
type SessionType string

// The four session types.
const (
	SessionPractice   SessionType = "practice"
	SessionQualifying SessionType = "qualifying"
	SessionRace       SessionType = "race"
	SessionTesting    SessionType = "testing"
)

// LapKind is how a lap counts. The spellings match the wire's.
type LapKind string

// The four lap kinds.
const (
	LapClean   LapKind = "clean"   // a full lap from the line, on track throughout
	LapIn      LapKind = "in"      // entered the pits
	LapOut     LapKind = "out"     // started on pit road
	LapInvalid LapKind = "invalid" // driven, but not to be counted
)

// Driver is who was at the wheel.
//
// A plugin that does not declare [Capabilities.ReadsDriverData] still receives
// this, because a coach with no idea who it is coaching cannot say "you". What
// the declaration is for is the operator's judgement about where that name is
// about to be sent.
type Driver struct {
	// ID is this server's own identifier for the driver. It is stable and it
	// is the right key for a plugin's own cache.
	ID int64 `json:"id"`
	// Slug is the short, URL-safe name.
	Slug string `json:"slug"`
	// Name is what the driver calls themselves, and what a cue should use.
	Name string `json:"name,omitempty"`
	// Team is the team name, empty when they are not in one.
	Team string `json:"team,omitempty"`
}

// Session is the context every fact in an event belongs to: one car, one
// circuit, one simulator, one sitting.
//
// The simulator is part of the identity and not decoration. Two simulators
// agree on "spa" and on "Ferrari 296 GT3" and disagree about the tyre model,
// the fuel burn and therefore the lap time, so a plugin that compares across
// them is comparing nothing.
type Session struct {
	// StintID is the capture this belongs to, a UUID.
	StintID string `json:"stint_id"`
	// Sim is the simulator's own identifier.
	Sim string `json:"sim"`
	// Track is the display name and TrackID the simulator's stable identifier.
	Track   string `json:"track"`
	TrackID string `json:"track_id"`
	// Car is the car's display name and CarClass the class it runs in.
	Car      string `json:"car"`
	CarClass string `json:"car_class,omitempty"`
	// Type is what the session was.
	Type SessionType `json:"type"`
	// StartedAt is when the stint began.
	StartedAt time.Time `json:"started_at"`
}

// CornerPattern is the shape of a mistake, named rather than described so that
// a plugin can branch on it and a prompt can be written against a fixed
// vocabulary.
type CornerPattern string

// The patterns the corner detector names.
//
// There are three, and there are three because three is what a detector can
// measure from the driver's own pedals at the apex of a corner. A pattern that
// would have to be guessed at — a lift the trace cannot tell from a short-shift,
// a late apex nothing in the data distinguishes from a good one — is not here,
// because a fact set is not a place to put an inference and call it measured.
//
// An empty pattern is a corner that was slower with nothing conclusive about
// why. It is a real answer, it is the commonest one, and it usually means the
// right thing to say is the deficit and no diagnosis.
const (
	// PatternEarlyApex is a corner still being slowed at its apex that the
	// driver then waits to get back on the power in: the car was turned in
	// before the corner arrived.
	PatternEarlyApex CornerPattern = "early_apex"
	// PatternLateBraking is a corner whose apex still carries brake pressure,
	// with the throttle picked up normally afterwards.
	PatternLateBraking CornerPattern = "late_braking"
	// PatternSlowExit is a corner the driver is off the brakes in but waits a
	// long way past the apex before picking the throttle up.
	PatternSlowExit CornerPattern = "slow_exit"
)

// Corner is where a lap was lost, one turn at a time.
//
// Turn is the detector's own numbering and not the circuit's. A lap's corners
// are numbered from 1 in the order they are driven, counting only the ones the
// detector found: a turn taken flat and one whose speed drop is below the
// detector's threshold are not in the list, and every later corner shifts down
// by one when they are dropped. It is what the client already speaks aloud
// ("Turn 4"), so it is what a written cue must say too — and a plugin that maps
// it onto a circuit's published turn table is inventing a number nobody can
// check.
//
// Every speed here is whole km/h, because the channel it is measured from is.
type Corner struct {
	// Turn is the corner's number on this lap, from 1, in the order driven. A
	// cue names it as "Turn 4" and never invents one that is not in this list.
	Turn int `json:"turn"`
	// ApexPct is where the apex is, in ‰ of the lap (0…1000). It is what tells
	// two corners of the same number on two laps apart, and what orders a list
	// of them along the track.
	ApexPct int `json:"apex_pct"`
	// ApexKmh is the minimum speed through the corner, and ReferenceApexKmh
	// the same number on the reference lap. Zero for the reference means there
	// was none to compare with.
	ApexKmh          int `json:"apex_kmh"`
	ReferenceApexKmh int `json:"ref_apex_kmh,omitempty"`
	// DeficitKmh is how much apex speed this corner lost against the
	// reference, in whole km/h, and it is positive: the corners that gained
	// time are not sent.
	//
	// It is a speed and not a time. Apex speed is what the client measures; a
	// time lost per corner would be an integration over a piece of track the
	// two laps did not cover at the same points, and a plugin told
	// "milliseconds" would repeat an estimate as a measurement.
	DeficitKmh int `json:"deficit_kmh"`
	// BrakeAtApex is the brake still applied at the apex, in percent, and
	// ThrottleLag the distance from the apex to the throttle pickup in ‰ of
	// the lap. They are the two numbers Pattern is read from, and they are
	// here so that a cue can carry the evidence rather than only the verdict.
	BrakeAtApex int `json:"brake_at_apex,omitempty"`
	ThrottleLag int `json:"throttle_lag,omitempty"`
	// Pattern is the shape of it, when the detector could name one.
	Pattern CornerPattern `json:"pattern,omitempty"`
}

// Position is where the driver is in the race. It is absent outside a race,
// because a gap to the car ahead in a practice session is not a fact about
// anything.
type Position struct {
	// ClassPos is the position in the driver's own class.
	ClassPos int `json:"class_pos"`
	// GapAheadMs and GapBehindMs are the gaps either side, in milliseconds.
	// Zero means there is nobody there.
	GapAheadMs  int `json:"gap_ahead_ms,omitempty"`
	GapBehindMs int `json:"gap_behind_ms,omitempty"`
}

// LapFacts is one completed lap, reduced to what can be said about it.
type LapFacts struct {
	// Number is the simulator's lap counter and LapMs the lap time.
	Number int `json:"number"`
	LapMs  int `json:"lap_ms"`
	// Kind is how the lap counts.
	Kind LapKind `json:"kind"`
	// DeltaMs is the lap time against [LapFacts.Reference], in milliseconds:
	// positive is slower. It means nothing when Reference is empty.
	DeltaMs int `json:"delta_ms,omitempty"`
	// Reference names what the delta is against, in words a driver would use —
	// "your best lap", "the class best". Empty means there was nothing to
	// compare against, which happens on a circuit nobody has driven here yet
	// and is not a failure.
	Reference string `json:"reference,omitempty"`
	// PersonalBest reports that this is the driver's best lap here.
	PersonalBest bool `json:"personal_best,omitempty"`
	// Corners are where the time went, worst first. It is empty when the
	// client sent no corner detection, and a plugin must cope with that rather
	// than inventing a turn number.
	Corners []Corner `json:"corners,omitempty"`
	// Position is the race picture, absent outside a race.
	Position *Position `json:"position,omitempty"`
	// SpokenLap is the lap time already rendered into speakable words — "one
	// minute 31.2 seconds". It exists because a speech engine reading "91240"
	// produces something nobody wants in their ear, and the client can render
	// it correctly while a model cannot be trusted to.
	SpokenLap string `json:"spoken_lap,omitempty"`
	// StartedAt is when the lap began.
	StartedAt time.Time `json:"started_at"`
}

// FuelSummary is what the car drank over a stint, in litres.
type FuelSummary struct {
	UsedL      float64 `json:"used_l"`
	RemainingL float64 `json:"remaining_l"`
	// PerLapL is the average over the counted laps, which is the number a
	// strategy is built on.
	PerLapL float64 `json:"per_lap_l,omitempty"`
}

// TyreSummary is the four corners at the end of the stint, in degrees Celsius,
// with the spread that says whether one corner of the car is working alone.
type TyreSummary struct {
	LF float64 `json:"lf"`
	RF float64 `json:"rf"`
	LR float64 `json:"lr"`
	RR float64 `json:"rr"`
	// SpreadC is the hottest minus the coldest.
	SpreadC float64 `json:"spread_c"`
}

// Wheel names a corner of the car.
type Wheel string

// The four wheels.
const (
	WheelLF Wheel = "lf"
	WheelRF Wheel = "rf"
	WheelLR Wheel = "lr"
	WheelRR Wheel = "rr"
)

// SetupValue is one named setting of a setup sheet.
//
// Text is what the simulator printed and is authoritative: "-2.5 deg", "Soft",
// "9 hole". Number and Unit are that text read as a measurement where it is
// one. A setting whose text carries no number has Number zero and Unit empty,
// which is indistinguishable from a value that really is zero — Text is what
// separates the two, which is why it is always present.
type SetupValue struct {
	// Group is the path the simulator published this under, slash-separated
	// ("Chassis/LeftFront"), or empty.
	Group string `json:"group,omitempty"`
	// Name is the key, spelled the way the simulator spells it, so a driver
	// can find the control on their setup screen. A plugin proposing a
	// [SetupChange] should spell Setting the same way.
	Name string `json:"name"`
	// Text is the value as published.
	Text string `json:"text"`
	// Number is the leading number of Text, and Unit what followed it.
	Number float64 `json:"number,omitempty"`
	Unit   string  `json:"unit,omitempty"`
}

// SetupTyre is one wheel of a [CarSetup]: what it was set to and what it came
// back at.
//
// The three temperatures and the three tread depths run inner, middle, outer as
// the car stands, on both sides of the car, whichever order the simulator
// published them in. That is the point of them: the spread across a tread is
// the canonical camber measurement, and an inner that ran twelve degrees hotter
// than the outer is a car leaning on the wrong part of the contact patch — a
// measurement, not an opinion, and not something a driver has to be surveyed
// about. HotKpa against ColdKpa is the other one: it says exactly how far the
// cold setting has to move.
//
// A field left at zero was not published. No real tyre is at zero kPa, zero
// degrees or zero tread.
type SetupTyre struct {
	// Wheel is which corner of the car this is.
	Wheel Wheel `json:"wheel"`
	// ColdKpa is the starting pressure on the setup sheet and HotKpa the
	// pressure the tyre was last read at, both in kPa.
	ColdKpa float64 `json:"cold_kpa,omitempty"`
	HotKpa  float64 `json:"hot_kpa,omitempty"`
	// The last tread temperatures across the tyre, in °C, inner to outer.
	TempInnerC  float64 `json:"temp_inner_c,omitempty"`
	TempMiddleC float64 `json:"temp_middle_c,omitempty"`
	TempOuterC  float64 `json:"temp_outer_c,omitempty"`
	// The tread remaining across the tyre, in percent, inner to outer.
	TreadInnerPct  float64 `json:"tread_inner_pct,omitempty"`
	TreadMiddlePct float64 `json:"tread_middle_pct,omitempty"`
	TreadOuterPct  float64 `json:"tread_outer_pct,omitempty"`
}

// CarSetup is the car the driver actually drove, as the simulator published it.
//
// It is what turns setup advice from "does it understeer?" into "your inner
// fronts ran twelve degrees hotter than the outers". A plugin answering
// [RequestSetup] is expected to reach for the measurements first and for the
// driver's own description second.
//
// It is absent more often than not, and absent is normal: a simulator that
// publishes no setup and a series that locks the setup away look the same from
// here, and neither is a failure. A plugin must say nothing about the setup
// rather than guess at one.
type CarSetup struct {
	// UpdateCount is the simulator's own revision counter for the sheet, when
	// it publishes one.
	UpdateCount int `json:"update_count,omitempty"`
	// Tyres is one entry per wheel the simulator published, in the order LF,
	// RF, LR, RR.
	Tyres []SetupTyre `json:"tyres,omitempty"`
	// RearWing is the rear wing setting, for the cars that have one. It is
	// lifted out of Values and does not appear there as well.
	RearWing *SetupValue `json:"rear_wing,omitempty"`
	// Values is every other setting of the sheet — aero, chassis, brakes,
	// drivetrain, fuel — in the order the simulator published them.
	//
	// A setup sheet is car-specific: a GT3 car has a dive plane count and a
	// stock car has a track bar. Those cannot be a fixed Go struct without
	// this interface releasing a version per car, so they arrive named rather
	// than typed. It is still not a document to parse: every entry carries its
	// group, its name and its number.
	Values []SetupValue `json:"values,omitempty"`
}

// TyreAt returns the setup of one wheel and whether the simulator published it.
func (s CarSetup) TyreAt(w Wheel) (SetupTyre, bool) {
	for _, t := range s.Tyres {
		if t.Wheel == w {
			return t, true
		}
	}
	return SetupTyre{}, false
}

// Value returns the named setting and whether the sheet carries it. The name is
// the simulator's own spelling and the match is exact, because a setup sheet
// names two different things "ToeIn" in two different groups and guessing which
// was meant is worse than answering nothing.
func (s CarSetup) Value(name string) (SetupValue, bool) {
	for _, v := range s.Values {
		if v.Name == name {
			return v, true
		}
	}
	return SetupValue{}, false
}

// Conditions are the session-mean weather values.
type Conditions struct {
	// Skies is the simulator's enum, 0 clear to 3 overcast, and Wetness its
	// own, 0 unknown to 7 very wet.
	Skies   int `json:"skies"`
	Wetness int `json:"wetness"`
	// WindKmh is km/h, Humidity a percentage, and the temperatures Celsius.
	WindKmh    float64 `json:"wind_kmh"`
	Humidity   float64 `json:"humidity"`
	TrackTempC float64 `json:"track_temp_c"`
	AirTempC   float64 `json:"air_temp_c"`
}

// StintFacts is a finished stint, reduced the same way a lap is.
type StintFacts struct {
	// Laps is every lap driven and CleanLaps the ones that counted.
	Laps      int `json:"laps"`
	CleanLaps int `json:"clean_laps"`
	// Incidents is the simulator's own count.
	Incidents int `json:"incidents"`
	// BestLapMs and AvgLapMs are over the counted laps.
	BestLapMs int `json:"best_lap_ms"`
	AvgLapMs  int `json:"avg_lap_ms"`
	// ConsistencyPct is 0 to 100: how tightly the counted laps cluster. It is
	// the single most useful number about a stint and the one a driver cannot
	// feel.
	ConsistencyPct int `json:"consistency_pct"`
	// TopSpeedKmh is the fastest the car went.
	TopSpeedKmh int `json:"top_speed_kmh"`
	// Fuel and Tyres are what the car had left and how hard it was working.
	Fuel  FuelSummary `json:"fuel"`
	Tyres TyreSummary `json:"tyres"`
	// Setup is the car the driver drove, or nil when the simulator published
	// none. It is on the stint and not on the lap because it is one per
	// sitting. See [CarSetup] — nil is normal and a plugin must cope with it.
	Setup *CarSetup `json:"setup,omitempty"`
	// Conditions is the weather it was driven in.
	Conditions Conditions `json:"conditions"`
	// FinishedAt is when the stint ended.
	FinishedAt time.Time `json:"finished_at"`
}
