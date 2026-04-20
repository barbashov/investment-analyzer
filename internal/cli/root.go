package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const defaultDBPath = "./data/investment.db"

// Execute is the package entry point called by cmd/invest/main.go.
func Execute(stdin io.Reader, stdout, stderr io.Writer) error {
	root := newRootCmd(stdin, stdout, stderr)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return err
	}
	return nil
}

func newRootCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	ac := &appContext{
		stdin: stdin,
		out:   stdout,
		err:   stderr,
		opts:  globalOpts{DBPath: defaultDBPath},
	}

	cmd := &cobra.Command{
		Use:           "invest",
		Short:         "Personal investment analyzer (Finam CSV + MOEX ISS)",
		Version:       versionValue(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	f := cmd.PersistentFlags()
	f.StringVar(&ac.opts.DBPath, "db", ac.opts.DBPath, "Path to the SQLite database")
	f.StringVar(&ac.opts.From, "from", "", "Start date (YYYY-MM-DD); default: all-time")
	f.StringVar(&ac.opts.To, "to", "", "End date (YYYY-MM-DD); default: today")

	cmd.AddCommand(
		newVersionCmd(ac),
		newImportCmd(ac),
		newTxCmd(ac),
		newDividendsCmd(ac),
		newFetchCmd(ac),
		newUpdateCmd(ac),
		newPositionsCmd(ac),
		newPricesCmd(ac),
		newFXCmd(ac),
		newCalendarCmd(ac),
		newROICmd(ac),
	)

	return cmd
}
