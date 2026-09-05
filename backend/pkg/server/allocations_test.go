package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDietzToOverviewDelta(t *testing.T) {
	bps := int64(150)
	delta := dietzToOverviewDelta(&performanceResponseJSON{
		StartDate:   "2026-08-27",
		EndDate:     "2026-08-28",
		GainCents:   50000,
		ReturnBps:   bps,
	})
	if delta == nil {
		t.Fatal("expected delta")
	}
	if delta.AbsoluteCents != 50000 {
		t.Fatalf("gain=%d, want 50000", delta.AbsoluteCents)
	}
	if delta.PercentBps == nil || *delta.PercentBps != 150 {
		t.Fatalf("return bps=%v, want 150", delta.PercentBps)
	}
	if delta.FromDate != "2026-08-27" || delta.ToDate != "2026-08-28" {
		t.Fatalf("dates=%s→%s", delta.FromDate, delta.ToDate)
	}
}

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
	targets := []overviewTargetInput{
		{Kind: targetKindTicker, Key: "VOO", TargetBps: 5000},
		{Kind: targetKindAssetClass, Key: "stock", TargetBps: 5000},
	}
	settings := overviewSettingsInput{DriftWarnBps: 500, SingleStockMaxBps: 1000}

	resp := buildPortfolioOverview(current, prior, prior, classBySymbol, targets, settings)

	if resp.TotalValueCents != 1000000 {
		t.Fatalf("expected invested 1000000 (ex-cash), got %d", resp.TotalValueCents)
	}
	for _, s := range resp.BySymbol {
		if s.Symbol == "SPAXX" {
			t.Fatal("SPAXX should be excluded from bySymbol")
		}
	}

	// GOOG ~30% and AAPL ~20% over 10% — only AAPL should warn (GOOG is exempt).
	hasCapError := false
	for _, w := range resp.Warnings {
		if w.Type == "single_name_cap" && w.Severity == "error" {
			hasCapError = true
			if !strings.Contains(w.Message, "AAPL") {
				t.Fatalf("expected AAPL in cap message, got %q", w.Message)
			}
			if strings.Contains(w.Message, "GOOG") {
				t.Fatalf("GOOG should be exempt from cap warning, got %q", w.Message)
			}
		}
	}
	if !hasCapError {
		t.Fatalf("expected single_name_cap error for non-GOOG stock over cap; warnings=%+v", resp.Warnings)
	}

	// Only GOOG over cap — no warning.
	currentOne := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "B", Symbol: "VOO", Quantity: 1, ValueCents: 800000},
		{AccountID: "a1", AccountName: "B", Symbol: "GOOG", Quantity: 1, ValueCents: 150000},
		{AccountID: "a1", AccountName: "B", Symbol: "AAPL", Quantity: 1, ValueCents: 50000},
	}
	resp2 := buildPortfolioOverview(currentOne, prior, prior, classBySymbol, nil, settings)
	for _, w := range resp2.Warnings {
		if w.Type == "single_name_cap" || w.Type == "concentration" {
			t.Fatalf("GOOG-only over cap should not warn; got %+v", w)
		}
	}

	// Non-GOOG stock alone over cap — warn.
	currentAVGO := []overviewHoldingInput{
		{AccountID: "a1", AccountName: "B", Symbol: "VOO", Quantity: 1, ValueCents: 800000},
		{AccountID: "a1", AccountName: "B", Symbol: "AVGO", Quantity: 1, ValueCents: 150000},
		{AccountID: "a1", AccountName: "B", Symbol: "AAPL", Quantity: 1, ValueCents: 50000},
	}
	classAVGO := map[string]string{
		"VOO":  assetClassETF,
		"AVGO": assetClassStock,
		"AAPL": assetClassStock,
	}
	resp3 := buildPortfolioOverview(currentAVGO, prior, prior, classAVGO, nil, settings)
	foundAVGO := false
	for _, w := range resp3.Warnings {
		if w.Type == "single_name_cap" && strings.Contains(w.Message, "AVGO") {
			foundAVGO = true
		}
	}
	if !foundAVGO {
		t.Fatalf("expected AVGO cap warning; warnings=%+v", resp3.Warnings)
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

func TestTargetMatchingAggregatesGroupMembers(t *testing.T) {
	symbols := []overviewSymbolJSON{
		{Symbol: "VOO", AssetClass: assetClassETF, ValueCents: 400000},
		{Symbol: "FXAIX", AssetClass: assetClassETF, ValueCents: 300000},
		{Symbol: "QQQ", AssetClass: assetClassETF, ValueCents: 100000},
		{Symbol: "AAPL", AssetClass: assetClassStock, ValueCents: 200000},
	}
	targets := []overviewTargetInput{
		{
			Kind:      targetKindGroup,
			Key:       "S&P 500",
			TargetBps: 7000,
			Members:   []string{"voo", "FXAIX"},
		},
		{Kind: targetKindAssetClass, Key: "etf", TargetBps: 1000},
		{Kind: targetKindAssetClass, Key: "stock", TargetBps: 2000},
	}
	buckets, actual := matchTargetBuckets(symbols, 1000000, targets)
	if actual["group:S&P 500"] != 7000 {
		t.Fatalf("S&P 500 weight bps = %d, want 7000", actual["group:S&P 500"])
	}
	if actual["asset_class:etf"] != 1000 {
		t.Fatalf("other ETF leftover bps = %d, want 1000 (QQQ only)", actual["asset_class:etf"])
	}
	if actual["asset_class:stock"] != 2000 {
		t.Fatalf("stock leftover bps = %d", actual["asset_class:stock"])
	}
	found := false
	for _, b := range buckets {
		if b.Key == "S&P 500" {
			found = true
			if b.ValueCents != 700000 {
				t.Fatalf("S&P 500 value = %d, want 700000", b.ValueCents)
			}
			if b.Label != "S&P 500" {
				t.Fatalf("label = %q", b.Label)
			}
		}
	}
	if !found {
		t.Fatalf("expected S&P 500 bucket, got %+v", buckets)
	}
}

func TestNormalizeGroupMembers(t *testing.T) {
	got := normalizeGroupMembers([]string{" voo ", "FXAIX", "VOO", "", "spy"})
	want := []string{"FXAIX", "SPY", "VOO"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	gotCSV := normalizeGroupMembers([]string{"VOO, FXAIX, spy"})
	if len(gotCSV) != 3 || gotCSV[0] != "FXAIX" || gotCSV[1] != "SPY" || gotCSV[2] != "VOO" {
		t.Fatalf("csv normalize got %v", gotCSV)
	}

	// Spaces alone must not split — only commas.
	gotSpaces := normalizeGroupMembers([]string{"VOO FXAIX"})
	if len(gotSpaces) != 1 || gotSpaces[0] != "VOO FXAIX" {
		t.Fatalf("space-only should stay one token, got %v", gotSpaces)
	}
}

func TestFlexibleStringListUnmarshal(t *testing.T) {
	var fromArray flexibleStringList
	if err := json.Unmarshal([]byte(`["voo"," FXAIX "]`), &fromArray); err != nil {
		t.Fatal(err)
	}
	got := normalizeGroupMembers([]string(fromArray))
	if len(got) != 2 || got[0] != "FXAIX" || got[1] != "VOO" {
		t.Fatalf("array form got %v", got)
	}

	var fromCSV flexibleStringList
	if err := json.Unmarshal([]byte(`"VOO, FXAIX, spy"`), &fromCSV); err != nil {
		t.Fatal(err)
	}
	got = normalizeGroupMembers([]string(fromCSV))
	if len(got) != 3 {
		t.Fatalf("csv form got %v", got)
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
			t.Fatal("NEW and SPAXX should not be in movers")
		}
	}
	_ = losers
}

func TestComputeMoversSortsByPercent(t *testing.T) {
	classBySymbol := map[string]string{
		"VOO":  assetClassETF,
		"SMALL": assetClassStock,
	}
	prior := []overviewHoldingInput{
		{Symbol: "VOO", ValueCents: 500000},
		{Symbol: "SMALL", ValueCents: 10000},
	}
	current := []overviewHoldingInput{
		{Symbol: "VOO", ValueCents: 510000},  // +2%
		{Symbol: "SMALL", ValueCents: 15000}, // +50%
	}
	gainers, _ := computeMovers(prior, current, classBySymbol)
	if len(gainers) != 2 {
		t.Fatalf("expected 2 gainers, got %d", len(gainers))
	}
	if gainers[0].Symbol != "SMALL" {
		t.Fatalf("expected SMALL first by %%, got %+v", gainers)
	}
}

func TestFindWeekComparisonDate(t *testing.T) {
	dates := []string{"2026-08-10", "2026-08-15", "2026-08-17", "2026-08-24"}
	got, ok := findWeekComparisonDate(dates)
	if !ok {
		t.Fatal("expected week comparison date")
	}
	if got != "2026-08-17" {
		t.Fatalf("expected 2026-08-17, got %s", got)
	}
}
