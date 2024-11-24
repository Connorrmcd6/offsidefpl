package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cmcd97/bytesize/app/types"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/models"
)

const (
	FPLEndpoint = "https://fantasy.premierleague.com/api/bootstrap-static/"
	timeout     = 30 * time.Second
)

func GetAllPlayers(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Printf("checking players...")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Get current DB state
	maxYear, _, err := getDBState(pb)
	if err != nil {
		return fmt.Errorf("failed to get DB state: %w", err)
	}

	// Fetch FPL data
	fplData, err := fetchFPLData(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch FPL data: %w", err)
	}

	// Process players if needed
	if err := processPlayers(pb, fplData, maxYear); err != nil {
		return fmt.Errorf("failed to process players: %w", err)
	}

	return nil
}

func getDBState(pb *pocketbase.PocketBase) (*int, int, error) {
	log.Printf("checking db state...")
	type QueryResponse struct {
		Count              int  `db:"count"`
		MaxSeasonStartYear *int `db:"maxSeasonStartYear"`
	}
	var qr QueryResponse

	err := pb.Dao().DB().
		NewQuery("SELECT count(*) as count, max(seasonStartYear) as maxSeasonStartYear FROM players").
		One(&qr)

	return qr.MaxSeasonStartYear, qr.Count, err
}

func fetchFPLData(ctx context.Context) (*types.FPLResponse, error) {
	log.Printf("fetching current players...")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FPLEndpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var fplData types.FPLResponse
	if err := json.Unmarshal(body, &fplData); err != nil {
		return nil, err
	}

	return &fplData, nil
}

func processPlayers(pb *pocketbase.PocketBase, fplData *types.FPLResponse, maxYear *int) error {
	log.Printf("processing players...")
	if len(fplData.Events) == 0 {
		return fmt.Errorf("no events found in FPL data")
	}

	deadlineTime, err := time.Parse(time.RFC3339, fplData.Events[0].DeadlineTime)
	if err != nil {
		return fmt.Errorf("error parsing deadline time: %w", err)
	}

	currentYear := deadlineTime.Year()
	if maxYear != nil && *maxYear >= currentYear {
		return nil // Already up to date
	}

	collection, err := pb.Dao().FindCollectionByNameOrId("players")
	if err != nil {
		return fmt.Errorf("error finding collection: %w", err)
	}

	const (
		batchSize  = 100
		numWorkers = 4
	)

	// Create channels
	recordsChan := make(chan []*models.Record, len(fplData.Elements)/batchSize+1)
	errorsChan := make(chan error, numWorkers)
	var wg sync.WaitGroup

	// Start worker pool
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for records := range recordsChan {
				if err := saveRecords(pb, records); err != nil {
					errorsChan <- err
					return
				}
			}
		}()
	}

	// Process players in batches
	records := make([]*models.Record, 0, batchSize)
	for _, player := range fplData.Elements {
		record := models.NewRecord(collection)
		record.Set("playerID", player.ID)
		record.Set("playerTeamID", player.Team)
		record.Set("playerName", player.WebName)
		record.Set("seasonStartYear", currentYear)

		records = append(records, record)

		if len(records) >= batchSize {
			recordsChan <- records
			records = make([]*models.Record, 0, batchSize)
		}
	}

	// Send remaining records
	if len(records) > 0 {
		recordsChan <- records
	}

	// Close channel and wait for workers
	close(recordsChan)
	wg.Wait()
	close(errorsChan)

	// Check for errors
	for err := range errorsChan {
		if err != nil {
			return fmt.Errorf("error in worker: %w", err)
		}
	}

	return nil
}

func saveRecords(pb *pocketbase.PocketBase, records []*models.Record) error {
	log.Printf("saving players updates...")
	var wg sync.WaitGroup
	errorsChan := make(chan error, len(records))

	for _, record := range records {
		wg.Add(1)
		go func(r *models.Record) {
			defer wg.Done()
			if err := pb.Dao().SaveRecord(r); err != nil {
				errorsChan <- fmt.Errorf("error saving record: %w", err)
			}
		}(record)
	}

	wg.Wait()
	close(errorsChan)

	// Return first error if any
	for err := range errorsChan {
		if err != nil {
			return err
		}
	}

	return nil
}

func GetAllFixtureEvents(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Printf("checking db state...")
	type QueryResponse struct {
		Count int `db:"count"`
	}
	var qr QueryResponse

	err := pb.Dao().DB().
		NewQuery("SELECT count(*) as count FROM events").
		One(&qr)

	if err != nil {
		return fmt.Errorf("failed to get DB state: %w", err)
	}

	if qr.Count > 0 {
		fmt.Println("Events already exist in the database")
		return nil
	}

	// get all events where finished is true
	endpoint := "https://fantasy.premierleague.com/api/fixtures/"

	resp, err := http.Get(endpoint)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to fetch team: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to read response: %v", err))
	}

	var Fixtures []types.FixtureStats
	if err := json.Unmarshal(body, &Fixtures); err != nil {
		fmt.Print(err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("failed to parse team data: %v", err))
	}

	collection, err := pb.Dao().FindCollectionByNameOrId("events")
	if err != nil {
		return fmt.Errorf("error finding collection: %w", err)
	}

	// Process fixtures

	for _, fixture := range Fixtures {
		if !fixture.Finished {
			continue
		}

		for _, stat := range fixture.Stats {
			// Process home team stats
			for _, home := range stat.Home {
				record := models.NewRecord(collection)
				record.Set("fixtureID", fixture.FixtureID)
				record.Set("gameweek", fixture.Gameweek)
				record.Set("playerID", home.Element)
				record.Set("eventType", string(stat.Identifier))
				record.Set("eventValue", home.Value)

				if err := pb.Dao().SaveRecord(record); err != nil {
					return fmt.Errorf("error saving home record: %w", err)
				}
			}

			// Process away team stats
			for _, away := range stat.Away {
				record := models.NewRecord(collection)
				record.Set("fixtureID", fixture.FixtureID)
				record.Set("gameweek", fixture.Gameweek)
				record.Set("playerID", away.Element)
				record.Set("eventType", string(stat.Identifier))
				record.Set("eventValue", away.Value)

				if err := pb.Dao().SaveRecord(record); err != nil {
					return fmt.Errorf("error saving away record: %w", err)
				}
			}
		}
	}

	return nil

}
