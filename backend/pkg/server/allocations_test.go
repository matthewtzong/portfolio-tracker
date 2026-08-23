package server

import (
	"strings"
	"testing"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
)

func TestMapPlaidTypeToAssetClass(t *testing.T) {
	tests := []struct {
		plaidType string
		symbol    string
		wantClass string
		wantSrc   string
	}{
		{"equity", "AAPL", assetClassStock, sourcePlaid},
		{"etf", "VOO", assetClassETF, sourcePlaid},
		{"mutual fund", "FXAIX", assetClassETF, sourcePlaid},
		{"cash", "USD", assetClassCash, sourcePlaid},
		{"bond", "BND", assetClassOther, sourcePlaid},
		{"", "XYZ", assetClassOther, sourcePlaid},
		{"equity", "SPAXX", assetClassCash, sourceHeuristic},
		{"mutual fund", "FCASH", assetClassCash, sourceHeuristic},
	}
	for _, tt := range tests {
		gotClass, gotSrc := mapPlaidTypeToAssetClass(tt.plaidType, tt.symbol)
		if gotClass != tt.wantClass || gotSrc != tt.wantSrc {
			t.Errorf("mapPlaidTypeToAssetClass(%q, %q) = (%q, %q), want (%q, %q)",
				tt.plaidType, tt.symbol, gotClass, gotSrc, tt.wantClass, tt.wantSrc)
		}
	}
}

func TestAggregateHoldingsBySymbolAcrossAccounts(t *testing.T) {
	classBySymbol := map[string]string{"VOO": assetClassETF, "SPAXX": assetClassCash}
	holdings := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "Brokerage", Symbol: "VOO", Quantity: 10, ValueCents: 500000},
		{AccountID: "a2", AccountName: "Roth", Symbol: "VOO", Quantity: 5, ValueCents: 250000},
		{AccountID: "a1", AccountName: "Brokerage", Symbol: "SPAXX", Quantity: 100, ValueCents: 10000},
	}
	agg := aggregateHoldingsBySymbol(holdings, classBySymbol)
	var voo *overviewSymbolJSON
	for i := range agg {
		if agg[i].Symbol == "VOO" {
			voo = &agg[i]
		}
	}
	if voo == nil {
		t.Fatal("expected VOO aggregate")
	}
	if voo.Quantity != 15 || voo.ValueCents != 750000 {
		t.Fatalf("unexpected VOO aggregate: %+v", voo)
	}
	if len(voo.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(voo.Accounts))
	}
}

func TestBuildPortfolioOverviewExcludesCashAndAllowsOneOverCap(t *testing.T) {
	classBySymbol := map[string]string{
		"VOO":  assetClassETF,
		"GOOG": assetClassStock,
		"AAPL": assetClassStock,
		"SPAXX": assetClassCash,
	}
	current := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "B", Symbol: "VOO", Quantity: 1, ValueCents: 500000},
		{AccountID: "a1", AccountName: "B", Symbol: "GOOG", Quantity: 1, ValueCents: 300000},
		{AccountID: "a1", AccountName: "B", Symbol: "AAPL", Quantity: 1, ValueCents: 200000},
		{AccountID: "a1", AccountName: "B", Symbol: "SPAXX", Quantity: 1, ValueCents: 50000},
	}
	prior := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "B", Symbol: "VOO", Quantity: 1, ValueCents: 480000},
		{AccountID: "a1", AccountName: "B", Symbol: "GOOG", Quantity: 1, ValueCents: 290000},
		{AccountID: "a1", AccountName: "B", Symbol: "AAPL", Quantity: 1, ValueCents: 210000},
	}
	day1 := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	snapshots := []database.DailySnapshot{
		{Date: database.DateOnly{Time: day1}, PortfolioValueCents: 980000},
		{Date: database.DateOnly{Time: day2}, PortfolioValueCents: 1000000},
	}
	monthly := []SnapshotDataPoint{
		{Date: "2026-07-01", PortfolioValueCents: 900000},
		{Date: "2026-08-01", PortfolioValueCents: 1000000},
	}
	targets := []overviewTargetInput{
		{Kind: targetKindTicker, Key: "VOO", TargetBps: 5000},
		{Kind: targetKindAssetClass, Key: "stock", TargetBps: 5000},
	}
	settings := overviewSettingsInput{DriftWarnBps: 500, SingleStockMaxBps: 1000}

	resp := buildPortfolioOverview(current, prior, prior, classBySymbol, snapshots, monthly, targets, settings)

	if resp.TotalValueCents != 1000000 {
		t.Fatalf("expected invested 1000000 (ex-cash), got %d", resp.TotalValueCents)
	}
	for _, s := range resp.BySymbol {
		if s.Symbol == "SPAXX" {
			t.Fatal("SPAXX should be excluded from bySymbol")
		}
	}
	if resp.DayOverDay == nil || resp.DayOverDay.AbsoluteCents != 20000 {
		t.Fatalf("unexpected DoD: %+v", resp.DayOverDay)
	}
	if resp.MonthOverMonth == nil || resp.MonthOverMonth.AbsoluteCents != 100000 {
		t.Fatalf("unexpected MoM: %+v", resp.MonthOverMonth)
	}

	// GOOG ~30% over 10% cap; AAPL ~20% over — two over cap => error.
	hasCapError := false
	hasConcentrationInfo := false
	for _, w := range resp.Warnings {
		if w.Type == "single_name_cap" && w.Severity == "error" {
			hasCapError = true
		}
		if w.Type == "concentration" {
			hasConcentrationInfo = true
		}
	}
	if !hasCapError {
		t.Fatalf("expected single_name_cap error when 2 stocks over cap; warnings=%+v", resp.Warnings)
	}
	for _, w := range resp.Warnings {
		if w.Type == "single_name_cap" {
			if !strings.Contains(w.Message, "GOOG") || !strings.Contains(w.Message, "AAPL") {
				t.Fatalf("expected tickers in cap message, got %q", w.Message)
			}
		}
	}
	if hasConcentrationInfo {
		t.Fatal("should not emit muted concentration when 2+ are over cap")
	}

	// Only GOOG over cap.
	currentOne := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "B", Symbol: "VOO", Quantity: 1, ValueCents: 800000},
		{AccountID: "a1", AccountName: "B", Symbol: "GOOG", Quantity: 1, ValueCents: 150000},
		{AccountID: "a1", AccountName: "B", Symbol: "AAPL", Quantity: 1, ValueCents: 50000},
	}
	resp2 := buildPortfolioOverview(currentOne, prior, prior, classBySymbol, snapshots, monthly, nil, settings)
	hasCapError = false
	hasConcentrationInfo = false
	for _, w := range resp2.Warnings {
		if w.Type == "single_name_cap" {
			hasCapError = true
		}
		if w.Type == "concentration" && w.Severity == "info" {
			hasConcentrationInfo = true
		}
	}
	if hasCapError {
		t.Fatal("one stock over cap should not be an error")
	}
	if !hasConcentrationInfo {
		t.Fatalf("expected muted concentration for single over-cap stock; warnings=%+v", resp2.Warnings)
	}
	for _, w := range resp2.Warnings {
		if w.Type == "concentration" && !strings.Contains(w.Message, "GOOG") {
			t.Fatalf("expected GOOG in concentration message, got %q", w.Message)
		}
	}
}

func TestBuildWarningsUsesDisplayLabels(t *testing.T) {
	warnings := buildWarnings(
		nil,
		map[string]int{"asset_class:etf": 3000, "asset_class:stock": 7000},
		[]overviewTargetInput{
			{Kind: targetKindAssetClass, Key: "etf", TargetBps: 2000},
			{Kind: targetKindAssetClass, Key: "stock", TargetBps: 2000},
		},
		overviewSettingsInput{DriftWarnBps: 500, SingleStockMaxBps: 1000},
		true,
		4000,
	)
	foundETF := false
	foundStock := false
	for _, w := range warnings {
		if w.Type == "drift" && strings.Contains(w.Message, "ETFs") {
			foundETF = true
		}
		if w.Type == "drift" && strings.Contains(w.Message, "Single stocks") {
			foundStock = true
		}
	}
	if !foundETF || !foundStock {
		t.Fatalf("expected display labels in drift warnings, got %+v", warnings)
	}
}

func TestTargetMatchingConsumesTickerThenClass(t *testing.T) {
	symbols := []overviewSymbolJSON{
		{Symbol: "VOO", AssetClass: assetClassETF, ValueCents: 500000},
		{Symbol: "QQQ", AssetClass: assetClassETF, ValueCents: 200000},
		{Symbol: "AAPL", AssetClass: assetClassStock, ValueCents: 300000},
	}
	targets := []overviewTargetInput{
		{Kind: targetKindTicker, Key: "VOO", TargetBps: 5000},
		{Kind: targetKindAssetClass, Key: "etf", TargetBps: 2000},
		{Kind: targetKindAssetClass, Key: "stock", TargetBps: 3000},
	}
	buckets, actual := matchTargetBuckets(symbols, 1000000, targets)
	if len(buckets) < 3 {
		t.Fatalf("expected at least 3 buckets, got %+v", buckets)
	}
	if actual["ticker:VOO"] != 5000 {
		t.Fatalf("VOO weight bps = %d", actual["ticker:VOO"])
	}
	if actual["asset_class:etf"] != 2000 {
		t.Fatalf("other ETF leftover bps = %d", actual["asset_class:etf"])
	}
	if actual["asset_class:stock"] != 3000 {
		t.Fatalf("stock leftover bps = %d", actual["asset_class:stock"])
	}
}

func TestComputeMoversSkipsCashAndNewPositions(t *testing.T) {
	classBySymbol := map[string]string{"AAPL": assetClassStock, "SPAXX": assetClassCash, "NEW": assetClassStock}
	prior := []overviewHoldingInput{
		{Symbol: "AAPL", ValueCents: 100000},
		{Symbol: "SPAXX", ValueCents: 50000},
	}
	current := []overviewHoldingInput{
		{Symbol: "AAPL", ValueCents: 110000},
		{Symbol: "SPAXX", ValueCents: 60000},
		{Symbol: "NEW", ValueCents: 20000},
	}
	gainers, losers := computeMovers(prior, current, classBySymbol)
	if len(gainers) != 1 || gainers[0].Symbol != "AAPL" {
		t.Fatalf("unexpected gainers: %+v", gainers)
	}
	for _, g := range gainers {
		if g.Symbol == "NEW" || g.Symbol == "SPAXX" {
			t.Fatal("NEW and SPAXX should not be in % movers")
		}
	}
	_ = losers
}
