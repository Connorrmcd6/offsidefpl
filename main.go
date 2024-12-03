package main

import (
	"log"

	"github.com/cmcd97/bytesize/app"
	"github.com/cmcd97/bytesize/auth"
	"github.com/cmcd97/bytesize/lib"
	"github.com/cmcd97/bytesize/middleware"
	"github.com/labstack/echo/v5"

	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func main() {
	if err := godotenv.Load(); err != nil {
		println("Error loading .env file. Please create one or check the spelling of the file name")
		log.Fatal(err)
	}

	pb := pocketbase.New()

	// serves static files from the provided public dir (if exists)
	pb.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.Static("/public", "public")
		// Add middleware to inject PB instance into context
		e.Router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("pb", pb)
				return next(c)
			}
		})

		authGroup := e.Router.Group("/auth", middleware.LoadAuthContextFromCookie(pb))
		auth.RegisterLoginRoutes(e, *authGroup)
		auth.RegisterRegisterRoutes(e, *authGroup)

		app.InitAppRoutes(e, pb)

		lib.GetAllPlayers(e, pb)
		lib.GetAllFixtureEvents(e, pb)
		lib.GetAllFixtures(e, pb)

		// lib.DailyDataCheck(e, pb)
		lib.HourlyDataCheck(e, pb)

		// c := cron.New()
		// c.MustAdd("Weekly Fixture Update Check", "0 10 * * 2", func() {
		// 	lib.CheckForFixtureUpdates(e, pb)
		// })

		// c.MustAdd("Weekly Player Update Check", "0 10 * * 2", func() {
		// 	lib.CheckForPlayerUpdates(e, pb)
		// })

		// c.Start()

		// Add cron job to run daily ETL
		// c := cron.New()
		// c.MustAdd("daily ETL", "*/2 * * * *", func() {
		// 	lib.DailyDataCheck(e, pb)
		// })
		// c.Start()

		return nil
	})

	if err := pb.Start(); err != nil {
		log.Fatal(err)
	}
}
