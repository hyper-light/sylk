package shared

import "context"

func runLinterAnalysis(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonLinter(ctx, runner, paths)
	case "go", "":
		return RunGolangCILint(ctx, runner, paths)
	default:
		return unsupportedAnalysisLanguageIssues("run_linter", language)
	}
}

func runTypeCheckerAnalysis(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonTypeChecker(ctx, runner, paths)
	case "go", "":
		return DeduplicateIssues(append(RunGoVet(ctx, runner, paths), RunStaticcheck(ctx, runner, paths)...))
	default:
		return unsupportedAnalysisLanguageIssues("run_type_checker", language)
	}
}

func runFormatterAnalysis(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonFormatterCheck(ctx, runner, paths)
	case "go", "":
		return RunGoFmt(ctx, runner, paths)
	default:
		return unsupportedAnalysisLanguageIssues("run_formatter_check", language)
	}
}

func runSecurityAnalysis(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonSecurityScan(ctx, runner, paths)
	case "go", "":
		return RunGoSec(ctx, runner, paths)
	default:
		return unsupportedAnalysisLanguageIssues("run_security_scan", language)
	}
}

func runCoverageAnalysis(ctx context.Context, runner *ToolRunner, paths []string, threshold float64) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonCoverage(ctx, runner, paths, threshold)
	case "go", "":
		return RunCoverage(ctx, runner, paths, threshold)
	default:
		return unsupportedAnalysisLanguageIssues("check_coverage", language)
	}
}

func runComplexityAnalysis(
	ctx context.Context,
	runner *ToolRunner,
	paths []string,
	maxCyclomatic int,
	maxCognitive int,
) []ValidationIssue {
	language := detectAnalysisLanguage(ctx, runner, paths)
	switch language {
	case "python":
		return RunPythonComplexity(ctx, runner, paths, maxCyclomatic, maxCognitive)
	case "go", "":
		return RunComplexity(ctx, runner, paths, maxCyclomatic, maxCognitive)
	default:
		return unsupportedAnalysisLanguageIssues("analyze_complexity", language)
	}
}

func unsupportedAnalysisLanguageIssues(toolName, language string) []ValidationIssue {
	if language == "" {
		return informationalToolIssues(toolName, toolName+" could not determine a supported language for the requested paths")
	}
	return informationalToolIssues(toolName, toolName+" does not have a configured analyzer for "+language+" targets")
}
