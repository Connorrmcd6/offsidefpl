package types

type FPLUser struct {
	PlayerFirstName string `json:"player_first_name"`
	PlayerLastName  string `json:"player_last_name"`
	Name            string `json:"name"`
}

type FPLUserLeague struct {
	LeagueID        int
	AdminTeamID     int
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
