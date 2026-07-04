package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addStartCmd wires `wwtr start` — run pre_start/post_start hooks.
func addStartCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "run start hooks (docker compose up, mocks)",
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			return app.RunStart(c.Context(), rc)
		},
	})
}
