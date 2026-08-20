package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
	// "github.com/matthewtzong/portfolio-tracker/backend/pkg/snaptrade"
)

// Account View Model
type AccountJSON struct {
	Provider        string  `json:"provider"`
	PlaidItemID     *string `json:"plaidItemId,omitempty"`
	AccountID       string  `json:"accountId"`
	Name            string  `json:"name"`
	InstitutionName *string `json:"institutionName,omitempty"`
	Mask            *string `json:"mask,omitempty"`
	Type            string  `json:"type"`
	Subtype         *string `json:"subtype,omitempty"`
	BalanceCents    int64   `json:"balanceCents"`
	IsLiability     bool    `json:"isLiability"`
}

// List of Accounts and Net Worth breakdown.
type AccountsResponse struct {
	Accounts         []AccountJSON `json:"accounts"`
	NetWorthCents    int64         `json:"netWorthCents"`
	CashCents        int64         `json:"cashCents"`
	InvestmentsCents int64         `json:"investmentsCents"`
	LiabilitiesCents int64         `json:"liabilitiesCents"`
}

// Request body for renaming a Plaid account.
type renameAccountRequest struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// Response after renaming a Plaid account.
type renameAccountResponse struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
}

// Registers the accounts route.
func registerAccountsRoutes(mux *http.ServeMux, deps apiDependencies) {
	// GET /api/accounts returns all accounts and the current net worth breakdown.
	mux.Handle("/api/accounts", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetAccounts(w, r, deps)
	})))

	// PATCH /api/accounts/rename updates the client-side display name for an account.
	mux.Handle("/api/accounts/rename", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			methodNotAllowed(w, http.MethodPatch)
			return
		}
		handleRenameAccount(w, r, deps)
	})))

	// GET /api/net-worth/snapshots returns monthly net worth snapshots over time.
	mux.Handle("/api/net-worth/snapshots", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleGetNetWorthSnapshots(w, r, deps)
	})))
}

// Fetches the accounts and net worth breakdown.
func handleGetAccounts(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database is not configured")
		return
	}

	// Validate the authenticated user exists
	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Load Plaid accounts from the database.
	plaidAccounts, err := deps.db.ListPlaidAccounts(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list Plaid accounts: "+err.Error())
		return
	}

	// Map institution names by Plaid item_id for the accounts list.
	institutionByItemID := make(map[string]string)
	plaidItems, err := deps.db.ListPlaidItems(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list Plaid items: "+err.Error())
		return
	}
	for _, item := range plaidItems {
		if item.InstitutionName != nil && *item.InstitutionName != "" {
			institutionByItemID[item.ItemID] = *item.InstitutionName
		}
	}

	// Converts the Plaid accounts to the AccountJSON view model.
	// Plaid accounts contribute to cash (HYSA, checking, CDs), liabilities (credit cards), and investments (stocks, ETFs, etc).
	accounts := make([]AccountJSON, 0)
	var (
		cashCents        int64
		investmentsCents int64
		liabilitiesCents int64
	)
	for _, account := range plaidAccounts {
		var institutionName *string
		if name, ok := institutionByItemID[account.PlaidItemID]; ok {
			institutionName = &name
		}
		accountJSON, cashDelta, investmentsDelta, liabilityDelta := loadPlaidAccounts(account, institutionName)
		accounts = append(accounts, accountJSON)
		cashCents += cashDelta
		investmentsCents += investmentsDelta
		liabilitiesCents += liabilityDelta
	}

	// Net worth = assets (cash + investments) - liabilities.
	netWorthCents := cashCents + investmentsCents - liabilitiesCents

	resp := AccountsResponse{
		Accounts:         accounts,
		NetWorthCents:    netWorthCents,
		CashCents:        cashCents,
		InvestmentsCents: investmentsCents,
		LiabilitiesCents: liabilitiesCents,
	}

	// Return the accounts and net worth breakdown.
	_ = json.NewEncoder(w).Encode(resp)
}

// Renames a Plaid account by saving a client-side display_name.
func handleRenameAccount(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database is not configured")
		return
	}

	if userID, ok := serverauth.UserIDFromContext(r.Context()); !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	var req renameAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		writeJSONError(w, http.StatusBadRequest, "accountId is required")
		return
	}

	existing, err := deps.db.GetPlaidAccountByAccountID(r.Context(), accountID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get Plaid account: "+err.Error())
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "Plaid account not found")
		return
	}

	trimmedName := strings.TrimSpace(req.DisplayName)
	var displayName *string
	if trimmedName != "" {
		displayName = &trimmedName
	}

	if err := deps.db.UpdatePlaidAccountDisplayName(r.Context(), accountID, displayName); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to update account name: "+err.Error())
		return
	}

	effectiveName := existing.Name
	if displayName != nil {
		effectiveName = *displayName
	}

	_ = json.NewEncoder(w).Encode(renameAccountResponse{
		AccountID: accountID,
		Name:      effectiveName,
	})
}

// Returns monthly net worth snapshots over time.
func handleGetNetWorthSnapshots(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")
	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "database is not configured")
		return
	}

	// Default: return full history (no pruning) so long-term net worth is visible.
	now := GetLocalNow()
	endMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, GetLocalLocation())
	startMonth := time.Date(1970, 1, 1, 0, 0, 0, 0, GetLocalLocation())
	snapshots, err := deps.db.ListMonthlyNetWorth(r.Context(), startMonth, endMonth)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list monthly net worth: "+err.Error())
		return
	}

	// Converts the monthly net worth snapshots to the netWorthSnapshotJSON view model.
	type netWorthSnapshotJSON struct {
		Month            string `json:"month"`
		NetWorthCents    int64  `json:"netWorthCents"`
		CashCents        int64  `json:"cashCents"`
		InvestmentsCents int64  `json:"investmentsCents"`
		LiabilitiesCents int64  `json:"liabilitiesCents"`
	}

	output := make([]netWorthSnapshotJSON, len(snapshots))
	for i, snapshot := range snapshots {
		output[i] = netWorthSnapshotJSON{
			Month:            snapshot.Month.Format("2006-01-02"),
			NetWorthCents:    snapshot.NetWorthCents,
			CashCents:        snapshot.CashCents,
			InvestmentsCents: snapshot.InvestmentsCents,
			LiabilitiesCents: snapshot.LiabilitiesCents,
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"monthly": output})
}

// Load Plaid Accounts from the database
func loadPlaidAccounts(a database.PlaidAccount, institutionName *string) (AccountJSON, int64, int64, int64) {
	var (
		subtype *string
		mask    *string
	)
	if a.Subtype != nil {
		subtype = a.Subtype
	}
	if a.Mask != nil {
		mask = a.Mask
	}

	// Convert the floating-point balance to cents.
	rawCents := int64(math.Round(a.CurrentBalance * 100))

	// Checks if the account is a liability to display as negative balance.
	isLiability := isPlaidLiability(a.Type)
	balanceCents := rawCents
	if isLiability {
		balanceCents = -rawCents
	}

	// Checks if the account is cash or liability for net worth calculation.
	var cashDelta, investDelta, liabilityDelta int64
	if isLiability {
		liabilityDelta = rawCents
	} else if isPlaidInvestment(a.Type) {
		investDelta = rawCents
	} else if isPlaidCash(a.Type, subtype) {
		cashDelta = rawCents
	} else {
		cashDelta = rawCents
	}

	// Converts the Plaid account to the AccountJSON view model.
	account := AccountJSON{
		Provider:        "plaid",
		PlaidItemID:     &a.PlaidItemID,
		AccountID:       a.AccountID,
		Name:            plaidAccountDisplayName(a),
		InstitutionName: institutionName,
		Mask:            mask,
		Type:            a.Type,
		Subtype:         subtype,
		BalanceCents:    balanceCents,
		IsLiability:     isLiability,
	}

	return account, cashDelta, investDelta, liabilityDelta
}

// Returns the user-facing account name: custom display_name when set, else Plaid name.
func plaidAccountDisplayName(a database.PlaidAccount) string {
	if a.DisplayName != nil && strings.TrimSpace(*a.DisplayName) != "" {
		return strings.TrimSpace(*a.DisplayName)
	}
	return a.Name
}

// Returns true if the Plaid account should be treated as an investment.
func isPlaidInvestment(accountType string) bool {
	return accountType == "investment"
}

// Returns true if the Plaid account should be treated as a liability.
func isPlaidLiability(accountType string) bool {
	switch accountType {
	case "credit", "loan":
		return true
	default:
		return false
	}
}

// Returns true if the Plaid account should be classified as cash (HYSA, checking, CDs)
func isPlaidCash(accountType string, subtype *string) bool {
	if accountType != "depository" {
		return false
	}
	if subtype == nil {
		return true
	}

	switch *subtype {
	case "checking", "savings", "money market", "cd":
		return true
	default:
		return false
	}
}

/*
// Load Snaptrade Accounts from the database
func loadSnaptradeAccounts(a snaptrade.Account) (AccountJSON, int64) {
	balanceCents := int64(math.Round(a.BalanceAmount * 100))

	// Extract last 4 digits from account number for mask if available.
	var mask *string
	if len(a.Number) >= 4 {
		last4 := a.Number[len(a.Number)-4:]
		mask = &last4
	}

	account := AccountJSON{
		Provider:     "snaptrade",
		PlaidItemID:  nil,
		AccountID:    a.ID,
		Name:         a.Name,
		Mask:         mask,
		Type:         "investment",
		Subtype:      nil,
		BalanceCents: balanceCents,
		IsLiability:  false,
	}

	return account, balanceCents
}
*/
