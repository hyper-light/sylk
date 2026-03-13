package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type globalTestWriteParams struct {
	TestCase   testershared.PlannedTestCase   `json:"test_case"`
	TargetFile string                         `json:"target_file"`
	OutputFile string                         `json:"output_file"`
	Content    string                         `json:"content"`
	Basis      versioning.WorkspaceWriteBasis `json:"basis"`
}

type globalTestWriteSkillConfig struct {
	skillName   string
	description string
	testType    string
}

func writeTestSkill(gt *GlobalTester) *skills.Skill {
	return newGlobalTestWriteSkill(gt, globalTestWriteSkillConfig{
		skillName:   "write_test",
		description: "Write a global test file into the shared VFS. Requires a fresh or still-leased global write basis for the output_file, auto-renews on lease expiry, and returns a refreshed next_basis.",
		testType:    "test",
	})
}

func writeIntegrationTestSkill(gt *GlobalTester) *skills.Skill {
	return newGlobalTestWriteSkill(gt, globalTestWriteSkillConfig{
		skillName:   "write_integration_test",
		description: "Write a cross-component integration test into the shared VFS. Requires a fresh or still-leased global write basis for the output_file, auto-renews on lease expiry, and returns a refreshed next_basis.",
		testType:    "integration",
	})
}

func writeE2ETestSkill(gt *GlobalTester) *skills.Skill {
	return newGlobalTestWriteSkill(gt, globalTestWriteSkillConfig{
		skillName:   "write_e2e_test",
		description: "Write an end-to-end system test into the shared VFS. Requires a fresh or still-leased global write basis for the output_file, auto-renews on lease expiry, and returns a refreshed next_basis.",
		testType:    "e2e",
	})
}

func newGlobalTestWriteSkill(gt *GlobalTester, cfg globalTestWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(cfg.skillName).
		Description(cfg.description).
		Domain("testing").
		Keywords("write", cfg.testType, "test", "global", "vfs", "basis").
		Priority(88).
		ObjectParam("test_case", "Structured planned test case metadata.", globalTestCaseProperties(), true).
		StringParam("target_file", "Source file or subsystem under test.", true).
		StringParam("output_file", "Destination test file path in the global workspace.", true).
		StringParam("content", "Concrete executable test code for the output file.", true).
		ObjectParam("basis", "Global write basis returned by prepare_global_write_context for the output_file.", globalWriteBasisProperties(), true).
		Usage("Call prepare_global_write_context for the target output file first, pass the returned basis into this skill, and reuse the next_basis returned by each successful write while the lease remains active.").
		BestPractice("If the same test file needs multiple writes, keep feeding the returned next_basis back into the next call instead of repreparing immediately.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params, err := decodeGlobalTestWriteParams(input)
			if err != nil {
				return nil, err
			}
			result, err := gt.writeGlobalTestFile(ctx, params.OutputFile, params.Content, &params.Basis)
			if err != nil {
				return nil, err
			}
			result["output_file"] = params.OutputFile
			result["written"] = true
			result["type"] = cfg.testType
			if name := strings.TrimSpace(params.TestCase.Name); name != "" {
				result["test_name"] = name
			}
			return result, nil
		}).
		Build()
}

func decodeGlobalTestWriteParams(input json.RawMessage) (globalTestWriteParams, error) {
	var params globalTestWriteParams
	if err := json.Unmarshal(input, &params); err != nil {
		return globalTestWriteParams{}, fmt.Errorf("invalid parameters: %w", err)
	}
	if strings.TrimSpace(params.TargetFile) == "" {
		params.TargetFile = strings.TrimSpace(params.TestCase.TargetFile)
	}
	if strings.TrimSpace(params.TargetFile) == "" {
		return globalTestWriteParams{}, fmt.Errorf("target_file is required")
	}
	if strings.TrimSpace(params.OutputFile) == "" {
		return globalTestWriteParams{}, fmt.Errorf("output_file is required")
	}
	if strings.TrimSpace(params.Content) == "" {
		return globalTestWriteParams{}, fmt.Errorf("content is required and must contain executable test code")
	}
	return params, nil
}

func (gt *GlobalTester) writeGlobalTestFile(
	ctx context.Context,
	path, content string,
	basis *versioning.WorkspaceWriteBasis,
) (map[string]any, error) {
	if basis == nil {
		return nil, fmt.Errorf("basis is required")
	}
	result := gt.invokeGlobalWriteSkill(ctx, path, content, *basis)
	if result.Success {
		return globalWriteResultData(result)
	}
	if !errors.Is(result.Err, versioning.ErrWorkspaceWriteLeaseExpired) {
		return nil, skillInvokeError("write_global_file", result)
	}
	if err := gt.refreshGlobalWriteBasis(ctx, path, basis); err != nil {
		return nil, err
	}
	retried := gt.invokeGlobalWriteSkill(ctx, path, content, *basis)
	if !retried.Success {
		return nil, skillInvokeError("write_global_file", retried)
	}
	return globalWriteResultData(retried)
}

func (gt *GlobalTester) invokeGlobalWriteSkill(
	ctx context.Context,
	path, content string,
	basis versioning.WorkspaceWriteBasis,
) *skills.Result {
	payload, err := json.Marshal(map[string]any{
		"path":    path,
		"content": content,
		"basis":   basis,
	})
	if err != nil {
		return &skills.Result{
			SkillName: "write_global_file",
			Success:   false,
			Error:     err.Error(),
			Err:       err,
		}
	}
	return gt.skills.Invoke(ctx, "write_global_file", payload)
}

func (gt *GlobalTester) refreshGlobalWriteBasis(
	ctx context.Context,
	path string,
	basis *versioning.WorkspaceWriteBasis,
) error {
	if basis == nil {
		return nil
	}
	if gt.workspaceViews == nil {
		return fmt.Errorf("workspace views are unavailable")
	}
	refreshed, err := versioning.RefreshWorkspaceWriteBasis(
		ctx,
		gt.workspaceViews,
		versioning.WorkspaceWriteScopeGlobal,
		path,
		"",
	)
	if err != nil {
		return err
	}
	*basis = refreshed
	return nil
}

func globalWriteResultData(result *skills.Result) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("write_global_file returned no result")
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("write_global_file returned unexpected result type %T", result.Data)
	}
	return data, nil
}

func skillInvokeError(name string, result *skills.Result) error {
	if result == nil {
		return fmt.Errorf("%s returned no result", name)
	}
	if result.Err != nil {
		return fmt.Errorf("%s: %w", name, result.Err)
	}
	if strings.TrimSpace(result.Error) != "" {
		return fmt.Errorf("%s: %s", name, result.Error)
	}
	return fmt.Errorf("%s failed", name)
}

func globalTestCaseProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"name":               {Type: "string", Description: "Deterministic test name."},
		"category":           {Type: "string", Description: "Test category."},
		"failure_hypothesis": {Type: "string", Description: "What defect this test should catch."},
		"input_strategy":     {Type: "string", Description: "How the test exercises the system."},
		"expected_behavior":  {Type: "string", Description: "What should happen when the test passes."},
		"target_file":        {Type: "string", Description: "Source file or subsystem under test."},
	}
}

func globalWriteBasisProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"scope":            {Type: "string", Description: "Must be global."},
		"path":             {Type: "string", Description: "Path prepared for mutation."},
		"target_view":      {Type: "string", Description: "Must be global."},
		"prepared_at":      {Type: "string", Description: "When the write basis snapshot was prepared."},
		"lease_expires_at": {Type: "string", Description: "When the write lease expires unless renewed by the next write."},
	}
}
