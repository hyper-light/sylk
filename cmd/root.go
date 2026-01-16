package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "sylk",
	Short: "Sylk - An AI agent framework",
	Long:  `Sylk is a powerful AI agent framework for building intelligent assistants.`,
}

func Execute() error {
	return rootCmd.Execute()
}
