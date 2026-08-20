package server

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
)

// Registers export routes.
func registerExportRoutes(mux *http.ServeMux, deps apiDependencies) {
	// GET /api/export/transactions?month=YYYY-MM exports transactions for a month as CSV.
	mux.Handle("/api/export/transactions", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleExportTransactions(w, r, deps)
	})))

	// GET /api/export/portfolio/snapshots?month=YYYY-MM exports portfolio snapshots for a month as CSV.
	mux.Handle("/api/export/portfolio/snapshots", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleExportPortfolioSnapshots(w, r, deps)
	})))

	// GET /api/export/portfolio/holdings?month=YYYY-MM exports portfolio holdings for a month as CSV.
	mux.Handle("/api/export/portfolio/holdings", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleExportPortfolioHoldings(w, r, deps)
	})))
}

// Exports transactions for a given month as CSV.
func handleExportTransactions(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	month := r.URL.Query().Get("month")
	if month == "" {
		writeJSONError(w, http.StatusBadRequest, "month parameter is required (format: YYYY-MM)")
		return
	}

	// Parses the month.
	monthTime, err := time.Parse("2006-01", month)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid month format (use YYYY-MM)")
		return
	}

	// Builds the CSV for the transactions.
	csvBytes, err := BuildTransactionsCSV(r.Context(), deps.db, monthTime)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to export transactions: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=transactions-%s.csv", month))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(csvBytes)
}

// Exports portfolio snapshots for a given month as CSV.
func handleExportPortfolioSnapshots(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	month := r.URL.Query().Get("month")
	if month == "" {
		writeJSONError(w, http.StatusBadRequest, "month parameter is required (format: YYYY-MM)")
		return
	}

	// Parses the month.
	monthTime, err := time.Parse("2006-01", month)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid month format (use YYYY-MM)")
		return
	}

	// Fetch monthly snapshots for the requested month.
	monthStart := time.Date(monthTime.Year(), monthTime.Month(), 1, 0, 0, 0, 0, GetLocalLocation())
	monthEnd := monthStart.AddDate(0, 1, -1)
	snapshots, err := deps.db.ListMonthlySnapshots(r.Context(), monthStart, monthEnd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch snapshots: "+err.Error())
		return
	}

	// Fetch all Plaid accounts to map IDs to names.
	accounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch accounts: "+err.Error())
		return
	}
	accountMap := make(map[string]string)
	for _, acc := range accounts {
		accountMap[acc.AccountID] = plaidAccountDisplayName(acc)
	}

	// Builds the CSV for the monthly snapshots.
	csvBytes, err := BuildPortfolioSnapshotsCSV(snapshots, accountMap)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to export snapshots: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=portfolio-snapshots-%s.csv", month))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(csvBytes)
}

// Exports portfolio holdings for a given month as CSV.
func handleExportPortfolioHoldings(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	month := r.URL.Query().Get("month")
	if month == "" {
		writeJSONError(w, http.StatusBadRequest, "month parameter is required (format: YYYY-MM)")
		return
	}

	// Parse month and get date range.
	monthTime, err := time.Parse("2006-01", month)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid month format (use YYYY-MM)")
		return
	}
	// Fetch daily holdings for the requested month.
	startDate := time.Date(monthTime.Year(), monthTime.Month(), 1, 0, 0, 0, 0, GetLocalLocation())
	endDate := monthTime.AddDate(0, 1, -1)

	// Fetch daily holdings for the month.
	holdings, err := deps.db.ListDailyHoldings(r.Context(), startDate, endDate)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch holdings: "+err.Error())
		return
	}

	// Fetch all Plaid accounts to map IDs to names.
	allAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch accounts: "+err.Error())
		return
	}

	accountMap := make(map[string]string)
	for _, acc := range allAccounts {
		accountMap[acc.AccountID] = plaidAccountDisplayName(acc)
	}

	// Set CSV headers and write CSV.
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=portfolio-holdings-%s.csv", month))
	w.Header().Set("Cache-Control", "no-cache")
	writer := csv.NewWriter(w)
	defer writer.Flush()
	csvHeaders := []string{"Date", "Account", "Symbol", "Quantity", "Value ($)"}
	err = writer.Write(csvHeaders)
	if err != nil {
		return
	}

	// Writes the rows.
	for _, holding := range holdings {
		portfolioValueDollars := float64(holding.ValueCents) / 100.0
		displayName := holding.AccountID
		if name, ok := accountMap[holding.AccountID]; ok {
			displayName = name
		}

		row := []string{
			holding.Date.Format("2006-01-02"),
			displayName,
			holding.Symbol,
			strconv.FormatFloat(holding.Quantity, 'f', 8, 64),
			strconv.FormatFloat(portfolioValueDollars, 'f', 2, 64),
		}
		err = writer.Write(row)
		if err != nil {
			return
		}
	}
}
