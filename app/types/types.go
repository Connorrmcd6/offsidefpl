package types

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
	DeadlineTime string `json:"deadline_time"`
}

type Player struct {
	PlayerID   string `json:"id"`
	TeamID     string `json:"team"`
	PlayerName string `json:"web_name"`
}

type FPLResponse struct {
	Events  []Event  `json:"events"`
	Players []Player `json:"elements"`
}
