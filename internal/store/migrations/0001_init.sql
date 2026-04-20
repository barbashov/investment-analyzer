CREATE TABLE transactions (
    id            INTEGER PRIMARY KEY,
    trade_hash    TEXT NOT NULL UNIQUE,
    source        TEXT NOT NULL,
    source_ref    TEXT,
    date          TEXT NOT NULL,
    time          TEXT,
    op_type       TEXT NOT NULL,
    op_label_ru   TEXT NOT NULL,
    asset_name    TEXT,
    ticker        TEXT,
    asset_class   TEXT,
    account       TEXT NOT NULL,
    amount        REAL NOT NULL,
    currency      TEXT NOT NULL,
    quantity      REAL,
    unit_price    REAL,
    comment       TEXT,
    div_tax       REAL,
    div_period    TEXT,
    created_at    TEXT NOT NULL
);

CREATE INDEX idx_tx_ticker_date ON transactions(ticker, date);
CREATE INDEX idx_tx_op_date     ON transactions(op_type, date);
CREATE INDEX idx_tx_account     ON transactions(account);

CREATE TABLE moex_dividends (
    ticker         TEXT NOT NULL,
    registry_date  TEXT NOT NULL,
    value          REAL NOT NULL,
    currency       TEXT NOT NULL,
    PRIMARY KEY (ticker, registry_date, value)
);

CREATE TABLE moex_prices (
    ticker  TEXT NOT NULL,
    date    TEXT NOT NULL,
    close   REAL NOT NULL,
    PRIMARY KEY (ticker, date)
);

CREATE TABLE moex_fx (
    pair  TEXT NOT NULL,
    date  TEXT NOT NULL,
    rate  REAL NOT NULL,
    PRIMARY KEY (pair, date)
);

CREATE TABLE fetch_state (
    ticker                  TEXT PRIMARY KEY,
    asset_class             TEXT NOT NULL,
    last_price_date         TEXT,
    last_dividend_check_at  TEXT
);

CREATE TABLE schema_migrations (
    version  INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);
