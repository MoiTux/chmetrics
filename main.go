package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	sheets "google.golang.org/api/sheets/v4"
)

// export GOOGLE_APPLICATION_CREDENTIALS=/path/to/credntiel

func main() {
	ctx := context.Background()

	var petitionName string
	var spreadsheetID string

	var currentValue string

	var hourlySheetName string
	var hourlySummaryRange string
	var sevenlySummaryRange string
	var hourlySheetID int64
	var hourlyChartID int64

	var dailySheetName string
	var dailySummaryRange string
	var weeklySummaryRange string
	var dailySheetID int64
	var dailyChartID int64

	flag.StringVar(&petitionName, "petition-name", "", "name of the petition to get metrics from")

	flag.StringVar(&spreadsheetID, "spreadsheet-id", "", "id of the spreadsheet to update")

	flag.StringVar(&currentValue, "current-value", "", "cell for the current value")

	flag.StringVar(&hourlySheetName, "hourly-sheet-name", "", "name of the sheet for hourly update")
	flag.StringVar(&hourlySummaryRange, "hourly-summary-range", "", "range for the hourly summary")
	flag.StringVar(&sevenlySummaryRange, "sevenly-summary-range", "", "range for the sevenly summary")
	flag.Int64Var(&hourlySheetID, "hourly-sheet-id", 0, "id of the sheet for hourly update")
	flag.Int64Var(&hourlyChartID, "hourly-chart-id", 0, "id of the chart for hourly update")

	flag.StringVar(&dailySheetName, "daily-sheet-name", "", "name of the sheet for daily update")
	flag.StringVar(&dailySummaryRange, "daily-summary-range", "", "range for the daily summary")
	flag.StringVar(&weeklySummaryRange, "weekly-summary-range", "", "range for the weekly summary")
	flag.Int64Var(&dailySheetID, "daily-sheet-id", 0, "id of the sheet for daily update")
	flag.Int64Var(&dailyChartID, "daily-chart-id", 0, "id of the chart for daily update")

	flag.Parse()
	if petitionName == "" || spreadsheetID == "" || currentValue == "" ||
		hourlySheetName == "" || hourlySummaryRange == "" || sevenlySummaryRange == "" ||
		hourlySheetID == 0 || hourlyChartID == 0 ||
		dailySheetName == "" || dailySummaryRange == "" || weeklySummaryRange == "" ||
		dailySheetID == 0 || dailyChartID == 0 {
		flag.PrintDefaults()
		return
	}

	sheetsService, err := sheets.NewService(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating a new sheet services", err)
		return
	}

	// err = GetSheet(ctx, sheetsService, spreadsheetID)
	// if err != nil {
	// 	fmt.Fprintln(os.Stderr, "getting sheet", err)
	// 	return
	// }
	// return

	signature, goal, err := GetMetrics(ctx, petitionName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting metrics", err)
		return
	}

	valueRange := []*sheets.ValueRange{
		{
			Range:          currentValue,
			MajorDimension: "COLUMNS",
			Values: [][]any{
				{
					signature,
					goal,
				},
			},
		},
	}

	hourRow, vr := computeHourlyRanges(hourlySheetName, signature, goal)
	valueRange = append(valueRange, vr)

	vr, err = rollingSummary(hourlySummaryRange, hourlySheetName, hourRow, 6)
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting hourly rolling summary", err)
		return
	}
	valueRange = append(valueRange, vr)

	vr, err = rollingSummary(sevenlySummaryRange, hourlySheetName, hourRow, 7*24)
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting weekly rolling summary", err)
		return
	}
	valueRange = append(valueRange, vr)

	dayRow, vrs, err := computeDailyRanges(dailySheetName, dailySummaryRange, weeklySummaryRange, signature)
	if err != nil {
		fmt.Fprintln(os.Stderr, "computing daily ranges", err)
		return
	}
	valueRange = append(valueRange, vrs...)

	err = updateData(ctx, sheetsService, spreadsheetID, valueRange)
	if err != nil {
		fmt.Fprintln(os.Stderr, "updating data", err)
		return
	}

	// Add the new value to the chart, row is excluded but was computed on a one-based indexed,
	// here we are on a zero-based one, so it's all good
	err = UpdateHouryChart(ctx, sheetsService, spreadsheetID, hourlyChartID, hourlySheetID, hourRow)
	if err != nil {
		fmt.Fprintln(os.Stderr, "updating sheet chart", err)
		return
	}

	if time.Now().Hour() != 0 {
		// not midnight we can stop here
		return
	}

	// Add current day to the chart, row is excluded but was computed on a one-based indexed,
	// here we are on a zero-based one, so it's all good
	err = UpdateDailyChart(ctx, sheetsService, spreadsheetID, dailyChartID, dailySheetID, dayRow)
	if err != nil {
		fmt.Fprintln(os.Stderr, "updating daily chart", err)
		return
	}
}

// GetMetrics get the later signatures count and signature Goal from change.org for the petitionName
func GetMetrics(ctx context.Context, petitionName string) (int64, int64, error) {
	type payload []struct {
		OperationName string `json:"operationName"`
		Variables     struct {
			PetitionName string `json:"petitionSlugOrId"`
		} `json:"variables"`
		Query string `json:"query"`
	}
	data := payload{
		{
			OperationName: "PetitionDetailsPageStats",
			Query:         "query PetitionDetailsPageStats($petitionSlugOrId: String!) { petitionStats: petitionBySlugOrId(slugOrId: $petitionSlugOrId) {signatureState {signatureCount { displayed } signatureGoal { displayed } } }}",
		},
	}
	data[0].Variables.PetitionName = petitionName

	raw, err := json.Marshal(data)
	if err != nil {
		return 0, 0, fmt.Errorf("marshaling payload: %w", err)
	}

	url := "https://www.change.org/api-proxy/graphql?op=PetitionDetailsPageStats"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, 0, fmt.Errorf("creating new request: %w", err)
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-requested-with", "http-link")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("calling API: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("reading body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected status code: %d, %s", resp.StatusCode, string(body))
	}

	var res []struct {
		Data struct {
			Petition struct {
				Signature struct {
					Count struct {
						Value int64 `json:"displayed"`
					} `json:"signatureCount"`
					Goal struct {
						Value int64 `json:"displayed"`
					} `json:"signatureGoal"`
				} `json:"signatureState"`
			} `json:"petitionStats"`
		} `json:"data"`
	}
	err = json.Unmarshal(body, &res)
	if err != nil {
		return 0, 0, fmt.Errorf("unmarshaling body: %s : %w", string(body), err)
	}
	if len(res) != 1 {
		return 0, 0, fmt.Errorf("unexpected result length: %s", string(body))
	}
	return res[0].Data.Petition.Signature.Count.Value, res[0].Data.Petition.Signature.Goal.Value, nil
}

func updateData(ctx context.Context, sheetsService *sheets.Service, spreadsheetID string, valueRange []*sheets.ValueRange) error {
	updateCall := sheets.NewSpreadsheetsValuesService(sheetsService).BatchUpdate(
		spreadsheetID,
		&sheets.BatchUpdateValuesRequest{
			ValueInputOption: "USER_ENTERED",
			Data:             valueRange,
		},
	)

	resp, err := updateCall.Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("calling API: %w", err)
	}
	if resp.HTTPStatusCode != http.StatusOK {
		// should not happen API is supposed to always return 200
		return fmt.Errorf("unexpected return status code: %d", resp.HTTPStatusCode)
	}
	return nil
}

// computeHourlyRanges computes the needed range to update the data about the hourly chart
func computeHourlyRanges(hourlySheetName string, signature, goal int64) (int64, *sheets.ValueRange) {
	start := time.Date(2024, 4, 15, 3, 0, 0, 0, time.Local) // row 18
	row := 18 + int64(time.Now().Sub(start).Hours())

	return row, &sheets.ValueRange{
		Range:          fmt.Sprintf("%s!A%d:C%d", hourlySheetName, row, row),
		MajorDimension: "ROWS",
		Values: [][]any{
			{
				time.Now().Format("02-01-2006 15:04:05"),
				signature,
				goal,
			},
		},
	}
}

// rollingSummary generates a summary for the rolling period.
// the period is define by hourlySummaryRange and step (in hours).
func rollingSummary(summaryRange, sheetName string, latestRow int64, step int) (*sheets.ValueRange, error) {
	first, last, err := parseRange(summaryRange)
	if err != nil {
		return nil, fmt.Errorf("parsing range: %w", err)
	}

	// Sheet!B(row) - Sheet!B(row-hours)
	current := fmt.Sprintf("'%s'!B%d-'%s'!B%d", sheetName, latestRow, sheetName, latestRow-int64(step))
	steps := last - first + 1
	data := make([][]any, 0, steps)
	var signatures bool
	for range steps {
		step += step
		// Sheet!B(row) - Sheet!B(row-hours)
		next := fmt.Sprintf("'%s'!B%d-'%s'!B%d", sheetName, latestRow, sheetName, latestRow-int64(step))

		values := []any{
			fmt.Sprintf("=%s", current),
			fmt.Sprintf("=2*(%s)-(%s)", current, next), // trend -> 2 * (current range) - (next range)
		}

		if latestRow-int64(step) < 0 {
			if signatures {
				values[0] = "-"
			}
			values[1] = "-"
			signatures = true
		}

		data = append(data, values)
		current = next
	}

	return &sheets.ValueRange{
		Range:          summaryRange,
		MajorDimension: "ROWS",
		Values:         data,
	}, nil
}

// computeDailyRanges computes the needed range to update the data about the daily chart and summary
func computeDailyRanges(dailySheetName, dailySummaryRange, weeklySummaryRange string, signature int64) (int64, []*sheets.ValueRange, error) {
	// Make sure we compute the number of day based UTC to avoid issues with daylight saving time.
	// As the compute is done at midnight in the local time zone (CET/CEST) we need to manually create the right date,
	// otherwise it can be converted to the previous day (using '.UTC()', or '.Truncate(24*time.Hour)'),
	// or having a day less than 24 hours, which might also result is missing one day.
	start := time.Date(2024, 3, 28, 0, 0, 0, 0, time.UTC)
	now := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC)

	// row are one-based indexed (usually the header), thus the first day (nbDay == 0) is at row == 2
	row := int64(now.Sub(start).Hours()/24) + 2

	valueRange := []*sheets.ValueRange{
		// compute current day
		{
			Range:          fmt.Sprintf("%s!B%d", dailySheetName, row+1),
			MajorDimension: "ROWS",
			Values: [][]any{
				{
					signature,
				},
			},
		},
	}

	if time.Now().Hour() != 0 {
		return row, valueRange, nil
	}

	// daily summary
	first, last, err := parseRange(dailySummaryRange)
	if err != nil {
		return 0, nil, fmt.Errorf("parsing daily range: %w", err)
	}

	steps := last - first + 1
	data := make([][]any, 0, steps)
	for step := range steps {
		data = append(data, []any{
			fmt.Sprintf("='%s'!C%d", dailySheetName, row-step),
			fmt.Sprintf("='%s'!D%d", dailySheetName, row-step),
		})
	}
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          dailySummaryRange,
		MajorDimension: "ROWS",
		Values:         data,
	})

	// weekly summary
	weekDay := int64(time.Now().Weekday()) - 1
	if weekDay < 0 {
		// it's Sunday
		weekDay = 6
	}

	current := fmt.Sprintf("SUM('%s'!C%d:C%d)", dailySheetName, row, row-weekDay)

	previousWeek := row - weekDay - 1 // end of the previous week
	next := fmt.Sprintf("SUM('%s'!C%d:C%d)", dailySheetName, previousWeek, previousWeek-6)

	first, last, err = parseRange(weeklySummaryRange)
	if err != nil {
		return 0, nil, fmt.Errorf("parsing range: %w", err)
	}

	steps = last - first + 1
	data = make([][]any, 0, steps)
	for range steps {
		data = append(data, []any{
			fmt.Sprintf("=%s", current),
			fmt.Sprintf("=%s-%s", current, next),
		})

		previousWeek -= 7
		current = next
		next = fmt.Sprintf("SUM('%s'!C%d:C%d)", dailySheetName, previousWeek, previousWeek-6)
	}
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          weeklySummaryRange,
		MajorDimension: "ROWS",
		Values:         data,
	})

	// add new day
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          fmt.Sprintf("%s!A%d:D%d", dailySheetName, row, row),
		MajorDimension: "ROWS",
		Values: [][]any{
			{
				time.Now().Format("02-01-2006"),
				signature,
				fmt.Sprintf("=B%d-B%d", row+1, row), // compute the difference with the new next day (which will be computed throughout the day)
				fmt.Sprintf("=C%d-C%d", row, row-1), // compute trend
			},
		},
	})
	return row, valueRange, nil
}

var regexpRange = regexp.MustCompile("^(.*)!([a-zA-Z]+)([0-9]+):([a-zA-Z]+)([0-9]+)$")

// parseRange parse a range like: Chart!F28:G31
// it returns:
// - the first row: 28
// - the last row: 31
func parseRange(range_ string) (int64, int64, error) {
	parsed := regexpRange.FindStringSubmatch(range_)
	if len(parsed) != 6 {
		return 0, 0, fmt.Errorf("invalid range: %s", range_)
	}

	first_row, err := strconv.ParseInt(parsed[3], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parsing str to int: %w", err)
	}

	last_row, err := strconv.ParseInt(parsed[5], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parsing str to int: %w", err)
	}

	return first_row, last_row, nil
}

// UpdateHouryChart update the ChartID in the spreadsheetID with data from sheetID
// The value of row is excluded for the range.
func UpdateHouryChart(_ context.Context, sheetsService *sheets.Service, spreadsheetID string, chartID, sheetID, row int64) error {
	resp, err := sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				UpdateChartSpec: &sheets.UpdateChartSpecRequest{
					ChartId: chartID,
					Spec: &sheets.ChartSpec{
						Title: "Données par heure",
						TitleTextPosition: &sheets.TextPosition{
							HorizontalAlignment: "CENTER",
						},
						BasicChart: &sheets.BasicChartSpec{
							ChartType: "LINE",
							Domains: []*sheets.BasicChartDomain{
								{
									Domain: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 0, 0, 1, row), // A0:A{ROW-1}
									},
								},
							},
							HeaderCount: 1,
							Series: []*sheets.BasicChartSeries{
								{
									Series: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 1, 0, 2, row), // B0:B{ROW-1}
									},
									TargetAxis: "LEFT_AXIS",
								},
								{
									Series: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 2, 0, 3, row), // C0:C{ROW-1}
									},
									TargetAxis: "LEFT_AXIS",
								},
							},
						},
					},
				},
			},
		},
	}).Do()
	if err != nil {
		return fmt.Errorf("calling API: %w", err)
	}
	if resp.HTTPStatusCode != http.StatusOK {
		// should not happen API is supposed to always return 200
		return fmt.Errorf("unexpected return status code: %d", resp.HTTPStatusCode)
	}
	return nil
}

// UpdateDailyChart update the ChartID in the spreadsheetID with data from sheetID
func UpdateDailyChart(_ context.Context, sheetsService *sheets.Service, spreadsheetID string, chartID, sheetID, row int64) error {
	resp, err := sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				UpdateChartSpec: &sheets.UpdateChartSpecRequest{
					ChartId: chartID,
					Spec: &sheets.ChartSpec{
						Title: "Données par jour",
						TitleTextPosition: &sheets.TextPosition{
							HorizontalAlignment: "CENTER",
						},
						BasicChart: &sheets.BasicChartSpec{
							ChartType: "COLUMN",
							Domains: []*sheets.BasicChartDomain{
								{
									Domain: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 0, 0, 1, row), // A0:A{ROW-1}
									},
								},
							},
							HeaderCount: 1,
							Series: []*sheets.BasicChartSeries{
								{
									Series: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 2, 0, 3, row), // C0:C{ROW-1}
									},
									TargetAxis: "LEFT_AXIS",
								},
							},
						},
					},
				},
			},
		},
	}).Do()
	if err != nil {
		return fmt.Errorf("calling API: %w", err)
	}
	if resp.HTTPStatusCode != http.StatusOK {
		// should not happen API is supposed to always return 200
		return fmt.Errorf("unexpected return status code: %d", resp.HTTPStatusCode)
	}
	return nil
}

func newChartData(sheetID, startColumnIndex, startRowIndex, endColumnIndex, endRowIndex int64) *sheets.ChartSourceRange {
	return &sheets.ChartSourceRange{
		Sources: []*sheets.GridRange{
			{
				SheetId:          sheetID,
				StartColumnIndex: startColumnIndex,
				StartRowIndex:    startRowIndex,
				EndColumnIndex:   endColumnIndex,
				EndRowIndex:      endRowIndex,
			},
		},
	}
}

// GetSheet get a spreadsheet information like the chartID
func GetSheet(_ context.Context, sheetsService *sheets.Service, spreadsheetID string) error {
	resp, err := sheetsService.Spreadsheets.Get(spreadsheetID).IncludeGridData(true).Do()
	if err != nil {
		return fmt.Errorf("calling API: %w", err)
	}
	if resp.HTTPStatusCode != http.StatusOK {
		// should not happen API is supposed to always return 200
		return fmt.Errorf("unexpected return status code: %d", resp.HTTPStatusCode)
	}

	fmt.Println(resp.Sheets[0].Charts[0].ChartId)

	return nil
}
