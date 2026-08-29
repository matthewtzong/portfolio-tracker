package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
	// "github.com/matthewtzong/portfolio-tracker/backend/pkg/snaptrade"
)

// Go's reference layout for YYYY-MM-DD.
const dateLayout = "2006-01-02"

// Current holding from Plaid in JSON format.
type HoldingJSON struct {
	AccountID      string  `json:"accountId"`
	AccountName    string  `json:"accountName"`
	Symbol         string  `json:"symbol"`
	Quantity       float64 `json:"quantity"`
	ValueCents     int64   `json:"valueCents"`
	CostBasisCents *int64  `json:"costBasisCents,omitempty"`
}

// Current holdings response.
type HoldingsResponse struct {
	Holdings []HoldingJSON `json:"holdings"`
}

// Snapshot data point for charts in JSON format.
type SnapshotDataPoint struct {
	Date                string `json:"date"`
	PortfolioValueCents int64  `json:"portfolioValueCents"`
}

// Holding data point for charts in JSON format.
type HoldingDataPoint struct {
	Date           string  `json:"date"`
	AccountID      string  `json:"accountId"`
	AccountName    string  `json:"accountName,omitempty"`
	Symbol         string  `json:"symbol"`
	Quantity       float64 `json:"quantity,omitempty"`
	ValueCents     int64   `json:"valueCents"`
	CostBasisCents *int64  `json:"costBasisCents,omitempty"`
}

// Portfolio snapshots response.
type SnapshotsResponse struct {
	Daily   []SnapshotDataPoint `json:"daily"`
	Monthly []SnapshotDataPoint `json:"monthly"`
}

// Portfolio holdings history in JSON format.
type HoldingsHistoryResponse struct {
	Daily []HoldingDataPoint `json:"daily"`
}

// Yearly portfolio summary by account.
type yearlyPortfolioAccountJSON struct {
	AccountID           string `json:"accountId"`
	PortfolioValueCents int64  `json:"portfolioValueCents"`
}

// Yearly portfolio summary response.
type yearlyPortfolioSummaryResponse struct {
	Year      int                          `json:"year"`
	ByAccount []yearlyPortfolioAccountJSON `json:"byAccount"`
}

// Registers the portfolio routes.
func registerPortfolioRoutes(mux *http.ServeMux, deps apiDependencies) {
	// GET /api/portfolio/holdings returns current positions from Plaid.
	mux.Handle("/api/portfolio/holdings", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetHoldings(w, r, deps)
	})))

	// GET /api/portfolio/snapshots returns daily and monthly snapshot data.
	mux.Handle("/api/portfolio/snapshots", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetSnapshots(w, r, deps)
	})))

	// GET /api/portfolio/holdings/history returns daily and monthly holdings data.
	mux.Handle("/api/portfolio/holdings/history", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetHoldingsHistory(w, r, deps)
	})))

	// GET /api/portfolio/summary/yearly returns yearly portfolio summary by account (end-of-year value per account).
	mux.Handle("/api/portfolio/summary/yearly", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetYearlyPortfolioSummary(w, r, deps)
	})))

	// GET /api/portfolio/performance/summary returns Dietz day-over-day and month-over-month gain.
	mux.Handle("/api/portfolio/performance/summary", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetPortfolioPerformanceSummary(w, r, deps)
	})))

	// GET /api/portfolio/performance returns contribution-adjusted return (Modified Dietz).
	mux.Handle("/api/portfolio/performance", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetPortfolioPerformance(w, r, deps)
	})))
}

// Fetches current holdings from Plaid.
func handleGetHoldings(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}

	// Validate the authenticated user exists
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Fetch Plaid accounts to get account IDs and names.
	plaidAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list Plaid accounts: "+err.Error())
		return
	}
	accountNameMap := make(map[string]string)
	for _, a := range plaidAccounts {
		accountNameMap[a.AccountID] = plaidAccountDisplayName(a)
	}

	// Loop through all accounts and fetch the latest holdings for each account.
	holdings := make([]HoldingJSON, 0)
	for _, acc := range plaidAccounts {
		latestDate, err := deps.db.GetLatestDailyHoldingsDateForAccount(r.Context(), acc.AccountID)
		if err != nil || latestDate == nil {
			continue
		}
		accountHoldings, err := deps.db.ListDailyHoldingsByAccount(r.Context(), acc.AccountID, *latestDate, *latestDate)
		if err != nil {
			continue
		}

		// Loop through all holdings for the current account and add them to the response.
		for _, holding := range accountHoldings {
			holdings = append(holdings, HoldingJSON{
				AccountID:      holding.AccountID,
				AccountName:    accountNameMap[holding.AccountID],
				Symbol:         holding.Symbol,
				Quantity:       holding.Quantity,
				ValueCents:     holding.ValueCents,
				CostBasisCents: holding.CostBasisCents,
			})
		}
	}

	resp := HoldingsResponse{
		Holdings: holdings,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

/*
// Fetches current holdings from Snaptrade.
func handleGetHoldings(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil || deps.snaptradeClient == nil {
		writeJSONError(w, http.StatusInternalServerError, "database or snaptrade client not configured")
		return
	}

	// Validate the authenticated user exists
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Get Snaptrade user
	snapUser, err := deps.db.GetSnaptradeUser(r.Context())
	if err != nil || snapUser == nil {
		writeJSONError(w, http.StatusNotFound, "snaptrade user not found")
		return
	}

	// List all accounts
	accounts, err := deps.snaptradeClient.ListAccounts(snapUser.UserID, snapUser.UserSecret)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}

	// Fetch positions for each account
	var holdings []HoldingJSON
	accountMap := make(map[string]string)
	for _, account := range accounts {
		accountMap[account.ID] = account.Name

		// List positions for the account.
		positions, err := deps.snaptradeClient.ListAccountPositions(snapUser.UserID, snapUser.UserSecret, account.ID)
		if err != nil {
			continue
		}

		// Add the positions to the holdings.
		for _, pos := range positions {
			holdings = append(holdings, HoldingJSON{
				AccountID:   account.ID,
				AccountName: account.Name,
				Symbol:      pos.Symbol,
				Quantity:    pos.Quantity,
				ValueCents:  pos.ValueCents,
			})
		}
	}

	resp := HoldingsResponse{
		Holdings: holdings,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
*/

// Fetches daily and monthly snapshot data.
func handleGetSnapshots(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database is not configured")
		return
	}

	// Validate the authenticated user exists
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Get necessary dates and parameters.
	accountID := r.URL.Query().Get("accountId")
	now := GetLocalNow()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation())
	dailyStart := dayStart.AddDate(0, 0, -30)

	// List the daily snapshots.
	dailySnapshots, err := deps.db.ListDailySnapshots(r.Context(), dailyStart, dayStart)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list daily snapshots: "+err.Error())
		return
	}

	// List the monthly snapshots
	monthlyStart := time.Date(now.Year()-2, 1, 1, 0, 0, 0, 0, GetLocalLocation())
	monthlyPoints := make([]SnapshotDataPoint, 0)
	if accountID != "" {
		// List the monthly snapshots for a given account.
		byAccount, err := deps.db.ListMonthlySnapshotsByAccount(r.Context(), monthlyStart, dayStart, accountID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to list monthly snapshots by account: "+err.Error())
			return
		}
		// Convert the monthly snapshots to the data points.
		monthlyPoints = make([]SnapshotDataPoint, 0, len(byAccount))
		for _, snapshot := range byAccount {
			monthlyPoints = append(monthlyPoints, SnapshotDataPoint{
				Date:                snapshot.Month.Format(dateLayout),
				PortfolioValueCents: snapshot.PortfolioValueCents,
			})
		}
	} else {
		// List the total monthly snapshots across all accounts and aggregate them.
		allMonthlySnapshots, err := deps.db.ListMonthlySnapshots(r.Context(), monthlyStart, dayStart)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to list monthly snapshots: "+err.Error())
			return
		}
		monthlySum := make(map[string]int64)
		for _, snapshot := range allMonthlySnapshots {
			month := snapshot.Month.Format(dateLayout)
			monthlySum[month] += snapshot.PortfolioValueCents
		}
		for month, sum := range monthlySum {
			monthlyPoints = append(monthlyPoints, SnapshotDataPoint{
				Date:                month,
				PortfolioValueCents: sum,
			})
		}
	}
	// Convert the daily snapshots into data points.
	dailyPoints := make([]SnapshotDataPoint, 0)
	if accountID != "" {
		// Get the daily holdings for each account.
		recentHoldings, _ := deps.db.ListDailyHoldingsByAccount(r.Context(), accountID, dailyStart, dayStart)
		holdingsByDate := make(map[string]int64)
		for _, holding := range recentHoldings {
			dateStr := holding.Date.Format(dateLayout)
			holdingsByDate[dateStr] += holding.ValueCents
		}

		// Collect only the dates we have data for.
		for d := dailyStart; !d.After(dayStart); d = d.AddDate(0, 0, 1) {
			dateStr := d.Format(dateLayout)
			if val, ok := holdingsByDate[dateStr]; ok {
				dailyPoints = append(dailyPoints, SnapshotDataPoint{
					Date:                dateStr,
					PortfolioValueCents: val,
				})
			}
		}
	} else {
		// Calculate the total daily snapshots across all accounts and aggregate them.
		snapshotMap := make(map[string]int64)
		for _, snapshot := range dailySnapshots {
			snapshotMap[snapshot.Date.Format(dateLayout)] = snapshot.PortfolioValueCents
		}

		var lastValue int64
		for d := dailyStart; !d.After(dayStart); d = d.AddDate(0, 0, 1) {
			dateStr := d.Format(dateLayout)
			if val, ok := snapshotMap[dateStr]; ok {
				lastValue = val
			}
			// Only add the data point if the last value is greater than 0.
			if lastValue > 0 {
				dailyPoints = append(dailyPoints, SnapshotDataPoint{
					Date:                dateStr,
					PortfolioValueCents: lastValue,
				})
			}
		}
	}

	// Include the current month in the monthly chart even if it hasn't ended yet.
	currentMonthKey := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, GetLocalLocation()).Format(dateLayout)
	hasCurrentMonth := false
	for _, monthPoint := range monthlyPoints {
		if monthPoint.Date == currentMonthKey {
			hasCurrentMonth = true
			break
		}
	}

	// Use the absolute latest daily point as the current month's "live" value.
	if !hasCurrentMonth && len(dailyPoints) > 0 {
		latestPoint := dailyPoints[len(dailyPoints)-1]
		monthlyPoints = append(monthlyPoints, SnapshotDataPoint{
			Date:                currentMonthKey,
			PortfolioValueCents: latestPoint.PortfolioValueCents,
		})
	}

	sortSnapshotDataPoints(monthlyPoints)

	resp := SnapshotsResponse{
		Daily:   dailyPoints,
		Monthly: monthlyPoints,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// Sorts the snapshot data points by date.
func sortSnapshotDataPoints(points []SnapshotDataPoint) {
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
}

// Fetches daily holdings history.
func handleGetHoldingsHistory(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database is not configured")
		return
	}

	// Validate the authenticated user exists
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Get the account ID and symbol from the query parameters.
	accountID := r.URL.Query().Get("accountId")
	symbol := r.URL.Query().Get("symbol")

	now := GetLocalNow()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation())
	dailyStart := dayStart.AddDate(0, 0, -30)

	var dailyHoldings []database.DailyHolding
	var err error

	if accountID != "" {
		dailyHoldings, err = deps.db.ListDailyHoldingsByAccount(r.Context(), accountID, dailyStart, dayStart)
	} else if symbol != "" {
		dailyHoldings, err = deps.db.ListDailyHoldingsBySymbol(r.Context(), symbol, dailyStart, dayStart)
	} else {
		dailyHoldings, err = deps.db.ListDailyHoldings(r.Context(), dailyStart, dayStart)
	}

	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list daily holdings: "+err.Error())
		return
	}

	// Fetch Plaid accounts to get account names.
	plaidAccounts, _ := deps.db.ListPlaidAccounts(r.Context())
	accountMap := make(map[string]string)
	for _, account := range plaidAccounts {
		accountMap[account.AccountID] = plaidAccountDisplayName(account)
	}
	// Group holdings by date for easier iteration.
	holdingsByDate := make(map[string][]database.DailyHolding)
	for _, holding := range dailyHoldings {
		dateStr := holding.Date.Format(dateLayout)
		holdingsByDate[dateStr] = append(holdingsByDate[dateStr], holding)
	}

	dailyPoints := make([]HoldingDataPoint, 0)

	// Iterate through the daily holdings and add them to the daily points.
	for d := dailyStart; !d.After(dayStart); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format(dateLayout)

		if newHoldings, ok := holdingsByDate[dateStr]; ok {
			for _, nh := range newHoldings {
				if nh.Quantity == 0 {
					continue
				}
				dailyPoints = append(dailyPoints, HoldingDataPoint{
					Date:           dateStr,
					AccountID:      nh.AccountID,
					AccountName:    accountMap[nh.AccountID],
					Symbol:         nh.Symbol,
					Quantity:       nh.Quantity,
					ValueCents:     nh.ValueCents,
					CostBasisCents: nh.CostBasisCents,
				})
			}
		}
	}

	resp := HoldingsHistoryResponse{
		Daily: dailyPoints,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// Returns yearly portfolio summary by account (end-of-year value per account).
func handleGetYearlyPortfolioSummary(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}

	// Parses the year.
	yearStr := r.URL.Query().Get("year")
	if yearStr == "" {
		writeJSONError(w, http.StatusBadRequest, "year parameter is required (e.g. 2024)")
		return
	}
	var year int
	_, err := fmt.Sscanf(yearStr, "%d", &year)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "year must be a valid 4-digit year")
		return
	}

	// Lists the yearly portfolio summaries.
	summaries, err := deps.db.ListYearlyPortfolioSummaries(r.Context(), year)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list yearly portfolio summary: "+err.Error())
		return
	}

	// Converts the summaries to our API model.
	byAccount := make([]yearlyPortfolioAccountJSON, 0, len(summaries))
	for _, summary := range summaries {
		byAccount = append(byAccount, yearlyPortfolioAccountJSON{
			AccountID:           summary.AccountID,
			PortfolioValueCents: summary.PortfolioValueCents,
		})
	}

	// Encodes the response.
	err = json.NewEncoder(w).Encode(yearlyPortfolioSummaryResponse{
		Year:      year,
		ByAccount: byAccount,
	})
	if err != nil {
		log.Printf("get yearly portfolio summary encode: %v", err)
	}
}
