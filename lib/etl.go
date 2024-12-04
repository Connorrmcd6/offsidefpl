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

		// update cards
		// updated results aggregated
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

func UpdateCards(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[CardsUpdate] Starting card update")

	// Fetch existing players from DB
	var existingCards []types.DatabaseCard
	err := pb.Dao().DB().
		NewQuery("SELECT * FROM cards").
		All(&existingCards)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get existing cards: %w", err)
	}

	// Create map of existing players for easy lookup
	cardsMap := make(map[string][]types.DatabaseCard)
	for _, c := range existingCards {
		cardsMap[c.UserID] = append(cardsMap[c.UserID], c)
	}

	// log.Print(cardsMap)

	// Fetch user leagues from DB
	var userLeagues []types.DatabaseLeague
	err = pb.Dao().DB().
		NewQuery("SELECT leagueID, userID FROM leagues where isLinked = TRUE").
		All(&userLeagues)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get user leagues: %w", err)
	}
	// Create map of user leagues for easy lookup
	leagueMap := make(map[string][]int)
	for _, l := range userLeagues {
		leagueMap[l.UserID] = append(leagueMap[l.UserID], l.LeagueID)
	}
	// log.Print(leagueMap)

	// Fetch existing players from DB
	var currentResults []types.DatabaseResults
	err = pb.Dao().DB().
		NewQuery("SELECT * FROM results").
		All(&currentResults)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get existing results %w", err)
	}

	// Create map of existing players for easy lookup
	resultsMap := make(map[string][]types.DatabaseResults)
	for _, c := range currentResults {
		resultsMap[c.UserID] = append(resultsMap[c.UserID], c)
	}

	// Fetch existing players from DB
	var currentEvents []types.DatabaseEvent
	err = pb.Dao().DB().
		NewQuery("SELECT * FROM events").
		All(&currentEvents)

	if err != nil {
		fmt.Println(err)
		return fmt.Errorf("failed to get existing events%w", err)
	}

	// Create map of existing players for easy lookup
	eventsMap := make(map[int][]types.DatabaseEvent)
	for _, c := range currentEvents {
		eventsMap[c.PlayerID] = append(eventsMap[c.PlayerID], c)
	}

	// log.Print(eventsMap)
	err = pb.Dao().RunInTransaction(func(txDao *daos.Dao) error {
		collection, err := txDao.FindCollectionByNameOrId("cards")
		if err != nil {
			return fmt.Errorf("error finding collection: %w", err)
		}

		// Process each user's results
		for userID, results := range resultsMap {
			userLeagues := leagueMap[userID]

			// Skip if user has no linked leagues
			if len(userLeagues) == 0 {
				continue
			}

			// Process each result
			for _, result := range results {
				// Get all player IDs from result positions
				playerIDs := []int{
					result.Pos1, result.Pos2, result.Pos3, result.Pos4, result.Pos5,
					result.Pos6, result.Pos7, result.Pos8, result.Pos9, result.Pos10, result.Pos11,
				}

				// Check each player for card events
				for _, playerID := range playerIDs {
					playerEvents := eventsMap[playerID]
					for _, event := range playerEvents {
						if event.Gameweek == result.Gameweek {
							// Create cards for each event value (number of cards)
							for cardIndex := 0; cardIndex < event.EventValue; cardIndex++ {
								// Process each league for this card event
								for _, leagueID := range userLeagues {
									// Create unique hash including card index
									cardHash := fmt.Sprintf("%s_%d_%d_%s_%d", userID, leagueID, result.Gameweek, event.EventType, cardIndex)

									// Check if card already exists
									existingCards := cardsMap[userID]
									cardExists := false
									for _, existing := range existingCards {
										if existing.CardHash == cardHash {
											cardExists = true
											break
										}
									}

									if !cardExists {
										// Insert new card record
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
										if err := txDao.SaveRecord(record); err != nil {
											return fmt.Errorf("error saving new card: %w", err)
										}
										log.Printf("[CardsUpdate] Added new card %d of %d type %s for user %s in league %d gameweek %d",
											cardIndex+1, event.EventValue, event.EventType, userID, leagueID, result.Gameweek)
									}
								}
							}
						}
					}
				}
			}

		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	log.Println("[CardsUpdate] Card update completed")

	return nil
}

// func updateResultsAggregated(e *core.ServeEvent, pb *pocketbase.PocketBase) error{}
