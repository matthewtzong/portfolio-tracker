package server

import (
	"log"
	"math"
	"net/http"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
)

// Updates daily and monthly portfolio snapshots for a given date from daily_holdings.
// Used by the nightly Plaid cron (and Fidelity CSV handlers if re-enabled).
func updatePortfolioSnapshots(r *http.Request, deps apiDependencies, date time.Time) error {
	// Fetch all daily holdings for this date.
	holdings, err := deps.db.ListDailyHoldings(r.Context(), date, date)
	if err != nil {
		return err
	}

	// Calculate total value for this date across ALL accounts (Plaid + Manual).
	var totalValueCents int64
	accountTotals := make(map[string]int64)
	for _, holding := range holdings {
		totalValueCents += holding.ValueCents
		accountTotals[holding.AccountID] += holding.ValueCents
	}

	// Gap-fill missing investment accounts with current balance only within the last 7 days.
	// Covers missed/late cron runs without injecting today's balances into older history.
	now := GetLocalNow()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation())
	snapDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, GetLocalLocation())
	allowGapFill := !snapDay.Before(today.AddDate(0, 0, -7))

	allAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		log.Printf("investments: failed to list plaid accounts for gap-filling: %v", err)
	} else if allowGapFill {
		for _, account := range allAccounts {
			if isPlaidInvestment(account.Type) {
				_, exists := accountTotals[account.AccountID]
				if !exists {
					cents := int64(math.Round(account.CurrentBalance * 100))
					totalValueCents += cents
					accountTotals[account.AccountID] = cents
				}
			}
		}
	}

	// Upsert daily snapshot.
	snapshot := &database.DailySnapshot{
		Date:                database.DateOnly{Time: date},
		PortfolioValueCents: totalValueCents,
	}
	err = deps.db.UpsertDailySnapshot(r.Context(), snapshot)
	if err != nil {
		return err
	}

	// Update current_balance for each Fidelity manual account that has holdings totals.
	manualItem := &database.PlaidItem{
		ItemID:          FidelityManualItemID,
		InstitutionName: stringPtr(FidelityInstitutionName),
		AccessToken:     "manual",
		Status:          "OK",
		LastUpdated:     date,
	}
	updatedAnyFidelity := false
	accountsToUpsert := make([]database.PlaidAccount, 0, 2)
	for _, fidelityAccount := range fidelityManualAccounts {
		total, ok := accountTotals[fidelityAccount.AccountID]
		if !ok {
			continue
		}
		updatedAnyFidelity = true
		accountsToUpsert = append(accountsToUpsert, database.PlaidAccount{
			PlaidItemID:    FidelityManualItemID,
			AccountID:      fidelityAccount.AccountID,
			Name:           fidelityAccount.DisplayName,
			Type:           "investment",
			CurrentBalance: float64(total) / 100.0,
		})
	}
	if updatedAnyFidelity {
		_ = deps.db.UpsertPlaidItem(r.Context(), manualItem)
		_ = deps.db.UpsertPlaidAccounts(r.Context(), accountsToUpsert)
	}

	// Update monthly snapshots if it's month-end.
	year, month, day := date.Date()
	lastDayOfMonthDate := time.Date(year, month+1, 0, 0, 0, 0, 0, GetLocalLocation())
	lastDayOfMonth := lastDayOfMonthDate.Day()

	if day == lastDayOfMonth {
		// Record monthly snapshots if the account existed on or before this date.
		for accountID, total := range accountTotals {
			var accountCreatedAt *time.Time
			for _, acc := range allAccounts {
				if acc.AccountID == accountID {
					accountCreatedAt = acc.CreatedAt
					break
				}
			}

			// Skip any accounts created after this month.
			if accountCreatedAt != nil && accountCreatedAt.After(lastDayOfMonthDate) {
				continue
			}

			monthlySnapshot := &database.MonthlySnapshot{
				Month:               database.DateOnly{Time: lastDayOfMonthDate},
				AccountID:           accountID,
				PortfolioValueCents: total,
			}
			err := deps.db.UpsertMonthlySnapshot(r.Context(), monthlySnapshot)
			if err != nil {
				log.Printf("investments: failed to upsert monthly snapshot for %s: %v", accountID, err)
			}
		}
		// Update monthly net worth.
		_ = maybeWriteMonthlyNetWorth(r, deps, date, totalValueCents)
	}

	return nil
}
