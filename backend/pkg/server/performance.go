package server

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
)

// External cash-flow subtypes from Plaid investment transactions.
// Buys/sells/dividends are intentionally excluded — they stay inside the account.
var externalCashFlowSubtypes = map[string]bool{
	"deposit":      true,
	"contribution": true,
	"withdrawal":   true,
	"distribution": true,
	"transfer":     true,
	"send":         true,
}

// A dated external cash flow for Modified Dietz (positive = money added to the account).
type cashFlow struct {
	Date   time.Time
	Amount int64 // cents; +deposit, -withdrawal
}

// Result of a Modified Dietz calculation.
type modifiedDietzResult struct {
	StartValueCents       int64
	EndValueCents         int64
	NetContributionsCents int64
	GainCents             int64
	ReturnBps             int64 // basis points (100 bps = 1%)
	Method                string
}

// Classifies a Plaid investment transaction as an external cash flow.
// Plaid: positive amount = cash debited (out); negative = cash credited (in).
// Returns contribution cents (+ in, − out) and whether it is an external flow.
func classifyExternalCashFlow(txnType, subtype string, amountCents int64) (contributionCents int64, isExternal bool) {
	t := strings.ToLower(strings.TrimSpace(txnType))
	s := strings.ToLower(strings.TrimSpace(subtype))

	if t == "buy" || t == "sell" || t == "cancel" || t == "fee" {
		return 0, false
	}
	if !externalCashFlowSubtypes[s] {
		return 0, false
	}
	if t != "cash" && t != "transfer" {
		return 0, false
	}
	// Invert Plaid sign: deposit (negative amount) → positive contribution.
	return -amountCents, true
}

// Computes Modified Dietz return over [startDate, endDate].
// flows should already be filtered to the period (exclusive of start boundary deposits on start day are included).
func modifiedDietz(startValue, endValue int64, startDate, endDate time.Time, flows []cashFlow) modifiedDietzResult {
	result := modifiedDietzResult{
		StartValueCents: startValue,
		EndValueCents:   endValue,
		Method:          "modified_dietz",
	}
	if !endDate.After(startDate) && !endDate.Equal(startDate) {
		return result
	}

	periodDays := endDate.Sub(startDate).Hours() / 24
	if periodDays < 1 {
		periodDays = 1
	}

	var netContrib int64
	var weighted float64
	for _, f := range flows {
		d := time.Date(f.Date.Year(), f.Date.Month(), f.Date.Day(), 0, 0, 0, 0, time.UTC)
		s := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.UTC)
		e := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.UTC)
		if d.Before(s) || d.After(e) {
			continue
		}
		netContrib += f.Amount
		daysRemaining := e.Sub(d).Hours() / 24
		weight := daysRemaining / periodDays
		if weight < 0 {
			weight = 0
		}
		if weight > 1 {
			weight = 1
		}
		weighted += float64(f.Amount) * weight
	}

	result.NetContributionsCents = netContrib
	result.GainCents = endValue - startValue - netContrib
	denom := float64(startValue) + weighted
	if denom != 0 {
		result.ReturnBps = int64(math.Round(float64(result.GainCents) / denom * 10000))
	}
	return result
}

// Syncs investment transactions from Plaid for all items (up to ~24 months back).
func syncInvestmentTransactions(ctx context.Context, deps apiDependencies, endDate time.Time) (int, error) {
	if deps.db == nil || deps.plaidClient == nil {
		return 0, nil
	}

	items, err := deps.db.ListPlaidItems(ctx)
	if err != nil {
		return 0, err
	}

	startDate := endDate.AddDate(-2, 0, 0)
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	upserted := 0

	for _, item := range items {
		if item.AccessToken == "" || item.AccessToken == "manual" {
			continue
		}
		txns, securities, err := deps.plaidClient.GetInvestmentTransactions(ctx, item.AccessToken, startStr, endStr)
		if err != nil {
			log.Printf("cron: investment transactions sync failed for item %s: %v", item.ItemID, err)
			continue
		}

		tickerBySecurity := make(map[string]string)
		for _, sec := range securities {
			var ticker string
			if sec.Ticker != nil && *sec.Ticker != "" {
				ticker = *sec.Ticker
			} else if sec.Name != nil {
				ticker = *sec.Name
			}
			if ticker != "" {
				tickerBySecurity[sec.SecurityID] = ticker
			}
		}

		batch := make([]database.InvestmentTransaction, 0, len(txns))
		for _, t := range txns {
			date, err := time.Parse("2006-01-02", t.Date)
			if err != nil {
				continue
			}
			row := database.InvestmentTransaction{
				PlaidInvestmentTransactionID: t.InvestmentTransactionID,
				AccountID:                    t.AccountID,
				Date:                         database.DateOnly{Time: date},
				Name:                         t.Name,
				Type:                         t.Type,
				Subtype:                      t.Subtype,
				AmountCents:                  int64(math.Round(t.Amount * 100)),
				SecurityID:                   t.SecurityID,
			}
			qty := t.Quantity
			price := t.Price
			row.Quantity = &qty
			row.Price = &price
			if t.SecurityID != nil {
				if sym, ok := tickerBySecurity[*t.SecurityID]; ok {
					s := sym
					row.Symbol = &s
				}
			}
			batch = append(batch, row)
		}

		const chunk = 200
		for i := 0; i < len(batch); i += chunk {
			end := i + chunk
			if end > len(batch) {
				end = len(batch)
			}
			if err := deps.db.UpsertInvestmentTransactions(ctx, batch[i:end]); err != nil {
				log.Printf("cron: upsert investment transactions failed for item %s: %v", item.ItemID, err)
				continue
			}
			upserted += end - i
		}
	}
	return upserted, nil
}

// Resolves portfolio value on or nearest to date using daily then monthly snapshots.
func resolvePortfolioValueCents(ctx context.Context, db *database.Client, date time.Time) (int64, time.Time, error) {
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)

	// Prefer exact daily snapshot.
	dailies, err := db.ListDailySnapshots(ctx, day, day)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(dailies) > 0 {
		return dailies[0].PortfolioValueCents, dailies[0].Date.Time, nil
	}

	// Nearest daily within ±7 days (prefer on/after, then before).
	windowStart := day.AddDate(0, 0, -7)
	windowEnd := day.AddDate(0, 0, 7)
	nearby, err := db.ListDailySnapshots(ctx, windowStart, windowEnd)
	if err != nil {
		return 0, time.Time{}, err
	}
	var best *database.DailySnapshot
	bestDist := int64(1 << 60)
	for i := range nearby {
		s := &nearby[i]
		dist := absInt64(s.Date.Time.Sub(day).Hours() / 24)
		if dist < bestDist {
			bestDist = int64(dist)
			best = s
		}
	}
	if best != nil {
		return best.PortfolioValueCents, best.Date.Time, nil
	}

	// Fall back to monthly EOM for the month containing date (sum across accounts).
	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthlies, err := db.ListMonthlySnapshots(ctx, monthStart, monthStart)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(monthlies) == 0 {
		// Try previous month EOM.
		prev := monthStart.AddDate(0, -1, 0)
		monthlies, err = db.ListMonthlySnapshots(ctx, prev, prev)
		if err != nil {
			return 0, time.Time{}, err
		}
		monthStart = prev
	}
	var sum int64
	for _, m := range monthlies {
		sum += m.PortfolioValueCents
	}
	if len(monthlies) == 0 {
		return 0, time.Time{}, nil
	}
	return sum, monthStart, nil
}

func absInt64(v float64) int64 {
	if v < 0 {
		return int64(-v)
	}
	return int64(v)
}

// Finds the earliest date we can use as all-time start (daily preferred, else monthly).
func earliestPerformanceStart(ctx context.Context, db *database.Client) (time.Time, error) {
	earliestDaily, err := db.GetEarliestDailySnapshot(ctx)
	if err != nil {
		return time.Time{}, err
	}
	earliestMonthly, err := db.GetEarliestMonthlySnapshot(ctx)
	if err != nil {
		return time.Time{}, err
	}
	switch {
	case earliestDaily != nil && earliestMonthly != nil:
		d := earliestDaily.Date.Time
		m := earliestMonthly.Month.Time
		if d.Before(m) {
			return d, nil
		}
		return m, nil
	case earliestDaily != nil:
		return earliestDaily.Date.Time, nil
	case earliestMonthly != nil:
		return earliestMonthly.Month.Time, nil
	default:
		return time.Time{}, nil
	}
}

type performanceResponseJSON struct {
	Range                 string `json:"range"`
	StartDate             string `json:"startDate"`
	EndDate               string `json:"endDate"`
	StartValueCents       int64  `json:"startValueCents"`
	EndValueCents         int64  `json:"endValueCents"`
	NetContributionsCents int64  `json:"netContributionsCents"`
	GainCents             int64  `json:"gainCents"`
	ReturnBps             int64  `json:"returnBps"`
	Method                string `json:"method"`
}

func handleGetPortfolioPerformance(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	rangeParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if rangeParam == "" {
		rangeParam = "all"
	}

	loc := GetLocalLocation()
	now := GetLocalNow()
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	var startDate time.Time
	switch rangeParam {
	case "ytd":
		startDate = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	case "all":
		earliest, err := earliestPerformanceStart(r.Context(), deps.db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to resolve start date: "+err.Error())
			return
		}
		if earliest.IsZero() {
			writeJSONError(w, http.StatusNotFound, "no portfolio snapshots available")
			return
		}
		startDate = earliest.In(loc)
	default:
		writeJSONError(w, http.StatusBadRequest, "range must be all or ytd")
		return
	}

	startValue, actualStart, err := resolvePortfolioValueCents(r.Context(), deps.db, startDate)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to resolve start value: "+err.Error())
		return
	}
	if actualStart.IsZero() {
		writeJSONError(w, http.StatusNotFound, "no portfolio value at start of range")
		return
	}

	endValue, actualEnd, err := resolvePortfolioValueCents(r.Context(), deps.db, endDate)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to resolve end value: "+err.Error())
		return
	}
	if actualEnd.IsZero() {
		// Fall back to latest holdings sum if no snapshot for today.
		endValue, err = currentHoldingsTotalCents(r.Context(), deps.db)
		if err != nil || endValue == 0 {
			writeJSONError(w, http.StatusNotFound, "no portfolio value at end of range")
			return
		}
		actualEnd = endDate
	}

	txns, err := deps.db.ListInvestmentTransactions(r.Context(), actualStart, actualEnd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list investment transactions: "+err.Error())
		return
	}

	flows := make([]cashFlow, 0)
	for _, t := range txns {
		contrib, ok := classifyExternalCashFlow(t.Type, t.Subtype, t.AmountCents)
		if !ok || contrib == 0 {
			continue
		}
		flows = append(flows, cashFlow{Date: t.Date.Time, Amount: contrib})
	}

	result := modifiedDietz(startValue, endValue, actualStart, actualEnd, flows)
	_ = json.NewEncoder(w).Encode(performanceResponseJSON{
		Range:                 rangeParam,
		StartDate:             actualStart.Format("2006-01-02"),
		EndDate:               actualEnd.Format("2006-01-02"),
		StartValueCents:       result.StartValueCents,
		EndValueCents:         result.EndValueCents,
		NetContributionsCents: result.NetContributionsCents,
		GainCents:             result.GainCents,
		ReturnBps:             result.ReturnBps,
		Method:                result.Method,
	})
}

func currentHoldingsTotalCents(ctx context.Context, db *database.Client) (int64, error) {
	latest, err := db.GetLatestDailyHoldingsDate(ctx)
	if err != nil || latest == nil {
		return 0, err
	}
	holdings, err := db.ListDailyHoldings(ctx, *latest, *latest)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, h := range holdings {
		sum += h.ValueCents
	}
	return sum, nil
}
