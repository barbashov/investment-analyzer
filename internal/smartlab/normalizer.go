package smartlab

// tickersPrefs lists Russian preferred-share tickers that suffix the common
// symbol with "P". smart-lab.ru hosts preferred-share dividend tables under the
// common-stock URL (e.g. SBERP data lives on /q/SBER/dividend/), so we fold
// the trailing P when building the fetch URL. Mirrors
// ../smartlab-dividend-fetcher/internal/domain/tickers/normalizer.go
// (https://github.com/barbashov/smartlab-dividend-fetcher) so we can update
// the two lists independently if needed.
var tickersPrefs = map[string]struct{}{
	"BANEP": {}, "BISVP": {}, "BSPBP": {}, "CNTLP": {}, "DZRDP": {}, "GAZAP": {}, "HIMCP": {},
	"IGSTP": {}, "JNOSP": {}, "KAZTP": {}, "KCHEP": {}, "KGKCP": {}, "KRKNP": {}, "KRKOP": {},
	"KROTP": {}, "KRSBP": {}, "KTSBP": {}, "KZOSP": {}, "LNZLP": {}, "LSNGP": {}, "MAGEP": {},
	"MFGSP": {}, "MGTSP": {}, "MISBP": {}, "MTLRP": {}, "NKNCP": {}, "NNSBP": {}, "OMZZP": {},
	"PMSBP": {}, "RTKMP": {}, "RTSBP": {}, "SAGOP": {}, "SAREP": {}, "SBERP": {}, "SNGSP": {},
	"STSBP": {}, "TASBP": {}, "TATNP": {}, "TGKBP": {}, "TORSP": {}, "TRNFP": {}, "VGSBP": {},
	"VJGZP": {}, "VRSBP": {}, "VSYDP": {}, "WTCMP": {}, "YKENP": {}, "YRSBP": {},
}

// tickerAliases maps a MOEX ticker to the slug smart-lab uses in its dividend
// URL when the two differ for reasons other than the SBERP→SBER preferred-share
// fold. The typical driver is a corporate redomiciliation that renamed the
// security on MOEX while smart-lab kept (or migrated to) the new symbol.
//
// Example: Rus Agro (Русагро) redomiciled; MOEX still lists GDR-era positions
// under AGRO but smart-lab publishes dividends at /q/RAGR/dividend/.
var tickerAliases = map[string]string{
	"AGRO": "RAGR",
}

// NormalizeTicker returns the URL-ticker for smart-lab. Preferred tickers fold
// to their common-share parent (SBERP → SBER); redomiciled tickers are
// rewritten via tickerAliases; others pass through unchanged.
func NormalizeTicker(ticker string) string {
	if alias, ok := tickerAliases[ticker]; ok {
		return alias
	}
	if _, ok := tickersPrefs[ticker]; ok {
		return ticker[:4]
	}
	return ticker
}

// tableTickersFor returns every ticker symbol that might appear inside
// `<td>…</td>` cells on the smart-lab dividend page for `ticker`. It always
// includes the original symbol plus any alias target (e.g. RAGR for AGRO) so
// historical rows tagged with the old ticker and future rows tagged with the
// new one are both parsed.
func tableTickersFor(ticker string) []string {
	out := []string{ticker}
	if alias, ok := tickerAliases[ticker]; ok && alias != ticker {
		out = append(out, alias)
	}
	return out
}
