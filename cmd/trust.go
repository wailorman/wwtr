package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addTrustCmd wires `wwtr trust [<path>]` — explicitly approve a config.
// path defaults to the discovered .wwtr.yml when omitted.
func addTrustCmd(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "trust [path]",
		Short: "explicitly approve a config (CI/scripts)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			return app.RunTrust(c.Context(), rc, path)
		},
	}
	root.AddCommand(cmd)
}
