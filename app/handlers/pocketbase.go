package handlers

import (
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
