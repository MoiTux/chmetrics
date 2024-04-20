package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	sheets "google.golang.org/api/sheets/v4"
)

// export GOOGLE_APPLICATION_CREDENTIALS=/path/to/credential

const (
	spreadsheetID       = "qwertyuiop"
	chartID       int64 = 234
	sheetID       int64 = 45345
	petitionName        = "name of a petition"
)

func main() {
	ctx := context.Background()
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

	row, err := UpdateSheetData(ctx, sheetsService, spreadsheetID, "ParHeure!A1:C1", []any{
		time.Now().Format("02-01-2006 15:04:05"),
		signature,
		goal,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "updating sheet data", err)
		return
	}

	err = UpdateSheetChart(ctx, sheetsService, spreadsheetID, chartID, sheetID, row)
	if err != nil {
		fmt.Fprintln(os.Stderr, "updating sheet chart", err)
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

// UpdateSheetData append values into the spreadsheets sheetID and based on the range (cell).
// It return the row number of where values have been added.
func UpdateSheetData(_ context.Context, sheetsService *sheets.Service, spreadsheetID, range_ string, values []any) (int64, error) {
	appendCall := sheets.NewSpreadsheetsValuesService(sheetsService).Append(
		spreadsheetID,
		range_,
		&sheets.ValueRange{
			MajorDimension: "ROWS",
			Values: [][]any{
				values,
			},
		},
	)

	resp, err := appendCall.ValueInputOption("USER_ENTERED").IncludeValuesInResponse(false).Do()
	if err != nil {
		return 0, fmt.Errorf("calling API: %w", err)
	}
	if resp.HTTPStatusCode != http.StatusOK {
		// should not happen API is supposed to always return 200
		return 0, fmt.Errorf("unexpected return status code: %d", resp.HTTPStatusCode)
	}

	reg, err := regexp.Compile(":[A-Z]+([0-9]+)$")
	if err != nil {
		return 0, fmt.Errorf("compiling the regexp: %w", err)
	}

	matches := reg.FindAllStringSubmatch(resp.TableRange, -1)
	if len(matches) != 1 && len(matches[0]) != 2 {
		return 0, fmt.Errorf("parsing the new range: %s", resp.TableRange)
	}

	row, err := strconv.ParseInt(matches[0][1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing str to int: %w", err)
	}
	return row, nil
}

// UpdateSheetChart update the ChartID in the spreadsheetID with data from sheetID
func UpdateSheetChart(_ context.Context, sheetsService *sheets.Service, spreadsheetID string, chartID, sheetID, row int64) error {
	resp, err := sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			&sheets.Request{
				UpdateChartSpec: &sheets.UpdateChartSpecRequest{
					ChartId: chartID,
					Spec: &sheets.ChartSpec{
						BasicChart: &sheets.BasicChartSpec{
							ChartType: "LINE",
							Axis: []*sheets.BasicChartAxis{
								&sheets.BasicChartAxis{
									Position:          "BOTTOM_AXIS",
									ViewWindowOptions: &sheets.ChartAxisViewWindowOptions{},
								},
								&sheets.BasicChartAxis{
									Position:          "LEFT_AXIS",
									ViewWindowOptions: &sheets.ChartAxisViewWindowOptions{},
								},
							},
							Domains: []*sheets.BasicChartDomain{
								&sheets.BasicChartDomain{
									Domain: &sheets.ChartData{
										SourceRange: &sheets.ChartSourceRange{
											Sources: []*sheets.GridRange{
												&sheets.GridRange{
													SheetId:        sheetID,
													EndColumnIndex: 1,
													EndRowIndex:    row,
												},
											},
										},
									},
								},
							},
							HeaderCount: 1,
							Series: []*sheets.BasicChartSeries{
								&sheets.BasicChartSeries{
									Series: &sheets.ChartData{
										SourceRange: &sheets.ChartSourceRange{
											Sources: []*sheets.GridRange{
												&sheets.GridRange{
													SheetId:          sheetID,
													StartColumnIndex: 1,
													EndColumnIndex:   2,
													EndRowIndex:      row,
												},
											},
										},
									},
									TargetAxis: "LEFT_AXIS",
								},
								&sheets.BasicChartSeries{
									Series: &sheets.ChartData{
										SourceRange: &sheets.ChartSourceRange{
											Sources: []*sheets.GridRange{
												&sheets.GridRange{
													SheetId:          sheetID,
													StartColumnIndex: 2,
													EndColumnIndex:   3,
													EndRowIndex:      row,
												},
											},
										},
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
