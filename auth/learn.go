package auth

import (
	"github.com/cmcd97/bytesize/lib"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase/core"
)

func RegisterLearnRoutes(e *core.ServeEvent, group echo.Group) {
	group.GET("/learn", func(c echo.Context) error {
		return lib.Render(c, 200, Learn())
	})

	group.GET("/cards", func(c echo.Context) error {
		return lib.Render(c, 200, Cards())
	})

	group.GET("/winners", func(c echo.Context) error {
		return lib.Render(c, 200, Winners())
	})

	group.GET("/cards", func(c echo.Context) error {
		return lib.Render(c, 200, Cards())
	})

	group.GET("/reversals", func(c echo.Context) error {
		return lib.Render(c, 200, Reversals())
	})
}
