package types

import (
	"encoding/json"
)

type FPLUser struct {
	PlayerFirstName string `json:"player_first_name"`
	PlayerLastName  string `json:"player_last_name"`
	Name            string `json:"name"`
}

type FPLUserLeague struct {
	LeagueID        int
	AdminUserID     string
	UserTeamID      int
	LeagueName      string
	SeasonStartYear int
	UserID          string
	IsLinked        bool
	IsActive        bool
}

type ClassicLeague struct {
	LeagueID   int    `json:"id"`
	Name       string `json:"name"`
	LeagueType string `json:"league_type"`
	Created    string `json:"created"`
}

// we dont need to define types for non classic leagues because we dont care about them
type Leagues struct {
	Classic    []ClassicLeague `json:"classic"`
	H2H        []any           `json:"h2h"`
	Cup        any             `json:"cup"`
	CupMatches []any           `json:"cup_matches"`
}

type FPLResponseLeagues struct {
	Leagues Leagues `json:"leagues"`
}

type UserLeagueSelection struct {
	ID          string
	LeagueID    int
	UserID      string
	AdminUserID string
	UserTeamID  int
	LeagueName  string
	IsLinked    bool
	IsActive    bool
	IsDefault   bool
}

type Event struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	DeadlineTime string `json:"deadline_time"`
}

// Player represents an FPL player
type Player struct {
	ID      int    `json:"id"`
	Team    int    `json:"team"`
	WebName string `json:"web_name"`
}

// FPLResponse represents the full API response
type FPLResponse struct {
	Events   []Event  `json:"events"`
	Elements []Player `json:"elements"`
}

type FixtureStats struct {
	Gameweek  int            `json:"event"`
	Finished  bool           `json:"finished"`
	FixtureID int            `json:"id"`
	Stats     []StatCategory `json:"stats"` // Fix: directly use []StatCategory
}

// StatValue represents an individual player statistic
type StatValue struct {
	Value   int `json:"value"`
	Element int `json:"element"`
}

// StatIdentifier represents possible stat types
type StatIdentifier string

const (
	OwnGoals        StatIdentifier = "own_goals"
	PenaltiesMissed StatIdentifier = "penalties_missed"
	RedCards        StatIdentifier = "red_cards"
)

// StatCategory represents a category of statistics with home and away values
type StatCategory struct {
	Identifier StatIdentifier `json:"identifier"`
	Away       []StatValue    `json:"a"`
	Home       []StatValue    `json:"h"`
}

// Stats represents the complete statistics structure
type Stats struct {
	Stats []StatCategory `json:"stats"`
}

// Add this to types.go
func (f *FixtureStats) UnmarshalJSON(data []byte) error {
	// Create a temporary struct to hold the raw data
	type TempFixture struct {
		Gameweek  int            `json:"event"`
		Finished  bool           `json:"finished"`
		FixtureID int            `json:"id"`
		Stats     []StatCategory `json:"stats"`
	}

	var temp TempFixture
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	// Copy the basic fields
	f.Gameweek = temp.Gameweek
	f.Finished = temp.Finished
	f.FixtureID = temp.FixtureID

	// Filter stats to only include desired identifiers
	wantedStats := map[StatIdentifier]bool{
		OwnGoals:        true,
		PenaltiesMissed: true,
		RedCards:        true,
	}

	// Filter and append only the stats we want
	for _, stat := range temp.Stats {
		if wantedStats[stat.Identifier] {
			f.Stats = append(f.Stats, stat)
		}
	}

	return nil
}

type Fixtures struct {
	FixtureID  int    `json:"id"`
	Gameweek   int    `json:"event"`
	Kickoff    string `json:"kickoff_time"`
	HomeTeamID int    `json:"team_h"`
	AwayTeamID int    `json:"team_a"`
}

type GameweekHistory struct {
	GameweekHistrory GameweekHistrory    `json:"entry_history"`
	ActiveChip       string              `json:"active_chip"`
	Players          []GameweekSelection `json:"picks"`
}

type GameweekHistrory struct {
	Gameweek     int `json:"event"`
	Points       int `json:"points"`
	Transfers    int `json:"event_transfers"`
	TransferCost int `json:"event_transfers_cost"`
	BenchPoints  int `json:"points_on_bench"`
}

type GameweekSelection struct {
	PlayerID int `json:"element"`
	Position int `json:"position"`
}

type CardHistory struct {
	TeamID          int
	UserID          string
	NominatorTeamID int
	NominatorUserID string
	Gameweek        int
	IsCompleted     bool
	AdminVerified   bool
	Type            string
}

// types/types.go
type DataUpdateDates struct {
	TS string `db:"ts"`
}
