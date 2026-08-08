## Portfolio Tracker

**Single‑user personal finance net worth tracker** for tracking net worth, expenses, budgets, and investment performance across banks, credit cards, and brokerages.

---

## What this app does

- **Aggregates financial data** from **Plaid** for banks, credit cards, and brokerages (balances, transactions, and holdings).
- **Calculates current net worth** in one place by combining:
  - Cash (checking, savings, money market, etc.).
  - Investments (brokerage accounts via Plaid).
  - Liabilities (credit cards, loans).
- **Tracks spending and budgets** using categorized Plaid transactions.
- **Tracks portfolio performance** over time with daily and monthly snapshots.
- **Visualizes everything** with charts for:
  - Net worth.
  - Portfolio value and per‑holding performance.
  - Expenses by category.
  - Budget vs. actual spend.
- **Supports manual CSV uploads** for Fidelity statements and holdings, ensuring accurate tracking even when APIs are limited.
- **Automatically prunes old data** while exporting it as CSV first, so the database stays small but long‑term summaries remain available.
  
---

## Key features

### Accounts and net worth

- **Unified accounts view** pulling from Plaid (banks, credit cards, and brokerage).
- Each account is classified as **cash**, **investment**, or **liability**.
- Net worth is computed as:
  - \(\text{cash} + \text{investments} - \text{liabilities}\)
- The dashboard shows:
  - Overall net worth.
  - Breakdown by type (cash, investments, liabilities).
  - A table of individual accounts with their balances.

### Transactions and expense tracking

- **Plaid transactions** are ingested via:
  - **Webhooks** that mark which items had new activity.
  - A **nightly cursor‑based sync** that fetches exactly the new/changed transactions for those items.
- Transactions are categorized using:
  - A **rules engine** (match on merchant or name) for special cases like Venmo, Fidelity, rent, etc.
  - Fallback to Plaid’s primary category.
  - A final **Uncategorized** bucket if nothing matches.
- The expense tracker UI lets you:
  - Select a month.
  - Filter by category.
  - Search by text.
  - See a monthly summary (income, expenses, invested, saved).

### Budget tracker

- A **monthly budget** is stored in the database as a single JSON allocation:
  - Same allocations apply to all months until changed.
- For any month you can see:
  - Budget vs. actual spend per category.
  - Over‑budget categories highlighted.
  - Totals for *budgeted*, *spent*, and *remaining*.

### Portfolio tracking (Plaid)

- Pulls **positions and account balances** from Plaid:
  - Per‑account totals.
  - Per‑holding value (symbol, quantity, value in cents).
- Stores **investment‑only snapshots**:
  - `daily_snapshots`: total portfolio value per day.
  - `daily_holdings`: per‑account, per‑symbol holdings per day.
  - `monthly_snapshots`: end‑of‑month per‑account portfolio value.
- The portfolio page shows:
  - Today’s total portfolio value.
  - Breakdown by account and by holding within each account.
- **Manual Fidelity Integration**: 
  - Supports two accounts - Brokerage and Roth IRAs
  - **Statement Uploads**: Supports uploading Fidelity brokerage statements (CSV) to retroactively fill historical monthly snapshots.
  - **Position Uploads**: Supports uploading current "Positions" CSV from Fidelity to update holdings and portfolio value.
  - **Seamless Merging**: Manual data is integrated with Plaid records to ensure a complete daily and monthly view even for accounts with limited API support.

### Charts and visualizations

- **Time‑series charts** for:
  - Portfolio value over the last 30 days (daily) and past 12 months (monthly).
  - Net worth over time (monthly).
- **Category charts** for:
  - Expenses by category for a given month (pie chart).
  - Budget vs. actual per category (bar chart).

### Retention, exports, and long‑term history

- **Retention rules** keep the database lean while preserving history:
  - Transactions are kept for **1 year**; older months are exported as CSV and summarized before deletion.
  - Portfolio **daily** snapshots are kept for **the last 30 days only**.
  - Portfolio **monthly** snapshots are kept for longer, then rolled up into **yearly** summaries.
- Before any delete, the app:
  - Builds CSV exports (transactions, portfolio snapshots/holdings).
  - Sends them via email (Resend) so there is an external archive.
- The UI exposes:
  - Manual on-demand CSV exports by month.
  - Yearly summary views for both expenses and portfolio.

---

## Architecture and design choices

- **Single‑user by design**
  - The entire stack assumes exactly one owner.
  - Backend validates Supabase JWTs and also checks against a single whitelisted email.

- **Stack**
  - **Backend**: Go HTTP server (JWT auth, Plaid client, cron + webhook endpoints).
  - **Frontend**: React + TypeScript + Vite, with Tailwind CSS and a small component library for charts.
  - **Database & Auth**: Supabase (Postgres + Auth).
  - **Deploy model**: Single deploy through Vercel where the Go server also serves the built React app.

- **Data flow**
  - **Plaid**
    - Webhook (`SYNC_UPDATES_AVAILABLE`) marks items with new transactions.
    - Nightly cron calls a cursor‑based `/transactions/sync` only for items that actually changed.
    - Nightly cron fetches accounts and positions and writes that day’s `daily_snapshots` and `daily_holdings`.
    - On month‑end, writes per‑account `monthly_snapshots`.
  - **Frontend**
    - Talks only to the Go API, which in turn talks to Supabase and Plaid.

- **Clean, testable code**
  - Business logic (categorization, net‑worth math, snapshot aggregation, retention) is implemented as small, test‑covered functions.
  - API routes are thin: they validate auth, call services, and return JSON.
  - The repo includes GitHub Actions CI that runs Go tests, frontend tests, lint, and build on each push.

For implementation details and the step‑by‑step vertical slices that shaped the app, see `TODO.md`,  `RETENTION.md`, `STATUS_CHECKING.md` and `.cursorrules`.

Snaptrade has stopped support for personal use of certain brokerages, so we have migrated investment tracking from Snaptrade to Plaid. SEe `MIGRATION_PLAID.md` for details.