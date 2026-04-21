package cmd

import (
	"strings"
	"testing"
)

func TestRootCommandIsTheTUIEntryPoint(t *testing.T) {
	if rootCmd.Use != "sylk" {
		t.Fatalf("root use = %q, want sylk", rootCmd.Use)
	}
	if rootCmd.RunE == nil {
		t.Fatal("expected root command to execute the TUI")
	}
	// Subcommands beyond the default TUI entry point are allowed; we only
	// require that each registered command carries a non-empty Use so it
	// surfaces in `sylk --help`.
	for _, sub := range rootCmd.Commands() {
		if strings.TrimSpace(sub.Use) == "" {
			t.Fatalf("subcommand missing Use: %#v", sub)
		}
	}
	if flag := rootCmd.Flags().Lookup("theme"); flag == nil {
		t.Fatal("expected root theme flag to be registered")
	}
	if flag := rootCmd.Flags().Lookup("mock"); flag == nil {
		t.Fatal("expected root mock flag to be registered")
	}
}
