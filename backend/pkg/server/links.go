package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/matthewtzong/portfolio-tracker/backend/pkg/database"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/plaid"
	"github.com/matthewtzong/portfolio-tracker/backend/pkg/serverauth"
)

// Client dependencies for the link management routes.
type apiDependencies struct {
	db          *database.Client
	plaidClient *plaid.Client
	// snaptradeClient *snaptrade.Client
}

// Exchange Plaid public token request format.
type exchangePlaidPublicTokenRequest struct {
	PublicToken     string `json:"publicToken"`
	InstitutionName string `json:"institutionName,omitempty"`
	InstitutionID   string `json:"institutionId,omitempty"`
}

// Remove Plaid item request format.
type removePlaidItemRequest struct {
	ItemID string `json:"itemId"`
}

// // Remove Snaptrade connection request format.
// type removeSnaptradeConnectionRequest struct {
// 	ConnectionID string `json:"connectionId"`
// }

// Reconnect Plaid item request format.
type reconnectPlaidItemRequest struct {
	ItemID string `json:"itemId"`
}

// Link token response format.
type linkTokenResponse struct {
	LinkToken string `json:"linkToken"`
}

// Exchange Plaid public token response format.
type exchangeTokenResponse struct {
	ItemID string `json:"itemId"`
}

// List links response format.
type linksResponse struct {
	PlaidItems    []database.PlaidItemJSON `json:"plaidItems"`
	FidelityItems []database.PlaidItemJSON `json:"fidelityItems"`
	// SnaptradeConnections []database.SnaptradeConnectionJSON `json:"snaptradeConnections"`
}

/*
// Connect URL response format.
type connectURLResponse struct {
	RedirectURI string `json:"redirectUri"`
}
*/

// Error response format.
type errorResponse struct {
	Error string `json:"error"`
}

// Registers the link management routes.
func registerLinkManagementRoutes(mux *http.ServeMux, deps apiDependencies) {
	// GET /api/plaid/link-token creates a Plaid link token.
	mux.Handle("/api/plaid/link-token", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleCreatePlaidLinkToken(w, r, deps)
	})))

	// POST /api/plaid/reconnect-link-token creates a Plaid link token in update mode for reconnecting.
	mux.Handle("/api/plaid/reconnect-link-token", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		handleCreatePlaidReconnectLinkToken(w, r, deps)
	})))

	// POST /api/plaid/exchange-token exchanges a Plaid public token for an access token and item ID.
	mux.Handle("/api/plaid/exchange-token", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		handleExchangePlaidPublicToken(w, r, deps)
	})))

	// GET /api/links lists all Plaid items.
	mux.Handle("/api/links", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		handleListLinks(w, r, deps)
	})))

	// POST /api/plaid/remove-item removes a Plaid item.
	mux.Handle("/api/plaid/remove-item", serverauth.JWTAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		handleRemovePlaidItem(w, r, deps)
	})))
}

// Creates a Plaid link token.
func handleCreatePlaidLinkToken(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.plaidClient == nil {
		writeJSONError(w, http.StatusInternalServerError, "Plaid is not configured (missing environment variables)")
		return
	}

	// Get the authenticated user ID.
	userID, ok := serverauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Get the webhook URL from environment.
	webhookURL := os.Getenv("PLAID_WEBHOOK_URL")

	// Get products from query parameter.
	productsQuery := r.URL.Query().Get("products")
	var products []string
	if productsQuery != "" {
		products = strings.Split(productsQuery, ",")
	} else {
		// Default to both if not specified.
		products = []string{"transactions", "investments"}
	}

	linkToken, err := deps.plaidClient.CreateLinkToken(r.Context(), userID, webhookURL, products)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to create Plaid link token: "+err.Error())
		return
	}

	resp := linkTokenResponse{LinkToken: linkToken}
	_ = json.NewEncoder(w).Encode(resp)
}

// Exchanges a Plaid public token for an access token and item ID.
func handleExchangePlaidPublicToken(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.plaidClient == nil {
		writeJSONError(w, http.StatusInternalServerError, "Plaid is not configured (missing environment variables)")
		return
	}

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "Database is not configured")
		return
	}

	// Parse the request body.
	var req exchangePlaidPublicTokenRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.PublicToken == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	accessToken, itemID, err := deps.plaidClient.ExchangePublicToken(r.Context(), req.PublicToken)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to exchange public token: "+err.Error())
		return
	}

	// Fetch the Plaid accounts.
	accounts, err := deps.plaidClient.GetAccounts(r.Context(), accessToken)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to fetch Plaid accounts: "+err.Error())
		return
	}

	now := GetLocalNow()

	// Check if an item with the same institution_id already exists (for reconnection / rotation).
	var existingItem *database.PlaidItem
	if req.InstitutionID != "" {
		existingItem, _ = deps.db.GetPlaidItemByInstitutionID(r.Context(), req.InstitutionID)
	}

	// Build the Plaid item fields for the database.
	item := &database.PlaidItem{
		ItemID:      itemID,
		AccessToken: accessToken,
		Status:      "OK",
		LastUpdated: now,
	}
	if req.InstitutionName != "" {
		item.InstitutionName = &req.InstitutionName
	}
	if req.InstitutionID != "" {
		item.InstitutionID = &req.InstitutionID
	}

	// If an item already exists for this institution, drop existing account and update the item.
	if existingItem != nil {
		_ = deps.db.DeletePlaidAccountsByItemID(r.Context(), existingItem.ItemID)

		err = deps.db.UpdatePlaidItemAfterReconnect(
			r.Context(),
			existingItem,
			itemID,
			accessToken,
			"OK",
			now,
			item.InstitutionID,
			item.InstitutionName,
		)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to update Plaid item: "+err.Error())
			return
		}
	} else {
		err = deps.db.UpsertPlaidItem(r.Context(), item)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save Plaid item: "+err.Error())
			return
		}
	}

	// Build the Plaid accounts for the database.
	var dbAccounts []database.PlaidAccount
	for _, a := range accounts {
		account := database.PlaidAccount{
			PlaidItemID:    itemID,
			AccountID:      a.AccountID,
			Name:           a.Name,
			Type:           a.Type,
			CurrentBalance: a.Balances.Current,
		}
		if a.Mask != "" {
			account.Mask = &a.Mask
		}
		if a.Subtype != "" {
			account.Subtype = &a.Subtype
		}
		dbAccounts = append(dbAccounts, account)
	}

	// Save accounts to DB, reconnecting will update existing accounts (upsert by account_id)
	if err := deps.db.UpsertPlaidAccounts(r.Context(), dbAccounts); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to save Plaid accounts: "+err.Error())
		return
	}

	// Trigger initial transaction sync immediately after linking/reconnecting.
	_ = SyncTransactionsForItem(r.Context(), deps.db, deps.plaidClient, item)

	// Backfill investment transactions for contribution-adjusted performance.
	_, _ = syncInvestmentTransactions(r.Context(), deps, GetLocalNow())

	// Return the item ID.
	resp := exchangeTokenResponse{ItemID: itemID}
	_ = json.NewEncoder(w).Encode(resp)
}

// Lists all Plaid items from the database.
func handleListLinks(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "Database is not configured")
		return
	}

	// Fetch Plaid items
	plaidItems, err := deps.db.ListPlaidItems(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list Plaid items: "+err.Error())
		return
	}

	// Fetch Snaptrade connections
	// snapConns, err := deps.db.ListSnaptradeConnections(r.Context())
	// if err != nil {
	// 	writeJSONError(w, http.StatusInternalServerError, "failed to list Snaptrade connections: "+err.Error())
	// 	return
	// }

	// Convert to JSON-safe representations and split out manual items.
	plaidJSON := []database.PlaidItemJSON{}
	fidelityJSON := []database.PlaidItemJSON{}
	for _, item := range plaidItems {
		if item.ItemID == "fidelity_manual_item" {
			fidelityJSON = append(fidelityJSON, item.ToJSON())
		} else {
			plaidJSON = append(plaidJSON, item.ToJSON())
		}
	}
	// snapJSON := []database.SnaptradeConnectionJSON{}
	// for _, conn := range snapConns {
	// 	snapJSON = append(snapJSON, conn.ToJSON())
	// }

	resp := linksResponse{
		PlaidItems: plaidJSON,
		// SnaptradeConnections: snapJSON,
		FidelityItems: fidelityJSON,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Removes a Plaid item.
func handleRemovePlaidItem(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "Database is not configured")
		return
	}

	// Parse the request body.
	var req removePlaidItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ItemID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Fetch the item from DB to get the access_token.
	item, err := deps.db.GetPlaidItemByItemID(r.Context(), req.ItemID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get Plaid item: "+err.Error())
		return
	}
	if item == nil {
		writeJSONError(w, http.StatusNotFound, "Plaid item not found")
		return
	}

	// Remove the item from Plaid.
	if deps.plaidClient != nil && item.AccessToken != "manual" {
		if err := deps.plaidClient.RemoveItem(r.Context(), item.AccessToken); err != nil {
			writeJSONError(w, http.StatusBadGateway, "failed to remove Plaid item: "+err.Error())
			return
		}
	}

	// Delete the item from the database.
	if err := deps.db.DeletePlaidAccountsByItemID(r.Context(), req.ItemID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to delete Plaid accounts: "+err.Error())
		return
	}

	// Delete the item from the database.
	if err := deps.db.DeletePlaidItem(r.Context(), req.ItemID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to delete Plaid item: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Writes a JSON error response to the response writer.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := errorResponse{Error: message}
	_ = json.NewEncoder(w).Encode(resp)
}

// Creates a Plaid link token in update mode for reconnecting an existing item.
func handleCreatePlaidReconnectLinkToken(w http.ResponseWriter, r *http.Request, deps apiDependencies) {
	w.Header().Set("Content-Type", "application/json")

	if deps.plaidClient == nil {
		writeJSONError(w, http.StatusInternalServerError, "Plaid is not configured (missing environment variables)")
		return
	}

	if deps.db == nil {
		writeJSONError(w, http.StatusInternalServerError, "Database is not configured")
		return
	}

	// Get the authenticated user ID.
	userID, ok := serverauth.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	// Parse the request body.
	var req reconnectPlaidItemRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.ItemID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get the existing item to retrieve its access token.
	existingItem, err := deps.db.GetPlaidItemByItemID(r.Context(), req.ItemID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to fetch Plaid item: "+err.Error())
		return
	}
	if existingItem == nil {
		writeJSONError(w, http.StatusNotFound, "Plaid item not found")
		return
	}

	// Get the webhook URL from environment.
	webhookURL := os.Getenv("PLAID_WEBHOOK_URL")

	// Create a link token in update mode using the existing access token.
	// We pass an empty slice for products because we just want to re-authorize the existing products.
	linkToken, err := deps.plaidClient.CreateLinkTokenWithAccessToken(r.Context(), userID, existingItem.AccessToken, webhookURL, []string{})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "failed to create reconnect link token: "+err.Error())
		return
	}

	resp := linkTokenResponse{LinkToken: linkToken}
	_ = json.NewEncoder(w).Encode(resp)
}

// Ensures only the allowed HTTP method is used.
func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
