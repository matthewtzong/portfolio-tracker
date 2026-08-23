-- Plaid investment transactions (for contribution-adjusted portfolio performance).

CREATE TABLE IF NOT EXISTS investment_transactions (
  id BIGSERIAL PRIMARY KEY,
  plaid_investment_transaction_id TEXT NOT NULL UNIQUE,
  account_id TEXT NOT NULL,
  date DATE NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  subtype TEXT NOT NULL DEFAULT '',
  amount_cents BIGINT NOT NULL,
  quantity NUMERIC(20, 8),
  price NUMERIC(20, 8),
  security_id TEXT,
  symbol TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS investment_transactions_date_idx
  ON investment_transactions (date);

CREATE INDEX IF NOT EXISTS investment_transactions_account_date_idx
  ON investment_transactions (account_id, date);
