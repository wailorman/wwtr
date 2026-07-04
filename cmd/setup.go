package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addSetupCmd wires `wwtr setup` — run pre_setup/post_setup hooks.
func addSetupCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "run setup hooks (deps install, db migrate)",
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			return app.RunSetup(c.Context(), rc)
		},
	})
}
