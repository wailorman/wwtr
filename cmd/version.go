package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wailorman/wwtr/internal/version"
)

// addVersionCmd wires a standalone `wwtr version` subcommand. The cobra-builtin
// `--version` flag is also available and prints the same output.
//
// We provide both because `--version` is the conventional "is this thing
// installed?" probe, while `version` reads better in CI scripts and pairs with
// future `version upgrade`/`version check` subcommands.
func addVersionCmd(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print wwtr version",
		Long:  "Print the version, commit and build date.",
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprintln(c.OutOrStdout(), version.Long())
			return nil
		},
	})
}
