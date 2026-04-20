package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/csvimport"
	"investment-analyzer/internal/store"
)

func newImportCmd(ac *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "import <csv...>",
		Short: "Import Finam-format CSV files into the local DB",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(ac.opts.DBPath)
			if err != nil {
				return apperr.Wrap("store_open", "open database", 2, err)
			}
			defer func() { _ = st.Close() }()

			var totalAdded, totalDup, totalErr int
			for _, path := range args {
				res, err := csvimport.ParseFile(path)
				if err != nil {
					_, _ = fmt.Fprintf(ac.err, "%s: %v\n", path, err)
					totalErr++
					continue
				}
				added, dup := 0, 0
				for _, tx := range res.Transactions {
					r, err := st.InsertTransaction(tx)
					if err != nil {
						_, _ = fmt.Fprintf(ac.err, "%s: insert %s: %v\n", path, tx.SourceRef, err)
						totalErr++
						continue
					}
					switch r {
					case store.InsertedNew:
						added++
					case store.InsertedDuplicate:
						dup++
					}
				}
				for _, perr := range res.Errors {
					_, _ = fmt.Fprintf(ac.err, "%s: %v\n", path, perr)
				}
				_, _ = fmt.Fprintf(ac.out, "%s: imported %d, skipped (duplicate) %d, parse errors %d\n",
					path, added, dup, len(res.Errors))
				totalAdded += added
				totalDup += dup
				totalErr += len(res.Errors)
			}
			_, _ = fmt.Fprintf(ac.out, "total: imported %d, skipped %d, errors %d\n", totalAdded, totalDup, totalErr)
			if totalErr > 0 {
				return apperr.New("import_partial", fmt.Sprintf("%d error(s) during import", totalErr), 3)
			}
			return nil
		},
	}
}
