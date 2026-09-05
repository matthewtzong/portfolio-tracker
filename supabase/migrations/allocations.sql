-- Securities classification (from Plaid Type) and allocation targets

CREATE TABLE IF NOT EXISTS securities (
  symbol TEXT PRIMARY KEY,
  asset_class TEXT NOT NULL CHECK (asset_class IN ('cash', 'etf', 'stock', 'other')),
  source TEXT NOT NULL CHECK (source IN ('plaid', 'heuristic', 'user')),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS allocation_targets (
  id BIGSERIAL PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('ticker', 'asset_class', 'group')),
  key TEXT NOT NULL,
  target_bps INT NOT NULL CHECK (target_bps >= 0 AND target_bps <= 10000),
  members TEXT[] NOT NULL DEFAULT '{}',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (kind, key)
);

-- Singleton settings for allocation warnings (id = 1).
CREATE TABLE IF NOT EXISTS allocation_settings (
  id BIGINT PRIMARY KEY,
  drift_warn_bps INT NOT NULL DEFAULT 500,
  single_stock_max_bps INT NOT NULL DEFAULT 1000,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO allocation_settings (id, drift_warn_bps, single_stock_max_bps)
VALUES (1, 500, 1000)
ON CONFLICT (id) DO NOTHING;
