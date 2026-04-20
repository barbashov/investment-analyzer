CREATE TABLE smartlab_dividends (
    ticker          TEXT NOT NULL,
    period          TEXT NOT NULL,
    t2_date         TEXT NOT NULL,
    ex_date         TEXT NOT NULL,
    value_per_share REAL NOT NULL,
    fetched_at      TEXT NOT NULL,
    PRIMARY KEY (ticker, period)
);

CREATE INDEX idx_smartlab_ex_date ON smartlab_dividends(ex_date);

ALTER TABLE fetch_state ADD COLUMN last_smartlab_check_at TEXT;
