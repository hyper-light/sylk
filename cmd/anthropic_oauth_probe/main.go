package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/providers"
)

const defaultOAuthUserAgent = "claude-cli/2.1.2 (external, cli)"

type probeConfig struct {
	AuthMode               string
	BearerToken            string
	OAuthCode              string
	OAuthCodeVerifier      string
	OAuthCodeStateAsVerify bool
	OAuthCodeStdin         bool
	Model                  string
	BaseURL                string
	BetaQuery              bool
	AnthropicVersion       string
	AnthropicBeta          string
	UserAgent              string
	MaxTokens              int
	Stream                 bool
	ThinkingBudget         int
	AdaptiveThinking       bool
	Tools                  int
	OAuthToolPrefix        bool
	ToolChoice             string
	DisableParallelToolUse bool
	UseCache               bool
	SystemPrompt           string
	SystemBlocks           multiStringFlag
	SystemPromptFile       string
	SystemPromptSize       int
	UserPrompt             string
	DumpRequestPath        string
	DumpResponsePath       string
	Timeout                time.Duration
	Preset                 string
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "probe failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() probeConfig {
	cfg := probeConfig{}
	flag.StringVar(&cfg.Preset, "preset", "minimal", "probe preset: minimal or guardian")
	flag.StringVar(&cfg.AuthMode, "auth", "oauth", "auth mode: oauth or api_key")
	flag.StringVar(&cfg.BearerToken, "bearer-token", "", "explicit bearer token to use instead of resolving stored OAuth auth")
	flag.StringVar(&cfg.OAuthCode, "oauth-code", "", "Anthropic OAuth authorization code or callback payload")
	flag.StringVar(&cfg.OAuthCodeVerifier, "oauth-code-verifier", "", "PKCE code verifier to use when exchanging -oauth-code")
	flag.BoolVar(&cfg.OAuthCodeStateAsVerify, "oauth-code-state-as-verifier", false, "treat the parsed OAuth state fragment as the PKCE verifier for legacy buggy flows")
	flag.BoolVar(&cfg.OAuthCodeStdin, "oauth-code-stdin", false, "read the Anthropic OAuth authorization code from stdin")
	flag.StringVar(&cfg.Model, "model", "claude-sonnet-4-6", "Anthropic model ID")
	flag.StringVar(&cfg.BaseURL, "base-url", "https://api.anthropic.com/v1/messages", "Anthropic messages endpoint")
	flag.BoolVar(&cfg.BetaQuery, "beta-query", false, "append ?beta=true to the request URL")
	flag.StringVar(&cfg.AnthropicVersion, "anthropic-version", "2023-06-01", "Anthropic API version header")
	flag.StringVar(&cfg.AnthropicBeta, "anthropic-beta", "", "comma-separated anthropic-beta header value")
	flag.StringVar(&cfg.UserAgent, "user-agent", "", "optional User-Agent header")
	flag.IntVar(&cfg.MaxTokens, "max-tokens", 128, "max_tokens request field")
	flag.BoolVar(&cfg.Stream, "stream", false, "send stream=true in the request body")
	flag.IntVar(&cfg.ThinkingBudget, "thinking-budget", 0, "extended thinking budget_tokens")
	flag.BoolVar(&cfg.AdaptiveThinking, "adaptive-thinking", false, "send thinking.type=adaptive")
	flag.IntVar(&cfg.Tools, "tools", 0, "number of dummy tools to include")
	flag.BoolVar(&cfg.OAuthToolPrefix, "oauth-tool-prefix", true, "prefix tool names with mcp_ in oauth mode")
	flag.StringVar(&cfg.ToolChoice, "tool-choice", "", "tool choice: auto, any, none, or a specific tool name")
	flag.BoolVar(&cfg.DisableParallelToolUse, "disable-parallel-tool-use", false, "set disable_parallel_tool_use on tool choice")
	flag.BoolVar(&cfg.UseCache, "cache", false, "apply prompt caching markers")
	flag.StringVar(&cfg.SystemPrompt, "system-prompt", "", "inline system prompt text")
	flag.Var(&cfg.SystemBlocks, "system-block", "repeatable system prompt block text")
	flag.StringVar(&cfg.SystemPromptFile, "system-prompt-file", "", "path to a system prompt file")
	flag.IntVar(&cfg.SystemPromptSize, "system-prompt-size", 0, "generate a synthetic system prompt of roughly this many bytes")
	flag.StringVar(&cfg.UserPrompt, "prompt", "Reply with the single word OK.", "user prompt text")
	flag.StringVar(&cfg.DumpRequestPath, "dump-request", "", "optional file path for the marshaled request JSON")
	flag.StringVar(&cfg.DumpResponsePath, "dump-response", "", "optional file path for the raw response body")
	flag.DurationVar(&cfg.Timeout, "timeout", 45*time.Second, "HTTP timeout")
	flag.Parse()
	applyPreset(&cfg)
	return cfg
}

func applyPreset(cfg *probeConfig) {
	switch strings.ToLower(strings.TrimSpace(cfg.Preset)) {
	case "", "minimal":
		if cfg.AuthMode == "" {
			cfg.AuthMode = "oauth"
		}
		if !cfg.BetaQuery && strings.EqualFold(cfg.AuthMode, "oauth") {
			cfg.BetaQuery = true
		}
		if cfg.AnthropicBeta == "" && strings.EqualFold(cfg.AuthMode, "oauth") {
			cfg.AnthropicBeta = "oauth-2025-04-20,interleaved-thinking-2025-05-14"
		}
		if cfg.UserAgent == "" && strings.EqualFold(cfg.AuthMode, "oauth") {
			cfg.UserAgent = defaultOAuthUserAgent
		}
	case "guardian":
		cfg.AuthMode = "oauth"
		cfg.Model = "claude-opus-4-6"
		cfg.BetaQuery = true
		cfg.AnthropicBeta = "oauth-2025-04-20,interleaved-thinking-2025-05-14"
		cfg.UserAgent = defaultOAuthUserAgent
		cfg.MaxTokens = 8192
		cfg.Stream = true
		cfg.ThinkingBudget = 2048
		cfg.Tools = 12
		if cfg.SystemPrompt == "" && cfg.SystemPromptFile == "" && cfg.SystemPromptSize == 0 {
			cfg.SystemPromptSize = 5000
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown preset %q; using explicit flags as-is\n", cfg.Preset)
	}
}

func run(ctx context.Context, cfg probeConfig) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	token, authSummary, err := resolveCredential(ctx, cfg)
	if err != nil {
		return err
	}
	systemBlocks, err := resolveSystemBlocks(cfg)
	if err != nil {
		return err
	}
	body := buildRequestBody(cfg, systemBlocks)
	bodyJSON, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if cfg.DumpRequestPath != "" {
		if err := os.WriteFile(cfg.DumpRequestPath, bodyJSON, 0o600); err != nil {
			return fmt.Errorf("write request dump: %w", err)
		}
	}

	reqURL, err := buildURL(cfg.BaseURL, cfg.BetaQuery)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", cfg.AnthropicVersion)
	if strings.EqualFold(cfg.AuthMode, "oauth") {
		req.Header.Set("authorization", "Bearer "+token)
	} else {
		req.Header.Set("x-api-key", token)
	}
	if strings.TrimSpace(cfg.AnthropicBeta) != "" {
		req.Header.Set("anthropic-beta", strings.TrimSpace(cfg.AnthropicBeta))
	}
	if strings.TrimSpace(cfg.UserAgent) != "" {
		req.Header.Set("user-agent", strings.TrimSpace(cfg.UserAgent))
	}

	fmt.Printf("auth: %s\n", authSummary)
	fmt.Printf("url: %s\n", req.URL.String())
	fmt.Printf("model: %s\n", cfg.Model)
	fmt.Printf("max_tokens: %d\n", cfg.MaxTokens)
	fmt.Printf("stream: %t\n", cfg.Stream)
	fmt.Printf("thinking_budget: %d\n", cfg.ThinkingBudget)
	fmt.Printf("adaptive_thinking: %t\n", cfg.AdaptiveThinking)
	fmt.Printf("tools: %d\n", cfg.Tools)
	totalSystemLen := 0
	for _, block := range systemBlocks {
		totalSystemLen += len(block)
	}
	fmt.Printf("system_blocks: %d\n", len(systemBlocks))
	fmt.Printf("system_prompt_len: %d\n", totalSystemLen)
	if cfg.DumpRequestPath != "" {
		fmt.Printf("request_dump: %s\n", cfg.DumpRequestPath)
	}

	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if cfg.DumpResponsePath != "" {
		if err := os.WriteFile(cfg.DumpResponsePath, respBody, 0o600); err != nil {
			return fmt.Errorf("write response dump: %w", err)
		}
	}

	fmt.Printf("status: %s\n", resp.Status)
	if requestID := strings.TrimSpace(resp.Header.Get("request-id")); requestID != "" {
		fmt.Printf("request_id_header: %s\n", requestID)
	}
	if contentType := strings.TrimSpace(resp.Header.Get("content-type")); contentType != "" {
		fmt.Printf("content_type: %s\n", contentType)
	}
	if cfg.DumpResponsePath != "" {
		fmt.Printf("response_dump: %s\n", cfg.DumpResponsePath)
	}

	pretty := prettyJSON(respBody)
	if pretty == "" {
		pretty = string(respBody)
	}
	fmt.Printf("response_body:\n%s\n", pretty)
	return nil
}

func resolveCredential(ctx context.Context, cfg probeConfig) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.AuthMode)) {
	case "oauth":
		if token := strings.TrimSpace(os.Getenv("ANTHROPIC_OAUTH_PROBE_BEARER_TOKEN")); token != "" {
			return token, "oauth explicit_env_token", nil
		}
		if token := strings.TrimSpace(cfg.BearerToken); token != "" {
			return token, "oauth explicit_flag_token", nil
		}
		code := strings.TrimSpace(cfg.OAuthCode)
		if code == "" {
			code = strings.TrimSpace(os.Getenv("ANTHROPIC_OAUTH_PROBE_CODE"))
		}
		if code == "" && cfg.OAuthCodeStdin {
			stdinCode, err := readOAuthCodeFromStdin()
			if err != nil {
				return "", "", err
			}
			code = stdinCode
		}
		verifier := strings.TrimSpace(cfg.OAuthCodeVerifier)
		if verifier == "" {
			verifier = strings.TrimSpace(os.Getenv("ANTHROPIC_OAUTH_PROBE_CODE_VERIFIER"))
		}
		if code != "" {
			auth, err := exchangeOAuthCode(ctx, code, verifier, cfg.OAuthCodeStateAsVerify)
			if err != nil {
				return "", "", err
			}
			summary := "oauth exchanged_code"
			if strings.TrimSpace(auth.Scope) != "" {
				summary += " scope=" + strings.TrimSpace(auth.Scope)
			}
			if !auth.AccessTokenExpiry.IsZero() {
				summary += " expires=" + auth.AccessTokenExpiry.UTC().Format(time.RFC3339)
			}
			return strings.TrimSpace(auth.AccessToken), summary, nil
		}
		auth, err := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}).Resolve(ctx)
		if err != nil {
			return "", "", fmt.Errorf("resolve anthropic oauth auth: %w", err)
		}
		if auth == nil || strings.TrimSpace(auth.AccessToken) == "" {
			return "", "", fmt.Errorf("anthropic oauth access token is empty")
		}
		summary := "oauth"
		if strings.TrimSpace(auth.Scope) != "" {
			summary += " scope=" + strings.TrimSpace(auth.Scope)
		}
		if !auth.AccessTokenExpiry.IsZero() {
			summary += " expires=" + auth.AccessTokenExpiry.UTC().Format(time.RFC3339)
		}
		return strings.TrimSpace(auth.AccessToken), summary, nil
	case "api_key":
		key := strings.TrimSpace(providers.ResolveAnthropicAPIKey(""))
		if key == "" {
			return "", "", fmt.Errorf("anthropic api key is empty")
		}
		return key, "api_key", nil
	default:
		return "", "", fmt.Errorf("unsupported auth mode %q", cfg.AuthMode)
	}
}

func readOAuthCodeFromStdin() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read oauth code from stdin: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func exchangeOAuthCode(ctx context.Context, rawCode, explicitVerifier string, stateAsVerifier bool) (*oauth.AnthropicOAuthAuth, error) {
	code, state := parseProbeOAuthCode(rawCode)
	if code == "" {
		return nil, fmt.Errorf("oauth code is empty")
	}
	verifier := strings.TrimSpace(explicitVerifier)
	if verifier == "" && stateAsVerifier {
		verifier = state
	}
	if verifier == "" {
		return nil, fmt.Errorf("oauth code verifier is required; pass -oauth-code-verifier or -oauth-code-state-as-verifier")
	}
	challenge := &oauth.AnthropicOAuthChallenge{
		AuthURL:      "https://claude.ai/oauth/authorize",
		RedirectURL:  "https://console.anthropic.com/oauth/code/callback",
		State:        state,
		CodeVerifier: verifier,
	}
	auth, err := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}).CompleteAuthCode(ctx, challenge, rawCode)
	if err != nil {
		return nil, fmt.Errorf("exchange oauth code: %w", err)
	}
	return auth, nil
}

func parseProbeOAuthCode(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if parsed, err := url.Parse(trimmed); err == nil {
			if code := strings.TrimSpace(parsed.Query().Get("code")); code != "" {
				state := strings.TrimSpace(parsed.Query().Get("state"))
				if state == "" && parsed.Fragment != "" {
					_, state = parseProbeOAuthCode(parsed.Fragment)
				}
				return code, state
			}
		}
	}
	if before, after, ok := strings.Cut(trimmed, "#"); ok {
		return strings.TrimSpace(before), strings.TrimSpace(strings.TrimPrefix(after, "state="))
	}
	if strings.Contains(trimmed, "state=") {
		values, err := url.ParseQuery(strings.TrimPrefix(trimmed, "?"))
		if err == nil {
			return strings.TrimSpace(values.Get("code")), strings.TrimSpace(values.Get("state"))
		}
	}
	return trimmed, ""
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func resolveSystemBlocks(cfg probeConfig) ([]string, error) {
	if len(cfg.SystemBlocks) > 0 {
		return append([]string(nil), cfg.SystemBlocks...), nil
	}
	switch {
	case strings.TrimSpace(cfg.SystemPromptFile) != "":
		data, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return nil, fmt.Errorf("read system prompt file: %w", err)
		}
		return []string{string(data)}, nil
	case strings.TrimSpace(cfg.SystemPrompt) != "":
		return []string{cfg.SystemPrompt}, nil
	case cfg.SystemPromptSize > 0:
		return []string{syntheticSystemPrompt(cfg.SystemPromptSize)}, nil
	default:
		return []string{"You are a diagnostic probe. Reply briefly and literally."}, nil
	}
}

func syntheticSystemPrompt(targetBytes int) string {
	base := "You are a diagnostic probe for Anthropic OAuth runtime validation. Follow instructions exactly, do not use markdown unless asked, and reply concisely.\n"
	if targetBytes <= len(base) {
		return base
	}
	var b strings.Builder
	b.Grow(targetBytes)
	for b.Len() < targetBytes {
		b.WriteString(base)
	}
	out := b.String()
	if len(out) > targetBytes {
		return out[:targetBytes]
	}
	return out
}

func buildURL(base string, betaQuery bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	if betaQuery {
		q := parsed.Query()
		q.Set("beta", "true")
		parsed.RawQuery = q.Encode()
	}
	return parsed.String(), nil
}

func buildRequestBody(cfg probeConfig, systemBlocks []string) map[string]any {
	system := make([]map[string]any, 0, len(systemBlocks))
	for _, block := range systemBlocks {
		system = append(system, textBlock(block, cfg.UseCache))
	}
	request := map[string]any{
		"model":      cfg.Model,
		"max_tokens": cfg.MaxTokens,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					textBlock(cfg.UserPrompt, cfg.UseCache),
				},
			},
		},
		"system": system,
	}
	if cfg.Stream {
		request["stream"] = true
	}
	if cfg.AdaptiveThinking {
		request["thinking"] = map[string]any{"type": "adaptive"}
	} else if cfg.ThinkingBudget > 0 {
		request["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": cfg.ThinkingBudget,
		}
	}
	if cfg.UseCache {
		request["cache_control"] = map[string]any{
			"type": "ephemeral",
			"ttl":  "5m",
		}
	}
	if cfg.Tools > 0 {
		request["tools"] = buildTools(cfg)
	}
	if choice := buildToolChoice(cfg); choice != nil {
		request["tool_choice"] = choice
	}
	return request
}

func textBlock(text string, useCache bool) map[string]any {
	block := map[string]any{
		"type": "text",
		"text": text,
	}
	if useCache {
		block["cache_control"] = map[string]any{
			"type": "ephemeral",
			"ttl":  "5m",
		}
	}
	return block
}

func buildTools(cfg probeConfig) []map[string]any {
	tools := make([]map[string]any, 0, cfg.Tools)
	for i := 1; i <= cfg.Tools; i++ {
		name := fmt.Sprintf("tool_%02d", i)
		if strings.EqualFold(cfg.AuthMode, "oauth") && cfg.OAuthToolPrefix {
			name = "mcp_" + name
		}
		tool := map[string]any{
			"name":        name,
			"description": fmt.Sprintf("Diagnostic tool %d", i),
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{
						"type": "string",
					},
				},
				"required": []string{"input"},
			},
		}
		if cfg.UseCache && i == cfg.Tools {
			tool["cache_control"] = map[string]any{
				"type": "ephemeral",
				"ttl":  "5m",
			}
		}
		tools = append(tools, tool)
	}
	return tools
}

func buildToolChoice(cfg probeConfig) map[string]any {
	choice := strings.TrimSpace(strings.ToLower(cfg.ToolChoice))
	if choice == "" || cfg.Tools == 0 {
		return nil
	}
	switch choice {
	case "auto", "any", "none":
		result := map[string]any{"type": choice}
		if cfg.DisableParallelToolUse && choice != "none" {
			result["disable_parallel_tool_use"] = true
		}
		return result
	default:
		name := choice
		if strings.EqualFold(cfg.AuthMode, "oauth") && cfg.OAuthToolPrefix && !strings.HasPrefix(name, "mcp_") {
			name = "mcp_" + name
		}
		result := map[string]any{
			"type": "tool",
			"name": name,
		}
		if cfg.DisableParallelToolUse {
			result["disable_parallel_tool_use"] = true
		}
		return result
	}
}

func prettyJSON(raw []byte) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var dst bytes.Buffer
	if err := json.Indent(&dst, raw, "", "  "); err != nil {
		return ""
	}
	return dst.String()
}
