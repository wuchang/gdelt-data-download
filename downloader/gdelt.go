// Package downloader handles GDELT ZIP file downloading.
package downloader

import (
	"fmt"
	"time"
)

// tableSuffix maps internal table names to GDELT ZIP file suffixes.
var tableSuffix = map[string]string{
	"export":              "export.CSV.zip",
	"gkg":                 "gkg.csv.zip",
	"mentions":            "mentions.CSV.zip",
	"export-translation":  "translation.export.CSV.zip",
	"gkg-translation":     "translation.gkg.csv.zip",
	"mentions-translation": "translation.mentions.CSV.zip",
}

// defaultTables is the list of tables downloaded when no specific --table is given.
var defaultTables = []string{"export", "gkg", "mentions"}

// ResolveTableList returns the list of table names to download.
func ResolveTableList(tables string, translation bool) []string {
	if tables == "" {
		return ResolveTableList("export,gkg,mentions", translation)
	}

	// Parse comma-separated table list
	var result []string
	start := 0
	for i := 0; i <= len(tables); i++ {
		if i == len(tables) || tables[i] == ',' {
			t := tables[start:i]
			if t != "" {
				result = append(result, t)
			}
			start = i + 1
		}
	}

	if translation {
		// Add translation variants
		original := make([]string, len(result))
		copy(original, result)
		for _, t := range original {
			result = append(result, t+"-translation")
		}
	}

	return result
}

// ValidateTable checks if a table name is known.
func ValidateTable(table string) error {
	if _, ok := tableSuffix[table]; !ok {
		return fmt.Errorf("unknown table: %s (valid: export, gkg, mentions, and -translation variants)", table)
	}
	return nil
}

// Suffix returns the GDELT ZIP suffix for a table name.
func Suffix(table string) string {
	return tableSuffix[table]
}

// TimeSlices generates the 96 (24h × 4) timestamp strings for a given date.
func TimeSlices(dateStr string) []string {
	year := dateStr[:4]
	month := dateStr[4:6]
	day := dateStr[6:8]

	slices := make([]string, 0, 96)
	for hour := 0; hour < 24; hour++ {
		for quarter := 0; quarter < 4; quarter++ {
			minute := quarter * 15
			ts := fmt.Sprintf("%s%s%s%02d%02d00", year, month, day, hour, minute)
			slices = append(slices, ts)
		}
	}
	return slices
}

// URLsFromTimeSlices generates download URLs for each time slice.
func URLsFromTimeSlices(baseURL string, table string, timeSlices []string) []string {
	suffix := Suffix(table)
	urls := make([]string, len(timeSlices))
	for i, ts := range timeSlices {
		urls[i] = fmt.Sprintf("%s/%s.%s", baseURL, ts, suffix)
	}
	return urls
}

// URLForTimestamp generates a single download URL for a specific timestamp.
func URLForTimestamp(baseURL string, table string, ts string) string {
	return fmt.Sprintf("%s/%s.%s", baseURL, ts, Suffix(table))
}

// LatestTimeSlice returns the most recent complete 15-minute time slice.
func LatestTimeSlice() string {
	now := time.Now().UTC()
	// Round down to the nearest 15 minutes
	minute := now.Minute() / 15 * 15
	latest := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), minute, 0, 0, time.UTC)
	return latest.Format("20060102150405")
}

// DateRange generates YYYYMMDD strings from start to end inclusive.
func DateRange(start, end string) ([]string, error) {
	startTime, err := time.Parse("20060102", start)
	if err != nil {
		return nil, fmt.Errorf("parse start date %s: %w", start, err)
	}

	var endTime time.Time
	if end == "" {
		endTime = time.Now().UTC()
	} else {
		endTime, err = time.Parse("20060102", end)
		if err != nil {
			return nil, fmt.Errorf("parse end date %s: %w", end, err)
		}
	}

	if endTime.Before(startTime) {
		return nil, fmt.Errorf("end date %s is before start date %s", end, start)
	}

	var dates []string
	current := startTime
	for !current.After(endTime) {
		dates = append(dates, current.Format("20060102"))
		current = current.AddDate(0, 0, 1)
	}
	return dates, nil
}

// BuildLocalPath constructs the output file path.
// When flat=false: {dir}/{table}/year=YYYY/month=MM/day=DD/{ts}.{suffix}
// When flat=true:  {dir}/{table}/{ts}.{suffix}
func BuildLocalPath(dir, table, dateStr, filename string, flat bool) string {
	if flat {
		return fmt.Sprintf("%s/%s/%s", dir, table, filename)
	}
	year := dateStr[:4]
	month := dateStr[4:6]
	day := dateStr[6:8]
	return fmt.Sprintf("%s/%s/year=%s/month=%s/day=%s/%s", dir, table, year, month, day, filename)
}
