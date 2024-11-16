package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cmcd97/bytesize/app/components"
	"github.com/cmcd97/bytesize/app/types"
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
		errorMessage := "Failed to fetch team data"
		return lib.Render(c, resp.StatusCode, components.ErrorAlert(errorMessage))
	}

	var teamData types.FPLUser
	if err := json.Unmarshal(body, &teamData); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse team data: %v", err))
	}

	var leagueResponse types.FPLResponseLeagues
	if err := json.Unmarshal(body, &leagueResponse); err != nil {
		return fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Access leagues data
	leagueData := leagueResponse.Leagues
	// Access classic leagues
	classicLeagues := leagueData.Classic
	// fmt.Println(classicLeagues)
	teamIDInt, err := strconv.Atoi(teamID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to convert teamID to int: %v", err))
	}
	// add user leagues to user struct
	userCustomLeagues := []types.FPLUserLeague{}
	for _, league := range classicLeagues {
		if league.LeagueType == "s" {
			continue
		}
		// Parse date string and get year
		dateStr := league.Created
		t, err := time.Parse(time.RFC3339Nano, dateStr)
		if err != nil {
			// handle error
		}
		year := t.Year()
		userCustomLeagues = append(userCustomLeagues, types.FPLUserLeague{
			LeagueID:        league.LeagueID,
			AdminTeamID:     0,
			UserTeamID:      teamIDInt,
			LeagueName:      lib.ReplaceSpacesWithUnderscores(league.Name),
			SeasonStartYear: year,
			UserID:          "temp",
			IsLinked:        false,
			IsActive:        false,
		})
	}

	//store userCustomLeagues in a temporary cookie that expires in 5 minutes
	cookie := new(http.Cookie)
	cookie.Name = "userCustomLeagues"
	cookie.Value = fmt.Sprintf("%v", userCustomLeagues)
	cookie.Expires = time.Now().Add(5 * time.Minute)
	c.SetCookie(cookie)

	return lib.Render(c, http.StatusOK, components.TeamCheck(teamID, teamData))
}

func convertToJSON(input string) (string, error) {
	// Log input
	log.Printf("Converting to JSON, input: %s", input)

	if input == "" {
		return "", fmt.Errorf("empty input string")
	}

	// Remove outer brackets and split into items
	input = strings.Trim(input, "[]")
	items := strings.Split(input, "} {")

	log.Printf("Split into %d items", len(items))
	var jsonObjects []string

	for i, item := range items {
		// Clean up each item
		item = strings.Trim(item, "{}")
		fields := strings.Fields(item)

		// Validate fields length
		if len(fields) != 8 {
			return "", fmt.Errorf("item %d has %d fields, expected 8: %v", i, len(fields), fields)
		}

		// Log fields for debugging
		log.Printf("Processing item %d: %v", i, fields)

		// Map to struct fields in order
		jsonObj := fmt.Sprintf(`{
            "LeagueID": %s,
            "AdminTeamID": %s,
            "UserTeamID": %s,
            "LeagueName": "%s",
            "SeasonStartYear": %s,
            "UserID": "%s",
            "IsLinked": %s,
            "IsActive": %s
        }`, fields[0], fields[1], fields[2], fields[3], fields[4], fields[5], fields[6], fields[7])

		// Validate JSON format
		var js map[string]interface{}
		if err := json.Unmarshal([]byte(jsonObj), &js); err != nil {
			return "", fmt.Errorf("invalid JSON format for item %d: %v", i, err)
		}

		jsonObjects = append(jsonObjects, jsonObj)
	}

	result := "[" + strings.Join(jsonObjects, ",") + "]"
	log.Printf("Conversion completed, result length: %d", len(result))

	return result, nil
}

func SetTeamID(c echo.Context) error {

	teamID := c.FormValue("teamID")
	log.Printf("Received teamID form value: %v", teamID)

	firstName := c.FormValue("firstName")
	log.Printf("Received fisrtName form value: %v", firstName)

	lastName := c.FormValue("lastName")
	log.Printf("Received lastName form value: %v", lastName)

	teamName := c.FormValue("teamName")
	log.Printf("Received teamName form value: %v", teamName)

	//get userCustomLeagues from cookie
	cookie, err := c.Cookie("userCustomLeagues")
	log.Printf("Received userCustomLeagues cookie")
	if err != nil {
		log.Printf("Error getting userCustomLeagues cookie: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to get userCustomLeagues cookie: %v", err))
	}

	jsonStr, err := convertToJSON(cookie.Value)
	if err != nil {
		log.Printf("Error converting userCustomLeagues cookie value to JSON: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to convert userCustomLeagues cookie value to JSON: %v", err))
	}

	log.Printf("Converted userCustomLeagues cookie value to JSON: %v", jsonStr)

	userCustomLeagues := []types.FPLUserLeague{}
	log.Print("Received userCustomLeagues cookie value")
	err = json.Unmarshal([]byte(jsonStr), &userCustomLeagues)
	if err != nil {
		log.Printf("Error unmarshalling userCustomLeagues: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to unmarshal userCustomLeagues: %v", err))
	}

	if teamID == "" {
		log.Printf("Error: Empty teamID received")
		return echo.NewHTTPError(http.StatusBadRequest, "teamID is required")
	}

	if firstName == "" {
		log.Printf("Error: Empty firstName received")
		return echo.NewHTTPError(http.StatusBadRequest, "firstName is required")
	}

	if lastName == "" {
		log.Printf("Error: Empty lastName received")
		return echo.NewHTTPError(http.StatusBadRequest, "lastName is required")
	}

	if teamName == "" {
		log.Printf("Error: Empty teamName received")
		return echo.NewHTTPError(http.StatusBadRequest, "teamName is required")
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
	record.Set("firstName", firstName)
	log.Printf("Setting firstName to: %s for user: %s", firstName, record.Id)
	record.Set("lastName", lastName)
	log.Printf("Setting lastName to: %s for user: %s", lastName, record.Id)
	record.Set("teamName", teamName)
	log.Printf("Setting teamName to: %s for user: %s", teamName, record.Id)

	// Save changes
	if err := pb.Dao().SaveRecord(record); err != nil {
		log.Printf("Error saving record: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save team ID")
	}
	log.Printf("Successfully updated teamID for user: %s", record.Id)

	// Update userCustomLeagues with user ID

	collection, err := pb.Dao().FindCollectionByNameOrId("leagues")
	if err != nil {
		return err
	}
	for _, league := range userCustomLeagues {
		league.UserID = record.Id
		record := models.NewRecord(collection)
		record.Set("leagueID", league.LeagueID)
		record.Set("adminTeamID", league.AdminTeamID)
		record.Set("teamID", league.UserTeamID)
		record.Set("leagueName", league.LeagueName)
		record.Set("seasonStartYear", league.SeasonStartYear)
		record.Set("userID", league.UserID)
		record.Set("isLinked", league.IsLinked)
		record.Set("isActive", league.IsActive)
		if err := pb.Dao().SaveRecord(record); err != nil {
			log.Printf("Error saving record: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to league")
		}
		log.Printf("Successfully created league record for user: %s", record.Id)
	}

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Team ID updated successfully",
	})
}