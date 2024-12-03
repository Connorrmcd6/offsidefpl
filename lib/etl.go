package lib

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/cmcd97/bytesize/app/types"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/cron"
)

func DailyDataCheck(e *core.ServeEvent, pb *pocketbase.PocketBase) error {
	log.Println("[DailyDataCheck] Starting daily gameweek completion check")

	// todayMidnight := getTodayMidnight()
	timestamps := []types.DataUpdateDates{}

	err := pb.DB().NewQuery("SELECT max(kickoff) as ts FROM fixtures GROUP BY date(kickoff) ORDER BY gameweek ASC").All(&timestamps)
	if err != nil {
		log.Printf("[DailyDataCheck] Database query failed: %v", err)
		return fmt.Errorf("failed to process fixtures: %w", err)
	}

	fmt.Print(
		"Timestamps: ", timestamps,
	)

	// for _, ts := range timestamps {
	// 	fixtureEndDate, err := roundUpToNextDay(ts.TS)
	// 	if err != nil {
	// 		log.Printf("[DailyDataCheck] Date parsing error: %v", err)
	// 		return fmt.Errorf("failed to parse date: %w", err)
	// 	}

	// 	randomNum := rand.Intn(5) + 1
	// 	log.Printf("[DailyDataCheck] Random check number generated: %d", randomNum)

	// 	if todayMidnight == fixtureEndDate || randomNum == 3 {
	// 		log.Println("[DailyDataCheck] Triggering hourly checks - gameweek completed or test condition met")
	// 		c := cron.New()
	// 		c.MustAdd("Hourly ETL", "*/1 * * * *", func() {
	// 			hourlyDataCheck(e, pb, c)
	// 		})
	// 		c.Start()
	// 		return nil
	// 	} else {
	// 		log.Println("[DailyDataCheck] No gameweek completion detected")
	// 		return nil
	// 	}
	// }
	return nil
}

func hourlyDataCheck(e *core.ServeEvent, pb *pocketbase.PocketBase, c *cron.Cron) error {
	log.Println("[HourlyDataCheck] Starting hourly data availability check")

	randomNum := rand.Intn(5) + 1
	log.Printf("[HourlyDataCheck] Random check number generated: %d", randomNum)

	if randomNum == 4 {
		log.Println("[HourlyDataCheck] Data available - stopping hourly checks")
		c.Remove("Hourly ETL")
		return nil
	}

	log.Println("[HourlyDataCheck] Data not yet available")
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
