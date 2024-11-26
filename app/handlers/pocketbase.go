package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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
	return c.Redirect(303, "/app")
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
