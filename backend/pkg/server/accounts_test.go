package server

import (
	"testing"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	// "github.com/matthewtzong/portfolio-tracker/backend/pkg/snaptrade"
)

func TestPlaidAccountDisplayName(t *testing.T) {
	custom := "My Brokerage"
	account := database.PlaidAccount{
		Name:        "Plaid Brokerage ****1234",
		DisplayName: &custom,
	}
	if got := plaidAccountDisplayName(account); got != custom {
		t.Errorf("plaidAccountDisplayName with display_name = %q, want %q", got, custom)
	}

	account.DisplayName = nil
	if got := plaidAccountDisplayName(account); got != account.Name {
		t.Errorf("plaidAccountDisplayName without display_name = %q, want %q", got, account.Name)
	}

	blank := "   "
	account.DisplayName = &blank
	if got := plaidAccountDisplayName(account); got != account.Name {
		t.Errorf("plaidAccountDisplayName with blank display_name = %q, want %q", got, account.Name)
	}
}

func TestIsPlaidLiability(t *testing.T) {
	tests := []struct {
		accountType string
		want        bool
	}{
		{"credit", true},
		{"loan", true},
		{"depository", false},
		{"investment", false},
		{"", false},
	}

	for _, test := range tests {
		if got := isPlaidLiability(test.accountType); got != test.want {
			t.Errorf("isPlaidLiability(%q) = %v, want %v", test.accountType, got, test.want)
		}
	}
}

func TestIsPlaidCash(t *testing.T) {
	checking := "checking"
	cd := "cd"
	other := "other"

	tests := []struct {
		accountType string
		subtype     *string
		want        bool
	}{
		{"depository", &checking, true},
		{"depository", &cd, true},
		{"depository", &other, false},
		{"depository", nil, true},
		{"investment", &checking, false},
	}

	for _, tt := range tests {
		if got := isPlaidCash(tt.accountType, tt.subtype); got != tt.want {
			t.Errorf("isPlaidCash(%q, %v) = %v, want %v", tt.accountType, tt.subtype, got, tt.want)
		}
	}
}

func TestLoadPlaidAccountsNetWorthClassification(t *testing.T) {
	checking := "checking"

	cashAccount := database.PlaidAccount{
		PlaidItemID:    "item-1",
		AccountID:      "acc-1",
		Name:           "Checking",
		Type:           "depository",
		Subtype:        &checking,
		CurrentBalance: 100.00,
	}

	creditAccount := database.PlaidAccount{
		PlaidItemID:    "item-2",
		AccountID:      "acc-2",
		Name:           "Credit Card",
		Type:           "credit",
		CurrentBalance: 50.00,
	}

	_, cashDelta, _, liabilityDelta := loadPlaidAccounts(cashAccount, nil)
	if cashDelta != 10000 {
		t.Errorf("cash account cashDelta = %d, want %d", cashDelta, 10000)
	}
	if liabilityDelta != 0 {
		t.Errorf("cash account liabilityDelta = %d, want %d", liabilityDelta, 0)
	}

	accountJSON, cashDelta, _, liabilityDelta := loadPlaidAccounts(creditAccount, nil)
	if !accountJSON.IsLiability {
		t.Errorf("credit account IsLiability = false, want true")
	}
	if accountJSON.BalanceCents != -5000 {
		t.Errorf("credit account BalanceCents = %d, want %d", accountJSON.BalanceCents, -5000)
	}
	if cashDelta != 0 {
		t.Errorf("credit account cashDelta = %d, want %d", cashDelta, 0)
	}
	if liabilityDelta != 5000 {
		t.Errorf("credit account liabilityDelta = %d, want %d", liabilityDelta, 5000)
	}
}

func TestNetWorthFormulaWithPlaid(t *testing.T) {
	checking := "checking"

	plaidCash := database.PlaidAccount{
		PlaidItemID:    "item-1",
		AccountID:      "plaid-cash",
		Name:           "Checking",
		Type:           "depository",
		Subtype:        &checking,
		CurrentBalance: 100.00,
	}

	plaidCredit := database.PlaidAccount{
		PlaidItemID:    "item-2",
		AccountID:      "plaid-credit",
		Name:           "Credit Card",
		Type:           "credit",
		CurrentBalance: 50.00,
	}

	plaidInvestment := database.PlaidAccount{
		PlaidItemID:    "item-3",
		AccountID:      "plaid-invest",
		Name:           "Brokerage",
		Type:           "investment",
		CurrentBalance: 200.00,
	}

	var cashCents, liabilitiesCents, investmentsCents int64

	_, cashDelta1, investDelta1, liabilityDelta1 := loadPlaidAccounts(plaidCash, nil)
	cashCents += cashDelta1
	investmentsCents += investDelta1
	liabilitiesCents += liabilityDelta1

	_, cashDelta2, investDelta2, liabilityDelta2 := loadPlaidAccounts(plaidCredit, nil)
	cashCents += cashDelta2
	investmentsCents += investDelta2
	liabilitiesCents += liabilityDelta2

	_, cashDelta3, investDelta3, liabilityDelta3 := loadPlaidAccounts(plaidInvestment, nil)
	cashCents += cashDelta3
	investmentsCents += investDelta3
	liabilitiesCents += liabilityDelta3

	netWorth := cashCents + investmentsCents - liabilitiesCents

	if cashCents != 10000 {
		t.Fatalf("expected cash 10000, got %d", cashCents)
	}
	if liabilitiesCents != 5000 {
		t.Fatalf("expected liabilities 5000, got %d", liabilitiesCents)
	}
	if investmentsCents != 20000 {
		t.Fatalf("expected investments 20000, got %d", investmentsCents)
	}
	if netWorth != 25000 {
		t.Fatalf("expected net worth 25000, got %d", netWorth)
	}
}

/*
func TestNetWorthFormulaWithPlaidAndSnaptrade(t *testing.T) {
	checking := "checking"

	plaidCash := database.PlaidAccount{
		PlaidItemID:    "item-1",
		AccountID:      "plaid-cash",
		Name:           "Checking",
		Type:           "depository",
		Subtype:        &checking,
		CurrentBalance: 100.00,
	}

	plaidCredit := database.PlaidAccount{
		PlaidItemID:    "item-2",
		AccountID:      "plaid-credit",
		Name:           "Credit Card",
		Type:           "credit",
		CurrentBalance: 50.00,
	}

	// snap := snaptrade.Account{
	// 	ID:            "snap-1",
	// 	Name:          "Brokerage",
	// 	Number:        "1234",
	// 	BalanceAmount: 200.00,
	// }

	var cashCents, liabilitiesCents, investmentsCents int64

	_, cashDelta1, _, liabilityDelta1 := loadPlaidAccounts(plaidCash, nil)
	cashCents += cashDelta1
	liabilitiesCents += liabilityDelta1

	_, cashDelta2, _, liabilityDelta2 := loadPlaidAccounts(plaidCredit, nil)
	cashCents += cashDelta2
	liabilitiesCents += liabilityDelta2

	_, investDelta := loadSnaptradeAccounts(snap)
	investmentsCents += investDelta

	netWorth := cashCents + investmentsCents - liabilitiesCents

	if cashCents != 10000 {
		t.Fatalf("expected cash 10000, got %d", cashCents)
	}
	if liabilitiesCents != 5000 {
		t.Fatalf("expected liabilities 5000, got %d", liabilitiesCents)
	}
	if investmentsCents != 20000 {
		t.Fatalf("expected investments 20000, got %d", investmentsCents)
	}
	if netWorth != 25000 {
		t.Fatalf("expected net worth 25000, got %d", netWorth)
	}
}
*/
