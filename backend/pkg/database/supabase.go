package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Represents a DATE column in Postgres.
type DateOnly struct {
	time.Time
}

// Parses either a full RFC3339 timestamp or a date-only string (YYYY-MM-DD).
func (d *DateOnly) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}

	// Try full RFC3339 first (e.g. "2006-01-02T15:04:05Z07:00").
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		d.Time = t
		return nil
	}

	// Fallback to date-only layout used by DATE columns.
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("invalid date %q: %w", s, err)
	}
	d.Time = t
	return nil
}

// Returns a date-only string (YYYY-MM-DD) for DATE columns.
func (d DateOnly) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Time.Format("2006-01-02"))
}

// Supabase client for database operations.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// Creates a Supabase client from environment variables.
func NewClientFromEnv() (*Client, error) {
	url := strings.TrimSuffix(os.Getenv("SUPABASE_URL"), "/")
	apiKey := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")

	if url == "" || apiKey == "" {
		return nil, errors.New("SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY must be set")
	}

	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    url,
		apiKey:     apiKey,
	}, nil
}

// Returns the PostgREST URL for a table.
func (c *Client) restURL(table string) string {
	return c.baseURL + "/rest/v1/" + table
}

// Executes an HTTP request with Supabase auth headers.
func (c *Client) doRequest(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	// If there is a body, marshal it into a JSON reader.
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	// Creates a new request with the context, method, URL, and body reader.
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	// Sets the headers for the request.
	req.Header.Set("apikey", c.apiKey)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	prefer := "return=representation"
	if method == http.MethodPost && strings.Contains(url, "on_conflict=") {
		prefer += ",resolution=merge-duplicates"
	}
	req.Header.Set("Prefer", prefer)

	return c.httpClient.Do(req)
}

// Returns the JSON-safe representation of a PlaidItem.
func (p *PlaidItem) ToJSON() PlaidItemJSON {
	name := ""
	if p.InstitutionName != nil {
		name = *p.InstitutionName
	}

	return PlaidItemJSON{
		ItemID:          p.ItemID,
		InstitutionName: name,
		Status:          p.Status,
		LastUpdated:     p.LastUpdated,
	}
}

// Inserts or updates a Plaid item.
func (c *Client) UpsertPlaidItem(ctx context.Context, item *PlaidItem) error {
	url := c.restURL("plaid_items") + "?on_conflict=item_id"

	resp, err := c.doRequest(ctx, http.MethodPost, url, item)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert plaid_items failed: %s", string(body))
	}
	return nil
}

// Updates only the status and last_updated fields for an existing Plaid item by item_id.
func (c *Client) UpdatePlaidItemStatus(ctx context.Context, itemID, status string, lastUpdated time.Time) error {
	if itemID == "" {
		return errors.New("plaid item_id must be non-empty for status update")
	}

	payload := map[string]interface{}{
		"status":       status,
		"last_updated": lastUpdated,
	}

	url := c.restURL("plaid_items") + "?item_id=eq." + url.QueryEscape(itemID)
	resp, err := c.doRequest(ctx, http.MethodPatch, url, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase update plaid_item status failed: %s", string(body))
	}
	return nil
}

// Updates core fields on an existing Plaid item after reconnect/rotation.
func (c *Client) UpdatePlaidItemAfterReconnect(ctx context.Context, existing *PlaidItem, newItemID, accessToken, status string, lastUpdated time.Time, institutionID, institutionName *string) error {
	if existing == nil {
		return errors.New("existing plaid item is nil")
	}

	// Creates the payload for the update.
	payload := map[string]interface{}{
		"item_id":      newItemID,
		"access_token": accessToken,
		"status":       status,
		"last_updated": lastUpdated,
	}
	if institutionID != nil {
		payload["institution_id"] = *institutionID
	}
	if institutionName != nil {
		payload["institution_name"] = *institutionName
	}

	// Patch by primary key id to avoid conflicts on item_id uniqueness.
	url := c.restURL("plaid_items") + "?id=eq." + fmt.Sprintf("%d", existing.ID)
	resp, err := c.doRequest(ctx, http.MethodPatch, url, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase update plaid_item after reconnect failed: %s", string(body))
	}
	return nil
}

// Returns all Plaid items.
func (c *Client) ListPlaidItems(ctx context.Context) ([]PlaidItem, error) {
	url := c.restURL("plaid_items") + "?order=last_updated.desc"

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list plaid_items failed: %s", string(body))
	}

	// Decodes the response body into a slice of Plaid items.
	var items []PlaidItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

// Returns a Plaid item by its Plaid item_id.
func (c *Client) GetPlaidItemByItemID(ctx context.Context, itemID string) (*PlaidItem, error) {
	url := c.restURL("plaid_items") + "?item_id=eq." + itemID

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get plaid_item failed: %s", string(body))
	}

	// Decodes the response body into a Plaid item.
	var items []PlaidItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// Returns a Plaid item by institution_id (for detecting existing connections).
func (c *Client) GetPlaidItemByInstitutionID(ctx context.Context, institutionID string) (*PlaidItem, error) {
	url := c.restURL("plaid_items") + "?institution_id=eq." + institutionID + "&limit=1"

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get plaid_item by institution_id failed: %s", string(body))
	}

	// Decodes the response body into a Plaid item
	var items []PlaidItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

// Deletes a Plaid item by its Plaid item_id.
func (c *Client) DeletePlaidItem(ctx context.Context, itemID string) error {
	url := c.restURL("plaid_items") + "?item_id=eq." + itemID

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete plaid_item failed: %s", string(body))
	}
	return nil
}

// Inserts or updates multiple Plaid accounts.
func (c *Client) UpsertPlaidAccounts(ctx context.Context, accounts []PlaidAccount) error {
	if len(accounts) == 0 {
		return nil
	}

	url := c.restURL("plaid_accounts") + "?on_conflict=account_id"

	resp, err := c.doRequest(ctx, http.MethodPost, url, accounts)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert plaid_accounts failed: %s", string(body))
	}
	return nil
}

// Deletes all accounts for a Plaid item.
func (c *Client) DeletePlaidAccountsByItemID(ctx context.Context, itemID string) error {
	url := c.restURL("plaid_accounts") + "?plaid_item_id=eq." + itemID

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete plaid_accounts failed: %s", string(body))
	}
	return nil
}

// Returns all Plaid accounts.
func (c *Client) ListPlaidAccounts(ctx context.Context) ([]PlaidAccount, error) {
	url := c.restURL("plaid_accounts")

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list plaid_accounts failed: %s", string(body))
	}

	// Decodes the response body into a slice of Plaid accounts.
	var accounts []PlaidAccount
	err = json.NewDecoder(resp.Body).Decode(&accounts)
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// Returns a Plaid account by its Plaid account_id.
func (c *Client) GetPlaidAccountByAccountID(ctx context.Context, accountID string) (*PlaidAccount, error) {
	if accountID == "" {
		return nil, errors.New("plaid account_id must be non-empty")
	}

	reqURL := c.restURL("plaid_accounts") + "?account_id=eq." + url.QueryEscape(accountID)
	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get plaid_account failed: %s", string(body))
	}

	var accounts []PlaidAccount
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	return &accounts[0], nil
}

// Updates the display_name for a Plaid account. Pass nil to clear the custom name.
func (c *Client) UpdatePlaidAccountDisplayName(ctx context.Context, accountID string, displayName *string) error {
	if accountID == "" {
		return errors.New("plaid account_id must be non-empty for display name update")
	}

	payload := map[string]interface{}{
		"display_name": displayName,
	}

	reqURL := c.restURL("plaid_accounts") + "?account_id=eq." + url.QueryEscape(accountID)
	resp, err := c.doRequest(ctx, http.MethodPatch, reqURL, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase update plaid_account display_name failed: %s", string(body))
	}
	return nil
}

/*
// Converts a SnaptradeConnection to its JSON-safe representation.
func (s *SnaptradeConnection) ToJSON() SnaptradeConnectionJSON {
	return SnaptradeConnectionJSON{
		ID:         s.ConnID,
		Brokerage:  s.Brokerage,
		Status:     s.Status,
		LastSynced: s.LastSynced,
	}
}

// Returns the Snaptrade user.
func (c *Client) GetSnaptradeUser(ctx context.Context) (*SnaptradeUser, error) {
	url := c.restURL("snaptrade_user") + "?limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}

	// Decodes the response body into a Snaptrade user.
	var users []SnaptradeUser
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil || len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

// Inserts a new Snaptrade user into the database.
func (c *Client) CreateSnaptradeUser(ctx context.Context, userID, userSecret string) (*SnaptradeUser, error) {
	user := &SnaptradeUser{
		UserID:     userID,
		UserSecret: userSecret,
	}

	url := c.restURL("snaptrade_user")
	resp, err := c.doRequest(ctx, http.MethodPost, url, user)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase insert snaptrade_user failed: %s", string(body))
	}

	return user, nil
}

// Upserts or Delete Snaptrade connections
func (c *Client) UpsertSnaptradeConnections(ctx context.Context, conns []SnaptradeConnection) error {
	// If no connections from API, delete all.
	if len(conns) == 0 {
		deletionURL := c.restURL("snaptrade_connections")
		deletionResponse, err := c.doRequest(ctx, http.MethodDelete, deletionURL+"?id=gt.0", nil)
		if err != nil {
			return err
		}
		deletionResponse.Body.Close()
		return nil
	}

	// Upsert by conn_id (update existing, insert new).
	url := c.restURL("snaptrade_connections") + "?on_conflict=conn_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, conns)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert snaptrade_connections failed: %s", string(body))
	}

	// Delete connections no longer returned by the API.
	existingIDs := make([]string, 0, len(conns))
	for _, conn := range conns {
		existingIDs = append(existingIDs, conn.ConnID)
	}
	return c.deleteSnaptradeConnectionsNotIn(ctx, existingIDs)
}

// Deletes Snaptrade connections not in the list of existing IDs.
func (c *Client) deleteSnaptradeConnectionsNotIn(ctx context.Context, existingIDs []string) error {
	if len(existingIDs) == 0 {
		return nil
	}

	// PostgREST: conn_id=not.in.(existingIDs)
	var b strings.Builder
	b.WriteString("not.in.(")
	for i, id := range existingIDs {
		if i > 0 {
			b.WriteString(",")
		}
		escaped := strings.ReplaceAll(id, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		b.WriteString("\"" + escaped + "\"")
	}
	b.WriteString(")")

	// Delete the connections
	deletionURL := c.restURL("snaptrade_connections") + "?conn_id=" + url.QueryEscape(b.String())
	deletionResponse, err := c.doRequest(ctx, http.MethodDelete, deletionURL, nil)
	if err != nil {
		return err
	}
	defer deletionResponse.Body.Close()
	if deletionResponse.StatusCode < 200 || deletionResponse.StatusCode >= 300 {
		body, _ := io.ReadAll(deletionResponse.Body)
		return fmt.Errorf("supabase delete snaptrade_connections not in failed: %s", string(body))
	}
	return nil
}

// Updates the status and last_synced for existing connections.
func (c *Client) UpdateSnaptradeConnectionStatuses(ctx context.Context, conns []SnaptradeConnection) error {
	for _, conn := range conns {
		payload := map[string]interface{}{
			"status":      conn.Status,
			"last_synced": conn.LastSynced,
		}

		// Patch the connection by conn_id.
		patchURL := c.restURL("snaptrade_connections") + "?conn_id=eq." + url.QueryEscape(conn.ConnID)
		resp, err := c.doRequest(ctx, http.MethodPatch, patchURL, payload)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("supabase patch snaptrade_connection %s failed: %s", conn.ConnID, string(body))
		}
	}
	return nil
}

// Returns all Snaptrade connections.
func (c *Client) ListSnaptradeConnections(ctx context.Context) ([]SnaptradeConnection, error) {
	url := c.restURL("snaptrade_connections") + "?order=last_synced.desc"

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list snaptrade_connections failed: %s", string(body))
	}

	// Decodes the response body into a slice of Snaptrade connections.
	var conns []SnaptradeConnection
	if err := json.NewDecoder(resp.Body).Decode(&conns); err != nil {
		return nil, err
	}
	return conns, nil
}

// Deletes a Snaptrade connection by its connection ID.
func (c *Client) DeleteSnaptradeConnection(ctx context.Context, connID string) error {
	url := c.restURL("snaptrade_connections") + "?conn_id=eq." + connID

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete snaptrade_connection failed: %s", string(body))
	}
	return nil
}
*/

// Returns all name-based category rules.
func (c *Client) ListCategoryRules(ctx context.Context) ([]CategoryRule, error) {
	url := c.restURL("category_rules") + "?order=id.asc"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list category_rules failed: %s", string(body))
	}

	// Decodes the response body into a slice of category rules.
	var categoryRules []CategoryRule
	if err := json.NewDecoder(resp.Body).Decode(&categoryRules); err != nil {
		return nil, err
	}
	return categoryRules, nil
}

// Updates transactions_cursor and new_transactions flag for a Plaid item.
func (c *Client) UpdatePlaidItemCursorAndPending(ctx context.Context, itemID, cursor string, pending bool) error {
	payload := map[string]interface{}{
		"transactions_cursor":      cursor,
		"new_transactions_pending": pending,
	}

	// Updates the Plaid item by item_id.
	url := c.restURL("plaid_items") + "?item_id=eq." + url.QueryEscape(itemID)
	resp, err := c.doRequest(ctx, http.MethodPatch, url, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase update plaid_item cursor/pending failed: %s", string(body))
	}
	return nil
}

// Sets new_transactions flag for a Plaid item (used by webhook).
func (c *Client) SetItemNewTransactionsPending(ctx context.Context, itemID string, pending bool) error {
	payload := map[string]interface{}{"new_transactions_pending": pending}
	url := c.restURL("plaid_items") + "?item_id=eq." + url.QueryEscape(itemID)
	resp, err := c.doRequest(ctx, http.MethodPatch, url, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase set new_transactions_pending failed: %s", string(body))
	}
	return nil
}

// Returns all Plaid items that received new transactions.
func (c *Client) ListPlaidItemsWithPendingTransactions(ctx context.Context) ([]PlaidItem, error) {
	url := c.restURL("plaid_items") + "?new_transactions_pending=eq.true"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list plaid_items pending failed: %s", string(body))
	}
	var items []PlaidItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

// Returns all categories.
func (c *Client) ListCategories(ctx context.Context) ([]Category, error) {
	url := c.restURL("categories")
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list categories failed: %s", string(body))
	}
	var list []Category
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// Upserts transactions by their Plaid_transaction_id.
func (c *Client) UpsertTransactions(ctx context.Context, txns []Transaction) error {
	if len(txns) == 0 {
		return nil
	}
	url := c.restURL("transactions") + "?on_conflict=plaid_transaction_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, txns)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert transactions failed: %s", string(body))
	}
	return nil
}

// Deletes transactions by their Plaid transaction_id.
func (c *Client) DeleteTransactionsByPlaidIDs(ctx context.Context, plaidIDs []string) error {
	if len(plaidIDs) == 0 {
		return nil
	}
	// Constructs the query
	var b strings.Builder
	b.WriteString("in.(")
	for i, id := range plaidIDs {
		if i > 0 {
			b.WriteString(",")
		}
		escaped := strings.ReplaceAll(id, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		b.WriteString("\"" + escaped + "\"")
	}
	b.WriteString(")")

	// Deletes the transactions
	url := c.restURL("transactions") + "?plaid_transaction_id=" + url.QueryEscape(b.String())
	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete transactions failed: %s", string(body))
	}
	return nil
}

// Returns transactions for the given month, optionally filtered by category and search.
func (c *Client) ListTransactions(ctx context.Context, f ListTransactionsFilter) ([]Transaction, error) {
	// Build query: order by date desc
	reqURL := c.restURL("transactions") + "?order=date.desc"
	if f.Month != "" {
		start := f.Month + "-01"
		reqURL += "&date=gte." + start + "&date=lte." + endOfMonth(f.Month)
	}
	if f.CategoryID != nil {
		reqURL += "&category_id=eq." + fmt.Sprintf("%d", *f.CategoryID)
	}
	if f.Search != "" {
		pattern := "%" + f.Search + "%"
		reqURL += "&or=(name.ilike." + url.QueryEscape(pattern) + ",merchant_name.ilike." + url.QueryEscape(pattern) + ")"
	}
	if f.ReviewStatus != nil && *f.ReviewStatus != "" {
		reqURL += "&review_status=eq." + url.QueryEscape(*f.ReviewStatus)
	}

	// Gets the transactions
	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list transactions failed: %s", string(body))
	}

	// Decodes the response body into a slice of transactions.
	var list []Transaction
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// Returns a transaction by internal ID.
func (c *Client) GetTransactionByID(ctx context.Context, id int64) (*Transaction, error) {
	reqURL := c.restURL("transactions") + fmt.Sprintf("?id=eq.%d", id)
	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get transaction failed: %s", string(body))
	}
	var list []Transaction
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// Returns transactions keyed by Plaid transaction ID.
func (c *Client) GetTransactionsByPlaidIDs(ctx context.Context, plaidIDs []string) (map[string]Transaction, error) {
	result := make(map[string]Transaction, len(plaidIDs))
	if len(plaidIDs) == 0 {
		return result, nil
	}
	var b strings.Builder
	b.WriteString("in.(")
	for i, id := range plaidIDs {
		if i > 0 {
			b.WriteString(",")
		}
		escaped := strings.ReplaceAll(id, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		b.WriteString("\"" + escaped + "\"")
	}
	b.WriteString(")")
	reqURL := c.restURL("transactions") + "?plaid_transaction_id=" + url.QueryEscape(b.String())
	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get transactions by plaid ids failed: %s", string(body))
	}
	var list []Transaction
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	for _, transaction := range list {
		result[transaction.PlaidTransactionID] = transaction
	}
	return result, nil
}

// Updates category, review status, and optional note for a transaction.
func (c *Client) UpdateTransactionReview(ctx context.Context, id int64, categoryID int64, reviewStatus string, userNote *string) error {
	payload := map[string]interface{}{
		"category_id":   categoryID,
		"review_status": reviewStatus,
		"user_note":     userNote,
		"updated_at":    time.Now().UTC().Format(time.RFC3339),
	}
	reqURL := c.restURL("transactions") + fmt.Sprintf("?id=eq.%d", id)
	resp, err := c.doRequest(ctx, http.MethodPatch, reqURL, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase update transaction review failed: %s", string(body))
	}
	return nil
}

// Returns the last day of the month.
func endOfMonth(month string) string {
	if len(month) != 7 {
		return month + "-31"
	}
	parts := strings.Split(month, "-")
	if len(parts) != 2 {
		return month + "-31"
	}
	var y, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &y); err != nil {
		return month + "-31"
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return month + "-31"
	}
	// Adds one month and subtracts one day to get the last day of the month.
	t := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	t = t.AddDate(0, 1, -1)
	return t.Format("2006-01-02")
}

// Returns the current budget.
func (c *Client) GetBudget(ctx context.Context) (*Budget, error) {
	url := c.restURL("budgets") + "?id=eq.1&limit=1"

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get budget failed: %s", string(body))
	}

	// Decodes the response body into a Budget slice
	var budgets []Budget
	if err := json.NewDecoder(resp.Body).Decode(&budgets); err != nil {
		return nil, err
	}
	if len(budgets) == 0 {
		return nil, nil
	}
	return &budgets[0], nil
}

// Inserts or updates the current budget.
func (c *Client) UpsertBudget(ctx context.Context, budget *Budget) error {
	if budget == nil {
		return errors.New("budget is nil")
	}

	url := c.restURL("budgets") + "?id=eq." + fmt.Sprintf("%d", budget.ID)
	resp, err := c.doRequest(ctx, http.MethodPatch, url, budget)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert budget failed: %s", string(body))
	}
	return nil
}

// Inserts or updates a daily snapshot.
func (c *Client) UpsertDailySnapshot(ctx context.Context, snapshot *DailySnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}

	url := c.restURL("daily_snapshots") + "?on_conflict=date"
	resp, err := c.doRequest(ctx, http.MethodPost, url, snapshot)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert daily_snapshots failed: %s", string(body))
	}
	return nil
}

// Lists daily snapshots within a date range (should be for last 30 days).
func (c *Client) ListDailySnapshots(ctx context.Context, startDate, endDate time.Time) ([]DailySnapshot, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("daily_snapshots") + fmt.Sprintf("?date=gte.%s&date=lte.%s&order=date.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list daily_snapshots failed: %s", string(body))
	}

	// Decodes the response body into a slice of daily snapshots.
	var snapshots []DailySnapshot
	err = json.NewDecoder(resp.Body).Decode(&snapshots)
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// Inserts or updates a daily holding.
func (c *Client) UpsertDailyHolding(ctx context.Context, holding *DailyHolding) error {
	if holding == nil {
		return errors.New("holding is nil")
	}

	url := c.restURL("daily_holdings") + "?on_conflict=date,account_id,symbol"
	resp, err := c.doRequest(ctx, http.MethodPost, url, holding)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert daily_holdings failed: %s", string(body))
	}
	return nil
}

// Lists daily holdings within a date range (should be for last 30 days).
func (c *Client) ListDailyHoldings(ctx context.Context, startDate, endDate time.Time) ([]DailyHolding, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("daily_holdings") + fmt.Sprintf("?date=gte.%s&date=lte.%s&order=date.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list daily_holdings failed: %s", string(body))
	}

	// Decodes the response body into a slice of daily holdings.
	var holdings []DailyHolding
	if err := json.NewDecoder(resp.Body).Decode(&holdings); err != nil {
		return nil, err
	}
	return holdings, nil
}

// Lists daily holdings for a specific account (should be for last 30 days).
func (c *Client) ListDailyHoldingsByAccount(ctx context.Context, accountID string, startDate, endDate time.Time) ([]DailyHolding, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("daily_holdings") + fmt.Sprintf("?account_id=eq.%s&date=gte.%s&date=lte.%s&order=date.asc", accountID, startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list daily_holdings by account failed: %s", string(body))
	}

	// Decodes the response body into a slice of daily holdings.
	var holdings []DailyHolding
	err = json.NewDecoder(resp.Body).Decode(&holdings)
	if err != nil {
		return nil, err
	}
	return holdings, nil
}

// Lists daily holdings for a specific symbol (should be for last 30 days).
func (c *Client) ListDailyHoldingsBySymbol(ctx context.Context, symbol string, startDate, endDate time.Time) ([]DailyHolding, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("daily_holdings") + fmt.Sprintf("?symbol=eq.%s&date=gte.%s&date=lte.%s&order=date.asc", symbol, startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list daily_holdings by symbol failed: %s", string(body))
	}

	var holdings []DailyHolding
	if err := json.NewDecoder(resp.Body).Decode(&holdings); err != nil {
		return nil, err
	}
	return holdings, nil
}

// Returns the latest date present in the daily_holdings table.
func (c *Client) GetLatestDailyHoldingsDate(ctx context.Context) (*time.Time, error) {
	url := c.restURL("daily_holdings") + "?select=date&order=date.desc&limit=1"

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}

	var results []struct {
		Date DateOnly `json:"date"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return nil, nil
	}

	return &results[0].Date.Time, nil
}

// Returns the latest date present in the daily_holdings table for a specific account.
func (c *Client) GetLatestDailyHoldingsDateForAccount(ctx context.Context, accountID string) (*time.Time, error) {
	url := c.restURL("daily_holdings") + fmt.Sprintf("?account_id=eq.%s&select=date&order=date.desc&limit=1", accountID)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}

	var results []struct {
		Date DateOnly `json:"date"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return nil, nil
	}

	return &results[0].Date.Time, nil
}

// Inserts or updates a monthly snapshot.
func (c *Client) UpsertMonthlySnapshot(ctx context.Context, snapshot *MonthlySnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}

	url := c.restURL("monthly_snapshots") + "?on_conflict=month,account_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, snapshot)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert monthly_snapshots failed: %s", string(body))
	}
	return nil
}

// Lists monthly snapshots within a month range.
func (c *Client) ListMonthlySnapshots(ctx context.Context, startMonth, endMonth time.Time) ([]MonthlySnapshot, error) {
	startStr := startMonth.Format("2006-01-02")
	endStr := endMonth.Format("2006-01-02")
	url := c.restURL("monthly_snapshots") + fmt.Sprintf("?month=gte.%s&month=lte.%s&order=month.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list monthly_snapshots failed: %s", string(body))
	}

	var snapshots []MonthlySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// Lists monthly snapshots for a single account within a month range.
func (c *Client) ListMonthlySnapshotsByAccount(ctx context.Context, startMonth, endMonth time.Time, accountID string) ([]MonthlySnapshot, error) {
	startStr := startMonth.Format("2006-01-02")
	endStr := endMonth.Format("2006-01-02")
	url := c.restURL("monthly_snapshots") + fmt.Sprintf("?account_id=eq.%s&month=gte.%s&month=lte.%s&order=month.asc",
		url.QueryEscape(accountID), startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list monthly_snapshots by account failed: %s", string(body))
	}

	var snapshots []MonthlySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// Deletes all transactions in the given month.
func (c *Client) DeleteTransactionsInMonth(ctx context.Context, monthStart time.Time) error {
	monthEnd := monthStart.AddDate(0, 1, -1)
	startStr := monthStart.Format("2006-01-02")
	endStr := monthEnd.Format("2006-01-02")
	url := c.restURL("transactions") + "?date=gte." + startStr + "&date=lte." + endStr

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete transactions in month failed: %s", string(body))
	}
	return nil
}

// Deletes daily snapshots older than the given date.
func (c *Client) DeleteDailySnapshotsOlderThan(ctx context.Context, cutoffDate time.Time) error {
	cutoffStr := cutoffDate.Format("2006-01-02")
	url := c.restURL("daily_snapshots") + "?date=lt." + cutoffStr

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete daily_snapshots older than failed: %s", string(body))
	}
	return nil
}

// Deletes daily holdings older than the given date.
func (c *Client) DeleteDailyHoldingsOlderThan(ctx context.Context, cutoffDate time.Time) error {
	cutoffStr := cutoffDate.Format("2006-01-02")
	url := c.restURL("daily_holdings") + "?date=lt." + cutoffStr

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete daily_holdings older than failed: %s", string(body))
	}
	return nil
}

// Deletes monthly snapshots for a given year.
func (c *Client) DeleteMonthlySnapshotsForYear(ctx context.Context, year int) error {
	yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	startStr := yearStart.Format("2006-01-02")
	endStr := yearEnd.Format("2006-01-02")
	url := c.restURL("monthly_snapshots") + fmt.Sprintf("?month=gte.%s&month=lte.%s", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete monthly_snapshots for year failed: %s", string(body))
	}
	return nil
}

// Upserts a monthly expense summary.
func (c *Client) UpsertMonthlyExpenseSummary(ctx context.Context, summary *MonthlyExpenseSummary) error {
	if summary == nil {
		return errors.New("summary is nil")
	}

	url := c.restURL("monthly_expense_summary") + "?on_conflict=month,category_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, summary)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert monthly_expense_summary failed: %s", string(body))
	}
	return nil
}

// Upserts a yearly expense summary.
func (c *Client) UpsertYearlyExpenseSummary(ctx context.Context, summary *YearlyExpenseSummary) error {
	if summary == nil {
		return errors.New("summary is nil")
	}

	url := c.restURL("yearly_expense_summary") + "?on_conflict=year,category_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, summary)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert yearly_expense_summary failed: %s", string(body))
	}
	return nil
}

// Upserts a yearly portfolio summary.
func (c *Client) UpsertYearlyPortfolioSummary(ctx context.Context, summary *YearlyPortfolioSummary) error {
	if summary == nil {
		return errors.New("summary is nil")
	}

	url := c.restURL("yearly_portfolio_summary") + "?on_conflict=year,account_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, summary)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert yearly_portfolio_summary failed: %s", string(body))
	}
	return nil
}

// Lists transactions for a given month (for export before deletion).
func (c *Client) ListTransactionsForMonth(ctx context.Context, month time.Time) ([]Transaction, error) {
	startDate := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, -1)
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("transactions") + fmt.Sprintf("?date=gte.%s&date=lte.%s&order=date.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list transactions for month failed: %s", string(body))
	}

	// Decodes the response body into a slice of transactions.
	var transactions []Transaction
	err = json.NewDecoder(resp.Body).Decode(&transactions)
	if err != nil {
		return nil, err
	}
	return transactions, nil
}

// Lists monthly snapshots for a given year (for export before deletion).
func (c *Client) ListMonthlySnapshotsForYear(ctx context.Context, year int) ([]MonthlySnapshot, error) {
	yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	return c.ListMonthlySnapshots(ctx, yearStart, yearEnd)
}

// Lists monthly expense summaries within a date range.
func (c *Client) ListMonthlyExpenseSummaries(ctx context.Context, startDate, endDate time.Time) ([]MonthlyExpenseSummary, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("monthly_expense_summary") + fmt.Sprintf("?month=gte.%s&month=lte.%s&order=month.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list monthly_expense_summary failed: %s", string(body))
	}

	// Decodes the response body into a slice of monthly expense summaries.
	var summaries []MonthlyExpenseSummary
	err = json.NewDecoder(resp.Body).Decode(&summaries)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

// Lists yearly expense summaries for a given year.
func (c *Client) ListYearlyExpenseSummaries(ctx context.Context, year int) ([]YearlyExpenseSummary, error) {
	url := c.restURL("yearly_expense_summary") + fmt.Sprintf("?year=eq.%d&order=category_id.asc", year)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list yearly_expense_summary failed: %s", string(body))
	}

	var summaries []YearlyExpenseSummary
	err = json.NewDecoder(resp.Body).Decode(&summaries)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

// Lists yearly portfolio summaries for a given year.
func (c *Client) ListYearlyPortfolioSummaries(ctx context.Context, year int) ([]YearlyPortfolioSummary, error) {
	url := c.restURL("yearly_portfolio_summary") + fmt.Sprintf("?year=eq.%d&order=account_id.asc", year)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list yearly_portfolio_summary failed: %s", string(body))
	}

	var summaries []YearlyPortfolioSummary
	err = json.NewDecoder(resp.Body).Decode(&summaries)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

// Upserts a monthly net worth snapshot.
func (c *Client) UpsertMonthlyNetWorth(ctx context.Context, snapshot *MonthlyNetWorth) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}

	url := c.restURL("monthly_net_worth") + "?on_conflict=month"
	resp, err := c.doRequest(ctx, http.MethodPost, url, snapshot)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert monthly_net_worth failed: %s", string(body))
	}
	return nil
}

// Lists monthly net worth snapshots within a month range.
func (c *Client) ListMonthlyNetWorth(ctx context.Context, startMonth, endMonth time.Time) ([]MonthlyNetWorth, error) {
	startStr := startMonth.Format("2006-01-02")
	endStr := endMonth.Format("2006-01-02")
	url := c.restURL("monthly_net_worth") + fmt.Sprintf("?month=gte.%s&month=lte.%s&order=month.asc", startStr, endStr)

	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list monthly_net_worth failed: %s", string(body))
	}

	// Decodes the response body into a slice of monthly net worth snapshots.
	var snapshots []MonthlyNetWorth
	err = json.NewDecoder(resp.Body).Decode(&snapshots)
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

// Deletes all daily holdings for a specific account on a specific date.
func (c *Client) DeleteDailyHoldingsByAccountAndDate(ctx context.Context, accountID string, date time.Time) error {
	dateStr := date.Format("2006-01-02")
	url := c.restURL("daily_holdings") + "?account_id=eq." + url.QueryEscape(accountID) + "&date=eq." + dateStr

	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete daily_holdings by account/date failed: %s", string(body))
	}
	return nil
}

// Represents a row in the plaid_items table.
type PlaidItem struct {
	ID                     int64      `json:"id,omitempty"`
	ItemID                 string     `json:"item_id"`
	AccessToken            string     `json:"access_token"`
	InstitutionID          *string    `json:"institution_id,omitempty"`
	InstitutionName        *string    `json:"institution_name,omitempty"`
	Status                 string     `json:"status"`
	LastUpdated            time.Time  `json:"last_updated"`
	CreatedAt              *time.Time `json:"created_at,omitempty"`
	TransactionsCursor     *string    `json:"transactions_cursor,omitempty"`
	NewTransactionsPending bool       `json:"new_transactions_pending"`
}

// The JSON-safe representation for API responses (hides access_token).
type PlaidItemJSON struct {
	ItemID          string    `json:"itemId"`
	InstitutionName string    `json:"institutionName,omitempty"`
	Status          string    `json:"status"`
	LastUpdated     time.Time `json:"lastUpdated"`
}

// Represents a row in the plaid_accounts table.
type PlaidAccount struct {
	ID             int64      `json:"id,omitempty"`
	PlaidItemID    string     `json:"plaid_item_id"`
	AccountID      string     `json:"account_id"`
	Name           string     `json:"name"`
	DisplayName    *string    `json:"display_name,omitempty"`
	Mask           *string    `json:"mask,omitempty"`
	Type           string     `json:"type"`
	Subtype        *string    `json:"subtype,omitempty"`
	CurrentBalance float64    `json:"current_balance"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

/*
// Represents a row in the snaptrade_user table.
type SnaptradeUser struct {
	ID         int64  `json:"id,omitempty"`
	UserID     string `json:"user_id"`
	UserSecret string `json:"user_secret"`
}

// Represents a row in the snaptrade_connections table.
type SnaptradeConnection struct {
	ID         int64      `json:"id,omitempty"`
	ConnID     string     `json:"conn_id"`
	Brokerage  string     `json:"brokerage"`
	Status     string     `json:"status"`
	LastSynced *time.Time `json:"last_synced,omitempty"`
}

// The JSON-safe representation for API responses (hides conn_id).
type SnaptradeConnectionJSON struct {
	ID         string     `json:"id"`
	Brokerage  string     `json:"brokerage"`
	Status     string     `json:"status"`
	LastSynced *time.Time `json:"lastSynced,omitempty"`
}
*/

// Represents a row in the categories table.
type Category struct {
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name"`
	PlaidName *string `json:"plaid_name,omitempty"`
	Expense   bool    `json:"expense"`
}

// Category rule maps transaction name/merchant substring to a category.
// Optional PlaidAccountID scopes the rule to a single account (nil = any account).
type CategoryRule struct {
	ID             int64   `json:"id,omitempty"`
	MatchString    string  `json:"match_string"`
	CategoryID     int64   `json:"category_id"`
	PlaidAccountID *string `json:"plaid_account_id,omitempty"`
}

// Review status for transactions that need manual categorization (e.g. Venmo).
const (
	ReviewStatusNone        = "none"
	ReviewStatusPending     = "pending"
	ReviewStatusCategorized = "categorized"
)

// Represents a row in the transactions table.
type Transaction struct {
	ID                 int64     `json:"id,omitempty"`
	PlaidAccountID     string    `json:"plaid_account_id"`
	PlaidTransactionID string    `json:"plaid_transaction_id"`
	Date               DateOnly  `json:"date"`
	AmountCents        int64     `json:"amount_cents"`
	Name               string    `json:"name"`
	MerchantName       *string   `json:"merchant_name"`
	CategoryID         *int64    `json:"category_id"`
	ReviewStatus       string    `json:"review_status"`
	UserNote           *string   `json:"user_note,omitempty"`
	Pending            bool      `json:"pending"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

// Holds optional filters for listing transactions.
type ListTransactionsFilter struct {
	Month        string
	CategoryID   *int64
	Search       string
	ReviewStatus *string
}

// Represents a row in the budgets table.
type Budget struct {
	ID          int64            `json:"id,omitempty"`
	Allocations map[string]int64 `json:"allocations"`
	UpdatedAt   time.Time        `json:"updated_at,omitempty"`
}

// Represents a row in the daily_snapshots table
type DailySnapshot struct {
	ID                  int64      `json:"id,omitempty"`
	Date                DateOnly   `json:"date"`
	PortfolioValueCents int64      `json:"portfolio_value_cents"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the daily_holdings table.
type DailyHolding struct {
	ID             int64      `json:"id,omitempty"`
	Date           DateOnly   `json:"date"`
	AccountID      string     `json:"account_id"`
	Symbol         string     `json:"symbol"`
	Quantity       float64    `json:"quantity"`
	ValueCents     int64      `json:"value_cents"`
	CostBasisCents *int64     `json:"cost_basis_cents,omitempty"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the monthly_snapshots table.
type MonthlySnapshot struct {
	ID                  int64      `json:"id,omitempty"`
	Month               DateOnly   `json:"month"`
	AccountID           string     `json:"account_id"`
	PortfolioValueCents int64      `json:"portfolio_value_cents"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the monthly_net_worth table.
type MonthlyNetWorth struct {
	ID               int64      `json:"id,omitempty"`
	Month            DateOnly   `json:"month"`
	NetWorthCents    int64      `json:"net_worth_cents"`
	CashCents        int64      `json:"cash_cents"`
	InvestmentsCents int64      `json:"investments_cents"`
	LiabilitiesCents int64      `json:"liabilities_cents"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the monthly_expense_summary table.
type MonthlyExpenseSummary struct {
	ID               int64      `json:"id,omitempty"`
	Month            DateOnly   `json:"month"`
	CategoryID       int64      `json:"category_id"`
	TotalCents       int64      `json:"total_cents"`
	TransactionCount int        `json:"transaction_count"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the yearly_expense_summary table.
type YearlyExpenseSummary struct {
	ID               int64      `json:"id,omitempty"`
	Year             int        `json:"year"`
	CategoryID       int64      `json:"category_id"`
	TotalCents       int64      `json:"total_cents"`
	TransactionCount int        `json:"transaction_count"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the yearly_portfolio_summary table.
type YearlyPortfolioSummary struct {
	ID                  int64      `json:"id,omitempty"`
	Year                int        `json:"year"`
	AccountID           string     `json:"account_id"`
	PortfolioValueCents int64      `json:"portfolio_value_cents"`
	CreatedAt           *time.Time `json:"created_at,omitempty"`
}

// Represents a row in the securities table.
type Security struct {
	Symbol     string     `json:"symbol"`
	AssetClass string     `json:"asset_class"`
	Source     string     `json:"source"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

// Represents a row in the allocation_targets table.
type AllocationTarget struct {
	ID        int64      `json:"id,omitempty"`
	Kind      string     `json:"kind"`
	Key       string     `json:"key"`
	TargetBps int        `json:"target_bps"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Represents the singleton allocation_settings row.
type AllocationSettings struct {
	ID                int64      `json:"id"`
	DriftWarnBps      int        `json:"drift_warn_bps"`
	SingleStockMaxBps int        `json:"single_stock_max_bps"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

// Returns a security by symbol, or nil if not found.
func (c *Client) GetSecurity(ctx context.Context, symbol string) (*Security, error) {
	url := c.restURL("securities") + "?symbol=eq." + url.QueryEscape(symbol) + "&limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get security failed: %s", string(body))
	}

	var rows []Security
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Lists all securities.
func (c *Client) ListSecurities(ctx context.Context) ([]Security, error) {
	url := c.restURL("securities") + "?select=*&order=symbol.asc"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list securities failed: %s", string(body))
	}

	var rows []Security
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Inserts or updates a security row (merge on symbol).
func (c *Client) UpsertSecurity(ctx context.Context, security *Security) error {
	if security == nil {
		return errors.New("security is nil")
	}

	url := c.restURL("securities") + "?on_conflict=symbol"
	resp, err := c.doRequest(ctx, http.MethodPost, url, security)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert security failed: %s", string(body))
	}
	return nil
}

// Upserts a security unless an existing row has source=user (user overrides win).
func (c *Client) UpsertSecurityIfNotUser(ctx context.Context, security *Security) error {
	if security == nil {
		return errors.New("security is nil")
	}
	existing, err := c.GetSecurity(ctx, security.Symbol)
	if err != nil {
		return err
	}
	if existing != nil && existing.Source == "user" {
		return nil
	}
	return c.UpsertSecurity(ctx, security)
}

// Lists all allocation targets.
func (c *Client) ListAllocationTargets(ctx context.Context) ([]AllocationTarget, error) {
	url := c.restURL("allocation_targets") + "?select=*&order=id.asc"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list allocation_targets failed: %s", string(body))
	}

	var rows []AllocationTarget
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Deletes all allocation targets.
func (c *Client) DeleteAllAllocationTargets(ctx context.Context) error {
	url := c.restURL("allocation_targets") + "?id=gt.0"
	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase delete allocation_targets failed: %s", string(body))
	}
	return nil
}

// Inserts allocation targets (batch).
func (c *Client) InsertAllocationTargets(ctx context.Context, targets []AllocationTarget) error {
	if len(targets) == 0 {
		return nil
	}
	url := c.restURL("allocation_targets")
	resp, err := c.doRequest(ctx, http.MethodPost, url, targets)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase insert allocation_targets failed: %s", string(body))
	}
	return nil
}

// Returns allocation settings (singleton id=1), or nil if missing.
func (c *Client) GetAllocationSettings(ctx context.Context) (*AllocationSettings, error) {
	url := c.restURL("allocation_settings") + "?id=eq.1&limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get allocation_settings failed: %s", string(body))
	}

	var rows []AllocationSettings
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Updates allocation settings (id=1).
func (c *Client) UpsertAllocationSettings(ctx context.Context, settings *AllocationSettings) error {
	if settings == nil {
		return errors.New("settings is nil")
	}
	settings.ID = 1
	url := c.restURL("allocation_settings") + "?id=eq.1"
	resp, err := c.doRequest(ctx, http.MethodPatch, url, settings)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert allocation_settings failed: %s", string(body))
	}
	return nil
}

// Represents a row in the investment_transactions table.
type InvestmentTransaction struct {
	ID                           int64      `json:"id,omitempty"`
	PlaidInvestmentTransactionID string     `json:"plaid_investment_transaction_id"`
	AccountID                    string     `json:"account_id"`
	Date                         DateOnly   `json:"date"`
	Name                         string     `json:"name"`
	Type                         string     `json:"type"`
	Subtype                      string     `json:"subtype"`
	AmountCents                  int64      `json:"amount_cents"`
	Quantity                     *float64   `json:"quantity,omitempty"`
	Price                        *float64   `json:"price,omitempty"`
	SecurityID                   *string    `json:"security_id,omitempty"`
	Symbol                       *string    `json:"symbol,omitempty"`
	CreatedAt                    *time.Time `json:"created_at,omitempty"`
}

// Inserts or updates investment transactions by Plaid ID.
func (c *Client) UpsertInvestmentTransactions(ctx context.Context, txns []InvestmentTransaction) error {
	if len(txns) == 0 {
		return nil
	}
	url := c.restURL("investment_transactions") + "?on_conflict=plaid_investment_transaction_id"
	resp, err := c.doRequest(ctx, http.MethodPost, url, txns)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase upsert investment_transactions failed: %s", string(body))
	}
	return nil
}

// Lists investment transactions in [startDate, endDate], ordered by date ascending.
func (c *Client) ListInvestmentTransactions(ctx context.Context, startDate, endDate time.Time) ([]InvestmentTransaction, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")
	url := c.restURL("investment_transactions") + fmt.Sprintf(
		"?date=gte.%s&date=lte.%s&order=date.asc", startStr, endStr,
	)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase list investment_transactions failed: %s", string(body))
	}
	var txns []InvestmentTransaction
	if err := json.NewDecoder(resp.Body).Decode(&txns); err != nil {
		return nil, err
	}
	return txns, nil
}

// Returns the earliest daily snapshot, or nil if none exist.
func (c *Client) GetEarliestDailySnapshot(ctx context.Context) (*DailySnapshot, error) {
	url := c.restURL("daily_snapshots") + "?order=date.asc&limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get earliest daily_snapshots failed: %s", string(body))
	}
	var rows []DailySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Returns the latest daily snapshot, or nil if none exist.
func (c *Client) GetLatestDailySnapshot(ctx context.Context) (*DailySnapshot, error) {
	url := c.restURL("daily_snapshots") + "?order=date.desc&limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get latest daily_snapshots failed: %s", string(body))
	}
	var rows []DailySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Returns the earliest monthly snapshot month (any account), or nil if none exist.
func (c *Client) GetEarliestMonthlySnapshot(ctx context.Context) (*MonthlySnapshot, error) {
	url := c.restURL("monthly_snapshots") + "?order=month.asc&limit=1"
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase get earliest monthly_snapshots failed: %s", string(body))
	}
	var rows []MonthlySnapshot
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}
