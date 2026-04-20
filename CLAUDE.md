# Investment Analyzer — Conventions for Future Sessions

A personal CLI for analyzing Russian-broker (Finam) investment history. Built around dividend tracking, with mark-to-market position views as a secondary feature. Buy-and-hold dividend strategy.

## What it does

- **Imports** Finam's semicolon-delimited CSV (`Объем транзакции`, etc.) into a local SQLite DB.
- **Resolves** dividend payouts to tickers via ISIN (Finam's dividend rows have an empty ticker column — the ISIN lives in the comment).
- **Fetches** prices and announced dividends from MOEX ISS (`iss.moex.com`). One source covers stocks, bonds, ETFs, and currencies (incl. gold).
- **Reports** dividends (gross/tax/net by ticker/year/month), positions (FIFO cost basis vs MOEX close), FX exposure, and an upcoming-dividends calendar.

## Architecture in one paragraph

CSV → `internal/csvimport` (semicolon, BOM, decimal comma; classifies RU op labels). Stored idempotently in SQLite via `internal/store` keyed by a content hash (`store.ComputeHash`). `internal/portfolio` is pure logic — FIFO positions, dividend grouping, ISIN-based ticker resolution. `internal/moex` is a polite HTTP client (5 req/s rate limit) with engine-aware routing for prices: stocks/ETFs → `engines/stock/markets/shares`, bonds → `markets/bonds`, currency (incl. gold!) → `engines/currency/markets/selt`. `internal/assets` decides which routing applies. Reports use `internal/ui` (lipgloss tables, synocli-style palette).

## Key conventions

- **Build**: `make build` (CGO_ENABLED=0; pure-Go SQLite via `modernc.org/sqlite`). Output → `bin/invest`.
- **Tests**: `make test`. Table-driven where it pays off. MOEX parser tests use saved JSON fixtures so they don't hit the network.
- **Default DB**: `./data/investment.db` (the `data/` dir is gitignored — it holds personal CSV exports).
- **Amounts are stored as absolute values** (≥ 0). Cash-flow direction is derived from `op_type` at report time. Mirrors Finam's `Объем транзакции` column.
- **Trade hash** (`store.ComputeHash`) is source-agnostic — same trade entered via CSV import or `invest tx add` collides. Recipe: sha256 of `date|time|op_type|ticker|account|fmt(amount)|currency|fmt(qty)|fmt(price)|comment`.
- **Op types**: BUY, SELL, DIVIDEND, DEPOSIT, WITHDRAWAL, TRANSFER, COMMISSION, FX_BUY, FX_SELL, INCOME, TAX. RU labels live in `internal/csvimport/classify.go`.
- **Dividend ticker resolution**: Finam's dividend rows have empty ticker. The comment contains an ISIN; we extract it (`csvimport.ParseDividendISIN`) and look up via `portfolio.MapTickerResolver`. The bootstrap map lives in `portfolio/isin_seed.go` — extend it when new tickers join your portfolio.
- **Gold (GLDRUB_TOM) is a currency on MOEX**, not a stock. The `assets.Classify` heuristic catches the `_TOM`/`_TMS`/`_TOD`/`_LTV`/`_SPT` suffixes.
- **MOEX history is immutable** — no TTL. We track `last_price_date` per ticker in `fetch_state` and ask MOEX for `[last+1, today]` only. Dividend lists are re-polled every 7 days because new announcements appear over time (existing entries never change).
- **`invest update` vs `invest fetch`**: `update` is the everyday command — refreshes everything you currently hold. `fetch --ticker X` is the low-level escape hatch for a single ticker (e.g., one you're researching).
- **Auto-fetch on demand**: `positions`, `prices`, `calendar` will fetch missing MOEX data inline. You don't have to call `update` first; it's just useful for pre-warming.

## Commands at a glance

```
invest import data/*.csv          # idempotent CSV ingest
invest tx add --op buy --date ... --ticker SBER --quantity 10 --price 285.50
invest tx add --op buy --date ... --ticker SBER --quantity 10 --amount 2855  # alt form
invest tx list                    # Bubbletea browser (filter / sort / delete manual rows)
invest dividends [--by ticker|year|month] [--gross]
invest dividends payouts          # Bubbletea per-payment browser w/ MOEX cross-reference
invest positions                  # FIFO cost basis + MOEX market value
invest prices [--watch --interval 30s]
invest fx                         # CNY / gold exposure in RUB
invest calendar [--days 90]       # upcoming ex-dates from MOEX
invest update                     # refresh MOEX cache for all current holdings
invest fetch --ticker SBER [--refresh]
```

Global flags: `--db PATH` (default `./data/investment.db`), `--from YYYY-MM-DD`, `--to YYYY-MM-DD`.

## Bubbletea browser framework

`internal/tui/browser` is a small list-with-detail framework. Consumers in `internal/tui/trades`
and `internal/tui/payouts` implement `browser.Row` (Cells/Detail/Match) and pass sort modes.
Keys: `j/k` move, `/` filter (`ticker:SBER from:2024-01-01`), `f` cycle sort, `c` clear filter,
`d` delete (custom hook in trades, manual rows only — two presses to confirm), `q` quit.

## Files to modify when extending

- New op type → `store/transactions.go` (constant), `csvimport/classify.go` (RU label mapping), `cli/tx.go` (`normalizeOp`), maybe `portfolio/positions.go` if it affects holdings.
- New asset class → `assets/classify.go` + `MOEXEngine`/`MOEXMarket` switches, `moex/updater.go` (dividend gating).
- New dividend payer in your portfolio whose ISIN isn't resolved → add to `portfolio/isin_seed.go`. Long-term, prefer `assets.ClassifyViaMOEX` to learn ISIN → ticker dynamically.

## Reference projects

- `../synocli` — visual + architectural template (Cobra + Lipgloss, `internal/cli` + `internal/cmdutil` split). Don't copy its session/auth machinery — investment-analyzer is stateless against MOEX (no login).
- `../smartlab-dividend-fetcher` — earlier dividend-fetching idea using HTML scraping. Not used here; MOEX ISS replaces it (no scraping fragility, no preferred-share normalization needed since SBER and SBERP are distinct MOEX secids).
