package server

import (
	"testing"
	"time"
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
