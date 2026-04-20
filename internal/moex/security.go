package moex

import (
	"context"
	"strings"
)

// SecurityInfo carries the bits of /iss/securities/{secid}.json we actually use.
type SecurityInfo struct {
	Ticker  string
	ISIN    string
	Type    string // SECTYPE — e.g. "common_share", "preferred_share", "ofz_bond", "currency"
	Group   string // SECGROUP — coarser grouping
	Boards  []string
	Markets []string
}

// FetchSecurity returns the metadata block for a security.
// `description.data` rows look like ["NAME", "Title", "value"]; the names we care about are
// "ISIN", "TYPE", "SECTYPE", "SECGROUP", etc.
func (c *Client) FetchSecurity(ctx context.Context, secid string) (*SecurityInfo, error) {
	var resp struct {
		Description Block `json:"description"`
		Boards      Block `json:"boards"`
	}
	if err := c.get(ctx, "/securities/"+strings.ToUpper(secid)+".json", nil, &resp); err != nil {
		return nil, err
	}
	info := &SecurityInfo{Ticker: strings.ToUpper(secid)}

	iName := resp.Description.columnIndex("name")
	iValue := resp.Description.columnIndex("value")
	for _, row := range resp.Description.Data {
		name := strings.ToUpper(stringAt(row, iName))
		value := stringAt(row, iValue)
		switch name {
		case "ISIN":
			info.ISIN = value
		case "TYPE", "SECTYPE":
			if info.Type == "" {
				info.Type = value
			}
		case "GROUP", "SECGROUP":
			if info.Group == "" {
				info.Group = value
			}
		}
	}

	iBoard := resp.Boards.columnIndex("boardid")
	iMarket := resp.Boards.columnIndex("market")
	seenBoard := map[string]bool{}
	seenMarket := map[string]bool{}
	for _, row := range resp.Boards.Data {
		if b := stringAt(row, iBoard); b != "" && !seenBoard[b] {
			info.Boards = append(info.Boards, b)
			seenBoard[b] = true
		}
		if m := stringAt(row, iMarket); m != "" && !seenMarket[m] {
			info.Markets = append(info.Markets, m)
			seenMarket[m] = true
		}
	}
	return info, nil
}
