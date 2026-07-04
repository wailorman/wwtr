package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addInfoCmd wires `wwtr info` — print resolved vars and builtins. The local
// --json and --env flags select the output format and are merged into the
// RunContext.Flags after the persistent PreRun builds it.
func addInfoCmd(root *cobra.Command) {
	var jsonOut, envOut bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "print resolved vars and builtins",
		Long: `Print resolved user vars and builtin variables.

Output formats (mutually exclusive in practice; --json wins if both set):
  default   human-readable
  --env     "export KEY=value" lines, suitable for eval "$(wwtr info --env)"
  --json    machine-readable JSON`,
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			rc.Flags.JSON = jsonOut
			rc.Flags.Env = envOut
			return app.RunInfo(c.Context(), rc)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable JSON output")
	cmd.Flags().BoolVar(&envOut, "env", false, "export KEY=value output for eval")
	root.AddCommand(cmd)
}
