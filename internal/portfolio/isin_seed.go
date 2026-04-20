package portfolio

// DefaultISINTickerMap is a bootstrap ISIN→ticker map for common MOEX dividend payers.
// At runtime, MOEX integration augments this by fetching ISINs from
// /iss/securities/{secid}.json and merging the result into the DB.
//
// The goal is to make `invest dividends` work end-to-end before any MOEX call has happened.
// Add entries here only for tickers that need to resolve offline.
var DefaultISINTickerMap = map[string]string{
	"RU000A0DKVS5": "NVTK",
	"RU0009029557": "SBERP",
	"RU0009024277": "LKOH",
	"RU0009092134": "LSNGP",
	"RU0009062467": "SIBN",
	"RU000A0J2Q06": "ROSN",
	"RU000A0JKQU8": "MGNT",
	"RU000A0JRKT8": "PHOR",
	"RU000A0HL5M1": "BELU",
	"RU000A1095U8": "BELU",
	"RU000A108KL3": "MDMG",
	"RU000A1054W1": "PLZL",
}
