package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

func versionValue() string {
	if v := strings.TrimSpace(buildVersion); v != "" {
		return v
	}
	return "dev"
}

func newVersionCmd(ac *appContext) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show CLI version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(ac.out, "invest %s (commit %s, built %s)\n",
				versionValue(),
				strings.TrimSpace(buildCommit),
				strings.TrimSpace(buildDate),
			)
			return err
		},
	}
}
