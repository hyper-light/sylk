package cmd

import (
	"fmt"

	"github.com/adalundhe/sylk/prompts"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "sylk",
	Short:        "Launch the interactive terminal UI",
	Long:         `Launch Sylk's terminal UI with multi-agent chat, session management, and code viewing.`,
	Args:         cobra.NoArgs,
	RunE:         runTUI,
	SilenceUsage: true,
}

func Execute() error {
	if err := prompts.Validate(); err != nil {
		return fmt.Errorf("embedded prompt initialization failed: %w", err)
	}
	return rootCmd.Execute()
}
