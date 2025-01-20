package handlers

import (
	"log"
	"net/http"

	"github.com/cmcd97/bytesize/app/views"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/models"
)

const (
	StatusOK    = 200
	TeamIDField = "teamID"
)

func ProfileGet(c echo.Context) error {
	// Get auth record with error handling
	record, ok := c.Get(apis.ContextAuthRecordKey).(*models.Record)
	if !ok || record == nil {
		log.Printf("Failed to get auth record for request: %v", c.Request().RequestURI)
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Get team ID with proper type assertion
	teamIDInterface := record.Get(TeamIDField)
	// log.Printf("Raw teamID value: %v (type: %T)", teamIDInterface, teamIDInterface)

	// Handle different types that teamID could be
	var teamIDValue int
	switch v := teamIDInterface.(type) {
	case int:
		teamIDValue = v
	case float64:
		teamIDValue = int(v)
	case nil:
		return lib.Render(c, StatusOK, views.Setup())
	default:
		log.Printf("Unexpected teamID type: %T", v)
		return lib.Render(c, StatusOK, views.Setup())
	}

	// Check for zero value
	if teamIDValue == 0 {
		return lib.Render(c, StatusOK, views.Setup())
	}

	return lib.Render(c, StatusOK, views.Profile(record))
}

func Redirect(c echo.Context) error {
	return lib.HtmxRedirect(c, "/app/profile")
}
