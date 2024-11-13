package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/cmcd97/bytesize/app/components"
	"github.com/cmcd97/bytesize/app/components/types"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
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

	return lib.Render(c, http.StatusOK, components.TeamCheck(teamData))
}
