package plugin

import (
	"encoding/json"
	"time"
)

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
// Two things cross as documents rather than as fields: the corner analysis on
// a lap and the car setup on a stint. Both are measured by the client, stored by
// the server without being read, and handed over exactly as stored. The server
// does not know what is in them, and that is the point — a client that measures
// something new reaches every plugin without the server changing. A plugin that
// wants the basic schema decodes with the protocol module's wire types; one with
// a client of its own decodes its own.
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
	// Corners is the lap's corner analysis, as the client sent it and the
	// server stored it: a JSON array, worst corner first, in the schema the
	// protocol module's wire.Corner describes. It is absent when the client
	// sent no corner detection, and a plugin must cope with that rather than
	// inventing a turn number.
	//
	// It is a document and not a struct so that this contract does not have to
	// know what a corner is. A plugin decodes the schema it understands and
	// ignores the rest.
	Corners json.RawMessage `json:"corners,omitempty"`
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

// FuelSummary is what the car drank over a stint, in litres. The figure per lap
// is UsedL over [StintFacts.Laps], and is the plugin's to work out.
type FuelSummary struct {
	UsedL      float64 `json:"used_l"`
	RemainingL float64 `json:"remaining_l"`
}

// TyreSummary is the four corners at the end of the stint, in degrees Celsius.
// The spread between the hottest and the coldest is the plugin's to work out.
type TyreSummary struct {
	LF float64 `json:"lf"`
	RF float64 `json:"rf"`
	LR float64 `json:"lr"`
	RR float64 `json:"rr"`
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
	// Setup is the car the driver drove, as the client sent it and the server
	// stored it: a JSON document in the schema the protocol module's
	// wire.CarSetup describes. It is on the stint and not on the lap because it
	// is one per sitting. It is absent when the simulator published none, which
	// is normal and not a failure, and a plugin must say nothing about the
	// setup rather than guess at one.
	//
	// A setup sheet is car-specific, so this was never going to be a fixed
	// struct; it is a document for the same reason [LapFacts.Corners] is.
	Setup json.RawMessage `json:"setup,omitempty"`
	// Conditions is the weather it was driven in.
	Conditions Conditions `json:"conditions"`
	// FinishedAt is when the stint ended.
	FinishedAt time.Time `json:"finished_at"`
}
