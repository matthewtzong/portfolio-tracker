package server

import (
	"testing"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/plaid"
)

func TestPlaidTransactionToDB_CategoryRuleTakesPrecedence(t *testing.T) {
	categoryVenmo := int64(1)
	categoryUncategorized := int64(2)

	plaidNameToCategoryID := map[string]int64{
		"Transfer": categoryUncategorized,
	}

	rules := []database.CategoryRule{
		{
			ID:          1,
			MatchString: "venmo",
			CategoryID:  categoryVenmo,
		},
	}

	tx := plaid.PlaidTransaction{
		AccountID:     "acc-1",
		TransactionID: "tx-1",
		Amount:        25.50,
		Date:          "2024-01-15",
		Name:          "Venmo payment to friend",
		MerchantName:  strPtr("VENMO"),
		Category:      []string{"Transfer"},
		Pending:       false,
	}

	result := plaidTransactionToDB(tx, plaidNameToCategoryID, categoryUncategorized, categoryVenmo, rules)

	if result.CategoryID == nil || *result.CategoryID != categoryVenmo {
		t.Fatalf("expected category %d from rule, got %#v", categoryVenmo, result.CategoryID)
	}
	if result.ReviewStatus != database.ReviewStatusPending {
		t.Fatalf("expected review_status pending for Venmo, got %q", result.ReviewStatus)
	}
	if result.AmountCents != int64(-2550) {
		t.Fatalf("expected amountCents -2550, got %d", result.AmountCents)
	}
}

func TestPlaidTransactionToDB_AccountScopedRule(t *testing.T) {
	categoryCCPayment := int64(30)
	categoryFood := int64(31)
	categoryUncategorized := int64(32)
	checkingID := "checking-acc"
	creditID := "credit-acc"

	plaidNameToCategoryID := map[string]int64{
		"Food and Drink": categoryFood,
	}

	rules := []database.CategoryRule{
		{
			ID:             1,
			MatchString:    "capital one",
			CategoryID:     categoryCCPayment,
			PlaidAccountID: &checkingID,
		},
	}

	// Checking + "Capital One" -> Credit Card Payments rule.
	checkingTx := plaid.PlaidTransaction{
		AccountID:     checkingID,
		TransactionID: "tx-check",
		Amount:        100.00,
		Date:          "2024-01-15",
		Name:          "Capital One",
		Category:      []string{"Transfer"},
		Pending:       false,
	}
	resultChecking := plaidTransactionToDB(checkingTx, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if resultChecking.CategoryID == nil || *resultChecking.CategoryID != categoryCCPayment {
		t.Fatalf("expected CC payment category %d on checking, got %#v", categoryCCPayment, resultChecking.CategoryID)
	}

	// Credit + "Capital One Cafe" -> rule skipped; falls back to Plaid Food and Drink.
	creditTx := plaid.PlaidTransaction{
		AccountID:     creditID,
		TransactionID: "tx-credit",
		Amount:        12.00,
		Date:          "2024-01-16",
		Name:          "Capital One Cafe",
		Category:      []string{"Food and Drink"},
		Pending:       false,
	}
	resultCredit := plaidTransactionToDB(creditTx, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if resultCredit.CategoryID == nil || *resultCredit.CategoryID != categoryFood {
		t.Fatalf("expected food category %d on credit, got %#v", categoryFood, resultCredit.CategoryID)
	}
}

func TestPlaidTransactionToDB_FallsBackToPlaidPrimaryOrUncategorized(t *testing.T) {
	categoryInvestments := int64(10)
	categoryUncategorized := int64(11)

	plaidNameToCategoryID := map[string]int64{
		"Investments": categoryInvestments,
	}

	rules := []database.CategoryRule{}

	txWithKnownPlaidCategory := plaid.PlaidTransaction{
		AccountID:     "acc-1",
		TransactionID: "tx-1",
		Amount:        100.00,
		Date:          "2024-01-01",
		Name:          "Fidelity purchase",
		MerchantName:  strPtr("Fidelity"),
		Category:      []string{"Investments"},
		Pending:       false,
	}

	txWithUnknownPlaidCategory := plaid.PlaidTransaction{
		AccountID:     "acc-1",
		TransactionID: "tx-2",
		Amount:        10.00,
		Date:          "2024-01-02",
		Name:          "Misc",
		MerchantName:  nil,
		Category:      []string{"Unknown Category"},
		Pending:       false,
	}

	resultKnown := plaidTransactionToDB(txWithKnownPlaidCategory, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if resultKnown.CategoryID == nil || *resultKnown.CategoryID != categoryInvestments {
		t.Fatalf("expected category %d, got %#v", categoryInvestments, resultKnown.CategoryID)
	}

	resultUnknown := plaidTransactionToDB(txWithUnknownPlaidCategory, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if resultUnknown.CategoryID == nil || *resultUnknown.CategoryID != categoryUncategorized {
		t.Fatalf("expected uncategorized %d, got %#v", categoryUncategorized, resultUnknown.CategoryID)
	}
}

func TestPlaidTransactionToDB_UsesPersonalFinanceCategoryWhenLegacyCategoryEmpty(t *testing.T) {
	categoryFood := int64(20)
	categoryUncategorized := int64(21)
	plaidNameToCategoryID := map[string]int64{
		"Food and Drink": categoryFood,
	}
	rules := []database.CategoryRule{}

	// Plaid often returns only personal_finance_category; legacy category can be empty.
	tx := plaid.PlaidTransaction{
		AccountID:               "acc-1",
		TransactionID:           "tx-1",
		Amount:                  25.00,
		Date:                    "2024-01-01",
		Name:                    "Whole Foods",
		Category:                nil,
		PersonalFinanceCategory: &plaid.PersonalFinanceCategory{Primary: "FOOD_AND_DRINK"},
		Pending:                 false,
	}
	result := plaidTransactionToDB(tx, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if result.CategoryID == nil || *result.CategoryID != categoryFood {
		t.Fatalf("expected category %d from PFC FOOD_AND_DRINK, got %#v", categoryFood, result.CategoryID)
	}

	// No category at all -> Uncategorized.
	txNoCat := plaid.PlaidTransaction{
		AccountID: "acc-1", TransactionID: "tx-2", Amount: 5, Date: "2024-01-02", Name: "Unknown",
		Category: nil, PersonalFinanceCategory: nil, Pending: false,
	}
	resultNoCat := plaidTransactionToDB(txNoCat, plaidNameToCategoryID, categoryUncategorized, 0, rules)
	if resultNoCat.CategoryID == nil || *resultNoCat.CategoryID != categoryUncategorized {
		t.Fatalf("expected uncategorized %d when no Plaid category, got %#v", categoryUncategorized, resultNoCat.CategoryID)
	}
}

func TestCalculateMonthlySpentByCategoryFromData(t *testing.T) {
	categoryGroceries := database.Category{
		ID:        1,
		Name:      "Groceries",
		PlaidName: nil,
		Expense:   true,
	}
	categoryVenmo := database.Category{
		ID:        3,
		Name:      "Venmo",
		PlaidName: nil,
		Expense:   true,
	}
	categoryIncome := database.Category{
		ID:        2,
		Name:      "Income",
		PlaidName: nil,
		Expense:   false,
	}

	transactions := []database.Transaction{
		{
			ID:          1,
			Date:        database.DateOnly{Time: time.Now()},
			AmountCents: -5000,
			Name:        "Grocery Store",
			CategoryID:  int64Ptr(categoryGroceries.ID),
		},
		{
			ID:          2,
			Date:        database.DateOnly{Time: time.Now()},
			AmountCents: 3000,
			Name:        "Refund from Grocery Store",
			CategoryID:  int64Ptr(categoryGroceries.ID),
		},
		{
			ID:          3,
			Date:        database.DateOnly{Time: time.Now()},
			AmountCents: 10000,
			Name:        "Paycheck",
			CategoryID:  int64Ptr(categoryIncome.ID),
		},
		{
			ID:           4,
			Date:         database.DateOnly{Time: time.Now()},
			AmountCents:  -4000,
			Name:         "Venmo",
			CategoryID:   int64Ptr(categoryVenmo.ID),
			ReviewStatus: database.ReviewStatusPending,
		},
	}

	spent := calculateMonthlySpentByCategoryFromData(transactions, []database.Category{categoryGroceries, categoryIncome, categoryVenmo})

	got := spent["Groceries"]
	if got != 2000 {
		t.Fatalf("expected Groceries spent 2000, got %d", got)
	}

	if _, ok := spent["Venmo"]; ok {
		t.Fatalf("expected pending Venmo to be excluded from spent")
	}

	if _, ok := spent["Income"]; ok {
		t.Fatalf("expected no entry for non-expense Income category")
	}
}

func TestTransactionsSummaryHandlesRefundsAndTransfers(t *testing.T) {
	categoryGroceries := database.Category{ID: 1, Name: "Groceries", Expense: true}
	categoryInvestments := database.Category{ID: 2, Name: "Investments", Expense: false}
	categoryTransfer := database.Category{ID: 3, Name: "Transfer", Expense: false}
	categoryIncome := database.Category{ID: 4, Name: "Salary", Expense: false}

	categories := []database.Category{
		categoryGroceries,
		categoryInvestments,
		categoryTransfer,
		categoryIncome,
	}

	// Helper function to make pointer IDs.
	toPtr := func(id int64) *int64 { return &id }
	transactions := []database.Transaction{
		// Grocery spend of $100.
		{ID: 1, AmountCents: -10000, CategoryID: toPtr(categoryGroceries.ID)},
		// Grocery refund of $20.
		{ID: 2, AmountCents: 2000, CategoryID: toPtr(categoryGroceries.ID)},
		// Investment of $50.
		{ID: 3, AmountCents: -5000, CategoryID: toPtr(categoryInvestments.ID)},
		// Credit card payment (Transfer) of $30 — should be ignored.
		{ID: 4, AmountCents: -3000, CategoryID: toPtr(categoryTransfer.ID)},
		// Salary income of $1,000.
		{ID: 5, AmountCents: 100000, CategoryID: toPtr(categoryIncome.ID)},
	}

	income, expenses, invested := summarizeTransactionsForTest(transactions, categories)

	// Groceries: -10000 + 2000 refund = -8000 net → expenses 8000.
	if expenses != 8000 {
		t.Fatalf("expected expenses 8000, got %d", expenses)
	}
	// Salary: +100000 income.
	if income != 100000 {
		t.Fatalf("expected income 100000, got %d", income)
	}
	// Investments: single -5000 transaction → invested 5000.
	if invested != 5000 {
		t.Fatalf("expected invested 5000, got %d", invested)
	}
}

// Helper function to make string pointers.
func strPtr(s string) *string {
	return &s
}

// Helper function to make int64 pointers.
func int64Ptr(v int64) *int64 {
	return &v
}

// Function for testing the calculation of monthly spent by category.
func calculateMonthlySpentByCategoryFromData(transactions []database.Transaction, categories []database.Category) map[string]int64 {
	monthlySpending := make(map[string]int64)
	if len(transactions) == 0 || len(categories) == 0 {
		return monthlySpending
	}

	categoriesByID := make(map[int64]database.Category, len(categories))
	for _, category := range categories {
		categoriesByID[category.ID] = category
	}

	for _, transaction := range transactions {
		if transaction.ReviewStatus == database.ReviewStatusPending {
			continue
		}
		if transaction.CategoryID == nil {
			continue
		}
		category, ok := categoriesByID[*transaction.CategoryID]
		if !ok || !category.Expense {
			continue
		}

		categoryName := category.Name
		monthlySpending[categoryName] += -transaction.AmountCents
	}

	return monthlySpending
}

// Function for testing the summarization of transactions.
func summarizeTransactionsForTest(transactions []database.Transaction, categories []database.Category) (incomeCents, expensesCents, investedCents int64) {
	var investmentsID, transferCategoryID int64
	expenseCategoryIDs := make(map[int64]bool)

	// Maps the categories to their IDs.
	for _, category := range categories {
		if category.Name == "Investments" {
			investmentsID = category.ID
		}
		if category.Name == "Transfer" {
			transferCategoryID = category.ID
		}
		if category.Expense {
			expenseCategoryIDs[category.ID] = true
		}
	}

	// Loops through the transactions and summarizes the income, expenses, and invested amounts.
	for _, transaction := range transactions {
		if transaction.ReviewStatus == database.ReviewStatusPending {
			continue
		}
		if transaction.CategoryID != nil && *transaction.CategoryID == transferCategoryID {
			continue
		}
		amountCents := transaction.AmountCents

		if transaction.CategoryID != nil && expenseCategoryIDs[*transaction.CategoryID] {
			expensesCents += -amountCents
			continue
		}
		if transaction.CategoryID != nil && *transaction.CategoryID == investmentsID {
			investedCents += -amountCents
			continue
		}
		if amountCents > 0 {
			incomeCents += amountCents
		} else {
			expensesCents += -amountCents
		}
	}

	return incomeCents, expensesCents, investedCents
}
