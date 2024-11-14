package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/cmcd97/bytesize/app/components"
	"github.com/cmcd97/bytesize/app/components/types"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/models"
)

func FetchFplTeam(c echo.Context) error {
	teamID := c.QueryParam("teamID")
	if teamID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "teamID is required")
	}

	teamURL := fmt.Sprintf("https://fantasy.premierleague.com/api/entry/%s/", teamID)
	log.Printf("Fetching team data from: %s", teamURL)

	resp, err := http.Get(teamURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch team: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("FPL API error: %s", string(body))
		return echo.NewHTTPError(resp.StatusCode, "Failed to fetch team data")
	}

	var teamData types.FPLTeamResponse
	if err := json.Unmarshal(body, &teamData); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse team data: %v", err))
	}

	// return c.JSON(http.StatusOK, teamData)

	return lib.Render(c, http.StatusOK, components.TeamCheck(teamID, teamData))
}

func SetTeamID(c echo.Context) error {
	teamID := c.FormValue("teamID")
	log.Printf("Received teamID form value: %v", teamID)

	if teamID == "" {
		log.Printf("Error: Empty teamID received")
		return echo.NewHTTPError(http.StatusBadRequest, "teamID is required")
	}

	// Convert teamID string to int
	teamIDint, err := strconv.Atoi(teamID)
	if err != nil {
		log.Printf("Error converting teamID to int: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid team ID format")
	}
	log.Printf("Converted teamID to int: %d", teamIDint)

	// Get auth record with error handling
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	log.Printf("Found auth record for user: %s", record.Id)

	// Get PocketBase instance from context
	pb, ok := c.Get("pb").(*pocketbase.PocketBase)

	fmt.Println(ok)
	if !ok || pb == nil {
		log.Printf("Error: PocketBase instance is nil or type assertion failed")
		return echo.NewHTTPError(http.StatusInternalServerError, "Database connection error")
	}

	// Find and update record
	record, err = pb.Dao().FindRecordById("users", record.Id)
	if err != nil {
		log.Printf("Error finding user record: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to find user record")
	}
	log.Printf("Found user record in database: %s", record.Id)

	// Update teamID
	record.Set("teamID", teamIDint)
	log.Printf("Setting teamID to: %d for user: %s", teamIDint, record.Id)

	// Save changes
	if err := pb.Dao().SaveRecord(record); err != nil {
		log.Printf("Error saving record: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save team ID")
	}
	log.Printf("Successfully updated teamID for user: %s", record.Id)

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Team ID updated successfully",
	})
}
