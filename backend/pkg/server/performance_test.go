package server

import (
	"testing"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
)

func TestClassifyExternalCashFlow(t *testing.T) {
	tests := []struct {
		name       string
		txnType    string
		subtype    string
		amount     int64
		wantCents  int64
		wantExtern bool
	}{
		{"deposit credits cash", "cash", "deposit", -500000, 500000, true},
		{"withdrawal debits cash", "cash", "withdrawal", 200000, -200000, true},
		{"contribution", "cash", "contribution", -100000, 100000, true},
		{"401k buy contribution", "buy", "contribution", 100000, 100000, true},
		{"employer contribution subtype", "cash", "employer contribution", -50000, 50000, true},
		{"transfer in", "transfer", "transfer", -50000, 50000, true},
		{"buy is internal", "buy", "buy", 100000, 0, false},
		{"sell is internal", "sell", "sell", -100000, 0, false},
		{"dividend not external", "cash", "dividend", -2500, 0, false},
		{"fee not external", "fee", "account fee", 1500, 0, false},
		{"interest not external", "cash", "interest", -100, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := classifyExternalCashFlow(tt.txnType, tt.subtype, tt.amount)
			if ok != tt.wantExtern {
				t.Fatalf("isExternal=%v, want %v", ok, tt.wantExtern)
			}
			if got != tt.wantCents {
				t.Fatalf("cents=%d, want %d", got, tt.wantCents)
			}
		})
	}
}

func TestModifiedDietzExcludesDepositFromGain(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)

	// Start 100k, deposit 10k mid-month, end 112k → gain 2k ≈ 1.90%
	result := modifiedDietz(10_000_000, 11_200_000, start, end, []cashFlow{
		{Date: mid, Amount: 1_000_000},
	})

	if result.GainCents != 200_000 {
		t.Fatalf("gain=%d, want 200000", result.GainCents)
	}
	if result.NetContributionsCents != 1_000_000 {
		t.Fatalf("netContrib=%d, want 1000000", result.NetContributionsCents)
	}
	// denom ≈ 10000000 + 1000000*(15/30) = 10500000; return ≈ 200000/10500000 ≈ 190 bps
	if result.ReturnBps < 180 || result.ReturnBps > 200 {
		t.Fatalf("returnBps=%d, want ~190", result.ReturnBps)
	}
}

func TestModifiedDietzNoFlows(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	result := modifiedDietz(10_000_000, 11_000_000, start, end, nil)
	if result.GainCents != 1_000_000 {
		t.Fatalf("gain=%d, want 1000000", result.GainCents)
	}
	if result.ReturnBps != 1000 {
		t.Fatalf("returnBps=%d, want 1000 (10%%)", result.ReturnBps)
	}
}

func TestModifiedDietzExcludesStartDayFlow(t *testing.T) {
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	// EOD start already includes the $6,134 contribution dated on start day.
	startDayContrib := cashFlow{Date: start, Amount: 613_400}
	endDayContrib := cashFlow{Date: end, Amount: 100_000}

	result := modifiedDietz(10_613_400, 10_713_400, start, end, []cashFlow{
		startDayContrib,
		endDayContrib,
	})

	if result.NetContributionsCents != 100_000 {
		t.Fatalf("netContrib=%d, want 100000 (start-day flow excluded)", result.NetContributionsCents)
	}
	// end - start - endDayContrib = 10713400 - 10613400 - 100000 = 0
	if result.GainCents != 0 {
		t.Fatalf("gain=%d, want 0", result.GainCents)
	}
}

func TestCalendarMonthBoundsIncludesEOM(t *testing.T) {
	first, last := calendarMonthBounds(2026, time.March)
	if first.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("first=%s, want 2026-03-01", first.Format("2006-01-02"))
	}
	if last.Format("2006-01-02") != "2026-03-31" {
		t.Fatalf("last=%s, want 2026-03-31", last.Format("2006-01-02"))
	}
}

func TestClampYTDStartToEarliest(t *testing.T) {
	ytd := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	earliest := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	start := ytd
	if start.Before(earliest) {
		start = earliest
	}
	if start.Format("2006-01-02") != "2026-03-31" {
		t.Fatalf("clamped start=%s, want 2026-03-31", start.Format("2006-01-02"))
	}
}

func TestInvestmentTxnSyncWindowIncremental(t *testing.T) {
	end := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	start := latest.AddDate(0, 0, -investmentTxnSyncOverlapDays)
	if start.Before(investmentTxnHistoryFloor) {
		start = investmentTxnHistoryFloor
	}
	if start.Format("2006-01-02") != "2026-08-13" {
		t.Fatalf("incremental start=%s, want 2026-08-13", start.Format("2006-01-02"))
	}
	_ = end
}

func TestInvestmentTxnSyncWindowEmptyUsesFloor(t *testing.T) {
	start := investmentTxnHistoryFloor
	if start.Format("2006-01-02") != "2026-03-31" {
		t.Fatalf("floor=%s, want 2026-03-31", start.Format("2006-01-02"))
	}
}

func TestMoMStartUsesPriorMonthEnd(t *testing.T) {
	loc := time.FixedZone("test", -4*3600)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, loc)
	earliest := time.Date(2026, 3, 31, 0, 0, 0, 0, loc)
	endDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	start, rangeName, err := resolvePerformanceStartDate("mom", now, earliest, endDate, loc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rangeName != "mom" {
		t.Fatalf("range=%s, want mom", rangeName)
	}
	if start.Format("2006-01-02") != "2026-07-31" {
		t.Fatalf("start=%s, want 2026-07-31", start.Format("2006-01-02"))
	}
}

func TestOneYearStartClampedToEarliest(t *testing.T) {
	end := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	earliest := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(-1, 0, 0) // 2025-08-23
	if start.Before(earliest) {
		start = earliest
	}
	if start.Format("2006-01-02") != "2026-03-31" {
		t.Fatalf("1y clamped start=%s, want 2026-03-31", start.Format("2006-01-02"))
	}
}

func TestSumHoldingsAsOfDayUsesLatestPerAccount(t *testing.T) {
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	holdings := []database.DailyHolding{
		// Account A only on Oct 2
		{AccountID: "a", Date: database.DateOnly{Time: day}, ValueCents: 5_000_000},
		{AccountID: "a", Date: database.DateOnly{Time: day}, ValueCents: 700_000},
		// Account B last updated Sept 28 — still included as-of Oct 2
		{AccountID: "b", Date: database.DateOnly{Time: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}, ValueCents: 3_000_000},
		// Stale older row for B should be ignored
		{AccountID: "b", Date: database.DateOnly{Time: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)}, ValueCents: 9_999_999},
		// Future row ignored
		{AccountID: "c", Date: database.DateOnly{Time: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}, ValueCents: 1_000_000},
	}

	sum, ok := sumHoldingsAsOfDay(holdings, day)
	if !ok {
		t.Fatal("expected ok")
	}
	// 5.7M (A) + 3.0M (B) = 8.7M — not just A's 5.7M from the exact end day
	if sum != 8_700_000 {
		t.Fatalf("sum=%d, want 8700000", sum)
	}
}
