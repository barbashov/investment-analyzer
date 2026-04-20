package smartlab

// tickersPrefs lists Russian preferred-share tickers that suffix the common
// symbol with "P". smart-lab.ru hosts preferred-share dividend tables under the
// common-stock URL (e.g. SBERP data lives on /q/SBER/dividend/), so we fold
// the trailing P when building the fetch URL. Mirrors
// ../smartlab-dividend-fetcher/internal/domain/tickers/normalizer.go so we
// can update the two lists independently if needed.
var tickersPrefs = map[string]struct{}{
	"BANEP": {}, "BISVP": {}, "BSPBP": {}, "CNTLP": {}, "DZRDP": {}, "GAZAP": {}, "HIMCP": {},
	"IGSTP": {}, "JNOSP": {}, "KAZTP": {}, "KCHEP": {}, "KGKCP": {}, "KRKNP": {}, "KRKOP": {},
	"KROTP": {}, "KRSBP": {}, "KTSBP": {}, "KZOSP": {}, "LNZLP": {}, "LSNGP": {}, "MAGEP": {},
	"MFGSP": {}, "MGTSP": {}, "MISBP": {}, "MTLRP": {}, "NKNCP": {}, "NNSBP": {}, "OMZZP": {},
	"PMSBP": {}, "RTKMP": {}, "RTSBP": {}, "SAGOP": {}, "SAREP": {}, "SBERP": {}, "SNGSP": {},
	"STSBP": {}, "TASBP": {}, "TATNP": {}, "TGKBP": {}, "TORSP": {}, "TRNFP": {}, "VGSBP": {},
	"VJGZP": {}, "VRSBP": {}, "VSYDP": {}, "WTCMP": {}, "YKENP": {}, "YRSBP": {},
}

// NormalizeTicker returns the URL-ticker for smart-lab. Preferred tickers fold
// to their common-share parent (SBERP → SBER); others pass through unchanged.
func NormalizeTicker(ticker string) string {
	if _, ok := tickersPrefs[ticker]; ok {
		return ticker[:4]
	}
	return ticker
}
