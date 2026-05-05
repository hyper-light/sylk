package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/mcp/catalog"
	"github.com/adalundhe/sylk/core/mcp/client"
	"github.com/adalundhe/sylk/core/mcp/transport"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Inspect and exercise the MCP capability plane",
	Long: `Subcommands:
  list      Print the effective MCP server catalog (global + project layers).
  probe     Connect to a configured server and print its capability inventory.
  servers   Render an example servers.yaml.
  trace     Dump the durable operation journal for a session.
`,
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print the effective MCP server catalog",
	RunE:  runMCPList,
}

var mcpProbeCmd = &cobra.Command{
	Use:   "probe <server-id>",
	Short: "Connect to a configured MCP server and list its capabilities",
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPProbe,
}

var mcpServersCmd = &cobra.Command{
	Use:   "servers",
	Short: "Print an example servers.yaml configuration",
	RunE:  runMCPServers,
}

var mcpTraceCmd = &cobra.Command{
	Use:   "trace <session-dir>",
	Short: "Dump the durable MCP operation journal for a session",
	Args:  cobra.ExactArgs(1),
	RunE:  runMCPTrace,
}

func init() {
	mcpCmd.AddCommand(mcpListCmd)
	mcpCmd.AddCommand(mcpProbeCmd)
	mcpCmd.AddCommand(mcpServersCmd)
	mcpCmd.AddCommand(mcpTraceCmd)
	rootCmd.AddCommand(mcpCmd)
}

func runMCPList(cmd *cobra.Command, _ []string) error {
	mgr, err := loadCatalog()
	if err != nil {
		return err
	}
	cat := mgr.Effective()
	if len(cat.Servers) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no MCP servers configured (run 'sylk mcp servers' for an example)")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTRANSPORT\tENDPOINT\tDISABLED\tDOMAINS")
	ids := sortedIDs(cat.Servers)
	for _, id := range ids {
		spec := cat.Servers[id]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n",
			id,
			spec.Transport,
			endpointFromSpec(spec),
			spec.Disabled,
			strings.Join(spec.Domains, ","),
		)
	}
	return tw.Flush()
}

func runMCPProbe(cmd *cobra.Command, args []string) error {
	mgr, err := loadCatalog()
	if err != nil {
		return err
	}
	id := args[0]
	cat := mgr.Effective()
	spec, ok := cat.Servers[id]
	if !ok {
		return fmt.Errorf("server %q not in catalog", id)
	}
	if spec.Disabled {
		return fmt.Errorf("server %q is disabled", id)
	}
	scope := concurrency.NewGoroutineScope(context.Background(), "sylk.cmd.mcp.probe", nil)
	defer scope.Shutdown(time.Second, 2*time.Second)
	tx, err := buildProbeTransport(spec, scope)
	if err != nil {
		return err
	}
	defer tx.Close()
	session, err := client.NewSession(client.SessionConfig{
		ServerID:           id,
		Transport:          tx,
		Scope:              scope,
		ClientInfo:         mcp.Implementation{Name: "sylk-mcp-probe", Version: "0.1"},
		ClientCapabilities: mcp.ClientCapabilities{Roots: &mcp.RootsCapability{ListChanged: true}},
	})
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err := session.Start(probeCtx); err != nil {
		return fmt.Errorf("probe %s: handshake: %w", id, err)
	}
	defer session.Close()
	snapshot, err := session.SyncAll(probeCtx)
	if err != nil {
		return fmt.Errorf("probe %s: capability sync: %w", id, err)
	}
	return printSnapshot(cmd, snapshot, session)
}

func runMCPServers(cmd *cobra.Command, _ []string) error {
	example := exampleServersYAML()
	_, err := fmt.Fprint(cmd.OutOrStdout(), example)
	return err
}

func runMCPTrace(cmd *cobra.Command, args []string) error {
	walPath := filepath.Join(args[0], "mcp", "events.wal")
	file, err := os.Open(walPath)
	if err != nil {
		return fmt.Errorf("open wal: %w", err)
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTIMESTAMP\tKIND\tOP\tSERVER\tNAME\tSTATUS")
	for dec.More() {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		fmt.Fprintf(tw, "%v\t%v\t%v\t%v\t%v\t%v\t%v\n",
			raw["sequence"],
			raw["timestamp"],
			raw["kind"],
			raw["operation_id"],
			raw["server_id"],
			raw["local_name"],
			raw["error_message"],
		)
	}
	return tw.Flush()
}

func loadCatalog() (*catalog.Manager, error) {
	mgr := catalog.NewManager()
	home, err := os.UserHomeDir()
	if err == nil {
		if err := mgr.LoadGlobal(catalog.DefaultGlobalPath(home)); err != nil {
			return nil, fmt.Errorf("load global catalog: %w", err)
		}
	}
	cwd, err := os.Getwd()
	if err == nil {
		if err := mgr.LoadProject(catalog.DefaultProjectPath(cwd)); err != nil {
			return nil, fmt.Errorf("load project catalog: %w", err)
		}
	}
	return mgr, nil
}

func endpointFromSpec(spec catalog.ServerSpec) string {
	switch spec.Transport {
	case catalog.TransportStdio:
		return spec.Command
	case catalog.TransportHTTP, catalog.TransportSSE:
		return spec.Endpoint
	}
	return ""
}

func sortedIDs(servers map[string]catalog.ServerSpec) []string {
	ids := make([]string, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func buildProbeTransport(spec catalog.ServerSpec, scope *concurrency.GoroutineScope) (transport.Transport, error) {
	switch spec.Transport {
	case catalog.TransportStdio:
		env := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, k+"="+v)
		}
		return transport.NewStdio(transport.StdioConfig{
			Command: spec.Command,
			Args:    spec.Args,
			Env:     env,
			WorkDir: spec.WorkDir,
			Scope:   scope,
		})
	case catalog.TransportHTTP, catalog.TransportSSE:
		return transport.NewHTTP(transport.HTTPConfig{
			Endpoint: spec.Endpoint,
			Scope:    scope,
		})
	}
	return nil, fmt.Errorf("transport %q not supported by probe", spec.Transport)
}

func printSnapshot(cmd *cobra.Command, snapshot *client.CapabilitySnapshot, session *client.Session) error {
	out := cmd.OutOrStdout()
	info := session.Capabilities()
	fmt.Fprintf(out, "server: %s\n", session.ServerID())
	fmt.Fprintf(out, "implementation: %s/%s\n", session.ServerInfo().Name, session.ServerInfo().Version)
	fmt.Fprintf(out, "fingerprint: %s\n", snapshot.Fingerprint)
	fmt.Fprintf(out, "tools: %d  resources: %d  prompts: %d\n",
		len(snapshot.Tools), len(snapshot.Resources), len(snapshot.Prompts))
	if instructions := strings.TrimSpace(session.Instructions()); instructions != "" {
		fmt.Fprintf(out, "instructions:\n  %s\n", strings.ReplaceAll(instructions, "\n", "\n  "))
	}
	printToolsBlock(out, snapshot)
	printResourcesBlock(out, snapshot)
	printPromptsBlock(out, snapshot)
	_ = info
	return nil
}

func printToolsBlock(out io.Writer, snapshot *client.CapabilitySnapshot) {
	if len(snapshot.Tools) == 0 {
		return
	}
	fmt.Fprintln(out, "tools:")
	for _, tool := range snapshot.Tools {
		fmt.Fprintf(out, "  - %s — %s\n", tool.Name, oneLine(tool.Description))
	}
}

func printResourcesBlock(out io.Writer, snapshot *client.CapabilitySnapshot) {
	if len(snapshot.Resources) == 0 {
		return
	}
	fmt.Fprintln(out, "resources:")
	for _, r := range snapshot.Resources {
		fmt.Fprintf(out, "  - %s [%s]\n", r.URI, r.MIMEType)
	}
}

func printPromptsBlock(out io.Writer, snapshot *client.CapabilitySnapshot) {
	if len(snapshot.Prompts) == 0 {
		return
	}
	fmt.Fprintln(out, "prompts:")
	for _, p := range snapshot.Prompts {
		fmt.Fprintf(out, "  - %s — %s\n", p.Name, oneLine(p.Description))
	}
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

func exampleServersYAML() string {
	return `# ~/.sylk/mcp/servers.yaml — example MCP server catalog.
servers:
  - id: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${env:GITHUB_TOKEN}"
    domains: [git, research]
    trusted_read_only: false   # set true if all tools from this server are read-only

  - id: docs
    transport: http
    endpoint: https://docs.example.com/mcp
    headers:
      Authorization: "Bearer ${cred:docs.token}"
    domains: [research]
    trusted_read_only: true
    server_instructions: |
      Use this server for documentation lookups when the local index doesn't
      cover the question. Prefer narrow queries.

  - id: deploy
    transport: stdio
    command: /usr/local/bin/deploy-mcp
    args: ["--profile", "prod"]
    domains: [system]
    allow_tools: ["status", "rollback"]   # explicit allowlist; deny everything else
`
}

// implementation note: this var ensures the import of `skills` is used so the
// command package compiles even before any CLI uses skills directly. Used by
// future subcommands that surface compiled MCP skills.
var _ = skills.Skill{}
