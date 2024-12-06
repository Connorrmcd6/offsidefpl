package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cmcd97/bytesize/app/types"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
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
			c.MustAdd("Hourly ETL", "0 * * * *", func() {
				HourlyDataCheck(e, pb, c)
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

func HourlyDataCheck(e *core.ServeEvent, pb *pocketbase.PocketBase, c *cron.Cron) error {
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
		gameweek := response.Status[0].Event
		err := checkForEventsUpdate(pb)
		if err != nil {
			log.Printf("[HourlyDataCheck] Failed to update events: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to update events: %v", err))
		}
		err = updateGameweekResults(pb, gameweek)
		if err != nil {
			log.Printf("[HourlyDataCheck] Failed to update results: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to update results: %v", err))
		}
		err = updateCards(pb)
		if err != nil {
			log.Printf("[HourlyDataCheck] Failed to update cards: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to update cards: %v", err))
		}
		// updated results aggregated
		err = updateResultsAggregated(pb)
		if err != nil {
			log.Printf("[HourlyDataCheck] Failed to update results aggregated: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to update results aggregated: %v", err))
		}

		// Stop cron job
		c.Remove("Hourly ETL")
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

func checkForEventsUpdate(pb *pocketbase.PocketBase) error {
	log.Println("[EventUpdate] Starting event update check")

	// Fetch existing players from DB
	var existingEvents []types.DatabaseFixtureStats
	err := pb.Dao().DB().
		NewQuery("SELECT * FROM events").
		All(&existingEvents)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get existing eventa: %w", err)
	}

	// Create map of existing players for easy lookup
	eventsMap := make(map[string]types.DatabaseFixtureStats)
	for _, e := range existingEvents {
		eventsMap[e.EventHash] = e
	}

	// Fetch latest players from API
	endpoint := "https://fantasy.premierleague.com/api/fixtures/"
	resp, err := http.Get(endpoint)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch events: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var apiEvents []types.FixtureStats
	if err := json.Unmarshal(body, &apiEvents); err != nil {
		fmt.Print(err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse team data: %v", err))
	}

	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("events")
		if err != nil {
			fmt.Println(err)
			return fmt.Errorf("error finding collection: %w", err)
		}

		for _, apiEvent := range apiEvents {
			if !apiEvent.Finished {
				continue
			}

			for _, stat := range apiEvent.Stats {

				// Process home team stats
				for _, home := range stat.Home {
					apiEventHash := CreateEventHash(apiEvent.FixtureID, apiEvent.Gameweek, string(stat.Identifier))
					_, exists := eventsMap[apiEventHash]

					if !exists {
						// New player - insert
						record := models.NewRecord(collection)
						record.Set("eventHash", apiEventHash)
						record.Set("fixtureID", apiEvent.FixtureID)
						record.Set("gameweek", apiEvent.Gameweek)
						record.Set("playerID", home.Element)
						record.Set("eventType", string(stat.Identifier))
						record.Set("eventValue", home.Value)

						if err := txDao.SaveRecord(record); err != nil {
							fmt.Println(err)
							return fmt.Errorf("error saving new event: %w", err)
						}
						log.Printf("[EventUpdate] Added new event ID: %s", apiEventHash)
						continue

					}
				}

				// Process away team stats
				for _, away := range stat.Away {
					apiEventHash := CreateEventHash(apiEvent.FixtureID, apiEvent.Gameweek, string(stat.Identifier))
					_, exists := eventsMap[apiEventHash]

					if !exists {
						// New player - insert
						record := models.NewRecord(collection)
						record.Set("eventHash", apiEventHash)
						record.Set("fixtureID", apiEvent.FixtureID)
						record.Set("gameweek", apiEvent.Gameweek)
						record.Set("playerID", away.Element)
						record.Set("eventType", string(stat.Identifier))
						record.Set("eventValue", away.Value)

						if err := txDao.SaveRecord(record); err != nil {
							fmt.Println(err)
							return fmt.Errorf("error saving new event: %w", err)
						}
						log.Printf("[EventUpdate] Added new event ID: %s", apiEventHash)
						continue

					}
				}
			}

		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[EventUpdate] Player update check completed")
	return nil
}

func updateGameweekResults(pb *pocketbase.PocketBase, gameweek int) error {
	log.Println("[ResultsUpdate] Starting results update check")
	// fetch existing results from DB
	var existingResults []types.DatabaseResults
	err := pb.Dao().DB().
		NewQuery(`
		SELECT
		gameweek,
		userID,
		teamID,
		points,
		transfers,
		hits,
		benchPoints,
		activeChip,
		pos_1,
		pos_2,
		pos_3,
		pos_4,
		pos_5,
		pos_6,
		pos_7,
		pos_8,
		pos_9,
		pos_10,
		pos_11,
		pos_12,
		pos_13,
		pos_14,
		pos_15
		FROM results`).
		All(&existingResults)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get stored results: %w", err)
	}

	// Create map of existing results for easy lookup
	resultsMap := make(map[string]types.DatabaseResults)

	for _, r := range existingResults {
		mapKey := fmt.Sprintf("%d_%s", r.Gameweek, r.UserID)
		resultsMap[mapKey] = r
	}

	// Fetch existing users from DB
	var users []types.DatabaseUsers
	err = pb.Dao().DB().
		NewQuery("SELECT DISTINCT id, teamID FROM users").
		All(&users)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get registered users: %w", err)
	}

	var latestResults []types.DatabaseResults
	for _, user := range users {
		result, err := getTeamGameweekResult(user.TeamID, gameweek, user.UserID)
		if err != nil {
			fmt.Println(err)
			return fmt.Errorf("failed to get gameweek result: %w", err)
		}
		// Append result to an array of latest results
		latestResults = append(latestResults, result)
	}

	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("results")
		if err != nil {
			fmt.Println(err)
			return fmt.Errorf("error finding collection: %w", err)
		}

		for _, latestResult := range latestResults {
			mapKey := fmt.Sprintf("%d_%s", latestResult.Gameweek, latestResult.UserID)
			existingResult, exists := resultsMap[mapKey]

			if !exists {
				// New result - insert
				record := models.NewRecord(collection)
				record.Set("gameweek", latestResult.Gameweek)
				record.Set("userID", latestResult.UserID)
				record.Set("teamID", latestResult.TeamID)
				record.Set("points", latestResult.Points)
				record.Set("transfers", latestResult.Transfers)
				record.Set("hits", latestResult.Hits/4)
				record.Set("benchPoints", latestResult.BenchPoints)
				record.Set("activeChip", latestResult.ActiveChip)
				record.Set("pos_1", latestResult.Pos1)
				record.Set("pos_2", latestResult.Pos2)
				record.Set("pos_3", latestResult.Pos3)
				record.Set("pos_4", latestResult.Pos4)
				record.Set("pos_5", latestResult.Pos5)
				record.Set("pos_6", latestResult.Pos6)
				record.Set("pos_7", latestResult.Pos7)
				record.Set("pos_8", latestResult.Pos8)
				record.Set("pos_9", latestResult.Pos9)
				record.Set("pos_10", latestResult.Pos10)
				record.Set("pos_11", latestResult.Pos11)
				record.Set("pos_12", latestResult.Pos12)
				record.Set("pos_13", latestResult.Pos13)
				record.Set("pos_14", latestResult.Pos14)
				record.Set("pos_15", latestResult.Pos15)

				if err := txDao.SaveRecord(record); err != nil {
					fmt.Println(err)
					return fmt.Errorf("error saving new result: %w", err)
				}
				log.Printf("[ResultsUpdate] Added new result for user %s gameweek %d",
					latestResult.UserID, latestResult.Gameweek)
				continue
			}

			// Check if result needs updating by comparing structs
			if existingResult != latestResult {
				record, err := txDao.FindFirstRecordByFilter("results", "gameweek = '{:gameweek}' && userID = {:userID}", dbx.Params{"gameweek": gameweek, "userID": latestResult.UserID})
				if err != nil {
					fmt.Println(err)
					return fmt.Errorf("error finding existing result: %w", err)
				}

				record.Set("gameweek", latestResult.Gameweek)
				record.Set("userID", latestResult.UserID)
				record.Set("teamID", latestResult.TeamID)
				record.Set("points", latestResult.Points)
				record.Set("transfers", latestResult.Transfers)
				record.Set("hits", latestResult.Hits/4)
				record.Set("benchPoints", latestResult.BenchPoints)
				record.Set("activeChip", latestResult.ActiveChip)
				record.Set("pos_1", latestResult.Pos1)
				record.Set("pos_2", latestResult.Pos2)
				record.Set("pos_3", latestResult.Pos3)
				record.Set("pos_4", latestResult.Pos4)
				record.Set("pos_5", latestResult.Pos5)
				record.Set("pos_6", latestResult.Pos6)
				record.Set("pos_7", latestResult.Pos7)
				record.Set("pos_8", latestResult.Pos8)
				record.Set("pos_9", latestResult.Pos9)
				record.Set("pos_10", latestResult.Pos10)
				record.Set("pos_11", latestResult.Pos11)
				record.Set("pos_12", latestResult.Pos12)
				record.Set("pos_13", latestResult.Pos13)
				record.Set("pos_14", latestResult.Pos14)
				record.Set("pos_15", latestResult.Pos15)

				if err := txDao.SaveRecord(record); err != nil {
					fmt.Println(err)
					return fmt.Errorf("error updating result: %w", err)
				}
				log.Printf("[ResultsUpdate] Updated result for user %s gameweek %d",
					latestResult.UserID, latestResult.Gameweek)
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[ResultsUpdate] Results update completed")
	return nil
}

func getTeamGameweekResult(teamID, gameweek int, userID string) (types.DatabaseResults, error) {

	baseEndpoint := "https://fantasy.premierleague.com/api/entry/%d/event/%d/picks/"

	// Fetch latest results from API
	endpoint := fmt.Sprintf(baseEndpoint, teamID, gameweek)
	resp, err := http.Get(endpoint)
	if err != nil {
		return types.DatabaseResults{}, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch events: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.DatabaseResults{}, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var result types.GameweekHistory
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Print(err)
		return types.DatabaseResults{}, echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse team data: %v", err))
	}

	return flattenAPIResults(result, teamID, userID), nil
}

func flattenAPIResults(results types.GameweekHistory, teamID int, userID string) types.DatabaseResults {
	return types.DatabaseResults{
		Gameweek:    results.GameweekHistrory.Gameweek,
		UserID:      userID,
		TeamID:      teamID,
		Points:      results.GameweekHistrory.Points,
		Transfers:   results.GameweekHistrory.Transfers,
		Hits:        results.GameweekHistrory.TransferCost,
		BenchPoints: results.GameweekHistrory.BenchPoints,
		ActiveChip:  results.ActiveChip,
		Pos1:        results.Players[0].PlayerID,
		Pos2:        results.Players[1].PlayerID,
		Pos3:        results.Players[2].PlayerID,
		Pos4:        results.Players[3].PlayerID,
		Pos5:        results.Players[4].PlayerID,
		Pos6:        results.Players[5].PlayerID,
		Pos7:        results.Players[6].PlayerID,
		Pos8:        results.Players[7].PlayerID,
		Pos9:        results.Players[8].PlayerID,
		Pos10:       results.Players[9].PlayerID,
		Pos11:       results.Players[10].PlayerID,
		Pos12:       results.Players[11].PlayerID,
		Pos13:       results.Players[12].PlayerID,
		Pos14:       results.Players[13].PlayerID,
		Pos15:       results.Players[14].PlayerID,
	}
}

func updateCards(pb *pocketbase.PocketBase) error {
	log.Println("[CardsUpdate] Starting card update")

	// Fetch all required data first
	cardsMap, err := fetchExistingCards(pb)
	if err != nil {
		log.Printf("[CardsUpdate] Error fetching existing cards: %v", err)
		return err
	}
	log.Printf("[CardsUpdate] Fetched %d existing card records", len(cardsMap))

	leagueMap, err := fetchUserLeagues(pb)
	if err != nil {
		log.Printf("[CardsUpdate] Error fetching user leagues: %v", err)
		return err
	}
	log.Printf("[CardsUpdate] Fetched league data for %d users", len(leagueMap))

	resultsMap, err := fetchResults(pb)
	if err != nil {
		log.Printf("[CardsUpdate] Error fetching results: %v", err)
		return err
	}
	log.Printf("[CardsUpdate] Fetched results for %d users", len(resultsMap))

	eventsMap, err := fetchEvents(pb)
	if err != nil {
		log.Printf("[CardsUpdate] Error fetching events: %v", err)
		return err
	}
	log.Printf("[CardsUpdate] Fetched events for %d players", len(eventsMap))

	// Setup concurrent processing
	workerCount := 5 // Adjust based on your needs
	userIDs := make([]string, 0, len(resultsMap))
	for userID := range resultsMap {
		userIDs = append(userIDs, userID)
	}
	log.Printf("[CardsUpdate] Processing %d users with %d workers", len(userIDs), workerCount)

	// Create channels
	jobs := make(chan string, len(userIDs))
	results := make(chan error, len(userIDs))

	// Start worker pool
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		log.Printf("[CardsUpdate] Starting worker %d", w+1)
		go worker(pb, jobs, results, &wg, cardsMap, leagueMap, resultsMap, eventsMap)
	}

	// Send jobs
	log.Println("[CardsUpdate] Sending jobs to workers")
	for _, userID := range userIDs {
		jobs <- userID
	}
	close(jobs)
	log.Println("[CardsUpdate] All jobs sent to workers")

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		log.Println("[CardsUpdate] All workers completed")
		close(results)
	}()

	// Collect results
	errorCount := 0
	for err := range results {
		if err != nil {
			errorCount++
			log.Printf("[CardsUpdate] Worker error: %v", err)
		}
	}

	if errorCount > 0 {
		return fmt.Errorf("completed with %d errors", errorCount)
	}

	log.Printf("[CardsUpdate] Card update completed successfully for %d users", len(userIDs))
	return nil
}

func worker(
	pb *pocketbase.PocketBase,
	jobs <-chan string,
	results chan<- error,
	wg *sync.WaitGroup,
	cardsMap map[string][]types.DatabaseCard,
	leagueMap map[string][]int,
	resultsMap map[string][]types.DatabaseResults,
	eventsMap map[int][]types.DatabaseEvent,
) {
	defer wg.Done()

	for userID := range jobs {
		err := processUser(pb, userID, cardsMap, leagueMap, resultsMap, eventsMap)
		results <- err
	}
}

func processUser(
	pb *pocketbase.PocketBase,
	userID string,
	cardsMap map[string][]types.DatabaseCard,
	leagueMap map[string][]int,
	resultsMap map[string][]types.DatabaseResults,
	eventsMap map[int][]types.DatabaseEvent,
) error {
	userLeagues := leagueMap[userID]
	if len(userLeagues) == 0 {
		return nil
	}

	return pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("cards")
		if err != nil {
			return fmt.Errorf("error finding collection: %w", err)
		}

		results := resultsMap[userID]
		for _, result := range results {
			if err := processResult(txDao, collection, result, userID, userLeagues, cardsMap, eventsMap); err != nil {
				return err
			}
		}
		return nil
	})
}

func processResult(
	txDao *daos.Dao,
	collection *models.Collection,
	result types.DatabaseResults,
	userID string,
	userLeagues []int,
	cardsMap map[string][]types.DatabaseCard,
	eventsMap map[int][]types.DatabaseEvent,
) error {
	playerIDs := []int{
		result.Pos1, result.Pos2, result.Pos3, result.Pos4, result.Pos5,
		result.Pos6, result.Pos7, result.Pos8, result.Pos9, result.Pos10, result.Pos11,
		result.Pos12, result.Pos13, result.Pos14, result.Pos15,
	}

	playerPositions := make(map[int]int)
	for pos, playerID := range playerIDs {
		playerPositions[playerID] = pos + 1
	}

	for _, playerID := range playerIDs {
		position := playerPositions[playerID]
		if position <= 11 {
			if err := processPlayerEvents(txDao, collection, playerID, position, result, userID, userLeagues, cardsMap, eventsMap); err != nil {
				return err
			}
		}
	}
	return nil
}

func fetchExistingCards(pb *pocketbase.PocketBase) (map[string][]types.DatabaseCard, error) {
	cardsMap := make(map[string][]types.DatabaseCard)

	records, err := pb.Dao().FindRecordsByExpr("cards")
	if err != nil {
		return nil, fmt.Errorf("error fetching cards: %w", err)
	}

	for _, record := range records {
		card := types.DatabaseCard{
			TeamID:          record.GetInt("teamID"),
			UserID:          record.GetString("userID"),
			NominatorTeamID: record.GetInt("nominatorTeamID"),
			NominatorUserID: record.GetString("nominatorUserID"),
			Gameweek:        record.GetInt("gameweek"),
			IsCompleted:     record.GetBool("isCompleted"),
			AdminVerified:   record.GetBool("adminVerified"),
			Type:            record.GetString("type"),
			LeagueID:        record.GetInt("leagueID"),
			CardHash:        record.GetString("cardHash"),
		}

		userID := card.UserID
		cardsMap[userID] = append(cardsMap[userID], card)
	}

	return cardsMap, nil
}

func fetchUserLeagues(pb *pocketbase.PocketBase) (map[string][]int, error) {
	leagueMap := make(map[string][]int)

	records, err := pb.Dao().FindRecordsByExpr("leagues",
		dbx.NewExp("isLinked = {:isLinked}", dbx.Params{"isLinked": true}),
	)
	if err != nil {
		return nil, fmt.Errorf("error fetching user leagues: %w", err)
	}

	for _, record := range records {
		userID := record.GetString("userID")
		leagueID := record.GetInt("leagueID")

		// Append league to user's leagues
		leagueMap[userID] = append(leagueMap[userID], leagueID)
	}

	return leagueMap, nil
}

func fetchResults(pb *pocketbase.PocketBase) (map[string][]types.DatabaseResults, error) {
	resultsMap := make(map[string][]types.DatabaseResults)

	records, err := pb.Dao().FindRecordsByExpr("results")
	if err != nil {
		return nil, fmt.Errorf("error fetching results: %w", err)
	}

	for _, record := range records {
		result := types.DatabaseResults{
			TeamID:   record.GetInt("teamID"),
			UserID:   record.GetString("userID"),
			Gameweek: record.GetInt("gameweek"),
			Pos1:     record.GetInt("pos_1"),
			Pos2:     record.GetInt("pos_2"),
			Pos3:     record.GetInt("pos_3"),
			Pos4:     record.GetInt("pos_4"),
			Pos5:     record.GetInt("pos_5"),
			Pos6:     record.GetInt("pos_6"),
			Pos7:     record.GetInt("pos_7"),
			Pos8:     record.GetInt("pos_8"),
			Pos9:     record.GetInt("pos_9"),
			Pos10:    record.GetInt("pos_10"),
			Pos11:    record.GetInt("pos_11"),
			Pos12:    record.GetInt("pos_12"),
			Pos13:    record.GetInt("pos_13"),
			Pos14:    record.GetInt("pos_14"),
			Pos15:    record.GetInt("pos_15"),
		}
		resultsMap[result.UserID] = append(resultsMap[result.UserID], result)
	}

	return resultsMap, nil
}

func fetchEvents(pb *pocketbase.PocketBase) (map[int][]types.DatabaseEvent, error) {
	eventsMap := make(map[int][]types.DatabaseEvent)

	records, err := pb.Dao().FindRecordsByExpr("events")
	if err != nil {
		return nil, fmt.Errorf("error fetching events: %w", err)
	}

	for _, record := range records {
		event := types.DatabaseEvent{
			PlayerID:   record.GetInt("playerID"),
			Gameweek:   record.GetInt("gameweek"),
			EventType:  record.GetString("eventType"),
			EventValue: record.GetInt("eventValue"),
		}
		eventsMap[event.PlayerID] = append(eventsMap[event.PlayerID], event)
	}

	return eventsMap, nil
}

func processPlayerEvents(
	txDao *daos.Dao,
	collection *models.Collection,
	playerID int,
	position int,
	result types.DatabaseResults,
	userID string,
	userLeagues []int,
	cardsMap map[string][]types.DatabaseCard,
	eventsMap map[int][]types.DatabaseEvent,
) error {
	playerEvents := eventsMap[playerID]
	log.Printf("[DEBUG] Processing events for player %d, found %d events", playerID, len(playerEvents))

	for _, event := range playerEvents {
		log.Printf("[DEBUG] Checking event: gameweek=%d, type=%s, value=%d (target gameweek=%d)",
			event.Gameweek, event.EventType, event.EventValue, result.Gameweek)

		if event.Gameweek == result.Gameweek {
			log.Printf("[DEBUG] Matched gameweek %d for player %d", event.Gameweek, playerID)

			for cardIndex := 0; cardIndex < event.EventValue; cardIndex++ {
				for _, leagueID := range userLeagues {
					cardHash := fmt.Sprintf("%s_%d_%d_%s_%d", userID, leagueID, result.Gameweek, event.EventType, cardIndex)
					log.Printf("[DEBUG] Checking card hash: %s", cardHash)

					existingCards := cardsMap[userID]
					cardExists := false
					for _, existing := range existingCards {
						if existing.CardHash == cardHash {
							cardExists = true
							log.Printf("[DEBUG] Found existing card with hash: %s", cardHash)
							break
						}
					}

					if !cardExists {
						record := models.NewRecord(collection)
						record.Set("teamID", result.TeamID)
						record.Set("userID", userID)
						record.Set("nominatorTeamID", nil)
						record.Set("nominatorUserID", "")
						record.Set("gameweek", result.Gameweek)
						record.Set("isCompleted", false)
						record.Set("adminVerified", false)
						record.Set("type", event.EventType)
						record.Set("leagueID", leagueID)
						record.Set("cardHash", cardHash)

						err := txDao.SaveRecord(record)
						if err != nil {
							log.Printf("[ERROR] Failed to save card: %v", err)
							return fmt.Errorf("error saving card record: %w", err)
						}

						// Add the new card to the cardsMap to prevent duplicates in subsequent processing
						newCard := types.DatabaseCard{
							TeamID:   result.TeamID,
							UserID:   userID,
							Gameweek: result.Gameweek,
							Type:     event.EventType,
							LeagueID: leagueID,
							CardHash: cardHash,
						}
						cardsMap[userID] = append(cardsMap[userID], newCard)

						log.Printf("[SUCCESS] Added new card %d of %d type %s for user %s in league %d gameweek %d position %d",
							cardIndex+1, event.EventValue, event.EventType, userID, leagueID, result.Gameweek, position)
					}
				}
			}
		}
	}
	return nil
}

func updateResultsAggregated(pb *pocketbase.PocketBase) error {
	log.Println("[ResultsAggregating] Starting aggregation pipeline")

	// Build map of users with 2+ cards and their max gameweek
	cardCounts := make(map[string]map[int]int)
	maxGameweeks := make(map[string]int)

	var outstandingCards []types.OutstandingCards
	log.Println("[ResultsAggregating] Fetching outstanding cards...")
	err := pb.Dao().DB().
		NewQuery("SELECT distinct teamID, userID, gameweek, type FROM cards where adminVerified = FALSE").
		All(&outstandingCards)

	if err != nil {
		log.Printf("[ResultsAggregating] Error fetching cards: %v", err)
		return fmt.Errorf("error fetching cards: %w", err)
	}
	log.Printf("[ResultsAggregating] Found %d outstanding cards", len(outstandingCards))

	// Count cards per user per gameweek and track max gameweeks
	for _, card := range outstandingCards {
		if cardCounts[card.UserID] == nil {
			cardCounts[card.UserID] = make(map[int]int)
		}
		cardCounts[card.UserID][card.Gameweek]++

		if card.Gameweek > maxGameweeks[card.UserID] {
			maxGameweeks[card.UserID] = card.Gameweek
		}
	}

	// Build list of users with 2+ cards
	penalizedUsers := make([]string, 0)
	for userID, weeks := range cardCounts {
		totalCards := 0
		for _, count := range weeks {
			totalCards += count
		}
		if totalCards >= 2 {
			penalizedUsers = append(penalizedUsers, userID)
		}
	}

	log.Printf("[ResultsAggregating] Found %d users to penalize", len(penalizedUsers))
	if len(penalizedUsers) == 0 {
		log.Println("[ResultsAggregating] No users to penalize, exiting")
		return nil
	}

	// Convert penalized users to SQL IN clause
	penalizedUsersStr := "'" + strings.Join(penalizedUsers, "','") + "'"
	log.Printf("[ResultsAggregating] Executing aggregation query for users: %s", penalizedUsersStr)

	var aggregatedResults []types.AggregatedResults
	query := fmt.Sprintf(`
    WITH new_results AS (
        SELECT 
            gameweek, 
            teamID, 
            userID,
            CASE 
                WHEN userID IN (%s) AND gameweek = (
                    SELECT MAX(gameweek) 
                    FROM cards 
                    WHERE adminVerified = FALSE 
                    AND userID = results.userID
                ) THEN 0
                ELSE points
            END as points,
            SUM(points - (hits * 4)) OVER (
                PARTITION BY userID 
                ORDER BY gameweek
                ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
            ) as totalPoints
        FROM results 
        GROUP BY gameweek, teamID, userID, points, hits
    )
    SELECT nr.*
    FROM new_results nr
    LEFT JOIN aggregated_results ra 
        ON nr.gameweek = ra.gameweek 
        AND nr.userID = ra.userID
    WHERE ra.id IS NULL
    ORDER BY nr.userID, nr.gameweek
`, penalizedUsersStr)

	err = pb.Dao().DB().NewQuery(query).All(&aggregatedResults)
	if err != nil {
		log.Printf("[ResultsAggregating] Error executing aggregation query: %v", err)
		return fmt.Errorf("error aggregating results: %w", err)
	}

	// Now we can directly save all results since we already filtered out existing ones
	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("aggregated_results")
		if err != nil {
			log.Printf("[ResultsAggregating] Error finding collection: %v", err)
			return fmt.Errorf("error finding collection: %w", err)
		}

		savedCount := 0
		for _, result := range aggregatedResults {
			record := models.NewRecord(collection)
			record.Set("gameweek", result.Gameweek)
			record.Set("teamID", result.TeamID)
			record.Set("userID", result.UserID)
			record.Set("points", result.Points)
			record.Set("totalPoints", result.TotalPoints)

			if err := txDao.SaveRecord(record); err != nil {
				log.Printf("[ResultsAggregating] Error saving record for user %s, gameweek %d: %v",
					result.UserID, result.Gameweek, err)
				return fmt.Errorf("error saving aggregated result: %w", err)
			}
			savedCount++
		}
		log.Printf("[ResultsAggregating] Transaction complete: %d new records saved", savedCount)
		return nil
	})

	if err != nil {
		log.Printf("[ResultsAggregating] Transaction failed: %v", err)
		return fmt.Errorf("transaction failed: %w", err)
	}

	err = verifyExpiredCards(pb)
	if err != nil {
		log.Printf("[ResultsAggregating] Error verifying expired cards: %v", err)
		return fmt.Errorf("error verifying expired cards: %w", err)
	}

	log.Println("[ResultsAggregating] Aggregation pipeline completed successfully")
	return nil
}

func verifyExpiredCards(pb *pocketbase.PocketBase) error {
	log.Println("[ExpiredCardCheck] pipeline starting")

	var aggregatedResults []types.AggregatedResults
	err := pb.Dao().DB().NewQuery("SELECT * FROM aggregated_results where points = 0").All(&aggregatedResults)

	if err != nil {
		log.Printf("[ExpiredCardCheck] Error fetching aggregated results: %v", err)
		return fmt.Errorf("error fetching aggregated results: %w", err)
	}

	log.Printf("[ExpiredCardCheck] Found %d results with 0 points", len(aggregatedResults))
	if len(aggregatedResults) == 0 {
		return nil
	}

	// Build map of userID -> gameweeks for easier lookup
	userGameweeks := make(map[string][]int)
	for _, result := range aggregatedResults {
		userGameweeks[result.UserID] = append(userGameweeks[result.UserID], result.Gameweek)
	}

	// Update cards in transaction
	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		for userID, gameweeks := range userGameweeks {
			// Convert gameweeks to string for IN clause
			gameweekStrs := make([]string, len(gameweeks))
			for i, gw := range gameweeks {
				gameweekStrs[i] = strconv.Itoa(gw)
			}
			// gameweeksStr := strings.Join(gameweekStrs, ",")

			// Execute bulk update
			updateQuery := fmt.Sprintf(`
                UPDATE cards 
                SET adminVerified = TRUE, 
                    isCompleted = TRUE
                WHERE userID = '%s' 
                AND adminVerified = FALSE`,
				userID,
			)

			result, err := txDao.DB().NewQuery(updateQuery).Execute()
			if err != nil {
				log.Printf("[ExpiredCardCheck] Error updating cards for user %s: %v", userID, err)
				return fmt.Errorf("error updating cards: %w", err)
			}

			rowsAffected, _ := result.RowsAffected()
			log.Printf("[ExpiredCardCheck] Updated %d cards for user %s", rowsAffected, userID)
		}
		return nil
	})

	if err != nil {
		log.Printf("[ExpiredCardCheck] Transaction failed: %v", err)
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[ExpiredCardCheck] Pipeline completed successfully")
	return nil
}
