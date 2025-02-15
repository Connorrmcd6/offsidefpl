package auth

import (
	"regexp"

	"github.com/a-h/templ"
	"github.com/cmcd97/bytesize/lib"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

type RegisterFormValue struct {
	username       string
	password       string
	passwordRepeat string
	stayLoggedIn   string
}

func (rfv RegisterFormValue) Validate() error {
	return validation.ValidateStruct(&rfv,
		validation.Field(&rfv.username,
			validation.Required.Error("Username is required"),
			validation.Length(3, 50).Error("Username must be between 3 and 50 characters"),
			validation.Match(regexp.MustCompile(`^[^\s]+$`)).Error("Username cannot contain spaces"),
		),
		validation.Field(&rfv.password,
			validation.Required.Error("Password is required"),
		),
		validation.Field(&rfv.passwordRepeat,
			validation.Required.Error("Please confirm your password"),
			validation.By(func(value interface{}) error {
				repeat := value.(string)
				if repeat != rfv.password {
					return validation.NewError("validation_passwords_mismatch", "Passwords do not match")
				}
				return nil
			}),
		),
	)
}

func getRegisterFormValue(c echo.Context) RegisterFormValue {
	return RegisterFormValue{
		username:       c.FormValue("username"),
		password:       c.FormValue("password"),
		passwordRepeat: c.FormValue("passwordRepeat"),
		stayLoggedIn:   c.FormValue("stayLoggedIn"),
	}
}

func RegisterRegisterRoutes(e *core.ServeEvent, group echo.Group) {
	group.GET("/register", func(c echo.Context) error {
		if c.Get(apis.ContextAuthRecordKey) != nil {
			return c.Redirect(302, "/app/profile")
		}

		return lib.Render(c, 200, Register(RegisterFormValue{}, nil))
	})

	group.POST("/register", func(c echo.Context) error {
		form := getRegisterFormValue(c)
		err := form.Validate()

		if err == nil {
			err = lib.Register(e, c, form.username, form.password, form.passwordRepeat, form.stayLoggedIn)
		}

		if err != nil {
			component := lib.HtmxRender(
				c,
				func() templ.Component { return RegisterForm(form, err) },
				func() templ.Component { return Register(form, err) },
			)
			return lib.Render(c, 200, component)
		}

		return lib.HtmxRedirect(c, "/app/profile")
	})
}
