package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/cmcd97/bytesize/app/components"
	"github.com/cmcd97/bytesize/app/types"
	"github.com/cmcd97/bytesize/app/views"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/models"
)

const (
	userLeaguesTimeout = 30 * time.Second
	leaguesCollection  = "leagues"
)

func UserLeaguesGet(c echo.Context) error {
	_, cancel := context.WithTimeout(c.Request().Context(), userLeaguesTimeout)
	defer cancel()

	// Context validation outside transaction
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	teamID := record.Get("teamID")

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}

	var leagueRecords []types.UserLeagueSelection

	// Run database operations in transaction
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		// Fetch league records
		leagueRecordPointers, err := txDao.FindRecordsByExpr(leaguesCollection,
			dbx.NewExp("teamID = {:teamID} order by leagueName asc",
				dbx.Params{"teamID": teamID}))
		if err != nil {
			log.Printf("Error finding league records: %v", err)
			return fmt.Errorf("find leagues: %w", err)
		}

		// Convert records to struct
		leagueRecords = make([]types.UserLeagueSelection, 0, len(leagueRecordPointers))
		for _, record := range leagueRecordPointers {
			league := types.UserLeagueSelection{
				ID:          record.GetString("id"),
				LeagueID:    record.GetInt("leagueID"),
				UserID:      record.GetString("userID"),
				AdminUserID: record.GetString("adminUserID"),
				UserTeamID:  record.GetInt("teamID"),
				LeagueName:  lib.ReplaceUnderscoresWithSpaces(record.GetString("leagueName")),
				IsLinked:    record.GetBool("isLinked"),
				IsActive:    record.GetBool("isActive"),
				IsDefault:   record.GetBool("isDefault"),
			}
			leagueRecords = append(leagueRecords, league)
		}

		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch league records")
	}

	log.Printf("Successfully fetched %d league records", len(leagueRecords))
	return lib.Render(c, http.StatusOK, components.LeagueList(leagueRecords))
}

const (
	defaultTimeout     = 30 * time.Second
	errMissingLeagueID = "leagueID parameter is required"
	errUpdateFailed    = "failed to update default league: %v"
)

func SetDefaultLeague(c echo.Context) error {
	_, cancel := context.WithTimeout(c.Request().Context(), defaultTimeout)
	defer cancel()

	log.Printf("Setting default league - Request started")

	// Validate input
	leagueID := c.QueryParam("leagueID")
	if leagueID == "" {
		log.Printf("Error: Missing leagueID parameter")
		return echo.NewHTTPError(http.StatusBadRequest, errMissingLeagueID)
	}
	log.Printf("Processing league ID: %s", leagueID)

	// Update default league
	leagueRecords, hasAdmin, err := updateDefaultLeague(c, leagueID)
	if err != nil {
		log.Printf("Error updating default league: %v", err)
		return echo.NewHTTPError(
			http.StatusInternalServerError,
			fmt.Sprintf(errUpdateFailed, err),
		)
	}

	// Handle league initialization if no admin exists
	if !hasAdmin {
		log.Printf("League %s requires initialization", leagueID)
		return lib.Render(c, http.StatusOK, components.InitLeague(leagueID))
	}

	log.Printf("Successfully set default league: %s", leagueID)
	lib.HtmxRedirect(c, "/app/profile")
	return lib.Render(c, http.StatusOK, components.LeagueList(leagueRecords))

}

func updateDefaultLeague(c echo.Context, leagueID string) ([]types.UserLeagueSelection, bool, error) {
	// Context validation outside transaction
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return []types.UserLeagueSelection{}, true, echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	authUserID := record.Id

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return []types.UserLeagueSelection{}, true, echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}

	var hasAdmin bool
	var leagueRecords []types.UserLeagueSelection

	// Run all database operations in a single transaction
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		// Find and update existing default league
		defaultLeague, err := txDao.FindFirstRecordByFilter(
			"leagues",
			"isDefault = true && userID = {:userID}",
			dbx.Params{"userID": authUserID},
		)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			return fmt.Errorf("database error: %w", err)
		}

		if defaultLeague != nil {
			defaultLeague.Set("isDefault", false)
			if err := txDao.SaveRecord(defaultLeague); err != nil {
				return fmt.Errorf("failed to update old default league: %w", err)
			}
			log.Printf("Removed default status from league %s", defaultLeague.Id)
		}

		// Update new league record
		league, err := txDao.FindRecordById("leagues", leagueID)
		if err != nil {
			return fmt.Errorf("failed to find new league: %w", err)
		}

		league.Set("isDefault", true)
		league.Set("isActive", true)
		league.Set("isLinked", true)

		// Check for existing admin
		fplLeagueID := league.GetInt("leagueID")
		existingAdmin, err := txDao.FindFirstRecordByFilter(
			"leagues",
			"adminUserID != 'temp' && leagueID = {:leagueID}",
			dbx.Params{"leagueID": fplLeagueID},
		)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			return fmt.Errorf("failed to check admin status: %w", err)
		}

		if existingAdmin != nil {
			adminID := existingAdmin.GetString("adminUserID")
			if adminID != "temp" {
				hasAdmin = true
				league.Set("adminUserID", adminID)
				log.Printf("Setting admin ID %s for league %s", adminID, leagueID)
			}
		}

		if err := txDao.SaveRecord(league); err != nil {
			return fmt.Errorf("failed to save league: %w", err)
		}

		// Get all user leagues
		leagueRecordPointers, err := txDao.FindRecordsByExpr("leagues",
			dbx.NewExp("userID = {:userID} order by leagueName asc", dbx.Params{"userID": authUserID}))
		if err != nil {
			return fmt.Errorf("failed to find league records: %w", err)
		}

		// Convert records to struct
		leagueRecords = make([]types.UserLeagueSelection, 0, len(leagueRecordPointers))
		for _, record := range leagueRecordPointers {
			league := types.UserLeagueSelection{
				ID:          record.GetString("id"),
				LeagueID:    record.GetInt("leagueID"),
				UserID:      record.GetString("userID"),
				AdminUserID: record.GetString("adminUserID"),
				UserTeamID:  record.GetInt("teamID"),
				LeagueName:  lib.ReplaceUnderscoresWithSpaces(record.GetString("leagueName")),
				IsLinked:    record.GetBool("isLinked"),
				IsActive:    record.GetBool("isActive"),
				IsDefault:   record.GetBool("isDefault"),
			}
			leagueRecords = append(leagueRecords, league)
		}

		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return []types.UserLeagueSelection{}, hasAdmin, err
	}

	log.Printf("Successfully updated league %s", leagueID)
	return leagueRecords, hasAdmin, nil
}

func InitialiseLeague(c echo.Context) error {
	leagueID := c.FormValue("leagueInitID")
	log.Printf("Initializing league with ID: %s", leagueID)

	// Context validation outside transaction
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	authUserID := record.Id
	log.Printf("Auth record retrieved successfully for user ID: %s", authUserID)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}
	log.Printf("PocketBase instance retrieved successfully")

	// Run database operations in transaction
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		// Find league record
		newLeagueRecord, err := txDao.FindRecordById("leagues", leagueID)
		if err != nil {
			log.Printf("Error finding league record: %v", err)
			return fmt.Errorf("failed to find league: %w", err)
		}
		log.Printf("Found league record with ID: %s", leagueID)

		// Update league settings
		newLeagueRecord.Set("isDefault", true)
		newLeagueRecord.Set("isActive", true)
		newLeagueRecord.Set("isLinked", true)
		newLeagueRecord.Set("adminUserID", authUserID)
		log.Printf("Updated league record fields for league ID: %s", leagueID)

		// Save changes within transaction
		if err := txDao.SaveRecord(newLeagueRecord); err != nil {
			log.Printf("Error saving league record: %v", err)
			return fmt.Errorf("failed to save league: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return err
	}

	log.Printf("Successfully initialized league with ID: %s", leagueID)
	return lib.HtmxRedirect(c, "/app/profile")
}

const (
	defaultLeagueFilter = "isDefault = TRUE && userID = {:userID}"
)

// CheckForLeague checks if the authenticated user has a default league.
// If no default league exists, it redirects to league setup, otherwise to profile page.
func CheckForLeague(c echo.Context) error {
	// Get authenticated user
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("[ERROR] Auth failed for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get PocketBase instance
	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("[ERROR] PocketBase instance unavailable for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusInternalServerError, "Service temporarily unavailable")
	}

	// Set cache control headers
	c.Response().Header().Set("Cache-Control", "private, no-cache, no-store, must-revalidate")

	// Search for default league
	defaultLeague, err := pb.Dao().FindFirstRecordByFilter(
		leaguesCollection,
		defaultLeagueFilter,
		dbx.Params{"userID": record.Id},
	)

	// Handle different error scenarios
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[INFO] No default league found for user %s", record.Id)
			return lib.Render(c, http.StatusOK, views.LeagueSetup())
		}
		log.Printf("[ERROR] Database error while checking league: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to check league status")
	}

	if defaultLeague == nil {
		return lib.Render(c, http.StatusOK, views.LeagueSetup())
	}

	return lib.Render(c, http.StatusOK, views.ProfilePage())
}

const (
	leaguesTimeout = 10 * time.Second
	defaultLimit   = 1
)

// getDefaultLeague retrieves the default league for a given teamID
func getDefaultLeague(txDao *daos.Dao, teamID interface{}) (*models.Record, error) {
	return txDao.FindFirstRecordByFilter(leaguesCollection,
		"teamID = {:teamID} && isDefault = TRUE",
		dbx.Params{"teamID": teamID})
}

// getLeagueMembers retrieves all members of a league
func getLeagueMembers(txDao *daos.Dao, leagueID int) ([]int, error) {
	leagueRecords, err := txDao.FindRecordsByExpr(leaguesCollection,
		dbx.NewExp("leagueID = {:leagueID}", dbx.Params{"leagueID": leagueID}))
	if err != nil {
		return nil, fmt.Errorf("find league members: %w", err)
	}

	teamIDs := make([]int, 0, len(leagueRecords))
	for _, record := range leagueRecords {
		teamIDs = append(teamIDs, record.GetInt("teamID"))
	}
	return teamIDs, nil
}

// getMaxGameweek retrieves the latest gameweek number
func getMaxGameweek(txDao *daos.Dao) (int, error) {
	maxGameweek := []struct {
		Gameweek int `db:"gameweek"`
	}{}

	err := txDao.DB().
		Select("aggregated_results.gameweek").
		From("aggregated_results").
		OrderBy("gameweek DESC").
		Limit(defaultLimit).
		All(&maxGameweek)

	if err != nil {
		return 0, fmt.Errorf("find max gameweek: %w", err)
	}

	if len(maxGameweek) == 0 {
		return 0, fmt.Errorf("no gameweeks found")
	}

	return maxGameweek[0].Gameweek, nil
}
func getNominated(txDao *daos.Dao, gameweek int) (bool, error) {
	records := []*models.Record{}

	err := txDao.RecordQuery("cards").
		AndWhere(dbx.HashExp{"gameweek": gameweek}).
		AndWhere(dbx.NewExp("nominatorUserID != ''")).
		AndWhere(dbx.NewExp("nominatorUserID IS NOT NULL")).
		All(&records)

	if err != nil {
		return false, fmt.Errorf("failed to check nomination status: %w", err)
	}

	return len(records) > 0, nil
}

// GameweekWinnerGet returns the winner for the most recent gameweek
func GameweekWinnerGet(c echo.Context) error {
	_, cancel := context.WithTimeout(c.Request().Context(), leaguesTimeout)
	defer cancel()

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}

	var winner types.GameweekWinner
	Nominated := false

	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			return fmt.Errorf("invalid team ID")
		}

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			return fmt.Errorf("default league not found: %w", err)
		}

		leagueID := defaultLeague.GetInt("leagueID")
		teamIDs, err := getLeagueMembers(txDao, leagueID)
		if err != nil {
			return err
		}

		if len(teamIDs) == 0 {
			return fmt.Errorf("no teams found in league")
		}

		gameweekNum, err := getMaxGameweek(txDao)
		if err != nil {
			return err
		}

		interfaceTeamIDs := make([]interface{}, len(teamIDs))
		for i, id := range teamIDs {
			interfaceTeamIDs[i] = id
		}

		log.Printf("trying to find winner")
		err = txDao.DB().
			Select("p.gameweek", "u.firstName", "u.teamName", "p.points", "p.userID as winnerID").
			From("aggregated_results p").
			InnerJoin("users u", dbx.NewExp("p.teamID = u.teamID")).
			Where(dbx.NewExp("p.gameweek = {:maxGW}", dbx.Params{"maxGW": gameweekNum})).
			AndWhere(dbx.In("p.teamID", interfaceTeamIDs...)).
			OrderBy("p.points DESC").
			Limit(defaultLimit).
			One(&winner)

		if err != nil {
			log.Print(err)
			return fmt.Errorf("find winner: %w", err)
		}

		Nominated, err = getNominated(txDao, gameweekNum)

		return nil
	})

	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	isWinner := false
	authUserID := record.Get("id")
	if winner.WinnerID == authUserID {
		isWinner = true
	}

	log.Printf("Winner found: %v", winner)

	return lib.Render(c, http.StatusOK, components.Statbar(winner.Gameweek, winner.FirstName, winner.TeamName, isWinner, Nominated))
}

func UserCardsGet(c echo.Context) error {
	_, cancel := context.WithTimeout(c.Request().Context(), leaguesTimeout)
	defer cancel()

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}

	var cards []types.DatabaseCard

	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing request for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		teamIDs, err := getLeagueMembers(txDao, leagueID)
		if err != nil {
			log.Printf("Failed to get league members: leagueID=%v, error=%v", leagueID, err)
			return err
		}

		if len(teamIDs) == 0 {
			log.Printf("No teams found in league: leagueID=%v", leagueID)
			return fmt.Errorf("no teams found in league")
		}
		log.Printf("Found %d teams in league", len(teamIDs))

		err = txDao.DB().
			Select("cards.*").
			From("cards").
			Where(dbx.NewExp("teamID = {:team_id} AND leagueID = {:league_id}", dbx.Params{"team_id": teamID, "league_id": leagueID})).
			AndWhere(dbx.NewExp("adminVerified = FALSE")).
			OrderBy("gameweek asc").
			All(&cards)

		if err != nil {
			log.Printf("Database query failed: teamID=%v, leagueID=%v, error=%v", teamID, leagueID, err)
			return fmt.Errorf("find cards: %w", err)
		}

		log.Printf("Retrieved %d cards for team %v in league %v", len(cards), teamID, leagueID)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	return lib.Render(c, http.StatusOK, components.FinesTable(cards))
}

func LeagueStandingsGet(c echo.Context) error {
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}

	var leagueRows []types.LeagueStandingRow
	var gameweek int

	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing league standings for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		teamIDs, err := getLeagueMembers(txDao, leagueID)
		if err != nil {
			log.Printf("Failed to get league members: leagueID=%v, error=%v", leagueID, err)
			return err
		}

		if len(teamIDs) == 0 {
			log.Printf("No teams found in league: leagueID=%v", leagueID)
			return fmt.Errorf("no teams found in league")
		}
		log.Printf("Found %d teams in league", len(teamIDs))

		gameweekNum, err := getMaxGameweek(txDao)
		if err != nil {
			return err
		}

		gameweek = gameweekNum

		interfaceTeamIDs := make([]interface{}, len(teamIDs))
		for i, id := range teamIDs {
			interfaceTeamIDs[i] = id
		}

		err = txDao.DB().
			Select(
				"ROW_NUMBER() OVER (ORDER BY ag.totalPoints desc) as position",
				"u.firstName",
				"u.lastName",
				"u.teamName",
				"ag.points as gameweekPoints",
				"ag.totalPoints",
				"(SELECT COUNT(*) FROM cards c2 WHERE c2.userID = ag.userID AND c2.adminVerified = FALSE) as cardCount").
			From("aggregated_results ag").
			LeftJoin("users u", dbx.NewExp("ag.userID = u.id")).
			Where(dbx.NewExp("ag.gameweek = {:maxGW}", dbx.Params{"maxGW": gameweekNum})).
			AndWhere(dbx.In("ag.teamID", interfaceTeamIDs...)).
			OrderBy("ag.totalPoints desc").
			All(&leagueRows)

		if err != nil {
			log.Printf("League standings query failed: teamID=%v, leagueID=%v, error=%v", teamID, leagueID, err)
			return fmt.Errorf("fetch standings: %w", err)
		}

		log.Printf("Retrieved standings for %d teams in league %v", len(leagueRows), leagueID)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	return lib.Render(c, http.StatusOK, components.LeagueTable(leagueRows, gameweek))
}

func CardSubmitPreview(c echo.Context) error {
	cardHash := c.FormValue("cardHash")

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}

	card, err := pb.Dao().FindFirstRecordByFilter(
		"cards",
		"cardHash = {:cardHash}",
		dbx.Params{"cardHash": cardHash},
	)
	// log.Print("card: %v", card)

	if err != nil {
		log.Printf("Error: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	nominatorTeamID := card.GetInt("nominatorTeamID")
	cardGameweek := card.GetInt("gameweek")
	cardType := card.Get("type")

	if nominatorTeamID != 0 {
		nominator, err := pb.Dao().FindFirstRecordByFilter(
			"users",
			"teamID = {:teamID}",
			dbx.Params{"teamID": nominatorTeamID},
		)
		if err != nil {
			log.Printf("query failed: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
		}

		var msg string
		if cardType == "nomination" {
			msg = fmt.Sprintf("a nomination by %s in gameweek %d", nominator.GetString("firstName"), cardGameweek)
		} else if cardType == "reverse" {
			msg = fmt.Sprintf("an uno reverse by %s in gameweek %d", nominator.GetString("firstName"), cardGameweek)
		}

		return lib.Render(c, http.StatusOK, components.SubmitPreview(msg, cardHash))
	}

	if nominatorTeamID == 0 {
		var msg string
		if cardType == "own_goals" {
			msg = fmt.Sprintf("an own goal in gameweek %d", cardGameweek)
		} else {
			msg = fmt.Sprintf("a red card in gameweek %d", cardGameweek)
		}
		return lib.Render(c, http.StatusOK, components.SubmitPreview(msg, cardHash))
	}

	return nil
}

func SubmitCard(c echo.Context) error {
	cardHash := c.FormValue("submitHash")
	log.Printf("Received card submission with hash: %s", cardHash)

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	card, err := pb.Dao().FindFirstRecordByFilter(
		"cards",
		"cardHash = {:cardHash}",
		dbx.Params{"cardHash": cardHash},
	)
	if err != nil {
		log.Printf("Error finding card with hash %s: %v", cardHash, err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}
	log.Printf("Found card with hash: %s", cardHash)

	card.Set("isCompleted", true)
	if err := pb.Dao().SaveRecord(card); err != nil {
		log.Printf("Error saving card with hash %s: %v", cardHash, err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to save card: %v", err))
	}
	log.Printf("Card with hash %s marked as completed", cardHash)

	// Redirect to home page after submitting
	log.Println("Redirecting to /app/profile")
	return lib.HtmxRedirect(c, "/app/profile")
}

func AdminVerifications(c echo.Context) error {
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	var cards []types.CardApprovals
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing league standings for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}
		log.Printf("Default league found: %v", defaultLeague)

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		// Check if the authenticated user is the admin of the league
		if record.Id != defaultLeague.GetString("adminUserID") {
			log.Printf("User %s is not the admin of league %v", record.Id, leagueID)
			return echo.NewHTTPError(http.StatusForbidden, "You are not authorized to view this page")
		}
		log.Printf("User %s is the admin of league %v", record.Id, leagueID)

		err = txDao.DB().
			Select(
				"C.*",
				"U.firstName as person").
			From("cards C").
			LeftJoin("users U", dbx.NewExp("C.userID = U.ID")).
			Where(dbx.NewExp("leagueID= {:leagueID}", dbx.Params{"leagueID": leagueID})).
			All(&cards)

		if err != nil {
			log.Printf("League standings query failed: teamID=%v, leagueID=%v, error=%v", teamID, leagueID, err)
			return fmt.Errorf("fetch standings: %w", err)
		}
		log.Printf("League standings fetched successfully for leagueID: %v", leagueID)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	log.Println("Rendering approval table")
	return lib.Render(c, http.StatusOK, components.ApprovalTable(cards))
}

func ApprovalPreview(c echo.Context) error {
	cardHash := c.FormValue("cardHash")

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}

	card, err := pb.Dao().FindFirstRecordByFilter(
		"cards",
		"cardHash = {:cardHash}",
		dbx.Params{"cardHash": cardHash},
	)
	// log.Print("card: %v", card)

	if err != nil {
		log.Printf("Error: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	nominatorTeamID := card.GetInt("nominatorTeamID")
	cardGameweek := card.GetInt("gameweek")
	cardType := card.Get("type")

	if nominatorTeamID != 0 {
		nominator, err := pb.Dao().FindFirstRecordByFilter(
			"users",
			"teamID = {:teamID}",
			dbx.Params{"teamID": nominatorTeamID},
		)
		if err != nil {
			log.Printf("query failed: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
		}

		var msg string
		if cardType == "nomination" {
			msg = fmt.Sprintf("a nomination by %s in gameweek %d", nominator.GetString("firstName"), cardGameweek)
		} else if cardType == "reverse" {
			msg = fmt.Sprintf("an uno reverse by %s in gameweek %d", nominator.GetString("firstName"), cardGameweek)
		}

		return lib.Render(c, http.StatusOK, components.SubmitPreview(msg, cardHash))
	}

	if nominatorTeamID == 0 {
		var msg string
		if cardType == "own_goals" {
			msg = fmt.Sprintf("an own goal in gameweek %d", cardGameweek)
		} else {
			msg = fmt.Sprintf("a red card in gameweek %d", cardGameweek)
		}
		return lib.Render(c, http.StatusOK, components.ApprovalPreview(msg, cardHash))
	}

	return nil
}

func ApproveCard(c echo.Context) error {
	cardHash := c.FormValue("submitHash")
	log.Printf("Received card submission with hash: %s", cardHash)

	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	card, err := pb.Dao().FindFirstRecordByFilter(
		"cards",
		"cardHash = {:cardHash}",
		dbx.Params{"cardHash": cardHash},
	)
	if err != nil {
		log.Printf("Error finding card with hash %s: %v", cardHash, err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}
	log.Printf("Found card with hash: %s", cardHash)

	card.Set("adminVerified", true)
	if err := pb.Dao().SaveRecord(card); err != nil {
		log.Printf("Error saving card with hash %s: %v", cardHash, err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to save card: %v", err))
	}
	log.Printf("Card with hash %s marked as completed", cardHash)

	// Redirect to home page after submitting
	log.Println("Redirecting to /app/profile")
	return lib.HtmxRedirect(c, "/app/profile")
}

func SingleNominationGet(c echo.Context) error {
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	var members []types.LeagueMembers
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing league standings for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}
		log.Printf("Default league found: %v", defaultLeague)

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		err = txDao.DB().
			Select(
				"concat(U.firstName, ' ', U.lastName) as userName",
				"l.leagueID",
				"l.userID",
				"l.teamID as userTeamID").
			From("leagues l").
			LeftJoin("users U", dbx.NewExp("l.userID = U.ID")).
			Where(dbx.NewExp("leagueID= {:leagueID}", dbx.Params{"leagueID": leagueID})).
			OrderBy("userName asc").
			All(&members)

		if err != nil {
			log.Printf("League standings query failed: teamID=%v, leagueID=%v, error=%v", teamID, leagueID, err)
			return fmt.Errorf("fetch standings: %w", err)
		}
		log.Printf("League standings fetched successfully for leagueID: %v", leagueID)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	log.Println("Rendering dropdown")

	return lib.Render(c, http.StatusOK, components.SingleNominate(members))
}

func SingleNominationPost(c echo.Context) error {
	log.Println("[SINGLE NOMINATION FUNCTION STARTING]")
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	selectedUserID := c.FormValue("selectedUser")
	log.Printf("Selected user ID: %s", selectedUserID)

	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.GetInt("teamID")
		nominatorUserID := record.GetString("id")
		log.Printf("Nominator user ID: %s", nominatorUserID)
		log.Printf("Team ID: %d", teamID)

		if teamID == 0 {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing nomination for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		gameweekNum, err := getMaxGameweek(txDao)
		if err != nil {
			log.Printf("Failed to get max gameweek: %v", err)
			return err
		}
		log.Printf("Using gameweek: %d", gameweekNum)

		cardHash := fmt.Sprintf("%s_%d_%d_%s_%d", selectedUserID, leagueID, gameweekNum, "nomination", 0)

		nomineeRecord, err := txDao.FindFirstRecordByFilter(
			"users",
			"id = {:userID}",
			dbx.Params{"userID": selectedUserID},
		)
		if err != nil {
			log.Printf("Failed to find nominee user: %v", err)
			return fmt.Errorf("fetch user: %w", err)
		}

		nomineeTeamID := nomineeRecord.GetInt("teamID")
		collection, err := pb.Dao().FindCollectionByNameOrId("cards")
		if err != nil {
			log.Printf("Failed to find cards collection: %v", err)
			return err
		}

		card := models.NewRecord(collection)

		card.Set("teamID", nomineeTeamID)
		card.Set("userID", selectedUserID)
		card.Set("nominatorTeamID", teamID)
		card.Set("nominatorUserID", nominatorUserID)
		card.Set("gameweek", gameweekNum)
		card.Set("type", "nomination")
		card.Set("leagueID", leagueID)
		card.Set("cardHash", cardHash)

		err = txDao.SaveRecord(card)
		if err != nil {
			log.Printf("Failed to save nomination card: %v", err)
			return fmt.Errorf("save nomination: %w", err)
		}
		log.Printf("Successfully created nomination card with hash: %s", cardHash)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	return lib.HtmxRedirect(c, "/app/profile")
}

func RandomNominationGet(c echo.Context) error {
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	var members []types.LeagueMembers
	err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		teamID := record.Get("teamID")
		if teamID == nil {
			log.Printf("Invalid team ID: %v", teamID)
			return fmt.Errorf("invalid team ID")
		}
		log.Printf("Processing league standings for teamID: %v", teamID)

		defaultLeague, err := getDefaultLeague(txDao, teamID)
		if err != nil {
			log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
			return fmt.Errorf("default league not found: %w", err)
		}
		log.Printf("Default league found: %v", defaultLeague)

		leagueID := defaultLeague.GetInt("leagueID")
		log.Printf("Found league ID: %v", leagueID)

		err = txDao.DB().
			Select(
				"concat(U.firstName, ' ', U.lastName) as userName",
				"l.leagueID",
				"l.userID",
				"l.teamID as userTeamID").
			From("leagues l").
			LeftJoin("users U", dbx.NewExp("l.userID = U.ID")).
			Where(dbx.NewExp("leagueID= {:leagueID}", dbx.Params{"leagueID": leagueID})).
			OrderBy("userName asc").
			All(&members)

		if err != nil {
			log.Printf("League standings query failed: teamID=%v, leagueID=%v, error=%v", teamID, leagueID, err)
			return fmt.Errorf("fetch standings: %w", err)
		}
		log.Printf("League standings fetched successfully for leagueID: %v", leagueID)
		return nil
	})

	if err != nil {
		log.Printf("Transaction failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	}

	log.Println("Rendering dropdown")

	finalMembers := getRandomMembers(members, 3)

	return lib.Render(c, http.StatusOK, components.RandomNominate(finalMembers))
}

func getRandomMembers(members []types.LeagueMembers, count int) []types.LeagueMembers {

	// Create copy of slice to shuffle
	shuffled := make([]types.LeagueMembers, len(members))
	copy(shuffled, members)

	// Fisher-Yates shuffle
	for i := len(shuffled) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	// Return first 'count' elements or all if less than count
	if len(shuffled) < count {
		return shuffled
	}
	return shuffled[:count]
}

func RandomNominationPost(c echo.Context) error {
	log.Println("[SINGLE NOMINATION FUNCTION STARTING]")
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Authentication failed: record=%v, ok=%v", record, ok)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid authentication")
	}
	log.Printf("Authenticated user: %s", record.Id)

	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Database connection failed: pb=%v, ok=%v", pb, ok)
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection unavailable")
	}
	log.Println("Database connection established")

	selectedUserID := c.FormValue("selectedUser")
	log.Printf("Selected user ID: %s", selectedUserID)

	// err := pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
	// 	teamID := record.GetInt("teamID")
	// 	nominatorUserID := record.GetString("id")
	// 	log.Printf("Nominator user ID: %s", nominatorUserID)
	// 	log.Printf("Team ID: %d", teamID)

	// 	if teamID == 0 {
	// 		log.Printf("Invalid team ID: %v", teamID)
	// 		return fmt.Errorf("invalid team ID")
	// 	}
	// 	log.Printf("Processing nomination for teamID: %v", teamID)

	// 	defaultLeague, err := getDefaultLeague(txDao, teamID)
	// 	if err != nil {
	// 		log.Printf("Default league lookup failed: teamID=%v, error=%v", teamID, err)
	// 		return fmt.Errorf("default league not found: %w", err)
	// 	}

	// 	leagueID := defaultLeague.GetInt("leagueID")
	// 	log.Printf("Found league ID: %v", leagueID)

	// 	gameweekNum, err := getMaxGameweek(txDao)
	// 	if err != nil {
	// 		log.Printf("Failed to get max gameweek: %v", err)
	// 		return err
	// 	}
	// 	log.Printf("Using gameweek: %d", gameweekNum)

	// 	cardHash := fmt.Sprintf("%s_%d_%d_%s_%d", selectedUserID, leagueID, gameweekNum, "nomination", 0)

	// 	nomineeRecord, err := txDao.FindFirstRecordByFilter(
	// 		"users",
	// 		"id = {:userID}",
	// 		dbx.Params{"userID": selectedUserID},
	// 	)
	// 	if err != nil {
	// 		log.Printf("Failed to find nominee user: %v", err)
	// 		return fmt.Errorf("fetch user: %w", err)
	// 	}

	// 	nomineeTeamID := nomineeRecord.GetInt("teamID")
	// 	collection, err := pb.Dao().FindCollectionByNameOrId("cards")
	// 	if err != nil {
	// 		log.Printf("Failed to find cards collection: %v", err)
	// 		return err
	// 	}

	// 	card := models.NewRecord(collection)

	// 	card.Set("teamID", nomineeTeamID)
	// 	card.Set("userID", selectedUserID)
	// 	card.Set("nominatorTeamID", teamID)
	// 	card.Set("nominatorUserID", nominatorUserID)
	// 	card.Set("gameweek", gameweekNum)
	// 	card.Set("type", "nomination")
	// 	card.Set("leagueID", leagueID)
	// 	card.Set("cardHash", cardHash)

	// 	err = txDao.SaveRecord(card)
	// 	if err != nil {
	// 		log.Printf("Failed to save nomination card: %v", err)
	// 		return fmt.Errorf("save nomination: %w", err)
	// 	}
	// 	log.Printf("Successfully created nomination card with hash: %s", cardHash)
	// 	return nil
	// })

	// if err != nil {
	// 	log.Printf("Transaction failed: %v", err)
	// 	return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process request: %v", err))
	// }
	return nil
	// return lib.HtmxRedirect(c, "/app/profile")
}
