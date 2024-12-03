package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/cmcd97/bytesize/app/types"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/tools/cron"
)

func DailyDataCheck(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[DailyDataCheck] Starting daily gameweek completion check")

	todayMidnight := getTodayMidnight()
	timestamps := []types.DataUpdateDates{}

	err := pb.DB().NewQuery("SELECT max(kickoff) as ts FROM fixtures GROUP BY date(kickoff) ORDER BY gameweek ASC").All(&timestamps)
	if err != nil {
		log.Printf("[DailyDataCheck] Database query failed: %v", err)
		return fmt.Errorf("failed to process fixtures: %w", err)
	}

	for _, ts := range timestamps {
		fixtureEndDate, err := roundUpToNextDay(ts.TS)
		if err != nil {
			log.Printf("[DailyDataCheck] Date parsing error: %v", err)
			return fmt.Errorf("failed to parse date: %w", err)
		}

		if todayMidnight == fixtureEndDate {
			log.Println("[DailyDataCheck] Triggering hourly checks - gameweek completed or test condition met")
			c := cron.New()
			c.MustAdd("Hourly ETL", "0 0 * * *", func() {
				// HourlyDataCheck(e, pb, c)
			})
			c.Start()
			return nil
		} else {
			log.Println("[DailyDataCheck] No fixture completion detected")
			return nil
		}
	}
	return nil
}

func HourlyDataCheck(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[HourlyDataCheck] Starting hourly data availability check")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	endpoint := "https://fantasy.premierleague.com/api/event-status/"
	resp, err := client.Get(endpoint)
	if err != nil {
		log.Printf("[HourlyDataCheck] API request failed: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch league status: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[HourlyDataCheck] Unexpected status code: %d", resp.StatusCode)
		return echo.NewHTTPError(http.StatusInternalServerError, "unexpected status code from API")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[HourlyDataCheck] Failed to read response: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var response types.FixtureUpdateStatus
	if err := json.Unmarshal(body, &response); err != nil {
		log.Printf("[HourlyDataCheck] Failed to parse response: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse league status data: %v", err))
	}

	if len(response.Status) == 0 {
		log.Println("[HourlyDataCheck] No status data received")
		return echo.NewHTTPError(http.StatusInternalServerError, "no status data received")
	}

	if response.Leagues == "Updated" {
		log.Println("[HourlyDataCheck] League data is updated - pulling latest data")
		return nil
	}

	return nil
}

func roundUpToNextDay(dateStr string) (time.Time, error) {
	// Parse the input date
	t, err := time.Parse("2006-01-02 15:04:05.000Z", dateStr)
	if err != nil {
		return time.Time{}, err
	}

	// Add 24 hours and truncate to midnight
	nextDay := t.Add(24 * time.Hour).Truncate(24 * time.Hour)
	return nextDay, nil
}

func getTodayMidnight() time.Time {
	return time.Now().Truncate(24 * time.Hour)
}

func CheckForFixtureUpdates(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[FixtureUpdate] Starting fixture update check")

	// Fetch existing fixtures from DB
	var existingFixtures []types.DatabaseFixtures
	err := pb.Dao().DB().
		NewQuery("SELECT * FROM fixtures").
		All(&existingFixtures)

	if err != nil {
		return fmt.Errorf("failed to get existing fixtures: %w", err)
	}

	// Create map of existing fixtures for easy lookup
	fixturesMap := make(map[int]types.DatabaseFixtures)
	for _, f := range existingFixtures {
		fixturesMap[f.FixtureID] = f
	}

	// Fetch latest fixtures from API
	endpoint := "https://fantasy.premierleague.com/api/fixtures/"
	resp, err := http.Get(endpoint)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch fixtures: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var apiFixtures []types.Fixtures
	if err := json.Unmarshal(body, &apiFixtures); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse fixture data: %v", err))
	}

	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("fixtures")
		if err != nil {
			return fmt.Errorf("error finding collection: %w", err)
		}

		for _, apiFixture := range apiFixtures {
			existingFixture, exists := fixturesMap[apiFixture.FixtureID]

			if !exists {
				// New fixture - insert
				record := models.NewRecord(collection)
				record.Set("fixtureID", apiFixture.FixtureID)
				record.Set("gameweek", apiFixture.Gameweek)
				record.Set("kickoff", apiFixture.Kickoff)
				record.Set("homeTeamID", apiFixture.HomeTeamID)
				record.Set("awayTeamID", apiFixture.AwayTeamID)

				if err := txDao.SaveRecord(record); err != nil {
					return fmt.Errorf("error saving new fixture: %w", err)
				}
				log.Printf("[FixtureUpdate] Added new fixture ID: %d", apiFixture.FixtureID)
				continue
			}

			// Check if fixture needs updating
			if existingFixture.ToAPIFixtures() != apiFixture {
				record, err := txDao.FindFirstRecordByData("fixtures", "fixtureID", existingFixture.FixtureID)
				if err != nil {
					return fmt.Errorf("error finding existing fixture: %w", err)
				}

				record.Set("gameweek", apiFixture.Gameweek)
				record.Set("kickoff", apiFixture.Kickoff)
				record.Set("homeTeamID", apiFixture.HomeTeamID)
				record.Set("awayTeamID", apiFixture.AwayTeamID)

				if err := txDao.SaveRecord(record); err != nil {
					return fmt.Errorf("error updating fixture: %w", err)
				}
				log.Printf("[FixtureUpdate] Updated fixture ID: %d", apiFixture.FixtureID)
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[FixtureUpdate] Fixture update check completed")
	return nil
}

func CheckForPlayerUpdates(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[PlayerUpdate] Starting player update check")

	// Fetch existing players from DB
	var existingPlayers []types.DatabasePlayer
	err := pb.Dao().DB().
		NewQuery("SELECT * FROM players").
		All(&existingPlayers)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get existing players: %w", err)
	}

	// Create map of existing players for easy lookup
	playersMap := make(map[int]types.DatabasePlayer)
	for _, p := range existingPlayers {
		playersMap[p.PlayerID] = p
	}

	// Fetch latest players from API
	endpoint := "https://fantasy.premierleague.com/api/bootstrap-static/"
	resp, err := http.Get(endpoint)
	if err != nil {
		fmt.Println(err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch response: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var apiResponseFull types.FPLResponse
	if err := json.Unmarshal(body, &apiResponseFull); err != nil {
		fmt.Println(err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse player data: %v", err))
	}
	apiPlayers := apiResponseFull.Elements

	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("players")
		if err != nil {
			fmt.Println(err)
			return fmt.Errorf("error finding collection: %w", err)
		}

		for _, apiPlayer := range apiPlayers {
			existingPlayer, exists := playersMap[apiPlayer.ID]

			if !exists {
				// New player - insert
				record := models.NewRecord(collection)
				record.Set("playerID", apiPlayer.ID)
				record.Set("playerTeamID", apiPlayer.Team)
				record.Set("playerName", apiPlayer.WebName)

				if err := txDao.SaveRecord(record); err != nil {
					fmt.Println(err)
					return fmt.Errorf("error saving new player: %w", err)
				}
				log.Printf("[PlayerUpdate] Added new player ID: %d", apiPlayer.ID)
				continue
			}

			// Check if player needs updating
			if existingPlayer.ToAPIPlayers() != apiPlayer {
				record, err := txDao.FindFirstRecordByData("players", "playerID", existingPlayer.PlayerID)
				if err != nil {
					fmt.Println(err)
					return fmt.Errorf("error finding existing player: %w", err)
				}

				record.Set("playerTeamID", apiPlayer.Team)
				record.Set("playerName", apiPlayer.WebName)

				if err := txDao.SaveRecord(record); err != nil {
					fmt.Println(err)
					return fmt.Errorf("error updating player: %w", err)
				}
				log.Printf("[PlayerUpdate] Updated player ID: %d", apiPlayer.ID)
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[PlayerUpdate] Player update check completed")
	return nil
}

// func checkForEventsUpdate(e *core.ServeEvent, pb *pocketbase.PocketBase) error{
// 	log.Println("[EventUpdate] Starting event update check")

// 	// Fetch existing players from DB
// 	var existingEvents []types.DatabaseStats
// 	err := pb.Dao().DB().
// 		NewQuery("SELECT * FROM events").
// 		All(&existingEvents)

// 	if err != nil {
// 		fmt.Println(err)
// 		return fmt.Errorf("failed to get existing players: %w", err)
// 	}

// 	// Create map of existing players for easy lookup
// 	eventsMap := make(map[int]types.DatabaseStats)
// 	for _, e := range existingEvents {
// 		eventsMap[e.EventID] = e
// 	}

// 	fmt.Print(eventsMap)
// 	// // Fetch latest players from API
// 	// endpoint := "https://fantasy.premierleague.com/api/bootstrap-static/"
// 	// resp, err := http.Get(endpoint)
// 	// if err != nil {
// 	// 	fmt.Println(err)
// 	// 	return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch response: %v", err))
// 	// }
// 	// defer resp.Body.Close()

// 	// body, err := io.ReadAll(resp.Body)
// 	// if err != nil {
// 	// 	fmt.Println(err)
// 	// 	return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
// 	// }

// 	// var apiResponseFull types.FPLResponse
// 	// if err := json.Unmarshal(body, &apiResponseFull); err != nil {
// 	// 	fmt.Println(err)
// 	// 	return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse response data: %v", err))
// 	// }
// 	// apiEvents := apiResponseFull.Elements

// 	// err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
// 	// 	collection, err := txDao.FindCollectionByNameOrId("events")
// 	// 	if err != nil {
// 	// 		fmt.Println(err)
// 	// 		return fmt.Errorf("error finding collection: %w", err)
// 	// 	}

// 	// 	for _, apiEvent := range apiEvents {
// 	// 		existingPlayer, exists := eventsMap[apiPlayer.ID]

// 	// 		if !exists {
// 	// 			// New player - insert
// 	// 			record := models.NewRecord(collection)
// 	// 			record.Set("playerID", apiPlayer.ID)
// 	// 			record.Set("playerTeamID", apiPlayer.Team)
// 	// 			record.Set("playerName", apiPlayer.WebName)

// 	// 			if err := txDao.SaveRecord(record); err != nil {
// 	// 				fmt.Println(err)
// 	// 				return fmt.Errorf("error saving new player: %w", err)
// 	// 			}
// 	// 			log.Printf("[PlayerUpdate] Added new player ID: %d", apiPlayer.ID)
// 	// 			continue
// 	// 		}

// 	// 		// Check if player needs updating
// 	// 		if existingPlayer.ToAPIPlayers() != apiPlayer {
// 	// 			record, err := txDao.FindFirstRecordByData("players", "playerID", existingPlayer.PlayerID)
// 	// 			if err != nil {
// 	// 				fmt.Println(err)
// 	// 				return fmt.Errorf("error finding existing player: %w", err)
// 	// 			}

// 	// 			record.Set("playerTeamID", apiPlayer.Team)
// 	// 			record.Set("playerName", apiPlayer.WebName)

// 	// 			if err := txDao.SaveRecord(record); err != nil {
// 	// 				fmt.Println(err)
// 	// 				return fmt.Errorf("error updating player: %w", err)
// 	// 			}
// 	// 			log.Printf("[PlayerUpdate] Updated player ID: %d", apiPlayer.ID)
// 	// 		}
// 	// 	}
// 	// 	return nil
// 	// })

// 	// if err != nil {
// 	// 	return fmt.Errorf("transaction failed: %w", err)
// 	// }

// 	log.Println("[PlayerUpdate] Player update check completed")
// 	return nil
// }

// func checkForResultsUpdate(e *core.ServeEvent, pb *pocketbase.PocketBase) error{}

// func updateCards(e *core.ServeEvent, pb *pocketbase.PocketBase) error {

// }

// func updateResultsAggregated(e *core.ServeEvent, pb *pocketbase.PocketBase) error{}
