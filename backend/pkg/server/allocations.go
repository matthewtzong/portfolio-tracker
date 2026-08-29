package server

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
)

const (
	assetClassCash  = "cash"
	assetClassETF   = "etf"
	assetClassStock = "stock"
	assetClassOther = "other"

	sourcePlaid     = "plaid"
	sourceHeuristic = "heuristic"
	sourceUser      = "user"

	targetKindTicker     = "ticker"
	targetKindAssetClass = "asset_class"

	defaultDriftWarnBps      = 500
	defaultSingleStockMaxBps = 1000

	// GOOG may exceed the single-stock cap without triggering a warning.
	singleStockCapExemptSymbol = "GOOG"
)

// Maps Plaid security type (+ sweep ticker heuristics) to our asset_class and source.
func mapPlaidTypeToAssetClass(plaidType, symbol string) (assetClass, source string) {
	normalizedSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	if normalizedSymbol == "SPAXX" || normalizedSymbol == "FCASH" {
		return assetClassCash, sourceHeuristic
	}

	switch strings.ToLower(strings.TrimSpace(plaidType)) {
	case "cash":
		return assetClassCash, sourcePlaid
	case "etf", "mutual fund":
		return assetClassETF, sourcePlaid
	case "equity":
		return assetClassStock, sourcePlaid
	default:
		return assetClassOther, sourcePlaid
	}
}

// Resolves asset class for a symbol; unknown symbols default to other.
func resolveAssetClass(symbol string, classBySymbol map[string]string) string {
	normalized := strings.ToUpper(strings.TrimSpace(symbol))
	if normalized == "SPAXX" || normalized == "FCASH" {
		return assetClassCash
	}
	if class, ok := classBySymbol[normalized]; ok && class != "" {
		return class
	}
	if class, ok := classBySymbol[symbol]; ok && class != "" {
		return class
	}
	return assetClassOther
}

// Holding row used when building the overview.
type overviewHoldingInput struct {
	AccountID   string
	AccountName string
	Symbol      string
	Quantity    float64
	ValueCents  int64
	Date        string
}

// Target input for overview matching.
type overviewTargetInput struct {
	Kind      string
	Key       string
	TargetBps int
}

// Settings for warning thresholds.
type overviewSettingsInput struct {
	DriftWarnBps      int
	SingleStockMaxBps int
}

// Account breakdown for an aggregated symbol.
type overviewAccountJSON struct {
	AccountID   string  `json:"accountId"`
	AccountName string  `json:"accountName"`
	Quantity    float64 `json:"quantity"`
	ValueCents  int64   `json:"valueCents"`
}

// Aggregated symbol allocation row.
type overviewSymbolJSON struct {
	Symbol     string                `json:"symbol"`
	AssetClass string                `json:"assetClass"`
	Quantity   float64               `json:"quantity"`
	ValueCents int64                 `json:"valueCents"`
	WeightBps  int                   `json:"weightBps"`
	Accounts   []overviewAccountJSON `json:"accounts"`
}

// Asset-class / target-bucket row.
type overviewBucketJSON struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ValueCents int64  `json:"valueCents"`
	WeightBps  int    `json:"weightBps"`
	TargetBps  *int   `json:"targetBps,omitempty"`
}

// Dollar and percent change.
type overviewDeltaJSON struct {
	AbsoluteCents int64  `json:"absoluteCents"`
	PercentBps    *int   `json:"percentBps,omitempty"`
	FromDate      string `json:"fromDate,omitempty"`
	ToDate        string `json:"toDate,omitempty"`
}

// Top mover row.
type overviewMoverJSON struct {
	Symbol        string `json:"symbol"`
	AbsoluteCents int64  `json:"absoluteCents"`
	PercentBps    *int   `json:"percentBps,omitempty"`
	FromCents     int64  `json:"fromCents"`
	ToCents       int64  `json:"toCents"`
}

// Top movers for a time window.
type overviewMoversPeriodJSON struct {
	FromDate string              `json:"fromDate,omitempty"`
	ToDate   string              `json:"toDate,omitempty"`
	Gainers  []overviewMoverJSON `json:"gainers"`
	Losers   []overviewMoverJSON `json:"losers"`
}

// Warning shown on the allocations page.
type overviewWarningJSON struct {
	Type     string   `json:"type"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Symbols  []string `json:"symbols,omitempty"`
	Key      string   `json:"key,omitempty"`
}

// Full overview API response.
type portfolioOverviewResponse struct {
	TotalValueCents   int64                    `json:"totalValueCents"`
	DayOverDay        *overviewDeltaJSON       `json:"dayOverDay,omitempty"`
	MonthOverMonth    *overviewDeltaJSON       `json:"monthOverMonth,omitempty"`
	BySymbol          []overviewSymbolJSON     `json:"bySymbol"`
	ByBucket          []overviewBucketJSON     `json:"byBucket"`
	ByAssetClass      []overviewBucketJSON     `json:"byAssetClass"`
	MoversDay         overviewMoversPeriodJSON `json:"moversDay"`
	MoversWeek        overviewMoversPeriodJSON `json:"moversWeek"`
	Warnings          []overviewWarningJSON    `json:"warnings"`
	TargetsSumBps     int                      `json:"targetsSumBps"`
	TargetsComplete   bool                     `json:"targetsComplete"`
	DriftWarnBps      int                      `json:"driftWarnBps"`
	SingleStockMaxBps int                      `json:"singleStockMaxBps"`
}

// Pure overview builder (unit-tested).
func buildPortfolioOverview(
	current []overviewHoldingInput,
	priorDay []overviewHoldingInput,
	oldestDay []overviewHoldingInput,
	classBySymbol map[string]string,
	dailySnapshots []database.DailySnapshot,
	monthlyTotals []SnapshotDataPoint,
	targets []overviewTargetInput,
	settings overviewSettingsInput,
) portfolioOverviewResponse {
	if settings.DriftWarnBps <= 0 {
		settings.DriftWarnBps = defaultDriftWarnBps
	}
	if settings.SingleStockMaxBps <= 0 {
		settings.SingleStockMaxBps = defaultSingleStockMaxBps
	}

	bySymbol := aggregateHoldingsBySymbol(current, classBySymbol)
	invested := int64(0)
	for _, row := range bySymbol {
		if row.AssetClass == assetClassCash {
			continue
		}
		invested += row.ValueCents
	}

	// Weight and drop cash from the allocation list.
	investedSymbols := make([]overviewSymbolJSON, 0, len(bySymbol))
	for _, row := range bySymbol {
		if row.AssetClass == assetClassCash {
			continue
		}
		row.WeightBps = weightBps(row.ValueCents, invested)
		investedSymbols = append(investedSymbols, row)
	}
	sort.Slice(investedSymbols, func(i, j int) bool {
		if investedSymbols[i].ValueCents == investedSymbols[j].ValueCents {
			return investedSymbols[i].Symbol < investedSymbols[j].Symbol
		}
		return investedSymbols[i].ValueCents > investedSymbols[j].ValueCents
	})

	byAssetClass := aggregateByAssetClass(investedSymbols, invested)
	byBucket, bucketActualBps := matchTargetBuckets(investedSymbols, invested, targets)

	resp := portfolioOverviewResponse{
		TotalValueCents:   invested,
		BySymbol:          investedSymbols,
		ByBucket:          byBucket,
		ByAssetClass:      byAssetClass,
		MoversDay:         overviewMoversPeriodJSON{Gainers: []overviewMoverJSON{}, Losers: []overviewMoverJSON{}},
		MoversWeek:        overviewMoversPeriodJSON{Gainers: []overviewMoverJSON{}, Losers: []overviewMoverJSON{}},
		Warnings:          []overviewWarningJSON{},
		DriftWarnBps:      settings.DriftWarnBps,
		SingleStockMaxBps: settings.SingleStockMaxBps,
	}

	resp.DayOverDay = deltaFromSnapshots(dailySnapshots)
	resp.MonthOverMonth = deltaFromMonthlyPoints(monthlyTotals)

	_ = oldestDay

	targetsSum := 0
	for _, t := range targets {
		targetsSum += t.TargetBps
	}
	resp.TargetsSumBps = targetsSum
	resp.TargetsComplete = len(targets) == 0 || targetsSum == 10000

	resp.Warnings = append(resp.Warnings, buildWarnings(
		investedSymbols,
		bucketActualBps,
		targets,
		settings,
		resp.TargetsComplete,
		targetsSum,
	)...)

	return resp
}

func aggregateHoldingsBySymbol(holdings []overviewHoldingInput, classBySymbol map[string]string) []overviewSymbolJSON {
	type agg struct {
		symbol     string
		assetClass string
		quantity   float64
		valueCents int64
		accounts   map[string]*overviewAccountJSON
	}
	bySymbol := make(map[string]*agg)

	for _, h := range holdings {
		symbol := strings.TrimSpace(h.Symbol)
		if symbol == "" {
			continue
		}
		key := strings.ToUpper(symbol)
		row, ok := bySymbol[key]
		if !ok {
			row = &agg{
				symbol:     key,
				assetClass: resolveAssetClass(key, classBySymbol),
				accounts:   make(map[string]*overviewAccountJSON),
			}
			bySymbol[key] = row
		}
		row.quantity += h.Quantity
		row.valueCents += h.ValueCents
		acc, ok := row.accounts[h.AccountID]
		if !ok {
			acc = &overviewAccountJSON{
				AccountID:   h.AccountID,
				AccountName: h.AccountName,
			}
			row.accounts[h.AccountID] = acc
		}
		acc.Quantity += h.Quantity
		acc.ValueCents += h.ValueCents
	}

	out := make([]overviewSymbolJSON, 0, len(bySymbol))
	for _, row := range bySymbol {
		accounts := make([]overviewAccountJSON, 0, len(row.accounts))
		for _, a := range row.accounts {
			accounts = append(accounts, *a)
		}
		sort.Slice(accounts, func(i, j int) bool {
			return accounts[i].AccountName < accounts[j].AccountName
		})
		out = append(out, overviewSymbolJSON{
			Symbol:     row.symbol,
			AssetClass: row.assetClass,
			Quantity:   row.quantity,
			ValueCents: row.valueCents,
			Accounts:   accounts,
		})
	}
	return out
}

func weightBps(part, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(float64(part) * 10000.0 / float64(total)))
}

func aggregateByAssetClass(symbols []overviewSymbolJSON, invested int64) []overviewBucketJSON {
	sums := map[string]int64{
		assetClassETF:   0,
		assetClassStock: 0,
		assetClassOther: 0,
	}
	for _, s := range symbols {
		sums[s.AssetClass] += s.ValueCents
	}
	order := []string{assetClassETF, assetClassStock, assetClassOther}
	labels := map[string]string{
		assetClassETF:   "ETFs",
		assetClassStock: "Single stocks",
		assetClassOther: "Other",
	}
	out := make([]overviewBucketJSON, 0, len(order))
	for _, key := range order {
		value := sums[key]
		if value == 0 {
			continue
		}
		out = append(out, overviewBucketJSON{
			Key:        key,
			Label:      labels[key],
			ValueCents: value,
			WeightBps:  weightBps(value, invested),
		})
	}
	return out
}

func matchTargetBuckets(
	symbols []overviewSymbolJSON,
	invested int64,
	targets []overviewTargetInput,
) ([]overviewBucketJSON, map[string]int) {
	tickerTargets := make(map[string]int)
	classTargets := make(map[string]int)
	for _, t := range targets {
		key := strings.ToUpper(strings.TrimSpace(t.Key))
		if t.Kind == targetKindTicker {
			tickerTargets[key] = t.TargetBps
		} else if t.Kind == targetKindAssetClass {
			classTargets[strings.ToLower(strings.TrimSpace(t.Key))] = t.TargetBps
		}
	}

	consumed := make(map[string]bool)
	actualBps := make(map[string]int)
	buckets := make([]overviewBucketJSON, 0)

	// Named ticker targets first.
	tickerKeys := make([]string, 0, len(tickerTargets))
	for k := range tickerTargets {
		tickerKeys = append(tickerKeys, k)
	}
	sort.Strings(tickerKeys)

	valueBySymbol := make(map[string]overviewSymbolJSON)
	for _, s := range symbols {
		valueBySymbol[s.Symbol] = s
	}

	for _, key := range tickerKeys {
		target := tickerTargets[key]
		value := int64(0)
		if s, ok := valueBySymbol[key]; ok {
			value = s.ValueCents
			consumed[key] = true
		}
		bps := weightBps(value, invested)
		actualBps["ticker:"+key] = bps
		tb := target
		buckets = append(buckets, overviewBucketJSON{
			Key:        key,
			Label:      key,
			ValueCents: value,
			WeightBps:  bps,
			TargetBps:  &tb,
		})
	}

	// Leftover by asset class.
	leftover := map[string]int64{
		assetClassETF:   0,
		assetClassStock: 0,
		assetClassOther: 0,
	}
	for _, s := range symbols {
		if consumed[s.Symbol] {
			continue
		}
		leftover[s.AssetClass] += s.ValueCents
	}

	classOrder := []string{assetClassETF, assetClassStock, assetClassOther}
	classLabels := map[string]string{
		assetClassETF:   "Other ETFs",
		assetClassStock: "Single stocks",
		assetClassOther: "Other",
	}
	for _, class := range classOrder {
		value := leftover[class]
		target, hasTarget := classTargets[class]
		if value == 0 && !hasTarget {
			continue
		}
		bps := weightBps(value, invested)
		actualBps["asset_class:"+class] = bps
		bucket := overviewBucketJSON{
			Key:        class,
			Label:      classLabels[class],
			ValueCents: value,
			WeightBps:  bps,
		}
		if hasTarget {
			tb := target
			bucket.TargetBps = &tb
		}
		buckets = append(buckets, bucket)
	}

	// If no targets configured, fall back to asset-class buckets for the pie.
	if len(targets) == 0 {
		return aggregateByAssetClass(symbols, invested), actualBps
	}
	return buckets, actualBps
}

func deltaFromSnapshots(snapshots []database.DailySnapshot) *overviewDeltaJSON {
	if len(snapshots) < 2 {
		return nil
	}
	sorted := make([]database.DailySnapshot, len(snapshots))
	copy(sorted, snapshots)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Date.Time.Before(sorted[j].Date.Time)
	})
	from := sorted[len(sorted)-2]
	to := sorted[len(sorted)-1]
	return makeDelta(from.PortfolioValueCents, to.PortfolioValueCents, from.Date.Format(dateLayout), to.Date.Format(dateLayout))
}

func deltaFromMonthlyPoints(points []SnapshotDataPoint) *overviewDeltaJSON {
	if len(points) < 2 {
		return nil
	}
	sorted := make([]SnapshotDataPoint, len(points))
	copy(sorted, points)
	sortSnapshotDataPoints(sorted)
	from := sorted[len(sorted)-2]
	to := sorted[len(sorted)-1]
	return makeDelta(from.PortfolioValueCents, to.PortfolioValueCents, from.Date, to.Date)
}

func makeDelta(fromCents, toCents int64, fromDate, toDate string) *overviewDeltaJSON {
	delta := &overviewDeltaJSON{
		AbsoluteCents: toCents - fromCents,
		FromDate:      fromDate,
		ToDate:        toDate,
	}
	if fromCents != 0 {
		bps := int(math.Round(float64(toCents-fromCents) * 10000.0 / float64(fromCents)))
		delta.PercentBps = &bps
	}
	return delta
}

func sumHoldingsBySymbol(holdings []overviewHoldingInput, classBySymbol map[string]string) map[string]int64 {
	sums := make(map[string]int64)
	for _, h := range holdings {
		symbol := strings.ToUpper(strings.TrimSpace(h.Symbol))
		if symbol == "" {
			continue
		}
		if resolveAssetClass(symbol, classBySymbol) == assetClassCash {
			continue
		}
		sums[symbol] += h.ValueCents
	}
	return sums
}

func computeMovers(prior, current []overviewHoldingInput, classBySymbol map[string]string) (gainers, losers []overviewMoverJSON) {
	from := sumHoldingsBySymbol(prior, classBySymbol)
	to := sumHoldingsBySymbol(current, classBySymbol)

	movers := make([]overviewMoverJSON, 0)
	for symbol, toCents := range to {
		fromCents, ok := from[symbol]
		if !ok || fromCents == 0 {
			continue
		}
		abs := toCents - fromCents
		bps := int(math.Round(float64(abs) * 10000.0 / float64(fromCents)))
		movers = append(movers, overviewMoverJSON{
			Symbol:        symbol,
			AbsoluteCents: abs,
			PercentBps:    &bps,
			FromCents:     fromCents,
			ToCents:       toCents,
		})
	}

	gainersAll := make([]overviewMoverJSON, 0)
	losersAll := make([]overviewMoverJSON, 0)
	for _, m := range movers {
		if m.PercentBps == nil {
			continue
		}
		if *m.PercentBps > 0 {
			gainersAll = append(gainersAll, m)
		} else if *m.PercentBps < 0 {
			losersAll = append(losersAll, m)
		}
	}

	sort.Slice(gainersAll, func(i, j int) bool {
		return *gainersAll[i].PercentBps > *gainersAll[j].PercentBps
	})
	sort.Slice(losersAll, func(i, j int) bool {
		return *losersAll[i].PercentBps < *losersAll[j].PercentBps
	})

	gainers = make([]overviewMoverJSON, 0, 5)
	losers = make([]overviewMoverJSON, 0, 5)
	for i := 0; i < len(gainersAll) && i < 5; i++ {
		gainers = append(gainers, gainersAll[i])
	}
	for i := 0; i < len(losersAll) && i < 5; i++ {
		losers = append(losers, losersAll[i])
	}
	return gainers, losers
}

func buildMoversPeriod(
	prior, current []overviewHoldingInput,
	fromDate, toDate string,
	classBySymbol map[string]string,
) overviewMoversPeriodJSON {
	gainers, losers := computeMovers(prior, current, classBySymbol)
	return overviewMoversPeriodJSON{
		FromDate: fromDate,
		ToDate:   toDate,
		Gainers:  gainers,
		Losers:   losers,
	}
}

const minWeekMoverSpanDays = 5

// findWeekComparisonDate returns the snapshot date closest to one week before latest.
func findWeekComparisonDate(dates []string) (string, bool) {
	if len(dates) < 2 {
		return "", false
	}
	latestStr := dates[len(dates)-1]
	latest, err := time.Parse(dateLayout, latestStr)
	if err != nil {
		return "", false
	}
	target := latest.AddDate(0, 0, -7)

	bestDate := ""
	var bestDiff time.Duration = -1
	for i := 0; i < len(dates)-1; i++ {
		d, err := time.Parse(dateLayout, dates[i])
		if err != nil || !d.Before(latest) {
			continue
		}
		if latest.Sub(d) < time.Duration(minWeekMoverSpanDays)*24*time.Hour {
			continue
		}
		diff := target.Sub(d)
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			bestDiff = diff
			bestDate = dates[i]
		}
	}
	if bestDate == "" {
		return "", false
	}
	return bestDate, true
}

func buildWarnings(
	symbols []overviewSymbolJSON,
	bucketActualBps map[string]int,
	targets []overviewTargetInput,
	settings overviewSettingsInput,
	targetsComplete bool,
	targetsSum int,
) []overviewWarningJSON {
	warnings := make([]overviewWarningJSON, 0)

	if len(targets) > 0 && !targetsComplete {
		warnings = append(warnings, overviewWarningJSON{
			Type:     "targets_sum",
			Severity: "info",
			Message:  "Target allocations do not sum to 100%.",
		})
		_ = targetsSum
	}

	for _, t := range targets {
		var key string
		var label string
		if t.Kind == targetKindTicker {
			key = "ticker:" + strings.ToUpper(t.Key)
			label = strings.ToUpper(t.Key)
		} else {
			class := strings.ToLower(t.Key)
			key = "asset_class:" + class
			label = assetClassDisplayLabel(class)
		}
		actual, ok := bucketActualBps[key]
		if !ok {
			actual = 0
		}
		drift := actual - t.TargetBps
		if drift < 0 {
			drift = -drift
		}
		if drift > settings.DriftWarnBps {
			warnings = append(warnings, overviewWarningJSON{
				Type:     "drift",
				Severity: "warning",
				Message:  label + " is off target by more than the drift threshold.",
				Key:      key,
			})
		}
	}

	overCapWarn := make([]string, 0)
	for _, s := range symbols {
		if s.AssetClass != assetClassStock {
			continue
		}
		if s.WeightBps <= settings.SingleStockMaxBps {
			continue
		}
		// GOOG is exempt from the single-stock cap warning.
		if strings.EqualFold(s.Symbol, singleStockCapExemptSymbol) {
			continue
		}
		overCapWarn = append(overCapWarn, s.Symbol)
	}
	sort.Strings(overCapWarn)
	capPercent := float64(settings.SingleStockMaxBps) / 100.0
	if len(overCapWarn) == 1 {
		warnings = append(warnings, overviewWarningJSON{
			Type:     "single_name_cap",
			Severity: "error",
			Message:  overCapWarn[0] + " is above the " + formatCapPercent(capPercent) + " single-stock concentration cap.",
			Symbols:  overCapWarn,
		})
	} else if len(overCapWarn) >= 2 {
		warnings = append(warnings, overviewWarningJSON{
			Type:     "single_name_cap",
			Severity: "error",
			Message:  joinTickerList(overCapWarn) + " are above the " + formatCapPercent(capPercent) + " single-stock concentration cap.",
			Symbols:  overCapWarn,
		})
	}

	return warnings
}

// Human-readable label for asset-class keys in warnings and UI.
func assetClassDisplayLabel(class string) string {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case assetClassETF:
		return "ETFs"
	case assetClassStock:
		return "Single stocks"
	case assetClassOther:
		return "Other"
	case assetClassCash:
		return "Cash"
	default:
		return class
	}
}

func formatCapPercent(pct float64) string {
	if pct == float64(int64(pct)) {
		return strconv.FormatInt(int64(pct), 10) + "%"
	}
	return strconv.FormatFloat(pct, 'f', 1, 64) + "%"
}

func joinTickerList(symbols []string) string {
	if len(symbols) == 0 {
		return ""
	}
	if len(symbols) == 1 {
		return symbols[0]
	}
	if len(symbols) == 2 {
		return symbols[0] + " and " + symbols[1]
	}
	return strings.Join(symbols[:len(symbols)-1], ", ") + ", and " + symbols[len(symbols)-1]
}

// Registers allocation / overview routes.
func registerAllocationRoutes(mux *http.ServeMux, deps apiDependencies) {
	mux.Handle("/api/portfolio/overview", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetPortfolioOverview(w, r, deps)
	})))

	mux.Handle("/api/portfolio/targets", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetAllocationTargets(w, r, deps)
		case http.MethodPut:
			handlePutAllocationTargets(w, r, deps)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
		}
	})))

	mux.Handle("/api/portfolio/securities", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetSecurities(w, r, deps)
		case http.MethodPut:
			handlePutSecurity(w, r, deps)
		default:
			methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
		}
	})))
}

func handleGetPortfolioOverview(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	plaidAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}
	accountNameMap := make(map[string]string)
	for _, a := range plaidAccounts {
		accountNameMap[a.AccountID] = a.Name
	}

	now := GetLocalNow()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, GetLocalLocation())
	dailyStart := dayStart.AddDate(0, 0, -30)

	holdingsHistory, err := deps.db.ListDailyHoldings(r.Context(), dailyStart, dayStart)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list holdings: "+err.Error())
		return
	}

	dates := uniqueHoldingDates(holdingsHistory)
	var current, prior, oldest []overviewHoldingInput
	if len(dates) >= 1 {
		current = filterHoldingsForDate(holdingsHistory, dates[len(dates)-1], accountNameMap)
	}
	if len(dates) >= 2 {
		prior = filterHoldingsForDate(holdingsHistory, dates[len(dates)-2], accountNameMap)
		oldest = filterHoldingsForDate(holdingsHistory, dates[0], accountNameMap)
	}

	securities, err := deps.db.ListSecurities(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list securities: "+err.Error())
		return
	}
	classBySymbol := make(map[string]string)
	for _, s := range securities {
		classBySymbol[strings.ToUpper(s.Symbol)] = s.AssetClass
	}

	dailySnapshots, err := deps.db.ListDailySnapshots(r.Context(), dailyStart, dayStart)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list daily snapshots: "+err.Error())
		return
	}

	monthlyStart := time.Date(now.Year()-2, 1, 1, 0, 0, 0, 0, GetLocalLocation())
	allMonthly, err := deps.db.ListMonthlySnapshots(r.Context(), monthlyStart, dayStart)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list monthly snapshots: "+err.Error())
		return
	}
	monthlySum := make(map[string]int64)
	for _, snapshot := range allMonthly {
		month := snapshot.Month.Format(dateLayout)
		monthlySum[month] += snapshot.PortfolioValueCents
	}
	monthlyPoints := make([]SnapshotDataPoint, 0, len(monthlySum))
	for month, sum := range monthlySum {
		monthlyPoints = append(monthlyPoints, SnapshotDataPoint{
			Date:                month,
			PortfolioValueCents: sum,
		})
	}
	sortSnapshotDataPoints(monthlyPoints)
	// Append current month from latest daily snapshot if missing.
	if len(dailySnapshots) > 0 {
		latestDaily := dailySnapshots[0]
		for _, s := range dailySnapshots {
			if s.Date.Time.After(latestDaily.Date.Time) {
				latestDaily = s
			}
		}
		// Use the latest daily's real date (not the 1st of the month) so MoM shows
		// e.g. Jul 31 → Aug 22 instead of Jul 31 → Aug 1.
		latestDailyDate := latestDaily.Date.Format(dateLayout)
		currentMonthPrefix := latestDailyDate[:7]
		hasCurrent := false
		for _, p := range monthlyPoints {
			if len(p.Date) >= 7 && p.Date[:7] == currentMonthPrefix {
				hasCurrent = true
				break
			}
		}
		if !hasCurrent {
			monthlyPoints = append(monthlyPoints, SnapshotDataPoint{
				Date:                latestDailyDate,
				PortfolioValueCents: latestDaily.PortfolioValueCents,
			})
			sortSnapshotDataPoints(monthlyPoints)
		}
	}

	dbTargets, err := deps.db.ListAllocationTargets(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list targets: "+err.Error())
		return
	}
	targets := make([]overviewTargetInput, 0, len(dbTargets))
	for _, t := range dbTargets {
		targets = append(targets, overviewTargetInput{
			Kind:      t.Kind,
			Key:       t.Key,
			TargetBps: t.TargetBps,
		})
	}

	settings := overviewSettingsInput{
		DriftWarnBps:      defaultDriftWarnBps,
		SingleStockMaxBps: defaultSingleStockMaxBps,
	}
	dbSettings, err := deps.db.GetAllocationSettings(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get settings: "+err.Error())
		return
	}
	if dbSettings != nil {
		settings.DriftWarnBps = dbSettings.DriftWarnBps
		settings.SingleStockMaxBps = dbSettings.SingleStockMaxBps
	}

	resp := buildPortfolioOverview(current, prior, oldest, classBySymbol, dailySnapshots, monthlyPoints, targets, settings)
	if len(dates) >= 2 {
		latest := dates[len(dates)-1]
		priorDate := dates[len(dates)-2]
		resp.MoversDay = buildMoversPeriod(prior, current, priorDate, latest, classBySymbol)
	}
	if weekDate, ok := findWeekComparisonDate(dates); ok && len(dates) >= 1 {
		latest := dates[len(dates)-1]
		weekPrior := filterHoldingsForDate(holdingsHistory, weekDate, accountNameMap)
		resp.MoversWeek = buildMoversPeriod(weekPrior, current, weekDate, latest, classBySymbol)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func uniqueHoldingDates(holdings []database.DailyHolding) []string {
	seen := make(map[string]bool)
	dates := make([]string, 0)
	for _, h := range holdings {
		d := h.Date.Format(dateLayout)
		if !seen[d] {
			seen[d] = true
			dates = append(dates, d)
		}
	}
	sort.Strings(dates)
	return dates
}

func filterHoldingsForDate(holdings []database.DailyHolding, date string, accountNameMap map[string]string) []overviewHoldingInput {
	out := make([]overviewHoldingInput, 0)
	for _, h := range holdings {
		if h.Date.Format(dateLayout) != date {
			continue
		}
		out = append(out, overviewHoldingInput{
			AccountID:   h.AccountID,
			AccountName: accountNameMap[h.AccountID],
			Symbol:      h.Symbol,
			Quantity:    h.Quantity,
			ValueCents:  h.ValueCents,
			Date:        date,
		})
	}
	return out
}

type allocationTargetsResponse struct {
	Targets           []allocationTargetJSON `json:"targets"`
	DriftWarnBps      int                    `json:"driftWarnBps"`
	SingleStockMaxBps int                    `json:"singleStockMaxBps"`
	TargetsSumBps     int                    `json:"targetsSumBps"`
	TargetsComplete   bool                   `json:"targetsComplete"`
}

type allocationTargetJSON struct {
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	TargetBps int    `json:"targetBps"`
}

type putAllocationTargetsRequest struct {
	Targets           []allocationTargetJSON `json:"targets"`
	DriftWarnBps      *int                   `json:"driftWarnBps,omitempty"`
	SingleStockMaxBps *int                   `json:"singleStockMaxBps,omitempty"`
}

func handleGetAllocationTargets(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	dbTargets, err := deps.db.ListAllocationTargets(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list targets: "+err.Error())
		return
	}
	settings := overviewSettingsInput{DriftWarnBps: defaultDriftWarnBps, SingleStockMaxBps: defaultSingleStockMaxBps}
	dbSettings, err := deps.db.GetAllocationSettings(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get settings: "+err.Error())
		return
	}
	if dbSettings != nil {
		settings.DriftWarnBps = dbSettings.DriftWarnBps
		settings.SingleStockMaxBps = dbSettings.SingleStockMaxBps
	}

	targets := make([]allocationTargetJSON, 0, len(dbTargets))
	sum := 0
	for _, t := range dbTargets {
		targets = append(targets, allocationTargetJSON{Kind: t.Kind, Key: t.Key, TargetBps: t.TargetBps})
		sum += t.TargetBps
	}
	_ = json.NewEncoder(w).Encode(allocationTargetsResponse{
		Targets:           targets,
		DriftWarnBps:      settings.DriftWarnBps,
		SingleStockMaxBps: settings.SingleStockMaxBps,
		TargetsSumBps:     sum,
		TargetsComplete:   len(targets) == 0 || sum == 10000,
	})
}

func handlePutAllocationTargets(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	var req putAllocationTargetsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	seen := make(map[string]bool)
	dbRows := make([]database.AllocationTarget, 0, len(req.Targets))
	sum := 0
	for _, t := range req.Targets {
		kind := strings.TrimSpace(t.Kind)
		key := strings.TrimSpace(t.Key)
		if kind != targetKindTicker && kind != targetKindAssetClass {
			writeJSONError(w, http.StatusBadRequest, "kind must be ticker or asset_class")
			return
		}
		if key == "" {
			writeJSONError(w, http.StatusBadRequest, "key is required")
			return
		}
		if t.TargetBps < 0 || t.TargetBps > 10000 {
			writeJSONError(w, http.StatusBadRequest, "targetBps must be between 0 and 10000")
			return
		}
		if kind == targetKindTicker {
			key = strings.ToUpper(key)
		} else {
			key = strings.ToLower(key)
			if key != assetClassETF && key != assetClassStock && key != assetClassOther {
				writeJSONError(w, http.StatusBadRequest, "asset_class key must be etf, stock, or other")
				return
			}
		}
		uniq := kind + ":" + key
		if seen[uniq] {
			writeJSONError(w, http.StatusBadRequest, "duplicate target: "+uniq)
			return
		}
		seen[uniq] = true
		sum += t.TargetBps
		dbRows = append(dbRows, database.AllocationTarget{
			Kind:      kind,
			Key:       key,
			TargetBps: t.TargetBps,
		})
	}
	if sum > 10000 {
		writeJSONError(w, http.StatusBadRequest, "targets sum cannot exceed 100%")
		return
	}

	if err := deps.db.DeleteAllAllocationTargets(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to clear targets: "+err.Error())
		return
	}
	if err := deps.db.InsertAllocationTargets(r.Context(), dbRows); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to save targets: "+err.Error())
		return
	}

	settings := database.AllocationSettings{
		ID:                1,
		DriftWarnBps:      defaultDriftWarnBps,
		SingleStockMaxBps: defaultSingleStockMaxBps,
	}
	existing, _ := deps.db.GetAllocationSettings(r.Context())
	if existing != nil {
		settings = *existing
	}
	if req.DriftWarnBps != nil {
		settings.DriftWarnBps = *req.DriftWarnBps
	}
	if req.SingleStockMaxBps != nil {
		settings.SingleStockMaxBps = *req.SingleStockMaxBps
	}
	if err := deps.db.UpsertAllocationSettings(r.Context(), &settings); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to save settings: "+err.Error())
		return
	}

	out := make([]allocationTargetJSON, 0, len(dbRows))
	for _, t := range dbRows {
		out = append(out, allocationTargetJSON{Kind: t.Kind, Key: t.Key, TargetBps: t.TargetBps})
	}
	_ = json.NewEncoder(w).Encode(allocationTargetsResponse{
		Targets:           out,
		DriftWarnBps:      settings.DriftWarnBps,
		SingleStockMaxBps: settings.SingleStockMaxBps,
		TargetsSumBps:     sum,
		TargetsComplete:   len(out) == 0 || sum == 10000,
	})
}

type securitiesResponse struct {
	Securities []securityJSON `json:"securities"`
}

type securityJSON struct {
	Symbol     string `json:"symbol"`
	AssetClass string `json:"assetClass"`
	Source     string `json:"source"`
}

type putSecurityRequest struct {
	Symbol     string `json:"symbol"`
	AssetClass string `json:"assetClass"`
}

func handleGetSecurities(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	rows, err := deps.db.ListSecurities(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list securities: "+err.Error())
		return
	}
	out := make([]securityJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, securityJSON{Symbol: s.Symbol, AssetClass: s.AssetClass, Source: s.Source})
	}
	_ = json.NewEncoder(w).Encode(securitiesResponse{Securities: out})
}

func handlePutSecurity(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database not configured")
		return
	}
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	var req putSecurityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	class := strings.ToLower(strings.TrimSpace(req.AssetClass))
	if symbol == "" {
		writeJSONError(w, http.StatusBadRequest, "symbol is required")
		return
	}
	switch class {
	case assetClassCash, assetClassETF, assetClassStock, assetClassOther:
	default:
		writeJSONError(w, http.StatusBadRequest, "assetClass must be cash, etf, stock, or other")
		return
	}

	security := &database.Security{
		Symbol:     symbol,
		AssetClass: class,
		Source:     sourceUser,
	}
	if err := deps.db.UpsertSecurity(r.Context(), security); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to save security: "+err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(securityJSON{Symbol: symbol, AssetClass: class, Source: sourceUser})
}
