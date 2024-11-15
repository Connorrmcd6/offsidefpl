package types

type FPLTeamResponse struct {
	PlayerFirstName string `json:"player_first_name"`
	PlayerLastName  string `json:"player_last_name"`
	Name            string `json:"name"`
	// Leagues         []FPLLeague `json:"leagues"`
}

type FPLLeague struct {
	LeagueID   int    `json:"id"`
	LeagueName string `json:"name"`
	TeamID     int
	UserID     int
}
