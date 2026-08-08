package server

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/email"
	// "github.com/matthewtzong/portfolio-tracker/backend/pkg/snaptrade"
)

// Cron Response.
type cronSyncResponse struct {
	PlaidSyncedItems        int  `json:"plaidSyncedItems"`
	DailySnapshotWritten    bool `json:"dailySnapshotWritten"`
	MonthlySnapshotsWritten int  `json:"monthlySnapshotsWritten"`
}

// Registers cron routes.
func registerCronRoutes(mux *http.ServeMux, deps apiDependencies) {
	mux.HandleFunc("/api/cron/daily-sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleDailySync(w, r, deps)
	})
}

// Handles the nightly cron job: (Plaid Syncs + Status Checks).
func handleDailySync(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	// Gets the cron secret.
	cronSecret := os.Getenv("CRON_SECRET")

	// Checks the Authorization header (Bearer token).
	authHeader := r.Header.Get("Authorization")
	expectedAuth := "Bearer " + cronSecret
	if authHeader != expectedAuth {
		writeJSONError(w, http.StatusUnauthorized, "invalid authorization")
		return
	}

	// Calculate target date (yesterday).
	now := GetLocalNow()
	targetDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation()).AddDate(0, 0, -1)

	// Update connection statuses for Plaid.
	_ = checkAndUpdatePlaidItemStatuses(r.Context(), deps.db, deps.plaidClient)

	/*
		// Update connection statuses for Snaptrade.
		_ = checkAndUpdateSnaptradeConnectionStatuses(r.Context(), deps.db, deps.snaptradeClient)
	*/

	// Sync Plaid items with pending transactions.
	plaidSynced, err := runPlaidSafetySync(r, deps)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Plaid sync failed: "+err.Error())
		return
	}

	// Fetch Plaid investment holdings/balances and write snapshots for the target date.
	dailyWritten, err := writeInvestmentSnapshotsForDate(r, deps, targetDate)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Investment snapshot failed: "+err.Error())
		return
	}

	/*
		// Fetch Snaptrade holdings/balances and write today's snapshots.
		dailyWritten, monthlyWritten, err := writeSnaptradeSnapshotsForToday(r, deps)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Snaptrade snapshot failed: "+err.Error())
			return
		}
	*/

	// Run retention job to prune old data.
	_ = runRetentionJob(r.Context(), deps, targetDate)

	// Returns the response.
	resp := cronSyncResponse{
		PlaidSyncedItems:     plaidSynced,
		DailySnapshotWritten: dailyWritten,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Syncs Plaid items with pending transactions using cursor-based sync.
func runPlaidSafetySync(r *http.Request, deps apiDependencies) (int, error) {
	if deps.db == nil || deps.plaidClient == nil {
		return 0, nil
	}

	// Lists the items with pending transactions.
	items, err := deps.db.ListPlaidItemsWithPendingTransactions(r.Context())
	if err != nil {
		return 0, err
	}

	// Syncs the transactions for each item.
	for _, item := range items {
		if item.AccessToken == "manual" {
			continue
		}
		err = SyncTransactionsForItem(r.Context(), deps.db, deps.plaidClient, &item)
		if err != nil {
			return 0, err
		}
	}

	return len(items), nil
}

// Adds daily Plaid holdings/snapshots for a given date along with end of month monthly snapshots.
func writeInvestmentSnapshotsForDate(r *http.Request, deps apiDependencies, date time.Time) (bool, error) {
	if deps.db == nil || deps.plaidClient == nil {
		return false, nil
	}

	// Fetch all Plaid items.
	items, err := deps.db.ListPlaidItems(r.Context())
	if err != nil {
		return false, err
	}

	// Calculate date for snapshots.
	today := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, GetLocalLocation())

	for _, item := range items {
		if item.AccessToken == "manual" {
			continue
		}
		// Fetch accounts for this item to identify investment accounts.
		accounts, err := deps.plaidClient.GetAccounts(r.Context(), item.AccessToken)
		if err != nil {
			log.Printf("cron: failed to get accounts for item %s: %v", item.ItemID, err)
			continue
		}

		investmentAccountIDs := make(map[string]bool)
		var dbAccounts []database.PlaidAccount
		for _, acc := range accounts {
			if acc.Type == "investment" {
				investmentAccountIDs[acc.AccountID] = true
			}
			account := database.PlaidAccount{
				PlaidItemID:    item.ItemID,
				AccountID:      acc.AccountID,
				Name:           acc.Name,
				Type:           acc.Type,
				CurrentBalance: acc.Balances.Current,
			}
			if acc.Mask != "" {
				account.Mask = &acc.Mask
			}
			if acc.Subtype != "" {
				account.Subtype = &acc.Subtype
			}
			dbAccounts = append(dbAccounts, account)
		}

		err = deps.db.UpsertPlaidAccounts(r.Context(), dbAccounts)
		if err != nil {
			log.Printf("cron: failed to upsert plaid accounts for item %s: %v", item.ItemID, err)
		}

		if len(investmentAccountIDs) == 0 {
			continue
		}

		// Fetch holdings for this item.
		plaidHoldings, securities, err := deps.plaidClient.GetHoldings(r.Context(), item.AccessToken)
		if err != nil {
			log.Printf("cron: failed to get holdings for item %s: %v", item.ItemID, err)
			continue
		}

		// Map security IDs to tickers.
		securityTickerMap := make(map[string]string)
		for _, sec := range securities {
			if sec.Ticker != nil {
				securityTickerMap[sec.SecurityID] = *sec.Ticker
			} else if sec.Name != nil {
				securityTickerMap[sec.SecurityID] = *sec.Name
			} else {
				securityTickerMap[sec.SecurityID] = "UNKNOWN"
			}
		}

		// Add holdings for investment accounts.
		for _, ph := range plaidHoldings {
			if !investmentAccountIDs[ph.AccountID] {
				continue
			}

			var costBasisCents *int64
			if ph.CostBasis != nil {
				cbc := int64(math.Round(*ph.CostBasis * 100))
				costBasisCents = &cbc
			}

			holding := &database.DailyHolding{
				Date:           database.DateOnly{Time: today},
				AccountID:      ph.AccountID,
				Symbol:         securityTickerMap[ph.SecurityID],
				Quantity:       ph.Quantity,
				ValueCents:     int64(math.Round(ph.InstitutionValue * 100)),
				CostBasisCents: costBasisCents,
			}
			err = deps.db.UpsertDailyHolding(r.Context(), holding)
			if err != nil {
				log.Printf("cron: failed to upsert daily holding for %s: %v", holding.Symbol, err)
			}
		}
	}

	// Update snapshots (daily and monthly) for today.
	err = updatePortfolioSnapshots(r, deps, today)
	if err != nil {
		return false, err
	}

	return true, nil
}

/*
// Adds today's Snaptrade daily holdings/snapshots along with end of month monthly snapshots.
func writeSnaptradeSnapshotsForToday(r *http.Request, deps apiDependencies) (bool, int, error) {
	if deps.db == nil || deps.snaptradeClient == nil {
		return false, 0, nil
	}

	// Gets the Snaptrade user.
	user, err := deps.db.GetSnaptradeUser(r.Context())
	if err != nil || user == nil {
		return false, 0, err
	}

	// List all Snaptrade accounts with balances.
	accounts, err := deps.snaptradeClient.ListAccounts(user.UserID, user.UserSecret)
	if err != nil {
		return false, 0, err
	}
	if len(accounts) == 0 {
		return false, 0, nil
	}

	// Sets the current time as the snapshot date.
	now := GetLocalNow()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation())

	var (
		totalPortfolioCents int64
		accountTotals       = make(map[string]int64)
	)

	// Loops through the accounts and computes the total portfolio value and per-account totals.
	for _, account := range accounts {
		accountTotalCents := int64(math.Round(account.BalanceAmount * 100))
		totalPortfolioCents += accountTotalCents
		accountTotals[account.ID] = accountTotalCents

		// Lists the positions for the account.
		positions, err := deps.snaptradeClient.ListAccountPositions(user.UserID, user.UserSecret, account.ID)
		if err != nil {
			continue
		}

		// Loops through the positions and adds them to the daily holdings.
		for _, pos := range positions {
			holding := &database.DailyHolding{
				Date:       database.DateOnly{Time: today},
				AccountID:  account.ID,
				Symbol:     pos.Symbol,
				Quantity:   pos.Quantity,
				ValueCents: pos.ValueCents,
			}
			_ = deps.db.UpsertDailyHolding(r.Context(), holding)
		}
	}

	// Writes today's total portfolio snapshot.
	snapshot := &database.DailySnapshot{
		Date:                database.DateOnly{Time: today},
		PortfolioValueCents: totalPortfolioCents,
	}
	err = deps.db.UpsertDailySnapshot(r.Context(), snapshot)
	if err != nil {
		return false, 0, err
	}

	// Writes the monthly snapshots.
	monthlyWritten, err := maybeWriteMonthlySnapshots(r, deps, today, accountTotals)
	if err != nil {
		return true, 0, err
	}

	// Writes the monthly net worth snapshot.
	err = maybeWriteMonthlyNetWorth(r, deps, today, totalPortfolioCents)
	if err != nil {
		return true, monthlyWritten, err
	}

	return true, monthlyWritten, nil
}
*/

// If today is the end of the month, write a single overall monthly net worth snapshot.
func maybeWriteMonthlyNetWorth(r *http.Request, deps apiDependencies, date time.Time, _ int64) error {
	// Check if today is the last calendar day of the month.
	year, month, _ := date.Date()
	nextDay := date.AddDate(0, 0, 1)
	if nextDay.Month() == date.Month() {
		return nil
	}

	if deps.db == nil {
		return nil
	}

	// Fetch all accounts to sum up cash, investments, and liabilities.
	plaidAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		return err
	}

	var cashCents, investmentsCents, liabilitiesCents int64
	var foundAny bool

	// Loop through all accounts and sum up cash, investments, and liabilities.
	for _, account := range plaidAccounts {
		if account.CreatedAt != nil && account.CreatedAt.After(time.Date(year, month+1, 0, 23, 59, 59, 0, GetLocalLocation())) {
			continue
		}

		foundAny = true
		_, cashDelta, investDelta, liabilityDelta := loadPlaidAccounts(account, nil)
		cashCents += cashDelta
		investmentsCents += investDelta
		liabilitiesCents += liabilityDelta
	}

	// Skip if no accounts were active as of this date.
	if !foundAny {
		log.Printf("cron: skipping monthly net worth for %s - no active accounts found", date.Format("2006-01-02"))
		return nil
	}

	netWorthCents := cashCents + investmentsCents - liabilitiesCents

	snapshot := &database.MonthlyNetWorth{
		Month:            database.DateOnly{Time: date},
		NetWorthCents:    netWorthCents,
		CashCents:        cashCents,
		InvestmentsCents: investmentsCents,
		LiabilitiesCents: liabilitiesCents,
	}
	return deps.db.UpsertMonthlyNetWorth(r.Context(), snapshot)
}

// Runs retention job to prune old data according to retention rules.
func runRetentionJob(ctx context.Context, deps apiDependencies, date time.Time) error {
	if deps.db == nil {
		return nil
	}

	today := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, GetLocalLocation())
	userEmail := os.Getenv("ALLOWED_USER_EMAIL")

	// Prunes 1 year old transactions on the first day of each month.
	if today.Day() == 1 {
		monthToPrune := time.Date(today.Year()-1, today.Month(), 1, 0, 0, 0, 0, GetLocalLocation())
		transactions, err := deps.db.ListTransactionsForMonth(ctx, monthToPrune)
		if err != nil {
			log.Printf("retention: list transactions for month %s: %v", monthToPrune.Format("2006-01"), err)
			return err
		}
		if len(transactions) == 0 {
			return nil
		}
		// Builds CSV, emails, creates summary, then deletes the data from the database.
		csvBytes, err := BuildTransactionsCSV(ctx, deps.db, monthToPrune)
		if err != nil {
			log.Printf("retention: build transactions CSV for %s: %v", monthToPrune.Format("2006-01"), err)
			return err
		}
		monthStr := monthToPrune.Format("2006-01")
		err = email.SendCSV(ctx, userEmail, "Portfolio Tracker: transactions export for "+monthStr, "transactions-"+monthStr+".csv", csvBytes)
		if err != nil {
			log.Printf("retention: send transactions email: %v", err)
			return err
		}
		_ = createMonthlyExpenseSummary(ctx, deps, monthToPrune)
		err = deps.db.DeleteTransactionsInMonth(ctx, monthToPrune)
		if err != nil {
			log.Printf("retention: delete transactions for month %s: %v", monthToPrune.Format("2006-01"), err)
		}
	}

	// Prunes daily snapshots and holdings older than 30 days.
	thirtyDaysAgo := today.AddDate(0, 0, -30)
	dayBeingDeleted := thirtyDaysAgo
	nextDay := dayBeingDeleted.AddDate(0, 0, 1)
	if nextDay.Month() != dayBeingDeleted.Month() {
		// Writes the monthly snapshot for the day being deleted.
		monthStart := time.Date(dayBeingDeleted.Year(), dayBeingDeleted.Month(), 1, 0, 0, 0, 0, GetLocalLocation())
		holdings, _ := deps.db.ListDailyHoldings(ctx, dayBeingDeleted, dayBeingDeleted)
		if len(holdings) > 0 {
			accountTotals := make(map[string]int64)
			for _, holding := range holdings {
				accountTotals[holding.AccountID] += holding.ValueCents
			}
			for accountID, total := range accountTotals {
				monthlySnapshot := &database.MonthlySnapshot{
					Month:               database.DateOnly{Time: monthStart},
					AccountID:           accountID,
					PortfolioValueCents: total,
				}
				_ = deps.db.UpsertMonthlySnapshot(ctx, monthlySnapshot)
			}
		}
	}
	// Deletes the daily snapshots and holdings older than 30 days.
	_ = deps.db.DeleteDailySnapshotsOlderThan(ctx, thirtyDaysAgo)
	_ = deps.db.DeleteDailyHoldingsOlderThan(ctx, thirtyDaysAgo)

	// Prunes yearly monthly snapshots on December 31.
	if today.Month() != 12 || today.Day() != 31 {
		return nil
	}

	lastYear := today.Year()
	snapshots, err := deps.db.ListMonthlySnapshotsForYear(ctx, lastYear)
	// Creates yearly summaries and deletes the monthly snapshots.
	if err != nil || len(snapshots) == 0 {
		_ = createYearlyExpenseSummaries(ctx, deps, lastYear)
		_ = deps.db.DeleteMonthlySnapshotsForYear(ctx, lastYear)
		return nil
	}

	// Fetch all Plaid accounts to map IDs to names.
	accounts, err := deps.db.ListPlaidAccounts(ctx)
	if err != nil {
		log.Printf("retention: failed to fetch accounts for yearly CSV: %v", err)
	}
	accountMap := make(map[string]string)
	for _, account := range accounts {
		accountMap[account.AccountID] = account.Name
	}

	// Builds the CSV for the yearly monthly snapshots.
	csvBytes, err := BuildPortfolioSnapshotsCSV(snapshots, accountMap)
	if err != nil {
		log.Printf("retention: build portfolio CSV for year %d: %v", lastYear, err)
		return err
	}
	yearStr := strconv.Itoa(lastYear)
	err = email.SendCSV(ctx, userEmail, "Portfolio Tracker: portfolio snapshots export for "+yearStr, "portfolio-snapshots-"+yearStr+".csv", csvBytes)
	if err != nil {
		log.Printf("retention: send portfolio email: %v", err)
		return err
	}
	_ = createYearlyExpenseSummaries(ctx, deps, lastYear)
	_ = deps.db.DeleteMonthlySnapshotsForYear(ctx, lastYear)
	return nil
}

// Creates monthly expense summary for a given month from transactions.
func createMonthlyExpenseSummary(ctx context.Context, deps apiDependencies, month time.Time) error {
	// Get transactions for the month.
	transactions, err := deps.db.ListTransactionsForMonth(ctx, month)
	if err != nil {
		return err
	}

	// Gets the categories.
	categories, err := deps.db.ListCategories(ctx)
	if err != nil {
		return err
	}
	categoriesByID := make(map[int64]database.Category, len(categories))
	for _, category := range categories {
		categoriesByID[category.ID] = category
	}

	categoryTotals := make(map[int64]int64)
	categoryCounts := make(map[int64]int)

	// Loops through the transactions and aggregates the spend by category.
	for _, transaction := range transactions {
		if transaction.CategoryID == nil {
			continue
		}
		category, ok := categoriesByID[*transaction.CategoryID]
		if !ok || !category.Expense {
			continue
		}
		delta := -transaction.AmountCents
		if delta == 0 {
			continue
		}
		categoryTotals[category.ID] += delta
		categoryCounts[category.ID]++
	}

	// Upsert monthly expense summaries.
	for categoryID, total := range categoryTotals {
		summary := &database.MonthlyExpenseSummary{
			Month:            database.DateOnly{Time: month},
			CategoryID:       categoryID,
			TotalCents:       total,
			TransactionCount: categoryCounts[categoryID],
		}
		_ = deps.db.UpsertMonthlyExpenseSummary(ctx, summary)
	}
	return nil
}

// Creates yearly summaries from monthly data for a given year.
func createYearlyExpenseSummaries(ctx context.Context, deps apiDependencies, year int) error {
	// Lists all monthly snapshots for the year and aggregates them (investments only).
	yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, GetLocalLocation())
	yearEnd := time.Date(year, 12, 31, 0, 0, 0, 0, GetLocalLocation())

	// Aggregate monthly expense summaries by category.
	monthlySummaries, err := deps.db.ListMonthlyExpenseSummaries(ctx, yearStart, yearEnd)
	if err == nil {
		categoryTotals := make(map[int64]int64)
		categoryCounts := make(map[int64]int)

		for _, summary := range monthlySummaries {
			categoryTotals[summary.CategoryID] += summary.TotalCents
			categoryCounts[summary.CategoryID] += summary.TransactionCount
		}

		for categoryID, total := range categoryTotals {
			yearlyExpenseSummary := &database.YearlyExpenseSummary{
				Year:             year,
				CategoryID:       categoryID,
				TotalCents:       total,
				TransactionCount: categoryCounts[categoryID],
			}
			_ = deps.db.UpsertYearlyExpenseSummary(ctx, yearlyExpenseSummary)
		}
	}

	// Aggregate monthly portfolio snapshots by account.
	monthlySnapshots, err := deps.db.ListMonthlySnapshotsForYear(ctx, year)
	if err == nil {
		accountTotals := make(map[string]int64)

		for _, snapshot := range monthlySnapshots {
			accountTotals[snapshot.AccountID] = snapshot.PortfolioValueCents
		}

		for accountID, total := range accountTotals {
			yearlyPortfolio := &database.YearlyPortfolioSummary{
				Year:                year,
				AccountID:           accountID,
				PortfolioValueCents: total,
			}
			_ = deps.db.UpsertYearlyPortfolioSummary(ctx, yearlyPortfolio)
		}
	}
	return nil
}
