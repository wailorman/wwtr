package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addInitCmd wires `wwtr init` — render templates, copy files, symlink, run
// init hooks (PLAN §9 step-by-step flow lives in app.RunInit).
func addInitCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "render templates, copy files, symlink, run init hooks",
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			return app.RunInit(c.Context(), rc)
		},
	})
}
