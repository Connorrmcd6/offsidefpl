package lib

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cmcd97/bytesize/middleware"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/tokens"
)

type Users struct {
	models.Record
}

func (*Users) TableName() string {
	return "users"
}

func Login(e *core.ServeEvent, c echo.Context, username string, password string, stayLoggedIn string) error {
	user, err := e.App.Dao().FindAuthRecordByUsername("users", username)
	if err != nil {
		return fmt.Errorf("Login failed")
	}

	valid := user.ValidatePassword(password)
	if !valid {
		return fmt.Errorf("Login failed")
	}

	return setAuthToken(e.App, c, user, stayLoggedIn)
}

func Register(e *core.ServeEvent, c echo.Context, username string, password string, passwordRepeat string, stayLoggedIn string) error {
	user, _ := e.App.Dao().FindAuthRecordByUsername("users", username)
	if user != nil {
		return fmt.Errorf("username already taken")
	}

	if password != passwordRepeat {
		return fmt.Errorf("passwords don't match")
	}

	collection, err := e.App.Dao().FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}

	newUser := models.NewRecord(collection)
	newUser.SetPassword(password)
	newUser.SetUsername(username)

	if err = e.App.Dao().SaveRecord(newUser); err != nil {
		return err
	}

	return setAuthToken(e.App, c, newUser, stayLoggedIn)
}

func setAuthToken(app core.App, c echo.Context, user *models.Record, stayLoggedIn string) error {
	s, tokenErr := tokens.NewRecordAuthToken(app, user)
	if tokenErr != nil {
		fmt.Println("Error generating auth token:", tokenErr)
		return fmt.Errorf("Login failed")
	}

	fmt.Printf("Setting auth token - stayLoggedIn: '%s'\n", stayLoggedIn)

	// Default to session cookie
	maxAge := 0                               // Changed from -1 to 0 for session cookie
	expires := time.Now().Add(24 * time.Hour) // Set default expiry

	if stayLoggedIn == "on" {
		maxAge = 60 * 60 * 24 * 90 // 3 months in seconds
		expires = time.Now().Add(time.Hour * 24 * 90)
	}

	fmt.Printf("Cookie settings - MaxAge: %d, Expires: %v\n", maxAge, expires)

	c.SetCookie(&http.Cookie{
		Name:     middleware.AuthCookieName,
		Value:    s,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		MaxAge:   maxAge,
		Expires:  expires,
	})

	return nil
}
