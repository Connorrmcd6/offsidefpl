package types

import (
	"encoding/json"
	"time"
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

type DatabasePlayer struct {
	PlayerID int    `db:"playerID"`
	Team     int    `db:"playerTeamID"`
	Name     string `db:"playerName"`
}

func (df DatabasePlayer) ToAPIPlayers() Player {

	return Player{
		ID:      df.PlayerID,
		Team:    df.Team,
		WebName: df.Name,
	}
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

type DatabaseFixtureStats struct {
	EventHash  string `db:"eventHash"`
	FixtureID  int    `db:"fixtureID"`
	Gameweek   int    `db:"gameweek"`
	PlayerID   int    `db:"playerID"`
	EventType  string `db:"eventType"`
	EventValue int    `db:"eventValue"`
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

func (df DatabaseFixtures) ToAPIFixtures() Fixtures {
	// Parse the timestamp and convert format
	var formattedKickoff string
	if t, err := time.Parse("2006-01-02 15:04:05.000Z", df.Kickoff); err == nil {
		formattedKickoff = t.Format("2006-01-02T15:04:05Z")
	} else {
		// Fallback to original if parsing fails
		formattedKickoff = df.Kickoff
	}

	return Fixtures{
		FixtureID:  df.FixtureID,
		Gameweek:   df.Gameweek,
		Kickoff:    formattedKickoff,
		HomeTeamID: df.HomeTeamID,
		AwayTeamID: df.AwayTeamID,
	}
}

type DatabaseFixtures struct {
	FixtureID  int    `db:"fixtureID"`
	Gameweek   int    `db:"gameweek"`
	Kickoff    string `db:"kickoff"`
	HomeTeamID int    `db:"homeTeamID"`
	AwayTeamID int    `db:"awayTeamID"`
}

type GameweekHistory struct {
	GameweekHistrory GameweekResults     `json:"entry_history"`
	ActiveChip       string              `json:"active_chip"`
	Players          []GameweekSelection `json:"picks"`
}

type GameweekResults struct {
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

// Status represents individual fixture status entries
type FixtureStatus struct {
	BonusAdded bool   `json:"bonus_added"`
	Date       string `json:"date"`
	Event      int    `json:"event"`
	Points     string `json:"points"`
}

// FixtureUpdateStatus represents the full API response structure
type FixtureUpdateStatus struct {
	Status  []FixtureStatus `json:"status"`
	Leagues string          `json:"leagues"`
}

type DatabaseUsers struct {
	UserID string `db:"id"`
	TeamID int    `db:"teamID"`
}

type DatabaseResults struct {
	Gameweek    int    `db:"gameweek"`
	UserID      string `db:"userID"`
	TeamID      int    `db:"teamID"`
	Points      int    `db:"points"`
	Transfers   int    `db:"transfers"`
	Hits        int    `db:"hits"`
	BenchPoints int    `db:"benchPoints"`
	ActiveChip  string `db:"activeChip"`
	Pos1        int    `db:"pos_1"`
	Pos2        int    `db:"pos_2"`
	Pos3        int    `db:"pos_3"`
	Pos4        int    `db:"pos_4"`
	Pos5        int    `db:"pos_5"`
	Pos6        int    `db:"pos_6"`
	Pos7        int    `db:"pos_7"`
	Pos8        int    `db:"pos_8"`
	Pos9        int    `db:"pos_9"`
	Pos10       int    `db:"pos_10"`
	Pos11       int    `db:"pos_11"`
	Pos12       int    `db:"pos_12"`
	Pos13       int    `db:"pos_13"`
	Pos14       int    `db:"pos_14"`
	Pos15       int    `db:"pos_15"`
}

type DatabaseCard struct {
	TeamID          int    `db:"teamID"`
	UserID          string `db:"userID"`
	NominatorTeamID int    `db:"nominatorTeamID"`
	NominatorUserID string `db:"nominatorUserID"`
	Gameweek        int    `db:"gameweek"`
	IsCompleted     bool   `db:"isCompleted"`
	AdminVerified   bool   `db:"adminVerified"`
	Type            string `db:"type"`
	LeagueID        int    `db:"leagueID"`
	CardHash        string `db:"cardHash"`
}

type DatabaseLeague struct {
	LeagueID int    `db:"leagueID"`
	UserID   string `db:"userID"`
}

type DatabaseEvent struct {
	Gameweek   int    `db:"gameweek"`
	PlayerID   int    `db:"playerID"`
	EventType  string `db:"eventType"`
	EventValue int    `db:"eventValue"`
}

type OutstandingCards struct {
	TeamID   int    `db:"teamID"`
	UserID   string `db:"userID"`
	Gameweek int    `db:"gameweek"`
	CardType string `db:"type"`
}

type AggregatedResults struct {
	Gameweek    int    `db:"gameweek"`
	TeamID      int    `db:"teamID"`
	UserID      string `db:"userID"`
	Points      int    `db:"points"`
	TotalPoints int    `db:"totalPoints"`
}

type GameweekWinner struct {
	Gameweek  int    `db:"gameweek"`
	FirstName string `db:"firstName"`
	TeamName  string `db:"teamName"`
	Points    int    `db:"points"`
	WinnerID  string `db:"winnerID"`
}

type LeagueStandingRow struct {
	Position       int    `db:"position"`
	FirstName      string `db:"firstName"`
	LastName       string `db:"lastName"`
	TeamName       string `db:"teamName"`
	GameweekPoints int    `db:"gameweekPoints"`
	TotalPoints    int    `db:"totalPoints"`
	CardCount      int    `db:"cardCount"`
}

type CardApprovals struct {
	TeamID          int    `db:"teamID"`
	UserID          string `db:"userID"`
	NominatorTeamID int    `db:"nominatorTeamID"`
	NominatorUserID string `db:"nominatorUserID"`
	Gameweek        int    `db:"gameweek"`
	IsCompleted     bool   `db:"isCompleted"`
	AdminVerified   bool   `db:"adminVerified"`
	Type            string `db:"type"`
	LeagueID        int    `db:"leagueID"`
	CardHash        string `db:"cardHash"`
	Person          string `db:"person"`
}

type LeagueMembers struct {
	UserName   string `db:"userName"`
	LeagueID   int    `db:"leagueID"`
	UserID     string `db:"userID"`
	UserTeamID int    `db:"userTeamID"`
}

type CardNomination struct {
	TeamID          int    `db:"teamID"`
	UserID          string `db:"userID"`
	NominatorTeamID int    `db:"nominatorTeamID"`
	NominatorUserID string `db:"nominatorUserID"`
	Gameweek        int    `db:"gameweek"`
	IsCompleted     bool   `db:"isCompleted"`
	AdminVerified   bool   `db:"adminVerified"`
	Type            string `db:"type"`
	LeagueID        int    `db:"leagueID"`
	CardHash        string `db:"cardHash"`
}

type GameweekStatusResponse struct {
	Events []StatusEvent `json:"events"`
}

type StatusEvent struct {
	ID          int  `json:"id"`
	Finished    bool `json:"finished"`
	DataChecked bool `json:"data_checked"`
}
