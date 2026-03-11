package cmd

import "testing"

func TestRootCommandIsTheTUIEntryPoint(t *testing.T) {
	if rootCmd.Use != "sylk" {
		t.Fatalf("root use = %q, want sylk", rootCmd.Use)
	}
	if rootCmd.RunE == nil {
		t.Fatal("expected root command to execute the TUI")
	}
	if len(rootCmd.Commands()) != 0 {
		t.Fatalf("expected no subcommands, got %d", len(rootCmd.Commands()))
	}
	if flag := rootCmd.Flags().Lookup("theme"); flag == nil {
		t.Fatal("expected root theme flag to be registered")
	}
	if flag := rootCmd.Flags().Lookup("mock"); flag == nil {
		t.Fatal("expected root mock flag to be registered")
	}
}
