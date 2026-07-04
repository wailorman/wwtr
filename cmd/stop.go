package cmd

import (
	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/app"
)

// addStopCmd wires `wwtr stop` — run pre_stop/post_stop hooks.
func addStopCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "run stop hooks (docker stop, kill processes)",
		RunE: func(c *cobra.Command, _ []string) error {
			rc, err := RunContextFromCmd(c)
			if err != nil {
				return err
			}
			return app.RunStop(c.Context(), rc)
		},
	})
}
