package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
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

	var hourlySheetName string
	var hourlySummaryRange string
	var hourlySheetID int64
	var hourlyChartID int64

	var dailySheetName string
	var dailySummaryRange string
	var dailySheetID int64
	var dailyChartID int64

	flag.StringVar(&petitionName, "petition-name", "", "name of the petition to get metrics from")

	flag.StringVar(&spreadsheetID, "spreadsheet-id", "", "id of the spreadsheet to update")

	flag.StringVar(&hourlySheetName, "hourly-sheet-name", "", "name of the sheet for hourly update")
	flag.StringVar(&hourlySummaryRange, "hourly-summary-range", "", "range in columns for the hourly summary")
	flag.Int64Var(&hourlySheetID, "hourly-sheet-id", 0, "id of the sheet for hourly update")
	flag.Int64Var(&hourlyChartID, "hourly-chart-id", 0, "id of the chart for hourly update")

	flag.StringVar(&dailySheetName, "daily-sheet-name", "", "name of the sheet for daily update")
	flag.StringVar(&dailySummaryRange, "daily-summary-range", "", "range in columns for the daily summary")
	flag.Int64Var(&dailySheetID, "daily-sheet-id", 0, "id of the sheet for daily update")
	flag.Int64Var(&dailyChartID, "daily-chart-id", 0, "id of the chart for daily update")

	flag.Parse()
	if petitionName == "" || spreadsheetID == "" ||
		hourlySheetName == "" || hourlySummaryRange == "" || hourlySheetID == 0 || hourlyChartID == 0 ||
		dailySheetName == "" || dailySummaryRange == "" || dailySheetID == 0 || dailyChartID == 0 {
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

	hourRow, valueRange := computeHourlyRanges(hourlySheetName, hourlySummaryRange, signature, goal)

	dayRow, vr, err := computeDailyRanges(dailySheetName, dailySummaryRange, signature)
	if err != nil {
		fmt.Fprintln(os.Stderr, "computing daily ranges", err)
		return
	}
	valueRange = append(valueRange, vr...)

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

	body, err := ioutil.ReadAll(resp.Body)
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

// computeHourlyRanges computes the needed range to update the data about the hourly chart and summary
func computeHourlyRanges(hourlySheetName, hourlySummaryRange string, signature, goal int64) (int64, []*sheets.ValueRange) {
	start := time.Date(2024, 4, 15, 3, 0, 0, 0, time.Local) // row 18
	row := 18 + int64(time.Now().Sub(start).Hours())

	valueRange := []*sheets.ValueRange{
		// add new hourly value
		&sheets.ValueRange{
			Range:          fmt.Sprintf("%s!A%d:C%d", hourlySheetName, row, row),
			MajorDimension: "ROWS",
			Values: [][]any{
				[]any{
					time.Now().Format("02-01-2006 15:04:05"),
					signature,
					goal,
				},
			},
		},
	}

	// compute new hourly summary
	steps := []int64{48, 24, 12, 6}
	data := make([]any, 0, len(steps))
	for _, step := range steps {
		data = append(data, fmt.Sprintf("='%s'!B%d-'%s'!B%d", hourlySheetName, row, hourlySheetName, row-step))
	}

	return row, append(valueRange, &sheets.ValueRange{
		Range:          hourlySummaryRange,
		MajorDimension: "COLUMNS",
		Values: [][]any{
			data,
		},
	})
}

// computeDailyRanges computes the needed range to update the data about the daily chart and summary
func computeDailyRanges(dailySheetName, dailySummaryRange string, signature int64) (int64, []*sheets.ValueRange, error) {
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
		&sheets.ValueRange{
			Range:          fmt.Sprintf("%s!B%d", dailySheetName, row+1),
			MajorDimension: "ROWS",
			Values: [][]any{
				[]any{
					signature,
				},
			},
		},
	}

	if time.Now().Hour() != 0 {
		return row, valueRange, nil
	}

	sheetName, column, summaryRow, err := parseRange(dailySummaryRange)
	if err != nil {
		return 0, nil, fmt.Errorf("parsing range: %w", err)
	}
	summaryColumn, err := computeNextColumn(column)
	if err != nil {
		return 0, nil, fmt.Errorf("computing next value: %w", err)
	}

	steps := []int64{4, 3, 2, 1, 0}

	// compute trend
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          fmt.Sprintf("%s!%s%d", sheetName, summaryColumn, summaryRow),
		MajorDimension: "ROWS",
		Values: [][]any{
			[]any{
				fmt.Sprintf("='%s'!%s%d-'%s'!C%d", sheetName, column, summaryRow, dailySheetName, row-steps[0]),
			},
		},
	})

	// compute summary
	data := make([]any, 0, len(steps[1:]))
	for _, step := range steps[1:] {
		data = append(data, fmt.Sprintf("='%s'!C%d", dailySheetName, row-step))
	}
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          dailySummaryRange,
		MajorDimension: "COLUMNS",
		Values: [][]any{
			data,
		},
	})

	// add new day
	valueRange = append(valueRange, &sheets.ValueRange{
		Range:          fmt.Sprintf("%s!A%d:C%d", dailySheetName, row, row),
		MajorDimension: "ROWS",
		Values: [][]any{
			[]any{
				time.Now().Format("02-01-2006"),
				signature,
				fmt.Sprintf("=B%d-B%d", row+1, row), // compute the difference with the new next day (which will be computed throughout the day)
			},
		},
	})
	return row, valueRange, nil
}

var regexpRange = regexp.MustCompile("^(.*)!(([a-zA-Z]+)([0-9]+)):[a-zA-Z]+[0-9]+$")

// parseRange parse a range like: Chart!F28:F31
// it returns:
// - the name: Chart
// - the first column: F
// - the first row: 28
func parseRange(range_ string) (string, string, int64, error) {
	parsed := regexpRange.FindStringSubmatch(range_)
	if len(parsed) != 5 {
		return "", "", 0, fmt.Errorf("invalid range: %s", range_)
	}

	row, err := strconv.ParseInt(parsed[4], 10, 64)
	if err != nil {
		return "", "", 0, fmt.Errorf("parsing str to int: %w", err)
	}

	return parsed[1], parsed[3], row, nil
}

func computeNextColumn(column string) (string, error) {
	if len(column) != 1 {
		return "", errors.New("invalid column")
	}

	var c rune
	for _, c = range column {
	}

	if c < 'A' || c >= 'Z' {
		return "", errors.New("out of range")
	}
	return string(c + 1), nil
}

// UpdateHouryChart update the ChartID in the spreadsheetID with data from sheetID
// The value of row is excluded for the range.
func UpdateHouryChart(_ context.Context, sheetsService *sheets.Service, spreadsheetID string, chartID, sheetID, row int64) error {
	resp, err := sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			&sheets.Request{
				UpdateChartSpec: &sheets.UpdateChartSpecRequest{
					ChartId: chartID,
					Spec: &sheets.ChartSpec{
						BasicChart: &sheets.BasicChartSpec{
							ChartType: "LINE",
							Domains: []*sheets.BasicChartDomain{
								&sheets.BasicChartDomain{
									Domain: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 0, 0, 1, row), // A0:A{ROW-1}
									},
								},
							},
							HeaderCount: 1,
							Series: []*sheets.BasicChartSeries{
								&sheets.BasicChartSeries{
									Series: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 1, 0, 2, row), // B0:B{ROW-1}
									},
									TargetAxis: "LEFT_AXIS",
								},
								&sheets.BasicChartSeries{
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
			&sheets.Request{
				UpdateChartSpec: &sheets.UpdateChartSpecRequest{
					ChartId: chartID,
					Spec: &sheets.ChartSpec{
						BasicChart: &sheets.BasicChartSpec{
							ChartType: "COLUMN",
							Domains: []*sheets.BasicChartDomain{
								&sheets.BasicChartDomain{
									Domain: &sheets.ChartData{
										SourceRange: newChartData(sheetID, 0, 0, 1, row), // A0:A{ROW-1}
									},
								},
							},
							HeaderCount: 1,
							Series: []*sheets.BasicChartSeries{
								&sheets.BasicChartSeries{
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
			&sheets.GridRange{
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
