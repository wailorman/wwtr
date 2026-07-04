package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addCleanCmd wires `wwtr clean` — pre_clean hooks, remove generated files,
// post_clean hooks, delete state.yaml.
func addCleanCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "clean",
		Short: "run clean hooks, remove generated files, delete state",
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			return app.RunClean(c.Context(), rc)
		},
	})
}
