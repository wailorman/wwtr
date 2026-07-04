package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addUntrustCmd wires `wwtr untrust [<path>]` — revoke a config approval.
func addUntrustCmd(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "untrust [path]",
		Short: "revoke a config approval",
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
			return app.RunUntrust(c.Context(), rc, path)
		},
	}
	root.AddCommand(cmd)
}
