package commandapproval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Evaluator struct {
	store *RuleStore
}

func NewEvaluator(store *RuleStore) *Evaluator {
	return &Evaluator{store: store}
}

func (e *Evaluator) Evaluate(req Request) (Evaluation, error) {
	analysis, err := Analyze(req)
	if err != nil {
		return Evaluation{}, err
	}
	if rule, ok := e.lookupStoredRule(analysis.ruleLookupKeys(), RuleActionDeny); ok {
		return Evaluation{
			Decision: DecisionDeny,
			Source:   MatchSourceStoredDeny,
			Reason:   firstNonEmpty(rule.Summary, "command denied by saved approval rule"),
			Analysis: analysis,
			Rule:     cloneRule(rule),
		}, nil
	}
	if matchesPatternSet(defaultDenyPatterns(), analysis.Normalized) {
		return Evaluation{
			Decision: DecisionDeny,
			Source:   MatchSourceBuiltinDeny,
			Reason:   "command is blocked by the default safety policy",
			Analysis: analysis,
		}, nil
	}
	if rule, ok := e.lookupStoredRule(analysis.ruleLookupKeys(), RuleActionAllow); ok {
		return Evaluation{
			Decision: DecisionAllow,
			Source:   MatchSourceStoredAllow,
			Reason:   firstNonEmpty(rule.Summary, "command allowed by saved approval rule"),
			Analysis: analysis,
			Rule:     cloneRule(rule),
		}, nil
	}
	if analysis.OutsideWorkspace {
		return Evaluation{
			Decision: DecisionPrompt,
			Source:   MatchSourceInteractive,
			Reason:   analysis.Risk,
			Analysis: analysis,
		}, nil
	}
	if matchesPatternSet(defaultAllowPatterns(), analysis.Normalized) {
		return Evaluation{
			Decision: DecisionAllow,
			Source:   MatchSourceBuiltinAllow,
			Reason:   "command matches the default pre-approved policy",
			Analysis: analysis,
		}, nil
	}
	return Evaluation{
		Decision: DecisionPrompt,
		Source:   MatchSourceInteractive,
		Reason:   analysis.Risk,
		Analysis: analysis,
	}, nil
}

func (e *Evaluator) lookupStoredRule(matchKeys []string, action RuleAction) (Rule, bool) {
	if e == nil || e.store == nil {
		return Rule{}, false
	}
	for _, matchKey := range matchKeys {
		rule, ok := e.store.Lookup(matchKey)
		if !ok || rule.Action != action {
			continue
		}
		return rule, true
	}
	return Rule{}, false
}

func Analyze(req Request) (Analysis, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return Analysis{}, fmt.Errorf("command is required")
	}
	tokens, err := splitCommand(command)
	if err != nil {
		return Analysis{}, err
	}
	if len(tokens) == 0 {
		return Analysis{}, fmt.Errorf("command is required")
	}
	program := tokens[0]
	verb, argStart := deriveVerb(tokens)
	flags, argClasses, paths := analyzeArguments(tokens, argStart, program, verb, req.WorkspaceRoot)
	workingDirScope := classifyWorkingDirScope(req.WorkingDir, req.WorkspaceRoot)
	workingDirZone := pathZone(req.WorkingDir, req.WorkspaceRoot)
	outside := workingDirScope == PathScopeOutsideWorkspace
	for _, pathArg := range paths {
		if pathArg.Scope == PathScopeOutsideWorkspace {
			outside = true
			break
		}
	}
	templateParts := []string{
		"program=" + program,
		"cwd=" + string(workingDirScope),
	}
	if verb != "" {
		templateParts = append(templateParts, "verb="+verb)
	}
	if len(flags) > 0 {
		templateParts = append(templateParts, "flags="+strings.Join(flags, ","))
	}
	if len(argClasses) > 0 {
		templateParts = append(templateParts, "args="+strings.Join(argClasses, ","))
	}
	templateKey := strings.Join(templateParts, "|")
	exactKey := "exact:" + hashCommand(strings.Join([]string{strings.Join(tokens, " "), req.WorkingDir}, "\n"))
	persistKey, persistLabel := derivePersistRule(program, verb, templateKey, exactKey, workingDirScope, workingDirZone, paths)
	return Analysis{
		RawCommand:       command,
		Normalized:       strings.Join(tokens, " "),
		Tokens:           append([]string(nil), tokens...),
		Program:          program,
		Verb:             verb,
		WorkingDir:       strings.TrimSpace(req.WorkingDir),
		WorkingDirScope:  workingDirScope,
		WorkingDirZone:   workingDirZone,
		PathArgs:         paths,
		Flags:            flags,
		TemplateKey:      templateKey,
		ExactKey:         exactKey,
		PersistKey:       persistKey,
		PersistLabel:     persistLabel,
		RuleLabel:        buildRuleLabel(program, verb, outside),
		Summary:          buildSummary(program, verb, outside),
		Risk:             buildRisk(program, verb, outside),
		OutsideWorkspace: outside,
		Mutating:         isMutating(program, verb),
	}, nil
}

func splitCommand(command string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
		escape  bool
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, r := range command {
		switch {
		case escape:
			current.WriteRune(r)
			escape = false
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
		case quote != 0 && r == quote:
			quote = 0
		case r == '\\' && quote != '\'':
			escape = true
		case quote == 0 && (r == ' ' || r == '\t'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escape {
		return nil, fmt.Errorf("command ends with an unfinished escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("command has an unterminated quoted string")
	}
	flush()
	return tokens, nil
}

func deriveVerb(tokens []string) (string, int) {
	if len(tokens) < 2 {
		return "", 1
	}
	program := tokens[0]
	switch program {
	case "python", "python3":
		if len(tokens) >= 3 && tokens[1] == "-m" {
			return "-m:" + tokens[2], 3
		}
		if scriptKind := scriptVerb(tokens[1]); scriptKind != "" {
			return scriptKind, 2
		}
	case "node", "bash", "sh":
		if scriptKind := scriptVerb(tokens[1]); scriptKind != "" {
			return scriptKind, 2
		}
	case "go", "git", "cargo", "npm", "pnpm", "yarn", "make":
		for idx := 1; idx < len(tokens); idx++ {
			if strings.HasPrefix(tokens[idx], "-") {
				continue
			}
			return tokens[idx], idx + 1
		}
	}
	return "", 1
}

func analyzeArguments(tokens []string, argStart int, program, verb, workspaceRoot string) ([]string, []string, []PathArg) {
	flagSet := make(map[string]struct{})
	argClasses := make([]string, 0, max(1, len(tokens)-argStart))
	paths := make([]PathArg, 0)
	expectPathValue := false
	for idx := 1; idx < len(tokens); idx++ {
		token := tokens[idx]
		if idx < argStart && token == verbToken(verb) {
			continue
		}
		if expectPathValue {
			scope := classifyPathScope(token, workspaceRoot)
			argClasses = appendArgClass(argClasses, "path:"+string(scope))
			paths = append(paths, PathArg{Value: token, Scope: scope, Zone: pathZone(token, workspaceRoot)})
			expectPathValue = false
			continue
		}
		if isFlagToken(token) {
			flagSet[token] = struct{}{}
			expectPathValue = pathValueFlag(token)
			continue
		}
		if looksLikePath(program, verb, token) {
			scope := classifyPathScope(token, workspaceRoot)
			argClasses = appendArgClass(argClasses, "path:"+string(scope))
			paths = append(paths, PathArg{Value: token, Scope: scope, Zone: pathZone(token, workspaceRoot)})
			continue
		}
		argClasses = appendArgClass(argClasses, "literal:"+literalClass(token))
	}
	flags := make([]string, 0, len(flagSet))
	for flag := range flagSet {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return flags, argClasses, paths
}

func appendArgClass(classes []string, value string) []string {
	if len(classes) >= 6 {
		return classes
	}
	return append(classes, value)
}

func looksLikePath(program, verb, token string) bool {
	if token == "" {
		return false
	}
	switch program {
	case "mkdir", "cat", "head", "tail", "ls", "wc", "touch", "rm", "cp", "mv", "ln", "chmod", "chown":
		return true
	case "python", "python3", "node", "bash", "sh":
		return true
	case "go":
		switch verb {
		case "test", "run", "build", "vet", "fmt", "generate":
			return true
		}
	case "git":
		switch verb {
		case "add", "checkout", "restore", "show":
			return true
		}
	}
	return token == "." ||
		token == ".." ||
		filepath.IsAbs(token) ||
		strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") ||
		strings.Contains(token, "/") ||
		strings.Contains(token, `\`) ||
		strings.Contains(token, "...") ||
		fileExtPattern.MatchString(token)
}

var fileExtPattern = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)

func classifyWorkingDirScope(workingDir, workspaceRoot string) PathScope {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		if strings.TrimSpace(workspaceRoot) == "" {
			return PathScopeNone
		}
		return PathScopeWorkspaceRoot
	}
	return classifyPathScope(workingDir, workspaceRoot)
}

func classifyPathScope(pathValue, workspaceRoot string) PathScope {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return PathScopeNone
	}
	if looksLikeHomePath(pathValue) || looksLikeEnvPath(pathValue) {
		return PathScopeOutsideWorkspace
	}
	if filepath.IsAbs(pathValue) {
		if withinWorkspace(pathValue, workspaceRoot) {
			return PathScopeWorkspaceAbsolute
		}
		return PathScopeOutsideWorkspace
	}
	clean := filepath.Clean(pathValue)
	if clean == "." {
		return PathScopeWorkspaceRelative
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return PathScopeOutsideWorkspace
	}
	return PathScopeWorkspaceRelative
}

func withinWorkspace(target, workspaceRoot string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return false
	}
	target = filepath.Clean(target)
	workspaceRoot = filepath.Clean(workspaceRoot)
	if target == workspaceRoot {
		return true
	}
	return strings.HasPrefix(target, workspaceRoot+string(filepath.Separator))
}

func pathZone(pathValue, workspaceRoot string) string {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return ""
	}
	if looksLikeHomePath(pathValue) {
		return "home"
	}
	if looksLikeEnvPath(pathValue) {
		return "env"
	}
	scope := classifyPathScope(pathValue, workspaceRoot)
	switch scope {
	case PathScopeWorkspaceRoot, PathScopeWorkspaceRelative, PathScopeWorkspaceAbsolute:
		return "workspace"
	case PathScopeNone:
		return ""
	}
	clean := filepath.Clean(pathValue)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "workspace_parent"
	}
	if filepath.IsAbs(clean) {
		return absoluteOutsideZone(clean)
	}
	return "outside"
}

func absoluteOutsideZone(pathValue string) string {
	clean := filepath.Clean(pathValue)
	volume := filepath.VolumeName(clean)
	trimmed := strings.TrimPrefix(clean, volume)
	trimmed = strings.TrimPrefix(trimmed, string(filepath.Separator))
	if trimmed == "" {
		if volume != "" {
			return "root:" + strings.ToLower(strings.TrimSuffix(volume, ":"))
		}
		return "root"
	}
	parts := strings.Split(trimmed, string(filepath.Separator))
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "root"
	}
	first := strings.ToLower(strings.TrimSpace(parts[0]))
	switch first {
	case "tmp", "var", "etc", "opt", "usr", "private":
		return "root:" + first
	case "users", "home":
		return "user_home"
	default:
		return "root:" + first
	}
}

func looksLikeHomePath(pathValue string) bool {
	pathValue = strings.TrimSpace(pathValue)
	return strings.HasPrefix(pathValue, "~")
}

func looksLikeEnvPath(pathValue string) bool {
	pathValue = strings.TrimSpace(pathValue)
	return strings.HasPrefix(pathValue, "$")
}

func scriptVerb(token string) string {
	if strings.HasSuffix(token, ".py") {
		return "script:.py"
	}
	if strings.HasSuffix(token, ".js") || strings.HasSuffix(token, ".cjs") || strings.HasSuffix(token, ".mjs") {
		return "script:.js"
	}
	if strings.HasSuffix(token, ".sh") {
		return "script:.sh"
	}
	return ""
}

func literalClass(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "empty"
	}
	if len(trimmed) > 24 {
		return "value"
	}
	return trimmed
}

func verbToken(verb string) string {
	switch {
	case strings.HasPrefix(verb, "-m:"):
		return strings.TrimPrefix(verb, "-m:")
	default:
		return verb
	}
}

func pathValueFlag(token string) bool {
	switch token {
	case "-C", "--cwd", "--directory", "--prefix":
		return true
	default:
		return false
	}
}

func isFlagToken(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-" && token != "--"
}

func buildRuleLabel(program, verb string, outside bool) string {
	if outside {
		return firstNonEmpty(compactCommandName(program, verb), program) + " outside workspace"
	}
	return firstNonEmpty(compactCommandName(program, verb), program) + " inside workspace"
}

func buildSummary(program, verb string, outside bool) string {
	name := firstNonEmpty(compactCommandName(program, verb), program)
	if outside {
		return name + " requires approval because it runs outside the project workspace"
	}
	return name + " requires approval because it is not pre-approved"
}

func buildRisk(program, verb string, outside bool) string {
	if outside {
		return "This command runs in or targets a location outside the project workspace."
	}
	if isMutating(program, verb) {
		return "This command mutates the workspace and is not in the pre-approved command set."
	}
	return "This command is not in the pre-approved command set."
}

func compactCommandName(program, verb string) string {
	if verb == "" {
		return program
	}
	if strings.HasPrefix(verb, "script:") {
		return program + " " + strings.TrimPrefix(verb, "script:")
	}
	return program + " " + verb
}

func derivePersistRule(
	program, verb, templateKey, exactKey string,
	workingDirScope PathScope,
	workingDirZone string,
	paths []PathArg,
) (string, string) {
	name := firstNonEmpty(compactCommandName(program, verb), program)
	if canGeneralizeWorkspaceRule(program, verb, workingDirScope, paths) {
		return templateKey, name + " inside workspace"
	}
	if zone, ok := generalizedOutsideZone(program, verb, workingDirScope, workingDirZone, paths); ok {
		return templateKey + "|outside_zone=" + zone, name + " in " + humanizeZone(zone)
	}
	return exactKey, "this exact command"
}

func canGeneralizeWorkspaceRule(program, verb string, workingDirScope PathScope, paths []PathArg) bool {
	if !generalizableWorkspaceProgram(program) {
		return false
	}
	if workingDirScope == PathScopeOutsideWorkspace {
		return false
	}
	if len(paths) == 0 {
		return workingDirScope == PathScopeWorkspaceRoot || workingDirScope == PathScopeWorkspaceRelative || workingDirScope == PathScopeWorkspaceAbsolute
	}
	for _, pathArg := range paths {
		switch pathArg.Scope {
		case PathScopeWorkspaceRoot, PathScopeWorkspaceRelative, PathScopeWorkspaceAbsolute:
		default:
			return false
		}
	}
	return true
}

func generalizedOutsideZone(program, verb string, workingDirScope PathScope, workingDirZone string, paths []PathArg) (string, bool) {
	if !generalizableWorkspaceProgram(program) {
		return "", false
	}
	zones := make([]string, 0, len(paths)+1)
	if workingDirScope == PathScopeOutsideWorkspace && workingDirZone != "" {
		zones = append(zones, workingDirZone)
	}
	for _, pathArg := range paths {
		if pathArg.Scope == PathScopeOutsideWorkspace && pathArg.Zone != "" {
			zones = append(zones, pathArg.Zone)
		}
	}
	if len(zones) == 0 {
		return "", false
	}
	first := zones[0]
	for _, zone := range zones[1:] {
		if zone != first {
			return "", false
		}
	}
	return first, true
}

func generalizableWorkspaceProgram(program string) bool {
	switch program {
	case "mkdir", "touch", "cp", "mv", "ln":
		return true
	default:
		return false
	}
}

func humanizeZone(zone string) string {
	switch zone {
	case "workspace_parent":
		return "the workspace parent"
	case "home":
		return "the home directory"
	case "env":
		return "an environment-derived path"
	case "user_home":
		return "user home"
	case "outside", "root":
		return "an outside-workspace location"
	default:
		if strings.HasPrefix(zone, "root:") {
			return "/" + strings.TrimPrefix(zone, "root:")
		}
		return zone
	}
}

func (a Analysis) ruleLookupKeys() []string {
	keys := make([]string, 0, 3)
	appendKey := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range keys {
			if existing == value {
				return
			}
		}
		keys = append(keys, value)
	}
	appendKey(a.ExactKey)
	appendKey(a.PersistKey)
	appendKey(a.TemplateKey)
	return keys
}

func isMutating(program, verb string) bool {
	switch program {
	case "mkdir", "touch", "cp", "mv", "rm", "chmod", "chown":
		return true
	case "git":
		switch verb {
		case "add", "checkout", "commit", "stash", "restore":
			return true
		}
	case "npm", "pnpm", "yarn":
		switch verb {
		case "install":
			return true
		}
	case "go":
		switch verb {
		case "mod", "generate":
			return true
		}
	}
	return false
}

func hashCommand(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

func cloneRule(rule Rule) *Rule {
	dup := rule
	return &dup
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultAllowPatterns() []*regexp.Regexp {
	return compiledPatterns(defaultAllowPatternStrings)
}

func defaultDenyPatterns() []*regexp.Regexp {
	return compiledPatterns(defaultDenyPatternStrings)
}

func matchesPatternSet(patterns []*regexp.Regexp, command string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func compiledPatterns(patterns []string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled = append(compiled, regexp.MustCompile(pattern))
	}
	return compiled
}

var defaultAllowPatternStrings = []string{
	`^go\s+(build|test|run|fmt|vet|mod|generate)(?:\s+.*)?$`,
	`^git\s+(status|diff|log|show|branch|checkout|add|commit|stash|restore)(?:\s+.*)?$`,
	`^make(?:\s+.*)?$`,
	`^npm\s+(install|run|test|build)(?:\s+.*)?$`,
	`^pnpm\s+(install|run|test|build|exec|dlx)(?:\s+.*)?$`,
	`^yarn\s+(install|run|test|build)(?:\s+.*)?$`,
	`^cargo\s+(build|test|run|check|fmt|clippy)(?:\s+.*)?$`,
	`^python(?:3)?\s+-m\s+(pytest|unittest|pip)(?:\s+.*)?$`,
	`^python(?:3)?\s+[A-Za-z0-9_./-]+\.py(?:\s+.*)?$`,
	`^pytest(?:\s+.*)?$`,
	`^node\s+[A-Za-z0-9_./-]+\.(js|cjs|mjs)(?:\s+.*)?$`,
	`^bash\s+[A-Za-z0-9_./-]+\.sh(?:\s+.*)?$`,
	`^sh\s+[A-Za-z0-9_./-]+\.sh(?:\s+.*)?$`,
	`^gopls(?:\s+.*)?$`,
	`^(ls|cat|head|tail|grep|find|wc)(?:\s+.*)?$`,
}

var defaultDenyPatternStrings = []string{
	`rm\s+-rf\s+/`,
	`rm\s+-rf\s+\*`,
	`>\s*/dev/sd`,
	`mkfs`,
	`dd\s+if=`,
	`:\(\)\{`,
	`chmod\s+-R\s+777`,
	`curl.*\|\s*(ba)?sh`,
	`wget.*\|\s*(ba)?sh`,
}
