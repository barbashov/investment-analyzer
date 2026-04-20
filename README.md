# invest

A small CLI for making sense of a Russian-broker (Finam) investment history. Built around tracking dividends from a buy-and-hold portfolio, with position and ROI views along for the ride.

It reads the semicolon-delimited CSV that Finam exports, stores everything in a local SQLite file, and pulls prices and announced dividends straight from MOEX ISS — no scraping, no login, no account-linking.

## Why it exists

The Finam web UI shows individual trades. It doesn't answer the questions that tend to actually matter:

- How much was paid out in dividends last year, by ticker?
- What's the real cost basis on a long-held position after many small buys?
- What's the next ex-dividend date for anything in the portfolio?
- Is the portfolio up or down, total, since the very first deposit?

So this tool answers them.

## Install

```sh
make build
# binary lands in ./bin/invest
```

Pure Go, no CGO — builds with `modernc.org/sqlite`. Go 1.26+.

## Typical flow

```sh
# 1. Drop your Finam CSV exports into ./data/ and import them.
invest import data/*.csv

# 2. Pull fresh prices and dividend calendars from MOEX for everything you hold.
invest update

# 3. Look around.
invest dividends --by year
invest dividends payouts         # interactive browser
invest positions                 # FIFO cost basis vs. market
invest calendar --days 90        # what's coming up
invest roi                       # total return since first deposit
```

The CSV import is idempotent — same file twice is a no-op. Manual corrections can go in via `invest tx add` and live alongside imported rows.

## Commands

| Command | What it does |
| --- | --- |
| `import FILE...` | Ingest Finam CSV exports. Idempotent. |
| `tx add` / `tx list` | Manually add a transaction, or browse/filter/delete manual ones. |
| `dividends [--by ticker\|year\|month]` | Net (or `--gross`) dividend totals. |
| `dividends payouts` | Interactive per-payment browser with MOEX cross-reference. |
| `positions` | FIFO cost basis with mark-to-market from MOEX. |
| `prices [--watch]` | Current quotes for what you hold. |
| `fx` | CNY and gold exposure expressed in RUB. |
| `calendar [--days N]` | Upcoming ex-dates from MOEX. |
| `roi` | Total return since the first deposit, with yearly breakdown. |
| `update` | Refresh the MOEX cache for everything you currently hold. |
| `fetch --ticker X` | Low-level escape hatch: fetch data for a single ticker. |

Global flags worth knowing: `--db PATH` (defaults to `./data/investment.db`), `--from YYYY-MM-DD`, `--to YYYY-MM-DD`.

## A few things that are less obvious

- **Dividends are matched by ISIN**, because Finam leaves the ticker column blank on dividend rows and puts the ISIN in the comment. If a new payer isn't resolving, it needs one line added to `internal/portfolio/isin_seed.go`.
- **Gold (`GLDRUB_TOM`) is a currency on MOEX**, not a stock. The classifier knows about the `_TOM` / `_TMS` / `_TOD` / `_LTV` / `_SPT` suffixes.
- **History is cached permanently** per ticker; only new dates get fetched. Dividend announcement lists are re-polled every 7 days since new announcements appear over time.
- **The `data/` directory is gitignored** — it's where your personal CSVs and SQLite database live.
- **MOEX is rate-limited to 5 req/s** from the client side. Be nice to them.

## Keys in the Bubbletea browsers (`tx list`, `dividends payouts`)

```
j / k       move
/           filter  (e.g. "ticker:SBER from:2024-01-01")
f           cycle sort
c           clear filter
d           delete  (manual rows only, in tx list — press twice to confirm)
q           quit
```

## Tests

```sh
make test
```

MOEX parser tests use saved JSON fixtures, so the suite doesn't hit the network.

## Scope

This is a single-user tool, not a product. It assumes a single Finam account, Russian-market instruments, and a strategy that's mostly "buy things, hold them, collect dividends." If you want to adapt it to something else, the paragraph-long architecture note at the top of `CLAUDE.md` is probably the fastest way in.
