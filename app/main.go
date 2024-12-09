package app

import (
	"github.com/cmcd97/bytesize/app/handlers"
	"github.com/cmcd97/bytesize/middleware"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func InitAppRoutes(e *core.ServeEvent, pb *pocketbase.PocketBase) {
	appGroup := e.Router.Group("/app", middleware.LoadAuthContextFromCookie(pb), middleware.AuthGuard)

	appGroup.GET("", func(c echo.Context) error {
		return c.Redirect(303, "/app")
	})
	appGroup.GET("/profile", handlers.ProfileGet)
	appGroup.GET("/fpl_team_id", handlers.FetchFplTeam)
	appGroup.POST("/set_team_id", handlers.SetTeamID)
	appGroup.GET("/user_league_selection", handlers.UserLeaguesGet)
	appGroup.GET("/set_default_league", handlers.SetDefaultLeague)
	appGroup.POST("/intialise_league", handlers.InitialiseLeague)
	appGroup.GET("/check_for_league", handlers.CheckForLeague)
	appGroup.GET("/rules", handlers.RulesGet)
	appGroup.GET("/gamweek_winner", handlers.GameweekWinnerGet)
	appGroup.GET("/user_cards", handlers.UserCardsGet)
	appGroup.GET("/league_standings", handlers.LeagueStandingsGet)

	e.Router.GET("/", func(c echo.Context) error {
		return c.Redirect(303, "/app")
	})
}
