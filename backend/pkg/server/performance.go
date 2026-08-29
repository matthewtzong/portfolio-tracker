package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
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

// Returns true when Plaid subtype indicates an external contribution (e.g. contribution, employer contribution).
func isContributionSubtype(subtype string) bool {
	s := strings.ToLower(strings.TrimSpace(subtype))
	return s == "contribution" || strings.Contains(s, "contribution")
}

// Classifies a Plaid investment transaction as an external cash flow.
// Plaid: positive amount = cash debited (out); negative = cash credited (in).
// Returns contribution cents (+ in, − out) and whether it is an external flow.
func classifyExternalCashFlow(txnType, subtype string, amountCents int64) (contributionCents int64, isExternal bool) {
	t := strings.ToLower(strings.TrimSpace(txnType))
	s := strings.ToLower(strings.TrimSpace(subtype))

	if isContributionSubtype(s) {
		// Buy+contribution (common for 401k): amount is positive (purchase outflow).
		if t == "buy" {
			return amountCents, true
		}
		// Cash/transfer contribution: invert Plaid sign (deposit is negative amount).
		return -amountCents, true
	}

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

// App data floor — matches frontend START_MONTH (2026-03). Never sync or use txns before this.
var investmentTxnHistoryFloor = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

const investmentTxnSyncOverlapDays = 7

// Syncs investment transactions from Plaid.
// First run (empty table): backfill from history floor → endDate.
// Later runs: only fetch from (latest stored date − overlap) → endDate.
func syncInvestmentTransactions(ctx context.Context, deps apiDependencies, endDate time.Time) (int, error) {
	if deps.db == nil || deps.plaidClient == nil {
		return 0, nil
	}

	items, err := deps.db.ListPlaidItems(ctx)
	if err != nil {
		return 0, err
	}

	endDay := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.UTC)
	startDay := investmentTxnHistoryFloor

	latest, err := deps.db.GetLatestInvestmentTransactionDate(ctx)
	if err != nil {
		return 0, err
	}
	if latest != nil {
		startDay = latest.AddDate(0, 0, -investmentTxnSyncOverlapDays)
		if startDay.Before(investmentTxnHistoryFloor) {
			startDay = investmentTxnHistoryFloor
		}
	}
	if startDay.After(endDay) {
		return 0, nil
	}

	startStr := startDay.Format("2006-01-02")
	endStr := endDay.Format("2006-01-02")
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
			if date.Before(investmentTxnHistoryFloor) {
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

// Resolves portfolio value on or nearest to date.
// Prefers sum of daily_holdings (real positions) over snapshots, which may have been
// gap-filled with live account balances on historical dates.
func resolvePortfolioValueCents(ctx context.Context, db *database.Client, date time.Time) (int64, time.Time, error) {
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)

	// Prefer exact holdings sum for that day.
	if sum, ok, err := sumDailyHoldingsOnDate(ctx, db, day); err != nil {
		return 0, time.Time{}, err
	} else if ok {
		return sum, day, nil
	}

	// Nearest holdings date within ±7 days.
	windowStart := day.AddDate(0, 0, -7)
	windowEnd := day.AddDate(0, 0, 7)
	nearbyHoldings, err := db.ListDailyHoldings(ctx, windowStart, windowEnd)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(nearbyHoldings) > 0 {
		byDate := make(map[string]int64)
		for _, h := range nearbyHoldings {
			key := h.Date.Time.Format("2006-01-02")
			byDate[key] += h.ValueCents
		}
		var bestDate time.Time
		bestDist := int64(1 << 60)
		var bestSum int64
		for key, sum := range byDate {
			d, err := time.Parse("2006-01-02", key)
			if err != nil {
				continue
			}
			dist := daysBetweenAbs(d, day)
			if dist < bestDist {
				bestDist = dist
				bestDate = d
				bestSum = sum
			}
		}
		if !bestDate.IsZero() {
			return bestSum, bestDate, nil
		}
	}

	// Fall back to daily snapshots.
	dailies, err := db.ListDailySnapshots(ctx, day, day)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(dailies) > 0 {
		return dailies[0].PortfolioValueCents, dailies[0].Date.Time, nil
	}

	nearby, err := db.ListDailySnapshots(ctx, windowStart, windowEnd)
	if err != nil {
		return 0, time.Time{}, err
	}
	var best *database.DailySnapshot
	bestDist := int64(1 << 60)
	for i := range nearby {
		s := &nearby[i]
		dist := daysBetweenAbs(s.Date.Time, day)
		if dist < bestDist {
			bestDist = dist
			best = s
		}
	}
	if best != nil {
		return best.PortfolioValueCents, best.Date.Time, nil
	}

	// Fall back to monthly snapshots for the month containing date (EOM storage).
	sum, monthDate, err := sumMonthlyForCalendarMonth(ctx, db, day.Year(), day.Month())
	if err != nil {
		return 0, time.Time{}, err
	}
	if monthDate.IsZero() {
		prev := day.AddDate(0, -1, 0)
		sum, monthDate, err = sumMonthlyForCalendarMonth(ctx, db, prev.Year(), prev.Month())
		if err != nil {
			return 0, time.Time{}, err
		}
	}
	if monthDate.IsZero() {
		return 0, time.Time{}, nil
	}
	return sum, monthDate, nil
}

func sumDailyHoldingsOnDate(ctx context.Context, db *database.Client, day time.Time) (int64, bool, error) {
	holdings, err := db.ListDailyHoldings(ctx, day, day)
	if err != nil {
		return 0, false, err
	}
	if len(holdings) == 0 {
		return 0, false, nil
	}
	var sum int64
	for _, h := range holdings {
		sum += h.ValueCents
	}
	return sum, true, nil
}

// Returns sorted unique dates with portfolio data (holdings or daily snapshots).
func portfolioValueDates(ctx context.Context, db *database.Client, start, end time.Time) ([]time.Time, error) {
	byKey := make(map[string]time.Time)

	holdings, err := db.ListDailyHoldings(ctx, start, end)
	if err != nil {
		return nil, err
	}
	for _, h := range holdings {
		key := h.Date.Time.Format("2006-01-02")
		byKey[key] = time.Date(h.Date.Time.Year(), h.Date.Time.Month(), h.Date.Time.Day(), 0, 0, 0, 0, time.UTC)
	}

	snapshots, err := db.ListDailySnapshots(ctx, start, end)
	if err != nil {
		return nil, err
	}
	for _, s := range snapshots {
		key := s.Date.Time.Format("2006-01-02")
		if _, ok := byKey[key]; !ok {
			byKey[key] = time.Date(s.Date.Time.Year(), s.Date.Time.Month(), s.Date.Time.Day(), 0, 0, 0, 0, time.UTC)
		}
	}

	dates := make([]time.Time, 0, len(byKey))
	for _, d := range byKey {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Before(dates[j])
	})
	return dates, nil
}

// Portfolio value on an exact calendar day only (no nearest-day window).
func resolvePortfolioValueOnExactDate(ctx context.Context, db *database.Client, day time.Time) (int64, bool, error) {
	if sum, ok, err := sumDailyHoldingsOnDate(ctx, db, day); err != nil {
		return 0, false, err
	} else if ok {
		return sum, true, nil
	}
	dailies, err := db.ListDailySnapshots(ctx, day, day)
	if err != nil {
		return 0, false, err
	}
	if len(dailies) > 0 {
		return dailies[0].PortfolioValueCents, true, nil
	}
	return 0, false, nil
}

// Sums monthly_snapshots for a calendar month. Months are stored as last-day dates.
func sumMonthlyForCalendarMonth(ctx context.Context, db *database.Client, year int, month time.Month) (int64, time.Time, error) {
	first, last := calendarMonthBounds(year, month)
	monthlies, err := db.ListMonthlySnapshots(ctx, first, last)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(monthlies) == 0 {
		return 0, time.Time{}, nil
	}
	var sum int64
	var asOf time.Time
	for _, m := range monthlies {
		sum += m.PortfolioValueCents
		if asOf.IsZero() || m.Month.Time.After(asOf) {
			asOf = m.Month.Time
		}
	}
	return sum, asOf, nil
}

func calendarMonthBounds(year int, month time.Month) (first, last time.Time) {
	first = time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	last = time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
	return first, last
}

func daysBetweenAbs(a, b time.Time) int64 {
	hours := a.Sub(b).Hours()
	if hours < 0 {
		hours = -hours
	}
	return int64(hours / 24)
}

// Finds the earliest performance start: monthly first (long history), else daily
// holdings / daily snapshots (portfolio only exists in the current month).
func earliestPerformanceStart(ctx context.Context, db *database.Client) (time.Time, error) {
	earliestMonthly, err := db.GetEarliestMonthlySnapshot(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if earliestMonthly != nil {
		return earliestMonthly.Month.Time, nil
	}

	holdingsDate, err := db.GetEarliestDailyHoldingsDate(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if holdingsDate != nil {
		return *holdingsDate, nil
	}

	earliestDaily, err := db.GetEarliestDailySnapshot(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if earliestDaily != nil {
		return earliestDaily.Date.Time, nil
	}
	return time.Time{}, nil
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

type performanceSummaryJSON struct {
	DayOverDay     *performanceResponseJSON `json:"dayOverDay,omitempty"`
	MonthOverMonth *performanceResponseJSON `json:"monthOverMonth,omitempty"`
}

var (
	errInvalidPerformanceRange = errors.New("invalid performance range")
	errNoPortfolioSnapshots    = errors.New("no portfolio snapshots available")
	errNoPortfolioValueAtStart = errors.New("no portfolio value at start of range")
	errNoPortfolioValueAtEnd   = errors.New("no portfolio value at end of range")
)

func resolvePerformanceStartDate(rangeParam string, now, earliest, endDate time.Time, loc *time.Location) (time.Time, string, error) {
	switch rangeParam {
	case "1d", "day", "dod":
		startDate := endDate.AddDate(0, 0, -1)
		if startDate.Before(earliest) {
			startDate = earliest
		}
		return startDate, "1d", nil
	case "mom", "month":
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		startDate := firstOfMonth.AddDate(0, 0, -1)
		if startDate.Before(earliest) {
			startDate = earliest
		}
		return startDate, "mom", nil
	case "mtd":
		startDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		if startDate.Before(earliest) {
			startDate = earliest
		}
		return startDate, "mtd", nil
	case "ytd":
		startDate := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
		if startDate.Before(earliest) {
			startDate = earliest
		}
		return startDate, "ytd", nil
	case "1y", "1year", "year":
		startDate := endDate.AddDate(-1, 0, 0)
		if startDate.Before(earliest) {
			startDate = earliest
		}
		return startDate, "1y", nil
	case "all":
		return earliest, "all", nil
	default:
		return time.Time{}, "", errInvalidPerformanceRange
	}
}

func computePortfolioPerformance(ctx context.Context, db *database.Client, rangeParam string) (*performanceResponseJSON, error) {
	loc := GetLocalLocation()
	now := GetLocalNow()
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	earliest, err := earliestPerformanceStart(ctx, db)
	if err != nil {
		return nil, err
	}
	if earliest.IsZero() {
		return nil, errNoPortfolioSnapshots
	}
	earliest = time.Date(earliest.Year(), earliest.Month(), earliest.Day(), 0, 0, 0, 0, loc)

	startDate, normalizedRange, err := resolvePerformanceStartDate(rangeParam, now, earliest, endDate, loc)
	if err != nil {
		return nil, err
	}

	var startValue, endValue int64
	var actualStart, actualEnd time.Time

	if normalizedRange == "1d" {
		lookback := endDate.AddDate(0, 0, -30)
		dates, err := portfolioValueDates(ctx, db, lookback, endDate)
		if err != nil {
			return nil, err
		}
		if len(dates) < 2 {
			return nil, errNoPortfolioValueAtStart
		}
		actualStart = dates[len(dates)-2]
		actualEnd = dates[len(dates)-1]
		var ok bool
		startValue, ok, err = resolvePortfolioValueOnExactDate(ctx, db, actualStart)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNoPortfolioValueAtStart
		}
		endValue, ok, err = resolvePortfolioValueOnExactDate(ctx, db, actualEnd)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNoPortfolioValueAtEnd
		}
	} else if normalizedRange == "mom" {
		lookback := endDate.AddDate(0, -3, 0)
		dates, err := portfolioValueDates(ctx, db, lookback, endDate)
		if err != nil {
			return nil, err
		}
		if len(dates) < 1 {
			return nil, errNoPortfolioValueAtEnd
		}
		actualEnd = dates[len(dates)-1]
		var ok bool
		endValue, ok, err = resolvePortfolioValueOnExactDate(ctx, db, actualEnd)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNoPortfolioValueAtEnd
		}

		firstOfEndMonth := time.Date(actualEnd.Year(), actualEnd.Month(), 1, 0, 0, 0, 0, loc)
		prevMonthLast := firstOfEndMonth.AddDate(0, 0, -1)

		for i := len(dates) - 1; i >= 0; i-- {
			d := dates[i]
			if !d.After(prevMonthLast) {
				actualStart = d
				break
			}
		}
		if !actualStart.IsZero() {
			startValue, ok, err = resolvePortfolioValueOnExactDate(ctx, db, actualStart)
			if err != nil {
				return nil, err
			}
			if !ok {
				actualStart = time.Time{}
			}
		}
		if actualStart.IsZero() {
			startValue, actualStart, err = resolvePortfolioValueCents(ctx, db, prevMonthLast)
			if err != nil {
				return nil, err
			}
			if actualStart.IsZero() {
				monthlyValue, monthlyDate, mErr := sumMonthlyForCalendarMonth(ctx, db, prevMonthLast.Year(), prevMonthLast.Month())
				if mErr != nil || monthlyDate.IsZero() {
					return nil, errNoPortfolioValueAtStart
				}
				startValue = monthlyValue
				actualStart = monthlyDate
			}
		}
	} else {
		startValue, actualStart, err = resolvePortfolioValueCents(ctx, db, startDate)
		if err != nil {
			return nil, err
		}
		if actualStart.IsZero() && !startDate.Equal(earliest) {
			startValue, actualStart, err = resolvePortfolioValueCents(ctx, db, earliest)
			if err != nil {
				return nil, err
			}
		}
		if actualStart.IsZero() {
			return nil, errNoPortfolioValueAtStart
		}

		endValue, actualEnd, err = resolvePortfolioValueCents(ctx, db, endDate)
		if err != nil {
			return nil, err
		}
		if actualEnd.IsZero() {
			endValue, err = currentHoldingsTotalCents(ctx, db)
			if err != nil || endValue == 0 {
				return nil, errNoPortfolioValueAtEnd
			}
			actualEnd = endDate
		}
	}

	txns, err := db.ListInvestmentTransactions(ctx, actualStart, actualEnd)
	if err != nil {
		return nil, err
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
	return &performanceResponseJSON{
		Range:                 normalizedRange,
		StartDate:             actualStart.Format("2006-01-02"),
		EndDate:               actualEnd.Format("2006-01-02"),
		StartValueCents:       result.StartValueCents,
		EndValueCents:         result.EndValueCents,
		NetContributionsCents: result.NetContributionsCents,
		GainCents:             result.GainCents,
		ReturnBps:             result.ReturnBps,
		Method:                result.Method,
	}, nil
}

func writePerformanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidPerformanceRange):
		writeJSONError(w, http.StatusBadRequest, "range must be all, 1y, ytd, 1d, mom, or mtd")
	case errors.Is(err, errNoPortfolioSnapshots),
		errors.Is(err, errNoPortfolioValueAtStart),
		errors.Is(err, errNoPortfolioValueAtEnd):
		writeJSONError(w, http.StatusNotFound, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
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

	resp, err := computePortfolioPerformance(r.Context(), deps.db, rangeParam)
	if err != nil {
		writePerformanceError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func handleGetPortfolioPerformanceSummary(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	resp := performanceSummaryJSON{}
	if dayOverDay, err := computePortfolioPerformance(r.Context(), deps.db, "1d"); err == nil {
		resp.DayOverDay = dayOverDay
	}
	if monthOverMonth, err := computePortfolioPerformance(r.Context(), deps.db, "mom"); err == nil {
		resp.MonthOverMonth = monthOverMonth
	}

	if resp.DayOverDay == nil && resp.MonthOverMonth == nil {
		writeJSONError(w, http.StatusNotFound, "not enough snapshot or investment-transaction data yet")
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
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
