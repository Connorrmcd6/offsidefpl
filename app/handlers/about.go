package handlers

import (
	"github.com/cmcd97/bytesize/app/views"
	"github.com/cmcd97/bytesize/lib"
	"github.com/labstack/echo/v5"
)

func AboutGet(c echo.Context) error {

	return lib.Render(c, StatusOK, views.About())
}
