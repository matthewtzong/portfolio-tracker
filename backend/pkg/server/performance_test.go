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
