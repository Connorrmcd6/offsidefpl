package lib

import (
	"fmt"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func GetAllPlayers(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	type PlayerCount struct {
		Count int `db:"count" json:"count"`
	}

	playerCount := PlayerCount{}

	err := pb.Dao().DB().
		NewQuery("SELECT count(*) as count FROM players").
		One(&playerCount)
	if err != nil {
		return err
	}

	if playerCount.Count > 0 {
		return nil
	}

	fmt.Print("no players in pocketbase, fetch them from fpl")

	return nil
}
