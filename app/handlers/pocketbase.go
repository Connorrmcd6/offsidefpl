package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/cmcd97/bytesize/app/components"
	"github.com/cmcd97/bytesize/app/types"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/models"
)

func FindLeaguesByFPLID(dao *daos.Dao, teamID int) ([]*models.Record, error) {
	query := dao.RecordQuery("leagues").
		AndWhere(dbx.HashExp{"teamID": teamID})

	records := []*models.Record{}
	if err := query.All(&records); err != nil {
		return nil, err
	}

	return records, nil
}

func UserLeaguesGet(c echo.Context) error {
	// Get auth record with error handling
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	teamID := record.Get("teamID")

	// Get PocketBase instance from context
	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}

	// leagueStruct := []types.UserLeagueSelection{}

	// retrieve multiple "articles" collection records by a custom dbx expression(s)
	leagueRecordPointers, err := pb.Dao().FindRecordsByExpr("leagues",
		dbx.NewExp("teamID = {:teamID} order by leagueName asc", dbx.Params{"teamID": teamID}))
	if err != nil {
		log.Printf("Error finding league records: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to find league records")
	}

	leagueRecords := []types.UserLeagueSelection{}

	// Iterate through found records and convert to struct
	for _, record := range leagueRecordPointers {
		league := types.UserLeagueSelection{
			ID:          record.GetString("id"),
			LeagueID:    record.GetInt("leagueID"),
			UserID:      record.GetString("userID"),
			AdminTeamID: record.GetInt("adminTeamID"),
			UserTeamID:  record.GetInt("teamID"),
			LeagueName:  lib.ReplaceUnderscoresWithSpaces(record.GetString("leagueName")),
			IsLinked:    record.GetBool("isLinked"),
			IsActive:    record.GetBool("isActive"),
			IsDefault:   record.GetBool("isDefault"),
		}
		leagueRecords = append(leagueRecords, league)
	}

	return lib.Render(c, StatusOK, components.LeagueList(leagueRecords))

}
func SetDefaultLeague(c echo.Context) error {

	// Get leagueID from query parameters
	leagueID := c.QueryParam("leagueID")

	fmt.Printf("LeagueID parameter: %s\n", leagueID)

	if leagueID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "leagueID parameter is required")
	}

	leagueRecords, err := updateDefaultLeague(c, leagueID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update default league")
	}

	return lib.Render(c, StatusOK, components.LeagueList(leagueRecords))

}

func updateDefaultLeague(c echo.Context, leagueID string) ([]types.UserLeagueSelection, error) {
	// Get auth record with error handling
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return []types.UserLeagueSelection{}, echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	authUserID := record.Id

	// Get PocketBase instance from context
	pb, ok := c.Get("pb").(*pocketbase.PocketBase)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return []types.UserLeagueSelection{}, echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}

	// get old default league and set to false
	defaultLeaguePointer, err := pb.Dao().FindFirstRecordByFilter(
		"leagues", "isDefault = True && userID = {:userID}",
		dbx.Params{"userID": authUserID},
	)

	if err != nil {
		log.Printf("Error finding default league record: %v", err)
		return []types.UserLeagueSelection{}, echo.NewHTTPError(http.StatusInternalServerError, "Failed to find default league record")
	}

	defaultLeagueRecord, err := pb.Dao().FindRecordById("leagues", defaultLeaguePointer.Id)
	if err != nil {
		log.Printf("Error finding default league record: %v", err)
		return []types.UserLeagueSelection{}, err
	}
	defaultLeagueRecord.Set("isDefault", false)
	if err := pb.Dao().SaveRecord(defaultLeagueRecord); err != nil {
		return []types.UserLeagueSelection{}, err
	}

	// update new default league and set to true
	newDefaultLeagueRecord, err := pb.Dao().FindRecordById("leagues", leagueID)
	if err != nil {
		log.Printf("Error finding new default league record: %v", err)
	}

	newDefaultLeagueRecord.Set("isDefault", true)
	if err := pb.Dao().SaveRecord(newDefaultLeagueRecord); err != nil {
		return []types.UserLeagueSelection{}, err
	}

	// retrieve multiple "articles" collection records by a custom dbx expression(s)
	leagueRecordPointers, err := pb.Dao().FindRecordsByExpr("leagues",
		dbx.NewExp("userID = {:userID} order by leagueName asc", dbx.Params{"userID": authUserID}))
	if err != nil {
		log.Printf("Error finding league records: %v", err)
		return []types.UserLeagueSelection{}, echo.NewHTTPError(http.StatusInternalServerError, "Failed to find league records")
	}

	leagueRecords := []types.UserLeagueSelection{}

	// Iterate through found records and convert to struct
	for _, record := range leagueRecordPointers {
		league := types.UserLeagueSelection{
			ID:          record.GetString("id"),
			LeagueID:    record.GetInt("leagueID"),
			UserID:      record.GetString("userID"),
			AdminTeamID: record.GetInt("adminTeamID"),
			UserTeamID:  record.GetInt("teamID"),
			LeagueName:  lib.ReplaceUnderscoresWithSpaces(record.GetString("leagueName")),
			IsLinked:    record.GetBool("isLinked"),
			IsActive:    record.GetBool("isActive"),
			IsDefault:   record.GetBool("isDefault"),
		}
		leagueRecords = append(leagueRecords, league)
	}

	return leagueRecords, nil

}
