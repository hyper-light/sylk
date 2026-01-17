# Sylk Implementation TODO

## Executive Summary

Build a highly concurrent multi-agent system capable of:
- **Dozens of concurrent sessions** with isolated workflows
- **Hundreds of sub-agents** per session executing DAG-based workflows
- **Maximized concurrency** through worker pools, sharded data structures, and parallel DAG execution
- **Zero direct agent-to-agent communication** - all messages route through Guide
- **Session isolation** with shared historical knowledge access
- **Progressive skill disclosure** - load capabilities only when needed

## Current State

| Component | Status | Location |
|-----------|--------|----------|
| Guide (Universal Router) | Needs Enhancement | `agents/guide/` |
| Archivalist (Historical RAG) | Needs Enhancement | `agents/archivalist/` |
| Core Messaging | Complete | `core/messaging/` |
| Core Skills | Complete | `core/skills/` |
| Tree-Sitter (AST Parsing) | Complete | `core/treesitter/` |
| Session Manager | Not Started | - |
| DAG Engine | Not Started | - |
| Academic | Not Started | - |
| Architect | Not Started | - |
| Orchestrator | Not Started | - |
| Engineer | Not Started | - |
| Librarian | Not Started | - |
| Inspector | Not Started | - |
| Tester | Not Started | - |

---

## Notes

- All agents must implement bus integration (Start/Stop/IsRunning)
- All agents must provide `AgentRoutingInfo` for Guide registration
- All agents must define core skills (always loaded) and extended skills (on demand)
- All state operations must be session-aware
- Engineers are invisible to users - all clarifications route through Architect
- Guide is the only message router - no direct agent communication
- Archivalist stores all workflow history for future reference
- Cross-session queries are read-only
- Use existing patterns from Guide and Archivalist as templates

---

## Discipline Protocols (Sisyphus-Inspired)

**Goal**: Implement distributed execution discipline across all agents to ensure thorough, robust, and mature behavior. These protocols exceed single-agent approaches by leveraging specialized agent expertise.

**Reference**: See ARCHITECTURE.md for detailed protocol specifications and system prompts.

### Communication Style (All Agents)

**Files to modify:**
- `core/agent/base_prompt.go` (create)
- Each agent's `system_prompt.go`

**Acceptance Criteria:**
- [ ] `AgentCommunicationStylePrompt` constant defined in base
- [ ] All agent system prompts include communication style fragment
- [ ] No flattery, hedging, status acknowledgments, or filler phrases in agent output
- [ ] Evidence-based responses with file paths and line numbers

### Librarian: Codebase Health Assessment

**Files to modify/create:**
- `agents/librarian/health_assessment.go`
- `agents/librarian/maturity.go`

**Acceptance Criteria:**
- [ ] `CodebaseMaturity` enum: `DISCIPLINED`, `TRANSITIONAL`, `LEGACY`, `GREENFIELD`
- [ ] `assess_codebase_health` skill implementation
- [ ] Pattern consistency scoring (0.0-1.0)
- [ ] Test coverage detection
- [ ] Technical debt marker counting (TODO/FIXME/HACK)
- [ ] Health assessment included in implementation context responses
- [ ] `LibrarianHealthAssessmentPrompt` added to system prompt

### Librarian: Context Quality Feedback

**Files to modify/create:**
- `agents/librarian/context_feedback.go`
- `agents/librarian/quality_metrics.go`

**Acceptance Criteria:**
- [ ] `ContextFeedback` struct with query_id, context_provided, failure_type, actual_outcome
- [ ] `receive_context_feedback` skill implementation
- [ ] `query_context_feedback` skill implementation
- [ ] `get_context_quality_metrics` skill implementation
- [ ] Feedback types: ENGINEER_FAILURE, PATTERN_MISMATCH, OUTDATED_CONTEXT, MISSING_CONTEXT, WRONG_RECOMMENDATION
- [ ] Feedback stored in Archivalist category "librarian_context_feedback"
- [ ] Cache invalidation on feedback receipt
- [ ] Query past feedback during context generation
- [ ] Confidence warning for query types with high failure history
- [ ] `LibrarianContextQualityPrompt` added to system prompt

### Archivalist: Failure Pattern Memory

**Files to modify/create:**
- `agents/archivalist/failure_memory.go`
- `agents/archivalist/failure_types.go`

**Acceptance Criteria:**
- [ ] `FailureEntry` struct with approach_signature, error_pattern, resolution, recurrence_count
- [ ] `query_failure_patterns` skill implementation
- [ ] `record_failure` skill implementation
- [ ] `get_approach_statistics` skill implementation
- [ ] `mark_decision_outcome` skill implementation
- [ ] Similar failure detection with 0.7 similarity threshold
- [ ] "SIMILAR FAILURE DETECTED" warning format
- [ ] Cross-session failure learning (failure patterns always readable)
- [ ] `ArchivalistFailureMemoryPrompt` added to system prompt

### Archivalist: Retrieval Accuracy Tracking

**Files to modify/create:**
- `agents/archivalist/retrieval_accuracy.go`
- `agents/archivalist/self_healing.go`

**Acceptance Criteria:**
- [ ] `RetrievalAccuracy` struct with query_id, entries_returned, accuracy_issue, correction
- [ ] `report_retrieval_issue` skill implementation
- [ ] `mark_entry_stale` skill implementation
- [ ] `verify_storage` skill implementation
- [ ] `get_retrieval_accuracy_metrics` skill implementation
- [ ] Issue types: STALE_RETRIEVAL, IRRELEVANT_RETRIEVAL, INCOMPLETE_RETRIEVAL, WRONG_RESOLUTION, CONFLICTING_ENTRIES
- [ ] Self-healing actions per issue type
- [ ] Read-after-write storage verification
- [ ] Proactive staleness detection (entry age vs file modification)
- [ ] `ArchivalistRetrievalAccuracyPrompt` added to system prompt

### Academic: Research Discipline Protocol

**Files to modify/create:**
- `agents/academic/research_discipline.go`
- `agents/academic/validation.go`

**Acceptance Criteria:**
- [ ] `validate_against_codebase` skill implementation
- [ ] `assess_applicability` skill implementation
- [ ] `identify_adaptation_needs` skill implementation
- [ ] Applicability classification: `DIRECT`, `ADAPTABLE`, `INCOMPATIBLE`
- [ ] Confidence scoring: `HIGH`, `MEDIUM`, `LOW`
- [ ] Theory-reality gap detection
- [ ] Mandatory Librarian consultation before finalizing recommendations
- [ ] Maturity-aware recommendations
- [ ] `AcademicResearchDisciplinePrompt` added to system prompt

### Academic: Recommendation Outcome Tracking

**Files to modify/create:**
- `agents/academic/outcome_tracking.go`
- `agents/academic/success_metrics.go`

**Acceptance Criteria:**
- [ ] `RecommendationOutcome` struct with recommendation_id, query, outcome, outcome_details, lessons_learned
- [ ] `record_recommendation_outcome` skill implementation
- [ ] `query_recommendation_outcomes` skill implementation
- [ ] `get_recommendation_success_rates` skill implementation
- [ ] Outcome types: IMPLEMENTATION_SUCCESS, IMPLEMENTATION_FAILURE, ADAPTATION_REQUIRED, BETTER_ALTERNATIVE_FOUND, PARTIAL_SUCCESS
- [ ] Outcomes stored in Archivalist category "academic_recommendation_outcome"
- [ ] Query past outcomes during research for similar topics
- [ ] Surface "PAST FAILURE" warnings when similar recommendations failed
- [ ] Surface "VALIDATED" notes when similar recommendations succeeded
- [ ] Adjust confidence based on historical success rates (>80% = HIGH, 50-80% = MEDIUM, <50% = LOW)
- [ ] `AcademicRecommendationOutcomePrompt` added to system prompt

### Guide: Intent Gate Classification (Phase 0)

**Files to modify/create:**
- `agents/guide/intent_gate.go`
- `agents/guide/validation.go`

**Acceptance Criteria:**
- [ ] `RequestType` enum: `TRIVIAL`, `EXPLICIT`, `EXPLORATORY`, `OPEN_ENDED`, `GITHUB_WORK`, `AMBIGUOUS`
- [ ] Intent classification before routing
- [ ] Fast-path for TRIVIAL requests
- [ ] Validation checks (assumptions explicit, scope bounded)
- [ ] Pre-routing consultation triggers (Librarian, Archivalist)
- [ ] Confidence threshold (0.7) for routing
- [ ] `GuideIntentGatePrompt` added to system prompt

### Guide: Routing Failure Tracking

**Files to modify/create:**
- `agents/guide/routing_failure.go`
- `agents/guide/routing_metrics.go`

**Acceptance Criteria:**
- [ ] `RoutingFailure` struct with original_request, routed_to, should_have_been, failure_signal
- [ ] `record_routing_failure` skill implementation
- [ ] `query_routing_failures` skill implementation
- [ ] `get_routing_accuracy` skill implementation
- [ ] Failure signal detection: EXPLICIT_REDIRECT, USER_CORRECTION, TASK_HELP_ESCALATION, CLARIFICATION_LOOP, EMPTY_RESPONSE
- [ ] Failures stored in Archivalist category "routing_failure"
- [ ] Query past failures during Intent Gate classification
- [ ] Adjust confidence scores based on similar past misroutes
- [ ] `GuideRoutingFailurePrompt` added to system prompt

### Architect: Pre-Delegation Planning Protocol

**Files to modify/create:**
- `agents/architect/pre_delegation.go`
- `agents/architect/consultation.go`

**Acceptance Criteria:**
- [ ] `pre_delegation_declare` skill implementation (MANDATORY)
- [ ] `consult_before_planning` skill implementation (MANDATORY)
- [ ] 4-part declaration format enforced
- [ ] Mandatory Librarian consultation (codebase health)
- [ ] Mandatory Archivalist consultation (failure patterns)
- [ ] Declaration stored in Archivalist for traceability
- [ ] Orchestrator validates declaration before dispatch
- [ ] `ArchitectPreDelegationPrompt` added to system prompt

### Architect: Atomic Task Generation System

The Architect generates engineering "tickets" - atomic, hyper-detailed implementation tasks
that pipeline agents execute. Quality of decomposition determines quality of implementation.

**Files to modify/create:**
- `agents/architect/task_generator.go`
- `agents/architect/task_types.go`
- `agents/architect/decomposition.go`
- `agents/architect/workflow_dag.go`
- `agents/architect/task_validator.go`

#### Deep Decomposition Engine
**Acceptance Criteria:**
- [ ] `DecompositionEngine` struct for request analysis
- [ ] `Decompose(request) DecompositionResult` method
- [ ] Extract explicit requirements from request
- [ ] Extract implicit assumptions (must be stated explicitly)
- [ ] Identify ambiguities requiring clarification
- [ ] Identify unknowns requiring agent consultation
- [ ] Enumerate edge cases (boundaries, empty, null, max, concurrent)
- [ ] Enumerate failure modes (what can go wrong)
- [ ] Identify design mitigations (trade-offs, alternatives considered)
- [ ] Map system context (how this fits into larger system)
- [ ] `DecompositionResult` struct with all extracted elements

#### Iterative Clarification Protocol
**Acceptance Criteria:**
- [ ] `ClarificationProtocol` struct for managing queries
- [ ] Query agents (via Guide) until answers are COMPLETE
- [ ] If agent answer incomplete, query again with more specificity
- [ ] If agent answer raises new questions, query those too
- [ ] Track all queries and responses for audit
- [ ] Only ask user after ALL agents exhausted
- [ ] User questions explain what was learned from agents
- [ ] User questions ask for MINIMAL information needed

#### Atomic Task Schema
**Acceptance Criteria:**
- [ ] `AtomicTask` struct with all required fields
- [ ] Task ID generation (unique, sequential within workflow)
- [ ] Title field (brief, descriptive)
- [ ] Description field (maximally informative, includes WHY)
- [ ] AcceptanceCriteria slice (specific, measurable, verifiable)
- [ ] TestCaseProposals slice (happy path, errors, edge cases, category refs)
- [ ] CodeReferences struct (files to create, patterns to follow, references)
- [ ] Dependencies slice (task IDs this depends on)
- [ ] FailureModes slice (what can go wrong, mitigations)
- [ ] JSON serialization for Orchestrator consumption

#### Task Field Requirements
**Acceptance Criteria:**
- [ ] AcceptanceCriterion struct: description, verifiable bool, category string
- [ ] TestCaseProposal struct: scenario, category (1-6), expectedOutcome
- [ ] CodeReference struct: path, action (create/modify/reference), pattern string
- [ ] Dependency struct: taskID, type (hard/soft), reason
- [ ] FailureMode struct: description, probability, mitigation
- [ ] Validation that all required fields are populated

#### Task Validator
**Acceptance Criteria:**
- [ ] `TaskValidator` struct for quality checks
- [ ] `Validate(task) []ValidationError` method
- [ ] Verify task is ATOMIC (cannot be split further)
- [ ] Verify task is INDEPENDENT (no circular deps)
- [ ] Verify task is TESTABLE (has verifiable criteria)
- [ ] Verify task is SCOPED (clear boundaries)
- [ ] Verify all 7 sections are populated
- [ ] Verify acceptance criteria are specific (not vague)
- [ ] Verify dependencies reference valid task IDs
- [ ] Reject tasks with validation errors

#### Workflow DAG Generator
**Acceptance Criteria:**
- [ ] `WorkflowDAG` struct for task organization
- [ ] `GenerateDAG(tasks []AtomicTask) (*WorkflowDAG, error)` method
- [ ] Topological sort of tasks by dependencies
- [ ] Identify tasks with no dependencies (Wave 1 candidates)
- [ ] Identify parallel execution groups (no conflicts)
- [ ] Organize into Waves (Foundation → Core → Integration → Validation)
- [ ] Detect circular dependencies (error if found)
- [ ] Detect missing dependencies (error if referenced task doesn't exist)
- [ ] Calculate critical path for estimation

#### Wave Organization
**Acceptance Criteria:**
- [ ] `Wave` struct: number, name, taskIDs, parallel bool, dependsOnWave
- [ ] Wave 1: Foundation (types, interfaces, shared utilities)
- [ ] Wave 2+: Core implementation (parallelizable)
- [ ] Later Waves: Integration (connect components)
- [ ] Final Wave: Validation (end-to-end verification)
- [ ] Tasks within a wave can execute in parallel unless noted
- [ ] Cross-wave dependencies properly tracked

#### Workflow Output Format
**Acceptance Criteria:**
- [ ] `WorkflowOutput` struct with JSON tags
- [ ] workflow_id field (unique identifier)
- [ ] total_tasks count
- [ ] waves array with wave details
- [ ] tasks map with full task specifications
- [ ] critical_path for estimation
- [ ] estimated_parallelism (max concurrent tasks)
- [ ] JSON serialization for Orchestrator handoff

#### Integration Tests
**Acceptance Criteria:**
- [ ] Test decomposition extracts all requirement types
- [ ] Test clarification protocol queries until complete
- [ ] Test task generation produces valid AtomicTasks
- [ ] Test task validation catches incomplete tasks
- [ ] Test DAG generation produces valid topological order
- [ ] Test wave organization groups correctly
- [ ] Test circular dependency detection
- [ ] Test full pipeline from request to workflow

### Orchestrator: Execution Discipline Protocol

**Files to modify/create:**
- `agents/orchestrator/execution_discipline.go`
- `agents/orchestrator/validation.go`

**Acceptance Criteria:**
- [ ] Declaration validation before dispatch
- [ ] Reject TASK_DISPATCH without pre_delegation_declaration
- [ ] Verify librarian_health and archivalist_check fields
- [ ] Dependency tracking and validation
- [ ] Failure coordination across tasks
- [ ] Escalation to Architect after failure threshold
- [ ] Progress signal enforcement

### Engineer: Failure Recovery Protocol

**Files to modify/create:**
- `agents/engineer/failure_recovery.go`
- `agents/engineer/checkpoint.go`

**Hooks to add:**
- [ ] `failure_counter` PostTool hook
- [ ] `failure_protocol_injection` PrePrompt hook
- [ ] `checkpoint_creation` PreTool hook

**Acceptance Criteria:**
- [ ] Consecutive failure tracking per task
- [ ] Warning injection at 2 failures
- [ ] Escalation at 3 failures
- [ ] Checkpoint creation before write operations
- [ ] Revert to checkpoint on escalation
- [ ] Failure recording to Archivalist
- [ ] `ErrFailureThresholdExceeded` error type
- [ ] `EngineerFailureRecoveryPrompt` added to system prompt

### Designer: Failure Recovery Protocol

**Files to modify/create:**
- `agents/designer/failure_recovery.go`

**Acceptance Criteria:**
- [ ] Same failure recovery hooks as Engineer
- [ ] Design-specific failure patterns (visual regression, accessibility)
- [ ] Checkpoint creation before component writes

### Inspector: Completion Evidence Protocol

**Files to modify/create:**
- `agents/inspector/completion_evidence.go`
- `agents/inspector/evidence_types.go`

**Acceptance Criteria:**
- [ ] `collect_build_evidence` skill implementation
- [ ] `collect_type_evidence` skill implementation
- [ ] `collect_lint_evidence` skill implementation
- [ ] `verify_pattern_compliance` skill implementation
- [ ] `store_evidence` skill implementation (MANDATORY)
- [ ] Evidence checklist: BUILD, TYPES, LINT, PATTERNS, SECURITY
- [ ] Evidence stored in Archivalist with task correlation
- [ ] "NO EVIDENCE = NOT COMPLETE" principle enforced
- [ ] `InspectorCompletionEvidencePrompt` added to system prompt

### Inspector: 8-Phase Validation System

The Inspector executes 8 sequential validation phases. ANY phase failure causes overall FAIL.
Inspector is RUTHLESS, SPECIFIC, and DETAILED - code must meet the HIGHEST bar.

**Files to modify/create:**
- `agents/inspector/validation_phases.go`
- `agents/inspector/phase_spec_compliance.go`
- `agents/inspector/phase_concurrency.go`
- `agents/inspector/phase_memory.go`
- `agents/inspector/phase_type_safety.go`
- `agents/inspector/phase_structure.go`
- `agents/inspector/phase_idioms.go`
- `agents/inspector/phase_documentation.go`
- `agents/inspector/phase_design.go`
- `agents/inspector/validation_report.go`

#### Phase 1: Spec Compliance Validation
**Acceptance Criteria:**
- [ ] `ValidationPhase` interface with `Name()`, `Execute()`, `Result()` methods
- [ ] `SpecCompliancePhase` struct implementing `ValidationPhase`
- [ ] Read and enumerate ALL requirements from provided specification
- [ ] Check each requirement against actual implementation
- [ ] Verify all required functions, types, interfaces exist
- [ ] Confirm error handling covers all specified error conditions
- [ ] Validate all edge cases mentioned in spec are handled
- [ ] FAIL if any required functionality missing
- [ ] FAIL if any specified behavior not implemented
- [ ] Report with exact file paths and line numbers for every check

#### Phase 2: Concurrency & Safety Validation
**Acceptance Criteria:**
- [ ] `ConcurrencyPhase` struct implementing `ValidationPhase`
- [ ] Detect race conditions in shared state access
- [ ] Verify proper mutex/lock usage around critical sections
- [ ] Detect potential deadlocks (lock ordering, nested locks)
- [ ] Validate channel usage (unbuffered blocking, closed channel writes)
- [ ] Check for goroutine leaks (goroutines that never terminate)
- [ ] Verify atomic operations used correctly
- [ ] Examine sync.WaitGroup, sync.Once, sync.Pool usage
- [ ] FAIL if any race condition detected
- [ ] FAIL if any deadlock potential identified
- [ ] FAIL if any goroutine leak pattern found

#### Phase 3: Memory & Resource Validation
**Acceptance Criteria:**
- [ ] `MemoryResourcePhase` struct implementing `ValidationPhase`
- [ ] Check for memory leaks (unclosed resources, retained references)
- [ ] Verify all opened resources properly closed (files, connections)
- [ ] Detect unbounded growth patterns (maps/slices without limits)
- [ ] Validate defer usage for cleanup
- [ ] Check context propagation for cancellation
- [ ] Verify error paths don't leak resources
- [ ] FAIL if any resource leak detected
- [ ] FAIL if unbounded memory growth pattern found
- [ ] FAIL if missing cleanup on error paths

#### Phase 4: Type Safety & Error Handling
**Acceptance Criteria:**
- [ ] `TypeSafetyPhase` struct implementing `ValidationPhase`
- [ ] Verify type safety (generics, interfaces, type assertions)
- [ ] Check for type hints/annotations (Python: required, TypeScript: required)
- [ ] Validate error types are SPECIFIC, not generic
- [ ] Ensure errors include context (wrapped with %w or equivalent)
- [ ] Check error handling is exhaustive (no swallowed errors)
- [ ] Verify sentinel errors used appropriately
- [ ] Validate custom error types implement error interface correctly
- [ ] FAIL if missing type annotations in typed languages
- [ ] FAIL if generic error messages without context
- [ ] FAIL if swallowed errors (caught but not handled/logged)

#### Phase 5: Code Structure & Complexity
**Acceptance Criteria:**
- [ ] `CodeStructurePhase` struct implementing `ValidationPhase`
- [ ] Check function length: FAIL if >100 lines, WARN if >50 lines
- [ ] Validate loop nesting: FAIL if >3 levels, WARN if >2 levels
- [ ] Check cyclomatic complexity: FAIL if >15, WARN if >10
- [ ] Verify parameter count: FAIL if >7 params, WARN if >5 params
- [ ] Detect god functions/methods (doing too many things)
- [ ] Check for proper separation of concerns
- [ ] Report line counts for every function exceeding thresholds
- [ ] Report nesting depth for every deep loop

#### Phase 6: Language Idioms & Modern Features
**Acceptance Criteria:**
- [ ] `LanguageIdiomsPhase` struct implementing `ValidationPhase`
- [ ] Verify use of modern language features (Go 1.21+, Python 3.10+, etc.)
- [ ] Check for outdated patterns with modern replacements
- [ ] Validate use of built-in methods over manual implementations
- [ ] Ensure idiomatic patterns for the language
- [ ] Check for proper use of standard library over reinventing
- [ ] FAIL if manual implementation of stdlib functionality
- [ ] FAIL if deprecated patterns when modern alternatives exist
- [ ] Language-specific ruleset registry

#### Phase 7: Documentation & Readability
**Acceptance Criteria:**
- [ ] `DocumentationPhase` struct implementing `ValidationPhase`
- [ ] Check all exported/public functions have documentation
- [ ] Verify docstrings describe WHAT and WHY, not just HOW
- [ ] Validate parameter and return value documentation
- [ ] Check for misleading or outdated comments
- [ ] Verify complex algorithms have explanatory comments
- [ ] Ensure error conditions are documented
- [ ] FAIL if exported function/type without documentation
- [ ] FAIL if documentation doesn't match implementation

#### Phase 8: Design & Architecture Smells
**Acceptance Criteria:**
- [ ] `DesignSmellsPhase` struct implementing `ValidationPhase`
- [ ] Check for inheritance abuse (prefer composition)
- [ ] Detect feature envy (method using more of another class's data)
- [ ] Identify shotgun surgery patterns (changes requiring many edits)
- [ ] Check for inappropriate intimacy between components
- [ ] Verify single responsibility principle
- [ ] Detect primitive obsession (using primitives vs small objects)
- [ ] Check for data clumps (same data group appearing together)
- [ ] FAIL if deep inheritance hierarchies (>3 levels)
- [ ] FAIL if circular dependencies between packages
- [ ] FAIL if tight coupling preventing testing

#### Validation Orchestration
**Acceptance Criteria:**
- [ ] `ValidationOrchestrator` struct managing all 8 phases
- [ ] `RegisterPhase(phase ValidationPhase)` method
- [ ] `Execute(ctx, spec, code) ValidationReport` method
- [ ] Sequential execution: stop at first phase failure
- [ ] Aggregate code smell counts across phases
- [ ] `ValidationReport` struct with per-phase results
- [ ] Report includes overall_result, failed_phase, code_smells
- [ ] Accumulator rules: 5+ minor OR 2+ moderate causes FAIL
- [ ] Each issue cites exact file path, line number, code snippet
- [ ] Knowledge agent consultation hooks (before Phase 1, during 2-3, for 6-8)

#### Validation Report Schema
**Acceptance Criteria:**
- [ ] `ValidationReport` struct with JSON tags
- [ ] `PhaseResult` struct: result, issues, evidence
- [ ] `ValidationIssue` struct: severity, file, line, snippet, message, fix
- [ ] `CodeSmell` struct: type, severity (minor/moderate/severe), location
- [ ] JSON serialization for Archivalist storage
- [ ] Human-readable rendering for CLI output
- [ ] Summary generation for Architect reporting

#### Integration Tests
**Acceptance Criteria:**
- [ ] Test Phase 1 with complete vs incomplete spec compliance
- [ ] Test Phase 2 with race condition examples
- [ ] Test Phase 3 with resource leak examples
- [ ] Test Phase 4 with error handling edge cases
- [ ] Test Phase 5 with complexity threshold violations
- [ ] Test Phase 6 with outdated vs modern code patterns
- [ ] Test Phase 7 with undocumented code
- [ ] Test Phase 8 with design smell examples
- [ ] Test accumulator thresholds (5 minor, 2 moderate, 1 severe)
- [ ] Test full pipeline with passing code
- [ ] Test full pipeline with failing code at each phase

### Tester: Completion Evidence Protocol

**Files to modify/create:**
- `agents/tester/completion_evidence.go`
- `agents/tester/evidence_types.go`

**Acceptance Criteria:**
- [ ] `collect_test_evidence` skill implementation
- [ ] `collect_coverage_evidence` skill implementation
- [ ] `verify_no_regression` skill implementation
- [ ] `verify_new_tests_exist` skill implementation
- [ ] `store_evidence` skill implementation (MANDATORY)
- [ ] Evidence checklist: TESTS_PASS, COVERAGE, REGRESSION, NEW_TESTS
- [ ] Evidence stored in Archivalist with task correlation
- [ ] "NO EVIDENCE = NOT COMPLETE" principle enforced
- [ ] `TesterCompletionEvidencePrompt` added to system prompt

### Tester: 6-Category Test System

The Tester generates THOROUGH tests across 6 categories. Tests code as if SYSTEM CRITICAL.
Tester is a SEASONED QE/SDET - discerning, thorough, and focused on real value.

**CRITICAL**: Tester MUST analyze implementation code BEFORE deciding what to test.
Categories 5 (Concurrency) and 6 (Integration) are CONDITIONAL - only apply when implementation requires.

**Files to modify/create:**
- `agents/tester/test_categories.go`
- `agents/tester/implementation_analyzer.go` (NEW - analyzes code to determine applicable categories)
- `agents/tester/category_happy_path.go`
- `agents/tester/category_negative_path.go`
- `agents/tester/category_error_handling.go`
- `agents/tester/category_edge_cases.go`
- `agents/tester/category_concurrency.go`
- `agents/tester/category_integration.go`
- `agents/tester/test_quality.go`
- `agents/tester/test_report.go`

#### Implementation Analysis (MANDATORY FIRST STEP)
**Acceptance Criteria:**
- [ ] `ImplementationAnalyzer` struct for code examination
- [ ] `Analyze(code) ImplementationProfile` method
- [ ] Detect all public APIs and their contracts
- [ ] Identify all error conditions and failure modes
- [ ] Detect concurrency patterns (goroutines, channels, mutexes, atomics, sync.*)
- [ ] Detect external dependencies (HTTP, DB, file I/O, network)
- [ ] `ImplementationProfile` struct with: hasConcurrency, hasExternalDeps, errorPaths, publicAPIs
- [ ] `DetermineApplicableCategories(profile) []CategoryType` method
- [ ] Categories 1-4 always applicable, 5-6 conditional on profile
- [ ] Analysis results stored for test plan justification

#### Category 1: Happy Path Tests
**Acceptance Criteria:**
- [ ] `TestCategory` interface with `Name()`, `Generate()`, `Validate()` methods
- [ ] `HappyPathCategory` struct implementing `TestCategory`
- [ ] Test primary use case with valid inputs
- [ ] Verify expected outputs and state changes
- [ ] Cover the "golden path" users typically follow
- [ ] Minimum 1 happy path test per public function/method enforced
- [ ] Auto-detection of public functions requiring happy path tests

#### Category 2: Negative Path Tests
**Acceptance Criteria:**
- [ ] `NegativePathCategory` struct implementing `TestCategory`
- [ ] Test with invalid inputs (null, empty, malformed)
- [ ] Test boundary violations (too large, too small, out of range)
- [ ] Test type mismatches where language allows
- [ ] Verify appropriate errors returned (not panics/crashes)
- [ ] Minimum 2-3 negative cases per public function enforced
- [ ] Input validation coverage tracking

#### Category 3: Error Handling Tests
**Acceptance Criteria:**
- [ ] `ErrorHandlingCategory` struct implementing `TestCategory`
- [ ] Test every error condition the code can produce
- [ ] Verify error messages are informative
- [ ] Verify error types are specific (not generic)
- [ ] Test error propagation through call chains
- [ ] Test cleanup/rollback on error paths
- [ ] Minimum 1 test per distinct error condition enforced
- [ ] Error path enumeration from code analysis

#### Category 4: Edge Case Tests
**Acceptance Criteria:**
- [ ] `EdgeCaseCategory` struct implementing `TestCategory`
- [ ] Test boundary values (0, 1, max-1, max, overflow)
- [ ] Test empty collections, single items, large collections
- [ ] Test unicode, special characters, very long strings
- [ ] Test concurrent access patterns
- [ ] Test timeout/cancellation scenarios
- [ ] Minimum 3-5 edge cases per complex function enforced
- [ ] Boundary value analysis integration

#### Category 5: Concurrency Tests (CONDITIONAL)
**PREREQUISITE**: ImplementationAnalyzer.hasConcurrency == true

Only generate if implementation contains: goroutines, channels, sync.Mutex, sync.RWMutex,
sync.WaitGroup, sync.Once, sync.Pool, sync.Map, sync/atomic, or shared state.

Writing concurrency tests for synchronous code is JUNK TESTING - do not do it.

**Acceptance Criteria:**
- [ ] `ConcurrencyCategory` struct implementing `TestCategory`
- [ ] `IsApplicable(profile ImplementationProfile) bool` method - checks hasConcurrency
- [ ] SKIP generation entirely if IsApplicable returns false
- [ ] Test for race conditions with parallel execution
- [ ] Test for deadlocks with lock acquisition patterns
- [ ] Test channel operations (send/receive, close, select)
- [ ] Test goroutine lifecycle (creation, completion, leak prevention)
- [ ] Use race detector during test execution (`go test -race`)
- [ ] Verify proper cleanup and no goroutine leaks
- [ ] Report in test plan WHY concurrency tests were included (cite specific patterns found)

#### Category 6: Integration Tests (CONDITIONAL)
**PREREQUISITE**: ImplementationAnalyzer.hasExternalDeps == true

Only generate if implementation contains: HTTP clients, database connections, file I/O,
network sockets, message queues, external service calls, or cross-component interactions.

Writing integration tests for pure functions is JUNK TESTING - do not do it.

**Acceptance Criteria:**
- [ ] `IntegrationCategory` struct implementing `TestCategory`
- [ ] `IsApplicable(profile ImplementationProfile) bool` method - checks hasExternalDeps
- [ ] SKIP generation entirely if IsApplicable returns false
- [ ] Test component interactions with realistic scenarios
- [ ] Test with real dependencies OR realistic mocks (prefer real when fast)
- [ ] Test end-to-end flows for critical paths
- [ ] Test failure modes of dependencies (timeouts, errors, unavailability)
- [ ] Test retry and recovery behavior
- [ ] Report in test plan WHY integration tests were included (cite specific deps found)

#### Test Quality Enforcement
**Acceptance Criteria:**
- [ ] `TestQualityChecker` struct for validation
- [ ] Single assertion per test validation (cyclomatic complexity check)
- [ ] Test naming convention enforcement: `Test<Function>_<Scenario>_<Expected>`
- [ ] Arrange-Act-Assert pattern detection
- [ ] Test independence validation (no shared mutable state)
- [ ] Parallelization capability check
- [ ] Flakiness detection (run 3x on uncertainty)

#### Junk Test Prevention
**Acceptance Criteria:**
- [ ] `JunkTestDetector` struct for filtering
- [ ] Detect tests of language built-ins (Go's append, Python's len, etc.)
- [ ] Detect tests of third-party library internals
- [ ] Detect tests of private implementation details
- [ ] Detect tests that always pass regardless of implementation
- [ ] Detect tests slower than code under test
- [ ] Detect concurrency tests for synchronous code (no goroutines/channels/locks)
- [ ] Detect integration tests for pure functions (no external deps)
- [ ] Cross-reference with ImplementationProfile to catch category mismatches
- [ ] Warning/rejection for junk test patterns
- [ ] Configurable allowlist for intentional exceptions

#### Test Report Generation
**Acceptance Criteria:**
- [ ] `TestReport` struct with JSON tags
- [ ] Per-category breakdown (count, test names, results)
- [ ] Coverage metrics (line coverage, branch coverage, uncovered lines)
- [ ] Execution metrics (total, passed, failed, skipped, duration)
- [ ] Quality assessment (all categories covered, no junk, independent, not flaky)
- [ ] JSON serialization for Archivalist storage
- [ ] Human-readable rendering for CLI output

#### Test Orchestration
**Acceptance Criteria:**
- [ ] `TestOrchestrator` struct managing all 6 categories
- [ ] `RegisterCategory(category TestCategory)` method
- [ ] `Analyze(implementation) TestPlan` method
- [ ] `Generate(plan) []TestFile` method
- [ ] `Execute(tests) TestReport` method
- [ ] Category applicability detection per implementation
- [ ] Coverage threshold enforcement (80%+ line, 70%+ branch)
- [ ] Knowledge agent consultation hooks (Librarian for patterns, Archivalist for history)

#### Integration Tests for Tester
**Acceptance Criteria:**
- [ ] Test happy path category generation
- [ ] Test negative path category with various invalid inputs
- [ ] Test error handling category finds all error paths
- [ ] Test edge case category boundary detection
- [ ] Test concurrency category detects shared state
- [ ] Test integration category dependency detection
- [ ] Test junk test detector catches bad patterns
- [ ] Test quality checker validates structure
- [ ] Test full orchestration pipeline
- [ ] Test report accuracy and completeness

### Cross-Agent Discipline Integration

**Acceptance Criteria:**
- [ ] Guide's Intent Gate triggers pre-routing consultations
- [ ] Librarian's health assessment flows to Architect's declarations
- [ ] Archivalist's failure patterns inform all planning decisions
- [ ] Academic's recommendations validated against Librarian's codebase state
- [ ] Engineer failures recorded in Archivalist for cross-session learning
- [ ] Inspector/Tester evidence stored in Archivalist for future queries

### Knowledge Agent Feedback Loops

**Acceptance Criteria:**

#### Guide → Archivalist Feedback
- [ ] Guide records routing failures in Archivalist
- [ ] Guide queries past routing failures during Intent Gate
- [ ] Routing accuracy metrics tracked over time

#### Librarian → Archivalist Feedback
- [ ] Engineer/Inspector failures with context_source: "librarian" trigger feedback
- [ ] Librarian queries past context feedback during query processing
- [ ] Context quality metrics tracked over time
- [ ] Cache entries invalidated based on feedback

#### Archivalist Self-Monitoring
- [ ] Retrieval accuracy issues logged
- [ ] Self-healing actions triggered per issue type
- [ ] Storage verification on all writes
- [ ] Proactive staleness detection

#### Academic → Archivalist Feedback
- [ ] Recommendation outcomes recorded after implementation
- [ ] Academic queries past outcomes for similar research
- [ ] Success rate metrics by topic, maturity, applicability
- [ ] Confidence adjusted based on historical success

#### Complete Feedback Flow
```
Engineer FAILURE
    ↓
Archivalist records with context_source
    ↓
├── If context_source = "librarian" → Librarian receives feedback
├── If context_source = "academic" → Academic receives feedback
└── If routing_issue → Guide receives feedback

All feedback stored cross-session for continuous improvement
```

### Guide: Additional Discipline Protocols

**Files to modify:**
- `agents/guide/ambiguity.go` (new)
- `agents/guide/enrichment.go` (new)
- `agents/guide/skill_verification.go` (new)

#### Guide Ambiguity Resolution Discipline
**Acceptance Criteria:**
- [ ] `AmbiguityDetector` struct with detection logic
- [ ] Detect multi-agent overlap (request matches 2+ agents with >0.5 confidence)
- [ ] Detect unbounded scope (no clear "done" criteria)
- [ ] Detect implicit assumptions (relies on unstated context)
- [ ] Detect conflicting signals (keywords suggest different types)
- [ ] Query routing failures for similar past misroutes
- [ ] Generate clarifying questions with specific options
- [ ] Track clarifications in session for future similar requests
- [ ] NEVER route with confidence < 0.7 - enforce asking instead
- [ ] Metrics: ambiguity detection rate, clarification-to-success rate

**Implementation Guide:**
```go
type AmbiguityDetector struct {
    routingHistory   *RoutingHistoryCache
    archivalistClient ArchivalistClient
}

func (d *AmbiguityDetector) DetectAmbiguity(ctx context.Context, request string) (*AmbiguityResult, error)
func (d *AmbiguityDetector) GenerateClarification(result *AmbiguityResult) *ClarificationQuestion
```

#### Guide Pre-Routing Enrichment Protocol
**Acceptance Criteria:**
- [ ] `EnrichmentService` struct managing pre-routing consultations
- [ ] Mandatory Librarian query: "What is codebase health for [target area]?"
- [ ] Mandatory Archivalist query: "Any failure patterns for [approach/area]?"
- [ ] 5-second timeout per consultation, proceed with warning if unavailable
- [ ] Attach enriched_context to routed message
- [ ] Skip enrichment for TRIVIAL requests and skill-matched requests
- [ ] Warn user if both consultations fail, ask if proceed anyway
- [ ] `EnrichedContext` struct with librarian_health, archivalist_warnings, status

**Implementation Guide:**
```go
type EnrichmentService struct {
    librarianClient   LibrarianClient
    archivalistClient ArchivalistClient
    timeout           time.Duration // 5 seconds
}

func (s *EnrichmentService) Enrich(ctx context.Context, request string) (*EnrichedContext, error)
```

#### Guide Skill Match Verification Protocol
**Acceptance Criteria:**
- [ ] `SkillVerifier` struct tracking skill execution outcomes
- [ ] Verify execution outcome after each skill match execution
- [ ] Track outcomes: SUCCESS, REROUTE_NEEDED, EXECUTION_FAILED, USER_REJECTED
- [ ] Adjust confidence: SUCCESS +0.01, REROUTE -0.05, FAILED -0.10, REJECTED -0.15
- [ ] Demotion rule: 3+ failures demotes skill from fast path to LLM routing
- [ ] Log verification results for pattern learning
- [ ] `SkillVerificationLog` stored in Archivalist

**Implementation Guide:**
```go
type SkillVerifier struct {
    outcomeTracker *OutcomeTracker
}

func (v *SkillVerifier) RecordOutcome(skillName string, outcome SkillOutcome) error
func (v *SkillVerifier) ShouldDemote(skillPattern string) bool
```

### Academic: Additional Discipline Protocols

**Files to modify:**
- `agents/academic/citation.go` (new)
- `agents/academic/depth_calibration.go` (new)
- `agents/academic/synthesis.go` (new)

#### Academic Source Citation Discipline
**Acceptance Criteria:**
- [ ] `Citation` struct with source_type, url, title, date, freshness, verification, confidence
- [ ] Source types: official_docs, blog_post, research_paper, community, internal
- [ ] Freshness calculation: current (<6mo), recent (6-24mo), aging (24-48mo), stale (>48mo)
- [ ] Stale sources cap confidence at MEDIUM
- [ ] NEVER fabricate sources - use "unverified" when cannot cite
- [ ] `CitationValidator` ensures all recommendations have citations
- [ ] Citation format in responses includes source, type, freshness, confidence

**Implementation Guide:**
```go
type Citation struct {
    SourceType   SourceType `json:"source_type"`
    URL          string     `json:"url"`
    Title        string     `json:"title"`
    Date         string     `json:"date"`
    Freshness    Freshness  `json:"freshness"`
    Verification Verification `json:"verification"`
    Confidence   Confidence `json:"confidence"`
}
```

#### Academic Research Depth Calibration Protocol
**Acceptance Criteria:**
- [ ] `DepthCalibrator` struct for research depth selection
- [ ] Initial depth selection: QUICK (1-2 sources), STANDARD (3-5), DEEP (5-10)
- [ ] Auto-escalation triggers: confidence <0.7, sources conflict, Librarian incompatibility, past failure
- [ ] Critical topics always DEEP: auth, security, encryption, schema, concurrency
- [ ] Report depth in response: "Research depth: [LEVEL] - [reason]"
- [ ] Track escalation triggers and patterns

**Implementation Guide:**
```go
type DepthCalibrator struct {
    librarianClient   LibrarianClient
    archivalistClient ArchivalistClient
}

func (c *DepthCalibrator) SelectDepth(ctx context.Context, topic string) (Depth, string)
func (c *DepthCalibrator) ShouldEscalate(results *ResearchResults) (bool, string)
```

#### Academic Cross-Agent Research Synthesis Protocol
**Acceptance Criteria:**
- [ ] `SynthesisService` coordinating Academic and Librarian
- [ ] Detect when synthesis required: code patterns, multiple approaches, "we/our" questions
- [ ] Query Librarian for existing patterns during research
- [ ] Gap analysis: ALIGNED, MINOR_GAP, MAJOR_GAP, INCOMPATIBLE, GREENFIELD
- [ ] Unified response format with external_best_practice, codebase_current_pattern, gap_analysis
- [ ] Adaptation steps for non-aligned recommendations

**Implementation Guide:**
```go
type SynthesisService struct {
    librarianClient LibrarianClient
}

func (s *SynthesisService) Synthesize(ctx context.Context, research *ResearchResult) (*SynthesizedRecommendation, error)
```

### Architect: Additional Discipline Protocols

**Files to modify:**
- `agents/architect/complexity.go` (new)
- `agents/architect/assumptions.go` (new)
- `agents/architect/failure_integration.go` (new)
- `agents/architect/scope_creep.go` (new)

#### Architect Plan Complexity Discipline
**Acceptance Criteria:**
- [ ] `ComplexityAnalyzer` struct enforcing limits
- [ ] Limits: MAX_TASKS=20, MAX_DEPTH=5, MAX_PARALLEL=8, MAX_ENGINEER_SCOPE=12
- [ ] Threshold actions: >10 WARN, >15 REQUIRE justification, >20 BLOCK
- [ ] `complexity_metrics` in every plan: total_tasks, max_depth, max_parallel, critical_path_length
- [ ] Complexity rating: LOW, MEDIUM, HIGH, EXCESSIVE
- [ ] EXCESSIVE triggers phase splitting

**Implementation Guide:**
```go
type ComplexityAnalyzer struct {
    limits ComplexityLimits
}

func (a *ComplexityAnalyzer) Analyze(plan *Plan) *ComplexityReport
func (a *ComplexityAnalyzer) ShouldSplit(report *ComplexityReport) bool
```

#### Architect Assumption Documentation Protocol
**Acceptance Criteria:**
- [ ] `Assumption` struct with category, assumption, source, verified, risk_if_wrong
- [ ] Categories: ENVIRONMENTAL, CODEBASE, BEHAVIORAL, PERFORMANCE, SECURITY, DATA
- [ ] Verification requirements per category (Librarian, user, spec)
- [ ] SECURITY assumptions ALWAYS require explicit verification
- [ ] Unverified assumptions marked for Inspector/Tester validation
- [ ] Every plan includes "assumptions" section

**Implementation Guide:**
```go
type Assumption struct {
    Category    AssumptionCategory `json:"category"`
    Assumption  string             `json:"assumption"`
    Source      string             `json:"source"`
    Verified    bool               `json:"verified"`
    RiskIfWrong string             `json:"risk_if_wrong"`
}
```

#### Architect Historical Failure Integration Protocol
**Acceptance Criteria:**
- [ ] Mandatory Archivalist query for every task: "Failure patterns for [approach]?"
- [ ] Threshold actions: 0=normal, 1=WARNING, 2=REQUIRE mitigation, 3+=alternative or acknowledgment
- [ ] Success rate <50% = RED FLAG, recommend alternative
- [ ] Task annotation with historical_failures: count, warnings, mitigations, alternative_considered
- [ ] Never suppress failure warnings

**Implementation Guide:**
```go
type HistoricalFailureIntegrator struct {
    archivalistClient ArchivalistClient
}

func (i *HistoricalFailureIntegrator) GetFailureContext(ctx context.Context, approach string) (*FailureContext, error)
func (i *HistoricalFailureIntegrator) AnnotateTask(task *Task, failures *FailureContext) error
```

#### Architect Scope Creep Detection Protocol
**Acceptance Criteria:**
- [ ] `ScopeMonitor` tracking original vs current scope
- [ ] Detect: NEW_TASK_ADDED, TASK_EXPANDED, DEPENDENCY_DISCOVERED, REQUIREMENT_CHANGED
- [ ] Thresholds: +20% tasks=WARN, +50%=BLOCK, >2 new deps=WARN, >12 Engineer todos=BLOCK
- [ ] Actions: WARN (log), BLOCK (pause), RE-PLAN (new plan), SPLIT (separate workflow)
- [ ] Scope boundary tracking with original_scope and current_scope

**Implementation Guide:**
```go
type ScopeMonitor struct {
    originalScope *ScopeBoundary
    currentScope  *ScopeBoundary
}

func (m *ScopeMonitor) DetectCreep() *ScopeCreepResult
func (m *ScopeMonitor) GetAction(result *ScopeCreepResult) ScopeAction
```

### Engineer: Additional Discipline Protocols

**Files to modify:**
- `agents/engineer/pre_impl_verification.go` (new)
- `agents/engineer/incremental_validation.go` (new)
- `agents/engineer/pattern_adherence.go` (new)
- `agents/engineer/rollback_docs.go` (new)

#### Engineer Pre-Implementation Verification Protocol
**Acceptance Criteria:**
- [ ] `PreImplVerifier` struct running mandatory checks
- [ ] Verify target files exist (Read/Glob confirmation)
- [ ] Verify patterns match task description (Librarian query if mismatch)
- [ ] Verify dependencies available (imports exist, packages accessible)
- [ ] Verify build passes before modification
- [ ] Verify tests pass for affected area
- [ ] Verification checklist logged for each task
- [ ] STOP if any verification fails, report and escalate

**Implementation Guide:**
```go
type PreImplVerifier struct {
    librarianClient LibrarianClient
    buildRunner     BuildRunner
    testRunner      TestRunner
}

func (v *PreImplVerifier) Verify(ctx context.Context, task *Task) (*VerificationResult, error)
```

#### Engineer Incremental Validation Protocol
**Acceptance Criteria:**
- [ ] `IncrementalValidator` for complex tasks (>5 steps, multi-file)
- [ ] Validation checkpoints after each file modification
- [ ] Run build, type check, affected tests at each checkpoint
- [ ] Fix failures before continuing to next step
- [ ] Checkpoint log with action, build status, test status, fix applied
- [ ] Benefits: earlier error detection, smaller debug scope, natural rollback points

**Implementation Guide:**
```go
type IncrementalValidator struct {
    checkpoints []Checkpoint
}

func (v *IncrementalValidator) AddCheckpoint(action string) error
func (v *IncrementalValidator) ValidateCheckpoint(ctx context.Context) (*CheckpointResult, error)
```

#### Engineer Pattern Adherence Discipline
**Acceptance Criteria:**
- [ ] `PatternAdherence` struct enforcing consistency
- [ ] Query Librarian for canonical pattern before implementation
- [ ] Read 2-3 examples of pattern in actual codebase
- [ ] Match: naming, structure, error handling, logging, testing, documentation
- [ ] Deviation requires justification: pattern, existing, deviation, justification, approved_by
- [ ] FORBIDDEN: new patterns when existing works, "improving" during implementation

**Implementation Guide:**
```go
type PatternAdherence struct {
    librarianClient LibrarianClient
}

func (p *PatternAdherence) GetCanonicalPattern(ctx context.Context, area string) (*Pattern, error)
func (p *PatternAdherence) ValidateAdherence(code string, pattern *Pattern) (*AdherenceResult, error)
```

#### Engineer Rollback Documentation Protocol
**Acceptance Criteria:**
- [ ] `RollbackDoc` struct with change_id, change_summary, files, rollback_steps, verified, dependencies, data_impact
- [ ] Required for: schema changes, API changes, config changes, deps, refactoring
- [ ] Rollback steps must be explicit and actionable
- [ ] Store in Archivalist with task correlation
- [ ] Automatic rollback triggers: Inspector fail, Tester fail, 3rd failure, user request

**Implementation Guide:**
```go
type RollbackDoc struct {
    ChangeID        string   `json:"change_id"`
    ChangeSummary   string   `json:"change_summary"`
    FilesModified   []string `json:"files_modified"`
    RollbackSteps   []string `json:"rollback_steps"`
    RollbackVerified bool    `json:"rollback_verified"`
    Dependencies    string   `json:"dependencies"`
    DataImpact      string   `json:"data_impact"`
}
```

### Designer: Additional Discipline Protocols

**Files to modify:**
- `agents/designer/visual_regression.go` (new)
- `agents/designer/token_validation.go` (new)
- `agents/designer/responsive_testing.go` (new)
- `agents/designer/animation_perf.go` (new)

#### Designer Visual Regression Awareness Protocol
**Acceptance Criteria:**
- [ ] Query Archivalist for visual regression history before modifying component
- [ ] Risk levels: 0=normal, 1-2=extra testing, 3+=FRAGILE
- [ ] FRAGILE requirements: before/after screenshots, all breakpoints, visual diff
- [ ] Always FRAGILE: header, nav, footer, checkout, payment, auth
- [ ] Document: component, regression_risk, past_issues, extra_verification, visual_diff_required

**Implementation Guide:**
```go
type VisualRegressionAwareness struct {
    archivalistClient ArchivalistClient
}

func (v *VisualRegressionAwareness) GetRegressionHistory(ctx context.Context, component string) (*RegressionHistory, error)
func (v *VisualRegressionAwareness) IsFragile(component string, history *RegressionHistory) bool
```

#### Designer Token Validation Gate Protocol
**Acceptance Criteria:**
- [ ] `TokenValidator` as BLOCKING gate before completion
- [ ] Detect hardcoded: colors (hex, rgb, hsl), spacing (px), typography, shadows, borders, breakpoints
- [ ] Zero violations required to pass
- [ ] Validation report: validation_passed, violations, token_coverage, blocking
- [ ] Exception process: document why, request token creation, get approval

**Implementation Guide:**
```go
type TokenValidator struct {
    tokenRegistry *DesignTokenRegistry
}

func (v *TokenValidator) Validate(componentCode string) (*TokenValidationResult, error)
func (v *TokenValidator) IsBlocking(result *TokenValidationResult) bool
```

#### Designer Responsive Testing Discipline
**Acceptance Criteria:**
- [ ] Mandatory breakpoints: SM (320px), MD (768px), LG (1024px), XL (1440px)
- [ ] SM verification: no horizontal scroll, text readable (14px+), touch targets 44px+
- [ ] MD verification: layout transitions, no orphaned elements
- [ ] LG verification: full layout, grids intact
- [ ] XL verification: no excessive stretching, max-width respected
- [ ] Completion requires breakpoint_verification object with all breakpoints verified

**Implementation Guide:**
```go
type ResponsiveTester struct {
    breakpoints []Breakpoint
}

func (t *ResponsiveTester) TestAtBreakpoint(component string, bp Breakpoint) (*BreakpointResult, error)
func (t *ResponsiveTester) GenerateReport() *ResponsiveTestReport
```

#### Designer Animation Performance Protocol
**Acceptance Criteria:**
- [ ] Performance budget: 60fps target, 30fps minimum
- [ ] Safe properties: transform, opacity, filter
- [ ] Avoid animating: width, height, padding, margin, top/left/right/bottom, font-size
- [ ] Animation review checklist: name, duration, properties, gpu_accelerated, performance_safe
- [ ] REQUIRED: prefers-reduced-motion support for all animations

**Implementation Guide:**
```go
type AnimationReviewer struct {
    safeProperties []string
    avoidProperties []string
}

func (r *AnimationReviewer) ReviewAnimation(animation *Animation) (*AnimationReview, error)
func (r *AnimationReviewer) SupportsReducedMotion(code string) bool
```

### Inspector: Additional Discipline Protocols

**Files to modify:**
- `agents/inspector/historical_crossref.go` (new)
- `agents/inspector/evidence_docs.go` (new)
- `agents/inspector/progressive_validation.go` (new)
- `agents/inspector/false_positive.go` (new)

#### Inspector Historical Issue Cross-Reference Protocol
**Acceptance Criteria:**
- [ ] Query Archivalist for each file: "Similar patterns that caused issues?"
- [ ] Thresholds: 0=standard, 1-2=WARNING, 3+=ALERT, known critical=BLOCK
- [ ] Pattern types: ERROR_PATTERNS, CONCURRENCY_PATTERNS, SECURITY_PATTERNS, PERFORMANCE_PATTERNS, INTEGRATION_PATTERNS
- [ ] Add historical_cross_reference to validation report
- [ ] Annotation: "⚠️ HISTORICAL: Pattern matched N past issues"

**Implementation Guide:**
```go
type HistoricalCrossRef struct {
    archivalistClient ArchivalistClient
}

func (h *HistoricalCrossRef) CheckHistory(ctx context.Context, file string) (*HistoricalMatchResult, error)
```

#### Inspector Validation Evidence Documentation Protocol
**Acceptance Criteria:**
- [ ] Every issue requires: file, line, snippet, issue, severity, category, fix, reproduction
- [ ] Severities: CRITICAL, HIGH, MEDIUM, LOW
- [ ] FORBIDDEN: vague issues, missing line numbers, no fix suggestion, unmarked severity
- [ ] Evidence format enforced by `EvidenceValidator`
- [ ] Engineers need precise evidence for quick fixes

**Implementation Guide:**
```go
type ValidationEvidence struct {
    File         string   `json:"file"`
    Line         int      `json:"line"`
    Snippet      string   `json:"snippet"`
    Issue        string   `json:"issue"`
    Severity     Severity `json:"severity"`
    Category     string   `json:"category"`
    Fix          string   `json:"fix"`
    Reproduction string   `json:"reproduction"`
}
```

#### Inspector Progressive Validation Protocol
**Acceptance Criteria:**
- [ ] Phase 1 (FAST, <5s): Build errors, type errors, syntax issues
- [ ] Phase 2 (STANDARD): Lint, pattern compliance, basic security
- [ ] Phase 3 (DEEP): Concurrency, memory/resource, complex patterns, historical cross-ref
- [ ] Fast-fail: If Phase 1 fails, STOP and report
- [ ] Report indicates phase: "Stopped at Phase 1: Build failed"
- [ ] Benefits: save time, faster feedback, reserve deep analysis for worthy code

**Implementation Guide:**
```go
type ProgressiveValidator struct {
    phases []ValidationPhase
}

func (v *ProgressiveValidator) Validate(ctx context.Context, code string) (*ProgressiveResult, error)
```

#### Inspector False Positive Tracking Protocol
**Acceptance Criteria:**
- [ ] Track overrides: USER_OVERRIDE, ARCHITECT_DISMISS, PATTERN_EXCEPTION, EXTERNAL_CONSTRAINT
- [ ] False positive record: original_issue, override_type, reason, pattern_signature, recurrence_count
- [ ] Learning: 1=track, 2=flag potential FP, 3+=reduce severity, 5+=consider removing
- [ ] Query during validation: "Has this pattern been overridden?"
- [ ] Note in report if pattern was previously overridden

**Implementation Guide:**
```go
type FalsePositiveTracker struct {
    archivalistClient ArchivalistClient
}

func (t *FalsePositiveTracker) RecordOverride(issue *ValidationIssue, override OverrideType) error
func (t *FalsePositiveTracker) ShouldAdjustSeverity(pattern string) (bool, Severity)
```

### Tester: Additional Discipline Protocols

**Files to modify:**
- `agents/tester/existing_test_integration.go` (new)
- `agents/tester/flaky_prevention.go` (new)
- `agents/tester/coverage_prioritization.go` (new)
- `agents/tester/maintenance_burden.go` (new)

#### Tester Existing Test Integration Protocol
**Acceptance Criteria:**
- [ ] Query Librarian before writing: "Tests for [affected code]?"
- [ ] Read existing test files in package
- [ ] Identify existing: fixtures, mocks, helpers
- [ ] REUSE existing fixtures and mocks (don't duplicate)
- [ ] MATCH existing naming conventions and assertion style
- [ ] Conflict detection: no duplicate scenarios, no conflicting assertions
- [ ] Document: existing_tests_found, fixtures_used, new_tests_added, conflicts_detected

**Implementation Guide:**
```go
type ExistingTestIntegrator struct {
    librarianClient LibrarianClient
}

func (i *ExistingTestIntegrator) DiscoverExisting(ctx context.Context, pkg string) (*ExistingTestInfo, error)
func (i *ExistingTestIntegrator) CheckConflicts(newTests []*Test, existing *ExistingTestInfo) []Conflict
```

#### Tester Flaky Test Prevention Protocol
**Acceptance Criteria:**
- [ ] Flakiness triggers: time dependency, random without seed, external service, shared state, order dependency, resource contention, async without sync
- [ ] Prevention: inject clock, seed random, mock external, isolate state, ensure independence, explicit sync
- [ ] `FlakinessReview` for every test: time_dependencies, random_usage, external_calls, shared_state, risk_level, mitigations_applied

**Implementation Guide:**
```go
type FlakyPrevention struct {
    triggers []FlakinessTrigger
}

func (p *FlakyPrevention) AnalyzeTest(test *Test) (*FlakinessReview, error)
func (p *FlakyPrevention) SuggestMitigation(review *FlakinessReview) []string
```

#### Tester Coverage Gap Prioritization Protocol
**Acceptance Criteria:**
- [ ] Priority order: P0=error paths, P1=boundaries, P2=integration, P3=happy paths, P4=convenience
- [ ] Error paths to test: network failures, invalid input, resource exhaustion, permission denied, timeouts, partial failures
- [ ] Coverage plan: priority, area, tests_planned for each level
- [ ] Anti-pattern detection: only happy paths = FALSE coverage

**Implementation Guide:**
```go
type CoveragePrioritizer struct {
    priorities []CoveragePriority
}

func (p *CoveragePrioritizer) PlanCoverage(implementation string) *CoveragePlan
func (p *CoveragePrioritizer) DetectAntiPatterns(plan *CoveragePlan) []AntiPattern
```

#### Tester Test Maintenance Burden Protocol
**Acceptance Criteria:**
- [ ] HIGH burden: tests implementation details, brittle, complex setup, tests private, many mocks
- [ ] LOW burden: tests behavior, isolated, simple setup, tests public, real deps when fast
- [ ] Burden assessment: burden_score, factors (tests_behavior, tests_implementation, mock_count, setup_complexity, change_sensitivity)
- [ ] HIGH burden requires justification: why value exceeds cost

**Implementation Guide:**
```go
type MaintenanceBurdenAnalyzer struct{}

func (a *MaintenanceBurdenAnalyzer) Assess(test *Test) *BurdenAssessment
func (a *MaintenanceBurdenAnalyzer) RequiresJustification(assessment *BurdenAssessment) bool
```

### Librarian: Additional Discipline Protocols

**Files to modify:**
- `agents/librarian/staleness.go` (new)
- `agents/librarian/confidence_calibration.go` (new)
- `agents/librarian/pattern_evolution.go` (new)
- `agents/librarian/index_health.go` (new)

#### Librarian Staleness Detection Discipline
**Acceptance Criteria:**
- [ ] Staleness thresholds: FRESH (<5min), RECENT (5-30min), AGING (30-60min), STALE (>60min)
- [ ] Check file modification times vs cache timestamps
- [ ] AGING triggers verification before responding
- [ ] STALE triggers full rescan
- [ ] Staleness signals: file mtime > cache, recent git commits, agent discrepancy
- [ ] Response annotation: staleness_check with status, cache_age, action_taken

**Implementation Guide:**
```go
type StalenessDetector struct {
    cacheManager *CacheManager
}

func (d *StalenessDetector) CheckStaleness(ctx context.Context, files []string) *StalenessResult
func (d *StalenessDetector) GetAction(result *StalenessResult) StalenessAction
```

#### Librarian Confidence Calibration Protocol
**Acceptance Criteria:**
- [ ] Track all confidence scores with outcomes (success/failure/corrected)
- [ ] Calibration bands: 0.9-1.0 (90%+ accurate), 0.7-0.9 (70-90%), 0.5-0.7 (50-70%)
- [ ] Adjustment: if 0.9 conf but <80% accurate, recalibrate down; if 0.7 but >90%, recalibrate up
- [ ] Track by query type (some harder than others)
- [ ] Include in response: "Confidence: 0.85 (historically 83% accurate for this query type)"

**Implementation Guide:**
```go
type ConfidenceCalibrator struct {
    archivalistClient ArchivalistClient
}

func (c *ConfidenceCalibrator) RecordOutcome(queryType string, confidence float64, success bool) error
func (c *ConfidenceCalibrator) GetCalibrationReport() *CalibrationReport
```

#### Librarian Pattern Evolution Tracking Protocol
**Acceptance Criteria:**
- [ ] Detect evolution: old pattern in N files, new pattern in M files
- [ ] Evolution states: ESTABLISHED (>90%), TRANSITIONAL (both >10%), EMERGING (<10% new), DEPRECATED (<10% old)
- [ ] TRANSITIONAL response: old pattern, new pattern, counts, recommendation
- [ ] Track migration_progress = M/(N+M)
- [ ] Warn consumers about transitional patterns

**Implementation Guide:**
```go
type PatternEvolutionTracker struct{}

func (t *PatternEvolutionTracker) DetectEvolution(ctx context.Context, pattern string) *EvolutionState
func (t *PatternEvolutionTracker) GetRecommendation(state *EvolutionState) string
```

#### Librarian Index Health Monitoring Protocol
**Acceptance Criteria:**
- [ ] Health metrics: FRESHNESS (time since update), COVERAGE (% indexed), LATENCY (query time), ACCURACY (feedback), SIZE (vs expected)
- [ ] Thresholds: FRESHNESS >24h=WARN >72h=CRITICAL, COVERAGE <80%=WARN <50%=CRITICAL, LATENCY >500ms=WARN >2s=CRITICAL
- [ ] Health states: HEALTHY, DEGRADED (1-2 out of range), UNHEALTHY (3+), CRITICAL
- [ ] Degraded actions: reindex, scan unindexed, alert for optimization, review feedback
- [ ] Periodic health check report

**Implementation Guide:**
```go
type IndexHealthMonitor struct {
    vectorDB VectorDBClient
}

func (m *IndexHealthMonitor) CheckHealth(ctx context.Context) *IndexHealthReport
func (m *IndexHealthMonitor) GetRecommendedAction(report *IndexHealthReport) HealthAction
```

### Archivalist: Additional Discipline Protocols

**Files to modify:**
- `agents/archivalist/knowledge_decay.go` (new)
- `agents/archivalist/cross_session_promotion.go` (new)
- `agents/archivalist/conflict_resolution.go` (new)
- `agents/archivalist/retrieval_feedback.go` (new)

#### Archivalist Knowledge Decay Protocol
**Acceptance Criteria:**
- [ ] Time decay weights: <1w=1.0, 1-4w=0.8, 1-3m=0.6, 3-6m=0.4, 6-12m=0.2, >12m=0.1
- [ ] Modifiers: HIGH_VALUE x1.5, REINFORCED x2.0, CONTRADICTED x0.5, DEPRECATED x0.1
- [ ] Query results ordered by: relevance * decay_weight * modifier
- [ ] Response annotation with decay info
- [ ] Old entries: >6m=AGING flag, >12m=VERIFY warning, >24m=archive to cold storage

**Implementation Guide:**
```go
type KnowledgeDecay struct{}

func (d *KnowledgeDecay) CalculateWeight(entry *Entry) float64
func (d *KnowledgeDecay) GetModifier(entry *Entry) float64
func (d *KnowledgeDecay) OrderResults(results []*Entry) []*Entry
```

#### Archivalist Cross-Session Promotion Protocol
**Acceptance Criteria:**
- [ ] Auto-promote when: 2+ session success, 3+ same-session success, marked best practice, recurring failure
- [ ] Promotion levels: SESSION_LOCAL, PROJECT_WIDE, GLOBAL, PINNED
- [ ] Promotion log: pattern_id, promoted_from, promoted_to, reason, success_count
- [ ] Demotion: if >30% failure rate post-promotion, demote one level with context note

**Implementation Guide:**
```go
type PromotionManager struct {
    archivalistClient ArchivalistClient
}

func (m *PromotionManager) ShouldPromote(entry *Entry) (bool, PromotionLevel, string)
func (m *PromotionManager) Promote(entry *Entry, level PromotionLevel) error
func (m *PromotionManager) CheckForDemotion(entry *Entry) (bool, string)
```

#### Archivalist Conflict Detection & Resolution Protocol
**Acceptance Criteria:**
- [ ] Conflict types: DIRECT_CONTRADICTION, RECOMMENDATION_CONFLICT, CONTEXT_OVERLAP, VERSION_CONFLICT
- [ ] Detection on store: similar signature (>0.8) + different recommendations
- [ ] Resolution strategies: NEWER_WINS, CONTEXT_SPLIT, MERGE, FLAG_FOR_REVIEW, KEEP_BOTH
- [ ] Conflict record: conflict_id, type, entry_a, entry_b, resolution, resolution_note
- [ ] Surface conflict note when returning conflicting entries

**Implementation Guide:**
```go
type ConflictResolver struct{}

func (r *ConflictResolver) DetectConflict(newEntry *Entry, existing []*Entry) *Conflict
func (r *ConflictResolver) Resolve(conflict *Conflict) (*Resolution, error)
```

#### Archivalist Retrieval Quality Feedback Loop Protocol
**Acceptance Criteria:**
- [ ] Positive signals: used successfully, warning heeded, resolution worked
- [ ] Negative signals: "not relevant", pattern failed, resolution didn't work, follow-up needed
- [ ] Actions: POSITIVE=boost weight, NOT_RELEVANT=add negative example, DIDNT_WORK=reduce weight, INCOMPLETE=add cross-refs
- [ ] Quality metrics: total_retrievals, positive_feedback, negative_feedback, quality_score, trend
- [ ] Self-improvement loop: collect → analyze → adjust → measure → repeat

**Implementation Guide:**
```go
type RetrievalFeedbackLoop struct {
    archivalistClient ArchivalistClient
}

func (l *RetrievalFeedbackLoop) RecordFeedback(entryID string, feedback FeedbackType) error
func (l *RetrievalFeedbackLoop) GetQualityMetrics() *QualityMetrics
func (l *RetrievalFeedbackLoop) TriggerImprovement() error
```

### Orchestrator: Additional Discipline Protocols

**Files to modify:**
- `core/orchestrator/anomaly_detection.go` (new)
- `core/orchestrator/pipeline_health.go` (new)
- `core/orchestrator/completion_verification.go` (new)
- `core/orchestrator/graceful_degradation.go` (new)

#### Orchestrator Execution Anomaly Detection Protocol
**Acceptance Criteria:**
- [ ] Anomaly types: TIME_ANOMALY (>3x expected), ERROR_SPIKE (>50%), TOOL_LOOP (>5x same), RESOURCE_EXHAUSTION, STALL_DETECTED (>2min), DEPENDENCY_TIMEOUT (>5min)
- [ ] Detection thresholds configurable
- [ ] Alert actions: WARN (log), ALERT (notify Architect), ESCALATE (pause), AUTO_CANCEL (timeout)
- [ ] Anomaly report: anomaly_id, type, task_id, severity, details, action_taken, architect_notified

**Implementation Guide:**
```go
type AnomalyDetector struct {
    thresholds AnomalyThresholds
}

func (d *AnomalyDetector) Monitor(ctx context.Context, task *Task) <-chan *Anomaly
func (d *AnomalyDetector) GetAction(anomaly *Anomaly) AnomalyAction
```

#### Orchestrator Pipeline Health Monitoring Protocol
**Acceptance Criteria:**
- [ ] Health metrics: THROUGHPUT (tasks/time), PROGRESS_RATE, ERROR_RATIO, RETRY_RATIO, BOTTLENECK, RESOURCE_UTILIZATION
- [ ] Health states: HEALTHY, DEGRADED (1-2 out), UNHEALTHY (3+), CRITICAL
- [ ] Check frequency: every task completion, every 30s, on anomaly
- [ ] Health report with metrics, status, bottleneck, recommendation
- [ ] Report to Architect when health degrades

**Implementation Guide:**
```go
type PipelineHealthMonitor struct{}

func (m *PipelineHealthMonitor) CheckHealth(pipeline *Pipeline) *PipelineHealthReport
func (m *PipelineHealthMonitor) GetRecommendation(report *PipelineHealthReport) string
```

#### Orchestrator Completion Verification Protocol
**Acceptance Criteria:**
- [ ] Verification checks: ARTIFACT_EXISTS, NO_ERROR_INDICATORS, DOWNSTREAM_READY, EVIDENCE_STORED, AGENT_CONFIRMED
- [ ] False completion indicators: "done" but no changes, build/test failing, files missing, errors in output
- [ ] Workflow: agent signals → run checks → VERIFIED or FALSE_COMPLETION
- [ ] FALSE_COMPLETION alerts Architect immediately
- [ ] Verification report: task_id, signaled_complete, verification_result, checks, failure_reason

**Implementation Guide:**
```go
type CompletionVerifier struct{}

func (v *CompletionVerifier) Verify(ctx context.Context, task *Task) *VerificationResult
func (v *CompletionVerifier) IsFalseCompletion(result *VerificationResult) bool
```

#### Orchestrator Graceful Degradation Protocol
**Acceptance Criteria:**
- [ ] Triggers: TASK_FAILURE, BRANCH_FAILURE, AGENT_UNAVAILABLE, RESOURCE_EXHAUSTED, TIMEOUT
- [ ] Actions: ISOLATE (mark failed), CONTINUE (independent branches), PARTIAL_COMPLETE, INFORM
- [ ] Isolation rules: failed → cancel dependents, independent → continue, downstream → BLOCKED
- [ ] Partial completion report: workflow_id, status=PARTIAL, completed_branches, failed_branches, blocked_tasks, salvageable_work, artifacts_produced
- [ ] User communication: clear partial results, offer to proceed

**Implementation Guide:**
```go
type GracefulDegradation struct{}

func (g *GracefulDegradation) OnFailure(ctx context.Context, task *Task) *DegradationPlan
func (g *GracefulDegradation) IsolateBranch(branch *Branch) error
func (g *GracefulDegradation) GeneratePartialReport(workflow *Workflow) *PartialCompletionReport
```

---

## Enhanced Agent Skills (Inspired by oh-my-opencode & opencode)

Skills and tooling inspired by tool exploration of oh-my-opencode and anomalyco/opencode repositories.

### Librarian: Enhanced Search & AST Skills

**Files to modify:**
- `agents/librarian/skills.go`
- `agents/librarian/lsp.go` (new)
- `agents/librarian/ast_search.go` (new)

**Acceptance Criteria:**

#### AST-Based Search
- [ ] `ast_grep_search` skill for structural code pattern matching
- [ ] Support for Go, TypeScript, Python, Rust, Java languages
- [ ] AST pattern syntax documentation
- [ ] Integration with existing search for fallback

#### LSP Integration
- [ ] `lsp_go_to_definition` skill via LSP protocol
- [ ] `lsp_find_references` skill for all symbol usages
- [ ] `lsp_hover` skill for type/documentation info
- [ ] `lsp_symbols` skill for file/workspace symbols
- [ ] `lsp_call_hierarchy` skill for incoming/outgoing calls
- [ ] LSP server lifecycle management
- [ ] Fallback to AST/regex when LSP unavailable

#### Semantic Code Search
- [ ] `codesearch` skill for natural language code queries
- [ ] Integration with codebase index
- [ ] Search type selection logic (when to use which tool)

### Academic: Web Research & Documentation Skills

**Files to modify:**
- `agents/academic/skills.go`
- `agents/academic/web.go` (new)

**Acceptance Criteria:**

#### Web Research
- [ ] `web_search` skill for internet research
- [ ] Domain filtering support
- [ ] Rate limiting and caching
- [ ] Result ranking and deduplication

#### Documentation Fetching
- [ ] `web_fetch` skill for URL content extraction
- [ ] `fetch_documentation` skill for package docs
- [ ] Support for Go, npm, PyPI, crates.io
- [ ] Version-specific documentation lookup

#### GitHub Integration
- [ ] `fetch_github_context` skill for repo info
- [ ] README, structure, issues, releases extraction
- [ ] Rate limiting for GitHub API

#### Package Search
- [ ] `search_packages` skill for registry search
- [ ] Multi-registry support (npm, go, pypi, crates)
- [ ] Sort by relevance, downloads, recency

### Orchestrator: Batch & Parallel Execution Skills

**Files to modify:**
- `core/orchestrator/executor.go`
- `core/orchestrator/batch.go` (new)
- `core/orchestrator/background.go` (new)

**Acceptance Criteria:**

#### Batch Dispatch
- [ ] `batch_dispatch` for parallel independent tasks
- [ ] Respect max_concurrency limits
- [ ] fail_fast option for early termination
- [ ] Result aggregation

#### Background Tasks
- [ ] `background_task` for long-running operations
- [ ] Progress signal emission at intervals
- [ ] `task_monitor` for status checking
- [ ] `kill_task` for graceful termination
- [ ] Timeout enforcement

#### Parallel Validation
- [ ] `parallel_validate` for concurrent validations
- [ ] Build, lint, type, test validation types
- [ ] Result aggregation before Architect reporting

### Engineer: Multi-Edit & Structural Refactoring Skills

**Files to modify:**
- `agents/engineer/skills.go`
- `agents/engineer/multiedit.go` (new)
- `agents/engineer/refactor.go` (new)

**Acceptance Criteria:**

#### Multi-Edit Operations
- [ ] `multi_edit` for atomic batch edits in single file
- [ ] Dry-run support for preview
- [ ] Rollback on any edit failure
- [ ] Edit conflict detection

#### Patch Operations
- [ ] `patch_file` for unified diff application
- [ ] Reverse patch support
- [ ] Context validation

#### AST-Based Refactoring
- [ ] `ast_grep_replace` for structural find-replace
- [ ] Cross-file rename support
- [ ] Dry-run with preview
- [ ] Language-specific patterns

#### Batch File Operations
- [ ] `batch_write` for atomic multi-file creation
- [ ] Directory creation support
- [ ] Rollback on failure

#### Background Execution
- [ ] `run_background` for long-running commands
- [ ] Timeout and monitoring support

### Designer: Vision & Multimodal Skills

**Files to modify:**
- `agents/designer/skills.go`
- `agents/designer/vision.go` (new)

**Acceptance Criteria:**

#### Image Analysis
- [ ] `look_at` skill for screenshot/image analysis
- [ ] Multiple analysis modes (describe, compare, accessibility, spacing, consistency)
- [ ] Integration with multimodal model (Gemini 3 Pro)

#### Visual Comparison
- [ ] `compare_screenshots` for visual regression detection
- [ ] Configurable diff threshold
- [ ] Diff highlighting output

#### Design Mockup Analysis
- [ ] `analyze_design_mockup` for implementation planning
- [ ] Extract components, spacing, colors, typography
- [ ] Framework-specific output (React, Vue, Svelte, CSS)

#### Visual Accessibility
- [ ] `visual_accessibility_check` for visual a11y validation
- [ ] Contrast ratio checking
- [ ] Touch target size validation
- [ ] Font size validation

### Inspector: LSP & AST Validation Skills

**Files to modify:**
- `agents/inspector/skills.go`
- `agents/inspector/lsp.go` (new)
- `agents/inspector/security.go` (new)

**Acceptance Criteria:**

#### LSP Diagnostics
- [ ] `lsp_diagnostics` for file diagnostics
- [ ] Severity filtering (error, warning, hint)
- [ ] Per-file caching until file changes

#### AST-Based Linting
- [ ] `ast_lint` for custom pattern detection
- [ ] Code smell patterns (long functions, deep nesting)
- [ ] Anti-pattern detection (error swallowing)
- [ ] Language-specific rules

#### Security Scanning
- [ ] `security_scan` for vulnerability detection
- [ ] Dependency CVE checking
- [ ] Code vulnerability detection
- [ ] Secret detection
- [ ] Configurable severity threshold

#### Complexity Analysis
- [ ] `complexity_analysis` for code metrics
- [ ] Cyclomatic complexity per function
- [ ] Cognitive complexity
- [ ] Configurable thresholds

#### Type Coverage
- [ ] `type_coverage` for type safety checking
- [ ] any/unknown usage detection
- [ ] Strict mode option

### Librarian: Tool Discovery System

**CRITICAL: Librarian owns ALL tool discovery. Inspector and Tester consult Librarian before execution.**

**Inspired by OpenCode's formatter/linter/test selection architecture, with distributed responsibility.**

**Files to create:**
- `core/detect/which.go` (new) - Binary detection
- `core/detect/files.go` (new) - File/config detection
- `core/detect/dependencies.go` (new) - Dependency detection
- `core/format/types.go` (new) - Formatter type definitions
- `core/format/formatters.go` (new) - Formatter definitions
- `core/format/selector.go` (new) - Formatter selection logic
- `core/lsp/types.go` (new) - LSP type definitions
- `core/lsp/servers.go` (new) - LSP server definitions
- `core/lsp/selector.go` (new) - LSP selection logic
- `core/test/types.go` (new) - Test framework type definitions
- `core/test/frameworks.go` (new) - Test framework definitions
- `core/test/selector.go` (new) - Test framework selection logic
- `agents/librarian/tool_discovery.go` (new) - Librarian tool discovery skills

**Parallelization Strategy:**
```
Phase 1 (FIRST - shared utilities, no dependencies):
├── 1A: Detection Utilities (core/detect/*)              ─┐
└── 1B: All Type Definitions (core/*/types.go)           ─┘ PARALLEL

Phase 2 (AFTER Phase 1 - can run in parallel):
├── 2A: Formatter Definitions (core/format/formatters.go)  ─┐
├── 2B: Formatter Selector (core/format/selector.go)        │
├── 2C: LSP Server Definitions (core/lsp/servers.go)        │ PARALLEL
├── 2D: LSP Selector (core/lsp/selector.go)                 │
├── 2E: Test Framework Definitions (core/test/frameworks.go)│
└── 2F: Test Framework Selector (core/test/selector.go)    ─┘

Phase 3 (AFTER Phase 2 - Librarian integration):
└── Librarian Tool Discovery Skills (agents/librarian/tool_discovery.go)

Phase 4 (AFTER Phase 3):
└── Integration Testing (Librarian detection accuracy)
```

**Acceptance Criteria:**

#### Phase 1A: Detection Utilities (Shared)

**File: `core/detect/which.go`**
- [x] `Which(binary string) string` - Find binary in PATH
- [x] Cross-platform support (Windows/Unix)
- [x] Caching of PATH lookups (invalidate on PATH change)

**File: `core/detect/files.go`**
- [x] `FileExists(root string, files ...string) bool` - Check if any file exists
- [x] `FindUp(startDir string, filename string) (string, error)` - Search upward for file
- [x] `FindUpAny(startDir string, filenames ...string) (string, string, error)` - Search upward for any file

**File: `core/detect/dependencies.go`**
- [x] `HasDependency(root string, pkg string) (bool, error)` - Check package.json
- [x] `HasGemDependency(root string, gem string) (bool, error)` - Check Gemfile
- [x] `HasPythonDependency(root string, pkg string) (bool, error)` - Check requirements.txt/pyproject.toml
- [x] `HasCargoDependency(root string, crate string) (bool, error)` - Check Cargo.toml
- [x] `HasGoDependency(root string, pkg string) (bool, error)` - Check go.mod

#### Phase 1B: Type Definitions (Shared)

**File: `core/format/types.go`**
- [x] `FormatterID` type definition
- [x] `FormatterDefinition` struct with ID, Name, Command, Extensions, Enabled func
- [x] `FormatterRegistry` struct with thread-safe map
- [x] `FormatterResult` struct with FormatterID, FilePath, Success, Changed, Error, Duration
- [x] `FormatterConfig` struct for user overrides (disabled, command, extensions)

**File: `core/lsp/types.go`**
- [x] `ServerID` type definition
- [x] `LanguageServerDefinition` struct with ID, Name, Command, Extensions, LanguageIDs, RootMarkers, Enabled func, AutoDownload
- [x] `AutoDownloadConfig` struct with Source, Package, Binary
- [x] `LSPClient` struct with ID, ServerID, ProjectRoot, Process, Conn, Capabilities
- [x] `DiagnosticResult` struct with ServerID, FilePath, Diagnostics
- [x] `LSPConfig` struct for user overrides (disabled, command, auto_download)

**File: `core/test/types.go`**
- [x] `TestFrameworkID` type definition
- [x] `TestFrameworkDefinition` struct (see Tester section for full definition)
- [x] `TestFrameworkRegistry` struct with thread-safe map
- [x] `TestResult` struct with passed, failed, skipped, duration, failures, coverage
- [x] `TestFailure` struct with TestName, File, Line, Message, Expected, Actual, StackTrace
- [x] `TestConfig` struct for user overrides

#### Phase 2A-2B: Formatter Detection (Librarian)

**File: `core/format/formatters.go`**
- [ ] `BuiltinFormatters` slice with all formatter definitions
- [ ] Go: gofmt, goimports (priority: goimports > gofmt)
- [ ] JS/TS: prettier, biome (priority: biome if configured > prettier)
- [ ] Python: ruff-format, black (priority: ruff > black)
- [ ] Rust: rustfmt
- [ ] C/C++: clang-format (requires config)
- [ ] Shell: shfmt
- [ ] Ruby: rubocop
- [ ] Terraform: terraform fmt
- [ ] Each formatter has correct Enabled() logic

**File: `core/format/selector.go`**
- [ ] `NewFormatterRegistry() *FormatterRegistry`
- [ ] `Register(def *FormatterDefinition) error`
- [ ] `SelectFormatter(ctx *ProjectContext, filePath string) (*FormatterDefinition, error)`
- [ ] `SelectFormatters(ctx *ProjectContext, filePath string) ([]*FormatterDefinition, error)`
- [ ] Extension-based filtering, Enabled() check, priority ordering
- [ ] User config overrides applied
- [ ] **Confidence scoring** for detection results

#### Phase 2C-2D: Linter/LSP Detection (Librarian)

**File: `core/lsp/servers.go`**
- [ ] `BuiltinServers` slice with all LSP server definitions
- [ ] Go: gopls with auto-download
- [ ] JS/TS: typescript-language-server, eslint, biome
- [ ] Python: pyright, ruff-lsp
- [ ] Rust: rust-analyzer
- [ ] C/C++: clangd
- [ ] Ruby: solargraph
- [ ] Java: jdtls
- [ ] YAML: yaml-language-server
- [ ] Terraform: terraform-ls
- [ ] Each server has correct RootMarkers, Enabled()

**File: `core/lsp/selector.go`**
- [ ] `NewLSPManager() *LSPManager`
- [ ] `RegisterServer(def *LanguageServerDefinition) error`
- [ ] `SelectServers(filePath string) ([]*LanguageServerDefinition, error)`
- [ ] `findProjectRoot(startDir string, markers []string) (string, error)`
- [ ] Extension-based filtering, Enabled() check
- [ ] **Confidence scoring** for detection results

#### Phase 2E-2F: Test Framework Detection (Librarian)

**File: `core/test/frameworks.go`**
- [ ] `BuiltinFrameworks` slice with all test framework definitions
- [ ] Go: go-test
- [ ] JS/TS: jest, vitest, mocha, bun-test, node-test (priority: vitest > jest > mocha)
- [ ] Python: pytest, unittest (priority: pytest > unittest)
- [ ] Rust: cargo-test
- [ ] Ruby: rspec, minitest
- [ ] Java: maven-test, gradle-test
- [ ] Elixir: mix-test
- [ ] PHP: phpunit
- [ ] C++: ctest
- [ ] Each framework has correct Enabled() logic

**File: `core/test/selector.go`**
- [ ] `NewTestFrameworkRegistry() *TestFrameworkRegistry`
- [ ] `Register(def *TestFrameworkDefinition) error`
- [ ] `SelectFramework(ctx *ProjectContext) (*TestFrameworkDefinition, error)`
- [ ] `SelectFrameworkForFile(ctx *ProjectContext, filePath string) (*TestFrameworkDefinition, error)`
- [ ] `SelectFrameworksByLanguage(ctx *ProjectContext, language string) ([]*TestFrameworkDefinition, error)`
- [ ] **Confidence scoring** for detection results

#### Phase 3: Librarian Tool Discovery Skills

**File: `agents/librarian/tool_discovery.go`**
- [ ] `detect_formatter` skill - Returns FormatterDefinition with confidence + reason
- [ ] `list_formatters` skill - Returns all enabled formatters
- [ ] `detect_linters` skill - Returns LSPServerDefinitions with confidence + reason
- [ ] `list_lsp_servers` skill - Returns all enabled LSP servers
- [ ] `detect_test_framework` skill - Returns TestFrameworkDefinition with confidence + reason
- [ ] `list_test_frameworks` skill - Returns all enabled test frameworks
- [ ] `get_project_tools` skill - Returns all tools for project
- [ ] **Caching layer** with config file change invalidation
- [ ] Integration with Librarian's existing context system

#### Phase 4: Testing (Librarian Detection)

**Unit Tests:**
- [ ] Detection utilities (which, fileExists, findUp, hasDependency)
- [ ] Formatter selection logic with mocked filesystem
- [ ] LSP server selection logic with mocked filesystem
- [ ] Test framework selection logic with mocked filesystem
- [ ] Confidence scoring accuracy
- [ ] Cache invalidation behavior

**Integration Tests:**
- [ ] End-to-end formatter detection for each language
- [ ] End-to-end LSP server detection for each language
- [ ] End-to-end test framework detection for each language
- [ ] Multi-tool conflict resolution
- [ ] Config override application
- [ ] Librarian skill responses include correct confidence/reason

---

## Agent Core Skill Gaps (Foundational)

These are foundational skills that agents need but were missing from their skill definitions.
All specifications added to ARCHITECTURE.md - implementation required.

### 6.150 Architect: File & Search Operations

**Files to modify:**
- `agents/architect/skills.go`

**Skills to implement:**
- [ ] `read_file` - Read file contents for planning context
- [ ] `glob` - Find files matching glob patterns
- [ ] `grep` - Search file contents for patterns

**Acceptance Criteria:**
- [ ] Architect can read files before creating plans
- [ ] Architect can find files by pattern to understand scope
- [ ] Architect can search content to identify affected areas
- [ ] All operations are read-only (no file modification)

---

### 6.151 Designer: File Operations & Search

**Files to modify:**
- `agents/designer/skills.go`

**Skills to implement:**
- [ ] `read_file` - Read existing component/style files
- [ ] `write_file` - Write component/style files to disk
- [ ] `edit_file` - Modify existing component/style files
- [ ] `glob` - Find component/style files by pattern
- [ ] `grep` - Search file contents for usage patterns

**Acceptance Criteria:**
- [ ] Designer can read existing components before extending
- [ ] Designer can write new component files directly
- [ ] Designer can modify existing component files
- [ ] Designer can find components by file pattern
- [ ] Designer can search for component usage across codebase

---

### 6.152 Inspector: File Operations, Search & Command Execution

**Files to modify:**
- `agents/inspector/skills.go`

**Skills to implement:**
- [ ] `read_file` - Read files for validation
- [ ] `glob` - Find files matching patterns for validation
- [ ] `grep` - Search for patterns/anti-patterns in code
- [ ] `run_command` - Execute linters, formatters, security scanners
- [ ] `auto_fix` - Auto-fix detected issues (formatting, imports, lint)
- [ ] `format_file` - Format file using detected formatter
- [ ] `organize_imports` - Sort and organize imports

**Acceptance Criteria:**
- [ ] Inspector can read files to validate them
- [ ] Inspector can find files to validate by pattern
- [ ] Inspector can search for anti-patterns across codebase
- [ ] Inspector can execute validation tools (linters, formatters, security scanners)
- [ ] Inspector can auto-fix fixable issues with dry_run preview
- [ ] Inspector can format individual files
- [ ] Inspector can organize imports in source files

---

### 6.153 Tester: File Operations, Search & Command Execution

**Files to modify:**
- `agents/tester/skills.go`

**Skills to implement:**
- [ ] `read_file` - Read implementation code for test writing
- [ ] `write_file` - Write test files to disk
- [ ] `edit_file` - Modify existing test files
- [ ] `glob` - Find test/implementation files by pattern
- [ ] `grep` - Search for test patterns, fixtures, mocks
- [ ] `run_command` - Execute test commands, coverage tools
- [ ] `coverage_report` - Generate detailed coverage reports
- [ ] `mutation_test` - Run mutation testing for test quality
- [ ] `detect_flaky_tests` - Run tests multiple times to detect flakes

**Acceptance Criteria:**
- [ ] Tester can read implementation code to write tests against
- [ ] Tester can write new test files directly
- [ ] Tester can modify existing test files
- [ ] Tester can find test/implementation files by pattern
- [ ] Tester can search for test patterns and fixtures
- [ ] Tester can execute arbitrary test commands
- [ ] Tester can generate coverage reports in multiple formats
- [ ] Tester can run mutation testing to verify test quality
- [ ] Tester can detect flaky tests through repeated execution

---

### 6.154 Librarian: File Read & Git History

**Files to modify:**
- `agents/librarian/skills.go`

**Skills to implement:**
- [ ] `read_file` - Read full file contents for context
- [ ] `git_log` - View commit history for code evolution
- [ ] `git_blame` - Show line-by-line authorship
- [ ] `git_show` - Show commit details
- [ ] `git_diff` - Show differences between commits
- [ ] `git_branch_list` - List branches with info

**Acceptance Criteria:**
- [ ] Librarian can read complete file contents (not just search results)
- [ ] Librarian can show commit history for any file/path
- [ ] Librarian can show who wrote each line and when
- [ ] Librarian can show details of specific commits
- [ ] Librarian can compare changes between commits
- [ ] Librarian can list all branches with their status

---

### 6.155 Engineer: Search Operations & Git WIP Management

**Files to modify:**
- `agents/engineer/skills.go`

**Skills to implement:**
- [ ] `glob` - Find files matching glob patterns
- [ ] `grep` - Search file contents for patterns
- [ ] `git_stash` - Stash/restore work-in-progress changes

**Acceptance Criteria:**
- [ ] Engineer can find files by pattern (not just exact paths)
- [ ] Engineer can search file contents for patterns
- [ ] Engineer can stash WIP when context-switching
- [ ] Engineer can restore stashed changes
- [ ] git_stash supports push/pop/list/show/drop actions
- [ ] git_stash can include untracked files

---

### 6.156 Engineer: Debugging Capabilities

**Files to modify:**
- `agents/engineer/skills.go`
- `agents/engineer/debug.go` (new)

**Skills to implement:**
- [ ] `debug_print` - Add temporary debug statements
- [ ] `debug_breakpoint` - Set breakpoints for interactive debugging
- [ ] `debug_run` - Run program with debugger attached
- [ ] `debug_inspect` - Inspect variable/expression values
- [ ] `debug_stack` - Show call stack
- [ ] `debug_cleanup` - Remove all added debug statements

**Acceptance Criteria:**
- [ ] Engineer can add debug print statements with labels
- [ ] Engineer can set conditional breakpoints
- [ ] Engineer can run programs with debugger (delve/node-inspect/pdb)
- [ ] Engineer can inspect variable values at runtime
- [ ] Engineer can view call stack during debugging
- [ ] Engineer can clean up all debug statements when done
- [ ] All debug capabilities work for Go, TypeScript, Python

---

## Inter-Agent Communication Gaps ✓ COMPLETE

These address missing communication skills that prevent proper agent coordination.
All specifications added to ARCHITECTURE.md - **implementation complete**.

**Completed Items (6.160-6.165):**
- 6.160: Inspector Academic Consultation ✓
- 6.161: Tester Academic Consultation ✓
- 6.162: Orchestrator Architect Signaling ✓
- 6.163: Engineer Pipeline Context & Artifact Discovery ✓
- 6.164: Architect Pre-Delegation Validation Hook ✓
- 6.165: DAG Executor Signaling Layer ✓

### 6.160 Inspector: Academic Consultation ✓ COMPLETE

**Files to modify:**
- `agents/inspector/skills.go`

**Skills to implement:**
- [x] `consult_academic` - Directly consult Academic for validation best practices

**Acceptance Criteria:**
- [x] Inspector can query Academic for security standards
- [x] Inspector can query Academic for code quality research
- [x] Inspector can query Academic for validation methodology best practices
- [x] Direct consultation (bypasses Guide) with intent parameter

---

### 6.161 Tester: Academic Consultation ✓ COMPLETE

**Files to modify:**
- `agents/tester/skills.go`

**Skills to implement:**
- [x] `consult_academic` - Directly consult Academic for testing best practices

**Acceptance Criteria:**
- [x] Tester can query Academic for testing methodology research
- [x] Tester can query Academic for coverage strategy best practices
- [x] Tester can query Academic for test design patterns
- [x] Direct consultation (bypasses Guide) with intent parameter

---

### 6.162 Orchestrator: Architect Signaling ✓ COMPLETE

**Files to modify:**
- `core/orchestrator/skills.go`
- `core/orchestrator/signals.go` (new)

**Skills to implement:**
- [x] `signal_task_complete` - Signal task completion to Architect
- [x] `signal_pipeline_complete` - Signal pipeline completion to Architect
- [x] `consult_architect` - Direct consultation for escalations

**Acceptance Criteria:**
- [x] Orchestrator signals Architect when tasks complete (not just failures)
- [x] Orchestrator signals Architect when pipelines complete with summary
- [x] Orchestrator can escalate decisions to Architect directly
- [x] Architect receives structured signals for decision-making
- [x] Signals include artifacts, follow-up suggestions, and next steps

---

### 6.163 Engineer: Pipeline Context & Artifact Discovery ✓ COMPLETE

**Files to modify:**
- `agents/engineer/skills.go`
- `agents/engineer/pipeline.go` (new)

**Skills to implement:**
- [x] `read_pipeline_context` - Read artifacts from predecessor tasks
- [x] `discover_artifacts` - Discover all artifacts in current workflow

**Acceptance Criteria:**
- [x] Engineer can read Designer artifacts (components, types, hooks)
- [x] Engineer can discover what other agents produced in workflow
- [x] Engineer avoids duplicate work by checking existing artifacts
- [x] Artifact discovery supports filtering by agent and type

---

### 6.164 Architect: Pre-Delegation Validation Hook ✓ COMPLETE

**Files to modify:**
- `agents/architect/delegation.go` (new)
- `agents/architect/hooks.go`

**Types to implement:**
- [x] `PreDelegationHook` - Hook that validates before dispatch
- [x] `PreDelegationDeclaration` - Formal declaration with evidence
- [x] `ConsultationEvidence` - Proof of agent consultation
- [x] `ValidatePreDelegation` - Validation function

**Acceptance Criteria:**
- [x] Dispatch blocked if Librarian not consulted (or stale >5 min)
- [x] Dispatch blocked if Archivalist not consulted (or stale >5 min)
- [x] Dispatch blocked if codebase health not assessed
- [x] Dispatch blocked if similar approach failed ≥2 times before
- [x] All consultations logged with query ID and timestamp
- [x] Validation errors are clear and actionable

---

### 6.165 DAG Executor: Signaling Layer ✓ COMPLETE

**Files to create:**
- `core/dag/signals.go` (new)
- `core/dag/callbacks.go` (new)
- `core/dag/signal_bus.go` (new)

**Types to implement:**
- [x] `DAGExecutorCallbacks` - Hooks for signaling without LLM
- [x] `TaskCompleteSignal` - Signal when task succeeds
- [x] `TaskFailedSignal` - Signal when task fails
- [x] `PipelineCompleteSignal` - Signal when pipeline finishes
- [x] `LayerCompleteSignal` - Signal when DAG layer finishes
- [x] `WorkflowCompleteSignal` - Signal when workflow finishes
- [x] `DAGExecutorSignalBus` - Routes signals to interested parties
- [x] `SignalRouting` - Configuration for signal routing

**Acceptance Criteria:**
- [x] DAG Executor signals Architect DIRECTLY (no LLM intermediation)
- [x] Task completion signals include artifacts and next tasks
- [x] Task failure signals include error details and suggested action
- [x] Pipeline signals include quality gate status and next pipeline
- [x] Workflow signals include user-friendly summary
- [x] Archivalist receives all signals asynchronously (fire-and-forget)
- [x] User notified of failures and completions immediately
- [x] Signal routing is configurable per signal type

---

### Inspector: Formatter & Linter Execution System

**CRITICAL: Inspector EXECUTES formatting/linting. Detection is Librarian's responsibility.**

**Files to create:**
- `core/format/executor.go` (new) - Formatter execution
- `core/lsp/client.go` (new) - LSP client management
- `core/lsp/download.go` (new) - LSP auto-download
- `agents/inspector/format.go` (new) - Format execution skills
- `agents/inspector/lint.go` (new) - Lint execution skills

**Parallelization Strategy:**
```
Phase 1 (AFTER Librarian Tool Discovery Phase 2):
├── 1A: Formatter Executor (core/format/executor.go)  ─┐
├── 1B: LSP Client (core/lsp/client.go)               │ PARALLEL
└── 1C: LSP Auto-Download (core/lsp/download.go)     ─┘

Phase 2 (AFTER Phase 1 - Inspector integration):
├── Inspector Format Skills (agents/inspector/format.go)  ─┐
└── Inspector Lint Skills (agents/inspector/lint.go)      ─┘ PARALLEL

Phase 3 (AFTER Phase 2):
└── Integration Testing (Inspector ↔ Librarian consultation)
```

**Acceptance Criteria:**

#### Phase 1A: Formatter Executor

**File: `core/format/executor.go`**
- [ ] `Execute(def *FormatterDefinition, filePath string, dryRun bool) (*FormatterResult, error)`
- [ ] Command building with $FILE placeholder replacement
- [ ] Working directory set to project root
- [ ] stdout/stderr capture
- [ ] Exit code handling
- [ ] File modification time check for "changed" detection
- [ ] Timeout support
- [ ] `ExecuteParallel(files []string, maxWorkers int) ([]*FormatterResult, error)`
- [ ] Error aggregation

#### Phase 1B: LSP Client

**File: `core/lsp/client.go`**
- [ ] `SpawnClient(def *LanguageServerDefinition, projectRoot string) (*LSPClient, error)`
- [ ] Process management (start, stdin/stdout pipes)
- [ ] JSON-RPC 2.0 connection setup
- [ ] `initialize` request with capabilities
- [ ] `initialized` notification
- [ ] `textDocument/didOpen` for file registration
- [ ] `textDocument/publishDiagnostics` handler
- [ ] `GetDiagnostics(filePath string) ([]protocol.Diagnostic, error)`
- [ ] `Shutdown()` for graceful termination
- [ ] Connection error handling and recovery
- [ ] Client caching by serverID:projectRoot key

#### Phase 1C: LSP Auto-Download

**File: `core/lsp/download.go`**
- [ ] `AutoDownload(config *AutoDownloadConfig) error`
- [ ] `downloadFromNPM(pkg string) error` - npm install -g
- [ ] `downloadFromGo(pkg string) error` - go install
- [ ] `downloadFromGitHub(repo string, binary string) error` - Download release binary
- [ ] `SYLK_DISABLE_LSP_DOWNLOAD` flag support
- [ ] Download progress reporting
- [ ] Error handling and cleanup on failure
- [ ] Binary verification after download

#### Phase 2: Inspector Execution Skills (Consult Librarian First)

**File: `agents/inspector/format.go`**
- [ ] `format_file` skill implementation
  - [ ] Accept optional `formatter` param (FormatterDefinition from Librarian)
  - [ ] If not provided, consult Librarian via Guide: `detect_formatter`
  - [ ] Cache Librarian response per file extension
  - [ ] Execute formatter with provided definition
  - [ ] Report result to Archivalist
- [ ] `format_files` skill with parallel execution
  - [ ] Batch consult Librarian for unique extensions
  - [ ] Execute formatters in parallel per file
- [ ] **Librarian consultation protocol** implementation
  - [ ] Cache invalidation on Librarian signal
  - [ ] Fallback error handling if Librarian unavailable

**File: `agents/inspector/lint.go`**
- [ ] `lint_file` skill implementation
  - [ ] Accept optional `lsp_servers` param (LSPServerDefinitions from Librarian)
  - [ ] If not provided, consult Librarian via Guide: `detect_linters`
  - [ ] Cache Librarian response per file extension
  - [ ] Execute LSP diagnostics with provided definitions
  - [ ] Report result to Archivalist
- [ ] `lint_files` skill with parallel execution
- [ ] `get_diagnostics` skill - retrieve cached diagnostics
- [ ] Diagnostic merging from multiple servers
- [ ] Deduplication by range+message
- [ ] **Librarian consultation protocol** implementation

#### Phase 3: Testing (Inspector Execution + Consultation)

**Unit Tests:**
- [ ] Formatter executor command building
- [ ] Formatter executor parallel execution
- [ ] LSP client communication (mocked)
- [ ] LSP auto-download logic (mocked)
- [ ] Inspector → Librarian consultation flow (mocked)
- [ ] Cache behavior and invalidation

**Integration Tests:**
- [ ] End-to-end: Librarian detects formatter → Inspector executes
- [ ] End-to-end: Librarian detects LSP → Inspector retrieves diagnostics
- [ ] Multi-formatter execution with Librarian detection
- [ ] Multi-LSP server results merging
- [ ] Auto-download flow triggered by Librarian response
- [ ] Consultation failure handling (Librarian unavailable)

**Performance Tests:**
- [ ] Large file formatting
- [ ] Parallel formatting of 100+ files
- [ ] LSP client startup time
- [ ] Diagnostic retrieval latency

### Architect: Plan Mode & Todo Management Skills

**Files to modify:**
- `agents/architect/skills.go`
- `agents/architect/plan_mode.go` (new)
- `agents/architect/todo.go` (new)

**Acceptance Criteria:**

#### Plan Mode
- [ ] `enter_plan_mode` for complex task planning
- [ ] `exit_plan_mode` with approval request
- [ ] `update_plan_file` for plan management
- [ ] Plan file format standardization
- [ ] User approval workflow

#### Todo Management
- [ ] `todo_write` for task list management
- [ ] `todo_mark_complete` for status updates
- [ ] Status tracking (pending, in_progress, completed)
- [ ] User visibility into progress

#### User Interaction
- [ ] `ask_user_question` for clarification requests
- [ ] Multi-option question support
- [ ] Integration with plan mode workflow

---

## FILESYSTEM Versioning System (MVCC + OT + AST-Aware VFS)

**Reference**: See `FILESYSTEM.md` for detailed architecture specification and `FILESYSTEM_TASKS.md` for complete task breakdown.

This system provides robust file versioning for multi-agent concurrent editing with:
- **Multi-Version Concurrency Control (MVCC)**: Version DAG with content-addressable storage
- **Operational Transformation (OT)**: Automatic conflict resolution for concurrent edits
- **AST-Aware Targeting**: Stable node paths instead of brittle line numbers
- **Dual VFS**: Pre-change snapshot + working VFS for isolation and instant rollback
- **Cross-Session Coordination**: File change signals and lock coordination

### FILESYSTEM Phase 1: Foundation Data Structures

**Files to create:**
- `core/versioning/version_id.go`
- `core/versioning/vector_clock.go`
- `core/versioning/operation.go`
- `core/versioning/target.go`
- `core/versioning/file_version.go`

**Parallelization Strategy:**
```
All items in Phase 1 can execute in parallel - no interdependencies.
```

**Acceptance Criteria:**

#### FS.1.1 VersionID and Content-Addressable Hashing ✅
- [x] `VersionID` type as `[32]byte` SHA-256 hash
- [x] `ComputeVersionID(content, metadata []byte) VersionID` - deterministic hash
- [x] `ComputeContentHash` for content-only hashing
- [x] `String()` returns 64-char lowercase hex
- [x] `Short()` returns first 8 chars for display
- [x] `ParseVersionID(s string)` round-trips correctly
- [x] `IsZero()` and `Equal()` support
- [x] Tests pass with race detector

#### FS.1.2 Vector Clock for Causality Tracking ✅
- [x] `VectorClock` maps `SessionID` → `uint64`
- [x] `Increment(sessionID)` returns new clock (immutable)
- [x] `Merge(other)` returns pointwise maximum
- [x] `HappensBefore(other)` for causal ordering
- [x] `Concurrent(other)` when neither precedes
- [x] `Clone()` creates independent copy
- [x] `Compare()` returns -1, 0, 1, or 2 (concurrent)
- [x] Tests pass with race detector

#### FS.1.3 OperationID and Operation Types ✅
- [x] `OperationID` as content-addressable `[32]byte`
- [x] `OpType`: Insert, Delete, Replace, Move with String()
- [x] `Operation` struct with: ID, BaseVersion, FilePath, Target, Type, Content, OldContent
- [x] Includes: PipelineID, SessionID, AgentID, AgentRole, Clock, Timestamp
- [x] `Invert()` for rollback (swap content/oldContent)
- [x] `Clone()`, `Size()`, `IsEmpty()` helpers
- [x] `NewOperation()` constructor with computed ID
- [x] Tests pass with race detector

#### FS.1.4 AST-Aware Target ✅
- [x] `Target` struct with NodePath, NodeType, NodeID
- [x] Offset fallback: StartOffset, EndOffset
- [x] Line info: StartLine, EndLine
- [x] `IsAST()`, `IsOffset()`, `IsLine()`, `IsEmpty()` checks
- [x] `Overlaps(other)` handles AST parent/child relationships
- [x] `Contains(other)` for strict containment
- [x] `Equal()`, `Clone()`, `Size()` helpers
- [x] `WithLines()`, `WithOffsets()` chainable setters
- [x] Tests pass with race detector

#### FS.1.5 FileVersion ✅
- [x] `FileVersion` struct: ID, FilePath, Parents, Operations, ContentHash, ContentSize
- [x] Includes: Clock, Timestamp, PipelineID, SessionID, IsMerge
- [x] Variant support: VariantGroupID, VariantLabel
- [x] `NewFileVersion()` constructor
- [x] `IsRoot()`, `HasParent()`, `IsVariant()` helpers
- [x] `SetVariant()`, `AddOperation()` mutators
- [x] `Clone()`, `Equal()`, `HappensBefore()`, `Concurrent()` methods
- [x] Tests pass with race detector

---

### FILESYSTEM Phase 2: Storage Layer

**Files to create:**
- `core/versioning/blob_store.go`
- `core/versioning/operation_log.go`
- `core/versioning/dag_store.go`
- `core/versioning/wal.go`

**Parallelization Strategy:**
```
├── 2A: BlobStore (depends: FS.1.1)                 ─┐
├── 2B: OperationLog (depends: FS.1.3)               │ PARALLEL
├── 2C: DAGStore (depends: FS.1.5)                   │
└── 2D: WAL (depends: none, but used by 2A-2C)     ─┘
```

**Acceptance Criteria:**

#### FS.2.1 Content-Addressable Blob Store ✅
- [x] `BlobStore` interface: Get, Put, Has, Delete, Size, Count
- [x] `MemoryBlobStore` implementation
- [x] Content deduplication (same content = same hash)
- [x] Thread-safe for concurrent access
- [x] `Hashes()` returns all stored hashes
- [x] `Clear()` removes all blobs
- [x] Tests pass with race detector

#### FS.2.2 Operation Log ✅
- [x] `OperationLog` interface: Append, Get, GetByVersion, GetByFile, GetSince, Count
- [x] Indexes by ID, base version, file path
- [x] `MemoryOperationLog` implementation
- [x] Thread-safe for concurrent append/read
- [x] `Clear()` removes all operations
- [x] Tests pass with race detector

#### FS.2.3 Version DAG Store ✅
- [x] `DAGStore` interface: Add, Get, GetHead, GetHistory, GetChildren, GetAncestors, Has, Count
- [x] Validates parents exist before adding
- [x] Tracks head per file, child relationships
- [x] `MemoryDAGStore` implementation
- [x] Thread-safe, handles merge commits
- [x] `Files()` returns all tracked files
- [x] Tests pass with race detector

#### FS.2.4 Write-Ahead Log ✅
- [x] `WriteAheadLog` interface: Append, Get, GetRange, GetSince, Checkpoint, Truncate, LastSequenceID, Close
- [x] `WALEntry` with Type, SequenceID, Timestamp, PipelineID, SessionID, Data, Checksum
- [x] Entry types: Operation, Version, Commit, Rollback, Checkpoint
- [x] `MemoryWAL` implementation with auto-truncation
- [x] CRC32 checksums with `ValidateChecksum()`
- [x] `EncodeOperation()` for operation serialization
- [x] Tests pass with race detector

---

### FILESYSTEM Phase 3: Virtual File System

**Files to create:**
- `core/versioning/vfs.go`
- `core/versioning/vfs_manager.go`

**Dependencies:** Phase 2 complete

**Acceptance Criteria:**

#### FS.3.1 VFS (Virtual File System) ✅
- [x] `VFS` interface extends existing `VirtualFilesystem` patterns
- [x] Core ops: Read, Write, Delete, Exists, List
- [x] Versioned ops: ReadAt(version), History, Diff
- [x] Transaction support: BeginTransaction, Commit, Rollback
- [x] `PipelineVFS` scoped to pipeline with:
  - [x] Security: `PermissionManager`, `SecretSanitizer`, `AgentRole` checks
  - [x] Path validation via existing `VirtualFilesystem.ValidatePath`
  - [x] Staging directory isolation
  - [x] CVS integration for version coordination
- [x] `FileModification` tracking (OriginalPath, StagingPath, Operation, Timestamp, ContentHash, BaseVersion)
- [x] `FileDiff` with hunks for version comparison

#### FS.3.2 Pipeline VFS Manager ✅
- [x] `VFSManager` interface for managing VFS instances
- [x] `CreatePipelineVFS(cfg)`, `GetPipelineVFS(id)`, `ClosePipelineVFS(id)`
- [x] Variant group support:
  - [x] `CreateVariantGroup(cfg)` - from Pipeline Variants architecture
  - [x] `AddVariantToGroup(groupID, variantID, cfg)`
  - [x] `SelectVariant(groupID, variantID)` - commits chosen variant
  - [x] `CancelVariantGroup(groupID)`
- [x] Session queries: `GetSessionVFSes(sessionID)`
- [x] Cleanup: `CleanupSession`, `CleanupStaging`
- [x] Integrates with `pipeline.VariantGroup`

---

### FILESYSTEM Phase 4: Operational Transformation Engine

**Files to create:**
- `core/versioning/diff.go`
- `core/versioning/ot.go`
- `core/versioning/conflict_ui.go`

**Dependencies:** Phase 1 complete

**Parallelization Strategy:**
```
├── 4A: Diff Algorithm (depends: FS.1.4)            ─┐
├── 4B: OT Engine (depends: FS.1.3, 4A)              │ SEQUENTIAL
└── 4C: Conflict UI (depends: 4B, Guide)            ─┘
```

**Acceptance Criteria:**

#### FS.4.1 Diff Algorithm ✅
- [x] `Differ` interface: DiffBytes, DiffVersions, ToOperations, FromOperations
- [x] `MyersDiffer` with configurable context lines
- [x] `ASTDiffer` for semantic diffs
- [x] `ASTDiff` with changes: Added, Removed, Modified, Moved
- [x] Operations can reconstruct target from base

#### FS.4.2 Operational Transformation ✅
- [x] `OTEngine` interface: Transform, TransformBatch, Compose, CanMergeAutomatically, DetectConflict
- [x] Transform matrix for all operation type pairs
- [x] `Conflict` struct with Op1, Op2, Type, Description, Resolutions
- [x] Conflict types: OverlappingEdit, DeleteEdit, MoveEdit, SemanticConflict
- [x] `Resolution` with Label, Description, ResultOp
- [x] Transform is symmetric (convergent)

#### FS.4.3 Conflict Resolution UI Integration ✅
- [x] `ConflictResolver` interface: ResolveConflict, ResolveConflictBatch, AutoResolve
- [x] `GuideConflictResolver` routes through Guide
- [x] Auto-resolve policies: None, KeepNewest, KeepOldest, KeepBoth
- [x] `ConflictMessage` and `ConflictResponse` for Guide communication
- [x] Signal: CONFLICT_DETECTED for pipeline handling

---

### FILESYSTEM Phase 5: Central Version Store (CVS)

**Files to create:**
- `core/versioning/cvs.go`

**Dependencies:** Phases 2, 3, 4 complete

**Acceptance Criteria:**

#### FS.5.1 Central Version Store ✅
- [x] `CVS` interface - main coordinator for all versioning
- [x] File operations: Read, Write, Delete (versioned)
- [x] Version queries: GetVersion, GetHead, GetHistory
- [x] Pipeline operations:
  - [x] `BeginPipeline(cfg)` returns `PipelineVFS`
  - [x] `CommitPipeline(pipelineID)` creates new versions
  - [x] `RollbackPipeline(pipelineID)` discards changes
- [x] Merge operations: `Merge`, `ThreeWayMerge` with OT
- [x] Cross-session coordination:
  - [x] `AcquireFileLock(filePath, sessionID)` / `ReleaseFileLock`
  - [x] File locks with timeout to prevent deadlocks
- [x] Variant operations:
  - [x] `BeginVariantGroup`, `AddVariant`, `SelectVariant`
- [x] Subscriptions for file changes:
  - [x] `Subscribe(filePath, sessionID, callback)` / `Unsubscribe`
- [x] Security checks via `PermissionManager` and `SecretSanitizer`
- [x] WAL integration for crash recovery
- [x] `CVSStats`: TotalFiles, TotalVersions, TotalOperations, ActivePipelines, ActiveVariants, ActiveLocks

---

### FILESYSTEM Phase 6: Pipeline Integration

**Files to create:**
- `core/pipeline/vfs_integration.go`
- `core/session/pipeline_scheduler_vfs.go`

**Dependencies:** Phase 5 complete, existing Pipeline architecture

**Acceptance Criteria:**

#### FS.6.1 Integrate VFS with Pipeline Runner
- [ ] `PipelineVFSIntegration` struct
- [ ] `SetupPipeline(p *Pipeline)` creates VFS on pipeline creation
- [ ] `TeardownPipeline(p, success)` commits/rolls back
- [ ] Engineer file operations route through VFS
- [ ] Updated engineer skills use VFS

#### FS.6.2 Integrate with Pipeline Scheduler
- [ ] `PipelineSchedulerVFS` extends existing scheduler
- [ ] File dependency tracking per task
- [ ] Write-write conflict detection: `DetectFileConflicts(tasks)`
- [ ] Conflicting tasks serialized (not parallel)
- [ ] VFS setup/teardown in task lifecycle

---

### FILESYSTEM Phase 7: Cross-Session Coordination

**Files to create:**
- `core/session/signal_dispatcher_vfs.go`
- `core/session/cross_session_pool_vfs.go`

**Dependencies:** Phase 5 complete, existing session infrastructure

**Acceptance Criteria:**

#### FS.7.1 Signal Dispatcher for File Changes ✅
- [x] File change signals: FILE_CREATED, FILE_MODIFIED, FILE_DELETED
- [x] Lock signals: FILE_LOCKED, FILE_UNLOCKED
- [x] Conflict signal: MERGE_CONFLICT
- [x] `FileChangeSignal` with FilePath, OldVersion, NewVersion, ChangedBy
- [x] `BroadcastFileChange(signal)` to interested sessions
- [x] `SubscribeFileChanges(sessionID, patterns)` with glob support

#### FS.7.2 Cross-Session Pool for File Coordination ✅
- [x] `CrossSessionPoolVFS` extends existing pool
- [x] `AcquireFileAccess(sessionID, files, mode)` - read/write locks
- [x] `ReleaseFileAccess(locks)`
- [x] Read access allows concurrent reads
- [x] Write access is exclusive
- [x] `GetFileOwners(files)` returns lock holders
- [x] Coordinated operations with automatic lock management

---

### FILESYSTEM Phase 8: Tree-Sitter Parsing Infrastructure

**Overview:** Pure Go tree-sitter integration using purego for cgo-free dynamic library loading.
Provides universal AST parsing for 30+ languages with incremental parsing and user-extensible grammar support.

**Files to create:**
- `core/treesitter/bindings.go` (purego bindings)
- `core/treesitter/types.go` (high-level Go types)
- `core/treesitter/parser.go` (Parser implementation)
- `core/treesitter/tree.go` (Tree/Node implementation)
- `core/treesitter/query.go` (Query/Cursor implementation)
- `core/treesitter/registry.go` (Grammar registry)
- `core/treesitter/downloader.go` (Grammar installation)
- `core/treesitter/manager.go` (CVS integration)
- `core/treesitter/tool.go` (Agent tool interface)
- `core/treesitter/skills/` (Agent-specific skills)

**Parallelization Strategy:**
```
┌─────────────────────────────────────────────────────────────────────────┐
│                     TREE-SITTER IMPLEMENTATION PHASES                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  TS PHASE 1 (All parallel - foundation):                                 │
│  ├── TS.1.1: Purego Bindings (libtree-sitter.so)                         │
│  ├── TS.1.2: High-Level Go Types                                         │
│  └── TS.1.3: Grammar Registry (known languages)                          │
│                                                                          │
│  TS PHASE 2 (After Phase 1, parallel):                                   │
│  ├── TS.2.1: Parser Implementation                                       │
│  ├── TS.2.2: Tree/Node Implementation                                    │
│  ├── TS.2.3: Query Engine                                                │
│  └── TS.2.4: Grammar Downloader                                          │
│                                                                          │
│  TS PHASE 3 (After Phase 2):                                             │
│  └── TS.3.1: TreeSitterManager (CVS integration)                         │
│                                                                          │
│  TS PHASE 4 (After Phase 3, parallel):                                   │
│  ├── TS.4.1: TreeSitterTool (agent interface)                            │
│  ├── TS.4.2: Librarian Skills                                            │
│  ├── TS.4.3: Engineer Skills                                             │
│  ├── TS.4.4: Inspector Skills                                            │
│  ├── TS.4.5: Tester Skills                                               │
│  └── TS.4.6: Designer Skills                                             │
│                                                                          │
│  TS PHASE 5 (After Phase 4):                                             │
│  ├── TS.5.1: CLI Commands                                                │
│  └── TS.5.2: Setup Integration                                           │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

**Acceptance Criteria:**

---

#### TS.1.1 Purego Bindings
- [ ] `purego.Dlopen` for libtree-sitter.so/.dylib
- [ ] Core type bindings: `TSParser`, `TSTree`, `TSNode`, `TSLanguage`
- [ ] Query types: `TSQuery`, `TSQueryCursor`, `TSQueryMatch`
- [ ] Function bindings via `purego.RegisterLibFunc`:
  - [ ] `ts_parser_new`, `ts_parser_delete`
  - [ ] `ts_parser_set_language`, `ts_parser_parse_string`
  - [ ] `ts_tree_root_node`, `ts_tree_delete`
  - [ ] `ts_node_*` functions (type, string, child, next_sibling, etc.)
  - [ ] `ts_query_new`, `ts_query_cursor_*` functions
- [ ] Platform detection (darwin/linux/windows) for library loading
- [ ] Error handling for missing library with clear instructions

#### TS.1.2 High-Level Go Types
- [ ] `Parser` struct wrapping `TSParser`
- [ ] `Language` struct wrapping `TSLanguage`
- [ ] `Tree` struct with Root(), Edit(), Copy()
- [ ] `Node` struct with Type(), Text(), Children(), Range(), etc.
- [ ] `Point` struct (Row, Column)
- [ ] `Range` struct (StartPoint, EndPoint, StartByte, EndByte)
- [ ] `InputEdit` for incremental parsing
- [ ] Memory management with finalizers

#### TS.1.3 Grammar Registry
- [ ] `GrammarInfo` struct: Name, Extensions, RepoURL, QueryPatterns
- [ ] `KnownGrammars` map with 30+ languages:
  - [ ] Go, Rust, Python, JavaScript, TypeScript, Ruby, Java, C, C++
  - [ ] HTML, CSS, JSON, YAML, TOML, Markdown, SQL, Bash
  - [ ] Swift, Kotlin, Scala, Haskell, OCaml, Lua, Zig, Odin
  - [ ] JSX, TSX, Vue, Svelte, Dockerfile, Terraform, GraphQL
- [ ] `GetGrammarForFile(path)` returns GrammarInfo
- [ ] `GetGrammarByName(name)` returns GrammarInfo
- [ ] `ListKnownGrammars()` returns all available
- [ ] `ListInstalledGrammars()` returns locally available

---

#### TS.2.1 Parser Implementation
- [ ] `NewParser()` creates parser with default options
- [ ] `SetLanguage(lang)` configures parser for language
- [ ] `Parse(source []byte)` returns Tree
- [ ] `ParseIncremental(oldTree, newSource, edit)` for incremental parsing
- [ ] `SetTimeout(micros)` for parsing timeout
- [ ] `SetIncludedRanges(ranges)` for partial parsing
- [ ] Thread-safe parsing (one parser per goroutine)
- [ ] Automatic language detection from file extension

#### TS.2.2 Tree/Node Implementation
- [ ] `Tree.RootNode()` returns root Node
- [ ] `Tree.Edit(edit)` updates tree for incremental parsing
- [ ] `Tree.Walk()` returns TreeCursor for traversal
- [ ] `Node.Type()` returns node type string
- [ ] `Node.Text()` returns node source text
- [ ] `Node.Children()` returns child nodes
- [ ] `Node.Parent()` returns parent node
- [ ] `Node.NextSibling()`, `Node.PrevSibling()`
- [ ] `Node.ChildByFieldName(name)` for named fields
- [ ] `Node.Range()` returns byte range
- [ ] `Node.StartPoint()`, `Node.EndPoint()` for position
- [ ] `Node.IsNamed()`, `Node.IsMissing()`, `Node.IsExtra()`
- [ ] Stable node path generation: `func.HandleRequest.body.if[0]`

#### TS.2.3 Query Engine
- [ ] `Query` struct for S-expression patterns
- [ ] `NewQuery(language, pattern)` compiles query
- [ ] `Query.CaptureNames()` returns capture names
- [ ] `QueryCursor` for executing queries
- [ ] `cursor.Exec(query, node)` starts matching
- [ ] `cursor.NextMatch()` iterates matches
- [ ] `QueryMatch` with Captures slice
- [ ] `QueryCapture` with Node and Name
- [ ] Predicate support: `#eq?`, `#match?`, `#not-eq?`
- [ ] Common query patterns for each language in registry

#### TS.2.4 Grammar Downloader
- [ ] `GrammarDownloader` struct with data directory config
- [ ] `IsInstalled(name)` checks local availability
- [ ] `Download(name)` fetches prebuilt binary or source
- [ ] Platform-specific binary selection (darwin-arm64, linux-amd64, etc.)
- [ ] Fallback to source compilation if prebuilt unavailable
- [ ] `InstallFromSource(repoURL)` clones and compiles
- [ ] `GetLibraryPath(name)` returns path to .so/.dylib
- [ ] Progress reporting via callback
- [ ] Checksum verification for downloads
- [ ] Installation state machine:
  - [ ] NOT_INSTALLED → DOWNLOADING → VERIFYING → INSTALLED
  - [ ] DOWNLOADING → FAILED (with retry logic)
  - [ ] NOT_INSTALLED → COMPILING → INSTALLED (source fallback)

---

#### TS.3.1 TreeSitterManager (CVS Integration) ✅
- [x] `TreeSitterManager` struct with Parser pool
- [x] `ComputeNodePath(tree, offset)` returns stable AST path
- [x] `ResolveNodePath(tree, path)` finds node from path
- [x] `ParseIncremental(oldVersion, newContent, edit)` returns new Tree
- [x] `GetEditFromOperation(op)` converts VFS operation to InputEdit
- [x] Integration with `Target` struct from FS.1.4:
  - [x] `Target.NodePath` populated by ComputeNodePath
  - [x] `Target.NodeType` from tree-sitter node type
- [x] Caching of parsed trees per FileVersion
- [x] Automatic language detection from file extension
- [x] Fallback to offset-based targeting for unknown languages

---

#### TS.4.1 TreeSitterTool (Agent Interface) ✅
- [x] `TreeSitterTool` implements `Tool` interface
- [x] Registered as `ts_*` skill family
- [x] Operations:
  - [x] `parse` - Parse file and return AST
  - [x] `query` - Execute S-expression query
  - [x] `find_node` - Find node at position or path
  - [x] `extract_symbols` - Get all definitions
  - [x] `get_node_path` - Get stable path for node
- [x] Automatic grammar installation on first use
- [x] Permission: Read (file access for parsing)
- [x] Output format: JSON with node details

#### TS.4.2 Librarian Tree-Sitter Skills ✅
- [x] `ts_parse` - Parse file and cache AST
- [x] `ts_query` - Execute pattern query
- [x] `ts_find_functions` - Find function definitions
- [x] `ts_find_types` - Find type/class/interface definitions
- [x] `ts_find_imports` - Find import statements
- [x] `ts_find_references` - Find symbol references
- [x] `ts_extract_symbols` - Extract all symbols with metadata
- [x] Integration with indexing for semantic search

#### TS.4.3 Engineer Tree-Sitter Skills ✅
- [x] `ts_parse` - Parse for structure understanding
- [x] `ts_rename_symbol` - Find all occurrences for rename
- [x] `ts_extract_function` - Identify extractable code blocks
- [x] `ts_find_edit_targets` - Get stable targets for edits
- [x] `ts_get_node_at` - Get node at cursor position
- [x] Integration with versioned_write_file for AST-aware edits

#### TS.4.4 Inspector Tree-Sitter Skills ✅
- [x] `ts_complexity_analysis` - Calculate cyclomatic complexity
- [x] `ts_find_code_smells` - Detect patterns like deep nesting
- [x] `ts_validate_structure` - Check AST for errors
- [x] `ts_count_nodes` - Count nodes by type (metrics)
- [x] `ts_parse_errors` - Find syntax errors and recovery points
- [x] Integration with 8-Phase Validation System

#### TS.4.5 Tester Tree-Sitter Skills ✅
- [x] `ts_discover_tests` - Find test functions/classes
- [x] `ts_find_testable_functions` - Identify untested functions
- [x] `ts_analyze_test_structure` - Analyze test organization
- [x] `ts_find_assertions` - Find assertion statements
- [x] `ts_match_tests_to_functions` - Map tests to implementations
- [x] Integration with 6-Category Test System

#### TS.4.6 Designer Tree-Sitter Skills ✅
- [x] `ts_extract_components` - Find React/Vue/Svelte components
- [x] `ts_analyze_jsx` - Analyze JSX structure
- [x] `ts_find_styles` - Find CSS-in-JS/styled components
- [x] `ts_extract_props` - Extract component props/interfaces
- [x] `ts_find_hooks` - Find React hooks usage
- [x] `ts_analyze_accessibility` - Find a11y attributes

---

#### TS.5.1 CLI Commands
- [ ] `sylk grammar list` - Show installed and available grammars
- [ ] `sylk grammar install <name>` - Install grammar from registry
- [ ] `sylk grammar add <repo-url>` - Add custom grammar
- [ ] `sylk grammar validate <path>` - Validate grammar.yaml
- [ ] `sylk grammar remove <name>` - Remove installed grammar
- [ ] `sylk grammar info <name>` - Show grammar details
- [ ] Output formatting with progress indicators

#### TS.5.2 Setup Integration
- [ ] `sylk setup` includes tree-sitter library installation
- [ ] Automatic libtree-sitter download for platform
- [ ] Default grammar set installation (Go, TS, Python, Rust)
- [ ] User grammar directory creation (~/.sylk/grammars/)
- [ ] Configuration file generation (grammar.yaml template)

---

#### TS.6.1 User-Defined Grammar Support
- [ ] Grammar directory structure: `~/.sylk/grammars/<name>/`
- [ ] `grammar.yaml` format:
  ```yaml
  name: mylang
  extensions: [.ml, .mli]
  library: parser.so      # or parser.dylib, parser.dll
  queries:
    highlights: queries/highlights.scm
    locals: queries/locals.scm
    tags: queries/tags.scm
  ```
- [ ] `~/.sylk/config.yaml` grammar paths configuration
- [ ] Hot-reload support for grammar changes
- [ ] Validation of grammar.yaml on load

#### TS.6.2 Testing and Documentation
- [ ] Unit tests for all purego bindings
- [ ] Integration tests with real grammars
- [ ] Benchmark: Parse time vs file size
- [ ] Benchmark: Incremental vs full parse
- [ ] Memory leak tests for tree lifecycle
- [ ] Documentation: Adding new language support
- [ ] Documentation: Query pattern cookbook
- [ ] Example: Custom domain-specific language grammar

---

### FILESYSTEM Phase 9: Agent Skills and Hooks

**Files to create:**
- `core/versioning/skills.go`
- `core/versioning/hooks.go`

**Dependencies:** Phase 5 complete

**Acceptance Criteria:**

#### FS.9.1 Versioning Skills for Engineers
- [ ] `versioned_read_file` - read at specific version or HEAD
- [ ] `versioned_write_file` - creates new version with message
- [ ] `versioned_file_history` - returns version list
- [ ] `versioned_file_diff` - diff between versions
- [ ] `versioned_rollback` - rollback to previous version
- [ ] All skills respect `AgentRole` permissions
- [ ] Replaces/extends existing engineer file skills

#### FS.9.2 File Operation Hooks
- [ ] `PreWriteHook` - validates permissions, sanitizes secrets, checks locks
- [ ] `PostWriteHook` - records to Archivalist, updates session context, emits signals
- [ ] `ConflictDetectionHook` - checks for concurrent modification
- [ ] Integrates with existing hook system from ARCHITECTURE.md
- [ ] Updates `SessionContext.ModifiedFiles`

---

### FILESYSTEM Phase 10-11: Testing and Documentation

**Files to create:**
- `core/versioning/integration_test.go`
- `core/versioning/stress_test.go`
- `docs/versioning.md`
- `examples/versioning/`

**Acceptance Criteria:**

#### FS.10.1 Integration Test Framework
- [ ] `TestHarness` with CVS, VFSManager, temp directory
- [ ] Test scenarios: SinglePipelineWorkflow, ConcurrentPipelines, VariantWorkflow
- [ ] Test scenarios: CrossSessionCoordination, ConflictResolution, CrashRecovery
- [ ] 90%+ code coverage

#### FS.10.2 Stress Tests
- [ ] High concurrency: 10+ sessions, 100+ pipelines
- [ ] Large files: 10MB+ without OOM
- [ ] Many versions: 1000+ per file, stable performance
- [ ] Benchmarks for Write, Read, Transform, GetHistory

#### FS.11.1 Documentation
- [ ] Architecture overview
- [ ] API reference
- [ ] Configuration options
- [ ] Troubleshooting guide

---

### FILESYSTEM Security Compliance Checklist

For each file operation task:
- [ ] Uses `security.PermissionManager` for access control
- [ ] Uses `security.AgentRole` for role-based permissions
- [ ] Uses `security.SecretSanitizer` before logging/storing
- [ ] Validates paths with `VirtualFilesystem.ValidatePath`
- [ ] Respects `FilesystemConfig` boundaries
- [ ] Records operations in audit log

---

## Plan Mode Implementation

**Reference**: See ARCHITECTURE.md "Plan Mode Architecture" section for detailed specifications.

### PM.1 Core Plan Package Types

**Files to create:**
- `core/plan/types.go`

**Acceptance Criteria:**

- [ ] `PlanState` type with constants: `IDLE`, `EXPLORING`, `DRAFTING`, `AWAITING_APPROVAL`, `APPROVED`, `EXECUTING`, `COMPLETED`, `FAILED`
- [ ] `PlanContext` struct with SessionID, State, CurrentPlan, Todos, PlanHistory, ExecutionState, CreatedAt, UpdatedAt
- [ ] `Plan` struct with ID, Title, Overview, FilesToModify, Steps, Considerations, AllowedPrompts, Version, CreatedAt, CreatedBy
- [ ] `FileChange` struct with Path, Action ("create", "modify", "delete"), Description
- [ ] `PlanStep` struct with Index, Description, Agent, DependsOn ([]int), Layer (computed), Estimated
- [ ] `TopologicalLayer` struct with Layer (int), Steps ([]PlanStep)
- [ ] `AllowedPrompt` struct with Tool, Prompt
- [ ] `PlanVersion` struct with Version, Plan, Reason, CreatedAt
- [ ] `TodoItem` struct with Content, ActiveForm, Status, StepIndex
- [ ] `TodoStatus` type with constants: `pending`, `in_progress`, `completed`
- [ ] `ExecutionState` struct with DAGID, CompletedSteps, CurrentStep, FailedStep, FailureReason, StartedAt, StepResults
- [ ] `StepResult` struct with StepIndex, Status, Output, Error, CompletedAt

### PM.2 Topological Layer Computation

**Files to create:**
- `core/plan/topology.go`

**Acceptance Criteria:**

- [ ] `ComputeTopologicalLayers(steps []PlanStep) []TopologicalLayer` - Kahn's algorithm variant
- [ ] `computeStepLayer(idx int, steps map[int]*PlanStep, computed map[int]int) int` - recursive layer computation
- [ ] Layer 0 contains steps with no dependencies
- [ ] Layer N contains steps whose dependencies are all in layers < N
- [ ] Steps in same layer can execute concurrently
- [ ] Function modifies step.Layer field in-place
- [ ] Cycle detection returns error for circular dependencies
- [ ] Unit test: linear chain (A→B→C) produces layers [0], [1], [2]
- [ ] Unit test: parallel roots (A, B, C) produces single layer [0, 1, 2]
- [ ] Unit test: diamond pattern (A→B, A→C, B→D, C→D) produces layers [0], [1, 2], [3]
- [ ] Unit test: complex DAG with mixed dependencies
- [ ] Unit test: cycle detection raises error

### PM.3 Plan Storage

**Files to create:**
- `core/plan/storage.go`
- `core/plan/slugify.go`

**Storage Location:** `~/.sylk/projects/{project_hash}/plans/{plan_slug}/`

Plans are stored in user space indexed by project hash. This ensures:
- Plans persist across sessions (not tied to ephemeral session lifecycle)
- No pollution of project directory (user-private, not committed to git)
- Project-aware organization via consistent hashing

**Directory Structure:**
```
~/.sylk/
├── sessions/{session_id}/              ← Ephemeral (existing)
│   └── active_plan.txt                 ← Pointer: "implement-user-auth"
│
└── projects/{project_hash}/            ← Persistent per-project
    ├── project_info.json               ← { path, hash, created_at }
    ├── plans/
    │   └── {plan_slug}/                ← e.g., "implement-user-auth"
    │       ├── metadata.json           ← Version history, change sets
    │       ├── context.json            ← Current PlanContext
    │       ├── todos.json              ← Todo list state
    │       ├── current.md              ← Copy of HEAD version
    │       ├── v1.md                   ← Version 1
    │       ├── v2.md                   ← Version 2
    │       └── checkpoints/
    │           └── v1_step3.json       ← Execution snapshots
    └── archived/
        └── {old_plan_slug}/
            ├── *.md, *.json
            └── outcome.json            ← Final result
```

**Example Paths:**
```
~/.sylk/projects/a1b2c3d4/plans/implement-user-auth/v3.md
~/.sylk/projects/a1b2c3d4/plans/implement-user-auth/metadata.json
~/.sylk/projects/a1b2c3d4/plans/implement-user-auth/checkpoints/v2_step3.json
~/.sylk/projects/a1b2c3d4/archived/fix-login-bug/outcome.json
```

**Implementation Guide:**

1. **Project Hash Function** - Use existing `ProjectHash()` from `core/plan/storage.go`:
   ```go
   func ProjectHash(projectRoot string) string {
       absPath, _ := filepath.Abs(projectRoot)
       hash := sha256.Sum256([]byte(absPath))
       return hex.EncodeToString(hash[:8]) // 16 chars
   }
   ```

2. **Slugify Function** - Convert plan title to filesystem-safe slug:
   ```go
   func Slugify(title string) string {
       slug := strings.ToLower(title)
       reg := regexp.MustCompile(`[^a-z0-9]+`)
       slug = reg.ReplaceAllString(slug, "-")
       slug = strings.Trim(slug, "-")
       if len(slug) > 50 { slug = slug[:50] }
       return slug
   }
   ```

3. **Storage Initialization** - On `NewStorage()`:
   - Resolve `~/.sylk` from `os.UserHomeDir()`
   - Compute project hash from provided project root
   - Create `~/.sylk/projects/{hash}/plans/` and `archived/` directories
   - Write `project_info.json` if not exists

4. **Version File Naming** - Use `v{N}.md` format (v1.md, v2.md, ...)
   - Include header comment: `<!-- Plan: {title} | Version: {N} | Created: {timestamp} -->`

5. **Current Pointer** - `current.md` is a copy (not symlink) of HEAD version
   - Symlinks can be problematic on Windows and some filesystems

**Acceptance Criteria:**

#### Storage Struct & Constructor
- [ ] `Storage` struct with fields: `sylkRoot`, `projectHash`, `projectRoot`
- [ ] `NewStorage(projectRoot string) (*Storage, error)` - initializes storage
- [ ] Constructor calls `os.UserHomeDir()` to resolve `~/.sylk`
- [ ] Constructor calls `ProjectHash()` to compute project identifier
- [ ] Constructor creates directory structure via `initProjectDir()`
- [ ] Constructor writes `project_info.json` with path, hash, created_at

#### Path Resolution Functions
- [ ] `projectDir() string` - returns `~/.sylk/projects/{hash}`
- [ ] `planDir(planSlug string) string` - returns `{projectDir}/plans/{slug}`
- [ ] `archivedDir() string` - returns `{projectDir}/archived`

#### Slugify Function
- [ ] `Slugify(title string) string` - converts title to filesystem-safe slug
- [ ] Lowercase conversion
- [ ] Replace non-alphanumeric with hyphens
- [ ] Trim leading/trailing hyphens
- [ ] Max length 50 characters
- [ ] Unit test: "Implement User Auth!" → "implement-user-auth"
- [ ] Unit test: "  Multiple   Spaces  " → "multiple-spaces"
- [ ] Unit test: "UPPERCASE" → "uppercase"

#### Plan Directory Management
- [ ] `EnsurePlanDir(planSlug string) error` - creates plan directory + checkpoints/
- [ ] `ListPlans() ([]string, error)` - returns all plan slugs for project
- [ ] `ArchivePlan(planSlug string, outcome *PlanOutcome) error` - moves to archived/

#### Context Persistence
- [ ] `SaveContext(ctx *PlanContext) error` - saves context.json, version md, todos.json
- [ ] `LoadContext(planSlug string) (*PlanContext, error)` - loads from context.json
- [ ] `LoadContext` returns error (not empty context) for non-existent plans
- [ ] `UpdatedAt` timestamp updated on every `SaveContext`

#### Version Management
- [ ] `saveVersionMarkdown(planSlug, plan) error` - writes v{N}.md with header
- [ ] `updateCurrentPointer(planSlug, version) error` - copies v{N}.md to current.md
- [ ] `LoadVersion(planSlug, version) (string, error)` - reads specific version
- [ ] Version files include metadata header comment

#### Metadata Persistence
- [ ] `SaveMetadata(planSlug string, meta *PlanMetadata) error`
- [ ] `LoadMetadata(planSlug string) (*PlanMetadata, error)`
- [ ] Returns empty metadata (not error) for new plans

#### Checkpoint Management
- [ ] `SaveCheckpoint(planSlug, version, stepIndex, state) error`
- [ ] Checkpoint filename: `v{version}_step{index}.json`
- [ ] Checkpoints stored in `{planDir}/checkpoints/`

#### Outcome Recording
- [ ] `PlanOutcome` struct with: Result, CompletedAt, StepsComplete, StepsTotal, FailureReason, Duration
- [ ] `outcome.json` written on archive

#### Unit Tests
- [ ] `ProjectHash` produces consistent 16-char hex string
- [ ] `Slugify` handles edge cases (empty, special chars, long strings)
- [ ] Save/Load context round-trip preserves all fields
- [ ] Version files created with correct naming (v1.md, v2.md)
- [ ] `current.md` updated when new version saved
- [ ] `ListPlans` returns correct slugs
- [ ] `ArchivePlan` moves directory and writes outcome
- [ ] Checkpoint files created in correct location

#### Integration Tests
- [ ] Multiple plans for same project stored correctly
- [ ] Plans persist after session ends and new session starts
- [ ] Different projects (different hashes) isolated
- [ ] Archived plans queryable but not in active list

### PM.4 Plan Markdown Formatter

**Files to create:**
- `core/plan/formatter.go`

**Acceptance Criteria:**

- [ ] `formatPlanMarkdown(plan *Plan) string` - generates layered markdown output
- [ ] Output includes: Title, Overview, Files to Modify, Execution Plan diagram, Detailed Steps by Layer, Sequential Step Reference, Considerations, Required Permissions
- [ ] Execution Plan shows ASCII flow diagram with layers
- [ ] Layers show "(parallel: N tasks)" when N > 1
- [ ] Detailed Steps grouped by layer with concurrency indicator
- [ ] Dependencies shown as "Depends on: steps [N, M]"
- [ ] Sequential reference shows layer number: "[Layer N] [agent] description"
- [ ] Unit test: single-step plan formats correctly
- [ ] Unit test: multi-layer plan shows correct grouping
- [ ] Unit test: parallel steps within layer show concurrency count

### PM.5 Plan State Machine

**Files to create:**
- `core/plan/state_machine.go`

**State Transitions:**
```
IDLE → EXPLORING (on enter_plan_mode)
EXPLORING → DRAFTING (on begin drafting)
DRAFTING → AWAITING_APPROVAL (on exit_plan_mode)
AWAITING_APPROVAL → APPROVED (on user approval)
AWAITING_APPROVAL → DRAFTING (on user rejection with feedback)
AWAITING_APPROVAL → IDLE (on user cancel)
APPROVED → EXECUTING (on execution start)
EXECUTING → COMPLETED (on all steps done)
EXECUTING → FAILED (on unrecoverable error)
COMPLETED/FAILED → IDLE (on new task)
```

**Acceptance Criteria:**

- [ ] `Transition(ctx *PlanContext, event PlanEvent) error` - validates and applies transition
- [ ] `PlanEvent` type with constants: `EnterPlanMode`, `BeginDrafting`, `ExitPlanMode`, `UserApprove`, `UserReject`, `UserCancel`, `StartExecution`, `ExecutionComplete`, `ExecutionFailed`, `Reset`
- [ ] Returns error for invalid transitions (e.g., IDLE → APPROVED)
- [ ] `ValidTransitions(state PlanState) []PlanEvent` - returns valid events for state
- [ ] `CanTransition(ctx *PlanContext, event PlanEvent) bool` - checks without applying
- [ ] Unit test: all valid transitions succeed
- [ ] Unit test: invalid transitions return error
- [ ] Unit test: state unchanged on invalid transition

### PM.6 Plan Mode Skills (Architect Agent)

**Files to create:**
- `agents/architect/plan_mode.go`

**Acceptance Criteria:**

#### enter_plan_mode skill
- [ ] Creates new PlanContext if not exists
- [ ] Transitions state IDLE → EXPLORING
- [ ] Persists context to storage
- [ ] Returns confirmation message
- [ ] Rejects if already in plan mode (state != IDLE)

#### exit_plan_mode skill
- [ ] Parameters: allowedPrompts ([]AllowedPrompt)
- [ ] Validates plan file exists and is complete
- [ ] Computes topological layers for all steps
- [ ] Transitions state DRAFTING → AWAITING_APPROVAL
- [ ] Persists plan version to history
- [ ] Returns plan summary for user review
- [ ] Rejects if state != DRAFTING

#### update_plan_file skill
- [ ] Parameters: title, overview, files_to_modify, steps, considerations
- [ ] Validates all required fields present
- [ ] Validates step dependencies reference valid indices
- [ ] Computes topological layers
- [ ] Increments plan version
- [ ] Transitions EXPLORING → DRAFTING if first update
- [ ] Persists updated plan
- [ ] Returns layer summary showing concurrency opportunities

### PM.7 Todo Management Skills (Architect Agent)

**Files to create:**
- `agents/architect/todo.go`

**Acceptance Criteria:**

#### todo_write skill
- [ ] Parameters: todos ([]TodoItem)
- [ ] Validates each todo has content, activeForm, status
- [ ] Only one todo can be in_progress at a time
- [ ] Persists to todos.json
- [ ] Returns formatted todo list

#### todo_mark_complete skill (convenience wrapper)
- [ ] Parameters: step_index (int)
- [ ] Finds todo by step_index
- [ ] Updates status to completed
- [ ] Persists change
- [ ] Returns updated list

### PM.8 User Interaction Skill

**Files to create:**
- `agents/architect/questions.go`

**Acceptance Criteria:**

#### ask_user_question skill
- [ ] Parameters: questions ([]Question), each with header, question, options, multiSelect
- [ ] Maximum 4 questions per call
- [ ] Each option has label, description
- [ ] Automatically adds "Other" option for free-text input
- [ ] Blocks until user responds
- [ ] Returns map of question_id → selected_option(s)
- [ ] Rejects if options < 2 or > 4 per question

### PM.9 Orchestrator Plan Execution

**Files to create:**
- `agents/orchestrator/plan_executor.go`

**Acceptance Criteria:**

- [ ] `ExecutePlan(ctx context.Context, plan *Plan, planCtx *PlanContext) error`
- [ ] Converts PlanStep[] to DAG nodes
- [ ] Groups steps by topological layer
- [ ] Executes layers sequentially
- [ ] Executes steps within layer concurrently (up to worker pool limit)
- [ ] Updates ExecutionState after each step
- [ ] Calls StepResult callback for each completed step
- [ ] Aborts remaining steps on failure (configurable)
- [ ] Updates PlanContext state to COMPLETED or FAILED
- [ ] **Emits `StepCompletedSignal` after each step for plan file sync**
- [ ] **Marks dependent steps as `skipped` when a step fails**
- [ ] Integration test: parallel steps in same layer run concurrently
- [ ] Integration test: step failure aborts remaining execution
- [ ] Integration test: dependency ordering enforced across layers
- [ ] **Integration test: plan file updated with checkboxes after each step**

### PM.9.1 Plan File Synchronization

**Files to create:**
- `agents/architect/step_handler.go`
- `core/signals/step_completed.go`

**Acceptance Criteria:**

- [ ] `StepCompletedSignal` struct with SessionID, PlanID, StepIndex, Status, Output, Error, Duration, AgentID, Timestamp
- [ ] Architect subscribes to `plan.step.completed` bus topic
- [ ] `HandleStepCompleted(sig *StepCompletedSignal) error` updates plan on each signal
- [ ] `updateStepStatus(plan, stepIndex, status)` modifies PlanStep.Status
- [ ] `updateTodoForStep(todos, stepIndex, status)` syncs TodoItem status
- [ ] `updateExecutionState(state, sig)` records step result
- [ ] Plan markdown file regenerated on every status change
- [ ] Status checkboxes: `[ ]` pending, `[~]` in_progress, `[x]` completed, `[!]` failed, `[-]` skipped
- [ ] Progress counter updated: "Progress: N/M steps completed"
- [ ] Unit test: step completion updates markdown file
- [ ] Unit test: failure marks step with `[!]` and dependents with `[-]`

### PM.9.2 Failure Recovery

**Files to create:**
- `agents/architect/recovery.go`

**Acceptance Criteria:**

- [ ] `HandleStepFailure(ctx *PlanContext, failedStep int, err error) error`
- [ ] Failed step marked with `StepStatusFailed`
- [ ] Dependent steps marked with `StepStatusSkipped`
- [ ] ExecutionState updated with FailedStep and FailureReason
- [ ] Plan file shows failure reason inline: `[!] Step N - **FAILED: reason**`
- [ ] Recovery options: retry (reset to pending), skip (continue), abort (fail plan)
- [ ] Unit test: failure cascades to dependent steps

### PM.9.3 Plan Archival (Archivalist Integration)

**Files to create:**
- `agents/architect/archival.go`

**Purpose:** Archive approved plans and outcomes to Archivalist for long-term storage and cross-session querying.

**Implementation Guide:**

1. **On User Approval** - Archive full plan to `approved_plans` category:
   - Include complete markdown content for searchability
   - Extract tags from file paths for categorization
   - Record approval timestamp and approver

2. **On Completion/Failure** - Archive outcome to `plan_outcomes` category:
   - Include execution statistics (steps completed, failed, duration)
   - Record failure reason if applicable
   - Link to original approved plan entry

**Acceptance Criteria:**

#### Plan Approval Archival
- [ ] `ArchivePlanOnApproval(ctx *PlanContext) error` - archives when user approves
- [ ] Validates state is `PlanStateApproved` before archiving
- [ ] Archives to Archivalist category `approved_plans`
- [ ] Content includes full `formatPlanMarkdown()` output
- [ ] Metadata includes:
  - `plan_id`: Unique plan identifier
  - `project_hash`: Links to project
  - `plan_slug`: Filesystem slug
  - `title`: Human-readable title
  - `step_count`: Number of steps
  - `layer_count`: Number of topological layers
  - `files_count`: Number of files to modify
  - `created_by`: Agent that created plan
  - `approved_at`: Approval timestamp
  - `tags`: Extracted from file paths (e.g., ["auth", "api", "handlers"])
- [ ] `extractPlanTags(plan)` extracts unique directory names from file paths

#### Outcome Archival
- [ ] `ArchivePlanOutcome(ctx *PlanContext) error` - archives on completion/failure
- [ ] Validates state is `PlanStateCompleted` or `PlanStateFailed`
- [ ] Archives to Archivalist category `plan_outcomes`
- [ ] Content includes:
  - Plan title and result summary
  - Execution statistics
  - Full plan markdown with step statuses
- [ ] Metadata includes:
  - `plan_id`: Links to approved plan
  - `project_hash`: Links to project
  - `outcome`: "success" or "failure"
  - `completed_steps`: Count of completed steps
  - `failed_steps`: Count of failed steps
  - `skipped_steps`: Count of skipped steps
  - `total_steps`: Total step count
  - `duration_seconds`: Total execution time
  - `failure_reason`: Error message if failed
  - `failed_step_index`: Which step failed (if applicable)
  - `completed_at`: Completion timestamp

#### Unit Tests
- [ ] Approval archival includes all required metadata fields
- [ ] Outcome archival correctly computes statistics
- [ ] Tags extraction handles nested paths correctly
- [ ] Error returned if archiving in wrong state

#### Integration Tests
- [ ] Approved plan queryable via Archivalist `search("approved_plans", ...)`
- [ ] Plan outcomes searchable by success/failure filter
- [ ] Can retrieve plan by project_hash + plan_slug
- [ ] Outcome links correctly to original approved plan

### PM.9.4 Plan Version Tracking (Archivalist)

**Files to create:**
- `agents/architect/version_tracker.go`
- `agents/archivalist/plan_versions.go`

**Purpose:** Enable Archivalist to track all plan versions and answer queries like "What changed between v2 and v4?"

**Storage Categories:**
- `plan_versions`: Each version stored as separate entry
- `plan_change_sets`: Computed diffs between versions (cached)

**Implementation Guide:**

1. **On Each Version Save** - Archive version to `plan_versions`:
   ```go
   entry := &archivalist.Entry{
       Category: "plan_versions",
       Content:  versionMarkdown,
       Metadata: map[string]any{
           "project_hash": projectHash,
           "plan_slug":    slug,
           "version":      version,
           "parent_version": parentVersion,
           "step_count":   len(steps),
           "layer_count":  countLayers(steps),
           "created_at":   time.Now(),
           "created_by":   agentID,
           "reason":       reason,  // "initial", "user_feedback", "step_failure", etc.
       },
   }
   ```

2. **Change Set Computation** - When diff requested:
   - Load both versions from Archivalist
   - Compute structural diff (steps added/removed/modified)
   - Cache result in `plan_change_sets` category
   - Return human-readable diff summary

3. **Query Interface** - Archivalist skill for version queries:
   ```
   User: "What changed between v2 and v4 of the auth plan?"

   Archivalist receives:
   {
       "type": "version_diff",
       "project_hash": "a1b2c3d4",
       "plan_slug": "implement-user-auth",
       "from_version": 2,
       "to_version": 4
   }
   ```

**Acceptance Criteria:**

#### Version Archival
- [ ] `ArchivePlanVersion(planSlug, plan, reason) error` - archives each version
- [ ] Called automatically when `Storage.SaveContext()` creates new version
- [ ] Archives to Archivalist category `plan_versions`
- [ ] Metadata includes: project_hash, plan_slug, version, parent_version, reason, created_at
- [ ] Content includes full version markdown

#### Change Set Types
- [ ] `ChangeSet` struct with: FromVersion, ToVersion, Summary, StepsAdded, StepsRemoved, StepsModified, FilesAdded, FilesRemoved, DependencyChanges
- [ ] `StepSummary` struct with: Index, Description, Agent, Layer
- [ ] `StepDiff` struct with: Index, Changes (map of field → {old, new})
- [ ] `DepChange` struct with: StepIndex, OldDependsOn, NewDependsOn

#### Diff Computation
- [ ] `ComputeVersionDiff(planSlug, fromVersion, toVersion) (*ChangeSet, error)`
- [ ] Correctly identifies added steps (in new, not in old)
- [ ] Correctly identifies removed steps (in old, not in new)
- [ ] Correctly identifies modified steps (same index, different content)
- [ ] Tracks dependency changes separately
- [ ] Tracks file list changes
- [ ] Computes layer count changes

#### Diff Formatting
- [ ] `FormatChangeSetMarkdown(cs *ChangeSet) string` - human-readable diff
- [ ] Output includes sections: Steps Added, Steps Removed, Steps Modified, Files Changed, Topology Changes
- [ ] Modified steps show field-level diffs (e.g., "Description: old → new")

#### Archivalist Query Skill
- [ ] `query_plan_versions` skill in Archivalist
- [ ] Accepts: project_hash, plan_slug, from_version, to_version
- [ ] Returns formatted diff or error if versions not found
- [ ] Caches computed diffs in `plan_change_sets` category

#### Caching
- [ ] Computed diffs cached with key `{project_hash}:{plan_slug}:v{from}→v{to}`
- [ ] Cache checked before recomputing
- [ ] Cache invalidated if new version added between from/to range

#### Unit Tests
- [ ] Diff correctly identifies step additions
- [ ] Diff correctly identifies step removals
- [ ] Diff correctly identifies step modifications
- [ ] Diff handles dependency changes
- [ ] Empty diff when versions identical
- [ ] Error when version not found

#### Integration Tests
- [ ] Query "what changed v1→v3" returns correct diff
- [ ] Query "what changed v2→v2" returns empty diff
- [ ] Cache hit on repeated query
- [ ] Version history query returns all versions for plan
- [ ] Cross-session: version archived in session A queryable in session B

#### Supported Queries (via Archivalist)
- [ ] "What changed between v2 and v4?"
- [ ] "Show version history for plan X"
- [ ] "Which version added step N?"
- [ ] "What was the original description of step 3?"
- [ ] "How many times was this plan revised?"
- [ ] "Compare topology between v1 and v5"

### PM.10 Guide Plan Mode Routing

**Files to modify:**
- `agents/guide/router.go`

**Acceptance Criteria:**

- [ ] Detect plan mode triggers in user messages (complex multi-step tasks)
- [ ] Route plan mode requests to Architect
- [ ] Forward user approval/rejection to Architect
- [ ] Route execution requests to Orchestrator
- [ ] Maintain plan context reference for session
- [ ] Unit test: plan trigger detection accuracy
- [ ] Unit test: approval routing to correct agent

### PM.11 Plan Mode SKILL.md Definitions

**Files to create:**
- `skills/plan_mode/SKILL.md`
- `skills/todo_management/SKILL.md`
- `skills/ask_user/SKILL.md`

**Acceptance Criteria:**

- [ ] Each skill file follows agentskills.io spec
- [ ] YAML frontmatter with name, description, license, compatibility, allowed-tools
- [ ] Markdown instructions for when/how to use
- [ ] Skills discoverable via `skills.Discover()`
- [ ] Skills loadable via `skills.ReadFull()`
- [ ] Skills validate via `skills.Validate()`

### PM.12 Integration Tests

**Files to create:**
- `tests/plan_mode_test.go`

**Acceptance Criteria:**

- [ ] Test: Full plan mode lifecycle (enter → draft → approve → execute → complete)
- [ ] Test: Plan rejection and revision cycle
- [ ] Test: Topological layer computation with complex dependencies
- [ ] Test: Concurrent step execution within layers
- [ ] Test: Step failure handling and state transitions
- [ ] Test: Plan versioning and history
- [ ] Test: Todo synchronization with plan steps
- [ ] Test: Multi-session plan isolation
- [ ] Test: Plan persistence across session restart
- [ ] Test: Markdown output format matches specification

### Plan Mode Parallelization Notes

The following tasks can be executed concurrently:

**Layer 0 (No Dependencies):**
- PM.1 Core Plan Package Types
- PM.2 Topological Layer Computation
- PM.11 Plan Mode SKILL.md Definitions

**Layer 1 (Depends on PM.1, PM.2):**
- PM.3 Plan Storage
- PM.4 Plan Markdown Formatter
- PM.5 Plan State Machine

**Layer 2 (Depends on PM.3, PM.4, PM.5):**
- PM.6 Plan Mode Skills
- PM.7 Todo Management Skills
- PM.8 User Interaction Skill

**Layer 3 (Depends on PM.6):**
- PM.9 Orchestrator Plan Execution
- PM.9.1 Plan File Synchronization
- PM.9.2 Failure Recovery
- PM.10 Guide Plan Mode Routing

**Layer 4 (Depends on PM.9, PM.9.1):**
- PM.9.3 Plan Archival (requires Archivalist integration)
- PM.9.4 Plan Version Tracking (requires Archivalist integration)
- PM.12 Integration Tests

---

## Research Paper Implementation

**Reference**: See ARCHITECTURE.md "Research Paper Architecture" section for detailed specifications.

Research papers are comprehensive technical documents produced by the Academic agent that bridge exploratory research and actionable plans. They follow the same storage pattern as plans.

### AR.1 Research Paper Types

**Files to create:**
- `core/research/types.go`

**Implementation Guide:**

Define all types needed for research paper management, following the pattern established in `core/plan/types.go`.

**Acceptance Criteria:**

#### Status Types
- [ ] `ResearchStatus` type with constants: `DRAFT`, `READY_FOR_REVIEW`, `READY_FOR_IMPLEMENTATION`, `APPROVED`, `SUPERSEDED`, `ARCHIVED`

#### Metadata Types
- [ ] `ResearchMetadata` struct with fields:
  - `PaperID` (string): Unique identifier
  - `Slug` (string): Filesystem-safe name
  - `Title` (string): Human-readable title
  - `Status` (ResearchStatus): Current status
  - `Head` (int): Current version number
  - `CreatedAt`, `UpdatedAt` (time.Time)
  - `AcademicAgent` (string): Creating agent ID
  - `SessionID` (string): Originating session
  - `ProjectHash` (string): Project identifier
  - `Tags` ([]string): Categorization tags
  - `ApproachesEvaluated` (int): Count of approaches analyzed
  - `RecommendedApproach` (string): Primary recommendation
  - `EdgeCasesCount` (int): Number of edge cases identified
  - `OpenQuestionsCount` (int): Unresolved questions
  - `LinkedPlan` (string): Associated plan ID (if converted)
  - `Versions` ([]VersionEntry): Version history
  - `ChangeSets` (map[string]ChangeSet): Diffs between versions

#### Decision Types
- [ ] `ResearchDecisions` struct with PaperID, Decisions, OpenQuestionsResolved
- [ ] `Decision` struct with ID, Title, Options, DecidedAt, DecidedBy
- [ ] `DecisionOption` struct with ID, Description, Status (ACCEPTED/REJECTED/DEFERRED/IGNORED)
- [ ] `ResolvedQuestion` struct with Question, Answer, ResolvedAt

#### Document Types
- [ ] `ResearchPaper` struct with Slug, Title, Version, Content, Sections, EdgeCases, etc.
- [ ] `ParsedPaper` struct for extracted sections after parsing

#### Unit Tests
- [ ] All types serialize/deserialize correctly to JSON
- [ ] Status constants have expected string values
- [ ] Decision status validation

### AR.2 Research Paper Storage

**Files to create:**
- `core/research/storage.go`

**Storage Location:** `~/.sylk/projects/{project_hash}/research/{slug}/`

**Directory Structure:**
```
~/.sylk/projects/{project_hash}/research/{slug}/
├── metadata.json           ← Version history, status
├── context.json            ← Current research context
├── decisions.json          ← User decisions on recommendations
├── sources.json            ← URLs, docs referenced
├── current.md              ← Copy of HEAD version
├── v1.md                   ← Version 1
├── v2.md                   ← Version 2
└── attachments/            ← Diagrams, benchmarks
```

**Implementation Guide:**

Follow the same pattern as `core/plan/storage.go`, using `ProjectHash()` for project identification and `Slugify()` for paper naming.

**Acceptance Criteria:**

#### Storage Struct
- [ ] `ResearchStorage` struct with `sylkRoot`, `projectHash`, `projectRoot`
- [ ] `NewResearchStorage(projectRoot string) (*ResearchStorage, error)`
- [ ] Constructor creates `~/.sylk/projects/{hash}/research/` directory

#### Path Resolution
- [ ] `projectDir() string` - returns `~/.sylk/projects/{hash}`
- [ ] `researchDir(slug string) string` - returns `{projectDir}/research/{slug}`
- [ ] `archivedDir() string` - returns `{projectDir}/archived/research`

#### Paper Persistence
- [ ] `SavePaper(paper *ResearchPaper) error` - saves paper and updates metadata
- [ ] `LoadPaper(slug string) (*ResearchPaper, error)` - loads current paper
- [ ] `SaveVersion(slug string, content string, reason string) error` - saves v{N}.md
- [ ] `updateCurrentPointer(slug string, version int) error` - copies v{N}.md to current.md

#### Metadata Management
- [ ] `SaveMetadata(slug string, meta *ResearchMetadata) error`
- [ ] `LoadMetadata(slug string) (*ResearchMetadata, error)`
- [ ] `SaveDecisions(slug string, decisions *ResearchDecisions) error`
- [ ] `LoadDecisions(slug string) (*ResearchDecisions, error)`

#### Version Management
- [ ] `ListVersions(slug string) ([]int, error)` - returns all version numbers
- [ ] `ListPapers() ([]string, error)` - returns all paper slugs

#### Archival
- [ ] `ArchivePaper(slug string, reason string) error` - moves to archived/

#### Unit Tests
- [ ] Save/load paper round-trip
- [ ] Version files created with correct naming
- [ ] Metadata persists across operations
- [ ] Decisions save and load correctly
- [ ] Archive moves directory correctly

### AR.3 PROPOSAL Message Type

**Files to modify:**
- `core/messages/types.go`

**Implementation Guide:**

Add the PROPOSAL message type used for Academic → Architect handoff.

**Acceptance Criteria:**

- [ ] `MessageTypeProposal MessageType = "PROPOSAL"` constant added
- [ ] `ProposalMessage` struct with:
  - `Type` (MessageType): Always "PROPOSAL"
  - `From` (string): "academic"
  - `SessionID` (string)
  - `ProjectHash` (string)
  - `ResearchSlug` (string)
  - `PaperPath` (string): Full path to current.md
  - `Version` (int)
  - `Summary` (string): Brief description
  - `Timestamp` (time.Time)
- [ ] JSON serialization/deserialization works correctly
- [ ] Unit test: message type matches expected value

### AR.4 Revision Message Types

**Files to modify:**
- `core/messages/types.go`
- `core/messages/revision.go` (create)

**Implementation Guide:**

Add message types for plan revision triggers from pipeline agents.

**Acceptance Criteria:**

#### Message Type Constants
- [ ] `MessageTypeTaskModifiedByUser MessageType = "TASK_MODIFIED_BY_USER"`
- [ ] `MessageTypeWorkflowChangeRequired MessageType = "WORKFLOW_CHANGE_REQUIRED"`

#### TaskModifiedByUserMessage
- [ ] Struct with: Type, From, SessionID, PlanSlug, StepIndex, Modification, Description, OriginalTask, ModifiedTask, Timestamp
- [ ] `Modification` values: "scope_expanded", "scope_reduced", "requirements_changed", "task_deleted", "task_added"

#### WorkflowChangeRequiredMessage
- [ ] Struct with: Type, From, SessionID, PlanSlug, StepIndex, ChangeType, Reason, ProposedChange, Blocking, Timestamp
- [ ] `ChangeType` values: "add_step", "delete_step", "split_step", "merge_steps", "change_deps", "modify_step"

#### ProposedChange
- [ ] Struct with: Action, TargetStep, NewStep, ModifiedStep, NewDeps
- [ ] `Action` values: "insert_step", "delete_step", "modify_step", "change_dependencies"

#### Unit Tests
- [ ] All message types serialize correctly
- [ ] Validation functions for required fields

### AR.5 Academic: write_research_paper Skill

**Files to create:**
- `agents/academic/skills/write_research_paper.go`
- `skills/write_research_paper/SKILL.md`

**Trigger Conditions:**
- User says "I'm ready to start implementing"
- User says "Write up your findings"
- User says "Create the research paper"
- Academic determines research is complete and actionable

**Implementation Guide:**

```go
func (s *WriteResearchPaperSkill) Execute(ctx context.Context, params map[string]any) error {
    // 1. Aggregate findings from conversation
    // 2. Structure into paper format
    // 3. Generate diagrams
    // 4. Compile edge cases with user decisions
    // 5. Determine version number
    // 6. Persist to storage
    // 7. Archive to Archivalist
    // 8. Send PROPOSAL to Architect via Guide
}
```

**Acceptance Criteria:**

#### Skill Registration
- [ ] Skill registered with Academic agent
- [ ] SKILL.md file created with proper frontmatter

#### Trigger Detection
- [ ] `detectWriteTrigger(message string) bool` - identifies trigger phrases
- [ ] Does NOT trigger on casual conversation
- [ ] Does NOT trigger on questions about research

#### Paper Generation
- [ ] `aggregateFindings(ctx) *Findings` - collects from conversation
- [ ] `structurePaper(findings) *ResearchPaper` - formats into sections
- [ ] `generateDiagrams(findings) []Diagram` - creates architecture/flow diagrams
- [ ] `compileEdgeCases(findings) []EdgeCase` - extracts with user decisions

#### Persistence
- [ ] Paper saved to `~/.sylk/projects/{hash}/research/{slug}/v{N}.md`
- [ ] Metadata updated with new version
- [ ] Decisions saved to `decisions.json`
- [ ] Sources saved to `sources.json`

#### Archivalist Integration
- [ ] Paper version archived to category `research_paper_versions`
- [ ] Metadata includes: slug, version, status, tags

#### PROPOSAL Message
- [ ] Message sent via `bus.RouteToAgent("architect", "requests", proposal)`
- [ ] Message includes: slug, paper_path, version, summary
- [ ] Unit test: message reaches architect.requests channel

### AR.6 Academic: fetch_version Skill

**Files to create:**
- `agents/academic/skills/fetch_version.go`

**Implementation Guide:**

Quick file search for research paper versions. Defaults to latest if no version specified.

**Acceptance Criteria:**

- [ ] `FetchVersion(slug string, version *int) (*VersionedDocument, error)`
- [ ] `FetchLatest(slug string) (*VersionedDocument, error)` - convenience wrapper
- [ ] `ListVersions(slug string) ([]int, error)`
- [ ] Search path: `~/.sylk/projects/{hash}/research/{slug}/v*.md`
- [ ] Glob pattern finds all version files
- [ ] Returns highest version number when version is nil
- [ ] Returns specific version when version is provided
- [ ] Error when version not found
- [ ] Loads metadata.json alongside content
- [ ] Unit test: fetch latest returns highest version
- [ ] Unit test: fetch specific returns exact version
- [ ] Unit test: non-existent version returns error

### AR.7 Academic: Revision Detection

**Files to create:**
- `agents/academic/revision_detector.go`

**Implementation Guide:**

Academic only revises when user EXPLICITLY requests modification.

**Acceptance Criteria:**

#### Detection Function
- [ ] `detectRevisionIntent(message string) RevisionIntent`
- [ ] `RevisionIntent` struct with: Detected, Action, Target, Description

#### Explicit Patterns (TRIGGERS revision)
- [ ] "update the research paper"
- [ ] "revise the proposal"
- [ ] "add X to the document"
- [ ] "remove X from the paper"
- [ ] "change the recommendation"
- [ ] "modify the edge cases"
- [ ] "I want to update the research"

#### Non-Triggers (does NOT trigger revision)
- [ ] Questions: "What did you find about X?"
- [ ] Acknowledgments: "okay", "interesting", "got it"
- [ ] Clarifications: "Can you explain that?"
- [ ] Discussion: "What about Y approach?"

#### Unit Tests
- [ ] All explicit patterns detected
- [ ] Non-trigger phrases return Detected=false
- [ ] Action correctly extracted (add/remove/modify/replace)

### AR.8 Architect: read_research_paper Skill

**Files to create:**
- `agents/architect/skills/read_research_paper.go`

**Trigger:** Receives PROPOSAL message type on `architect.requests` channel

**Implementation Guide:**

```go
func (s *ReadResearchPaperSkill) Execute(ctx context.Context, params map[string]any) error {
    // 1. Fetch paper via fetch_version
    // 2. Parse structured sections
    // 3. Load decisions
    // 4. Consult Librarian to verify file paths
    // 5. Convert to Plan structure
    // 6. Enter plan mode with draft
    // 7. Present to user
}
```

**Acceptance Criteria:**

#### Skill Registration
- [ ] Skill triggered when message type == "PROPOSAL"
- [ ] Extracts research_slug, paper_path, version from message

#### Paper Parsing
- [ ] `parsePaper(content string) *ParsedPaper` - extracts sections
- [ ] Extracts: title, summary, approaches, recommended, files_to_modify, implementation_steps, acceptance_criteria, edge_cases, open_questions

#### Plan Conversion
- [ ] `convertToPlan(parsed, decisions, verified) *Plan`
- [ ] Implementation steps → PlanSteps with correct indices
- [ ] Layer hints from paper ("Layer 0", "Layer 1") → DependsOn arrays
- [ ] Accepted edge case mitigations → additional PlanSteps
- [ ] Acceptance criteria attached to relevant steps
- [ ] Plan.Metadata["source_research"] = paper slug
- [ ] Plan.Metadata["research_version"] = version

#### Librarian Consultation
- [ ] Verify file paths exist in codebase
- [ ] Identify additional files that may need changes
- [ ] Flag missing files for creation

#### Plan Mode Entry
- [ ] Creates PlanContext with state DRAFTING
- [ ] CurrentPlan populated from conversion
- [ ] Presents plan to user with diff from research

### AR.9 Architect: fetch_version Skill

**Files to create:**
- `agents/architect/skills/fetch_version.go`

**Implementation Guide:**

Identical to Academic's fetch_version but searches plans directory.

**Acceptance Criteria:**

- [ ] `FetchVersion(slug string, version *int) (*VersionedDocument, error)`
- [ ] Search path: `~/.sylk/projects/{hash}/plans/{slug}/v*.md`
- [ ] All same criteria as AR.6 but for plans
- [ ] Unit test: fetch plan version works correctly

### AR.10 Architect: Request Handler Updates

**Files to modify:**
- `agents/architect/handler.go`

**Implementation Guide:**

Update request handler to process PROPOSAL, TASK_MODIFIED_BY_USER, and WORKFLOW_CHANGE_REQUIRED messages.

**Acceptance Criteria:**

#### Message Routing
- [ ] `handleRequest(msg) error` switches on msg.Type
- [ ] `MessageTypeProposal` → `handleProposal(msg)`
- [ ] `MessageTypeTaskModifiedByUser` → `handleUserTaskModification(msg)`
- [ ] `MessageTypeWorkflowChangeRequired` → `handleWorkflowChangeRequest(msg)`

#### Proposal Handling
- [ ] Unmarshals ProposalMessage from payload
- [ ] Triggers read_research_paper skill with params
- [ ] Unit test: PROPOSAL triggers skill execution

#### User Task Modification Handling
- [ ] Unmarshals TaskModifiedByUserMessage
- [ ] Loads current plan
- [ ] Updates step description/criteria as needed
- [ ] Creates new plan version
- [ ] Archives and ingests

#### Workflow Change Request Handling
- [ ] Unmarshals WorkflowChangeRequiredMessage
- [ ] If blocking: pauses orchestrator execution
- [ ] Evaluates impact of proposed change
- [ ] Presents to user for approval
- [ ] If approved: applies change, creates new version, resumes
- [ ] If rejected: notifies requesting agent, resumes without change

### AR.11 Vector DB Incremental Ingestion

**Files to create:**
- `core/vectordb/ingestion.go`
- `core/vectordb/chunking.go`

**Implementation Guide:**

Implement incremental ingestion that only embeds new/changed content.

**Acceptance Criteria:**

#### Document Chunking
- [ ] `chunkDocument(doc) []DocumentChunk` - splits by section
- [ ] `DocumentChunk` struct with: ChunkID, DocumentID, DocumentType, Content, ContentHash, ProjectHash, Slug, Version, Section, Embedding, IngestedAt
- [ ] ChunkID format: `{doc_type}:{slug}:{section}:{index}`
- [ ] Sections: executive_summary, problem_statement, approach_N, edge_case_N, step_N, etc.

#### Hash-Based Diffing
- [ ] `computeHash(content string) string` - SHA256
- [ ] `getDocumentHashes(docID string) (map[string]string, error)` - loads from metadata
- [ ] `updateDocumentHashes(docID string, hashes map[string]string) error` - saves to metadata

#### Incremental Update
- [ ] `IngestDocument(doc *ApprovedDocument) error`
- [ ] Computes current chunk hashes
- [ ] Loads existing hashes
- [ ] Identifies: new chunks, removed chunks, unchanged chunks
- [ ] Only embeds new chunks (saves compute)
- [ ] Deletes vectors for removed chunks
- [ ] Skips unchanged chunks
- [ ] Updates metadata with current hashes

#### Unit Tests
- [ ] First ingestion embeds all chunks
- [ ] Second ingestion with no changes embeds zero chunks
- [ ] Modified section only re-embeds that chunk
- [ ] Removed section deletes corresponding vectors

### AR.12 Archivalist Version Tracking for Research

**Files to modify:**
- `agents/archivalist/research_versions.go` (create)

**Implementation Guide:**

Enable Archivalist to track research paper versions and answer diff queries.

**Acceptance Criteria:**

#### Categories
- [ ] `research_paper_versions` - each version as entry
- [ ] `approved_research_papers` - approved papers with decisions
- [ ] `research_change_sets` - cached diffs

#### Version Storage
- [ ] Each version stored with: slug, version, content, status, tags
- [ ] Change sets computed on demand or cached

#### Query Support
- [ ] "What changed between v2 and v4?" → computes/returns diff
- [ ] "Show version history for paper X" → lists all versions
- [ ] "Find research about caching" → semantic search

### AR.13 Integration Tests

**Files to create:**
- `tests/research_paper_test.go`
- `tests/research_handoff_test.go`

**Acceptance Criteria:**

#### Research Paper Tests
- [ ] Full paper lifecycle: create → revise → approve → archive
- [ ] Version persistence and retrieval
- [ ] Decision tracking
- [ ] Storage structure matches specification

#### Handoff Tests
- [ ] Academic write_research_paper → PROPOSAL → Architect read_research_paper
- [ ] Plan correctly derived from research
- [ ] Edge case mitigations become plan steps
- [ ] Source research linked in plan metadata

#### Vector DB Tests
- [ ] Approved paper ingested correctly
- [ ] Revision only ingests changed chunks
- [ ] Semantic search finds relevant content

#### Revision Trigger Tests
- [ ] Academic only revises on explicit request
- [ ] Architect revises on user request
- [ ] Architect revises on TASK_MODIFIED_BY_USER
- [ ] Architect handles WORKFLOW_CHANGE_REQUIRED correctly

### Research Paper Parallelization Notes

The following tasks can be executed concurrently:

**Layer 0 (No Dependencies):**
- AR.1 Research Paper Types
- AR.3 PROPOSAL Message Type
- AR.4 Revision Message Types

**Layer 1 (Depends on AR.1):**
- AR.2 Research Paper Storage
- AR.7 Academic: Revision Detection

**Layer 2 (Depends on AR.2):**
- AR.5 Academic: write_research_paper Skill
- AR.6 Academic: fetch_version Skill
- AR.9 Architect: fetch_version Skill

**Layer 3 (Depends on AR.3, AR.4, AR.5):**
- AR.8 Architect: read_research_paper Skill
- AR.10 Architect: Request Handler Updates

**Layer 4 (Depends on AR.2, AR.8):**
- AR.11 Vector DB Incremental Ingestion
- AR.12 Archivalist Version Tracking

**Layer 5 (Depends on all above):**
- AR.13 Integration Tests

---

### Knowledge Agent Consultation Protocol (All Agents via Guide)

**Universal mechanism for implementation agents to consult knowledge agents.**

**Files to create/modify:**
- `core/messages/consultation.go` (new) - Consultation message types
- `agents/guide/consultation_router.go` (new) - Routing logic for consultations
- `agents/guide/consultation_cache.go` (new) - Response caching
- All agent files - Add consultation emission helpers

**Acceptance Criteria:**

#### Phase 1: Message Types and Routing

**File: `core/messages/consultation.go`**
- [ ] `ConsultationRequest` struct with type, from_agent, session_id, query, timeout_ms
- [ ] `ConsultationResponse` struct with type, from_agent, to_agent, result, cached, latency_ms
- [ ] `ConsultationQuery` struct with intent, subject, context, specificity
- [ ] Message type constants: CONTEXT_REQUEST, HISTORY_REQUEST, RESEARCH_REQUEST
- [ ] Response type constants: CONTEXT_RESPONSE, HISTORY_RESPONSE, RESEARCH_RESPONSE
- [ ] Validation functions for request/response

**File: `agents/guide/consultation_router.go`**
- [ ] `RouteConsultation(req *ConsultationRequest) (*ConsultationResponse, error)`
- [ ] Route CONTEXT_REQUEST to Librarian
- [ ] Route HISTORY_REQUEST to Archivalist
- [ ] Route RESEARCH_REQUEST to Academic
- [ ] Session validation (verify agent belongs to session)
- [ ] Timeout handling
- [ ] Error wrapping for routing failures

#### Phase 2: Caching and Optimization

**File: `agents/guide/consultation_cache.go`**
- [ ] `ConsultationCache` struct with TTL, max_entries
- [ ] `Get(key string) (*ConsultationResponse, bool)` - Check cache
- [ ] `Set(key string, response *ConsultationResponse)` - Store response
- [ ] Cache key generation from request (agent + intent + subject)
- [ ] TTL-based expiration (configurable per request type)
- [ ] Cache invalidation on relevant changes (file edits, new decisions)
- [ ] Metrics: hit rate, latency reduction

#### Phase 3: Agent Integration

**All Agents - Consultation Emission Helpers:**
- [ ] `EmitContextRequest(subject, context string) (*ConsultationResponse, error)` - Query Librarian
- [ ] `EmitHistoryRequest(subject, context string) (*ConsultationResponse, error)` - Query Archivalist
- [ ] `EmitResearchRequest(subject, context string) (*ConsultationResponse, error)` - Query Academic
- [ ] Request builder with sensible defaults
- [ ] Response parsing helpers
- [ ] Timeout configuration

**Agent-Specific Implementation:**
- [ ] Architect: Already has consultation protocol (verify integration)
- [ ] Engineer: Already has consult_* skills (verify integration)
- [ ] Designer: Add consultation emission to system prompt guidance
- [ ] Inspector: Add consultation emission for pattern validation
- [ ] Tester: Add consultation emission for test pattern lookup
- [ ] Orchestrator: Add consultation emission (rare, for execution context)

#### Phase 4: Testing

**Unit Tests:**
- [ ] Consultation request/response serialization
- [ ] Route determination (request type → knowledge agent)
- [ ] Cache key generation
- [ ] Cache TTL expiration
- [ ] Session validation
- [ ] Timeout handling

**Integration Tests:**
- [ ] End-to-end: Engineer → Guide → Librarian → Guide → Engineer
- [ ] End-to-end: Inspector → Guide → Archivalist → Guide → Inspector
- [ ] End-to-end: Tester → Guide → Academic → Guide → Tester
- [ ] Cache hit scenario (second identical request)
- [ ] Cache invalidation scenario (file changed)
- [ ] Timeout scenario (slow knowledge agent)
- [ ] Session isolation (agent can't query other session's agents)

**Performance Tests:**
- [ ] Consultation latency (< 500ms for cached, < 2s for uncached)
- [ ] Cache hit rate under typical workload
- [ ] Concurrent consultations from multiple agents

---

### Direct Consultation Protocol (Token-Optimized Agent-to-Agent)

**Purpose:** Token-efficient direct communication between agents with known targets, bypassing Guide routing overhead.

**Problem:** When agents consult each other through Guide:
- Guide's context accumulates ALL consultation traffic
- At 100 consultations/session: Guide context grows by 50-100K tokens
- This causes more frequent Guide compaction and higher per-turn costs
- Estimated ~65% token reduction with direct consultation

**Solution:** Direct consultation skills that bypass Guide for known targets, with async logging to Archivalist.

**Files to create/modify:**
- `core/messages/direct_consultation.go` (new) - Direct consultation types
- `core/bus/direct.go` (new) - Direct message bus methods
- `agents/*/consultation.go` (new for each agent) - Direct consultation skills
- `agents/archivalist/consultation_log.go` (new) - Async consultation logging

**Consultation Matrix (Who Can Directly Consult Whom):**

| Agent | Direct Consultation Targets |
|-------|---------------------------|
| Guide | All agents (it's the universal router) |
| Architect | Engineer, Designer, Inspector, Tester, Librarian, Archivalist, Academic |
| Engineer | Architect, Designer, Inspector, Tester, Librarian, Archivalist, Academic |
| Designer | Architect, Engineer, Inspector, Tester, Librarian, Archivalist, Academic |
| Inspector | Architect, Engineer, Designer, Tester, Librarian, Archivalist |
| Tester | Architect, Engineer, Designer, Inspector, Librarian, Archivalist |
| Librarian | Archivalist, Academic |
| Archivalist | Librarian, Academic |
| Academic | Librarian, Archivalist |

**Acceptance Criteria:**

#### Phase 1: Direct Consultation Types

**File: `core/messages/direct_consultation.go`**
- [ ] `ConsultRequest` struct with question, context, priority, max_tokens
- [ ] `ConsultResponse` struct with answer, confidence, references, suggestions
- [ ] `ConsultationLog` struct with from, to, summary (truncated), timestamp, session_id
- [ ] Priority enum: "blocking", "async"
- [ ] Standard consultation parameters template

```go
type ConsultRequest struct {
    Question   string         `json:"question"`
    Context    map[string]any `json:"context,omitempty"`
    Priority   string         `json:"priority,omitempty"`  // "blocking", "async"
    MaxTokens  int            `json:"max_tokens,omitempty"`
}

type ConsultResponse struct {
    Answer      string   `json:"answer"`
    Confidence  float64  `json:"confidence,omitempty"`
    References  []string `json:"references,omitempty"`
    Suggestions []string `json:"suggestions,omitempty"`
}

type ConsultationLog struct {
    From      string    `json:"from"`
    To        string    `json:"to"`
    Summary   string    `json:"summary"`    // Truncated (max 100 chars)
    Timestamp time.Time `json:"timestamp"`
    SessionID string    `json:"session_id"`
}
```

#### Phase 2: Direct Message Bus

**File: `core/bus/direct.go`**
- [ ] `RequestDirect(ctx, target, msg) (*Response, error)` - Direct agent-to-agent
- [ ] Session validation (both agents in same session)
- [ ] Timeout handling
- [ ] Circuit breaker for unavailable targets
- [ ] Fallback to Guide routing if direct fails

```go
func (b *Bus) RequestDirect(ctx context.Context, target string, msg *Message) (*Message, error) {
    // 1. Validate target agent exists and is in same session
    // 2. Send directly to target agent's inbox
    // 3. Wait for response with timeout
    // 4. If fails, optionally fallback to Guide routing
    return response, nil
}
```

#### Phase 3: Async Consultation Logging

**File: `agents/archivalist/consultation_log.go`**
- [ ] `LogConsultation(log *ConsultationLog) error` - Async fire-and-forget
- [ ] Truncate question to 100 chars for minimal storage
- [ ] Category: `consultation_log`
- [ ] Retention policy: 30 days default
- [ ] Query API for consultation analytics

#### Phase 4: Agent Consultation Skills Implementation

**Guide Consultation Skills (`agents/guide/consultation.go`):**
- [ ] `consult_architect` - Task decomposition and workflow questions
- [ ] `consult_engineer` - Implementation questions
- [ ] `consult_designer` - UI/UX questions
- [ ] `consult_inspector` - Code quality questions
- [ ] `consult_tester` - Testing questions
- [ ] `consult_librarian` - Codebase questions
- [ ] `consult_archivalist` - Historical context
- [ ] `consult_academic` - Research and best practices

**Architect Consultation Skills (`agents/architect/consultation.go`):**
- [ ] `consult_engineer` - Implementation feasibility
- [ ] `consult_designer` - UI/UX approach
- [ ] `consult_inspector` - Quality considerations
- [ ] `consult_tester` - Testing strategy
- [ ] `consult_librarian` - Codebase patterns
- [ ] `consult_archivalist` - Historical decisions
- [ ] `consult_academic` - Best practices

**Engineer Consultation Skills (`agents/engineer/consultation.go`):**
- [ ] `consult_architect` - Task clarification
- [ ] `consult_designer` - UI implementation guidance
- [ ] `consult_inspector` - Code quality questions
- [ ] `consult_tester` - Testing approach
- [ ] `consult_librarian` - Codebase patterns
- [ ] `consult_archivalist` - Historical issues
- [ ] `consult_academic` - Best practices

**Designer Consultation Skills (`agents/designer/consultation.go`):**
- [ ] `consult_architect` - Design requirements
- [ ] `consult_engineer` - Implementation feasibility
- [ ] `consult_inspector` - UI quality (token validation)
- [ ] `consult_tester` - Visual/a11y testing
- [ ] `consult_librarian` - Component search
- [ ] `consult_archivalist` - Design history
- [ ] `consult_academic` - UX research

**Inspector Consultation Skills (`agents/inspector/consultation.go`):**
- [ ] `consult_architect` - Validation requirements
- [ ] `consult_engineer` - Implementation clarification
- [ ] `consult_designer` - UI validation questions
- [ ] `consult_tester` - Test coverage questions
- [ ] `consult_librarian` - Codebase patterns
- [ ] `consult_archivalist` - Validation history

**Tester Consultation Skills (`agents/tester/consultation.go`):**
- [ ] `consult_architect` - Testing requirements
- [ ] `consult_engineer` - Implementation details
- [ ] `consult_designer` - UI testing questions
- [ ] `consult_inspector` - Validation coordination
- [ ] `consult_librarian` - Test patterns
- [ ] `consult_archivalist` - Test history

**Librarian Consultation Skills (`agents/librarian/consultation.go`):**
- [ ] `consult_archivalist` - Historical code context
- [ ] `consult_academic` - Pattern research

**Archivalist Consultation Skills (`agents/archivalist/consultation.go`):**
- [ ] `consult_librarian` - Current codebase state
- [ ] `consult_academic` - Research context

**Academic Consultation Skills (`agents/academic/consultation.go`):**
- [ ] `consult_librarian` - Codebase patterns for validation
- [ ] `consult_archivalist` - Historical research outcomes

#### Phase 5: Testing

**Unit Tests:**
- [ ] Direct message routing
- [ ] Session validation for direct messages
- [ ] Consultation log truncation
- [ ] Timeout handling
- [ ] Fallback to Guide routing

**Integration Tests:**
- [ ] Engineer → Designer direct consultation
- [ ] Architect → Librarian direct consultation
- [ ] Inspector → Archivalist direct consultation
- [ ] Consultation logging verification
- [ ] Session isolation (cross-session blocked)

**Performance Tests:**
- [ ] Direct vs Guide-routed latency comparison
- [ ] Token usage comparison (100 consultations)
- [ ] Guide context growth comparison
- [ ] Concurrent direct consultations

**Expected Results:**
- Direct consultation: ~1500 tokens per consultation
- Guide-routed: ~3000+ tokens per consultation (Guide overhead)
- 100 consultations: ~150K (direct) vs ~450K (Guide) tokens
- ~65% token savings on consultation-heavy sessions

---

### Session Recording & Replay (Guide + Archivalist)

**Files to create/modify:**
- `core/recordings/types.go` (new)
- `core/recordings/recorder.go` (new)
- `core/recordings/replayer.go` (new)
- `agents/guide/skills.go`
- `agents/guide/replay.go` (new)
- `agents/archivalist/skills.go`
- `agents/archivalist/recordings.go` (new)

**Parallelization Strategy:**
```
Phase 1 (FIRST - shared dependency):
├── Recording Data Model (core/recordings/types.go)

Phase 2 (PARALLEL after Phase 1):
├── Guide Recording Skills          ─┐
├── Guide Replay Skills              │ Can execute in PARALLEL
├── Archivalist Storage Skills       │
└── Archivalist Query Skills        ─┘

Phase 3 (AFTER Phase 2):
├── Guide ↔ Archivalist Integration
├── Replay Engine
└── End-to-end Testing
```

**Acceptance Criteria:**

#### Phase 1: Recording Data Model (Shared Dependency)
- [ ] `RecordingID` type definition
- [ ] `SessionRecording` struct with ID, name, session_id, timestamps, tags
- [ ] `RecordedQuery` struct with index, timestamp, raw_query, intent, routed_to, response, success
- [ ] `AgentAction` struct for detailed action capture (optional)
- [ ] `ReplayState` struct for tracking replay progress
- [ ] JSON serialization/deserialization

#### Phase 2A: Guide Recording Skills (Parallel)
- [ ] `start_recording` skill implementation
- [ ] `stop_recording` skill with Archivalist storage
- [ ] Recording state management (active recording tracking)
- [ ] Query capture hook in routing pipeline
- [ ] Response capture (optional based on config)
- [ ] Agent action capture (optional based on config)

#### Phase 2B: Guide Replay Skills (Parallel)
- [ ] `replay_session` skill with mode support (exact, interactive, dry_run)
- [ ] `replay_query` skill for single query replay
- [ ] `list_recordings` skill with filtering
- [ ] `get_recording_info` skill
- [ ] Interactive replay controls (continue, skip, modify, abort)
- [ ] Replay state tracking

#### Phase 2C: Archivalist Recording Storage (Parallel)
- [ ] `store_recording` skill implementation
- [ ] `get_recording` skill (by ID or name)
- [ ] Recording persistence (file-based or database)
- [ ] Cross-session recording access

#### Phase 2D: Archivalist Recording Query (Parallel)
- [ ] `query_recordings` skill with filters (tags, session, date, content)
- [ ] `delete_recording` skill
- [ ] `update_recording_metadata` skill
- [ ] Recording search indexing

#### Phase 3: Integration & Engine (After Phase 2)
- [ ] Guide → Archivalist recording storage flow
- [ ] Archivalist → Guide recording retrieval flow
- [ ] Replay engine with query re-injection
- [ ] Modification application during replay
- [ ] Failure handling during replay
- [ ] Progress reporting during replay

#### Testing
- [ ] Unit tests for Recording data model
- [ ] Unit tests for Guide recording skills
- [ ] Unit tests for Guide replay skills
- [ ] Unit tests for Archivalist recording storage
- [ ] Integration test: Record → Store → Retrieve → Replay
- [ ] Integration test: Interactive replay with modifications
- [ ] Integration test: Cross-session recording access

### Tester: Test Execution System

**CRITICAL: Tester EXECUTES tests. Framework detection is Librarian's responsibility.**

**Files to create:**
- `core/test/executor.go` (new) - Test execution
- `core/test/parsers/common.go` (new) - Common parser utilities
- `core/test/parsers/go.go` (new) - Go test output parser
- `core/test/parsers/jest.go` (new) - Jest output parser
- `core/test/parsers/pytest.go` (new) - pytest output parser
- `core/test/parsers/cargo.go` (new) - Cargo test output parser
- `agents/tester/framework.go` (new) - Tester execution skills
- `agents/tester/skills.go` (modify) - Skills registration

**Parallelization Strategy:**
```
Phase 1 (AFTER Librarian Tool Discovery Phase 2):
├── 1A: Common Parser Utilities (core/test/parsers/common.go)  ─┐
├── 1B: Go Output Parser (core/test/parsers/go.go)              │
├── 1C: Jest Output Parser (core/test/parsers/jest.go)          │ PARALLEL
├── 1D: pytest Output Parser (core/test/parsers/pytest.go)      │
└── 1E: Cargo Output Parser (core/test/parsers/cargo.go)       ─┘

Phase 2 (AFTER Phase 1):
└── Test Executor (core/test/executor.go)

Phase 3 (AFTER Phase 2 - Tester integration):
├── Tester Execution Skills (agents/tester/framework.go)  ─┐
└── Tester Skills Registration (agents/tester/skills.go)  ─┘ PARALLEL

Phase 4 (AFTER Phase 3):
└── Integration Testing (Tester ↔ Librarian consultation)
```

**Acceptance Criteria:**

#### Phase 1A: Common Parser Utilities

**File: `core/test/parsers/common.go`**
- [ ] `NormalizeTestResult(raw interface{}, format string) *TestResult`
- [ ] `ExtractStackTrace(output string) string`
- [ ] `ParseDuration(s string) time.Duration` - Handle various formats ("1.23s", "1230ms", "1m23s")
- [ ] `ExtractCoverage(output string) *CoverageResult` - Generic coverage extraction
- [ ] `TestResultFromJUnit(xml []byte) *TestResult` - JUnit XML parser (used by many frameworks)

#### Phase 1B-1E: Output Parsers (Parallel)

**File: `core/test/parsers/go.go`**
- [ ] `ParseGoTestOutput(output []byte) (*TestResult, error)`
- [ ] Handle `-json` flag output (JSONL format)
- [ ] Extract test name, package, status, duration
- [ ] Extract failure details (file, line, message)
- [ ] Extract coverage percentage from `-cover` output

**File: `core/test/parsers/jest.go`**
- [ ] `ParseJestOutput(output []byte) (*TestResult, error)`
- [ ] Handle `--json` flag output
- [ ] Extract test suites, test cases, status
- [ ] Extract failure details with snapshots
- [ ] Extract coverage from `--coverage` output

**File: `core/test/parsers/pytest.go`**
- [ ] `ParsePytestOutput(output []byte) (*TestResult, error)`
- [ ] Handle `--json-report` output (if available)
- [ ] Handle standard pytest output (fallback)
- [ ] Extract test names, status, parametrized test details
- [ ] Extract failure details with pytest traceback

**File: `core/test/parsers/cargo.go`**
- [ ] `ParseCargoTestOutput(output []byte) (*TestResult, error)`
- [ ] Handle `--format json` output
- [ ] Extract test name, module, status
- [ ] Extract failure details with Rust backtrace

#### Phase 2: Test Executor

**File: `core/test/executor.go`**
- [ ] `NewTestExecutor() *TestExecutor`
- [ ] `RunAll(def *TestFrameworkDefinition, ctx *ProjectContext) (*TestResult, error)`:
  - Build command from def.RunCommand
  - Execute with timeout
  - Parse output using def.ParseOutput
  - Return structured result
- [ ] `RunFile(def *TestFrameworkDefinition, ctx *ProjectContext, filePath string) (*TestResult, error)`:
  - Build command from def.RunFileCommand with $FILE placeholder
  - Execute and parse
- [ ] `RunSingle(def *TestFrameworkDefinition, ctx *ProjectContext, filePath string, testName string) (*TestResult, error)`:
  - Build command with $FILE and $TEST placeholders
  - Execute single test
- [ ] `RunWithCoverage(def *TestFrameworkDefinition, ctx *ProjectContext) (*TestResult, error)`:
  - Use def.CoverageCommand
  - Parse coverage output
  - Include coverage in result
- [ ] `Watch(def *TestFrameworkDefinition, ctx *ProjectContext) (<-chan *TestResult, error)`:
  - Start watch process if def.WatchCommand available
  - Stream results via channel
  - Handle process restart on changes
- [ ] `RunChanged(def *TestFrameworkDefinition, ctx *ProjectContext, gitRef string) (*TestResult, error)`:
  - Detect changed test files via git diff
  - Run only affected tests
- [ ] Command placeholder replacement ($FILE, $TEST, $LINE, $DIR)
- [ ] Working directory set to project root
- [ ] Environment variable injection (TEST_ENV vars)
- [ ] Timeout handling with graceful process termination
- [ ] stdout/stderr capture and merging

#### Phase 3: Tester Execution Skills (Consult Librarian First)

**File: `agents/tester/framework.go`**
- [ ] `run_tests_smart` skill implementation
  - [ ] Accept optional `framework` param (TestFrameworkDefinition from Librarian)
  - [ ] If not provided, consult Librarian via Guide: `detect_test_framework`
  - [ ] Cache Librarian response per project root
  - [ ] Execute tests with provided definition
  - [ ] Report result to Archivalist
  - [ ] Support scope: "all", "file", "single", "changed"
- [ ] `run_tests_with_coverage` skill
  - [ ] Consult Librarian for framework if not cached
  - [ ] Execute with coverage command
  - [ ] Parse and report coverage results
- [ ] `watch_tests` skill
  - [ ] Consult Librarian for framework
  - [ ] Start watch mode with streaming results
- [ ] `run_changed_tests` skill
  - [ ] Consult Librarian for framework
  - [ ] Execute only changed tests
- [ ] `find_test_files` skill
  - [ ] Consult Librarian for TestFilePatterns
  - [ ] Find matching files in project
- [ ] `get_test_for_file` skill
  - [ ] Consult Librarian for naming conventions
  - [ ] Suggest test file path for source file
- [ ] **Librarian consultation protocol** implementation
  - [ ] Cache invalidation on Librarian signal
  - [ ] Fallback error handling if Librarian unavailable

**File: `agents/tester/skills.go`**
- [ ] Import framework skills from `agents/tester/framework.go`
- [ ] Register all test execution skills in Tester's skill registry
- [ ] Update Tester system prompt to include Librarian consultation requirement
- [ ] Add test framework preference to user config

#### Phase 4: Testing (Tester Execution + Consultation)

**Unit Tests:**
- [ ] Common parser utilities
- [ ] Go test output parser with sample outputs
- [ ] Jest JSON output parser with sample outputs
- [ ] pytest output parser with sample outputs
- [ ] Cargo test output parser with sample outputs
- [ ] Executor command building with placeholder replacement
- [ ] Executor timeout handling
- [ ] Tester → Librarian consultation flow (mocked)
- [ ] Cache behavior and invalidation

**Integration Tests:**
- [ ] End-to-end: Librarian detects Go framework → Tester executes → parses
- [ ] End-to-end: Librarian detects Jest → Tester executes → parses
- [ ] End-to-end: Librarian detects pytest → Tester executes → parses
- [ ] End-to-end: Librarian detects Cargo → Tester executes → parses
- [ ] Multi-framework project (e.g., Go + JS) with Librarian detection
- [ ] Watch mode start/stop
- [ ] Coverage extraction and reporting
- [ ] Run changed tests with git diff
- [ ] Consultation failure handling (Librarian unavailable)

**Performance Tests:**
- [ ] Large test suite output parsing (1000+ tests)
- [ ] Concurrent test runs across multiple frameworks
- [ ] Watch mode responsiveness

---

## Phase 0: Infrastructure Foundation

**Goal**: Build shared infrastructure that all agents depend on.

**Dependencies**: None (can start immediately)

**Parallelization**:
- **Batch 1** (no deps): 0.1, 0.2, 0.3, 0.4, 0.5, 0.6 can execute in parallel
- **Batch 2** (no deps): 0.7, 0.8, 0.9, 0.10 can execute in parallel (LLM adapters, config, queue, rate limit)
- **Batch 3** (after 0.10): 0.11 Signal Bus (needed for rate limit integration)
- **Batch 4** (after 0.11): 0.12 Agent Signal Handler
- **Batch 5** (after 0.7): 0.13, 0.14 can execute in parallel (budget, context)
- **Batch 6** (after all): 0.15 LLM Gate integration, 0.16 Usage CLI

### 0.1 Session Manager

Creates and manages isolated sessions with their own state.

**Files to create:**
- `core/session/types.go`
- `core/session/session.go`
- `core/session/manager.go`
- `core/session/context.go`
- `core/session/persistence.go`
- `core/session/session_test.go`

**Acceptance Criteria:**

#### Session Lifecycle
- [ ] `Session` struct with unique ID, creation time, state, metadata
- [ ] Session state enum: `Created`, `Active`, `Paused`, `Suspended`, `Completed`, `Failed`
- [ ] `SessionManager` with `Create()`, `Get()`, `List()`, `Switch()`, `Pause()`, `Resume()`, `Close()` methods
- [ ] Thread-safe operations via sharded map
- [ ] Maximum concurrent sessions limit with backpressure
- [ ] Graceful shutdown with child resource cleanup

#### Session Context
- [ ] `SessionContext` holds all session-scoped state
- [ ] Isolated Guide instance per session (or session-scoped routing)
- [ ] Isolated agent registrations per session
- [ ] Session-scoped route cache
- [ ] Session-scoped pending requests
- [ ] Session-scoped workflow state (current DAG, phase, progress)

#### Session Switching
- [ ] `Switch(sessionID)` pauses current session, activates target
- [ ] Context preservation on pause (serialize state)
- [ ] Context restoration on resume (deserialize state)
- [ ] Active session tracking (only one active per user)

#### Session Persistence
- [ ] Save session state to disk on pause/suspend
- [ ] Restore session state from disk on resume
- [ ] Session metadata persistence (name, description, branch, timestamps)
- [ ] Automatic session save on graceful shutdown

#### Session Isolation Boundaries
- [ ] Session-local: Guide routing state, agent instances, workflow state, route cache
- [ ] Session-shared (read-only): Archivalist historical DB, Librarian index, Academic sources
- [ ] No context pollution between sessions

**Tests:**
- [ ] Create 100 sessions concurrently, verify isolation
- [ ] Switch between sessions, verify context preservation
- [ ] Pause/resume session, verify state restored
- [ ] Concurrent access to shared Archivalist, verify no pollution

```go
// Required interfaces
type SessionManager interface {
    Create(ctx context.Context, cfg SessionConfig) (*Session, error)
    Get(id string) (*Session, bool)
    GetActive() (*Session, bool)
    List() []*Session
    Switch(id string) error
    Pause(id string) error
    Resume(id string) error
    Close(id string) error
    CloseAll() error
    Stats() SessionManagerStats
}

type SessionContext interface {
    ID() string
    Guide() *guide.Guide
    Bus() guide.EventBus
    State() SessionState
    Metadata() map[string]any
    SetMetadata(key string, value any)
    Serialize() ([]byte, error)
    Restore(data []byte) error
}
```

### 0.2 DAG Engine (DONE)

Executes directed acyclic graph workflows with parallel layer execution.

**Files to create:**
- `core/dag/types.go`
- `core/dag/node.go`
- `core/dag/dag.go`
- `core/dag/validator.go`
- `core/dag/executor.go`
- `core/dag/scheduler.go`
- `core/dag/dag_test.go`

**Acceptance Criteria:**

#### DAG Structure
- [x] `DAG` struct with nodes, edges, execution order, policy
- [x] `DAGNode` with Name (human-readable key, e.g. `create_dashboard`), agent type, prompt, context, dependencies, metadata
- [x] **Node names are human-readable, snake_case, unique within DAG** (generated by Architect)
- [x] `execution_order` as layered array (each layer runs in parallel)
- [x] Policy: `max_concurrency`, `fail_fast`, `default_retry`

#### Node State Machine
- [x] Node states: `Pending`, `Queued`, `Running`, `Succeeded`, `Failed`, `Blocked`, `Skipped`, `Cancelled`
- [x] State transitions with validation
- [x] State change events for observability

#### Validation
- [x] Topological sort validation (reject cycles)
- [x] Dependency validation (all deps exist)
- [ ] Agent type validation (agent exists)

#### Execution
- [x] `DAGExecutor` executes layer-by-layer with bounded concurrency
- [x] Dependency resolution (node starts only when all deps succeed)
- [x] Result propagation from parent nodes to dependent nodes
- [x] `fail_fast` policy support (stop on first failure)
- [x] `continue_on_failure` policy support (mark deps as skipped)
- [x] Per-node timeout with cancellation
- [x] Per-node retry with backoff
- [x] Cancellation support (cancel all pending/running nodes)

#### Context Propagation
- [x] Node receives context from completed dependencies
- [x] Output artifacts from parent nodes available to children
- [ ] Session context available to all nodes

**Tests:**
- [x] Execute 5-layer DAG with 20 nodes, verify correct ordering
- [x] Test fail_fast with mid-DAG failure
- [x] Test continue_on_failure with skipped nodes
- [x] Test cancellation mid-execution

```go
// Required interfaces
type DAGExecutor interface {
    Execute(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error)
    Cancel() error
    Status() *DAGStatus
    Subscribe(handler DAGEventHandler) func()
}

type NodeDispatcher interface {
    Dispatch(ctx context.Context, node *DAGNode, parentResults map[string]*NodeResult) (*NodeResult, error)
}

type DAGEventHandler func(event *DAGEvent)
```

### 0.3 Worker Pool Enhancements (DONE)

Extend existing worker pool for multi-session fair scheduling.

**Files to modify:**
- `agents/guide/worker_pool.go`

**Files to create:**
- `core/pool/priority_pool.go`
- `core/pool/session_pool.go`
- `core/pool/fairness.go`
- `core/pool/pool_test.go`

**Acceptance Criteria:**

#### Priority Pool
- [x] `PriorityWorkerPool` with priority lanes (Critical, High, Normal, Low, Background)
- [x] Priority-based job selection (higher priority first)
- [x] Job stealing between priority lanes when idle
- [x] Starvation prevention (age-based promotion)

#### Session-Aware Pool
- [x] `SessionAwarePool` with fair scheduling across sessions
- [x] Per-session job limits (prevent session starvation)
- [x] Per-session job queues
- [x] Round-robin or weighted-fair selection across sessions
- [x] Session job counts and wait times

#### Global Limits
- [x] Global concurrency cap across all sessions
- [x] Per-agent-type concurrency limits (e.g., max 10 Engineers)
- [x] Dynamic limit adjustment based on load

#### Metrics
- [x] Jobs per session
- [x] Wait time per priority lane
- [x] Processing time distribution
- [x] Queue depths

**Tests:**
- [x] Submit 1000 jobs from 10 sessions, verify fair distribution
- [x] Verify priority ordering
- [x] Verify starvation prevention

### 0.4 Guide Enhancements

Extend Guide for multi-session support and new message types.

**Files to modify:**
- `agents/guide/guide.go`
- `agents/guide/types.go`
- `agents/guide/routing.go`
- `agents/guide/bus.go`
- `agents/guide/route_cache.go`

**Files to create:**
- `agents/guide/session_router.go`
- `agents/guide/message_types.go`
- `agents/guide/skills.go`

**Acceptance Criteria:**

#### Session-Aware Routing
- [ ] `SessionRouter` wraps Guide with session context
- [ ] Session ID in all route requests
- [ ] Session-scoped agent registration
- [ ] Session-scoped route cache (with global fallback)
- [ ] Session-scoped pending request store

#### New Message Types
- [ ] `DAG_EXECUTE` - Architect → Orchestrator
- [ ] `DAG_STATUS` - Orchestrator → Architect
- [ ] `DAG_CANCEL` - User → Orchestrator
- [ ] `TASK_DISPATCH` - Orchestrator → Engineer
- [ ] `TASK_COMPLETE` - Engineer → Orchestrator
- [ ] `TASK_FAILED` - Engineer → Orchestrator
- [ ] `TASK_HELP` - Engineer → Orchestrator
- [ ] `CLARIFICATION_REQUEST` - Architect → User
- [ ] `CLARIFICATION_RESPONSE` - User → Architect
- [ ] `VALIDATE_TASK` - Orchestrator → Inspector
- [ ] `VALIDATION_RESULT` - Inspector → Orchestrator
- [ ] `VALIDATION_FULL` - Inspector → User
- [ ] `VALIDATION_CORRECTIONS` - Inspector → Architect
- [ ] `TEST_PLAN_REQUEST` - Architect → Tester
- [ ] `TEST_PLAN_RESPONSE` - Tester → User
- [ ] `TEST_DAG_REQUEST` - Tester → Architect
- [ ] `TESTS_READY` - Orchestrator → Tester
- [ ] `TEST_RESULTS` - Tester → User
- [ ] `TEST_CORRECTIONS` - Tester → Architect
- [ ] `USER_OVERRIDE` - User → Inspector/Tester
- [ ] `USER_INTERRUPT` - User → Architect
- [ ] `WORKFLOW_COMPLETE` - Architect → User
- [ ] `REROUTE_REQUEST` - Agent → Guide: not my domain, reroute this

#### Session-Scoped Topics
- [ ] Topic pattern: `session.{id}.{agent}.requests`
- [ ] Global topics for cross-session (e.g., `archivalist.query`)
- [ ] Topic routing based on session context

#### Intent Classification for Librarian Routing

**CRITICAL**: When routing to Librarian, the Guide extracts additional metadata to enable intent-aware caching - at **zero additional token cost** (same LLM call as routing).

**Files to add:**
- `agents/guide/librarian_intent.go`

**Acceptance Criteria:**

##### Intent Types for Librarian Queries
- [ ] `QueryIntent` enum: `LOCATE`, `PATTERN`, `EXPLAIN`, `GENERAL`
- [ ] Classification happens during routing (no extra LLM call)
- [ ] Subject/concept extraction (e.g., "auth code" → "authentication")

| Intent | Trigger Patterns | Example |
|--------|-----------------|---------|
| LOCATE | "where is", "find", "show me", "which file" | "where is the auth code" |
| PATTERN | "strategy", "approach", "pattern", "how do we" | "what is our caching strategy" |
| EXPLAIN | "how does", "explain", "what does X do" | "how does CreateSession work" |
| GENERAL | Other codebase questions | "what languages are used" |

##### Routing Prompt Enhancement
- [ ] Add LibrarianRoutingInstructions to Guide's system prompt
- [ ] Extract intent and subject during Librarian routing
- [ ] Include confidence score in metadata

```go
// Added to Guide's routing prompt
const LibrarianRoutingInstructions = `
When routing to Librarian, also classify the query:

Intent (required):
- LOCATE: User wants to find where something is (file, function, struct)
- PATTERN: User asks about patterns, strategies, approaches, conventions
- EXPLAIN: User wants to understand how something works
- GENERAL: Other codebase questions

Subject (required): Primary entity/concept being asked about.
Normalize to snake_case (e.g., "auth code" → "authentication")

Examples:
- "where is the auth middleware" → LOCATE, "auth_middleware"
- "what is our caching strategy" → PATTERN, "caching"
- "how does CreateSession work" → EXPLAIN, "create_session"
`
```

##### Routed Message Enhancement
- [ ] `LibrarianRoutingMetadata` added to messages routed to Librarian
- [ ] Metadata includes: Intent, Subject, Confidence

```go
type LibrarianRoutingMetadata struct {
    Intent     QueryIntent `json:"intent"`
    Subject    string      `json:"subject"`
    Confidence float64     `json:"confidence"`
}

// Message to Librarian includes metadata
type LibrarianRequest struct {
    Query       string                    `json:"query"`
    SessionID   string                    `json:"session_id"`
    Metadata    *LibrarianRoutingMetadata `json:"metadata,omitempty"`
}
```

##### Token Cost Analysis
```
WITHOUT Intent Classification:
  Guide: ~200 tokens (routing only)
  Librarian: ~3000 tokens (every query)
  TOTAL: ~3200 tokens per query

WITH Intent Classification (cache hit):
  Guide: ~250 tokens (routing + intent, same call)
  Librarian: 0 tokens (cache hit)
  TOTAL: ~250 tokens per query

At 70% cache hit rate:
  100 queries old: 320,000 tokens
  100 queries new: 115,000 tokens
  SAVINGS: 64%
```

**Tests:**
- [ ] Test intent classification for LOCATE queries
- [ ] Test intent classification for PATTERN queries
- [ ] Test intent classification for EXPLAIN queries
- [ ] Test intent classification fallback to GENERAL
- [ ] Test subject extraction normalization
- [ ] Test confidence scoring
- [ ] Verify no additional LLM call (same as routing)

#### Guide Skills (Progressive Disclosure)

**Core Skills (always loaded):**
- [ ] `route` skill - classify and route requests (supports @to:agent syntax)
- [ ] `guide_route` skill - route prompt to specific agent via @guide:<agent> tag
- [ ] `task_interact` skill - route user message to specific pipeline via /task command (see Phase 2.3)
- [ ] `help` skill - provide routing help
- [ ] `status` skill - system status
- [ ] `agents` skill - list registered agents
- [ ] `route_to` skill - route message to another agent (common skill)
- [ ] `reply_to` skill - reply to routed message (common skill)
- [ ] `broadcast` skill - broadcast to multiple agents (common skill)

**@guide:<agent> Routing:**
```go
// guide_route - Route prompt to specific agent via @guide:<agent> tag
skills.NewSkill("guide_route").
    Description("Route a prompt directly to a specific agent via @guide:<agent> tag").
    Domain("routing").
    Keywords("@guide", "route", "direct", "agent").
    EnumParam("agent", "Target agent", []string{
        "academic",
        "architect",
        "librarian",
        "archivalist",
        "tester",
        "inspector",
    }, true).
    StringParam("prompt", "Prompt to route to the agent", true).
    ObjectParam("context", "Additional context to include", false)

// Example usage:
// @guide:architect "plan a new authentication feature"
// @guide:librarian "find all files using the database package"
// @guide:academic "research best practices for rate limiting"
// @guide:archivalist "find similar past decisions"
// @guide:tester "run tests for the auth module"
// @guide:inspector "validate the new changes"
```

**Extended Skills (loaded on demand):**
- [ ] `sessions` skill - list sessions
- [ ] `metrics` skill - routing metrics
- [ ] `switch_session` skill - switch active session
- [ ] `create_session` skill - create new session
- [ ] `close_session` skill - close session

#### Guide Tools
```go
var GuideTools = []ToolDefinition{
    // Core routing
    {Name: "guide_route", Skill: "route"},
    {Name: "guide_agent_route", Skill: "guide_route"},  // @guide:<agent> routing
    {Name: "guide_task_interact", Skill: "task_interact"},  // /task command for pipelines
    {Name: "guide_help", Skill: "help"},
    {Name: "guide_status", Skill: "status"},
    {Name: "guide_agents", Skill: "agents"},
    // Session management
    {Name: "guide_sessions", Skill: "sessions"},
    {Name: "guide_switch_session", Skill: "switch_session"},
    {Name: "guide_create_session", Skill: "create_session"},
    {Name: "guide_close_session", Skill: "close_session"},
    // Metrics
    {Name: "guide_metrics", Skill: "metrics"},
    // Routing (common skills)
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "broadcast", Skill: "broadcast"},
}
```

#### Intent Gate Classification (Phase 0)

**CRITICAL**: Before routing ANY request, Guide classifies intent to ensure appropriate handling.

**Files to create:**
- `agents/guide/intent_gate.go`
- `agents/guide/validation.go`

**Acceptance Criteria:**

##### Step 0: Role-Aware Skill Decision (Guide's Implementation)
**CRITICAL: Guide implements the universal Agent Step 0 for routing decisions.**

Guide asks: "Given I'm the router and this request, SHOULD I invoke my skills directly, or should I classify and route to another agent?"

- [ ] Receive request (user request or agent message)
- [ ] Evaluate against Guide's role (router, session management)
- [ ] Evaluate against Guide's domain (routing, session commands, system commands)
- [ ] **YES, my domain** → Execute skill directly (session/status/system commands)
- [ ] **NO, needs routing** → Continue to Step 1 (Request Type Classification)

| Skill Category | Examples | Behavior |
|----------------|----------|----------|
| Session commands | "/session new", "/session list", "/status" | Execute immediately |
| Workflow commands | "/plan", "/interrupt", "/approve" | Execute immediately |
| GitHub commands | "/pr", "/issue", "/commit" | Execute immediately |
| Direct queries | "what is [X]", "show me [X]" | Execute immediately |

**Why role-aware (not just keyword matching):**
- Guide considers its role as router before deciding
- Skills are deterministic - no need for LLM classification
- Faster response time for known commands
- Reduces token usage by skipping classification for clear intents

##### Step 1: Request Type Classification
- [ ] `RequestType` enum: `TRIVIAL`, `EXPLICIT`, `EXPLORATORY`, `OPEN_ENDED`, `GITHUB_WORK`, `AMBIGUOUS`
- [ ] Classification happens ONLY if no skill match in Step 0
- [ ] Fast-path for TRIVIAL requests (skip Architect)

| Type | Trigger Patterns | Routing |
|------|-----------------|---------|
| TRIVIAL | Single-line fix, typo | Fast-path to Engineer |
| EXPLICIT | User specified exact approach | Architect with locked approach |
| EXPLORATORY | "How would I...", research | Academic/Librarian first |
| OPEN_ENDED | Ambiguous scope | Architect for clarification |
| GITHUB_WORK | PR, issue, commit mentioned | Full cycle expected |
| AMBIGUOUS | Cannot classify confidently | Ask clarification |

##### Step 2: Validation Checks
- [ ] Check if assumptions are explicit or implicit
- [ ] Check if scope is bounded or unbounded
- [ ] Check if request matches skill patterns
- [ ] Check if knowledge agents should be consulted first
- [ ] Reject routing if confidence < 0.7

##### Step 3: Pre-Routing Consultation
- [ ] For implementation requests: Query Librarian for codebase health
- [ ] For novel approaches: Query Archivalist for failure patterns
- [ ] Attach enriched context to routed message

##### Step 4: Handle REROUTE_REQUESTs from Agents
**When an agent's Step 0 determines a request is not in its domain, it sends a REROUTE_REQUEST to Guide.**

- [ ] Receive `REROUTE_REQUEST` from agent
- [ ] Parse reason and suggested_target from request
- [ ] Track reroute count for this request_id
- [ ] **First reroute**: Normal, route to suggested_target or re-classify
- [ ] **Second reroute**: Warning logged, likely ambiguous request
- [ ] **Third+ reroute**: STOP, ask user for clarification
- [ ] Maintain reroute_history: `{from, reason, to}` for each hop
- [ ] User clarification message explains: "Routed to [agent1] and [agent2], neither could handle. [reasons]. Can you clarify?"

```go
type RerouteTracking struct {
    RequestID      string          `json:"request_id"`
    RerouteCount   int             `json:"reroute_count"`
    RerouteHistory []RerouteHop    `json:"reroute_history"`
    Action         string          `json:"action"` // "REROUTE" or "ASK_USER_CLARIFICATION"
}

type RerouteHop struct {
    From   string `json:"from"`
    Reason string `json:"reason"`
    To     string `json:"to"`
}
```

##### Guide System Prompt
```go
const GuideSystemPrompt = `
You are the Guide agent. You are the universal message router for Sylk.
ALL messages between agents flow through you.

REQUEST PROCESSING PROTOCOL:

STEP 0: SKILL MATCHING (BLOCKING - do this FIRST)
Check if request matches a registered skill pattern:
- Session commands: "/session new", "/session list", "/status"
- Workflow commands: "/plan", "/interrupt", "/approve"
- GitHub commands: "/pr", "/issue", "/commit"
- Direct queries: "what is [X]", "show me [X]"

If skill matches with confidence > 0.9, execute skill directly and SKIP classification.
Skills are deterministic and don't need LLM classification overhead.

STEP 1: CLASSIFY REQUEST TYPE (only if no skill match)
- TRIVIAL: Single-line fix, typo → Fast-path to Engineer
- EXPLICIT: User specified approach → Architect with locked approach
- EXPLORATORY: Research question → Academic/Librarian first
- OPEN_ENDED: Ambiguous scope → Architect for clarification
- GITHUB_WORK: PR/issue mentioned → Full cycle expected
- AMBIGUOUS: Cannot classify → Ask clarification

STEP 2: VALIDATION CHECKS
1. Are assumptions explicit?
2. Is scope bounded?
3. Is there enough context to route confidently?

STEP 3: PRE-ROUTING CONSULTATION
For implementation requests, BEFORE routing to Architect:
- Librarian: "What is codebase health for [target area]?"
- Archivalist: "Any failure patterns for [approach]?"
Attach responses to routed message as enriched context.

PRE-ROUTING CONSULTATIONS (for implementation):
- Librarian: "Codebase health for target area?"
- Archivalist: "Failure patterns for approach?"
`
```

**Tests:**
- [ ] Test TRIVIAL classification and fast-path
- [ ] Test EXPLICIT classification
- [ ] Test EXPLORATORY routing to Academic/Librarian
- [ ] Test AMBIGUOUS triggers clarification
- [ ] Test GITHUB_WORK triggers full cycle
- [ ] Test pre-routing consultation triggers
- [ ] Test confidence threshold rejection

#### Guide Memory Management

**Thresholds**: 50%, 75%, 90% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/guide/memory.go`
- `agents/guide/checkpoint.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger checkpoint at 50%, 75%, 90%
- [ ] Trigger compaction at 95%

##### Checkpoint Summaries (Routing Knowledge)
- [ ] `GuideSummary` struct with routing information:
  - [ ] KnownRoutings (pattern → agent mapping)
  - [ ] FrequentMatches (common routes with confidence)
  - [ ] FailedRoutings (routes that didn't work)
  - [ ] AgentCapabilities (observed capabilities per agent)
  - [ ] RequestPatterns (common request types)
  - [ ] SessionRoutingStats
- [ ] Submit checkpoint to Archivalist with category `guide_checkpoint`

```go
type GuideSummary struct {
    Timestamp           time.Time                `json:"timestamp"`
    SessionID           string                   `json:"session_id"`
    ContextUsage        float64                  `json:"context_usage"`
    CheckpointIndex     int                      `json:"checkpoint_index"`
    KnownRoutings       map[string]string        `json:"known_routings"`
    FrequentMatches     []RoutingMatch           `json:"frequent_matches"`
    FailedRoutings      []FailedRouting          `json:"failed_routings"`
    AgentCapabilities   map[string][]string      `json:"agent_capabilities"`
    RequestPatterns     []RequestPattern         `json:"request_patterns"`
    SessionRoutingStats RoutingStats             `json:"session_routing_stats"`
}
```

##### Compaction at 95%
- [ ] Create final checkpoint before compacting
- [ ] Clear verbose routing logs from context
- [ ] Retain merged routing knowledge
- [ ] Target: ~30% context usage after compaction

#### Guide Hooks
- [ ] Pre-route hook: inject session context
- [ ] Post-route hook: update session state
- [ ] Pre-dispatch hook: validate target agent health
- [ ] Post-dispatch hook: record routing decision
- [ ] Context-threshold hook: trigger checkpoint at 50%, 75%, 90%
- [ ] Context-threshold hook: trigger compaction at 95%

**Tests:**
- [ ] Route requests with session context
- [ ] Verify session isolation in route cache
- [ ] Test new message types round-trip
- [ ] Test skill loading on demand
- [ ] Test checkpoint creation at 50%, 75%, 90%
- [ ] Test compaction at 95%
- [ ] Test routing knowledge preservation after compaction

#### Intent Preservation & Confidence Propagation

**CRITICAL**: Prevent information loss as requests flow through multiple agents. The message envelope carries all state (Guide remains stateless). Each agent reports confidence, and cumulative confidence = product of all confidences.

**Files to modify:**
- `agents/guide/types.go` (Message envelope extensions)
- `agents/guide/guide.go` (StructuredIntent extraction at intake)
- All agent base types (confidence reporting)

**Files to create:**
- `core/messages/intent.go` (StructuredIntent, ConfidenceEntry, RerouteHop types)

##### Message Envelope Extensions

**Acceptance Criteria:**
- [ ] `StructuredIntent` struct added to Message envelope:
  - [ ] `OriginalRequest` - Verbatim user request (bounded to 500 chars)
  - [ ] `KeyConstraints` - Extracted constraints (e.g., ["must use existing auth", "no new deps"])
  - [ ] `SuccessCriteria` - What constitutes success (e.g., ["tests pass", "handles edge case X"])
  - [ ] `IntentType` - Classified intent type (CREATE, MODIFY, FIX, EXPLAIN, RESEARCH)
- [ ] `ConfidenceChain` slice added to Message envelope:
  - [ ] Each `ConfidenceEntry` contains: Agent, Confidence (0.0-1.0), Reason
  - [ ] Chain is append-only (agents only add, never modify previous entries)
- [ ] `RerouteHistory` slice added to Message envelope:
  - [ ] Each `RerouteHop` contains: From, Reason, To
  - [ ] History tracks all reroutes for user escalation decisions

```go
// StructuredIntent captures the original user request in a structured format.
// Carried in Message envelope throughout request lifecycle.
type StructuredIntent struct {
    OriginalRequest string   `json:"original_request"` // Verbatim (bounded)
    KeyConstraints  []string `json:"key_constraints"`  // Extracted constraints
    SuccessCriteria []string `json:"success_criteria"` // What constitutes success
    IntentType      string   `json:"intent_type"`      // CREATE, MODIFY, FIX, EXPLAIN, RESEARCH
}

// ConfidenceEntry records an agent's confidence after processing.
type ConfidenceEntry struct {
    Agent      string  `json:"agent"`      // Agent ID
    Confidence float64 `json:"confidence"` // 0.0 - 1.0
    Reason     string  `json:"reason"`     // Brief explanation
}

// RerouteHop records a reroute event (agent rejected request).
type RerouteHop struct {
    From   string `json:"from"`   // Agent that rejected
    Reason string `json:"reason"` // Why it rejected
    To     string `json:"to"`     // Where it was rerouted
}
```

##### Guide: StructuredIntent Extraction at Request Intake

**Acceptance Criteria:**
- [ ] Guide extracts `StructuredIntent` during initial routing (same LLM call, zero extra cost)
- [ ] Extraction prompt added to Guide's routing instructions
- [ ] `OriginalRequest` bounded to 500 characters (truncate with ellipsis)
- [ ] At least one `KeyConstraint` and one `SuccessCriterion` extracted
- [ ] `IntentType` classified from: CREATE, MODIFY, FIX, EXPLAIN, RESEARCH
- [ ] StructuredIntent attached to outgoing message before routing

```go
// Added to Guide's routing prompt
const StructuredIntentExtraction = `
When processing a request, also extract structured intent:

IntentType (required): CREATE, MODIFY, FIX, EXPLAIN, RESEARCH
KeyConstraints: Any explicit constraints (max 5)
SuccessCriteria: What would satisfy the request (max 3)

Examples:
- "Add logout button without changing auth flow"
  → IntentType: CREATE
  → KeyConstraints: ["no changes to auth flow"]
  → SuccessCriteria: ["logout button visible", "user session terminated on click"]

- "Fix the race condition in worker pool"
  → IntentType: FIX
  → KeyConstraints: []
  → SuccessCriteria: ["race condition resolved", "tests pass with -race flag"]
`
```

##### Agent: Confidence Reporting

**Acceptance Criteria:**
- [ ] All agents append `ConfidenceEntry` to message when responding
- [ ] Confidence reflects quality of agent's contribution:
  - 0.90-1.00: High confidence, clear and complete
  - 0.70-0.89: Good confidence, minor uncertainties
  - 0.50-0.69: Moderate confidence, notable gaps
  - 0.30-0.49: Low confidence, significant assumptions
  - 0.00-0.29: Very low, mostly speculation
- [ ] `Reason` field explains confidence (e.g., "exact match found", "inferred from context")
- [ ] Agents do NOT modify previous entries in ConfidenceChain
- [ ] Confidence reporting happens in agent's `ProcessResponse()` method

```go
// Example: Librarian appending confidence
func (l *Librarian) ProcessResponse(ctx context.Context, msg *Message, result *QueryResult) *Message {
    entry := ConfidenceEntry{
        Agent:      "librarian",
        Confidence: result.Confidence,
        Reason:     result.MatchType, // "exact_match", "semantic_match", "no_match"
    }
    msg.ConfidenceChain = append(msg.ConfidenceChain, entry)
    return msg
}
```

##### Requesting Agent: Response Evaluation & Escalation

**Acceptance Criteria:**
- [ ] Requesting agent (e.g., Architect) evaluates response from knowledge agent
- [ ] Cumulative confidence calculated as product: `conf1 * conf2 * ... * confN`
- [ ] If cumulative confidence < 0.5, agent MAY escalate to user
- [ ] If response does not satisfy StructuredIntent criteria, agent MAY query again or escalate
- [ ] Escalation via `CLARIFICATION_REQUEST` message through Guide
- [ ] Escalation message includes: what was tried, why it failed, specific question

```
                              RESPONSE EVALUATION FLOW
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│   Agent receives response from knowledge agent (via Guide)                      │
│                          │                                                      │
│                          ▼                                                      │
│   ┌──────────────────────────────────────────────────────────────────────┐      │
│   │ 1. Extract response's ConfidenceEntry                                │      │
│   │ 2. Calculate cumulative confidence = product of all entries          │      │
│   │ 3. Check if response satisfies StructuredIntent.SuccessCriteria      │      │
│   └──────────────────────────────────────────────────────────────────────┘      │
│                          │                                                      │
│            ┌─────────────┴─────────────┐                                        │
│            ▼                           ▼                                        │
│   ┌─────────────────┐        ┌─────────────────────────┐                        │
│   │ Satisfactory    │        │ Unsatisfactory          │                        │
│   │ cumulative>0.5  │        │ cumulative<=0.5 OR      │                        │
│   │ criteria met    │        │ criteria not met        │                        │
│   └────────┬────────┘        └───────────┬─────────────┘                        │
│            │                             │                                      │
│            ▼                             ▼                                      │
│   Continue with task           Check RerouteHistory.length                      │
│                                          │                                      │
│                          ┌───────────────┴───────────────┐                      │
│                          ▼                               ▼                      │
│                  len < 3: Try again           len >= 3: Escalate to user        │
│                  (different query)            via CLARIFICATION_REQUEST         │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

##### Reroute Handling

**Acceptance Criteria:**
- [ ] Agent sends `REROUTE_REQUEST` via Guide when Step 0 determines "not my domain"
- [ ] `REROUTE_REQUEST` includes: original message, reason for rejection
- [ ] Guide appends `RerouteHop` to message's `RerouteHistory`
- [ ] Guide re-routes to appropriate agent based on rejection reason
- [ ] If `RerouteHistory.length >= 3`, Guide sends `CLARIFICATION_REQUEST` to user
- [ ] Clarification message explains: "Routed to [agents], none could handle. [reasons]"

##### Tests

- [ ] Test StructuredIntent extraction for CREATE, MODIFY, FIX, EXPLAIN, RESEARCH intents
- [ ] Test confidence chain accumulation across 3+ agents
- [ ] Test cumulative confidence calculation (product)
- [ ] Test response evaluation with satisfactory response (proceed)
- [ ] Test response evaluation with unsatisfactory response (retry or escalate)
- [ ] Test REROUTE_REQUEST handling with RerouteHistory tracking
- [ ] Test user escalation after 3 reroutes
- [ ] Test escalation message content (what was tried, why it failed)
- [ ] Verify Guide remains stateless (all state in message)
- [ ] Verify no agent modifies previous ConfidenceChain entries

#### Implicit Requirements Inference

**Goal**: Prevent implicit requirement misses by querying Academic for domain expectations before decomposition.

**Dependencies**: StructuredIntent (above), Academic agent (Phase 1)

**Parallelization**: Items 0.4.IRI.1-3 can execute in parallel. Item 0.4.IRI.4 depends on 0.4.IRI.1-2.

##### 0.4.IRI.1 Domain Expectations Types

**Files to create:**
- `core/messages/domain_expectations.go`

**Implementation Guidelines:**
1. Define `ExpectationPriority` enum: `REQUIRED`, `RECOMMENDED`, `OPTIONAL`
2. Define `DomainExpectation` struct with Expectation, Priority, Rationale fields
3. Define `DomainExpectationsRequest` with IntentType, FeatureDomain, ExistingConstraints
4. Define `DomainExpectationsResponse` with Domain, Expectations slice, Confidence
5. Define `EnrichedSuccessCriterion` with Criterion, Source ("explicit"/"inferred"), Priority
6. Add `DOMAIN_EXPECTATIONS_REQUEST` and `DOMAIN_EXPECTATIONS_RESPONSE` message types

**Acceptance Criteria:**
- [ ] `ExpectationPriority` enum with REQUIRED, RECOMMENDED, OPTIONAL constants
- [ ] `DomainExpectation` struct with json tags for Expectation, Priority, Rationale
- [ ] `DomainExpectationsRequest` struct with IntentType, FeatureDomain, ExistingConstraints
- [ ] `DomainExpectationsResponse` struct with Domain, Expectations, Confidence
- [ ] `EnrichedSuccessCriterion` struct with Criterion, Source, Priority fields
- [ ] Message type constants for DOMAIN_EXPECTATIONS_REQUEST/RESPONSE
- [ ] Unit tests for struct serialization/deserialization

##### 0.4.IRI.2 Academic Domain Expectations Skill

**Files to create:**
- `agents/academic/domain_expectations.go`
- `agents/academic/domain_knowledge.go` (knowledge base)

**Implementation Guidelines:**
1. Create `provide_domain_expectations` skill for Academic agent
2. Build domain knowledge base mapping feature domains to expectations
3. Categorize expectations by priority (REQUIRED, RECOMMENDED, OPTIONAL)
4. Include rationale for each expectation
5. Return confidence based on how well domain matches knowledge base

**Domain Knowledge Structure:**
```go
var DomainExpectations = map[string][]DomainExpectation{
    "authentication/logout": {
        {Expectation: "session termination", Priority: REQUIRED, Rationale: "security"},
        {Expectation: "token/cookie cleanup", Priority: REQUIRED, Rationale: "security"},
        {Expectation: "redirect to login", Priority: REQUIRED, Rationale: "UX"},
        {Expectation: "confirmation if unsaved", Priority: RECOMMENDED, Rationale: "data loss prevention"},
    },
    "authentication/login": {...},
    "api/caching": {...},
    // ... more domains
}
```

**Acceptance Criteria:**
- [ ] `provide_domain_expectations` skill registered with Academic
- [ ] Skill handles DOMAIN_EXPECTATIONS_REQUEST message type
- [ ] Domain knowledge base with at least 10 common feature domains
- [ ] Each domain has REQUIRED, RECOMMENDED, OPTIONAL expectations categorized
- [ ] Rationale provided for each expectation
- [ ] Confidence returned: 0.9+ for exact domain match, 0.7+ for partial, 0.5 for inferred
- [ ] Graceful handling of unknown domains (return empty with low confidence)
- [ ] Unit tests for each domain in knowledge base

##### 0.4.IRI.3 Feature Domain Extraction

**Files to modify:**
- `agents/architect/architect.go`

**Files to create:**
- `agents/architect/domain_extractor.go`

**Implementation Guidelines:**
1. Extract feature_domain from StructuredIntent (intent_type + original_request analysis)
2. Normalize to hierarchical format: "category/feature" (e.g., "authentication/logout")
3. Use keyword matching + semantic analysis
4. Support common categories: authentication, api, database, ui, testing, deployment

**Extraction Rules:**
```
IntentType: CREATE + "logout" in request → "authentication/logout"
IntentType: CREATE + "cache" in request → "api/caching"
IntentType: FIX + "login" in request → "authentication/login"
IntentType: MODIFY + "dashboard" in request → "ui/dashboard"
```

**Acceptance Criteria:**
- [ ] `ExtractFeatureDomain(intent *StructuredIntent) string` function
- [ ] Hierarchical domain format: "category/feature"
- [ ] At least 15 extraction rules covering common domains
- [ ] Fallback to "general/unknown" for unrecognized patterns
- [ ] Unit tests for each extraction rule
- [ ] Integration test with sample StructuredIntents

##### 0.4.IRI.4 Architect Merge Logic

**Files to modify:**
- `agents/architect/architect.go`

**Files to create:**
- `agents/architect/implicit_requirements.go`

**Implementation Guidelines:**
1. Add pre-decomposition hook to query Academic for domain expectations
2. Implement merge logic respecting precedence rules
3. Track source ("explicit" vs "inferred") for all criteria
4. Detect conflicts using semantic similarity
5. Update StructuredIntent with enriched criteria before decomposition

**Merge Precedence (highest to lowest):**
1. Explicit KeyConstraints (always win)
2. Explicit SuccessCriteria
3. REQUIRED expectations from Academic
4. RECOMMENDED expectations from Academic
5. OPTIONAL expectations (only if specifically relevant)

**Acceptance Criteria:**
- [ ] `MergeImplicitRequirements(intent, expectations) *StructuredIntent` function
- [ ] Pre-decomposition hook triggers DOMAIN_EXPECTATIONS_REQUEST
- [ ] Explicit constraints override inferred expectations
- [ ] Explicit criteria preserved with source="explicit"
- [ ] Inferred criteria added with source="inferred" and priority
- [ ] `conflictsWithConstraints(expectation, constraints)` conflict detection
- [ ] Semantic similarity check for conflict detection (threshold 0.8)
- [ ] `inferred_from` field tracks Academic domain source
- [ ] Unit tests for merge logic with conflict scenarios
- [ ] Integration test: full flow from request to enriched intent

##### 0.4.IRI.5 Implicit Requirements Tests

**Files to create:**
- `agents/architect/implicit_requirements_test.go`
- `agents/academic/domain_expectations_test.go`

**Acceptance Criteria:**
- [ ] Test domain extraction for CREATE, MODIFY, FIX intent types
- [ ] Test Academic returns correct expectations for "authentication/logout"
- [ ] Test Academic returns empty for unknown domain with low confidence
- [ ] Test merge preserves explicit criteria
- [ ] Test merge adds inferred criteria with correct source
- [ ] Test explicit constraint "skip confirmation" blocks "confirmation if unsaved"
- [ ] Test explicit constraint "redirect to home" blocks "redirect to login"
- [ ] Test no conflict when constraints don't semantically overlap
- [ ] Integration test: "Add logout button" → enriched with session/token/redirect criteria
- [ ] Integration test: "Add logout, skip confirmation" → confirmation NOT in criteria

#### Feedback Validation Gate

**Goal**: Prevent local-focus bias by validating corrections against StructuredIntent before sending.

**Dependencies**: StructuredIntent (above), EnrichedSuccessCriterion (0.4.IRI.1)

**Parallelization**: Items 0.4.FVG.1-2 can execute in parallel. Items 0.4.FVG.3-4 can execute in parallel after 0.4.FVG.1.

##### 0.4.FVG.1 Intent Alignment Types

**Files to create:**
- `core/messages/intent_alignment.go`

**Implementation Guidelines:**
1. Define `CriteriaImpact` struct with Criterion, Impact (POSITIVE/NEGATIVE/NEUTRAL), Reason
2. Define `IntentAlignment` struct with ConflictsWithConstraints, AffectedCriteria, RootCauseConfidence, RootCauseAssessment, Warning
3. Define `CorrectionEntry` struct with Issue, Correction, IntentAlignment
4. Define `ValidationGateResult` enum: PASSED, FLAGGED, FLAGGED_STRONG, FLAGGED_NOTE, BLOCKED

**Acceptance Criteria:**
- [ ] `CriteriaImpact` struct with Criterion, Impact, Reason fields
- [ ] Impact enum values: "POSITIVE", "NEGATIVE", "NEUTRAL"
- [ ] `IntentAlignment` struct with all fields including optional Warning
- [ ] `CorrectionEntry` struct wrapping Issue, Correction, IntentAlignment
- [ ] `ValidationGateResult` enum with PASSED, FLAGGED, FLAGGED_STRONG, FLAGGED_NOTE, BLOCKED
- [ ] JSON tags for all structs
- [ ] Unit tests for serialization

##### 0.4.FVG.2 Semantic Conflict Detection Utilities

**Files to create:**
- `core/analysis/semantic_conflict.go`

**Implementation Guidelines:**
1. Implement semantic similarity function for conflict detection
2. Support negation detection ("skip X" conflicts with "add X")
3. Support alternative detection ("redirect to home" conflicts with "redirect to login")
4. Use embedding-based similarity if available, keyword fallback otherwise
5. Configurable threshold (default 0.8 for conflict)

**Conflict Patterns:**
```go
// Negation patterns
"skip X" ↔ "add X", "show X", "include X"
"no X" ↔ "with X", "include X"
"without X" ↔ "with X", "using X"

// Alternative patterns
"redirect to A" ↔ "redirect to B" (where A ≠ B)
"use A" ↔ "use B" (where A ≠ B)
```

**Acceptance Criteria:**
- [ ] `SemanticConflict(a, b string) bool` function
- [ ] `SemanticSimilarity(a, b string) float64` function
- [ ] Negation pattern detection (skip/no/without vs add/show/with)
- [ ] Alternative pattern detection (redirect to X vs redirect to Y)
- [ ] Configurable similarity threshold
- [ ] Unit tests for negation conflicts
- [ ] Unit tests for alternative conflicts
- [ ] Unit tests for non-conflicting pairs

##### 0.4.FVG.3 Inspector Validation Gate

**Files to modify:**
- `agents/inspector/inspector.go`

**Files to create:**
- `agents/inspector/validation_gate.go`

**Implementation Guidelines:**
1. Add `ValidateCorrection` method that runs gate before sending corrections
2. Implement three checks: constraint conflicts, criteria impact, root cause assessment
3. Generate appropriate warnings based on gate result
4. Block corrections that conflict with constraints (do not send)
5. Flag corrections with negative criteria impact or low root cause confidence

**Gate Decision Logic:**
```go
if constraint_conflicts > 0 {
    return BLOCKED
} else if any_negative_criteria_impact {
    if root_cause_confidence < 0.50 {
        return FLAGGED_STRONG
    }
    return FLAGGED
} else if root_cause_confidence < 0.50 {
    return FLAGGED_NOTE
}
return PASSED
```

**Acceptance Criteria:**
- [ ] `ValidateCorrection(correction, issue string, intent *StructuredIntent) (*CorrectionEntry, ValidationGateResult)`
- [ ] Check 1: Constraint conflict detection using `conflictsWith`
- [ ] Check 2: Criteria impact assessment using `assessImpact`
- [ ] Check 3: Root cause confidence using `assessRootCauseConfidence`
- [ ] BLOCKED result when constraint conflicts > 0
- [ ] FLAGGED_STRONG when negative impact AND root_cause < 0.50
- [ ] FLAGGED when negative impact AND root_cause >= 0.50
- [ ] FLAGGED_NOTE when no negative impact AND root_cause < 0.50
- [ ] PASSED when no negative impact AND root_cause >= 0.50
- [ ] Warning generation for FLAGGED results
- [ ] Blocked corrections NOT added to VALIDATION_CORRECTIONS message
- [ ] Unit tests for each gate outcome

##### 0.4.FVG.4 Tester Validation Gate

**Files to modify:**
- `agents/tester/tester.go`

**Files to create:**
- `agents/tester/validation_gate.go`

**Implementation Guidelines:**
1. Copy pattern from Inspector validation gate
2. Adapt for TEST_CORRECTIONS message type
3. Same gate logic, same decision matrix
4. Tester-specific warning templates

**Acceptance Criteria:**
- [ ] `ValidateCorrection` method matching Inspector signature
- [ ] Same gate logic as Inspector
- [ ] Tester-specific warning templates
- [ ] Blocked corrections NOT added to TEST_CORRECTIONS message
- [ ] Unit tests mirroring Inspector tests

##### 0.4.FVG.5 Root Cause Assessment

**Files to create:**
- `core/analysis/root_cause.go`

**Implementation Guidelines:**
1. Assess whether a correction addresses root cause or symptom
2. Heuristics:
   - "mock X" → likely symptom fix (confidence 0.3-0.4)
   - "fix X logic" → likely root cause (confidence 0.7-0.8)
   - "add null check" → depends on context (confidence 0.5-0.6)
3. Consider correction-issue relationship
4. Return confidence 0.0-1.0 and assessment string

**Symptom Fix Indicators:**
```go
symptomIndicators := []string{
    "mock", "stub", "skip test", "disable", "ignore",
    "suppress warning", "add timeout", "retry",
}

rootCauseIndicators := []string{
    "fix logic", "correct calculation", "handle edge case",
    "validate input", "check null", "update algorithm",
}
```

**Acceptance Criteria:**
- [ ] `AssessRootCauseConfidence(correction, issue string) float64`
- [ ] `GenerateRootCauseAssessment(correction, issue string) string`
- [ ] Symptom fix indicators return confidence < 0.50
- [ ] Root cause indicators return confidence >= 0.70
- [ ] Context-dependent corrections return confidence 0.50-0.70
- [ ] Assessment string explains reasoning
- [ ] Unit tests for symptom indicators
- [ ] Unit tests for root cause indicators
- [ ] Unit tests for context-dependent cases

##### 0.4.FVG.6 Feedback Validation Gate Tests

**Files to create:**
- `agents/inspector/validation_gate_test.go`
- `agents/tester/validation_gate_test.go`
- `core/analysis/root_cause_test.go`

**Acceptance Criteria:**
- [ ] Test BLOCKED when correction conflicts with KeyConstraint
- [ ] Test FLAGGED_STRONG when negative criteria impact AND low root cause confidence
- [ ] Test FLAGGED when negative criteria impact AND medium+ root cause confidence
- [ ] Test FLAGGED_NOTE when no negative impact AND low root cause confidence
- [ ] Test PASSED when no negative impact AND high root cause confidence
- [ ] Test "mock the cache" correction flagged against "caching implemented" criterion
- [ ] Test "fix cache TTL" correction passed against "caching implemented" criterion
- [ ] Test blocked correction not included in VALIDATION_CORRECTIONS
- [ ] Test warning content includes affected criteria and root cause assessment
- [ ] Integration test: Inspector validates correction flow end-to-end
- [ ] Integration test: Tester validates correction flow end-to-end

#### Implicit Requirements + Feedback Validation Integration

**Goal**: Ensure both mechanisms work together for complete intent preservation.

**Dependencies**: 0.4.IRI.* and 0.4.FVG.* must be complete

##### 0.4.INT.1 Integration Tests

**Files to create:**
- `tests/integration/intent_preservation_test.go`

**Acceptance Criteria:**
- [ ] Test: "Add logout button" → enriched criteria → Inspector validates against enriched criteria
- [ ] Test: Inspector detects missing redirect, correction PASSED because criterion is inferred
- [ ] Test: Correction that mocks session cleanup FLAGGED because it bypasses inferred criterion
- [ ] Test: Full loop from user request to validated correction message
- [ ] Test: Architect receives FLAGGED correction and can request root cause investigation
- [ ] Test: Architect receives BLOCKED correction and must rethink approach
- [ ] Benchmark: Measure token overhead of implicit requirements query (target: < 200 tokens)
- [ ] Benchmark: Measure latency overhead of validation gate (target: < 50ms)

#### Intent Classification for RAG Agents

**CRITICAL**: The Guide extracts intent metadata during routing (zero additional token cost) to enable intent-aware caching for both Librarian and Archivalist.

**Files to create:**
- `agents/guide/intent_classifier.go`

**Files to modify:**
- `agents/guide/guide.go`
- `agents/guide/routing.go`

##### Librarian Intent Classification

```go
// QueryIntent classifies Librarian queries for caching
type QueryIntent int

const (
    QueryIntentLocate  QueryIntent = iota  // "where is X", "find X"
    QueryIntentPattern                      // "what is our X strategy"
    QueryIntentExplain                      // "how does X work"
    QueryIntentGeneral                      // Fallback
)

// LibrarianRoutingMetadata extracted during routing
type LibrarianRoutingMetadata struct {
    Intent     QueryIntent `json:"intent"`
    Subject    string      `json:"subject"`     // Normalized subject (e.g., "auth_middleware")
    Confidence float64     `json:"confidence"`  // Use cache if > 0.8
}
```

##### Archivalist Intent Classification

```go
// ArchivalistIntent classifies Archivalist queries for caching
type ArchivalistIntent int

const (
    ArchivalistIntentHistorical ArchivalistIntent = iota  // "how did we handle X"
    ArchivalistIntentActivity                              // "what changed recently"
    ArchivalistIntentOutcome                               // "did X work"
    ArchivalistIntentSimilar                               // "similar issues to this"
    ArchivalistIntentResume                                // "where did we leave off"
    ArchivalistIntentGeneral                               // Fallback
)

// ArchivalistRoutingMetadata extracted during routing
type ArchivalistRoutingMetadata struct {
    Intent     ArchivalistIntent `json:"intent"`
    Subject    string            `json:"subject"`     // Normalized subject (e.g., "auth_migration")
    TimeScope  string            `json:"time_scope"`  // Optional: "last_month", "recently", ""
    Confidence float64           `json:"confidence"`  // Use cache if > 0.8
}
```

##### Routing Prompt Enhancement

```go
const RAGRoutingInstructions = `
When routing to Librarian, classify the query:
- Intent: LOCATE | PATTERN | EXPLAIN | GENERAL
- Subject: Primary entity (snake_case)

When routing to Archivalist, classify the query:
- Intent: HISTORICAL | ACTIVITY | OUTCOME | SIMILAR | RESUME | GENERAL
- Subject: Primary concept (snake_case)
- Time Scope: "last_month", "yesterday", "recently", "" (optional)

Examples:
Librarian:
- "where is auth middleware" → LOCATE, "auth_middleware"
- "what is our caching strategy" → PATTERN, "caching"

Archivalist:
- "how did we handle auth migration" → HISTORICAL, "auth_migration", ""
- "what changed in API recently" → ACTIVITY, "api", "recently"
`
```

##### Unified RoutedMessage with Intent

```go
// RoutedMessage includes intent metadata for cache lookup
type RoutedMessage struct {
    Query     string `json:"query"`
    Target    string `json:"target"`
    SessionID string `json:"session_id"`

    // Intent metadata (target-specific)
    LibrarianMeta  *LibrarianRoutingMetadata  `json:"librarian_meta,omitempty"`
    ArchivalistMeta *ArchivalistRoutingMetadata `json:"archivalist_meta,omitempty"`
}
```

**Tests:**
- [ ] Librarian LOCATE intent detected for "where is X" patterns
- [ ] Librarian PATTERN intent detected for "strategy/approach" patterns
- [ ] Librarian EXPLAIN intent detected for "how does X work" patterns
- [ ] Librarian GENERAL fallback for unclassified queries
- [ ] Archivalist HISTORICAL intent detected for "how did we" patterns
- [ ] Archivalist ACTIVITY intent detected for "recent changes" patterns
- [ ] Archivalist OUTCOME intent detected for "did it work" patterns
- [ ] Archivalist SIMILAR intent detected for "similar to" patterns
- [ ] Archivalist RESUME intent detected for "continue/pick up" patterns
- [ ] Archivalist time scope extracted correctly
- [ ] Subject normalization to snake_case
- [ ] Confidence threshold (0.8) respected
- [ ] Intent metadata passed in RoutedMessage
- [ ] Zero additional LLM calls (same routing call)

### 0.5 Archivalist Enhancements

Extend Archivalist for multi-session support with shared historical access.

**Files to modify:**
- `agents/archivalist/archivalist.go`
- `agents/archivalist/storage.go`
- `agents/archivalist/types.go`
- `agents/archivalist/query_cache.go`

**Files to create:**
- `agents/archivalist/session_store.go`
- `agents/archivalist/cross_session.go`
- `agents/archivalist/workflow_store.go`
- `agents/archivalist/skills.go`

**Acceptance Criteria:**

#### Archivalist System Prompt

**Core Identity:**
- [ ] Model: Claude Sonnet 4.5 (optimized for pattern matching and history queries)
- [ ] Role: Historical RAG - cross-session memory of failures, decisions, and patterns
- [ ] User Interaction: INDIRECT (triggered by history queries via Guide)

**Core Responsibilities:**
- [ ] FAILURE MEMORY: Track and recall failure patterns to prevent repetition
- [ ] DECISION HISTORY: Store and retrieve past decisions with outcomes
- [ ] PATTERN STORAGE: Maintain patterns that can be promoted to global knowledge
- [ ] SESSION BRIEFINGS: Provide context briefings (micro, standard, full)
- [ ] CROSS-SESSION LEARNING: Enable institutional learning across sessions

**Failure Pattern Memory Protocol (CRITICAL):**
- [ ] Track outcome field: success/failure/partial when storing decisions
- [ ] If failure, extract approach_signature and error_pattern
- [ ] Increment recurrence_count for similar failures
- [ ] ALWAYS check failure_patterns first when queried about approaches
- [ ] Include warning if similar failure exists (similarity > 0.7)
- [ ] Format: "SIMILAR FAILURE DETECTED: [approach] failed [N] times. [error]. Resolution: [fix or 'unknown']"

**Alert Thresholds:**
- [ ] recurrence_count >= 2: "SIMILAR FAILURE DETECTED" warning
- [ ] recurrence_count >= 5: "RECURRING FAILURE" - suggest different approach
- [ ] Same session failure: "REPEATED FAILURE" - trigger escalation

**Cross-Session Learning:**
- [ ] Failure patterns are ALWAYS cross-session readable
- [ ] A failure in session A MUST warn session B
- [ ] Resolutions discovered later update all related failures

**Retrieval Accuracy Protocol:**
- [ ] STALE_RETRIEVAL: Mark "needs_review", add staleness warning
- [ ] IRRELEVANT_RETRIEVAL: Add as negative example for similarity
- [ ] INCOMPLETE_RETRIEVAL: Create cross-reference links
- [ ] WRONG_RESOLUTION: Add "context_specific" flag
- [ ] CONFLICTING_ENTRIES: Add reconciliation note

**Storage Verification:**
- [ ] Read-after-write verification after every store operation
- [ ] Test retrieval with expected query patterns
- [ ] Verify cross-references work both directions

**Query Intent Classification:**
- [ ] HISTORICAL: "What did we do before for X?", "Past solutions"
- [ ] ACTIVITY: "What files changed?", "What happened in last task?"
- [ ] OUTCOME: "Did tests pass?", "What was the result?"
- [ ] SIMILAR: "Have we seen this error before?", "Similar past decisions"
- [ ] RESUME: "Where did we leave off?", "Current status"
- [ ] GENERAL: Other history questions

**Storage Categories:**
- [ ] decision, insight, pattern, failure, task_state, timeline
- [ ] user_voice, hypothesis, open_thread, general

**Communication Style:**
- [ ] Be concise, direct, technical
- [ ] Provide evidence: timestamps, session IDs, recurrence counts
- [ ] If uncertain about retrieval accuracy, flag it
- [ ] NO status acknowledgments, flattery, or hedging
- [ ] Proactively warn about failure patterns

**Critical Constraints:**
- [ ] NEVER return stale information without flagging
- [ ] NEVER ignore failure patterns when queried about approaches
- [ ] ALWAYS verify storage after write operations
- [ ] ALWAYS include recurrence counts for failure patterns
- [ ] ALWAYS cross-reference related entries

**YOU DO NOT:**
- [ ] Suppress failure warnings to seem helpful
- [ ] Return outdated information without staleness flags
- [ ] Store without verification
- [ ] Ignore accuracy issues reported by agents
- [ ] Provide incomplete context when fuller context exists

#### Session Isolation
- [ ] Session ID on all stored entries
- [ ] Default queries scoped to current session
- [ ] Explicit cross-session queries with flag
- [ ] Session context in all store operations

#### Cross-Session Queries (Read-Only)
- [ ] `QueryCrossSession()` searches all sessions
- [ ] `QuerySessions()` returns matching sessions
- [ ] `GetSessionHistory()` returns session timeline
- [ ] Results tagged with source session ID
- [ ] No write access to other sessions

#### Workflow Storage
- [ ] Store DAG definitions with session ID
- [ ] Store DAG execution history (node results, timing)
- [ ] Store workflow outcomes (success, failure, corrections)
- [ ] Query similar past workflows

#### Token Savings Tracking
- [ ] Record cache hits per session
- [ ] Record skipped work (existing functionality found)
- [ ] Record consultation results
- [ ] Generate savings report per session/global

#### Archivalist Skills (Progressive Disclosure)

**Core Skills (always loaded):**
- [ ] `store` skill - store entries
- [ ] `query` skill - query entries
- [ ] `briefing` skill - get session briefing
- [ ] `route_to` skill - route message to another agent (common skill)
- [ ] `reply_to` skill - reply to routed message (common skill)

**Extended Skills (loaded on demand):**
- [ ] `cross_session_query` skill - query across sessions
- [ ] `workflow_history` skill - query past workflows
- [ ] `token_savings` skill - get savings report
- [ ] `session_timeline` skill - get session events
- [ ] `pattern_search` skill - find similar patterns
- [ ] `failure_search` skill - find similar failures
- [ ] `decision_search` skill - find similar decisions

#### Archivalist Tools
```go
var ArchivalistTools = []ToolDefinition{
    // Core storage
    {Name: "archivalist_store", Skill: "store"},
    {Name: "archivalist_query", Skill: "query"},
    {Name: "archivalist_briefing", Skill: "briefing"},
    // Extended queries
    {Name: "archivalist_cross_session_query", Skill: "cross_session_query"},
    {Name: "archivalist_workflow_history", Skill: "workflow_history"},
    {Name: "archivalist_token_savings", Skill: "token_savings"},
    {Name: "archivalist_session_timeline", Skill: "session_timeline"},
    {Name: "archivalist_pattern_search", Skill: "pattern_search"},
    {Name: "archivalist_failure_search", Skill: "failure_search"},
    {Name: "archivalist_decision_search", Skill: "decision_search"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Archivalist Hooks
- [ ] Pre-store hook: inject session context
- [ ] Post-store hook: invalidate relevant caches
- [ ] Pre-query hook: add session filter
- [ ] Post-query hook: record query for analytics

**Tests:**
- [ ] Store entries from multiple sessions
- [ ] Query within session, verify isolation
- [ ] Cross-session query, verify results tagged
- [ ] Concurrent sessions storing, verify no corruption

#### Intent-Aware Query Caching

**CRITICAL**: The Archivalist benefits from intent-aware caching for historical queries. Unlike Librarian (live code), historical data is immutable, enabling higher cache hit rates.

**Files to create:**
- `agents/archivalist/intent_cache.go`

**Files to modify:**
- `agents/archivalist/query_cache.go`
- `agents/archivalist/archivalist.go`

##### ArchivalistIntent Type

```go
// ArchivalistIntent classifies historical queries for caching
type ArchivalistIntent int

const (
    ArchivalistIntentHistorical ArchivalistIntent = iota  // Past solutions: "how did we handle X"
    ArchivalistIntentActivity                              // Recent work: "what changed", "who worked on"
    ArchivalistIntentOutcome                               // Results: "did it work", "status of"
    ArchivalistIntentSimilar                               // Pattern matching: "similar issues"
    ArchivalistIntentResume                                // Session state: "where did we leave off"
    ArchivalistIntentGeneral                               // Fallback for other history queries
)
```

##### Intent-Aware Cache Structure

```go
// IntentCacheKey combines intent + subject + time scope for cache lookup
type IntentCacheKey struct {
    Intent    ArchivalistIntent `json:"intent"`
    Subject   string            `json:"subject"`     // Normalized subject (e.g., "auth_migration")
    TimeScope string            `json:"time_scope"`  // Optional: "last_month", "recently", ""
    SessionID string            `json:"session_id"`
}

// IntentCachedResponse wraps cached responses with intent metadata
type IntentCachedResponse struct {
    Key         IntentCacheKey `json:"key"`
    Response    []byte         `json:"response"`
    CreatedAt   time.Time      `json:"created_at"`
    ExpiresAt   time.Time      `json:"expires_at"`
    HitCount    int64          `json:"hit_count"`
    Confidence  float64        `json:"confidence"` // From Guide classification
}

// IntentCache provides intent-aware caching for Archivalist
type IntentCache struct {
    mu sync.RWMutex

    // Cache by intent type for fast lookup
    byIntent map[ArchivalistIntent]map[string]*IntentCachedResponse

    // Secondary index by subject for cross-intent queries
    bySubject map[string][]*IntentCachedResponse

    // Embedding cache for similarity fallback (95% threshold)
    embeddingCache *QueryCache  // Existing embedding-based cache

    // Statistics
    hits   int64
    misses int64
}
```

##### Intent-Specific TTLs

```go
func getTTLForArchivalistIntent(intent ArchivalistIntent) time.Duration {
    switch intent {
    case ArchivalistIntentHistorical:
        return 60 * time.Minute  // Past solutions rarely change

    case ArchivalistIntentActivity:
        return 5 * time.Minute   // Activity updates frequently

    case ArchivalistIntentOutcome:
        return 30 * time.Minute  // Outcomes stable once recorded

    case ArchivalistIntentSimilar:
        return 45 * time.Minute  // Similar patterns rarely change

    case ArchivalistIntentResume:
        return 1 * time.Minute   // Resume state is highly volatile

    case ArchivalistIntentGeneral:
        return 15 * time.Minute  // Default for unclassified

    default:
        return 15 * time.Minute
    }
}
```

##### Cache Lookup Flow

```go
// Get attempts to find a cached response for the intent + subject
func (ic *IntentCache) Get(key IntentCacheKey) (*IntentCachedResponse, bool) {
    ic.mu.RLock()
    defer ic.mu.RUnlock()

    // 1. Try exact intent + subject + time_scope match
    intentMap, ok := ic.byIntent[key.Intent]
    if ok {
        cacheKey := ic.buildCacheKey(key)
        if resp, found := intentMap[cacheKey]; found && !resp.IsExpired() {
            atomic.AddInt64(&ic.hits, 1)
            atomic.AddInt64(&resp.HitCount, 1)
            return resp, true
        }
    }

    // 2. Try broader time scope (e.g., no time scope if specific one not found)
    if key.TimeScope != "" {
        broadKey := key
        broadKey.TimeScope = ""
        if resp, found := ic.tryBroadMatch(broadKey); found {
            return resp, true
        }
    }

    // 3. Miss - caller should query Archivalist
    atomic.AddInt64(&ic.misses, 1)
    return nil, false
}
```

##### Invalidation Rules (Intent-Specific)

| Intent | Invalidation Trigger | Rationale |
|--------|---------------------|-----------|
| **HISTORICAL** | TTL only (60min) | Past solutions don't change |
| **ACTIVITY** | New entry in same subject | Activity queries show recent changes |
| **OUTCOME** | TTL only (30min) | Outcomes are immutable once recorded |
| **SIMILAR** | TTL only (45min) | Similar patterns change slowly |
| **RESUME** | Session activity | Any session activity invalidates resume state |
| **GENERAL** | TTL only (15min) | Conservative default |

```go
// InvalidateForNewEntry invalidates relevant caches when new entry stored
func (ic *IntentCache) InvalidateForNewEntry(entry *ArchiveEntry) {
    ic.mu.Lock()
    defer ic.mu.Unlock()

    // Activity queries for this subject should be invalidated
    if activityMap, ok := ic.byIntent[ArchivalistIntentActivity]; ok {
        for key := range activityMap {
            if strings.Contains(key, entry.Subject) {
                delete(activityMap, key)
            }
        }
    }

    // Resume queries for this session should be invalidated
    if resumeMap, ok := ic.byIntent[ArchivalistIntentResume]; ok {
        for key, resp := range resumeMap {
            if resp.Key.SessionID == entry.SessionID {
                delete(resumeMap, key)
            }
        }
    }
}
```

##### Expected Hit Rates by Intent

| Intent | Expected Hit Rate | Rationale |
|--------|------------------|-----------|
| HISTORICAL | 85% | Users often ask same questions about past work |
| ACTIVITY | 40% | Updates frequently, but often repeated "what changed" |
| OUTCOME | 75% | Status checks repeated, outcomes immutable |
| SIMILAR | 70% | Similar issues often queried multiple times |
| RESUME | 50% | Session state queried repeatedly in short windows |
| GENERAL | 60% | Mixed queries, moderate caching benefit |

**Weighted Average: ~65-70% hit rate**

**Tests:**
- [ ] Cache hit for exact intent + subject match
- [ ] Cache miss triggers Archivalist query
- [ ] TTL expiration by intent type
- [ ] Activity cache invalidated on new entry
- [ ] Resume cache invalidated on session activity
- [ ] Historical cache persists across new entries
- [ ] Outcome cache persists across new entries
- [ ] Broad time scope fallback works
- [ ] Session isolation (no cross-session cache hits)
- [ ] Statistics tracking (hits, misses, by intent)
- [ ] Concurrent access safety
- [ ] Memory limits respected (eviction when full)

### 0.6 Bus Enhancements

Extend event bus for session-scoped topics and wildcards.

**Files to modify:**
- `agents/guide/bus.go`
- `agents/guide/channel_bus.go` (add sharding)

**Files to create:**
- `agents/guide/topic_router.go`
- `agents/guide/session_bus.go`

**Acceptance Criteria:**

#### Session-Scoped Topics
- [ ] Topic format: `session.{session_id}.{agent}.{channel}`
- [ ] Session topic creation on session start
- [ ] Session topic cleanup on session close
- [ ] Global topics (no session prefix) for shared agents

#### Topic Wildcards
- [ ] Subscribe to `session.*.status` for all session status
- [ ] Subscribe to `*.requests` for all agent requests
- [ ] Efficient wildcard matching (trie or similar)

#### Message Filtering
- [ ] Filter by session ID in message metadata
- [ ] Filter by message type
- [ ] Filter by priority

#### Session Bus Wrapper
- [ ] `SessionBus` wraps EventBus with session context
- [ ] Auto-prefix topics with session ID
- [ ] Session-scoped subscriptions (auto-cleanup)

#### ChannelBus Sharding (`agents/guide/channel_bus.go`)

**Current Problem:** Single `sync.RWMutex` creates lock contention with many topics/sessions.

**Required Changes:**
- [ ] Replace single `subscriptions map[string][]*channelSubscription` with sharded structure
- [ ] Configurable shard count (default: 32, must be power of 2)
- [ ] Topic-to-shard mapping via FNV hash
- [ ] Per-shard mutex (eliminates single-lock bottleneck)
- [ ] Backwards compatible - same `EventBus` interface

```go
// BEFORE (current)
type ChannelBus struct {
    mu            sync.RWMutex
    subscriptions map[string][]*channelSubscription  // Single lock bottleneck
    // ...
}

// AFTER (sharded)
type ChannelBus struct {
    shards    []*busShard
    shardMask uint32  // shardCount - 1 for fast modulo
    // ...
}

type busShard struct {
    mu            sync.RWMutex
    subscriptions map[string][]*channelSubscription
}

func (b *ChannelBus) getShard(topic string) *busShard {
    h := fnv32a(topic)
    return b.shards[h & b.shardMask]
}

// Publish now only locks one shard
func (b *ChannelBus) Publish(topic string, msg *Message) error {
    shard := b.getShard(topic)
    shard.mu.RLock()
    subs := shard.subscriptions[topic]
    shard.mu.RUnlock()
    // ...
}
```

- [ ] Benchmark: 32 shards with 1000 topics should show < 10% lock contention
- [ ] Benchmark: Linear scaling up to shard count concurrent publishers

**Tests:**
- [ ] Publish/subscribe with session topics
- [ ] Wildcard subscription matching
- [ ] Message filtering by session
- [ ] Concurrent publish across shards (verify no single-lock bottleneck)
- [ ] 100+ topics distributed across shards

---

### 0.7 LLM Provider Adapters

Abstract LLM provider interactions behind a common interface.

**Files:**
- `core/providers/provider.go` (existing - DONE)
- `core/providers/config.go` (existing - DONE)
- `core/providers/stream.go` (existing - DONE)
- `core/providers/anthropic.go` (existing - DONE)
- `core/providers/openai.go` (existing - DONE)
- `core/providers/google.go` (existing - DONE)
- `core/providers/token_counter.go` (new - NOT DONE)

**Parallelization:** Can execute in parallel with 0.8, 0.9, 0.10.

**Acceptance Criteria:**

#### Provider Interface (DONE - core/providers/provider.go)
- [x] `Provider` interface with `Name()`, `Generate()`, `Stream()`, `ValidateConfig()`, `SupportsModel()`, `DefaultModel()`, `Close()`
- [x] `Request` struct: Model, Messages, MaxTokens, Temperature, TopP, Tools, StopSequences, SystemPrompt, ReasoningEffort
- [x] `Response` struct: Content, Model, StopReason, Usage, ToolCalls, ProviderMetadata
- [x] `Message` struct: Role, Content, ToolCalls, ToolCallID
- [x] `Tool` struct: Name, Description, Parameters (JSON schema)
- [x] `ToolCall` struct: ID, Name, Arguments
- [x] `Usage` struct: InputTokens, OutputTokens, TotalTokens, CacheReadTokens, CacheWriteTokens
- [x] `StopReason` constants: end_turn, max_tokens, stop_sequence, tool_use, error
- [x] `StreamHandler` callback type for streaming
- [x] `SupportedModels() []ModelInfo` - returns available models with pricing info
- [ ] `CountTokens(messages []Message) (int, error)` - pre-request token estimation
- [ ] `MaxContextTokens(model string) int` - model context window size
- [ ] `HealthCheck(ctx context.Context) error` - provider availability check

#### Anthropic Adapter (DONE - core/providers/anthropic.go)
- [x] Implement `Provider` interface for Anthropic API
- [x] Support Claude models: claude-opus-4-5-20251101, claude-sonnet-4-5-20250901, claude-haiku-4-5-20251001
- [x] Handle streaming responses via SSE with NewStreaming()
- [x] Parse usage from response for token tracking (InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens)
- [x] Map tool_use blocks to `ToolCall` struct
- [x] Beta headers for interleaved thinking and fine-grained tool streaming
- [x] Long context support (1M tokens) for Sonnet

#### OpenAI Adapter (DONE - core/providers/openai.go)
- [x] Implement `Provider` interface for OpenAI API
- [x] Support models via Responses API: gpt-5.2-codex
- [x] Handle streaming responses with ResponseTextDeltaEvent
- [x] Parse usage from response
- [x] Map function_call to `ToolCall` struct
- [x] Support reasoning effort configuration (low, medium, high, xhigh)
- [x] Organization and Project headers support

#### Google Adapter (DONE - core/providers/google.go)
- [x] Implement `Provider` interface for Google Generative AI API
- [x] Support Gemini models: gemini-3-pro-preview
- [x] Handle streaming responses with GenerateContentStream iterator
- [x] Parse UsageMetadata for token tracking
- [x] Map FunctionCall to `ToolCall` struct
- [x] Vertex AI support (project/location configuration)
- [x] Safety settings configuration
- [x] TopK parameter support

#### Token Counter (NOT DONE - core/providers/token_counter.go)
- [ ] `TokenCounter` interface with `Count(messages []Message) int`
- [ ] Provider-specific implementations (tiktoken for OpenAI, approximation for others)
- [ ] Cache tokenizer instances per model
- [ ] Fallback to character-based estimation if tokenizer unavailable
- [ ] Include tool definitions in token count

**Tests:**
- [x] Each adapter correctly formats requests for its provider
- [x] Streaming responses parsed correctly
- [ ] Token counts match provider response
- [ ] HealthCheck returns provider status
- [ ] Invalid API key returns clear error
- [ ] Rate limit (429) handled correctly

```go
// Current interface (implemented)
type Provider interface {
    Name() string
    Generate(ctx context.Context, req *Request) (*Response, error)
    Stream(ctx context.Context, req *Request, handler StreamHandler) error
    ValidateConfig() error
    SupportsModel(model string) bool
    DefaultModel() string
    Close() error
}

// Missing methods (to be added)
// SupportedModels() []ModelInfo
// CountTokens(messages []Message) (int, error)
// MaxContextTokens(model string) int
// HealthCheck(ctx context.Context) error
```

---

### 0.8 Provider Configuration & Auth

Credential management and provider configuration.

**Files to create:**
- `core/llm/config.go`
- `core/llm/credentials.go`
- `core/llm/registry.go`
- `cmd/auth.go` (CLI command)

**Parallelization:** Can execute in parallel with 0.7, 0.9, 0.10.

**Acceptance Criteria:**

#### Credential Resolution
- [x] Load from environment variable first: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`
- [x] Fall back to config file: `~/.sylk/credentials.yaml`
- [ ] Credentials file encrypted at rest using OS keyring or AES-256
- [x] Never log or serialize API keys

#### Provider Config (`core/llm/config.go`)
- [x] `ProviderConfig` struct: Provider, BaseURL (optional), Models map, SoftLimit, Enabled
- [x] `UsageLimit` struct: Requests, Tokens, Period, WarnAt threshold
- [x] Load config from `~/.sylk/config.yaml`
- [x] Validate config on load (required fields, valid values)

#### Provider Registry (`core/llm/registry.go`)
- [x] `ProviderRegistry` manages all configured providers
- [x] `Register(adapter ProviderAdapter)` adds provider
- [x] `Get(name string) ProviderAdapter` retrieves provider
- [x] `GetForModel(model string) ProviderAdapter` finds provider supporting model
- [x] `ListEnabled() []ProviderAdapter` returns enabled providers
- [x] Lazy initialization of adapters (don't connect until first use)

#### Auth CLI (`cmd/auth.go`)
- [x] `sylk auth <provider>` - interactive API key setup
- [x] `sylk auth <provider> --api-key <key>` - direct key input
- [x] `sylk auth google --credentials-file <path>` - service account JSON
- [x] `sylk auth status` - show configured providers and status
- [x] `sylk auth remove <provider>` - remove credentials
- [ ] Validate key with provider health check before saving

**Tests:**
- [x] Environment variable takes precedence over config file
- [x] Invalid credentials rejected with clear error
- [x] Config validation catches missing required fields
- [x] Registry returns correct adapter for model
- [ ] Auth command saves encrypted credentials

```go
// Config structure
type ProviderConfig struct {
    Provider    string            `yaml:"provider"`
    APIKey      string            `yaml:"-"`  // Never serialized
    BaseURL     string            `yaml:"base_url,omitempty"`
    Models      map[string]string `yaml:"models,omitempty"`
    SoftLimit   *UsageLimit       `yaml:"soft_limit,omitempty"`
    Enabled     bool              `yaml:"enabled"`
}
```

---

### 0.9 Request Priority Queue

Priority-based request queuing for LLM calls.

**Files to create:**
- `core/llm/queue.go`
- `core/llm/priority.go`
- `core/llm/queue_test.go`

**Parallelization:** Can execute in parallel with 0.7, 0.8, 0.10.

**Acceptance Criteria:**

#### Priority Levels
- [x] `RequestPriority` type with constants: UserInteractive (100), Planning (80), Execution (60), Validation (40), Background (20)
- [x] Priority determination function: if user-invoked → UserInteractive, else based on agent type
- [x] Architect → Planning, Engineer/Designer → Execution, Inspector/Tester → Validation, Archivalist/Librarian → Background

#### LLM Request
- [x] `LLMRequest` struct with: ID, SessionID, PipelineID, TaskID, AgentID, AgentType, Provider, Model, Messages, Priority, CreatedAt, TokenEstimate
- [x] Request ID generation (UUID)
- [ ] Token estimation before queuing (for budget checks)

#### Priority Queue
- [x] `PriorityQueue` using heap-based implementation
- [x] Thread-safe Push/Pop operations
- [x] Pop blocks when queue empty (using sync.Cond)
- [x] Higher priority value = processed first
- [x] Within same priority, FIFO ordering (use CreatedAt as tiebreaker)
- [x] `Close()` method to unblock waiting consumers
- [x] `Len()` method for monitoring

**Tests:**
- [x] Higher priority requests dequeued first
- [x] Same priority maintains FIFO order
- [x] Pop blocks on empty queue
- [x] Close unblocks waiting Pop calls
- [x] Concurrent Push/Pop is thread-safe
- [x] 10,000 requests queued/dequeued correctly

```go
// Required interface
type RequestQueue interface {
    Push(req *LLMRequest)
    Pop(ctx context.Context) (*LLMRequest, error)
    Len() int
    Close()
}
```

---

### 0.10 Adaptive Rate Limiter

Exponential backoff rate limiting with workflow pause signaling.

**Files to create:**
- `core/llm/ratelimit.go`
- `core/llm/backoff.go`
- `core/llm/ratelimit_test.go`

**Parallelization:** Can execute in parallel with 0.7, 0.8, 0.9.

**Dependencies:** Requires 0.11 (Signal Bus) for pause signaling - stub initially.

**Acceptance Criteria:**

#### Rate Limit State
- [x] `RateLimitState` enum: OK, Warning, Exceeded
- [x] State transitions: OK → Warning (approaching soft limit), OK/Warning → Exceeded (429 received)
- [x] State recovery: Exceeded → OK (after backoff period)

#### Exponential Backoff
- [x] Base backoff: 1 second
- [x] Max backoff: 5 minutes
- [x] Backoff formula: `min(baseBackoff * 2^attempt, maxBackoff)`
- [x] Parse `Retry-After` header if present and use instead
- [x] Reset backoff attempt counter on successful request

#### Provider Rate Limiter
- [x] `ProviderRateLimiter` struct per provider
- [x] `OnResponse(statusCode int, headers http.Header)` - handle response
- [x] `CanProceed() bool` - check if requests allowed
- [x] `RecordUsage(tokens int)` - track for soft limits
- [x] Thread-safe operations

#### Soft Limit Tracking
- [x] Track requests and tokens per period
- [x] Reset counters when period expires
- [x] Emit `SignalQuotaWarning` at warn threshold (e.g., 80%)
- [x] Continue allowing requests (soft limit = warning only)

#### Workflow Pause Integration
- [x] On 429: broadcast `SignalPauseAll` via SignalBus (stub interface)
- [x] Include in payload: provider, resumeAt time, attempt count
- [x] On backoff complete: broadcast `SignalResumeAll`

**Tests:**
- [x] 429 response triggers exponential backoff
- [x] Backoff time doubles each attempt up to max
- [x] Retry-After header respected
- [x] Success resets backoff counter
- [x] Soft limit warning emitted at threshold
- [x] CanProceed returns false during backoff

```go
// Required interface
type RateLimiter interface {
    OnResponse(statusCode int, headers http.Header)
    CanProceed() bool
    RecordUsage(tokens int)
    State() RateLimitState
    BackoffRemaining() time.Duration
}
```

---

### 0.10a Streaming + Event Bus Integration

Real-time LLM streaming responses integrated with the event bus for reactive agent coordination.

**Reference:** See ARCHITECTURE.md "Streaming + Event Bus Integration" section for detailed design.

**Files:**
- `core/providers/stream.go` (existing - enhance)
- `core/providers/stream_bridge.go` (new)
- `core/providers/stream_metrics.go` (new)
- `core/providers/anthropic.go` (existing - done)
- `core/providers/google.go` (existing - done)
- `core/providers/openai.go` (existing - done)

**Dependencies:** Requires 0.6 (Bus Enhancements), 0.11 (Signal Bus) for full integration.

**Acceptance Criteria:**

#### Core Streaming Types (DONE - core/providers/stream.go)

- [x] `StreamChunk` struct with Index, Text, Type, ToolCall, Usage, StopReason, Timestamp
- [x] `StreamChunkType` constants: text, tool_start, tool_delta, tool_end, start, end, error
- [x] `ToolCallChunk` struct with ID, Name, ArgumentsDelta
- [x] `StreamAccumulator` with thread-safe chunk collection
- [x] `StreamAccumulator.Add()` handles all chunk types correctly
- [x] `StreamAccumulator.Response()` builds complete Response from chunks
- [x] `StreamCollector` wraps StreamHandler with optional callbacks
- [x] `StreamToChannel()` converts callback-based streaming to Go channels
- [x] `StreamWithCallback()` convenience function for simple text streaming
- [x] Channel buffer size 100 for backpressure absorption

#### Provider Streaming Implementations (DONE)

- [x] Anthropic adapter: Stream() with event-based SSE handling
- [x] Anthropic: Converts ContentBlockDeltaEvent, ContentBlockStartEvent, ContentBlockStopEvent to StreamChunk
- [x] Anthropic: Tracks tool call IDs via toolCallIDForIndex map
- [x] Anthropic: Sends ChunkTypeStart at stream beginning
- [x] Anthropic: Sends ChunkTypeEnd with final Usage and StopReason
- [x] Google adapter: Stream() with iterator-based API
- [x] Google: Range-based iteration over GenerateContentStream
- [x] Google: Handles function calls with toolCallsSeen deduplication
- [x] OpenAI adapter: Stream() with Responses API events
- [x] OpenAI: Handles ResponseTextDeltaEvent, ResponseFunctionCallArgumentsDeltaEvent
- [x] OpenAI: Handles ResponseCompletedEvent, ResponseFailedEvent, ResponseIncompleteEvent
- [x] All providers: Handler error return halts stream cleanly
- [x] All providers: Context cancellation propagates correctly

#### Event Bus Message Wrapping (NOT DONE - core/providers/stream_bridge.go)

- [ ] `StreamContext` struct: CorrelationID, RequestID, SessionID, ProviderName, TargetAgent, Model, AgentID, Priority
- [ ] `WrapStreamChunk(chunk *StreamChunk, ctx StreamContext) Message[StreamChunk]` function
- [ ] Message includes CorrelationID linking all chunks from one generation
- [ ] Message includes ParentID linking to triggering request
- [ ] Message metadata includes model, agent_id, chunk_index, chunk_type
- [ ] Priority inheritance from request context
- [ ] MessageType constant: `MessageTypeStream = "stream"`

#### Stream Bridge Service (NOT DONE - core/providers/stream_bridge.go)

- [ ] `StreamBridge` struct manages streaming-to-eventbus conversion
- [ ] `StartStream(ctx context.Context, provider Provider, req *Request, streamCtx StreamContext) error`
- [ ] Goroutine reads from StreamToChannel, wraps chunks, publishes to event bus
- [ ] Graceful shutdown on context cancellation
- [ ] Error chunks converted to ChunkTypeError messages
- [ ] `CancelStream(correlationID string)` sends cancellation signal via event bus
- [ ] `StreamControl` message type: {Action: "cancel", Reason: string}

#### Stream Metrics Collection (NOT DONE - core/providers/stream_metrics.go)

- [ ] `StreamMetrics` struct: TimeToFirstToken, TimeToFirstToolCall, TotalDuration, TokensPerSecond, ChunksReceived, EarlyAborts, TokensSaved, BackpressureEvents
- [ ] `CollectStreamMetrics(chunks <-chan *StreamChunk, done <-chan struct{}) *StreamMetrics`
- [ ] Time to first token tracked from stream start
- [ ] Time to first tool call tracked when ChunkTypeToolStart received
- [ ] Tokens per second calculated from final Usage
- [ ] Early aborts counted when stream cancelled before ChunkTypeEnd
- [ ] Tokens saved estimated from aborted streams
- [ ] Backpressure events counted when channel send blocks
- [ ] Metrics accessible via `MetricsRegistry.Get("stream")` for CLI display

#### Stream Timeout Watchdog (NOT DONE - core/providers/stream_bridge.go)

- [ ] `StreamWatchdog` monitors streams for timeout
- [ ] Configurable timeout duration (default: 60s between chunks)
- [ ] Watchdog cancels context if no chunks received within timeout
- [ ] Timeout events logged with stream metadata
- [ ] Partial response preserved on timeout

#### Agent Stream Integration Patterns (NOT DONE - documentation only)

- [ ] Document pattern: Agent subscribes to MessageTypeStream with CorrelationID filter
- [ ] Document pattern: Progressive UI rendering from ChunkTypeText
- [ ] Document pattern: Real-time token budget checking
- [ ] Document pattern: Early tool preparation from ChunkTypeToolStart
- [ ] Document pattern: Validation-based early abort

#### Backpressure Monitoring (NOT DONE - core/providers/stream_metrics.go)

- [ ] Detect when channel send blocks (producer faster than consumer)
- [ ] Track cumulative backpressure time per stream
- [ ] Emit warning when backpressure exceeds threshold (configurable, default 5s cumulative)
- [ ] Metrics include backpressure_events_total and backpressure_duration_seconds

#### Edge Case Handling (PARTIALLY DONE)

- [x] Network interruption: stream.Err() returns error, ChunkTypeError sent
- [x] Context cancellation: ctx.Done() propagates, handler returns ctx.Err()
- [x] Multiple tool calls: toolCallIDForIndex map handles correctly
- [ ] Rate limit (429) during stream: Convert to ChunkTypeError, trigger RateLimiter.OnRateLimit()
- [ ] Malformed tool arguments: Validation in accumulator, error in Response
- [ ] Stream timeout: Watchdog cancels, partial response preserved

**Tests:**

- [ ] StreamToChannel correctly converts all chunk types
- [ ] WrapStreamChunk creates valid Message[StreamChunk] with correct metadata
- [ ] StreamBridge publishes chunks to event bus in order
- [ ] CancelStream sends control message and halts stream
- [ ] StreamMetrics accurately tracks all timing metrics
- [ ] StreamWatchdog cancels stream after timeout
- [ ] Backpressure events detected and counted
- [ ] Edge cases (timeout, 429, cancellation) handled correctly
- [ ] No goroutine leaks on stream completion or cancellation
- [ ] Concurrent streams to same event bus work correctly

```go
// Required new types
type StreamContext struct {
    CorrelationID string
    RequestID     string
    SessionID     string
    ProviderName  string
    TargetAgent   string
    Model         string
    AgentID       string
    Priority      Priority
}

type StreamControl struct {
    Action string `json:"action"` // "cancel"
    Reason string `json:"reason"`
}

type StreamMetrics struct {
    TimeToFirstToken    time.Duration
    TimeToFirstToolCall time.Duration
    TotalDuration       time.Duration
    TokensPerSecond     float64
    ChunksReceived      int
    EarlyAborts         int
    TokensSaved         int
    BackpressureEvents  int
}
```

---

### 0.10b Streaming Edge Cases & Cross-Cutting Concerns

Comprehensive edge case handling, configuration, observability, and resilience patterns for the streaming system.

**Reference:** See ARCHITECTURE.md "Streaming + Event Bus Integration" section for design context.

**Files:**
- `core/providers/stream_config.go` (new)
- `core/providers/stream_resilience.go` (new)
- `core/providers/stream_test.go` (new/enhance)
- `core/observability/stream_telemetry.go` (new)

**Dependencies:** Requires 0.10a (Streaming + Event Bus Integration), 0.11 (Signal Bus).

**Acceptance Criteria:**

#### Streaming Configuration (NOT DONE - core/providers/stream_config.go)

- [ ] `StreamConfig` struct with all configurable parameters
- [ ] `ChannelBufferSize` (default: 100) - buffer for backpressure absorption
- [ ] `ChunkTimeoutDuration` (default: 60s) - max time between chunks before timeout
- [ ] `MaxStreamDuration` (default: 10m) - maximum total stream duration
- [ ] `BackpressureWarningThreshold` (default: 5s) - cumulative backpressure before warning
- [ ] `EnableMetrics` (default: true) - toggle stream metrics collection
- [ ] `EnableTracing` (default: true) - toggle distributed tracing
- [ ] `RetryOnNetworkError` (default: true) - auto-retry on transient network failures
- [ ] `MaxRetryAttempts` (default: 3) - max retries for recoverable errors
- [ ] `RetryBackoffBase` (default: 1s) - base duration for exponential retry backoff
- [ ] Configuration loaded from YAML with environment variable overrides
- [ ] Validation on load (reasonable bounds, required fields)

```go
type StreamConfig struct {
    ChannelBufferSize            int           `yaml:"channel_buffer_size"`
    ChunkTimeoutDuration         time.Duration `yaml:"chunk_timeout_duration"`
    MaxStreamDuration            time.Duration `yaml:"max_stream_duration"`
    BackpressureWarningThreshold time.Duration `yaml:"backpressure_warning_threshold"`
    EnableMetrics                bool          `yaml:"enable_metrics"`
    EnableTracing                bool          `yaml:"enable_tracing"`
    RetryOnNetworkError          bool          `yaml:"retry_on_network_error"`
    MaxRetryAttempts             int           `yaml:"max_retry_attempts"`
    RetryBackoffBase             time.Duration `yaml:"retry_backoff_base"`
}
```

#### Stream Resilience Patterns (NOT DONE - core/providers/stream_resilience.go)

- [ ] `StreamRetrier` handles recoverable stream failures
- [ ] Retry on transient network errors (connection reset, timeout)
- [ ] NO retry on authentication errors (401/403)
- [ ] NO retry on rate limit (429) - defer to RateLimiter
- [ ] NO retry on content policy violations
- [ ] Exponential backoff between retries: `min(base * 2^attempt, 30s)`
- [ ] Partial response preservation on retry (accumulator state saved)
- [ ] Resume-from-partial capability (if provider supports)
- [ ] `StreamCircuitBreaker` per-provider failure protection
- [ ] Circuit opens after 5 consecutive stream failures
- [ ] Half-open state after 30s, single probe request
- [ ] Circuit closes after 3 successful probes
- [ ] Circuit breaker events emitted to Signal Bus

```go
type StreamRetrier struct {
    config      StreamConfig
    accumulator *StreamAccumulator  // Preserved across retries
    attempts    int
    lastError   error
}

type StreamCircuitBreaker struct {
    provider    string
    state       CircuitState  // Closed, Open, HalfOpen
    failures    int
    lastFailure time.Time
    signalBus   *SignalBus
}
```

#### Provider-Specific Edge Cases (NOT DONE)

**Anthropic-Specific:**
- [ ] Handle `overloaded_error` (503) with retry + longer backoff
- [ ] Handle `api_error` with error details extraction
- [ ] Handle thinking block interleaving (beta feature)
- [ ] Handle long context (1M tokens) streaming for Sonnet with adjusted timeout
- [ ] Graceful handling of beta header deprecation warnings

**OpenAI-Specific:**
- [ ] Handle `server_error` events with retry
- [ ] Handle `rate_limit_exceeded` with Retry-After header parsing
- [ ] Handle reasoning token streaming for o1/o3 models
- [ ] Handle function call parallel execution hints
- [ ] Graceful fallback when Responses API unavailable

**Google-Specific:**
- [ ] Handle `SAFETY` finish reason (content blocked)
- [ ] Handle `RECITATION` finish reason (citation required)
- [ ] Handle Vertex AI authentication token refresh mid-stream
- [ ] Handle quota exceeded with project-specific rate limiting
- [ ] Graceful handling of model version deprecation

#### Memory Management (NOT DONE)

- [ ] `StreamAccumulator` memory bounds enforcement
- [ ] Maximum accumulated text size (default: 10MB)
- [ ] Maximum tool call arguments size (default: 1MB per call)
- [ ] Memory warning emitted at 80% of bounds
- [ ] Graceful abort with partial response at 100% bounds
- [ ] Chunk slice pre-allocation based on model context window
- [ ] Periodic GC hint during long streams (every 1000 chunks)
- [ ] Memory metrics included in StreamMetrics

```go
type MemoryBounds struct {
    MaxTextSize     int64 // bytes
    MaxToolArgsSize int64 // bytes per tool call
    WarnThreshold   float64 // 0.8 = 80%
}
```

#### Observability & Telemetry (NOT DONE - core/metrics/)

**Note:** Sylk is a terminal CLI application, NOT a distributed system. We use in-memory metrics with CLI display and optional file export - NOT Prometheus or OpenTelemetry.

- [ ] `MetricsRegistry` singleton for in-memory metrics storage
- [ ] `Counter` type with atomic increment and thread-safe read
- [ ] `Gauge` type with atomic set/get
- [ ] `Histogram` type with bucketed distribution (p50, p95, p99 calculation)
- [ ] `Timer` type wrapping Histogram for duration tracking
- [ ] Streaming metrics tracked:
  - Stream duration (histogram)
  - Time to first token (histogram)
  - Chunks per stream (counter by provider, model)
  - Tokens per stream (counter by provider, model, direction)
  - Errors (counter by provider, error_type)
  - Early aborts (counter by provider, reason)
  - Backpressure events (counter)
  - Retries (counter by provider)
- [ ] `sylk metrics` CLI command displays current session metrics
- [ ] `sylk metrics --json` exports metrics as JSON to stdout
- [ ] `sylk metrics --export <file>` exports metrics to JSON file
- [ ] Metrics reset on session start (session-scoped)
- [ ] Optional: Historical metrics stored in SQLite for cross-session analysis
- [ ] Structured logging for stream lifecycle events (slog integration)
- [ ] Debug mode: log every chunk (configurable via `--verbose` or config)
- [ ] Correlation ID propagated through all log entries

#### Graceful Degradation (NOT DONE)

- [ ] Fallback to non-streaming Generate() when Stream() fails repeatedly
- [ ] Automatic fallback after circuit breaker opens
- [ ] Fallback decision logged with reason
- [ ] Metrics track fallback frequency
- [ ] User notification when operating in degraded mode
- [ ] Streaming auto-recovery probe (attempt streaming every 5 minutes when in fallback mode)

#### Concurrent Stream Management (NOT DONE)

- [ ] Maximum concurrent streams per session (default: N_CPU_CORES)
- [ ] Maximum concurrent streams globally (default: N_CPU_CORES * 2)
- [ ] Stream queuing when limits reached
- [ ] Priority-based stream scheduling (user > pipeline)
- [ ] Oldest stream preemption when at limit and high-priority arrives
- [ ] Stream count metrics per session and global

#### Testing Strategy (NOT DONE - core/providers/stream_test.go)

**Unit Tests:**
- [ ] StreamChunk serialization/deserialization
- [ ] StreamAccumulator handles all chunk types correctly
- [ ] StreamAccumulator thread-safety (concurrent Add calls)
- [ ] StreamToChannel converts all chunk types
- [ ] WrapStreamChunk creates valid messages
- [ ] Memory bounds enforcement triggers at correct thresholds

**Integration Tests:**
- [ ] End-to-end streaming with mock provider
- [ ] Backpressure simulation (slow consumer)
- [ ] Timeout handling (no chunks for extended period)
- [ ] Cancellation mid-stream
- [ ] Network error recovery
- [ ] Rate limit handling during stream
- [ ] Multiple concurrent streams

**Provider Tests (with mocked responses):**
- [ ] Anthropic event parsing
- [ ] OpenAI Responses API event parsing
- [ ] Google GenerateContentStream parsing
- [ ] Tool call accumulation across chunks
- [ ] Usage extraction from final chunk

**Stress Tests:**
- [ ] 100 concurrent streams
- [ ] Long-running stream (10 minutes)
- [ ] High-frequency chunks (simulated fast model)
- [ ] Memory usage under sustained load
- [ ] No goroutine leaks (verify with runtime.NumGoroutine)

**Chaos Tests:**
- [ ] Random network failures mid-stream
- [ ] Provider returning malformed JSON
- [ ] Sudden context cancellation
- [ ] Event bus congestion

#### Cross-Cutting Concerns (NOT DONE)

**Budget Integration:**
- [ ] Real-time token estimation during stream (approximate from text length)
- [ ] Budget check every N chunks (configurable, default: 10)
- [ ] Soft budget warning emitted when approaching limit
- [ ] Hard budget enforcement cancels stream
- [ ] Partial response usable after budget cancel

**Audit Trail:**
- [ ] Stream start event logged with request metadata
- [ ] Stream complete event logged with final metrics
- [ ] Stream error events logged with error details
- [ ] Tool calls logged with timing
- [ ] All events include correlation ID for tracing

**User Interrupt Handling:**
- [ ] Ctrl+C propagates to stream context
- [ ] Clean stream termination within 100ms of interrupt
- [ ] Partial response available after interrupt
- [ ] Interrupt event emitted to Signal Bus
- [ ] UI acknowledges interrupt visually

**Session Isolation:**
- [ ] Stream context includes session ID
- [ ] Streams routed only to same-session event bus subscribers
- [ ] Cross-session stream isolation verified in tests
- [ ] Session termination cancels all session streams

**Tests:**
- [ ] Configuration loads correctly from YAML
- [ ] Configuration validates bounds
- [ ] Retrier respects max attempts
- [ ] Circuit breaker state transitions correct
- [ ] Memory bounds trigger at correct thresholds
- [ ] In-memory metrics tracked correctly
- [ ] CLI metrics command displays correct values
- [ ] Graceful degradation fallback works
- [ ] Concurrent stream limits enforced
- [ ] All provider-specific edge cases handled
- [ ] Budget integration cancels streams correctly
- [ ] Audit trail complete and queryable

---

### 0.11 Signal Bus & Workflow Control

Pub/sub signal system for workflow coordination with ack support.

**Files to create:**
- `core/signal/types.go`
- `core/signal/bus.go`
- `core/signal/handler.go`
- `core/signal/checkpoint.go`
- `core/signal/bus_test.go`

**Parallelization:** Should complete before 0.12 (tight dependency).

**Acceptance Criteria:**

#### Signal Types
- [ ] `Signal` enum: PauseAll, ResumeAll, PausePipeline, ResumePipeline, CancelTask, AbortSession, QuotaWarning
- [ ] `SignalMessage` struct: ID, Signal, TargetID, Reason, Payload, RequiresAck, Timeout, SentAt
- [ ] `PausePayload` struct: Provider, ResumeAt, Attempt, Message
- [ ] `SignalAck` struct: SignalID, SubscriberID, AgentID, ReceivedAt, State, Checkpoint

#### Signal Bus
- [ ] `SignalBus` manages subscriptions and broadcasts
- [ ] `Subscribe(sub SignalSubscriber)` - register for signals
- [ ] `Unsubscribe(subscriberID string)` - remove subscription
- [ ] `Broadcast(msg SignalMessage) error` - send to all subscribers
- [ ] `Acknowledge(ack SignalAck)` - receive ack from subscriber
- [ ] Subscribers specify which signals they handle

#### Acknowledgment System
- [ ] Track pending acks per signal
- [ ] Wait for all subscribers to ack (with timeout)
- [ ] Retry to non-acked subscribers once
- [ ] Return error if ack timeout exceeded after retry
- [ ] Configurable timeout per signal (default: 5s)

#### Signal Subscriber
- [ ] `SignalSubscriber` struct: ID, AgentID, Signals (which to handle), Channel
- [ ] Buffered channel to prevent blocking broadcast
- [ ] Log warning if channel full (subscriber may be stuck)

**Tests:**
- [ ] Broadcast reaches all subscribers
- [ ] Only subscribed signals delivered
- [ ] Ack timeout triggers retry
- [ ] All acks received = success
- [ ] Unsubscribe stops delivery
- [ ] Concurrent broadcast/subscribe is safe

```go
// Required interfaces
type SignalBus interface {
    Subscribe(sub SignalSubscriber)
    Unsubscribe(subscriberID string)
    Broadcast(msg SignalMessage) error
    Acknowledge(ack SignalAck)
}

type SignalSubscriber struct {
    ID       string
    AgentID  string
    Signals  []Signal
    Channel  chan SignalMessage
}
```

---

### 0.12 Agent Signal Handler & Checkpointing

Signal handling in agents with checkpoint/resume capability.

**Files to create:**
- `core/signal/agent_handler.go`
- `core/signal/checkpoint.go`
- `core/signal/resume.go`
- `core/signal/agent_handler_test.go`

**Dependencies:** Requires 0.11 (Signal Bus).

**Acceptance Criteria:**

#### Agent State Machine
- [ ] `AgentState` enum: Idle, Running, Paused, Checkpointing, Resuming
- [ ] State transitions: Running → Checkpointing → Paused (on pause signal)
- [ ] State transitions: Paused → Resuming → Running (on resume signal)
- [ ] Only valid transitions allowed

#### Signal Handler
- [ ] `SignalHandler` per agent, listens for signals
- [ ] `Start()` begins listening goroutine
- [ ] `Stop()` stops listener and unsubscribes
- [ ] `State() AgentState` returns current state
- [ ] Automatic subscription to: PauseAll, ResumeAll, PausePipeline, ResumePipeline, CancelTask, AbortSession

#### Checkpoint Creation
- [ ] `Checkpoint` struct: ID, AgentID, AgentType, SessionID, PipelineID, TaskID, CreatedAt
- [ ] Include: LastOperation, OperationState (Pending/InProgress/Completed)
- [ ] Include: MessagesHash (for change detection), MessageCount
- [ ] Include: ToolsInProgress, PendingActions
- [ ] Include: RecentMessages (last 10 for context)
- [ ] Checkpoint created immediately on pause signal (before ack sent)

#### Resume Decision Logic
- [ ] `ResumeDecision` enum: Continue, Retry, Abort
- [ ] If MessagesHash changed → Retry (context changed while paused)
- [ ] If OperationState == InProgress → Retry (interrupted mid-operation)
- [ ] If ToolsInProgress not empty → Retry (tools may have partial state)
- [ ] Otherwise → Continue from checkpoint

#### Resume Execution
- [ ] `Continue`: Restore pending actions, clear checkpoint
- [ ] `Retry`: Re-queue last operation, clear checkpoint
- [ ] `Abort`: Report error, clear checkpoint, transition to Idle

**Tests:**
- [ ] Pause signal creates checkpoint and transitions to Paused
- [ ] Ack sent after checkpoint created
- [ ] Resume evaluates checkpoint correctly
- [ ] Context change detected via hash
- [ ] In-progress operation triggers retry
- [ ] Clean state allows continue

```go
// Required interface
type SignalHandler interface {
    Start()
    Stop()
    State() AgentState
    Checkpoint() *Checkpoint
}

type Checkpoint struct {
    ID              string
    AgentID         string
    AgentType       AgentType
    SessionID       string
    PipelineID      string
    TaskID          string
    CreatedAt       time.Time
    LastOperation   *Operation
    OperationState  OperationState
    MessagesHash    string
    MessageCount    int
    ToolsInProgress []ToolCall
    PendingActions  []Action
    RecentMessages  []Message
}
```

---

### 0.13 Token Budget Manager

Hierarchical token budget enforcement with full attribution.

**Files to create:**
- `core/llm/budget.go`
- `core/llm/tracker.go`
- `core/llm/attribution.go`
- `core/llm/budget_test.go`

**Dependencies:** Can start after 0.7 (needs types).

**Acceptance Criteria:**

#### Budget Hierarchy
- [x] `TokenBudget` struct with: GlobalLimit, SessionLimits map, TaskLimits map, ProviderLimits map
- [x] Limits are `int64` (-1 = unlimited)
- [x] `CheckBudget(req *LLMRequest) error` checks all applicable limits
- [x] Return specific error: ErrGlobalBudgetExceeded, ErrSessionBudgetExceeded, ErrTaskBudgetExceeded, ErrProviderBudgetExceeded

#### Usage Tracker
- [x] `UsageTracker` stores all usage records
- [x] `UsageRecord` struct: Timestamp, SessionID, PipelineID, TaskID, AgentID, AgentType, Provider, Model, InputTokens, OutputTokens, TotalTokens, Cost, Currency, Latency
- [x] `Record(record UsageRecord)` adds record
- [x] Thread-safe operations

#### Aggregation Queries
- [x] `TotalTokens() int64` - global total
- [x] `TokensBySession(sessionID string) int64`
- [x] `TokensByTask(taskID string) int64`
- [x] `TokensByProvider(provider string) int64`
- [x] `TokensByAgent(agentType AgentType) int64`
- [x] Use cached aggregates for O(1) lookups

#### Cost Calculation
- [x] Cost = (InputTokens / 1M * InputPrice) + (OutputTokens / 1M * OutputPrice)
- [x] Price loaded from `ModelInfo` in adapter
- [x] Currency always USD

#### Attribution Report
- [x] `AttributionReport` struct: SessionID, Period, TotalRequests, TotalTokens, TotalCost
- [x] Breakdown by: Provider, Agent, Pipeline, Task
- [x] Each breakdown includes: Requests, InputTokens, OutputTokens, TotalCost
- [x] `GenerateReport(filter UsageFilter) *AttributionReport`

**Tests:**
- [x] Budget check fails when limit exceeded
- [x] Hierarchical limits checked in order
- [x] Usage recorded correctly
- [x] Aggregates match sum of records
- [x] Cost calculation matches expected
- [x] Attribution report accurate

```go
// Required interfaces
type TokenBudget interface {
    CheckBudget(req *LLMRequest) error
    RecordUsage(record UsageRecord)
    SetGlobalLimit(limit int64)
    SetSessionLimit(sessionID string, limit int64)
    SetTaskLimit(taskID string, limit int64)
    SetProviderLimit(provider string, limit int64)
}

type UsageTracker interface {
    Record(record UsageRecord)
    TotalTokens() int64
    TokensBySession(sessionID string) int64
    TokensByTask(taskID string) int64
    TokensByProvider(provider string) int64
    TokensByAgent(agentType AgentType) int64
    GenerateReport(filter UsageFilter) *AttributionReport
}
```

---

### 0.14 Context Window Manager

Smart context management with overflow handling.

**Files to create:**
- `core/llm/context.go`
- `core/llm/context_test.go`

**Dependencies:** Requires 0.7 (adapters for token counting), integrates with Archivalist.

**Acceptance Criteria:**

#### Context Manager
- [x] `ContextManager` struct: model, maxContextTokens, reserveTokens (for response)
- [x] `PrepareContext(messages []Message, query string) ([]Message, error)`
- [x] If fits → return as-is
- [x] If overflow → apply strategies in order

#### Strategy 1: Smart Selection
- [x] `selectRelevant(messages, query, maxTokens)` keeps semantically relevant
- [x] Score messages by relevance to current query
- [x] Keep highest-scoring messages that fit budget
- [x] Preserve original message order in output

#### Strategy 2: Archivalist Handoff
- [x] Store overflow messages in Archivalist: `archivalist.StoreContextOverflow(messages)`
- [x] Messages available for RAG retrieval later
- [x] Log handoff for debugging

#### Strategy 3: Summarization
- [x] Use agent compactor to summarize overflow
- [x] Insert summary as system message: "[Context summary of N earlier messages] ..."
- [x] Preserve recent messages after summary

#### Context Reconstruction
- [x] Final context: SystemPrompt + Summary (if any) + RecentMessages
- [x] Total tokens must fit within available budget
- [x] Return error if even minimal context exceeds budget

**Tests:**
- [x] Small context returned unchanged
- [x] Large context triggers smart selection
- [x] Overflow stored in Archivalist
- [x] Summary generated for excess
- [x] Final context within token budget
- [x] Message order preserved

```go
// Required interface
type ContextManager interface {
    PrepareContext(messages []Message, query string) ([]Message, error)
    MaxTokens() int
    AvailableTokens() int  // MaxTokens - ReserveTokens
}
```

---

### 0.15 LLM Request Gate (Integration)

Central gate combining queue, budget, rate limiting, and routing.

**Files to create:**
- `core/llm/gate.go`
- `core/llm/gate_test.go`

**Dependencies:** Requires 0.7, 0.8, 0.9, 0.10, 0.13, 0.14.

**Acceptance Criteria:**

#### Request Flow
1. Request arrives at gate
2. Determine priority (user-invoked vs task-phase)
3. Check token budget (reject if exceeded)
4. Queue request by priority
5. Dequeue worker checks rate limit (wait if exceeded)
6. Prepare context (smart selection/summarize if needed)
7. Route to provider adapter
8. Record usage on response
9. Return response to caller

#### Gate Interface
- [ ] `LLMGate` struct combining all components
- [ ] `Submit(ctx context.Context, req *LLMRequest) (*CompletionResponse, error)`
- [ ] `SubmitStream(ctx context.Context, req *LLMRequest) (<-chan StreamChunk, error)`
- [ ] Request attribution populated before response returned

#### Worker Pool
- [ ] Configurable number of workers (default: 4)
- [ ] Workers dequeue from priority queue
- [ ] Workers respect rate limits (block if needed)
- [ ] Workers handle context preparation
- [ ] Workers call provider adapters

#### Error Handling
- [ ] Budget exceeded → immediate reject with `ErrBudgetExceeded`
- [ ] Rate limited → wait for backoff (not reject)
- [ ] Provider error → return error to caller
- [ ] Context error → return error to caller

#### Metrics
- [ ] Track: requests submitted, requests completed, requests failed
- [ ] Track: average latency, p99 latency
- [ ] Track: queue depth, wait time
- [ ] Expose via `Stats() GateStats`

**Tests:**
- [ ] Request flows through all stages
- [ ] Priority ordering respected
- [ ] Budget rejection works
- [ ] Rate limit causes wait (not reject)
- [ ] Context prepared correctly
- [ ] Usage recorded on completion
- [ ] Multiple workers process concurrently

```go
// Required interface
type LLMGate interface {
    Submit(ctx context.Context, req *LLMRequest) (*CompletionResponse, error)
    SubmitStream(ctx context.Context, req *LLMRequest) (<-chan StreamChunk, error)
    Stats() GateStats
    Close() error
}
```

---

### 0.15a CLI Metrics Infrastructure

In-memory metrics system for terminal CLI application. NOT Prometheus/OpenTelemetry - those are for distributed systems with HTTP endpoints.

**Files to create:**
- `core/metrics/types.go`
- `core/metrics/registry.go`
- `core/metrics/counter.go`
- `core/metrics/gauge.go`
- `core/metrics/histogram.go`
- `core/metrics/timer.go`
- `core/metrics/export.go`
- `core/metrics/storage.go`

**Design Principles:**
- All metrics are in-memory with atomic operations
- No external dependencies (no Prometheus, no OpenTelemetry)
- Metrics are session-scoped by default
- Optional SQLite persistence for historical analysis
- CLI commands for viewing and exporting

**Acceptance Criteria:**

#### Core Types (`core/metrics/types.go`)
- [ ] `Metric` interface with `Name()`, `Type()`, `Value()`, `Labels()`, `Reset()`
- [ ] `MetricType` enum: Counter, Gauge, Histogram, Timer
- [ ] `Labels` map type for dimensional metrics
- [ ] `MetricSnapshot` struct for point-in-time capture
- [ ] `MetricsSummary` struct for CLI display

#### Registry (`core/metrics/registry.go`)
- [ ] `MetricsRegistry` singleton pattern
- [ ] `Register(name string, metric Metric)` adds metric
- [ ] `Get(name string) Metric` retrieves metric
- [ ] `GetByPrefix(prefix string) []Metric` retrieves all with prefix
- [ ] `Snapshot() map[string]MetricSnapshot` captures all metrics
- [ ] `Reset()` clears all metrics (for new session)
- [ ] Thread-safe operations throughout
- [ ] Namespacing: metrics prefixed by subsystem (e.g., "stream.", "tool.", "llm.")

#### Counter (`core/metrics/counter.go`)
- [ ] `Counter` struct with atomic int64 value
- [ ] `Inc()` increments by 1
- [ ] `Add(delta int64)` increments by delta
- [ ] `Value() int64` returns current value
- [ ] `Reset()` sets to 0
- [ ] Optional labels for dimensional counters
- [ ] `NewCounter(name string, labels ...string) *Counter`

#### Gauge (`core/metrics/gauge.go`)
- [ ] `Gauge` struct with atomic float64 value
- [ ] `Set(value float64)` sets value
- [ ] `Inc()` / `Dec()` for convenience
- [ ] `Add(delta float64)` / `Sub(delta float64)`
- [ ] `Value() float64` returns current value
- [ ] `NewGauge(name string, labels ...string) *Gauge`

#### Histogram (`core/metrics/histogram.go`)
- [ ] `Histogram` struct with sorted slice of observations
- [ ] `Observe(value float64)` adds observation
- [ ] `Count() int64` returns number of observations
- [ ] `Sum() float64` returns sum of observations
- [ ] `Percentile(p float64) float64` calculates percentile (0.5, 0.95, 0.99)
- [ ] `Min() float64` / `Max() float64`
- [ ] `Mean() float64`
- [ ] Configurable max observations (default: 10000, FIFO eviction)
- [ ] `NewHistogram(name string, labels ...string) *Histogram`

#### Timer (`core/metrics/timer.go`)
- [ ] `Timer` wraps Histogram for duration tracking
- [ ] `Start() func()` returns stop function that records duration
- [ ] `Record(d time.Duration)` records duration directly
- [ ] `P50() time.Duration` / `P95()` / `P99()` convenience methods
- [ ] `NewTimer(name string, labels ...string) *Timer`

```go
// Usage example
timer := metrics.NewTimer("stream.duration")
stop := timer.Start()
// ... do work ...
stop() // automatically records duration

// Or manual
timer.Record(time.Since(startTime))
```

#### Export (`core/metrics/export.go`)
- [ ] `ExportJSON() ([]byte, error)` exports all metrics as JSON
- [ ] `ExportToFile(path string) error` writes JSON to file
- [ ] `FormatForCLI() string` formats metrics for terminal display
- [ ] `FormatTable(metrics []Metric) string` tabular format
- [ ] Histograms export with p50/p95/p99/min/max/mean

#### SQLite Storage (`core/metrics/storage.go`)
- [ ] `MetricsStore` interface for persistence
- [ ] `SQLiteMetricsStore` implementation
- [ ] `Save(sessionID string, snapshot map[string]MetricSnapshot) error`
- [ ] `Load(sessionID string) (map[string]MetricSnapshot, error)`
- [ ] `Query(filter MetricsFilter) ([]SessionMetrics, error)`
- [ ] `Aggregate(metricName string, period time.Duration) []AggregatedMetric`
- [ ] Schema migration on first run
- [ ] Automatic cleanup of old data (configurable retention)

#### Pre-Registered Metrics
- [ ] `stream.duration` (timer) - LLM stream duration
- [ ] `stream.time_to_first_token` (timer) - TTFT
- [ ] `stream.chunks` (counter, labels: provider, model)
- [ ] `stream.tokens` (counter, labels: provider, model, direction)
- [ ] `stream.errors` (counter, labels: provider, error_type)
- [ ] `stream.early_aborts` (counter, labels: provider, reason)
- [ ] `llm.requests` (counter, labels: provider, model, agent)
- [ ] `llm.tokens.input` (counter, labels: provider, model)
- [ ] `llm.tokens.output` (counter, labels: provider, model)
- [ ] `tool.loads` (counter, labels: agent, tool)
- [ ] `tool.load_latency` (timer, labels: agent)
- [ ] `tool.tokens_saved` (counter, labels: agent)
- [ ] `session.duration` (gauge) - current session duration
- [ ] `session.pipelines` (gauge) - active pipelines

**Tests:**
- [ ] Counter increments atomically under concurrent access
- [ ] Gauge set/get is thread-safe
- [ ] Histogram percentiles calculated correctly
- [ ] Timer records durations accurately
- [ ] Registry get/set is thread-safe
- [ ] JSON export produces valid JSON
- [ ] SQLite storage persists and retrieves correctly
- [ ] CLI format is human-readable

```go
// Example metric registration
func init() {
    metrics.Register("stream.duration", metrics.NewTimer("stream.duration"))
    metrics.Register("stream.tokens", metrics.NewCounter("stream.tokens", "provider", "model", "direction"))
}

// Example usage
metrics.Get("stream.duration").(*metrics.Timer).Record(elapsed)
metrics.Get("stream.tokens").(*metrics.Counter).Add(tokenCount)
```

---

### 0.16 Usage CLI Commands

CLI commands for viewing usage and attribution.

**Files to create:**
- `cmd/usage.go`

**Dependencies:** Requires 0.13 (tracker/attribution).

**Acceptance Criteria:**

#### Usage Command
- [ ] `sylk usage` - current session summary
- [ ] `sylk usage --session <id>` - specific session breakdown
- [ ] `sylk usage --provider <name>` - provider-specific usage
- [ ] `sylk usage --period <day|week|month>` - time-filtered usage
- [ ] `sylk usage --detailed` - full attribution breakdown

#### Output Format
- [ ] Default: human-readable table
- [ ] `--json` flag for JSON output
- [ ] Include: requests, input tokens, output tokens, cost
- [ ] Breakdown by provider, agent, task as applicable

#### Cost Display
- [ ] Format cost as currency: "$1.23"
- [ ] Show per-provider costs
- [ ] Show estimated vs actual (if budget set)

**Tests:**
- [ ] All command variations work
- [ ] JSON output valid
- [ ] Cost formatting correct
- [ ] Empty usage handled gracefully

---

### 0.17 Goroutine Model & Agent Lifecycle (DONE)

Implements the core goroutine architecture: one goroutine per standalone agent, one goroutine per pipeline.

**Files to create:**
- `core/concurrency/agent_runner.go`
- `core/concurrency/lifecycle.go`

**Dependencies:** Requires 0.11 (Signal Bus) for shutdown coordination.

**Acceptance Criteria:**

#### Standalone Agent Goroutines
- [x] `AgentRunner` struct manages goroutine lifecycle
- [x] `Start(ctx context.Context) error` - spawns goroutine
- [x] `Stop() error` - graceful shutdown with timeout
- [x] `IsRunning() bool` - status check
- [x] Standalone agents: Guide, Architect, Orchestrator, Librarian, Archivalist, Academic
- [x] No concurrency limit on standalone agents (they self-regulate via LLM queue)

#### Pipeline Goroutines
- [x] Pipeline spawns single goroutine for all phases
- [x] Sequential internal execution: Inspector → Tester → Worker → Verify
- [x] `PipelineRunner` wraps pipeline lifecycle
- [x] Clean shutdown on context cancellation
- [x] Panic recovery with error reporting

#### Lifecycle States
- [x] States: `Created`, `Starting`, `Running`, `Stopping`, `Stopped`, `Failed`
- [x] State transitions are atomic
- [x] State change events published to Signal Bus
- [x] `WaitForState(state, timeout) error` - blocking wait

**Tests:**
- [x] Agent starts and stops cleanly
- [x] Concurrent start/stop safe
- [x] Panic in goroutine captured and reported
- [x] Context cancellation triggers shutdown

---

### 0.18 Pipeline Scheduler (DONE)

Implements N_CPU_CORES bounded pipeline scheduling with priority ordering.

**Files to create:**
- `core/concurrency/scheduler.go`
- `core/concurrency/priority_queue.go`

**Dependencies:** Requires 0.17 (Agent Lifecycle).

**Acceptance Criteria:**

#### Scheduler Core
- [x] `PipelineScheduler` struct with configurable `maxConcurrent`
- [x] Default `maxConcurrent = runtime.NumCPU()`
- [x] Active map: `map[string]*Pipeline` (currently executing)
- [x] Ready queue: priority-ordered pipelines awaiting slots
- [x] Waiting map: pipelines blocked on dependencies

#### Priority Queue
- [x] Priority by: Architect-assigned priority (primary), spawn time (secondary)
- [x] `Push(pipeline *Pipeline)`
- [x] `Pop() *Pipeline` - returns highest priority
- [x] `Remove(pipelineID string)` - for cancellation
- [x] `Len() int`

#### Scheduling Logic
- [x] `Schedule(pipeline *Pipeline) error` - enqueue for execution
- [x] Eager scheduling: start immediately when slot available
- [x] `NotifyComplete(pipelineID string)` - release slot, schedule next
- [x] `Cancel(pipelineID string)` - remove from any queue/active

#### Dependency Management
- [x] Pipelines specify dependencies (other pipeline IDs)
- [x] Pipeline moves ready → waiting if dependencies incomplete
- [x] Dependency completion triggers re-evaluation
- [x] Circular dependency detection with error

**Tests:**
- [x] Respects maxConcurrent limit
- [x] Priority ordering correct
- [x] Dependencies block correctly
- [x] Completion triggers next pipeline
- [x] Cancellation cleans up properly

---

### 0.19 Dual Queue LLM Gate

Implements separate queues for user-interactive and pipeline requests with user priority.

**Files to create:**
- `core/concurrency/llm_gate.go`
- `core/concurrency/unbounded_queue.go`

**Dependencies:** Requires 0.9 (Priority Queue), 0.10 (Rate Limiter).

**Acceptance Criteria:**

#### Dual Queue Architecture
- [ ] `DualQueueGate` struct
- [ ] `userQueue`: unbounded queue for user requests (NEVER reject)
- [ ] `pipelineQueue`: bounded priority queue for pipeline requests
- [ ] User queue ALWAYS drains first before pipeline queue

#### User Queue (Unbounded)
- [ ] `UnboundedQueue` with dynamic growth
- [ ] Initial capacity: 16, grows as needed
- [ ] No upper limit (user responsiveness is paramount)
- [ ] FIFO ordering within user queue

#### Pipeline Queue (Bounded + Priority)
- [ ] Max size configurable (default: 1000)
- [ ] Priority ordering matches scheduler priority
- [ ] Rejection when full (pipeline retries)
- [ ] Preemption support: user request can pause in-flight pipeline request

#### Preemption Logic
- [ ] `Preempt(pipelineRequestID string) error`
- [ ] In-flight pipeline request paused (not cancelled)
- [ ] User request proceeds immediately
- [ ] Pipeline request resumes after user completes
- [ ] Preemption event published to Signal Bus

#### Gate Interface
- [ ] `Submit(request *LLMRequest) (chan *LLMResponse, error)`
- [ ] `SubmitUser(request *LLMRequest) (chan *LLMResponse, error)` - always succeeds
- [ ] `Cancel(requestID string)`
- [ ] `Stats() GateStats`

**Tests:**
- [ ] User requests never rejected
- [ ] User requests drain before pipeline
- [ ] Preemption works correctly
- [ ] Pipeline queue respects bounds
- [ ] Priority ordering in pipeline queue

---

### 0.20 Staging Manager

Implements copy-on-write file staging for pipeline isolation.

**Files to create:**
- `core/concurrency/staging.go`
- `core/concurrency/merge.go`

**Dependencies:** None (can be built in parallel with 0.17-0.19).

**Acceptance Criteria:**

#### Staging Directory
- [ ] `StagingManager` creates isolated directories per pipeline
- [ ] Location: `~/.sylk/staging/<pipeline-id>/`
- [ ] Copy files on first write
- [ ] Track original file hashes for conflict detection

#### File Operations
- [ ] `CreateStaging(pipelineID string) (*PipelineStaging, error)`
- [ ] `ReadFile(staging, path) ([]byte, error)` - reads from original or staged
- [ ] `WriteFile(staging, path, content) error` - always writes to staging
- [ ] `DeleteFile(staging, path) error` - marks as deleted in staging
- [ ] `ListModified(staging) []string` - all changed files

#### Conflict Detection
- [ ] Store original hash when file first staged
- [ ] On merge, compare current original hash to stored hash
- [ ] Conflict if original changed while pipeline working
- [ ] `CheckConflicts(staging) ([]Conflict, error)`

#### Merge Strategy
- [ ] `Merge(staging) error` - applies staged changes to working directory
- [ ] Atomic: all-or-nothing via temp + rename
- [ ] On conflict: reject merge, return conflicts
- [ ] On success: clean up staging directory
- [ ] `Abort(staging) error` - discard staging, clean up

#### Cleanup
- [ ] Auto-cleanup on pipeline completion (success or failure)
- [ ] Periodic cleanup of orphaned staging dirs (crash recovery)
- [ ] Configurable max staging age (default: 24h)

**Tests:**
- [ ] Staging isolation works
- [ ] Conflict detection correct
- [ ] Merge is atomic
- [ ] Concurrent pipeline staging independent
- [ ] Cleanup removes directories

---

### 0.21 Adaptive Channels (DONE)

Implements auto-resizing channels with overflow handling for user responsiveness.

**Files to create:**
- `core/concurrency/adaptive_channel.go`

**Dependencies:** None (can be built in parallel with 0.17-0.20).

**Acceptance Criteria:**

#### Adaptive Channel Core
- [x] `AdaptiveChannel[T]` generic struct
- [x] `minSize`: minimum buffer (default: 16)
- [x] `maxSize`: maximum buffer (default: 4096)
- [x] `currentSize`: tracks current allocation
- [x] Overflow slice for beyond-max scenarios (user paths only)

#### Send Operations
- [x] `Send(msg T) error` - sends with auto-resize
- [x] `SendTimeout(msg T, timeout time.Duration) error` - with timeout
- [x] If channel full and under maxSize: resize up (2x)
- [x] If at maxSize: use overflow (user channels) or timeout (other channels)
- [x] Never block indefinitely

#### Receive Operations
- [x] `Receive() (T, error)` - blocking receive
- [x] `ReceiveTimeout(timeout time.Duration) (T, error)`
- [x] `TryReceive() (T, bool)` - non-blocking
- [x] Drain overflow before channel

#### Auto-Resize Logic
- [x] Resize up: when >80% full
- [x] Resize down: when <20% full for sustained period (1 minute)
- [x] Minimum stays at minSize
- [x] Resize is transparent to senders/receivers

#### Metrics
- [x] `Size() int` - current buffer size
- [x] `Len() int` - current message count
- [x] `OverflowLen() int` - overflow queue length
- [x] `Stats() ChannelStats` - hit rates, resize counts

**Tests:**
- [x] Auto-resize up works
- [x] Auto-resize down works
- [x] Overflow handles burst
- [x] Timeout behavior correct
- [x] No message loss

---

### 0.22 Write-Ahead Log

Implements WAL for crash recovery durability.

**Files to create:**
- `core/concurrency/wal.go`
- `core/concurrency/wal_entry.go`

**Dependencies:** None (can be built in parallel with 0.17-0.21).

**Acceptance Criteria:**

#### WAL Core
- [ ] `WriteAheadLog` struct
- [ ] Location: `~/.sylk/wal/`
- [ ] Segment files: `wal-<sequence>.log`
- [ ] Max segment size: 64MB (configurable)
- [ ] Automatic segment rotation

#### Entry Format
- [ ] `WALEntry` struct: sequence, timestamp, type, payload
- [ ] Entry types: `Checkpoint`, `StateChange`, `LLMRequest`, `LLMResponse`, `FileChange`
- [ ] Binary encoding with length prefix
- [ ] CRC32 checksum per entry

#### Write Operations
- [ ] `Append(entry *WALEntry) (uint64, error)` - returns sequence number
- [ ] `Sync() error` - force fsync
- [ ] Configurable sync mode: every write, batched, or periodic
- [ ] Default: batched with 100ms max delay

#### Read Operations
- [ ] `ReadFrom(sequence uint64) ([]*WALEntry, error)`
- [ ] `ReadAll() ([]*WALEntry, error)`
- [ ] Skip corrupted entries with warning
- [ ] Iterator interface for streaming

#### Segment Management
- [ ] `Truncate(beforeSequence uint64) error` - remove old segments
- [ ] Segments older than last checkpoint safe to remove
- [ ] Automatic truncation after successful checkpoint

**Tests:**
- [ ] Entries survive crash (kill -9 test)
- [ ] CRC detects corruption
- [ ] Segment rotation works
- [ ] Truncation cleans old segments
- [ ] Concurrent append safe

---

### 0.23 Checkpointer

Implements continuous checkpointing with 5-second intervals.

**Files to create:**
- `core/concurrency/checkpointer.go`
- `core/concurrency/checkpoint.go`

**Dependencies:** Requires 0.22 (WAL).

**Acceptance Criteria:**

#### Checkpoint Content
- [ ] `Checkpoint` struct captures full system state
- [ ] Pipeline states (active, waiting, ready queues)
- [ ] LLM queue states (user queue, pipeline queue)
- [ ] Staging directory manifest (what files modified)
- [ ] Agent states (current task, context summary)
- [ ] WAL sequence number (replay point)

#### Checkpointer Service
- [ ] `Checkpointer` runs background goroutine
- [ ] Default interval: 5 seconds
- [ ] `TriggerCheckpoint()` - force immediate checkpoint
- [ ] `Stop()` - graceful shutdown with final checkpoint

#### Atomic Writes
- [ ] Write to temp file first
- [ ] Validate temp file readable
- [ ] Atomic rename to checkpoint location
- [ ] Keep previous checkpoint until new one confirmed
- [ ] Location: `~/.sylk/checkpoints/checkpoint-<timestamp>.json`

#### Checkpoint Validation
- [ ] JSON schema validation on read
- [ ] Version field for forward compatibility
- [ ] Hash of content for integrity

#### Signal Integration
- [ ] Subscribe to pause signals
- [ ] Force checkpoint on pause signal before ack
- [ ] Checkpoint on graceful shutdown

**Tests:**
- [ ] Checkpoint created at interval
- [ ] Atomic write (no partial checkpoints)
- [ ] Pause triggers checkpoint
- [ ] Corrupt checkpoint detected
- [ ] Old checkpoints cleaned up

---

### 0.24 Recovery Manager

Implements crash recovery using WAL and checkpoints.

**Files to create:**
- `core/concurrency/recovery.go`

**Dependencies:** Requires 0.22 (WAL), 0.23 (Checkpointer).

**Acceptance Criteria:**

#### Recovery Flow
- [x] `RecoveryManager` struct
- [x] `Recover() (*RecoveryResult, error)` - main entry point
- [x] Detect if recovery needed (unclean shutdown marker)
- [x] Load latest valid checkpoint
- [x] Replay WAL from checkpoint sequence

#### Checkpoint Loading
- [x] Find latest checkpoint file
- [x] Validate checkpoint integrity
- [x] If corrupt, try previous checkpoint
- [x] If no valid checkpoint, start fresh with warning

#### WAL Replay
- [x] Replay entries after checkpoint sequence
- [x] Apply state changes in order
- [x] Skip already-applied entries (idempotent replay)
- [x] Stop at end of WAL

#### Pipeline Recovery
- [x] Completed pipelines: no action
- [x] In-progress pipelines: restart from last phase
- [x] Waiting pipelines: re-enqueue
- [x] Staging directories: validate or recreate

#### Recovery Result
- [x] `RecoveryResult` struct with recovered state
- [x] List of recovered pipelines
- [x] List of failed recoveries (with reasons)
- [x] WAL entries replayed count

#### Shutdown Marker
- [x] Write marker on startup
- [x] Remove marker on clean shutdown
- [x] Presence of marker = recovery needed

**Tests:**
- [x] Clean shutdown needs no recovery
- [x] Crash recovery restores state
- [x] Corrupt checkpoint handled
- [x] WAL replay idempotent
- [x] Pipeline recovery correct

---

### 0.25 Concurrency Integration Tests

End-to-end tests for concurrency system.

**Files to create:**
- `core/concurrency/integration_test.go`

**Dependencies:** Requires 0.17-0.24.

**Acceptance Criteria:**

#### Pipeline Scheduling Tests
- [ ] N pipelines scheduled with N_CPU_CORES limit
- [ ] Priority ordering respected under load
- [ ] Dependency chains execute correctly
- [ ] Cancellation cleans up properly

#### LLM Gate Tests
- [ ] User requests always succeed during pipeline load
- [ ] Preemption works under high load
- [ ] Pipeline queue bounds respected
- [ ] Rate limiting integrates correctly

#### File Staging Tests
- [ ] Multiple pipelines modify same file independently
- [ ] Conflict detection on concurrent modification
- [ ] Merge succeeds when no conflicts
- [ ] Abort cleans up correctly

#### Crash Recovery Tests
- [ ] Simulate crash during pipeline execution
- [ ] Verify recovery restores state
- [ ] Verify WAL replay correct
- [ ] Verify staging directories recovered

#### Stress Tests
- [ ] 100 concurrent pipelines
- [ ] Rapid user request bursts
- [ ] Memory usage under control
- [ ] No goroutine leaks

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] Performance meets targets (<100ms scheduling overhead)

---

### Concurrency Parallelization Notes

The following tasks can be executed in parallel:
- **Group C1**: 0.20, 0.21, 0.22 (no dependencies between them)
- **Group C2**: 0.17 → 0.18 → 0.19 (sequential dependency)
- **Group C3**: 0.22 → 0.23 → 0.24 (sequential dependency)

Optimal execution order:
1. Start 0.17, 0.20, 0.21, 0.22 in parallel
2. When 0.17 completes → start 0.18
3. When 0.18 completes → start 0.19
4. When 0.22 completes → start 0.23
5. When 0.23 completes → start 0.24
6. When 0.19 and 0.24 complete → start 0.25

---

### 0.26 Error Type System

Implements 5-tier error taxonomy with classification and handling behavior.

**Files to create:**
- `core/errors/types.go`
- `core/errors/classifier.go`
- `core/errors/config.go`

**Dependencies:** None (can start immediately, parallel with 0.17-0.25).

**Acceptance Criteria:**

#### Error Tiers
- [x] `ErrorTier` enum: Transient, Permanent, UserFixable, ExternalRateLimit, ExternalDegrading
- [x] Each tier has defined behavior (retry policy, notification, escalation)
- [x] Error wrapping preserves tier through call stack

#### Error Classifier
- [x] `ErrorClassifier` struct with configurable patterns
- [x] `Classify(error) ErrorTier` - determines tier from error
- [x] HTTP status code detection (429 → RateLimit, 5xx → Degrading)
- [x] Pattern matching for known error types (regex configurable)
- [x] Default to Permanent for unknown errors (fail fast, don't mask)

#### Configuration
- [x] `ErrorClassifierConfig` struct with YAML tags
- [x] `transient_patterns`: regex list for transient errors
- [x] `permanent_patterns`: regex list for permanent errors
- [x] `user_fixable_patterns`: regex list for user-fixable errors
- [x] `rate_limit_statuses`: HTTP codes (default: [429])
- [x] `degrading_statuses`: HTTP codes (default: [500, 502, 503, 504])

**Tests:**
- [x] All error tiers classified correctly
- [x] HTTP status codes map to correct tiers
- [x] Pattern matching works
- [x] Unknown errors default to Permanent
- [x] Configuration loading works

---

### 0.27 Transient Error Tracker

Implements silent retry with frequency-based notification for transient errors.

**Files to create:**
- `core/errors/transient_tracker.go`

**Dependencies:** Requires 0.26 (Error Types), 0.11 (Signal Bus).

**Acceptance Criteria:**

#### Frequency Tracking
- [x] `TransientTracker` struct with sliding window
- [x] `Record(error) bool` - returns true if notification needed
- [x] Configurable `frequency_count` (default: 3)
- [x] Configurable `frequency_window` (default: 10s)
- [x] Configurable `notification_cooldown` (default: 60s)

#### Sliding Window
- [x] Prune errors outside window on each record
- [x] Thread-safe with mutex
- [x] Memory-bounded (only track timestamps, not full errors)

#### Notification Logic
- [x] Return false (silent) for isolated transient errors
- [x] Return true when frequency threshold exceeded
- [x] Respect cooldown after notification
- [ ] Publish SignalTransientFrequencyAlert to Signal Bus when true

**Tests:**
- [x] Single transient error is silent
- [x] N errors in window triggers notification
- [x] Errors outside window don't count
- [x] Cooldown prevents repeated notifications
- [x] Thread-safe under concurrent access

---

### 0.28 Retry Policies

Implements per-tier retry behavior with exponential backoff.

**Files to create:**
- `core/errors/retry.go`
- `core/errors/backoff.go`

**Dependencies:** Requires 0.26 (Error Types).

**Acceptance Criteria:**

#### Retry Policy
- [x] `RetryPolicy` struct per error tier
- [x] `max_attempts`: maximum retry count
- [x] `initial_delay`: starting backoff duration
- [x] `max_delay`: maximum backoff duration
- [x] `multiplier`: backoff multiplier (default: 2.0)
- [x] `use_retry_after`: respect Retry-After header (for RateLimit)

#### Default Policies (all configurable)
- [x] Transient: 5 attempts, 100ms initial, 5s max, 2.0x
- [x] ExternalRateLimit: 10 attempts, 1s initial, 60s max, 2.0x, use Retry-After
- [x] ExternalDegrading: 3 attempts, 5s initial, 30s max, 2.0x
- [x] Permanent: 0 attempts (no retry)
- [x] UserFixable: 0 attempts (no retry)

#### Backoff Calculator
- [x] `CalculateDelay(attempt int, policy RetryPolicy) time.Duration`
- [x] Exponential: delay = initial * (multiplier ^ attempt)
- [x] Capped at max_delay
- [x] Jitter option (±10%) to prevent thundering herd

#### Retry Executor
- [x] `RetryWithPolicy(ctx, fn, tier) (result, error)`
- [x] Respects context cancellation
- [x] Returns last error if all attempts fail
- [ ] Publishes retry events to Signal Bus (for UI countdown)

**Tests:**
- [x] Exponential backoff calculates correctly
- [x] Max delay caps backoff
- [x] Jitter stays within bounds
- [x] Context cancellation stops retries
- [x] Retry-After header respected

---

### 0.29 Circuit Breaker

Implements per-resource circuit breakers with configurable thresholds.

**Files to create:**
- `core/errors/circuit_breaker.go`
- `core/errors/circuit_registry.go`

**Dependencies:** Requires 0.26 (Error Types), 0.11 (Signal Bus).

**Acceptance Criteria:**

#### Circuit States
- [x] `CircuitState` enum: Closed, Open, HalfOpen
- [x] State transitions are atomic
- [ ] State change publishes to Signal Bus

#### Circuit Breaker
- [x] `CircuitBreaker` struct per resource
- [x] `RecordResult(success bool)` - tracks outcome
- [x] `Allow() bool` - checks if request should proceed
- [x] `ForceReset()` - manual reset (user override)

#### Trip Conditions (configurable per resource)
- [x] Count-based: N consecutive failures
- [x] Rate-based: failure rate in sliding window
- [x] Trip on EITHER condition met

#### Per-Resource Defaults
- [x] LLM: 5 consecutive, 50% rate, 20 window, 30s cooldown
- [x] File: 2 consecutive, 30% rate, 10 window, 10s cooldown
- [x] Network: 3 consecutive, 50% rate, 20 window, 30s cooldown
- [x] Subprocess: 3 consecutive, 40% rate, 15 window, 20s cooldown

#### Half-Open Probe
- [x] After cooldown, allow single probe request
- [x] Success → close circuit
- [x] Failure → back to open, reset cooldown
- [x] Configurable success_threshold for closing (default: 3)

#### Registry
- [x] `CircuitRegistry` manages all circuit breakers
- [x] `Get(resourceID string) *CircuitBreaker`
- [x] Lazy initialization with defaults
- [x] Status endpoint for all circuits

**Tests:**
- [x] Count-based trip works
- [x] Rate-based trip works
- [x] Half-open probe works
- [x] Manual reset works
- [x] Cooldown timing correct
- [x] Multiple circuits independent

---

### 0.30 Escalation Chain

Implements Agent → Architect → User escalation with token-budgeted workarounds.

**Files to create:**
- `core/errors/escalation.go`
- `core/errors/workaround_budget.go`
- `core/errors/remedy.go`

**Dependencies:** Requires 0.26-0.29 (Error System), 0.11 (Signal Bus).

**Acceptance Criteria:**

#### Workaround Budget
- [x] `WorkaroundBudget` struct tracks token spending
- [x] Configurable `max_tokens` (default: 1000)
- [x] `CanSpend(tokens int) bool`
- [x] `Spend(tokens int)`
- [x] `Reset()` on successful recovery
- [x] Per-error-type budget overrides (optional)

#### Escalation Manager
- [x] `EscalationManager` coordinates escalation flow
- [x] `Escalate(error, context) (*Remedy, error)`
- [x] Level 1: Agent self-recovery (retry based on tier)
- [x] Level 2: Architect workaround (token-budgeted)
- [x] Level 3: User decision (present remedies)

#### Critical Error Fast-Path
- [x] ExternalDegrading errors skip Level 2
- [x] Immediately notify user + show "working on it"
- [x] Architect works in background while user informed
- [x] Present remedy when ready (or timeout)

#### Remedy Structure
- [x] `Remedy` struct: description, action, confidence
- [x] Multiple remedies ranked by likelihood
- [x] User can accept, reject, modify, or abort
- [ ] Remedy execution tracked in Archivalist

**Tests:**
- [x] Self-recovery works for transient
- [x] Architect workaround within budget works
- [x] Budget exceeded escalates to user
- [x] Critical errors fast-path to user
- [x] Remedy presentation correct

---

### 0.31 Retry Briefing System

Implements Archivalist-powered context briefing for retry attempts.

**Files to create:**
- `core/errors/retry_briefing.go`

**Dependencies:** Requires 0.30 (Escalation), Archivalist agent.

**Acceptance Criteria:**

#### Retry Briefing
- [x] `RetryBriefing` struct: prior attempts, failure analysis, suggested approach, avoid patterns
- [x] `AttemptSummary`: timestamp, approach, error, tokens spent, phases complete

#### Briefing Preparation
- [x] `PrepareRetryBriefing(ctx, pipelineID, error) (*RetryBriefing, error)`
- [x] Query Archivalist for prior attempts on this task
- [x] Analyze failure patterns across attempts
- [x] Generate suggested approach avoiding prior failures
- [x] List explicit "avoid patterns" from failures

#### Agent Integration
- [x] Briefing injected into agent context before retry
- [x] Agent sees: "Attempt 2. Prior attempt failed due to X. Avoid Y. Try Z."
- [x] Prevents token waste from rediscovery
- [x] Archivalist non-fatal: proceed without history if unavailable

**Tests:**
- [x] Briefing generated from Archivalist data
- [x] Avoid patterns extracted correctly
- [x] Suggested approach differs from failed attempts
- [x] Graceful degradation without Archivalist

---

### 0.32 Rollback Manager

Implements multi-layer rollback with history preservation.

**Files to create:**
- `core/errors/rollback.go`
- `core/errors/rollback_receipt.go`

**Dependencies:** Requires 0.20 (Staging), 0.22-0.24 (WAL/Checkpoint), Archivalist.

**Acceptance Criteria:**

#### Rollback Layers
- [x] Layer 1: File staging (discard staging dir)
- [x] Layer 2: Agent state (reset context to checkpoint)
- [x] Layer 3: Git state (local: reset --soft, pushed: user decision)
- [x] Layer 4: External state (log calls, flag as inconsistent)

#### Rollback Execution
- [x] `RollbackManager` coordinates all layers
- [x] `Rollback(pipelineID, options) (*RollbackReceipt, error)`
- [x] Atomic where possible (staging, git local)
- [x] User decision required for pushed commits
- [x] Never auto-revert pushed changes

#### History Preservation
- [ ] Archivalist marks attempt as "rolled_back" (doesn't forget)
- [x] Failure record preserved for learning
- [x] Queryable for future retry briefings

#### Rollback Receipt
- [ ] `RollbackReceipt` struct: what was undone, what couldn't be undone
- [x] Staged files discarded (count)
- [x] Agent context reset (yes/no)
- [x] Local commits removed (count)
- [x] Pushed commits (user action needed)
- [x] External calls logged (cannot undo)

#### User Presentation
- [x] Receipt shown to user after rollback
- [x] Clear indication of what was undone
- [x] Warnings for external state inconsistency
- [x] Options for pushed commits

**Tests:**
- [x] Staging rollback works
- [x] Agent state reset works
- [x] Local git rollback works
- [x] Pushed commits not auto-reverted
- [x] Receipt accurate
- [x] Archivalist records failure

---

### 0.33 Memory Monitor

Implements per-component memory budgets with token-weighted eviction.

**Files to create:**
- `core/resources/memory_monitor.go`
- `core/resources/eviction.go`

**Dependencies:** Requires 0.11 (Signal Bus). Can run parallel with 0.26-0.32.

**Acceptance Criteria:**

#### Per-Component Budgets
- [x] `MemoryMonitor` struct tracks all components
- [x] `ComponentMemory` struct per component
- [x] Configurable budgets: QueryCache, Staging, AgentContext, WAL
- [x] Defaults: 500MB, 1GB, 500MB, 200MB

#### Global Ceiling
- [x] Configurable global_ceiling_percent (default: 80%)
- [x] Calculate from system available memory
- [x] Query system memory periodically

#### Eviction Cascade
- [x] 70% component → LRU eviction
- [x] 90% component → aggressive eviction + warn user
- [x] 80% global → pause new pipelines + cross-component eviction
- [x] 95% global → pause ALL + emergency eviction + notify user

#### Token-Weighted Eviction (QueryCache)
- [x] `EvictionScore = TokenCost / MemorySize * RecencyFactor`
- [x] High token cost entries evicted LAST
- [x] Recency factor decays over hours
- [x] Sort by score, evict lowest first

#### Monitoring Loop
- [x] Background goroutine checks at configurable interval (default: 1s)
- [x] Publish signals on threshold crossings
- [x] Never crash, never OOM

**Tests:**
- [x] Component budgets enforced
- [x] Global ceiling enforced
- [x] Token-weighted eviction correct
- [x] Eviction cascade triggers at thresholds
- [x] Signals published correctly

---

### 0.34 Resource Pools

Implements file handle, network, and subprocess pools with user reservation.

**Files to create:**
- `core/resources/pool.go`
- `core/resources/file_pool.go`
- `core/resources/network_pool.go`
- `core/resources/process_pool.go`

**Dependencies:** Requires 0.11 (Signal Bus). Can run parallel with 0.26-0.33.

**Acceptance Criteria:**

#### Generic Resource Pool
- [x] `ResourcePool` struct: total, userReserved, available, waitQueue
- [x] `AcquireUser(ctx) (*ResourceHandle, error)` - never fails
- [x] `AcquirePipeline(ctx, priority) (*ResourceHandle, error)` - may queue
- [x] `Release(handle)` - returns to pool

#### User Reservation
- [x] Configurable user_reserved_percent (default: 20%)
- [x] User requests ALWAYS succeed (use reserved or preempt)
- [x] Preemption signals lowest-priority pipeline to pause

#### Pipeline Queuing
- [x] Priority queue for waiting pipelines
- [x] Configurable pipeline_wait_timeout (default: 30s)
- [x] Timeout returns error (pipeline retries or fails)

#### File Handle Pool
- [x] Query OS ulimit on startup
- [x] Use configurable percentage (default: 50%)
- [x] Track per-handle for leak detection

#### Network Connection Pool
- [x] Per-provider limit (default: 10)
- [x] Global limit (default: 50)
- [x] Connection reuse where possible

#### Subprocess Pool
- [x] Max = multiplier × N_CPU_CORES (default: 2×)
- [x] Track process IDs for cleanup

**Tests:**
- [x] User acquisition never fails
- [x] User preempts pipeline when needed
- [x] Pipeline queuing works
- [x] Timeout returns error
- [x] Pool sizes respect config
- [x] Release returns resources correctly

---

### 0.35 Disk Quota Manager

Implements auto-scaling disk quota with bounded percentage.

**Files to create:**
- `core/resources/disk_quota.go`

**Dependencies:** Requires 0.11 (Signal Bus). Can run parallel with 0.26-0.34.

**Acceptance Criteria:**

#### Quota Calculation
- [x] `DiskQuotaManager` struct
- [x] `CalculateQuota() int64` - returns effective quota
- [x] Formula: `max(quota_min, min(quota_max, free_space * quota_percent))`
- [x] Defaults: min=1GB, max=20GB, percent=10%

#### Usage Tracking
- [x] `CanWrite(size int64) bool`
- [x] `RecordWrite(size int64) error`
- [x] `RecordDelete(size int64)`
- [x] Periodic disk scan to reconcile actual vs tracked

#### Thresholds
- [x] Warning at 80% (configurable)
- [x] Cleanup trigger at 90% (configurable)
- [x] Publish signals at thresholds

#### Cleanup
- [x] `TriggerCleanup()` - remove old data
- [x] Priority: temp files, old WAL segments, old checkpoints, old staging
- [x] Never delete active session data

**Tests:**
- [x] Quota calculation respects bounds
- [x] Usage tracking accurate
- [x] Thresholds trigger signals
- [x] Cleanup removes correct files
- [x] Quota adjusts to available space

---

### 0.36 Resource Broker

Implements central coordinator for all-or-nothing resource allocation.

**Files to create:**
- `core/resources/broker.go`
- `core/resources/allocation.go`

**Dependencies:** Requires 0.33-0.35 (Memory, Pools, Disk).

**Acceptance Criteria:**

#### Resource Bundle
- [x] `ResourceBundle` struct: FileHandles, NetworkConns, Subprocesses, MemoryEstimate, DiskEstimate
- [x] Pipeline specifies required resources upfront
- [x] Broker validates all available before granting

#### All-or-Nothing Allocation
- [x] `AcquireBundle(ctx, pipelineID, bundle, priority) (*AllocationSet, error)`
- [x] Check memory and disk first (non-blocking)
- [x] Acquire pools in fixed order (prevent deadlock)
- [x] On any failure: release already-acquired, return error

#### Allocation Tracking
- [x] `AllocationSet` tracks all resources held by pipeline
- [x] `allocations` map for deadlock detection
- [x] Timestamp for debugging long-held resources

#### User Bypass
- [x] `AcquireUser(ctx, bundle) (*AllocationSet, error)` (via underlying pool's AcquireUser)
- [x] Uses reserved capacity in all pools
- [x] Never queues, never fails (preempts if needed)

#### Release
- [x] `ReleaseBundle(alloc)` - atomic release of all resources
- [x] Wake waiting pipelines
- [x] Clear from allocation tracking

#### Deadlock Detection (optional)
- [x] Configurable deadlock_detection (default: true)
- [x] Periodic check for cycles in wait graph
- [x] Alert if potential deadlock detected

**Tests:**
- [x] All-or-nothing works
- [x] Partial failure releases acquired
- [x] User bypass works
- [x] Release wakes waiters
- [x] Fixed order prevents deadlock

---

### 0.37 Graceful Degradation Controller

Implements priority-based pause/resume under resource pressure.

**Files to create:**
- `core/resources/degradation.go`

**Dependencies:** Requires 0.33-0.36 (Resource System), 0.18 (Pipeline Scheduler), 0.23 (Checkpointer).

**Acceptance Criteria:**

#### Pressure Detection
- [ ] Subscribe to resource warning signals
- [ ] Memory pressure, disk pressure, pool exhaustion
- [ ] Determine severity level

#### Pause Selection
- [ ] User-interactive: NEVER paused
- [ ] Sort pipelines by: priority (asc), then age (desc, newest first)
- [ ] Pause lowest-value pipelines first
- [ ] Continue pausing until pressure relieved

#### Pause Execution
- [ ] Signal pipeline to pause via SignalBus
- [ ] Pipeline checkpoints state
- [ ] Pipeline releases resources to broker
- [ ] Notify user: "Pipeline X paused due to resource pressure"

#### Resume Logic
- [ ] Monitor for pressure relief
- [ ] Resume highest-priority paused pipeline first
- [ ] Pipeline restores from checkpoint
- [ ] Notify user: "Pipeline X resumed"
- [ ] Gradual resume (don't flood)

**Tests:**
- [ ] User never paused
- [ ] Lowest priority paused first
- [ ] Checkpoint on pause
- [ ] Resources released on pause
- [ ] Resume order correct
- [ ] Gradual resume works

---

### 0.38 Error & Resource Integration Tests

End-to-end tests for error and resource systems.

**Files to create:**
- `core/errors/integration_test.go`
- `core/resources/integration_test.go`

**Dependencies:** Requires 0.26-0.37.

**Acceptance Criteria:**

#### Error System Tests
- [ ] 5-tier classification works end-to-end
- [ ] Transient frequency detection + notification
- [ ] Circuit breaker trips and recovers
- [ ] Escalation chain works through all levels
- [ ] Retry briefing improves retry success
- [ ] Rollback cleans up correctly

#### Resource System Tests
- [ ] Memory pressure triggers eviction cascade
- [ ] Token-weighted eviction preserves expensive entries
- [ ] Resource pools respect user reservation
- [ ] Disk quota auto-scales correctly
- [ ] Broker all-or-nothing works under load
- [ ] Graceful degradation pauses correct pipelines

#### Integration Tests
- [ ] Error during resource pressure handled correctly
- [ ] Circuit breaker + resource exhaustion combo
- [ ] Rollback releases resources
- [ ] Recovery after pressure-induced pause

#### Stress Tests
- [ ] Many concurrent errors classified correctly
- [ ] Resource pressure during error storm
- [ ] No resource leaks
- [ ] No goroutine leaks

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] Performance acceptable under load

---

### Error & Resource Parallelization Notes

The following tasks can be executed in parallel:
- **Group E1**: 0.26 (no dependencies, can start immediately)
- **Group E2**: 0.27, 0.28, 0.29 (depend on 0.26, can run parallel with each other)
- **Group E3**: 0.30 → 0.31 → 0.32 (sequential chain)
- **Group R1**: 0.33, 0.34, 0.35 (no dependencies on error system, parallel with E1-E3)
- **Group R2**: 0.36 → 0.37 (depend on R1)

Optimal execution order:
1. Start 0.26, 0.33, 0.34, 0.35 in parallel
2. When 0.26 completes → start 0.27, 0.28, 0.29 in parallel
3. When 0.27, 0.28, 0.29 complete → start 0.30
4. When 0.30 completes → start 0.31
5. When 0.31 completes → start 0.32
6. When 0.33, 0.34, 0.35 complete → start 0.36
7. When 0.36 completes → start 0.37
8. When 0.32 and 0.37 complete → start 0.38

Cross-group dependencies:
- 0.30 (Escalation) needs 0.29 (Circuit Breaker)
- 0.32 (Rollback) needs 0.20 (Staging), 0.22-0.24 (WAL/Checkpoint)
- 0.37 (Degradation) needs 0.18 (Scheduler), 0.23 (Checkpointer)

---

### 0.39 Session Registry

Implements SQLite-based cross-session coordination and discovery.

**Files to create:**
- `core/session/registry.go`
- `core/session/schema.sql`

**Dependencies:** None (can start immediately, parallel with 0.26-0.38).

**Acceptance Criteria:**

#### Database Schema
- [x] SQLite database at `~/.sylk/state.db`
- [x] WAL mode enabled for concurrent access
- [x] Sessions table: session_id, pid, start_time, last_heartbeat, activity_score, running_pipelines, allocated_slots
- [x] Allocations table: session_id, resource_type, allocated_count
- [x] Migrations support for schema updates

#### Session Lifecycle
- [x] `Register(sessionID string) error` - add session to registry
- [x] `Unregister(sessionID string) error` - remove session
- [x] `Heartbeat(sessionID, runningPipelines, recentInvocations) error` - update liveness
- [x] Activity score calculation: `running_pipelines + recent_invocations × decay_rate`
- [x] Configurable heartbeat interval (default: 1s)

#### Session Discovery
- [x] `GetActiveSessions() ([]SessionRecord, error)` - all non-stale sessions
- [x] `GetSession(sessionID) (*SessionRecord, error)` - single session
- [x] Configurable stale threshold (default: 10s)
- [x] `CleanupStaleSessions() error` - remove dead sessions

#### Process Validation
- [x] Verify PID still running on session lookup
- [x] Remove sessions with dead PIDs
- [x] Handle PID reuse (check start time)

**Tests:**
- [x] Session registration works
- [x] Heartbeat updates activity score
- [x] Stale detection works
- [x] Concurrent access safe (WAL)
- [x] Dead PID detection works

---

### 0.40 Fair Share Calculator

Implements activity-based resource allocation across sessions.

**Files to create:**
- `core/session/fair_share.go`

**Dependencies:** Requires 0.39 (Session Registry).

**Acceptance Criteria:**

#### Allocation Model
- [x] `FairShareCalculator` struct
- [x] Three-tier model: User (absolute), Active (proportional), Idle (baseline)
- [x] User reserved: configurable percent (default: 20%) of all pools
- [x] Idle baseline: configurable slots (default: 1) per idle session

#### Fair Share Calculation
- [x] `Calculate(totalSlots int) (map[string]SessionAllocation, error)`
- [x] Active share = `(session_score / total_score) × remaining_capacity`
- [x] Minimum 1 slot for any active session
- [x] Rebalance on: session join, session leave, significant activity change

#### SessionAllocation Struct
- [x] SubprocessSlots, FileHandleSlots, NetworkSlots, MemoryBudget
- [x] All resource types scaled by same fair share ratio

#### Rebalancing
- [x] Configurable rebalance interval (default: 500ms)
- [x] Publish rebalance signal when allocations change significantly (>10%)
- [x] Sessions adjust to new allocation within grace period

**Tests:**
- [x] Two equal sessions split evenly
- [x] High activity session gets more
- [x] Idle session gets baseline only
- [x] Rebalance triggers correctly
- [x] Edge case: single session gets all

---

### 0.41 Signal Dispatcher

Implements fsnotify-based cross-session signaling.

**Files to create:**
- `core/session/signal_dispatcher.go`
- `core/session/signals.go`

**Dependencies:** Requires 0.39 (Session Registry).

**Acceptance Criteria:**

#### Signal Directory Structure
- [x] Base: `~/.sylk/signals/`
- [x] Per-session: `~/.sylk/signals/<session-id>/`
- [x] Signal files: `<type>-<timestamp>.signal`
- [x] Auto-create directories on session start

#### Signal Types
- [x] `SignalPreempt`: User needs resources NOW
- [x] `SignalPressure`: Memory/disk pressure alert
- [x] `SignalRebalance`: Fair share changed
- [x] `SignalShutdown`: Session ending

#### CrossSessionSignal Struct
- [x] Type, FromSession, ToSession (empty=broadcast), Timestamp, Payload
- [x] JSON serialization

#### Send Operations
- [x] `SendSignal(signal CrossSessionSignal) error`
- [x] Targeted: write to specific session's directory
- [x] Broadcast: write to all session directories (except self)

#### Receive Operations
- [x] `Watch(ctx context.Context) error` - start watching
- [x] fsnotify on session's signal directory
- [x] Parse and dispatch to registered handlers
- [x] Delete signal file after processing (consume)

#### Handler Registration
- [x] `RegisterHandler(signalType, handler func(CrossSessionSignal))`
- [x] Multiple handlers per type allowed

**Tests:**
- [x] Signal delivery works (same machine)
- [x] Broadcast reaches all sessions
- [x] Signal consumed after processing
- [x] Handlers invoked correctly
- [x] Cleanup on session shutdown

---

### 0.42 Cross-Session Resource Pool

Extends resource pools (0.34) with cross-session fair share enforcement.

**Files to create:**
- `core/session/cross_session_pool.go`

**Dependencies:** Requires 0.34 (Resource Pools), 0.40 (Fair Share), 0.41 (Signal Dispatcher).

**Acceptance Criteria:**

#### Pool Integration
- [x] `CrossSessionPool` wraps `ResourcePool`
- [x] Enforces per-session allocation limits from fair share
- [x] Tracks allocations per session in registry

#### Acquisition with Fair Share
- [x] `Acquire(ctx, sessionID, priority) (*ResourceHandle, error)`
- [x] Check session's remaining allocation
- [x] If over allocation: queue (pipeline) or preempt (user)
- [x] Update registry allocation count

#### Cross-Session Preemption
- [x] User request can preempt pipelines in OTHER sessions
- [x] Find lowest-priority pipeline across all sessions
- [x] Send preempt signal to that session
- [x] Wait for slot release (with timeout)
- [x] If timeout: escalate to next lowest priority

#### Release with Rebalance
- [x] `Release(handle)` - standard release
- [x] Decrement session's allocation count
- [x] If other sessions waiting: signal availability

#### Allocation Queries
- [x] `GetSessionAllocation(sessionID) int`
- [x] `GetTotalAllocated() int`
- [x] `GetWaitingCount() int`

**Tests:**
- [x] Fair share enforced per session
- [x] Cross-session preemption works
- [x] User from Session A preempts Session B's pipeline
- [x] Allocation tracking accurate
- [x] Release wakes correct waiters

---

### 0.43 Tool Executor (DONE)

Implements subprocess spawning with hybrid direct/shell execution.

**Files to create:**
- `core/tools/executor.go`
- `core/tools/process_group.go`

**Dependencies:** Requires 0.42 (Cross-Session Pool).

**Acceptance Criteria:**

#### Spawn Decision
- [ ] `ToolExecutor` struct with configurable shell patterns
- [ ] Default shell patterns: `|`, `&&`, `||`, `;`, `*`, `?`, `$`, backtick
- [ ] `needsShell(command string) bool`
- [ ] Direct: `exec.CommandContext(ctx, cmd, args...)`
- [ ] Shell: `exec.CommandContext(ctx, "sh", "-c", command)`

#### Environment Handling
- [ ] Inherit parent environment
- [ ] Apply blocklist filter (strip sensitive vars)
- [ ] Default blocklist: `*_API_KEY`, `*_SECRET`, `*_TOKEN`, etc.
- [ ] Merge additional env vars from invocation

#### Working Directory
- [ ] `validateWorkingDir(dir string) error`
- [ ] Must be absolute path
- [ ] Resolve symlinks
- [ ] Check against allowed boundaries (project, staging, temp)
- [ ] Reject if outside boundaries

#### Process Group (Unix)
- [ ] `ProcessGroup` struct
- [ ] `Setup(cmd)`: set `Setpgid: true`
- [ ] `Signal(sig)`: `syscall.Kill(-pgid, sig)`
- [ ] `Kill()`: SIGKILL to process group

#### Process Group (Windows)
- [ ] Job Object creation
- [ ] `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
- [ ] `TerminateJobObject` for kill

#### Pool Integration
- [ ] Acquire subprocess slot before spawn
- [ ] Release slot on completion/kill
- [ ] Track active processes for cleanup

**Tests:**
- [ ] Direct execution works
- [ ] Shell execution works
- [ ] Shell detection correct
- [ ] Env blocklist filters correctly
- [ ] Working dir validation works
- [ ] Process group kills children

---

### 0.44 Adaptive Timeout

Implements timeout extension based on meaningful output.

**Files to create:**
- `core/tools/adaptive_timeout.go`

**Dependencies:** None (can run parallel with 0.43).

**Acceptance Criteria:**

#### Timeout Configuration
- [x] `AdaptiveTimeoutConfig` struct
- [x] `base_timeout`: starting timeout (from tool config or default)
- [x] `extend_on_output`: extension duration (default: 30s)
- [x] `max_timeout`: hard ceiling (default: 30m)
- [x] Per-tool timeout overrides

#### Noise Detection
- [x] Configurable noise patterns (regex)
- [x] Default noise: dots, spinners, percentages alone
- [x] `isNoise(line string) bool`

#### Timeout Extension
- [x] `OnOutput(line string)` - called for each line
- [x] If not noise: update `lastOutputTime`
- [x] `ShouldTimeout(started time.Time) bool`
- [x] False if: recent meaningful output within extension window
- [x] True if: elapsed > base AND no recent output, OR elapsed > max

#### Integration
- [x] Output streamer calls `OnOutput` for each line
- [x] Executor checks `ShouldTimeout` periodically
- [x] Kill sequence triggered when timeout

**Tests:**
- [x] Base timeout triggers without output
- [x] Meaningful output extends timeout
- [x] Noise doesn't extend timeout
- [x] Max timeout caps extension
- [x] Per-tool config works

---

### 0.45 Kill Sequence Manager

Implements SIGINT → SIGTERM → SIGKILL escalation with configurable timings.

**Files to create:**
- `core/tools/kill_sequence.go`

**Dependencies:** Requires 0.43 (Tool Executor - process groups).

**Acceptance Criteria:**

#### Kill Sequence
- [ ] `KillSequenceManager` struct
- [ ] Configurable: `sigint_grace` (default: 5s), `sigterm_grace` (default: 3s)
- [ ] `Execute(pg *ProcessGroup) KillResult`

#### Sequence Execution
- [ ] Send SIGINT to process group
- [ ] Wait up to `sigint_grace` for exit
- [ ] If still running: send SIGTERM
- [ ] Wait up to `sigterm_grace` for exit
- [ ] If still running: send SIGKILL

#### KillResult
- [ ] SentSIGINT, SentSIGTERM, SentSIGKILL bools
- [ ] ExitedAfter: which signal caused exit
- [ ] Duration: total time to kill

#### User Feedback Integration
- [ ] Publish progress to Signal Bus
- [ ] "Stopping..." on SIGINT
- [ ] "Force stopping..." on SIGTERM
- [ ] "Killed" on SIGKILL

**Tests:**
- [ ] Clean exit on SIGINT
- [ ] Escalation to SIGTERM works
- [ ] Escalation to SIGKILL works
- [ ] Timing respected
- [ ] Process group killed (not just parent)

---

### 0.46 Output Handler

Implements streaming output with smart truncation and progressive disclosure.

**Files to create:**
- `core/tools/output_handler.go`
- `core/tools/smart_truncator.go`
- `core/tools/importance_detector.go`

**Dependencies:** None (can run parallel with 0.43-0.45).

**Acceptance Criteria:**

#### Output Streaming
- [x] `OutputHandler` struct
- [x] `CreateStreams(streamTo io.Writer) (stdout, stderr io.Writer)`
- [x] Tee: write to user stream AND buffer
- [x] Line-by-line streaming (flush on newline)

#### Smart Truncator
- [x] `SmartTruncator` struct
- [x] Configurable: `keep_first_lines` (default: 50), `keep_last_lines` (default: 100)
- [x] Configurable: `max_buffer_size` (default: 1MB)
- [x] `Truncate(stdout, stderr []byte) *TruncatedOutput`

#### Importance Detection
- [x] `ImportanceDetector` with configurable patterns
- [x] Default patterns: error, warning, failed, exception, panic, file:line:col, stack trace
- [x] `IsImportant(line string) bool`
- [x] Extract all important lines during truncation

#### TruncatedOutput
- [x] Summary: "N errors, M warnings in X lines"
- [x] ImportantLines: all lines matching patterns
- [x] FirstLines: first N lines
- [x] LastLines: last M lines
- [x] TotalLines, Truncated bool

#### Progressive Disclosure
- [x] `ProcessOutput(tool, stdout, stderr) *ProcessedOutput`
- [x] If parser available: return parsed only
- [x] Else: return smart-truncated
- [x] Full raw available on explicit request

**Tests:**
- [x] Streaming works in real-time
- [x] Truncation keeps important lines
- [x] First/last lines preserved
- [x] Summary accurate
- [x] Large output handled

---

### 0.47 Tool Output Parsers ✅ COMPLETED

Implements parsers for common development tools.

**Files created:**
- `core/tools/parsers/parser.go` ✅
- `core/tools/parsers/go_parser.go` ✅
- `core/tools/parsers/eslint_parser.go` ✅
- `core/tools/parsers/git_parser.go` ✅

**Note:** NPM and Pytest parsers deferred - Go/ESLint/Git cover primary use cases.

**Dependencies:** Requires 0.46 (Output Handler).

**Acceptance Criteria:**

#### Parser Interface
- [x] `OutputParser` interface: `Parse(stdout, stderr []byte) (interface{}, error)`
- [x] Each parser returns tool-specific struct
- [x] Parser registry: `map[string]OutputParser`

#### Go Parser
- [x] Parse `go build` errors: file, line, column, message
- [x] Parse `go test` output: pass/fail per test, duration, coverage
- [x] Parse `go vet` warnings (shares build error format)

#### NPM Parser
- [ ] ~~Parse `npm install` output~~ (deferred)
- [ ] ~~Parse `npm run build` errors~~ (deferred)
- [ ] ~~Parse `npm test` (jest format)~~ (deferred)

#### Pytest Parser
- [ ] ~~Parse test results~~ (deferred)
- [ ] ~~Parse failure details~~ (deferred)
- [ ] ~~Parse coverage if present~~ (deferred)

#### ESLint Parser
- [x] Parse lint errors: file, line, rule, message, severity
- [x] Support JSON output format (--format json)
- [x] Fallback to text parsing

#### Git Parser
- [x] Parse `git status`: staged, unstaged, untracked files
- [x] Parse `git diff`: changed files, additions, deletions
- [x] Parse `git log`: commits with hash, author, message

#### Extensibility
- [x] `RegisterParser(toolPattern, parser)` for custom parsers
- [x] Tool pattern supports glob (e.g., "go *", "eslint *")

**Tests:**
- [x] Each parser handles typical output
- [x] Each parser handles error cases
- [x] Parser selection by tool name works
- [x] Custom parser registration works

---

### 0.48 Parse Template Cache ✅ COMPLETED

Implements caching of LLM-learned parse templates with optional persistence.

**Files created:**
- `core/tools/parsers/template_cache.go` ✅
- `core/tools/parsers/errors.go` ✅

**Dependencies:** Requires 0.47 (Parsers). Archivalist integration via TemplateStore interface.

**Acceptance Criteria:**

#### Template Structure
- [x] `ParseTemplate` struct: tool pattern, extraction rules, example
- [x] Extraction rules: regex patterns for key fields
- [x] Validated against example before caching

#### Cache Operations
- [x] `Get(tool string) *ParseTemplate`
- [x] `Store(tool string, template *ParseTemplate)`
- [x] Backed by optional TemplateStore (persistent across sessions)
- [x] In-memory cache for fast lookup with LRU eviction

#### LLM Template Learning
- [ ] ~~When no parser and no template: request LLM to create template~~ (deferred - requires LLM integration)
- [ ] ~~LLM returns: field names, regex patterns, example parsing~~ (deferred)
- [x] Validate template against actual output
- [x] If valid: cache (Archivalist integration via TemplateStore)

#### Template Application
- [x] `Apply(template, stdout, stderr) (interface{}, error)`
- [x] Apply regex patterns to extract fields
- [x] Return structured TemplateResult with fields and warnings

**Tests:**
- [x] Template caching works
- [x] Template retrieval works
- [x] Template applied correctly (22 tests)
- [x] Invalid templates rejected
- [x] Persistence integration works (via TemplateStore mock)

---

### 0.49 Filesystem Manager

Implements safe file operations with write abstraction and symlink boundary checking.

**Files to create:**
- `core/tools/filesystem.go`
- `core/tools/symlink_resolver.go`

**Dependencies:** Requires 0.20 (Staging), 0.39 (Session Registry).

**Acceptance Criteria:**

#### Read Path (Direct)
- [ ] Direct `os.ReadFile` for reads
- [ ] No abstraction overhead
- [ ] Permission errors returned as-is

#### Write Path (Abstracted)
- [ ] `Write(path, content, perm) error`
- [ ] Pre-check permission (user feedback)
- [ ] Resolve and validate symlinks
- [ ] Route to staging if in pipeline
- [ ] Audit log write operation
- [ ] Actual write to resolved path

#### Symlink Boundary Check
- [ ] `resolveWithBoundaryCheck(path) (string, error)`
- [ ] Follow symlinks (configurable)
- [ ] Allowed boundaries: project root, staging, temp, configured extras
- [ ] Reject if resolved path outside boundaries
- [ ] Clear error message with resolution path

#### Temp Directory Hierarchy
- [ ] `GetTempDir() string` returns appropriate temp
- [ ] Pipeline: `~/.sylk/tmp/<session-id>/pipeline-<id>/`
- [ ] Standalone: `~/.sylk/tmp/<session-id>/standalone/`
- [ ] Auto-create on first use

#### Cleanup
- [ ] Pipeline temp cleaned on pipeline completion
- [ ] Session temp cleaned on session end
- [ ] Orphan cleanup for crashed sessions

**Tests:**
- [ ] Reads work directly
- [ ] Writes go through abstraction
- [ ] Symlink escape blocked
- [ ] Staging routing works
- [ ] Temp hierarchy correct
- [ ] Cleanup removes directories

---

### 0.50 Tool Cancellation Manager ✅ COMPLETED

Implements cascading cancellation with cleanup and partial result preservation.

**Files created:**
- `core/tools/cancellation.go` ✅
- `core/tools/cancellation_test.go` ✅

**Dependencies:** Requires 0.43-0.45 (Executor, Timeout, Kill Sequence).

**Acceptance Criteria:**

#### Cascading Timeouts
- [x] Total budget: configurable (default: 30s)
- [x] Agent level: budget - 5s
- [x] Tool level: agent_budget - 5s
- [x] Process level: SIGINT(5s) + SIGTERM(3s) + SIGKILL(instant)
- [x] Each level respects remaining budget

#### Cancellation Flow
- [x] `Cancel(ctx context.Context) error`
- [x] Propagate cancel to all running tools in agent
- [x] Each tool initiates kill sequence
- [x] Collect results (success, partial, timeout)

#### Cleanup Phase
- [x] Budget: 5s (best-effort)
- [x] Kill orphan processes (process group)
- [x] Remove tool's temp files
- [x] Release file locks (if any)
- [x] Release pool slots
- [x] If cleanup times out: log warning, continue

#### Partial Results
- [x] Preserve output captured before cancel
- [x] Mark result: `Partial = true`
- [x] Include reason: "cancelled by user" / "timeout"
- [x] Agent can inspect partial output

#### User Feedback
- [x] "Cancelling operation..."
- [x] "Stopping [tool]..."
- [x] "Force stopping [tool]..."
- [x] "Killed [tool]"
- [x] "Cleanup incomplete, temp files may remain" (if cleanup timeout)

**Tests:**
- [x] Cancellation stops all tools
- [x] Cascading budget respected
- [x] Cleanup runs within budget
- [x] Partial results preserved
- [x] User sees progress

---

### 0.51 Tool Output Cache

Implements caching for deterministic tool invocations.

**Files to create:**
- `core/tools/output_cache.go`

**Dependencies:** None (can run parallel with 0.43-0.50).

**Acceptance Criteria:**

#### Cache Configuration
- [x] `ToolCacheConfig` struct
- [x] `max_entries`: maximum cache entries (default: 1000)
- [x] `max_size`: maximum total size (default: 100MB)
- [x] `ttl`: time-to-live (default: 5m)
- [x] Per-tool cache policy: cacheable, ttl, invalidate_on patterns

#### Input Hashing
- [x] `ComputeInputHash(invocation) string`
- [x] Hash: command + args + working_dir
- [x] For file-based tools: include file content hashes
- [x] Configurable per-tool which inputs matter

#### Cache Operations
- [x] `Get(tool, inputHash) (*ToolResult, bool)`
- [x] `Put(tool, inputHash, result)`
- [x] Respect per-tool TTL
- [x] Evict on max_entries or max_size exceeded

#### Invalidation
- [x] File change invalidates matching cache entries
- [x] `invalidate_on` patterns per tool (e.g., "*.js" for eslint)
- [x] Watch project files for changes (optional)
- [x] Manual `Invalidate(tool)` for explicit clear

#### Default Cacheable Tools
- [x] eslint, prettier, go vet (deterministic on same input)
- [x] NOT: go test (may have side effects), npm install (network)

**Tests:**
- [x] Cache hit returns stored result
- [x] Cache miss returns not found
- [x] TTL expiration works
- [x] File change invalidates
- [x] Size limit enforced

---

### 0.52 Tool Invocation Batcher ✅ COMPLETED

Implements batching for tools that support multiple file inputs.

**Files created:**
- `core/tools/batcher.go` ✅
- `core/tools/batcher_test.go` ✅

**Dependencies:** Requires 0.43 (Tool Executor).

**Acceptance Criteria:**

#### Batch Configuration
- [x] Per-tool batch support: max_files, separator
- [x] Default batch-capable: eslint, prettier, go vet, go build
- [x] Non-batchable: tools with single-file semantics

#### Batch Collection
- [x] `Batcher` collects invocations for same tool
- [x] Within configurable window (default: 100ms)
- [x] Up to max_files per batch

#### Batch Execution
- [x] Combine file args: `eslint file1.js file2.js file3.js`
- [x] Single subprocess instead of N
- [x] Parse output to attribute results to individual files

#### Result Attribution
- [x] Split batch result back to individual invocations
- [x] Each caller receives their file's result
- [x] Handle partial failures (some files error, others don't)

#### Fallback
- [x] If batching fails: fall back to individual execution
- [x] Log warning about batch failure

**Tests:**
- [x] Batching reduces subprocess count
- [x] Results attributed correctly
- [x] Non-batchable tools not batched
- [x] Partial failure handled
- [x] Fallback works

---

### 0.53 Streaming Output Parser ✅ COMPLETED

Implements real-time parsing during tool execution.

**Files created:**
- `core/tools/parsers/streaming_parser.go` ✅

**Dependencies:** Requires 0.47 (Parsers), 0.46 (Output Handler).

**Acceptance Criteria:**

#### Streaming Interface
- [x] `StreamingParser` interface: `OnLine(line string) []ParsedEvent`
- [x] Returns parsed events as they're detected
- [x] Events: Error, Warning, TestResult, Progress

#### ParsedEvent Types
- [x] `ErrorEvent`: file, line, column, message, severity
- [x] `WarningEvent`: file, line, message
- [x] `TestResultEvent`: name, status (pass/fail/skip), duration
- [x] `ProgressEvent`: percent, message

#### Real-Time Detection
- [x] Error patterns detected immediately
- [x] EventCollector with HasFatalError() for early detection
- [x] StreamingParserWrapper for handler integration

#### Tool-Specific Streaming Parsers
- [x] Go test/build: errors, test pass/fail, package failures
- [x] Pytest: test results, errors, progress
- [x] Generic: error/warning pattern matching (npm, webpack, etc.)

#### Integration
- [x] StreamingParserRegistry for tool-specific parser selection
- [ ] ~~Parsed events published to Signal Bus~~ (deferred - requires signal bus integration)
- [x] EventCollector for aggregating events

**Tests:**
- [x] Errors detected in real-time (22 tests)
- [x] Events published to collector
- [x] Concurrent event collection
- [x] Early fatal error detection

---

### 0.54 Tool Execution Integration Tests

End-to-end tests for tool execution system.

**Files to create:**
- `core/tools/integration_test.go`
- `core/session/integration_test.go`

**Dependencies:** Requires 0.39-0.53.

**Acceptance Criteria:**

#### Multi-Session Tests
- [ ] Two sessions share subprocess pool fairly
- [ ] User in Session A preempts Session B's pipeline tool
- [ ] Session join/leave rebalances allocations
- [ ] Stale session cleanup works

#### Tool Execution Tests
- [ ] Simple command executes correctly
- [ ] Shell command with pipes works
- [ ] Adaptive timeout extends on output
- [ ] Kill sequence terminates hung process
- [ ] Process group kills all children

#### Output Handling Tests
- [ ] Streaming output reaches user in real-time
- [ ] Smart truncation preserves important lines
- [ ] Parser extracts structured data
- [ ] Cache hit returns cached result

#### Cancellation Tests
- [ ] User cancel stops all tools
- [ ] Partial results preserved
- [ ] Cleanup completes within budget
- [ ] Cleanup timeout handled gracefully

#### Filesystem Tests
- [ ] Symlink escape blocked
- [ ] Pipeline writes go to staging
- [ ] Temp cleanup on completion/failure

#### Stress Tests
- [ ] Many concurrent tool invocations
- [ ] High output volume
- [ ] Rapid cancel/restart cycles
- [ ] No resource leaks

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] Performance acceptable

---

### Tool Execution Parallelization Notes

The following tasks can be executed in parallel:
- **Group T1**: 0.39, 0.44, 0.46, 0.51 (no dependencies)
- **Group T2**: 0.40, 0.41 (depend on 0.39)
- **Group T3**: 0.42 → 0.43 → 0.45, 0.50, 0.52 (sequential)
- **Group T4**: 0.47 → 0.48, 0.53 (parser chain)
- **Group T5**: 0.49 (depends on 0.20, 0.39)

Optimal execution order:
1. Start 0.39, 0.44, 0.46, 0.51 in parallel
2. When 0.39 completes → start 0.40, 0.41, 0.49 in parallel
3. When 0.40, 0.41 complete → start 0.42
4. When 0.42 completes → start 0.43
5. When 0.43 completes → start 0.45, 0.50, 0.52 in parallel
6. When 0.46 completes → start 0.47
7. When 0.47 completes → start 0.48, 0.53 in parallel
8. When all 0.39-0.53 complete → start 0.54

Cross-group dependencies:
- 0.42 needs 0.34 (Resource Pools from Tier 1)
- 0.49 needs 0.20 (Staging from Concurrency)
- 0.48 needs Archivalist agent

---

## Tier 1 Multi-Session Amendments

These tasks amend the original Tier 1 components to support multi-session coordination.

### 0.55 Global Subscription Tracker ✅ COMPLETED

Cross-session usage tracking for subscription-based rate limiting (weekly/monthly quotas).

**Files created:**
- `core/session/subscription_tracker.go` ✅

**Dependencies:** Requires 0.39 (Session Registry) for shared SQLite.

**Acceptance Criteria:**

#### Schema
- [x] `subscription_usage` table: provider, period_start, period_end, requests_used, tokens_used
- [x] Atomic increment with SQLite ON CONFLICT upsert
- [x] Period auto-rotation (weekly/monthly boundary detection)

#### Tracking
- [x] RecordUsage atomically updates global counts
- [x] Per-provider tracking (anthropic, openai, google)
- [x] Support for nil limits (unlimited - rely on 429)
- [x] Period configurable: week, month, day

#### Warnings
- [x] Warn at configurable threshold (default 80%)
- [x] Broadcast warning to ALL sessions via Signal Dispatcher
- [x] Different warning levels: OK (0), Warning (1), Critical (2)
- [x] Warning includes: provider, usage stats, period end date

#### Configuration
- [x] `subscription.{provider}.period`: week | month | day
- [x] `subscription.{provider}.max_requests`: int or nil
- [x] `subscription.{provider}.max_tokens`: int or nil
- [x] `subscription.{provider}.warn_threshold`: float (0.8)

**Tests:** (17 tests)
- [x] Two sessions increment same provider atomically
- [x] Warning broadcast on threshold breach
- [x] Period bounds calculated correctly
- [x] Nil limit means unlimited (no warning)

---

### 0.56 Cross-Session Dual Queue Gate ✅ COMPLETED

Multi-session LLM request queue with cross-session user preemption.

**Files created:**
- `core/session/dual_queue_gate.go` ✅
- `core/session/dual_queue_gate_test.go` ✅

**Dependencies:** Requires 0.39 (Session Registry), 0.41 (Signal Dispatcher), 0.55 (Subscription Tracker).

**Acceptance Criteria:**

#### Global Queues
- [x] Single global user-interactive queue (FIFO across sessions)
- [x] Single global pipeline queue (priority-ordered across sessions)
- [x] In-memory queue with signal-based coordination

#### User Priority
- [x] User request from ANY session preempts pipeline from ANY session
- [x] User queue ALWAYS drains before pipeline queue
- [x] FIFO ordering among user requests (fair across sessions)

#### Preemption
- [x] Find lowest-priority active pipeline request across ALL sessions
- [x] Cancel and notify owning session via Signal Dispatcher
- [x] Cancelled request re-queued automatically

#### Per-Session State
- [x] Track active requests per session
- [x] Track queued requests per session
- [x] Fair share enforcement (sessions can't monopolize)

#### Integration
- [x] Check GlobalSubscriptionTracker before accepting requests
- [x] Check GlobalCircuitBreakerRegistry before dispatching
- [x] Respect per-session fair share allocation

**Tests:**
- [x] User in Session A preempts pipeline in Session B
- [x] Pipeline priority ordering works across sessions
- [x] Concurrent submit handling
- [x] Fair share prevents session monopolization

---

### 0.57 Global Pipeline Scheduler ✅ COMPLETED

Cross-session pipeline scheduling with global N_CPU_CORES limit.

**Files created:**
- `core/session/pipeline_scheduler.go` ✅
- `core/session/pipeline_scheduler_test.go` ✅

**Dependencies:** Requires 0.39 (Session Registry), 0.40 (Fair Share Calculator), 0.41 (Signal Dispatcher).

**Acceptance Criteria:**

#### Global Limit
- [x] N_CPU_CORES is GLOBAL across all sessions
- [x] Track totalActive across all sessions
- [x] In-memory with signal-based coordination

#### Fair Share Integration
- [x] Use FairShareCalculator (0.40) for per-session allocation
- [x] Rebalance on session join/leave
- [x] Rebalance on allocation change signal

#### Per-Session Tracking
- [x] Track sessionActive[sessionID] = running count
- [x] Track sessionQueued[sessionID] = priority queue
- [x] Session can only use up to its fair share allocation

#### Scheduling
- [x] Schedule() respects both global limit and session fair share
- [x] OnPipelineComplete() triggers cross-session rebalancing
- [x] Queued pipelines dispatched fairly across sessions

#### Preemption Support
- [x] User-invoked pipeline can preempt background pipeline
- [x] Preemption from any session's user activity
- [x] Notify preempted session via Signal Dispatcher

**Tests:**
- [x] Two sessions share N_CPU_CORES fairly
- [x] Active session gets more slots than idle
- [x] Rebalancing works on session join/leave
- [x] No session exceeds its fair share allocation

---

### 0.58 Multi-Session WAL Manager ✅ COMPLETED

Per-session WAL files with shared metadata for crash recovery.

**Files created:**
- `core/session/wal_manager.go` ✅
- `core/session/wal_manager_test.go` ✅

**Dependencies:** Requires 0.22 (WAL), 0.39 (Session Registry).

**Acceptance Criteria:**

#### Directory Structure
- [x] Per-session: ~/.sylk/sessions/{sessionID}/wal/
- [x] Per-session: ~/.sylk/sessions/{sessionID}/checkpoints/
- [x] Shared: ~/.sylk/shared/wal_metadata.db (metadata)

#### WAL Management
- [x] GetOrCreateWAL(sessionID) creates/retrieves WAL
- [x] Each session has independent WAL (crash isolation)
- [x] Register session in shared DB on creation

#### Recovery
- [x] RecoverAllSessions() on startup
- [x] Query shared DB for unrecovered sessions
- [x] Recover each session's WAL independently
- [x] Mark as recovered in shared DB

#### Cleanup
- [x] Remove session directory on explicit cleanup
- [x] Retain WAL for configurable period (default 7 days)
- [x] Prune old sessions periodically

**Tests:**
- [x] Session A crash doesn't affect Session B WAL
- [x] Recovery finds all unrecovered sessions
- [x] Cleanup removes old session data
- [x] Concurrent WAL writes don't conflict

---

### 0.59 Global Circuit Breaker Registry ✅ COMPLETED

Shared circuit breaker state across all sessions.

**Files created:**
- `core/session/circuit_breaker_registry.go` ✅
- `core/session/circuit_breaker_registry_test.go` ✅

**Dependencies:** Requires 0.29 (Circuit Breaker), 0.39 (Session Registry), 0.41 (Signal Dispatcher).

**Acceptance Criteria:**

#### Shared State
- [x] `circuit_breakers` table: resource_id, state, failures, last_failure, last_state_change, cooldown_ends
- [x] Atomic state transitions in SQLite
- [x] Resource IDs: llm:anthropic, llm:openai, network, file, subprocess

#### Local Cache
- [x] Each session maintains local cache of circuit states
- [x] Refresh cache on Signal Dispatcher notification
- [x] Allow() uses local cache (fast path)

#### State Changes
- [x] RecordResult() updates shared state
- [x] Broadcast state change to ALL sessions via Signal Dispatcher
- [x] All sessions receive update within signal_debounce (default 100ms)

#### Benefits
- [x] Session A hits 429 → ALL sessions know immediately
- [x] Shared cooldown prevents duplicate probes
- [x] Global half-open allows ONE probe across all sessions

#### Manual Reset
- [x] Reset() updates shared state
- [x] Broadcasts reset to all sessions
- [x] All sessions resume immediately

**Tests:**
- [x] Session A trips circuit → Session B sees OPEN
- [x] Cooldown shared (only one probe attempt globally)
- [x] State change broadcast reaches all sessions
- [x] Local cache provides fast Allow() checks

---

### 0.60 Tier 1 Multi-Session Integration Tests

End-to-end tests for multi-session Tier 1 amendments.

**Files to create:**
- `core/llm/multi_session_test.go`
- `core/pipeline/multi_session_test.go`
- `core/state/multi_session_test.go`
- `core/errors/multi_session_test.go`

**Dependencies:** Requires 0.55-0.59.

**Acceptance Criteria:**

#### Subscription Tests
- [ ] Two sessions share subscription quota correctly
- [ ] Warning broadcasts to both sessions
- [ ] Period rotation resets for all sessions

#### Queue Tests
- [ ] User preemption works across sessions
- [ ] Pipeline priority ordering is global
- [ ] Fair share prevents monopolization

#### Scheduler Tests
- [ ] N_CPU_CORES shared globally
- [ ] Rebalancing works on session changes
- [ ] Preemption notifications delivered

#### WAL Tests
- [ ] Session A crash recovery independent
- [ ] Shared metadata consistent
- [ ] Cleanup removes correct sessions

#### Circuit Breaker Tests
- [ ] Trip in one session affects all
- [ ] Recovery synchronized across sessions
- [ ] No stampede on recovery

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] SQLite contention handled gracefully

---

### Tier 1 Multi-Session Parallelization Notes

The following tasks can be executed in parallel:
- **Group M1**: 0.55 (depends on 0.39 only)
- **Group M2**: 0.58 (depends on 0.22, 0.39)
- **Group M3**: 0.59 (depends on 0.29, 0.39, 0.41)

Sequential dependencies:
- 0.55 → 0.56 (Queue needs Subscription Tracker)
- 0.39, 0.40, 0.41 → 0.57 (Scheduler needs all session coordination)
- 0.55-0.59 → 0.60 (Integration tests need all components)

Optimal execution order:
1. Ensure 0.39-0.41 complete (from Tier 2)
2. Start 0.55, 0.58, 0.59 in parallel
3. When 0.55 completes → start 0.56
4. When 0.39-0.41 complete → start 0.57
5. When all 0.55-0.59 complete → start 0.60

---

## Tier 3: Storage & Configuration

Foundational storage layout, configuration management, credential handling, and database operations. **These tasks have NO dependencies on Tier 1 or Tier 2** and can be executed in parallel with all other work.

### 0.61 Directory Manager

Platform-native directory resolution with XDG support.

**Files to create:**
- `core/storage/dirs.go`
- `core/storage/dirs_darwin.go`
- `core/storage/dirs_linux.go`
- `core/storage/dirs_windows.go`

**Dependencies:** None (foundational).

**Acceptance Criteria:**

#### Directory Resolution
- [ ] Check XDG environment variables first (all platforms)
- [ ] Fall back to platform-native defaults:
  - [ ] Linux: ~/.config, ~/.local/share, ~/.cache, ~/.local/state
  - [ ] macOS: ~/Library/Preferences, ~/Library/Application Support, ~/Library/Caches, ~/Library/Logs
  - [ ] Windows: %APPDATA%, %LOCALAPPDATA%
- [ ] Create directories on first access if missing

#### Directory Types
- [ ] Config: user settings, credentials
- [ ] Data: sessions, databases, per-project knowledge
- [ ] Cache: regenerable (TTL-tiered: hot/warm/cold)
- [ ] State: logs, runtime, temp, signals

#### Project-Local
- [ ] Detect .sylk/ in project root
- [ ] Support config.yaml (committed) vs local/ (gitignored)
- [ ] ProjectHash() for consistent project identification

#### Helpers
- [ ] EnsureDir() creates with correct permissions (0700 for sensitive)
- [ ] CleanupDir() removes with validation
- [ ] TempDir() returns OS-managed temp with sylk prefix

**Tests:**
- [ ] Platform detection works correctly
- [ ] XDG override works on all platforms
- [ ] Directory creation idempotent
- [ ] Permissions correct on Unix

---

### 0.62 Configuration Manager

YAML configuration with JSON Schema validation, layering, and hot reload.

**Files to create:**
- `core/config/manager.go`
- `core/config/schema.go`
- `core/config/merge.go`
- `core/config/watcher.go`
- `core/config/config.schema.json`

**Dependencies:** 0.61 (Directory Manager).

**Acceptance Criteria:**

#### Config Loading
- [ ] Load defaults from code
- [ ] Deep merge project config (.sylk/config.yaml)
- [ ] Deep merge user config (user wins over project)
- [ ] Deep merge project-local overrides (.sylk/local/config.yaml)
- [ ] Apply environment variables last

#### Environment Variables
- [ ] Simple values: SYLK_LLM_TIMEOUT → llm.timeout
- [ ] File references: SYLK_LLM_PROVIDERS_FILE → load and merge
- [ ] Document all supported variables

#### Validation
- [ ] JSON Schema validation
- [ ] Warn on invalid values (don't refuse to start)
- [ ] Use defaults for invalid fields
- [ ] Emit summary of warnings

#### Hot Reload
- [ ] SIGHUP triggers reload
- [ ] fsnotify watches config files
- [ ] Debounce 500ms
- [ ] Notify user on reload success/failure
- [ ] Atomic config swap (no partial state)

#### Schema
- [ ] Complete JSON Schema for all config options
- [ ] IDE autocomplete support
- [ ] Default values documented in schema

**Tests:**
- [ ] Layering precedence correct
- [ ] Deep merge works for nested maps
- [ ] Invalid config doesn't crash startup
- [ ] Hot reload applies changes correctly
- [ ] Environment override works

---

### 0.63 Credential Manager

Credential storage with keychain integration and encrypted fallback.

**Files to create:**
- `core/credentials/manager.go`
- `core/credentials/keychain_darwin.go`
- `core/credentials/keychain_linux.go`
- `core/credentials/keychain_windows.go`
- `core/credentials/encrypted_file.go`
- `core/credentials/profiles.go`

**Dependencies:** 0.61 (Directory Manager).

**Acceptance Criteria:**

#### Resolution Order
- [ ] 1. Environment variable (ANTHROPIC_API_KEY, etc.)
- [ ] 2. System keychain (if available)
- [ ] 3. Encrypted file fallback

#### Keychain Integration
- [ ] macOS: Keychain Access API
- [ ] Linux: Secret Service API (D-Bus)
- [ ] Windows: Credential Manager API
- [ ] Graceful fallback if unavailable

#### Encrypted File Fallback
- [ ] Hardware-backed key if available (TPM/Secure Enclave)
- [ ] Machine-bound key derivation (Argon2id)
- [ ] NOT portable between machines (intentional)
- [ ] AES-256-GCM encryption

#### Verification
- [ ] Verify API key before storing (lightweight API call)
- [ ] Clear error message on invalid key
- [ ] Support --skip-verify flag for offline setup

#### Named Profiles
- [ ] Default profile used when unspecified
- [ ] sylk auth <provider> --profile <name>
- [ ] sylk --profile <name> to use profile
- [ ] SYLK_PROFILE environment override
- [ ] Project can specify profile in .sylk/config.yaml

**Tests:**
- [ ] Environment variable takes precedence
- [ ] Keychain storage/retrieval works
- [ ] Encrypted file works when keychain unavailable
- [ ] Verification catches invalid keys
- [ ] Profile switching works

---

### 0.64 Database Manager

SQLite database management with WAL, connection pooling, and migrations.

**Files to create:**
- `core/database/manager.go`
- `core/database/pool.go`
- `core/database/migration.go`
- `core/database/locks.go`
- `core/database/migrations/system.go`
- `core/database/migrations/knowledge.go`
- `core/database/migrations/session.go`

**Dependencies:** 0.61 (Directory Manager).

**Acceptance Criteria:**

#### Database Organization
- [ ] system.db: GLOBAL (sessions, subscription, circuit breakers)
- [ ] projects/{hash}/knowledge.db: PER-PROJECT knowledge
- [ ] projects/{hash}/index.db: PER-PROJECT VectorGraphDB
- [ ] sessions/{id}/session.db: PER-SESSION state

#### SQLite Configuration
- [ ] WAL mode for concurrent access
- [ ] Configurable busy timeout (default 30s)
- [ ] Foreign keys enabled
- [ ] Appropriate cache size

#### Connection Pooling
- [ ] Per-database connection pool
- [ ] Configurable pool size
- [ ] Connection health checks
- [ ] Graceful shutdown

#### Migrations
- [ ] Embedded schema versioning (PRAGMA user_version)
- [ ] Additive migrations: apply directly
- [ ] Destructive migrations: backup first
- [ ] Transaction per migration
- [ ] Rollback on failure

#### Advisory Locks
- [ ] Cross-process locks for critical operations
- [ ] Lock files in state/locks/
- [ ] Used for: migrations, rebuilds, backups
- [ ] Timeout-aware acquisition

**Tests:**
- [ ] WAL mode enables concurrent reads
- [ ] Migrations apply in order
- [ ] Destructive migration creates backup
- [ ] Advisory locks prevent conflicts
- [ ] Pool handles connection failures

---

### 0.65 Backup Manager

Database backup with periodic, pre-session, and pre-migration triggers.

**Files to create:**
- `core/backup/manager.go`
- `core/backup/scheduler.go`
- `core/backup/retention.go`

**Dependencies:** 0.61 (Directory Manager), 0.64 (Database Manager).

**Acceptance Criteria:**

#### Backup Triggers
- [ ] Periodic: configurable interval (default 24h)
- [ ] Pre-session: before each new session starts
- [ ] Pre-migration: before destructive schema changes

#### Backup Implementation
- [ ] SQLite Online Backup API (non-blocking)
- [ ] Consistent snapshot
- [ ] Timestamped filenames

#### Backup Location
- [ ] {data}/backups/system/
- [ ] {data}/backups/projects/{hash}/

#### Retention
- [ ] Keep last N backups (configurable, default 10)
- [ ] Automatic cleanup of older backups
- [ ] Preserve pre-migration backups longer (configurable)

#### Scheduler
- [ ] Background goroutine for periodic backups
- [ ] Configurable schedule
- [ ] Skip if backup already recent

**Tests:**
- [ ] Periodic backup triggers on schedule
- [ ] Pre-session backup created
- [ ] Retention cleanup removes oldest
- [ ] Online backup doesn't block reads

---

### 0.66 Integrity Monitor

Database integrity checking with auto-recovery.

**Files to create:**
- `core/integrity/monitor.go`
- `core/integrity/recovery.go`
- `core/integrity/rebuild.go`

**Dependencies:** 0.64 (Database Manager), 0.65 (Backup Manager).

**Acceptance Criteria:**

#### Detection
- [ ] On startup: full PRAGMA integrity_check
- [ ] Periodic: quick check every 5 minutes (configurable)
- [ ] On error: check after unexpected SQL errors

#### Recovery Flow
- [ ] Log corruption details
- [ ] Notify user
- [ ] Attempt auto-restore from backup if available
- [ ] Offer rebuild if database is rebuildable

#### Rebuildable Databases
- [ ] index.db: re-index codebase
- [ ] knowledge.db: rebuild from logs + re-learn

#### Non-Rebuildable
- [ ] system.db: require backup
- [ ] session.db: require backup

#### Rebuild Process
- [ ] Create fresh database
- [ ] Re-run indexing (Librarian for index.db)
- [ ] Mark as rebuilt (may have lost some data)

**Tests:**
- [ ] Startup check catches corruption
- [ ] Periodic check runs on schedule
- [ ] Auto-restore from backup works
- [ ] Rebuild creates valid database
- [ ] User notified of recovery actions

---

### 0.67 Tier 3 Integration Tests

End-to-end tests for storage and configuration system.

**Files to create:**
- `core/storage/integration_test.go`
- `core/config/integration_test.go`
- `core/credentials/integration_test.go`
- `core/database/integration_test.go`

**Dependencies:** 0.61-0.66.

**Acceptance Criteria:**

#### Directory Tests
- [ ] Platform-native paths correct
- [ ] XDG override works
- [ ] Project-local detection works

#### Config Tests
- [ ] Full layering scenario
- [ ] Hot reload updates running system
- [ ] Invalid config handled gracefully

#### Credential Tests
- [ ] Full resolution chain
- [ ] Profile switching
- [ ] Verification flow

#### Database Tests
- [ ] Multi-session concurrent access
- [ ] Migration sequence
- [ ] Backup and restore cycle
- [ ] Corruption detection and recovery

#### Stress Tests
- [ ] Many concurrent DB operations
- [ ] Rapid config reloads
- [ ] Backup during heavy writes

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] Cross-platform CI passes

---

### Tier 3 Parallelization Notes

**CRITICAL: Tier 3 has NO dependencies on Tier 1 or Tier 2.**

These tasks can be executed in parallel with ALL other work:

```
Tier 3 (Storage & Config)     Tier 1 & 2 (Agent Infrastructure)
═══════════════════════════   ═══════════════════════════════════

0.61 Directory Manager ─┐     0.7-0.60 (all Tier 1 & 2 tasks)
0.62 Config Manager ────┤         │
0.63 Credential Manager ┼─────────┼───► Can run completely in parallel
0.64 Database Manager ──┤         │
0.65 Backup Manager ────┤         │
0.66 Integrity Monitor ─┤         │
0.67 Integration Tests ─┘         │
```

**Within Tier 3, dependencies:**
```
0.61 (Dirs) ─┬─► 0.62 (Config)
             ├─► 0.63 (Credentials)
             └─► 0.64 (Database) ─┬─► 0.65 (Backup)
                                  └─► 0.66 (Integrity)
                                           │
All 0.61-0.66 ─────────────────────────────┴─► 0.67 (Integration)
```

**Optimal parallel execution:**
1. Start 0.61 immediately (no dependencies)
2. When 0.61 completes → start 0.62, 0.63, 0.64 in parallel
3. When 0.64 completes → start 0.65, 0.66 in parallel
4. When all 0.61-0.66 complete → start 0.67

**Integration points (connect later):**
- 0.39 (Session Registry) will USE 0.64 (Database Manager) for SQLite access
- 0.55 (Subscription Tracker) will USE 0.64 for database, 0.61 for paths
- VectorGraphDB will USE 0.64 for database management
- All agents will USE 0.62 for configuration

These integration points are wired up AFTER both sides complete - no blocking dependency.

---

## Tier 4: Security Model

Security infrastructure providing defense-in-depth through permissions, sandboxing, audit logging, and session isolation.

**CRITICAL: Tier 4 has NO dependencies on Tier 1, 2, or 3 core implementations.**

Tier 4 can run in parallel with all other work. Integration points connect after both sides complete.

---

### 0.68 Permission Manager ✅ COMPLETED

Role-based permission system with per-project persistent allowlists.

**Files created:**
- `core/security/permission_manager.go` ✅
- `core/security/roles.go` ✅
- `core/security/allowlist.go` ✅
- `core/security/safe_defaults.go` ✅
- `core/security/pipeline_permissions.go` ✅

**Dependencies:** None (foundational).

**Acceptance Criteria:**

#### Role System
- [x] `AgentRole` type with 6 levels: Observer, ObserverKnowledge, Worker, Supervisor, Orchestrator, Admin
- [x] `DefaultAgentRoles` mapping agent types to default roles
- [x] `roleHasCapability(role, action)` function
- [x] Role hierarchy enforced (higher roles have lower role permissions)

#### Permission Actions
- [x] `PermissionAction` struct with Type, Target, PathPerm fields
- [x] Action types: Command, Network, Path, Config, Credential
- [x] `PathPerm` struct with Read, Write, Delete booleans

#### Safe Defaults
- [x] `DefaultSafeCommands()` returns read-only and common build commands
- [x] `DefaultSafeDomains()` returns package registries and documentation sites
- [x] Configurable via `security.permissions.custom_safe_list` path

#### Permission Checking
- [x] `CheckPermission(ctx, agentID, role, action)` returns `PermissionResult`
- [x] Check order: role → safe list → project allowlist → require approval
- [x] Command matching ignores arguments (allow `ls` = allow `ls -la`)
- [x] Domain matching is exact (no wildcards)

#### Project Allowlists
- [x] Load/save from `.sylk/local/permissions.yaml` (gitignored)
- [x] `ApproveAndPersist(action)` adds to allowlist and saves
- [x] Separate maps: commandAllowlist, domainAllowlist, pathAllowlist
- [x] Special handling for .env and .gitignore (modify OK, delete requires approval)

#### Pipeline Integration
- [x] `WorkflowPermissions` struct for pre-declared permissions
- [x] `PipelinePermissionManager` wraps base manager with workflow scope
- [x] Pre-declared permissions approved at workflow start
- [x] Runtime escalation support (if `AllowRuntimeEscalation` true)

**Tests:**
- [x] Role capability checking correct
- [x] Safe defaults always allowed without prompt
- [x] Project allowlist persisted and reloaded
- [x] Command matching ignores arguments
- [x] Pipeline pre-declared permissions work
- [x] Runtime escalation queued correctly

---

### 0.69 Sandbox Manager ✅ COMPLETED

Layered sandbox providing OS isolation, VFS, and network proxy.

**Files to create:**
- `core/security/sandbox.go`
- `core/security/sandbox_linux.go` (bubblewrap)
- `core/security/sandbox_darwin.go` (Seatbelt)
- `core/security/sandbox_windows.go` (Job Objects)
- `core/security/vfs.go`
- `core/security/network_proxy.go`

**Dependencies:** None (foundational).

**Acceptance Criteria:**

#### Sandbox Core
- [x] `SandboxConfig` struct with Enabled, OSIsolation, VFSLayer, NetworkProxy bools
- [x] Default: Enabled=false (OFF by default)
- [x] `Sandbox` struct with Execute method
- [x] Graceful degradation: if sandbox unavailable, warn and continue (configurable)

#### OS Sandbox (Linux - bubblewrap)
- [x] Check `bwrap` available at startup
- [x] `wrapWithBubblewrap(cmd)` creates isolated command
- [x] Namespace isolation (--unshare-all)
- [x] Working directory bind-mounted read-write
- [x] System paths bind-mounted read-only
- [x] Resource limits (--die-with-parent)

#### OS Sandbox (macOS - Seatbelt)
- [x] `wrapWithSeatbelt(cmd)` creates sandbox-exec command
- [x] Generate Seatbelt profile dynamically
- [x] Deny default, allow specific operations
- [x] Working directory allowed read-write
- [x] System paths allowed read-only

#### OS Sandbox (Windows)
- [x] Job Objects for process containment (stub - returns unavailable)
- [x] Resource limits via Job Object (stub - returns unavailable)
- [x] Document limitations vs Unix sandboxes (self-documenting via ErrSandboxUnavailable)

#### Virtual Filesystem Layer
- [x] `VirtualFilesystem` struct with staging directory
- [x] `PrepareForCommand(cmd)` sets up staging
- [x] `ValidatePath(path)` checks boundary violations
- [x] Symlink resolution to detect escapes
- [x] User-approved paths tracked in `approvedPaths`
- [x] `MergeChanges(cmd)` applies staged modifications
- [x] Copy-on-write semantics

#### Network Proxy
- [x] `NetworkProxy` struct with HTTP proxy listener
- [x] `RouteCommand(cmd)` sets HTTP_PROXY env vars
- [x] Domain allowlist checking in handleRequest
- [x] Log allowed and blocked requests (via stats tracking)
- [x] Statistics tracking (requestsAllowed, requestsBlocked)

#### CLI Commands
- [ ] `/sandbox status` shows current configuration (cmd layer - separate item)
- [ ] `/sandbox enable` enables all layers (cmd layer - separate item)
- [ ] `/sandbox disable` disables with warning (cmd layer - separate item)
- [ ] `/sandbox enable vfs` enables specific layer (cmd layer - separate item)
- [ ] `/sandbox allow path /etc/hosts` adds path exception (cmd layer - separate item)

**Tests:**
- [x] Sandbox disabled by default
- [x] bubblewrap wrapping works on Linux
- [x] Seatbelt wrapping works on macOS
- [x] VFS prevents writes outside working dir
- [x] VFS merges changes correctly
- [x] Network proxy blocks unlisted domains
- [x] Graceful degradation when sandbox unavailable

---

### 0.70 Audit Logger ✅ COMPLETED

Tamper-evident audit logging with cryptographic chaining.

**Files to create:**
- `core/security/audit_logger.go`
- `core/security/audit_entry.go`
- `core/security/audit_query.go`
- `core/security/audit_integrity.go`

**Dependencies:** None (foundational).

**Acceptance Criteria:**

#### Audit Entry Structure
- [x] `AuditEntry` struct with all fields from ARCHITECTURE.md
- [x] ID (UUID), Timestamp, Sequence (monotonic)
- [x] Category: permission, file, process, network, llm, session, config
- [x] Severity: info, warning, security, critical
- [x] Context: SessionID, ProjectID, AgentID, WorkflowID
- [x] Integrity: PreviousHash, EntryHash

#### Logging Operations
- [x] `Log(entry)` writes tamper-evident entry
- [x] Automatic sequence numbering
- [x] Hash chain: each entry contains hash of previous
- [x] `computeEntryHash(entry)` uses SHA256
- [x] Append-only file writing
- [x] File rotation at configurable size

#### Periodic Signatures
- [x] Ed25519 signing key generated/loaded at startup
- [x] `writeSignature()` every N entries (default: 100)
- [x] `AuditSignature` struct with SequenceFrom, SequenceTo, ChainHash, Signature
- [x] Signature lines prefixed with "SIG:"

#### Integrity Verification
- [x] `VerifyIntegrity()` returns `IntegrityReport`
- [x] Check hash chain continuity
- [x] Check sequence continuity
- [x] Verify entry hashes match
- [x] Verify periodic signatures
- [x] Report errors without stopping on first

#### Category-Specific Logging
- [x] `LogPermissionGranted/Denied(agentID, action, source/reason)`
- [x] `LogFileOperation(op, path, agentID, size, hash)`
- [x] `LogProcessExecution(cmd, agentID, exitCode)`
- [x] `LogNetworkAllowed/Blocked(domain, url)`
- [x] `LogLLMCall(provider, model, tokens, cost)`
- [x] `LogSessionEvent(event, sessionID)`

#### Configuration
- [x] `AuditLogConfig` struct with all settings
- [x] Retention policy: "indefinite" (default)
- [x] Rotation size: 100MB default
- [x] Signature interval: 100 entries default

**Tests:**
- [x] Entries have correct hash chain
- [x] Sequence numbers are continuous
- [x] Signature verification works
- [x] Tampering detected (modify entry, check fails)
- [x] File rotation works (implemented, implicit in checkRotation)
- [x] All category loggers work

---

### 0.71 Audit Query Interface ✅ COMPLETED

CLI and programmatic interface for querying audit logs.

**Files to create:**
- `core/security/audit_query.go` (extends 0.70)
- `cmd/audit.go`

**Dependencies:** 0.70.

**Acceptance Criteria:**

#### Query Filter
- [x] `QueryFilter` struct with all filter options
- [x] Time range: StartTime, EndTime
- [x] Category filter: []AuditCategory
- [x] Severity filter: []AuditSeverity
- [x] Session/Agent/Workflow ID filters
- [x] Action and outcome filters
- [x] Target pattern (regex) filter
- [x] Pagination: Limit, Offset

#### Query Execution
- [x] `Query(filter)` returns matching entries
- [x] Efficient scanning (don't load entire file for recent queries)
- [x] Support querying across rotated log files

#### Output Formats
- [x] Table format (default): human-readable columns
- [x] JSON format: machine-parseable
- [x] CSV format: spreadsheet-compatible

#### CLI Commands
- [ ] `sylk audit query --category permission --severity security --since 24h`
- [ ] `sylk audit query --session abc123 --format json`
- [ ] `sylk audit query --action "file_write" --target "*.go"`
- [ ] `sylk audit verify` runs integrity check
- [ ] `sylk audit export --since 7d` exports for external analysis
- [ ] `sylk audit purge --before 90d` purges old entries (with confirmation)

#### Purge Operation
- [x] Require explicit confirmation (caller responsibility - CLI layer)
- [x] Write final signature before purging (via logger.Close())
- [x] Create backup before purge (optional)
- [x] Log purge operation

**Tests:**
- [x] Query by category works
- [x] Query by time range works
- [x] Query by target pattern works
- [x] All output formats correct
- [x] Purge requires confirmation (caller responsibility)
- [x] Purge creates backup

---

### 0.72 Session Credential Manager ✅ COMPLETED

Session-scoped credential handling with profile overrides and temporary credentials.

**Files created:**
- `core/security/session_credentials.go` ✅
- `core/security/session_credentials_test.go` ✅

**Dependencies:** 0.63 (Credential Manager).

**Acceptance Criteria:**

#### Credential Resolution
- [x] `SessionCredentialManager` wraps base `CredentialManager`
- [x] Resolution order: temp credentials → profile override → base manager
- [x] `GetAPIKey(provider)` follows resolution chain

#### Profile Override
- [x] `SetProfileOverride(profile)` changes profile for session
- [x] Verify profile exists before setting
- [x] Override cleared on session end

#### Temporary Credentials
- [x] `TempCredential` struct with Provider, APIKey, ExpiresAt, Source
- [x] `SetTemporaryCredential(provider, apiKey, ttl)` stores in memory
- [x] Temporary credentials never persisted to disk
- [x] Auto-expire based on TTL
- [x] Max TTL configurable (default: 24h)

#### Session Cleanup
- [x] `ClearSession()` zeros and removes all temp credentials
- [x] Called automatically on session end
- [x] Profile override cleared

#### Audit Integration
- [x] Log credential access events (not values)
- [x] Log profile override changes
- [x] Log temp credential usage (provider only, not key)

**Tests:**
- [x] Resolution order correct
- [x] Profile override works
- [x] Temp credentials expire correctly
- [x] Session cleanup removes all credentials
- [x] Temp credentials not shared between sessions

---

### 0.73 Session Knowledge Manager ✅ COMPLETED

Cross-session knowledge sharing with proper isolation.

**Files created:**
- `core/session/knowledge_manager.go` ✅
- `core/session/knowledge_manager_test.go` ✅

**Dependencies:** VectorGraphDB (existing).

**Acceptance Criteria:**

#### Knowledge Scoping
- [x] `SessionKnowledgeManager` with project and session knowledge DBs
- [x] `KnowledgeScope` enum: ScopeProject, ScopeSession
- [x] Project knowledge: shared, Archivalist write access
- [x] Session knowledge: session writes, others read-only

#### Store Operations
- [x] `StoreKnowledge(entry)` routes to appropriate DB
- [x] Project scope: only Archivalist can write
- [x] Session scope: current session writes

#### Query Operations
- [x] `QueryKnowledge(query, opts)` searches accessible DBs
- [x] Always includes project knowledge
- [x] Always includes own session knowledge
- [x] `IncludeOtherSessions` option for read-only access to other sessions
- [x] Deduplicate results

#### Other Session Views
- [x] `otherSessionViews` map for read-only access
- [x] Views discovered via session registry
- [x] Read errors don't fail query

**Tests:**
- [x] Project knowledge shared across sessions
- [x] Session knowledge isolated
- [x] Cross-session read-only works
- [x] Archivalist can write project knowledge
- [x] Non-Archivalist cannot write project knowledge

---

### 0.74 Tier 4 Integration Tests

End-to-end tests for security system.

**Files to create:**
- `core/security/integration_test.go`
- `core/security/permission_integration_test.go`
- `core/security/sandbox_integration_test.go`
- `core/security/audit_integration_test.go`

**Dependencies:** 0.68-0.73.

**Acceptance Criteria:**

#### Permission Integration
- [ ] Full permission check flow
- [ ] Allowlist persistence and reload
- [ ] Pipeline permission inheritance
- [ ] Runtime escalation flow

#### Sandbox Integration
- [ ] Full sandbox execution (where available)
- [ ] VFS staging and merge
- [ ] Network proxy with allowlist
- [ ] Graceful degradation

#### Audit Integration
- [ ] All event types logged
- [ ] Integrity verification passes
- [ ] Query filters work together
- [ ] Cross-session queries

#### Session Integration
- [ ] Credential isolation between sessions
- [ ] Knowledge sharing and isolation
- [ ] Session cleanup complete

#### Stress Tests
- [ ] Many concurrent permission checks
- [ ] Rapid sandbox executions
- [ ] High-volume audit logging
- [ ] Integrity check performance

**Tests:**
- [ ] All integration scenarios pass
- [ ] No race conditions (run with -race)
- [ ] Cross-platform CI passes

---

### Tier 4 Parallelization Notes

**CRITICAL: Tier 4 has NO dependencies on Tier 1, 2, or 3.**

These tasks can be executed in parallel with ALL other work:

```
Tier 4 (Security)             Tier 1, 2, 3 (All other work)
═══════════════════════════   ═══════════════════════════════════

0.68 Permission Manager ─┐    0.7-0.67 (all Tier 1, 2, 3 tasks)
0.69 Sandbox Manager ────┤         │
0.70 Audit Logger ───────┼─────────┼───► Can run completely in parallel
0.71 Audit Query ────────┤         │
0.72 Session Credentials ┤         │
0.73 Session Knowledge ──┤         │
0.74 Integration Tests ──┘         │
```

**Within Tier 4, dependencies:**
```
0.68 (Permissions) ──────────────────┐
0.69 (Sandbox) ──────────────────────┤
0.70 (Audit Logger) ─► 0.71 (Query)  │
                                     ├──► 0.74 (Integration)
0.72 (Session Creds) ────────────────┤
     depends on 0.63                 │
0.73 (Session Knowledge) ────────────┘
     depends on VectorGraphDB
```

**Optimal parallel execution:**
1. Start 0.68, 0.69, 0.70 immediately (no internal deps)
2. When 0.70 completes → start 0.71
3. When 0.63 completes → start 0.72
4. When VectorGraphDB ready → start 0.73
5. When all 0.68-0.73 complete → start 0.74

**Integration points (connect later):**
- 0.68 integrates with all agents (permission checks)
- 0.69 integrates with subprocess execution (0.43)
- 0.70 integrates with all components (logging)
- 0.72 integrates with 0.63 (base credential manager)
- 0.73 integrates with VectorGraphDB

These integration points are wired up AFTER both sides complete - no blocking dependency.

---

## Session Architecture

### Session Isolation Model

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              SESSION ISOLATION MODEL                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  SESSION-LOCAL (Isolated per session):                                              │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Guide routing state (pending requests, session route cache)                 │ │
│  │  • Agent instances (Architect, Orchestrator, Engineers, Inspector, Tester)     │ │
│  │  • Workflow state (current DAG, phase, progress)                               │ │
│  │  • Session context (metadata, branch, user preferences)                        │ │
│  │  • Bus subscriptions (session-scoped topics)                                   │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  SESSION-SHARED (Read-only access across sessions):                                 │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Archivalist historical DB (query across sessions, write to own session)     │ │
│  │  • Librarian index (shared codebase, read-only)                                │ │
│  │  • Academic sources (shared research, read-only)                               │ │
│  │  • Global route cache (common routes, read-only fallback)                      │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  CONTEXT POLLUTION PREVENTION:                                                      │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • All writes tagged with session ID                                           │ │
│  │  • Default queries filtered by session ID                                      │ │
│  │  • Cross-session queries explicit and read-only                                │ │
│  │  • No shared mutable state between sessions                                    │ │
│  │  • Agent instances never shared (except Librarian, Academic, Archivalist)      │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Session Lifecycle

```
CREATE          ACTIVE              PAUSE              RESUME            COMPLETE
   │               │                   │                  │                  │
   ▼               ▼                   ▼                  ▼                  ▼
┌──────┐      ┌────────┐          ┌────────┐        ┌────────┐        ┌──────────┐
│Create│─────▶│ Active │─────────▶│ Paused │───────▶│ Active │───────▶│ Complete │
│State │      │Working │          │Saved   │        │Restored│        │ Archived │
└──────┘      └────────┘          └────────┘        └────────┘        └──────────┘
   │               │                   │                  │                  │
   │               │                   │                  │                  │
   │          ┌────┴────┐              │                  │                  │
   │          ▼         ▼              │                  │                  │
   │     Execute    Interrupt          │                  │                  │
   │     Workflow   (User)             │                  │                  │
   │          │         │              │                  │                  │
   │          └────┬────┘              │                  │                  │
   │               │                   │                  │                  │
   │               └───────────────────┘                  │                  │
   │                                                      │                  │
   └──────────────────────────────────────────────────────┘                  │
                                                                             │
                      Archivalist stores all session history ◀───────────────┘
```

---

## Concurrency Design Principles

### 1. All Messages Through Guide
```go
// CORRECT: Route through Guide
guide.PublishRequest(&RouteRequest{
    SessionID:     session.ID(),
    Input:         "query codebase for middleware",
    TargetAgentID: "librarian",
})

// WRONG: Direct agent call
librarian.Query("middleware") // Never do this between agents
```

### 2. Session-Scoped State
```go
// All state operations include session context
store.Insert(entry, session.ID())
cache.Get(key, session.ID())
pending.Add(request, session.ID())
```

### 3. Shared Read, Isolated Write
```go
// Read from any session (explicit)
archivalist.QueryCrossSession(query)

// Write only to own session (enforced)
archivalist.Store(entry) // Automatically tagged with session ID
```

### 4. Bounded Concurrency
```go
type SessionConfig struct {
    MaxConcurrentTasks int // e.g., 50
    MaxEngineers       int // e.g., 20
}

type SystemConfig struct {
    MaxSessions        int // e.g., 100
    MaxTotalEngineers  int // e.g., 500
}
```

---

## Existing Agent Modifications

This section details the specific changes required to make the existing Guide and Archivalist agents compatible with the multi-session architecture.

### Guide Agent Modifications (`agents/guide/`)

The existing Guide agent needs significant modifications to support multi-session routing, session-scoped state, and the new agent ecosystem.

#### Required Code Changes

**1. Session-Aware Routing (`guide.go`)**
```go
// CURRENT: Single-session routing
type Guide struct {
    sessionID string
    // ...
}

// REQUIRED: Multi-session support
type Guide struct {
    sessionManager    SessionManager           // Reference to session manager
    sessionRouters    map[string]*SessionRouter // Per-session routing state
    globalRouteCache  *RouteCache              // Shared route cache (read-only fallback)
    sharedAgents      map[string]Agent         // Librarian, Academic, Archivalist
    // ...
}

// Add session context to all routing methods
func (g *Guide) RouteWithSession(ctx context.Context, sessionID string, req *RouteRequest) (*RouteResult, error)
func (g *Guide) GetSessionRouter(sessionID string) (*SessionRouter, error)
func (g *Guide) CreateSessionRouter(sessionID string) (*SessionRouter, error)
func (g *Guide) CloseSessionRouter(sessionID string) error
```

**2. New SessionRouter Type (`session_router.go` - new file)**
```go
type SessionRouter struct {
    sessionID        string
    routeCache       *RouteCache           // Session-scoped cache
    pendingRequests  *PendingRequestStore  // Session-scoped pending
    agentRegistry    map[string]Agent      // Session-scoped agents (Architect, Orchestrator, etc.)
    subscriptions    []func()              // Cleanup functions for session topics
    mu               sync.RWMutex
}
```

**3. Message Type Extensions (`types.go`)**
- [ ] Add all new message types (DAG_EXECUTE, DAG_STATUS, DAG_CANCEL, etc.)
- [ ] Add SessionID field to RouteRequest
- [ ] Add SessionID field to RouteResult
- [ ] Add message priority field
- [ ] Add correlation ID for request/response matching

**4. Bus Integration Updates (`bus.go`)**
- [ ] Add session topic creation: `session.{id}.{agent}.{channel}`
- [ ] Add session topic cleanup on session close
- [ ] Add wildcard subscription support
- [ ] Add message filtering by session ID

**5. Skill System Updates (`skills.go` - new file)**
- [ ] Implement `SkillLoader` interface for progressive disclosure
- [ ] Add skill registration by tier (Core, Domain, Specialized)
- [ ] Add skill loading triggers (domain keywords, explicit request)
- [ ] Add skill unloading with LRU eviction
- [ ] Add token budget tracking for loaded skills

**6. Hook System Updates**
- [ ] Add pre-route hook injection point
- [ ] Add post-route hook injection point
- [ ] Add pre-dispatch hook injection point
- [ ] Add post-dispatch hook injection point
- [ ] Implement hook chain execution with abort capability

#### Required Interface Changes

```go
// Current Skills() signature - needs enhancement
func (g *Guide) Skills() []skills.Skill

// Required signature
func (g *Guide) Skills() []skills.Skill                    // Core skills (always loaded)
func (g *Guide) ExtendedSkills() []skills.Skill            // Extended skills (on demand)
func (g *Guide) LoadSkill(name string) error               // Load specific skill
func (g *Guide) UnloadSkill(name string) error             // Unload skill
func (g *Guide) LoadedSkills() []string                    // Currently loaded skills
func (g *Guide) SkillTokenBudget() (used int, max int)     // Token tracking

// Current Hooks() signature - needs enhancement
func (g *Guide) Hooks() skills.AgentHooks

// Required hook types
type GuideHooks struct {
    PreRoute     []func(ctx context.Context, req *RouteRequest) (*RouteRequest, error)
    PostRoute    []func(ctx context.Context, req *RouteRequest, result *RouteResult) error
    PreDispatch  []func(ctx context.Context, target string, msg *Message) (*Message, error)
    PostDispatch []func(ctx context.Context, target string, msg *Message, result any) error
}
```

#### Required New Methods

```go
// Session management
func (g *Guide) AttachSession(session *Session) error
func (g *Guide) DetachSession(sessionID string) error
func (g *Guide) ActiveSession() (*Session, bool)
func (g *Guide) SwitchSession(sessionID string) error

// Agent registration (session-scoped)
func (g *Guide) RegisterSessionAgent(sessionID string, agent Agent) error
func (g *Guide) UnregisterSessionAgent(sessionID string, agentID string) error
func (g *Guide) GetSessionAgents(sessionID string) []Agent

// Shared agent access
func (g *Guide) GetSharedAgent(agentID string) (Agent, bool)
func (g *Guide) RegisterSharedAgent(agent Agent) error

// Message routing
func (g *Guide) RouteToSession(sessionID string, msg *Message) error
func (g *Guide) BroadcastToSession(sessionID string, msg *Message) error

// Metrics (session-aware)
func (g *Guide) SessionMetrics(sessionID string) *SessionRouteMetrics
func (g *Guide) GlobalMetrics() *GlobalRouteMetrics
```

#### Files to Modify

| File | Changes |
|------|---------|
| `guide.go` | Add SessionManager reference, multi-session support, session-aware routing |
| `types.go` | Add new message types, SessionID fields, priority field |
| `routing.go` | Session-scoped route resolution, global fallback |
| `bus.go` | Session topic support, wildcard subscriptions |
| `route_cache.go` | Session-scoped cache with global fallback |
| `worker_pool.go` | Session-aware job scheduling, fair allocation |

#### Files to Create

| File | Purpose |
|------|---------|
| `session_router.go` | Per-session routing state and logic |
| `message_types.go` | New message type definitions |
| `skills.go` | Skill loading/unloading, progressive disclosure |
| `hooks.go` | Hook chain implementation |

---

### Archivalist Agent Modifications (`agents/archivalist/`)

The existing Archivalist agent needs modifications to enforce session isolation, support cross-session queries, and store workflow history.

#### Required Code Changes

**1. Session Enforcement (`archivalist.go`)**
```go
// CURRENT: Optional session tracking
type Archivalist struct {
    // sessionID used but not enforced
}

// REQUIRED: Mandatory session enforcement
type Archivalist struct {
    defaultSessionID string                    // Default session for writes
    sessionStores    map[string]*SessionStore  // Per-session storage partitions
    crossSessionIdx  *CrossSessionIndex        // Index for cross-session queries
    workflowStore    *WorkflowStore            // DAG execution history
    // ...
}

// All write operations MUST include session
func (a *Archivalist) Store(entry *Entry) error                          // Uses defaultSessionID
func (a *Archivalist) StoreInSession(sessionID string, entry *Entry) error // Explicit session

// All read operations default to current session
func (a *Archivalist) Query(query *ArchiveQuery) ([]*Entry, error)       // Current session only
func (a *Archivalist) QueryCrossSession(query *ArchiveQuery) ([]*Entry, error) // All sessions
```

**2. Session Store Type (`session_store.go` - new file)**
```go
type SessionStore struct {
    sessionID      string
    entries        map[string]*Entry
    facts          *FactStore           // Session-local facts
    summaries      []*CompactedSummary  // Session summaries
    tokenCount     int                  // Hot memory token tracking
    mu             sync.RWMutex
}

// Session-scoped operations
func (s *SessionStore) Insert(entry *Entry) error
func (s *SessionStore) Get(id string) (*Entry, bool)
func (s *SessionStore) Query(query *ArchiveQuery) ([]*Entry, error)
func (s *SessionStore) Archive(entryID string) error
func (s *SessionStore) TokenBudget() (used int, max int)
```

**3. Cross-Session Query Support (`cross_session.go` - new file)**
```go
type CrossSessionIndex struct {
    byCategory  map[Category][]string    // Entry IDs by category (all sessions)
    bySource    map[SourceModel][]string // Entry IDs by source
    byKeyword   map[string][]string      // Entry IDs by keyword
    sessionMap  map[string]string        // Entry ID → Session ID
    mu          sync.RWMutex
}

func (c *CrossSessionIndex) Search(query *ArchiveQuery) ([]CrossSessionResult, error)
func (c *CrossSessionIndex) IndexEntry(sessionID string, entry *Entry) error
func (c *CrossSessionIndex) RemoveEntry(entryID string) error

type CrossSessionResult struct {
    Entry     *Entry
    SessionID string
    Score     float64  // Relevance score
}
```

**4. Workflow Storage (`workflow_store.go` - new file)**
```go
type WorkflowStore struct {
    dags      map[string]*StoredDAG       // DAG ID → definition
    runs      map[string]*DAGRun          // Run ID → execution record
    bySession map[string][]string         // Session ID → Run IDs
    mu        sync.RWMutex
}

type StoredDAG struct {
    ID          string
    Definition  *DAG
    SessionID   string
    CreatedAt   time.Time
    ExecutionCount int
}

type DAGRun struct {
    ID           string
    DAGID        string
    SessionID    string
    StartTime    time.Time
    EndTime      *time.Time
    Status       DAGStatus
    NodeResults  map[string]*NodeResult
    Corrections  []*Correction
    FinalOutcome string
}

func (w *WorkflowStore) StoreDAG(sessionID string, dag *DAG) (string, error)
func (w *WorkflowStore) RecordRun(run *DAGRun) error
func (w *WorkflowStore) QuerySimilarWorkflows(query string) ([]*StoredDAG, error)
func (w *WorkflowStore) GetSessionHistory(sessionID string) ([]*DAGRun, error)
```

**5. Type Updates (`types.go`)**
- [ ] Add `IncludeArchived` flag to `ArchiveQuery` (already exists, verify usage)
- [ ] Add `SessionIDs` filter for cross-session queries (already exists, enhance)
- [ ] Add `WorkflowQuery` struct for DAG history queries
- [ ] Add `CrossSessionResult` wrapper type
- [ ] Add `DAGRun` and `StoredDAG` types

**6. Skill System Updates (`skills.go` - new file)**
```go
// Core Skills (always loaded)
var CoreSkills = []skills.Skill{
    skills.NewSkill("store").Description("Store an entry").Domain("storage"),
    skills.NewSkill("query").Description("Query entries").Domain("storage"),
    skills.NewSkill("briefing").Description("Get session briefing").Domain("status"),
}

// Extended Skills (on demand)
var ExtendedSkills = []skills.Skill{
    skills.NewSkill("cross_session_query").Description("Query across sessions").Domain("history"),
    skills.NewSkill("workflow_history").Description("Query past workflows").Domain("history"),
    skills.NewSkill("token_savings").Description("Get token savings report").Domain("metrics"),
    skills.NewSkill("session_timeline").Description("Get session event timeline").Domain("history"),
    skills.NewSkill("pattern_search").Description("Find similar patterns").Domain("patterns"),
    skills.NewSkill("failure_search").Description("Find similar failures").Domain("patterns"),
    skills.NewSkill("decision_search").Description("Find similar decisions").Domain("patterns"),
    skills.NewSkill("promote_to_global").Description("Promote entry to global scope").Domain("admin"),
}
```

#### Required Interface Changes

```go
// Current interface - needs enhancement
type Archivalist interface {
    Store(entry *Entry) (*SubmissionResult, error)
    Query(query *ArchiveQuery) ([]*Entry, error)
    // ...
}

// Required interface additions
type Archivalist interface {
    // Session management
    SetDefaultSession(sessionID string)
    GetDefaultSession() string

    // Session-scoped operations
    StoreInSession(sessionID string, entry *Entry) (*SubmissionResult, error)
    QueryInSession(sessionID string, query *ArchiveQuery) ([]*Entry, error)

    // Cross-session operations (read-only)
    QueryCrossSession(query *ArchiveQuery) ([]CrossSessionResult, error)
    GetSessionList() []SessionInfo
    GetSessionSummary(sessionID string) (*SessionSummary, error)

    // Workflow storage
    StoreWorkflow(sessionID string, dag *DAG) (string, error)
    RecordWorkflowRun(run *DAGRun) error
    QuerySimilarWorkflows(query string) ([]*StoredDAG, error)
    GetWorkflowHistory(sessionID string, limit int) ([]*DAGRun, error)

    // Token/savings tracking
    GetTokenSavings(sessionID string) (*TokenSavingsReport, error)
    GetGlobalTokenSavings() (*TokenSavingsReport, error)

    // Promotion (session-local → global)
    PromoteToGlobal(entryID string, reason string) error
}
```

#### Required New Methods

```go
// Session lifecycle hooks
func (a *Archivalist) OnSessionCreate(sessionID string) error
func (a *Archivalist) OnSessionClose(sessionID string) error
func (a *Archivalist) OnSessionSwitch(fromID, toID string) error

// Snapshot support for session persistence
func (a *Archivalist) CreateSessionSnapshot(sessionID string) (*ChronicleSnapshot, error)
func (a *Archivalist) RestoreSessionSnapshot(sessionID string, snapshot *ChronicleSnapshot) error

// Compaction (cross-session aware)
func (a *Archivalist) CompactSession(sessionID string) error
func (a *Archivalist) CompactGlobal() error
```

#### Files to Modify

| File | Changes |
|------|---------|
| `archivalist.go` | Session enforcement, cross-session query support |
| `storage.go` | Session-partitioned storage, workflow storage |
| `types.go` | New types for workflow storage, cross-session results |
| `query_cache.go` | Session-scoped caching, cross-session cache invalidation |

#### Files to Create

| File | Purpose |
|------|---------|
| `session_store.go` | Per-session storage partition |
| `cross_session.go` | Cross-session index and query logic |
| `workflow_store.go` | DAG definition and execution history storage |
| `skills.go` | Skill definitions with progressive disclosure |
| `hooks.go` | Hook implementations for session lifecycle |

---

### Migration Path

The following sequence ensures backward compatibility during migration:

1. **Phase A: Add Session Support (Non-Breaking)**
   - Add SessionID fields with defaults
   - Add new methods alongside existing ones
   - Existing code continues to work

2. **Phase B: Enable Session Isolation**
   - Enable session enforcement flag
   - Migrate existing data to "default" session
   - Update all callers to provide session context

3. **Phase C: Remove Legacy Methods**
   - Deprecate non-session-aware methods
   - Remove after all callers migrated
   - Enforce session context on all operations

---

## Phase 1: Knowledge Agents

**Goal**: Build the three knowledge RAG agents with skills and progressive disclosure.

**Dependencies**: Phase 0 complete

**Parallelization**: Items 1.1, 1.2 can execute in parallel after 0.1 complete.

### 1.1 Librarian (Local Codebase RAG)

Indexes and queries the current codebase. Read-only operations.

**Files to create:**
- `agents/librarian/types.go`
- `agents/librarian/indexer.go`
- `agents/librarian/ast_parser.go`
- `agents/librarian/storage.go`
- `agents/librarian/query.go`
- `agents/librarian/embeddings.go`
- `agents/librarian/librarian.go`
- `agents/librarian/routing.go`
- `agents/librarian/skills.go`
- `agents/librarian/tools.go`
- `agents/librarian/hooks.go`
- `agents/librarian/librarian_test.go`

**Acceptance Criteria:**

#### Librarian System Prompt

**Core Identity:**
- [ ] Model: Claude Sonnet 4.5 (fast code search and navigation)
- [ ] Role: Codebase RAG - local knowledge about patterns, file locations, tooling, health
- [ ] User Interaction: DIRECT (triggered by codebase queries)

**Core Responsibilities:**
- [ ] CODEBASE CONTEXT: Provide accurate context about code patterns, file locations, architecture
- [ ] TOOL DISCOVERY: Detect and report formatters, linters, LSP servers, test frameworks
- [ ] HEALTH ASSESSMENT: Evaluate codebase maturity before implementation tasks
- [ ] PATTERN DETECTION: Identify coding patterns, conventions, technical debt
- [ ] SYMBOL NAVIGATION: Support go-to-definition, find-references, call hierarchy

**Single Source of Truth For:**
- [ ] Formatters (prettier, gofmt, black, etc.)
- [ ] Linters and LSP servers
- [ ] Test frameworks and test file patterns
- [ ] Codebase patterns and conventions
- [ ] File locations and structure

**Other Agents Must Consult Before:**
- [ ] Inspector: Executing formatting or linting
- [ ] Tester: Running tests or discovering test files
- [ ] Engineer/Designer: Understanding existing patterns before implementation

**Health Assessment Protocol:**
- [ ] MATURITY LEVELS: DISCIPLINED, TRANSITIONAL, LEGACY, GREENFIELD
- [ ] Include in implementation context: "CODEBASE HEALTH [area]: [MATURITY] | Patterns: [0.0-1.0] | Coverage: [%] | Debt: [count]"

**Query Classification:**
- [ ] LOCATE: Find where something is
- [ ] PATTERN: Ask about patterns, strategies, conventions
- [ ] EXPLAIN: Understand how something works
- [ ] GENERAL: Other codebase questions

**Communication Style:**
- [ ] Be concise, direct, technical
- [ ] Provide evidence: file paths, line numbers, confidence scores
- [ ] If uncertain, say "I don't know" or "Low confidence"
- [ ] NO status acknowledgments, flattery, or hedging

**Critical Constraints:**
- [ ] NEVER guess about codebase structure - search and verify
- [ ] NEVER provide stale information - check modification times
- [ ] ALWAYS include confidence scores for pattern detection
- [ ] ALWAYS validate tool availability before reporting
- [ ] Cache results appropriately but invalidate on file changes

#### Indexing
- [ ] Index all Go source files (AST parsing for symbols, types, functions)
- [ ] Index file structure and organization
- [ ] Index imports and dependencies
- [ ] Index inline comments and documentation
- [ ] Index configuration files (go.mod, yaml, json)
- [ ] Incremental index updates on file change
- [ ] Background indexing with progress events

#### Query
- [ ] Semantic search over indexed content
- [ ] Query by file path pattern (glob)
- [ ] Query by symbol name (function, type, const, var)
- [ ] Query by usage pattern ("how is X used")
- [ ] Query by dependency ("what uses package X")
- [ ] Query by documentation content

#### Bus Integration
- [ ] Subscribe to `librarian.requests` (global, shared across sessions)
- [ ] Publish to `librarian.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Recall` (query), `Check` (verify existence)
- [ ] Support domains: `Files`, `Patterns`, `Code`

#### Librarian Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// find_symbol - Find a symbol by name
skills.NewSkill("find_symbol").
    Description("Find a function, type, constant, or variable by name").
    Domain("codebase").
    Keywords("find", "where", "locate", "symbol", "function", "type").
    StringParam("name", "Symbol name to find", true).
    EnumParam("kind", "Symbol kind", []string{"function", "type", "const", "var", "any"}, false)

// search_code - Search code content
skills.NewSkill("search_code").
    Description("Search for code patterns or text in the codebase").
    Domain("codebase").
    Keywords("search", "grep", "find", "code", "pattern").
    StringParam("query", "Search query or pattern", true).
    StringParam("file_pattern", "File glob pattern to filter", false)

// get_file - Read a file
skills.NewSkill("get_file").
    Description("Read the contents of a file").
    Domain("codebase").
    Keywords("read", "show", "get", "file", "content").
    StringParam("path", "File path to read", true).
    IntParam("start_line", "Start line (optional)", false).
    IntParam("end_line", "End line (optional)", false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_imports - Analyze import graph
skills.NewSkill("analyze_imports").
    Description("Analyze import relationships for a package").
    Domain("codebase").
    Keywords("imports", "dependencies", "packages", "graph").
    StringParam("package", "Package path to analyze", true)

// find_usages - Find all usages of a symbol
skills.NewSkill("find_usages").
    Description("Find all places where a symbol is used").
    Domain("codebase").
    Keywords("usages", "references", "callers", "used").
    StringParam("symbol", "Symbol to find usages of", true)

// explain_pattern - Explain a code pattern
skills.NewSkill("explain_pattern").
    Description("Explain a code pattern used in the codebase").
    Domain("codebase").
    Keywords("explain", "pattern", "convention", "style").
    StringParam("pattern", "Pattern to explain", true)

// list_files - List files matching pattern
skills.NewSkill("list_files").
    Description("List files matching a glob pattern").
    Domain("codebase").
    Keywords("list", "files", "glob", "directory").
    StringParam("pattern", "Glob pattern", true)

// find_files - Find files using find command
skills.NewSkill("find_files").
    Description("Find files using the find command with various criteria").
    Domain("codebase").
    Keywords("find", "locate", "files", "directory", "name", "type").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory, l=symlink)", false).
    StringParam("mtime", "Modified time (e.g., -7 for last 7 days)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// get_structure - Get file/package structure
skills.NewSkill("get_structure").
    Description("Get the structure of a file or package").
    Domain("codebase").
    Keywords("structure", "outline", "overview", "symbols").
    StringParam("path", "File or package path", true)
```

**Remote Repository Skills (dynamically configurable):**
```go
// scan_remote_repo - Scan a remote GitHub repository
skills.NewSkill("scan_remote_repo").
    Description("Scan a remote GitHub repository (including private)").
    Domain("remote").
    Keywords("github", "remote", "repo", "scan", "clone").
    StringParam("url", "GitHub repository URL", true).
    StringParam("auth_token", "GitHub auth token (for private repos)", false).
    BoolParam("shallow", "Shallow clone for faster scanning", false)

// identify_languages - Identify languages in repository
skills.NewSkill("identify_languages").
    Description("Identify programming languages used in repository").
    Domain("tooling").
    Keywords("languages", "detect", "identify", "stack").
    StringParam("path", "Repository path (local or remote)", true)

// identify_tooling - Identify development tooling
skills.NewSkill("identify_tooling").
    Description("Identify linters, formatters, LSPs, and build tools").
    Domain("tooling").
    Keywords("linter", "formatter", "lsp", "tooling", "eslint", "prettier", "gopls").
    StringParam("path", "Repository path", true).
    ArrayParam("languages", "Specific languages to check", false)

// get_tool_config - Get configuration for a specific tool
skills.NewSkill("get_tool_config").
    Description("Get configuration file and settings for a dev tool").
    Domain("tooling").
    Keywords("config", "settings", "configuration").
    StringParam("tool", "Tool name (eslint, prettier, gopls, etc.)", true).
    StringParam("path", "Repository path", true)
```

**Language/Tool Detection Registry (dynamically configurable):**
```go
// Tool configurations are loaded dynamically based on detected languages
type ToolRegistry struct {
    // Detected tools per language
    Linters    map[string][]ToolConfig  // language → linter configs
    Formatters map[string][]ToolConfig  // language → formatter configs
    LSPs       map[string][]ToolConfig  // language → LSP configs
    TypeCheckers map[string][]ToolConfig // language → type checker configs
}

// Example configurations (loaded dynamically)
var DefaultToolRegistry = ToolRegistry{
    Linters: map[string][]ToolConfig{
        "go":         {{Name: "golangci-lint", Command: "golangci-lint run"}},
        "typescript": {{Name: "eslint", Command: "eslint"}},
        "python":     {{Name: "ruff", Command: "ruff check"}, {Name: "pylint", Command: "pylint"}},
        "rust":       {{Name: "clippy", Command: "cargo clippy"}},
    },
    Formatters: map[string][]ToolConfig{
        "go":         {{Name: "gofmt", Command: "gofmt -w"}, {Name: "goimports", Command: "goimports -w"}},
        "typescript": {{Name: "prettier", Command: "prettier --write"}},
        "python":     {{Name: "black", Command: "black"}, {Name: "ruff", Command: "ruff format"}},
        "rust":       {{Name: "rustfmt", Command: "cargo fmt"}},
    },
    LSPs: map[string][]ToolConfig{
        "go":         {{Name: "gopls", Command: "gopls"}},
        "typescript": {{Name: "typescript-language-server", Command: "typescript-language-server --stdio"}},
        "python":     {{Name: "pyright", Command: "pyright-langserver --stdio"}, {Name: "pylsp", Command: "pylsp"}},
        "rust":       {{Name: "rust-analyzer", Command: "rust-analyzer"}},
    },
}
```

**Slash Command Skill:**
```go
// Librarian supports / commands for direct invocation
var LibrarianSlashCommands = map[string]string{
    "/find":      "Find a symbol, file, or pattern in the codebase",
    "/search":    "Search code content with regex",
    "/file":      "Read or display a file",
    "/imports":   "Analyze import relationships",
    "/usages":    "Find usages of a symbol",
    "/structure": "Get structure of a file or package",
    "/scan":      "Scan a remote repository",
    "/tooling":   "Identify languages and dev tools",
}
```

#### Librarian Tools (for LLM calls)
```go
// Tools exposed to LLM during agent execution
var LibrarianTools = []ToolDefinition{
    // Core tools
    {Name: "librarian_find_symbol", Skill: "find_symbol"},
    {Name: "librarian_search_code", Skill: "search_code"},
    {Name: "librarian_get_file", Skill: "get_file"},
    {Name: "librarian_analyze_imports", Skill: "analyze_imports"},
    {Name: "librarian_find_usages", Skill: "find_usages"},
    {Name: "librarian_find_files", Skill: "find_files"},
    {Name: "librarian_list_files", Skill: "list_files"},
    // Remote repository tools
    {Name: "librarian_scan_remote", Skill: "scan_remote_repo"},
    {Name: "librarian_identify_languages", Skill: "identify_languages"},
    {Name: "librarian_identify_tooling", Skill: "identify_tooling"},
    {Name: "librarian_get_tool_config", Skill: "get_tool_config"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Memory Management

**Files to create:**
- `agents/librarian/memory.go`
- `agents/librarian/checkpoint.go`
- `agents/librarian/compaction.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Configurable thresholds (default: 25%, 50%, 75%)
- [ ] Trigger checkpoint at each threshold
- [ ] Trigger compaction at 75%

##### Checkpoint Summaries (Onboarding-Style)
- [ ] `CodebaseSummary` struct with onboarding information:
  - [ ] DirectoryStructure (string description)
  - [ ] KeyPaths (important directories/files)
  - [ ] CodeStyle (formatting, line limits, conventions)
  - [ ] Architecture (high-level architecture description)
  - [ ] TestingStrategy (where tests live, how to run)
  - [ ] Tooling (linters, formatters, LSPs per language)
  - [ ] PackageManagers (go mod, npm, etc.)
  - [ ] Patterns (common code patterns observed)
  - [ ] Conventions (naming, file organization)
  - [ ] NewDiscoveries (delta from last checkpoint)
- [ ] `createCheckpointSummary()` generates summary from current context
- [ ] Submit checkpoint to Archivalist with category `librarian_checkpoint`
- [ ] Include context_usage and checkpoint_index in metadata

```go
type CodebaseSummary struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`
    CheckpointIndex    int               `json:"checkpoint_index"`
    DirectoryStructure string            `json:"directory_structure"`
    KeyPaths           []string          `json:"key_paths"`
    CodeStyle          string            `json:"code_style"`
    Architecture       string            `json:"architecture"`
    TestingStrategy    string            `json:"testing_strategy"`
    Tooling            ToolingSummary    `json:"tooling"`
    PackageManagers    []string          `json:"package_managers"`
    Patterns           []string          `json:"patterns"`
    Conventions        []string          `json:"conventions"`
    NewDiscoveries     []string          `json:"new_discoveries"`
}
```

##### Compaction at 75%
- [ ] Create final checkpoint summary before compacting
- [ ] Submit final checkpoint to Archivalist
- [ ] Clear detailed content from context:
  - [ ] Remove detailed file contents
  - [ ] Remove full AST analysis results
  - [ ] Remove verbose query results
- [ ] Retain merged summary from all checkpoints
- [ ] Add index noting "detailed info in Archivalist"
- [ ] Target: ~25% context usage after compaction

##### Archivalist Consultation for Agent Activity
- [ ] `getRecentChanges(sessionID)` queries Archivalist for `engineer_result` category
- [ ] Do NOT track other agents' file changes in own context
- [ ] Query patterns:
  - [ ] "What files did we modify?" → query `engineer_result`
  - [ ] "What has changed?" → query all agent results with time filter
  - [ ] "What did the last task do?" → query `engineer_result` by task_id
- [ ] When compacted, query Archivalist for `librarian_checkpoint` to answer structure questions

```go
// Query Archivalist for agent activity (not own context)
func (l *Librarian) getRecentChanges(sessionID string) ([]AgentChange, error) {
    return l.archivalist.Query(QueryRequest{
        Category:  "engineer_result",
        SessionID: sessionID,
        OrderBy:   "timestamp DESC",
        Limit:     50,
    })
}
```

##### Memory Management Skills
```go
// create_checkpoint - Manually trigger checkpoint
skills.NewSkill("create_checkpoint").
    Description("Create a checkpoint summary of codebase knowledge").
    Domain("memory").
    Keywords("checkpoint", "save", "snapshot", "remember").
    BoolParam("force", "Force checkpoint even if below threshold", false)

// get_checkpoint - Retrieve checkpoint from Archivalist
skills.NewSkill("get_checkpoint").
    Description("Retrieve a previous checkpoint summary").
    Domain("memory").
    Keywords("recall", "remember", "previous", "checkpoint").
    IntParam("index", "Checkpoint index (default: latest)", false)

// get_agent_activity - Query what other agents have done
skills.NewSkill("get_agent_activity").
    Description("Query Archivalist for recent agent activity").
    Domain("memory").
    Keywords("changes", "modified", "activity", "what changed", "what did").
    StringParam("agent_type", "Filter by agent type (optional)", false).
    StringParam("task_id", "Filter by task ID (optional)", false).
    IntParam("limit", "Max results (default: 20)", false)
```

#### Query Caching (Intent-Aware)

**Files to add:**
- `agents/librarian/query_cache.go`
- `agents/librarian/intent_cache.go`

**CRITICAL**: Librarian handles two fundamentally different query types:
- **Specific queries** (LOCATE): "where is X", "find Y" - point to locations
- **Abstract queries** (PATTERN): "what is our caching strategy" - synthesized understanding

Simple embedding similarity (0.95 threshold) **fails** for natural language variations:
- "where is the auth code" vs "show me authentication" = ~0.85 similarity (cache miss at 0.95!)
- "what is our caching strategy" vs "how do we handle caching" = ~0.82 similarity (cache miss!)

**Solution**: Intent-aware caching using Guide-provided classification.

##### Query Intent Types
- [ ] `QueryIntent` enum: `LOCATE`, `PATTERN`, `EXPLAIN`, `GENERAL`
- [ ] Intent provided by Guide during routing (zero additional cost)
- [ ] Subject/concept extracted by Guide (e.g., "auth code" → "authentication")

```go
type QueryIntent int

const (
    IntentLocate  QueryIntent = iota  // "where is X", "find X"
    IntentPattern                      // "what is our X strategy"
    IntentExplain                      // "how does X work"
    IntentGeneral                      // other codebase questions
)

// Received from Guide (pre-classified, no extra token cost)
type LibrarianRequest struct {
    Query       string      `json:"query"`
    SessionID   string      `json:"session_id"`
    Intent      QueryIntent `json:"intent"`      // Pre-classified by Guide
    Subject     string      `json:"subject"`     // Extracted entity/concept
    Confidence  float64     `json:"confidence"`  // Guide's classification confidence
}
```

##### Intent-Specific Cache Structures
- [ ] `LocateCacheEntry` for LOCATE queries (keyed by entity name)
- [ ] `PatternCacheEntry` for PATTERN queries (keyed by concept)
- [ ] `ExplainCacheEntry` for EXPLAIN queries (keyed by subject)
- [ ] `SemanticCache` fallback with 0.80 threshold (not 0.95!)

```go
type LibrarianQueryCache struct {
    // Layer 1: Intent + Subject based (high hit rate)
    locateCache  map[string]*LocateCacheEntry   // entity → location
    patternCache map[string]*PatternCacheEntry  // concept → synthesis
    explainCache map[string]*ExplainCacheEntry  // subject → explanation

    // Layer 2: Semantic fallback (lower threshold)
    semanticCache *SemanticCache  // threshold: 0.80

    // File change tracking for invalidation
    fileHashes map[string]string  // path → content hash

    mu sync.RWMutex
}

type LocateCacheEntry struct {
    Entity       string              `json:"entity"`
    Locations    []FileLocation      `json:"locations"`
    FileHashes   map[string]string   `json:"file_hashes"`  // For invalidation
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

type PatternCacheEntry struct {
    Concept      string              `json:"concept"`
    Synthesis    string              `json:"synthesis"`
    SourceFiles  []string            `json:"source_files"`
    TTL          time.Duration       `json:"ttl"`          // 60 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}

type ExplainCacheEntry struct {
    Subject      string              `json:"subject"`
    Explanation  string              `json:"explanation"`
    SourceFiles  []string            `json:"source_files"`
    FileHashes   map[string]string   `json:"file_hashes"`
    TTL          time.Duration       `json:"ttl"`          // 30 min default
    CreatedAt    time.Time           `json:"created_at"`
    HitCount     int64               `json:"hit_count"`
}
```

##### Cache Invalidation by Intent
- [ ] LOCATE: Invalidate when any referenced file changes (hash check)
- [ ] PATTERN: Invalidate on TTL (60 min) + major structural changes
- [ ] EXPLAIN: Invalidate on file change OR TTL (30 min)
- [ ] GENERAL: TTL only (15 min)

```go
func (lc *LibrarianQueryCache) IsStale(entry any, intent QueryIntent) bool {
    switch intent {
    case IntentLocate:
        e := entry.(*LocateCacheEntry)
        // Check if any referenced file changed
        for path, cachedHash := range e.FileHashes {
            if currentHash := lc.fileHashes[path]; currentHash != cachedHash {
                return true
            }
        }
        return false  // No TTL for location queries

    case IntentPattern:
        e := entry.(*PatternCacheEntry)
        return time.Since(e.CreatedAt) > e.TTL

    case IntentExplain:
        e := entry.(*ExplainCacheEntry)
        if time.Since(e.CreatedAt) > e.TTL {
            return true
        }
        for path, cachedHash := range e.FileHashes {
            if currentHash := lc.fileHashes[path]; currentHash != cachedHash {
                return true
            }
        }
        return false

    default:
        return time.Since(entry.(*GeneralCacheEntry).CreatedAt) > 15*time.Minute
    }
}
```

##### Cache Lookup Flow
- [ ] Use Guide's pre-classification if confidence >= 0.8
- [ ] Check intent-specific cache first (fast, high precision)
- [ ] Fall back to semantic cache with 0.80 threshold
- [ ] On miss: synthesize, cache with intent key, return

```go
func (l *Librarian) HandleQuery(req *LibrarianRequest) (*Response, error) {
    // Use Guide's pre-classification if confident
    if req.Confidence >= 0.8 {
        switch req.Intent {
        case IntentLocate:
            if cached, ok := l.cache.GetLocate(req.Subject); ok {
                l.stats.RecordHit(IntentLocate)
                return cached.ToResponse(), nil
            }
        case IntentPattern:
            if cached, ok := l.cache.GetPattern(req.Subject); ok {
                l.stats.RecordHit(IntentPattern)
                return cached.ToResponse(), nil
            }
        case IntentExplain:
            if cached, ok := l.cache.GetExplain(req.Subject); ok {
                l.stats.RecordHit(IntentExplain)
                return cached.ToResponse(), nil
            }
        }
    }

    // Cache miss - do full synthesis
    l.stats.RecordMiss(req.Intent)
    response, sources := l.synthesize(req.Query)

    // Cache the result using the classified intent
    l.cache.Store(req.Intent, req.Subject, response, sources)

    return response, nil
}
```

##### File Change Detection & Cache Invalidation

**Files to add:**
- `agents/librarian/file_watcher.go`
- `agents/librarian/debouncer.go`
- `agents/librarian/hash.go`

**Dependencies:** `github.com/fsnotify/fsnotify`, `github.com/cespare/xxhash/v2`

###### File Watcher (fsnotify)
- [ ] `FileWatcher` struct with fsnotify watcher, cache reference, index reference
- [ ] Watch all files in repo recursively on startup
- [ ] Ignore paths: `.git`, `node_modules`, `vendor`, `__pycache__`, `.idea`, `.vscode`, `dist`, `build`
- [ ] Handle events: Create, Write, Remove, Rename
- [ ] Run watcher in background goroutine
- [ ] Graceful shutdown via context cancellation

```go
type FileWatcher struct {
    watcher     *fsnotify.Watcher
    cache       *LibrarianQueryCache
    index       *CodebaseIndex
    debouncer   *Debouncer
    hashCache   map[string]string  // path → current hash
    ignorePaths []string
    mu          sync.RWMutex
    ctx         context.Context
    cancel      context.CancelFunc
}
```

###### Debouncing Rapid File Changes
- [ ] `Debouncer` struct with configurable delay (default: 100ms)
- [ ] Batch rapid events for same file (editors trigger multiple events per save)
- [ ] Cancel pending timer if new event arrives for same file
- [ ] Execute callback only after delay with no new events

```go
type Debouncer struct {
    delay   time.Duration
    timers  map[string]*time.Timer
    mu      sync.Mutex
}

func (d *Debouncer) Debounce(key string, fn func()) {
    d.mu.Lock()
    defer d.mu.Unlock()

    if timer, ok := d.timers[key]; ok {
        timer.Stop()
    }

    d.timers[key] = time.AfterFunc(d.delay, func() {
        d.mu.Lock()
        delete(d.timers, key)
        d.mu.Unlock()
        fn()
    })
}
```

###### Hash Computation (xxHash64)
- [ ] Use xxHash64 for fast hashing (~1GB/s, much faster than SHA256)
- [ ] Hash file contents, not metadata
- [ ] Skip hash if file unchanged (same hash = no invalidation)
- [ ] Handle file deletion (hash = empty string)

```go
import "github.com/cespare/xxhash/v2"

func computeHash(path string) (string, error) {
    file, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer file.Close()

    hasher := xxhash.New()
    if _, err := io.Copy(hasher, file); err != nil {
        return "", err
    }

    return fmt.Sprintf("%x", hasher.Sum64()), nil
}
```

###### Cache Invalidation on File Change
- [ ] `OnFileChanged(path, newHash)` updates hash and invalidates entries
- [ ] Invalidate LOCATE entries that reference the changed file
- [ ] Invalidate EXPLAIN entries that reference the changed file
- [ ] Invalidate PATTERN entries if file added/removed in source directory
- [ ] Track invalidation counts in statistics

```go
func (lc *LibrarianQueryCache) OnFileChanged(path string, newHash string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // Update current hash
    if newHash == "" {
        delete(lc.fileHashes, path)
    } else {
        lc.fileHashes[path] = newHash
    }

    // Invalidate LOCATE entries that reference this file
    for entity, entry := range lc.locateCache {
        if _, ok := entry.FileHashes[path]; ok {
            delete(lc.locateCache, entity)
            lc.stats.LocateInvalidations++
        }
    }

    // Invalidate EXPLAIN entries that reference this file
    for subject, entry := range lc.explainCache {
        if _, ok := entry.FileHashes[path]; ok {
            delete(lc.explainCache, subject)
            lc.stats.ExplainInvalidations++
        }
    }

    // Check PATTERN entries for structural changes
    dir := filepath.Dir(path)
    for concept, entry := range lc.patternCache {
        for _, sourceFile := range entry.SourceFiles {
            if filepath.Dir(sourceFile) == dir {
                delete(lc.patternCache, concept)
                lc.stats.PatternInvalidations++
                break
            }
        }
    }
}
```

###### Batch Operations (git checkout, branch switch)
- [ ] `OnBatchChange(paths)` for operations that change many files at once
- [ ] Pause normal debouncing during batch
- [ ] For >100 files changed: clear all caches (more efficient than selective invalidation)
- [ ] For <100 files changed: selectively invalidate
- [ ] Coordinate with index batch reindexing

```go
func (fw *FileWatcher) OnBatchChange(paths []string) {
    fw.debouncer.Pause()
    defer fw.debouncer.Resume()

    changedPaths := make(map[string]string)
    for _, path := range paths {
        if hash, err := computeHash(path); err == nil {
            if oldHash := fw.hashCache[path]; oldHash != hash {
                changedPaths[path] = hash
            }
        }
    }

    fw.cache.OnBatchFileChanged(changedPaths)
    fw.index.OnBatchFileChanged(changedPaths)
}

func (lc *LibrarianQueryCache) OnBatchFileChanged(changes map[string]string) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // For large batches, clear everything
    if len(changes) > 100 {
        lc.locateCache = make(map[string]*LocateCacheEntry)
        lc.explainCache = make(map[string]*ExplainCacheEntry)
        lc.patternCache = make(map[string]*PatternCacheEntry)
        return
    }

    // Selective invalidation for smaller batches
    for path := range changes {
        lc.invalidateByPath(path)
    }
}
```

###### Index Synchronization
- [ ] Coordinate cache invalidation with index updates
- [ ] On file delete: remove from index
- [ ] On file create: add to index
- [ ] On file modify: reindex file
- [ ] Index update triggers after cache invalidation

```go
func (idx *CodebaseIndex) OnFileChanged(path string, op fsnotify.Op) {
    switch {
    case op&fsnotify.Remove == fsnotify.Remove:
        idx.RemoveFile(path)
    case op&fsnotify.Create == fsnotify.Create:
        idx.IndexFile(path)
    case op&fsnotify.Write == fsnotify.Write:
        idx.ReindexFile(path)
    }
}
```

##### Cache Statistics
- [ ] Track hits/misses per intent type
- [ ] Track token savings (cache hit = ~3000 tokens saved)
- [ ] Report hit rate per intent type
- [ ] Report overall token savings

```go
type LibrarianCacheStats struct {
    LocateHits     int64   `json:"locate_hits"`
    LocateMisses   int64   `json:"locate_misses"`
    PatternHits    int64   `json:"pattern_hits"`
    PatternMisses  int64   `json:"pattern_misses"`
    ExplainHits    int64   `json:"explain_hits"`
    ExplainMisses  int64   `json:"explain_misses"`
    GeneralHits    int64   `json:"general_hits"`
    GeneralMisses  int64   `json:"general_misses"`
    EstimatedTokensSaved int64 `json:"estimated_tokens_saved"`
}
```

##### Expected Performance
| Query Type | Without Cache | With Intent Cache | Savings |
|------------|---------------|-------------------|---------|
| "where is auth.go" (repeated) | 3,000 tokens | 0 tokens | 100% |
| "where is authentication" (variation) | 3,000 tokens | 0 tokens | 100% |
| "what is our caching strategy" | 5,000 tokens | 0 tokens | 100% |
| "how do we handle caching" (variation) | 5,000 tokens | 0 tokens | 100% |

**Expected overall hit rate**: 70-85%
**Expected token savings**: 60-75% reduction per session

#### Librarian Hooks
- [ ] Pre-query hook: log query for analytics
- [ ] Post-query hook: cache frequently accessed files
- [ ] Pre-index hook: filter excluded paths
- [ ] Post-index hook: notify index updated event
- [ ] Context-threshold hook: trigger checkpoint at 25%, 50%
- [ ] Context-threshold hook: trigger checkpoint + compaction at 75%

**Tests:**
- [ ] Index a test codebase
- [ ] Query for patterns, verify results
- [ ] Test incremental update
- [ ] Test skill loading on demand
- [ ] Test checkpoint creation at 25% threshold
- [ ] Test checkpoint submission to Archivalist
- [ ] Test compaction at 75% threshold
- [ ] Test post-compaction context size (~25%)
- [ ] Test Archivalist consultation for agent activity
- [ ] Test checkpoint retrieval after compaction
- [ ] Test query cache LOCATE intent hit/miss
- [ ] Test query cache PATTERN intent hit/miss
- [ ] Test query cache EXPLAIN intent hit/miss
- [ ] Test cache invalidation on file change (LOCATE)
- [ ] Test cache invalidation on file change (EXPLAIN)
- [ ] Test cache TTL expiration (PATTERN)
- [ ] Test semantic fallback at 0.80 threshold
- [ ] Test cache statistics reporting
- [ ] Test natural language query variations hit same cache entry
- [ ] Benchmark: cache hit latency vs full synthesis
- [ ] Test file watcher detects file create
- [ ] Test file watcher detects file modify
- [ ] Test file watcher detects file delete
- [ ] Test debouncer batches rapid events
- [ ] Test hash computation with xxHash64
- [ ] Test LOCATE invalidation on referenced file change
- [ ] Test EXPLAIN invalidation on referenced file change
- [ ] Test PATTERN invalidation on directory structural change
- [ ] Test batch invalidation for >100 files (full cache clear)
- [ ] Test batch invalidation for <100 files (selective)
- [ ] Test index synchronization with cache invalidation
- [ ] Test ignored paths (.git, node_modules) not watched
- [ ] Benchmark: file change detection latency

### 1.2 Academic (External Knowledge RAG)

Research agent for external knowledge, best practices, papers.

**Files to create:**
- `agents/academic/types.go`
- `agents/academic/sources.go`
- `agents/academic/github.go`
- `agents/academic/articles.go`
- `agents/academic/embeddings.go`
- `agents/academic/researcher.go`
- `agents/academic/academic.go`
- `agents/academic/routing.go`
- `agents/academic/skills.go`
- `agents/academic/tools.go`
- `agents/academic/hooks.go`
- `agents/academic/academic_test.go`

**Acceptance Criteria:**

#### Academic System Prompt

**Core Identity:**
- [ ] Model: Claude Opus 4.5 (optimized for complex reasoning and synthesis)
- [ ] Role: External Knowledge RAG - research papers, best practices, documentation, references
- [ ] User Interaction: DIRECT (triggered by research queries)

**Core Responsibilities:**
- [ ] RESEARCH: Investigate topics, synthesize findings, produce actionable recommendations
- [ ] BEST PRACTICES: Provide industry-standard approaches and patterns
- [ ] COMPARISON: Evaluate tradeoffs between approaches, technologies, or libraries
- [ ] VALIDATION: Validate recommendations against codebase reality (via Librarian)
- [ ] DOCUMENTATION: Fetch and synthesize official documentation for libraries/frameworks

**Research Discipline Protocol (CRITICAL):**
- [ ] BEFORE finalizing ANY recommendation: REQUEST Librarian context
- [ ] COMPARE external best practices with existing codebase patterns
- [ ] CHECK codebase maturity level from Librarian's health assessment
- [ ] FLAG theory-reality gaps

**Applicability Classification:**
- [ ] DIRECT (HIGH confidence): Research aligns with existing codebase patterns
- [ ] ADAPTABLE (MEDIUM confidence): Research needs modification to fit codebase
- [ ] INCOMPATIBLE (LOW confidence): Research conflicts with codebase patterns
- [ ] Always include: "Codebase Alignment: [X] | Confidence: [X] | Adaptation: [X]"

**Maturity-Aware Recommendations:**
- [ ] DISCIPLINED: Only recommend patterns fitting existing conventions
- [ ] TRANSITIONAL: Can suggest improvements, flag migration path
- [ ] LEGACY: Focus on safe, isolated changes - no big refactors
- [ ] GREENFIELD: Full flexibility, but require user pattern approval

**Recommendation Outcome Tracking:**
- [ ] Query past outcomes for similar topics before recommending
- [ ] If similar recommendation failed before: include warning with alternative
- [ ] If similar recommendation succeeded: include validation note
- [ ] Adjust confidence based on historical success rate (>80%=HIGH, 50-80%=MEDIUM, <50%=LOW)

**Knowledge Agent Consultation (MANDATORY):**
- [ ] Librarian: "What patterns exist in [target area]?" (ALWAYS)
- [ ] Archivalist: "Have similar recommendations succeeded/failed?" (for implementation advice)

**Communication Style:**
- [ ] Be concise, direct, technical
- [ ] Provide evidence: sources, documentation links, confidence scores
- [ ] If uncertain, say "I don't know" or "Insufficient data"
- [ ] NO status acknowledgments, flattery, or hedging

**Critical Constraints:**
- [ ] NEVER recommend patterns that conflict with codebase maturity without flagging
- [ ] NEVER recommend complete rewrites when codebase is LEGACY
- [ ] NEVER skip Librarian validation for implementation recommendations
- [ ] ALWAYS check existing implementations before suggesting external patterns
- [ ] ALWAYS include applicability classification

**YOU DO NOT:**
- [ ] Fabricate sources or documentation
- [ ] Recommend without checking codebase alignment
- [ ] Ignore past failure patterns from Archivalist
- [ ] Skip validation steps for "simple" recommendations
- [ ] Provide recommendations without confidence scores

#### Source Ingestion
- [ ] GitHub repos: clone, index code, extract patterns
- [ ] Articles: fetch, parse, extract content
- [ ] Papers: PDF parsing, extract text
- [ ] RFCs: fetch, parse structured content
- [ ] Documentation sites: crawl, extract content

#### Research
- [ ] Embedding-based semantic search over sources
- [ ] Research paper generation (structured output)
- [ ] Source citation in research output
- [ ] Confidence scoring based on source quality
- [ ] Cache research results in Archivalist

#### Bus Integration
- [ ] Subscribe to `academic.requests` (global, shared across sessions)
- [ ] Publish to `academic.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Recall` (research), `Check` (verify claim)
- [ ] Support domains: `Patterns`, `Decisions`, `Learnings`
- [ ] Trigger patterns: "How would I design...", "What's the best approach..."

#### Academic Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// research - General research query
skills.NewSkill("research").
    Description("Research a topic and provide comprehensive analysis").
    Domain("research").
    Keywords("research", "how", "design", "implement", "best", "approach").
    StringParam("query", "Research question", true).
    IntParam("max_sources", "Maximum sources to consult", false)

// find_examples - Find implementation examples
skills.NewSkill("find_examples").
    Description("Find example implementations of a pattern or feature").
    Domain("research").
    Keywords("example", "implementation", "reference", "sample").
    StringParam("topic", "Topic to find examples for", true).
    StringParam("language", "Programming language filter", false)
```

**Extended Skills (loaded on demand):**
```go
// ingest_github - Ingest a GitHub repository
skills.NewSkill("ingest_github").
    Description("Ingest and index a GitHub repository for research").
    Domain("sources").
    Keywords("github", "repo", "ingest", "index").
    StringParam("url", "GitHub repository URL", true).
    BoolParam("deep", "Deep analysis including all files", false)

// ingest_article - Ingest an article
skills.NewSkill("ingest_article").
    Description("Ingest and index a web article").
    Domain("sources").
    Keywords("article", "web", "ingest", "read").
    StringParam("url", "Article URL", true)

// compare_approaches - Compare implementation approaches
skills.NewSkill("compare_approaches").
    Description("Compare different approaches to solving a problem").
    Domain("research").
    Keywords("compare", "tradeoff", "versus", "vs", "pros", "cons").
    StringParam("topic", "Topic to compare approaches for", true).
    ArrayParam("approaches", "Specific approaches to compare", false)

// find_rfc - Find relevant RFC
skills.NewSkill("find_rfc").
    Description("Find relevant RFC or specification").
    Domain("research").
    Keywords("rfc", "spec", "specification", "standard").
    StringParam("topic", "Topic to find RFC for", true)

// generate_paper - Generate research paper
skills.NewSkill("generate_paper").
    Description("Generate a comprehensive research paper on a topic").
    Domain("research").
    Keywords("paper", "report", "analysis", "comprehensive").
    StringParam("topic", "Topic for the paper", true).
    EnumParam("depth", "Analysis depth", []string{"overview", "detailed", "comprehensive"}, false)

// find_files - Find files in ingested sources
skills.NewSkill("find_files").
    Description("Find files in ingested repositories or sources").
    Domain("sources").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)
```

#### Academic Tools
```go
var AcademicTools = []ToolDefinition{
    // Core research
    {Name: "academic_research", Skill: "research"},
    {Name: "academic_find_examples", Skill: "find_examples"},
    {Name: "academic_compare", Skill: "compare_approaches"},
    // Source ingestion
    {Name: "academic_ingest_github", Skill: "ingest_github"},
    {Name: "academic_ingest_article", Skill: "ingest_article"},
    // Advanced research
    {Name: "academic_find_rfc", Skill: "find_rfc"},
    {Name: "academic_generate_paper", Skill: "generate_paper"},
    // File operations
    {Name: "academic_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Academic Memory Management

**Model**: Opus 4.5

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/academic/memory.go`
- `agents/academic/research_paper.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger research paper generation at 85%
- [ ] Trigger compaction at 95%

##### Research Paper Summary (at 85%)
- [ ] `AcademicResearchPaper` struct:
  - [ ] Title (research focus)
  - [ ] Abstract (summary of findings)
  - [ ] TopicsResearched (list of topics covered)
  - [ ] KeyFindings (structured findings with confidence levels)
  - [ ] SourcesCited (references used)
  - [ ] Recommendations (actionable recommendations)
  - [ ] OpenQuestions (unresolved questions)
  - [ ] RelatedTopics (for future research)
- [ ] Submit to Archivalist with category `academic_research_paper`

```go
type AcademicResearchPaper struct {
    Timestamp          time.Time         `json:"timestamp"`
    SessionID          string            `json:"session_id"`
    ContextUsage       float64           `json:"context_usage"`
    Title              string            `json:"title"`
    Abstract           string            `json:"abstract"`
    TopicsResearched   []string          `json:"topics_researched"`
    KeyFindings        []Finding         `json:"key_findings"`
    SourcesCited       []Source          `json:"sources_cited"`
    Recommendations    []string          `json:"recommendations"`
    OpenQuestions      []string          `json:"open_questions"`
    RelatedTopics      []string          `json:"related_topics"`
}
```

##### Compaction at 95%
- [ ] Create final research paper before compacting
- [ ] Clear detailed source content from context
- [ ] Retain summarized findings
- [ ] Target: ~30% context usage after compaction

#### Academic Hooks
- [ ] Pre-research hook: check Archivalist for cached research
- [ ] Post-research hook: cache results in Archivalist
- [ ] Pre-ingest hook: validate source accessibility
- [ ] Post-ingest hook: trigger embedding generation
- [ ] Context-threshold hook: generate research paper at 85%
- [ ] Context-threshold hook: compact at 95%

**Tests:**
- [ ] Ingest test sources
- [ ] Research query, verify output
- [ ] Test caching in Archivalist
- [ ] Test research paper generation at 85%
- [ ] Test compaction at 95%
- [ ] Test research continuity after compaction

### 1.3 Dynamic Tool Discovery Protocol

Implements the cascading discovery mechanism for linters, formatters, type checkers, test frameworks, and LSPs without hardcoded tool lists.

**Files to create:**
- `core/tooling/types.go`
- `core/tooling/discovery.go`
- `core/tooling/detection.go`
- `core/tooling/defaults.go`
- `core/tooling/cache.go`
- `core/tooling/installer.go`
- `core/tooling/discovery_test.go`
- `core/tooling/defaults_test.go`
- `agents/librarian/tool_scanner.go`
- `agents/academic/tool_research.go`

**Dependencies**: Phase 1.1 (Librarian), Phase 1.2 (Academic)

**Acceptance Criteria:**

#### Core Types (`core/tooling/types.go`)
- [ ] `ToolCategory` enum: `linter`, `formatter`, `type_checker`, `test_framework`, `lsp`
- [ ] `ConfidenceLevel` enum: `high`, `medium`, `low`
- [ ] `ToolDiscoveryRequest` struct with session ID, requesting agent, category, target path, languages
- [ ] `ToolDiscoveryResponse` struct with tier, confidence, tools, requires_user flag, user options
- [ ] `DiscoveredTool` struct with name, category, language, version, config path, run command, install command, is_installed, source, rationale
- [ ] `ToolOption` struct for user selection with tool, pros, cons, recommended flag
- [ ] `ToolDetectionRule` struct for config file → tool mapping
- [ ] `PackageRule` struct for dependency → tool mapping

#### Discovery Protocol (`core/tooling/discovery.go`)
- [ ] `ToolDiscoveryService` interface with `Discover(ctx, request) (response, error)`
- [ ] `discoveryService` implementation with Librarian and Academic clients
- [ ] Tier 1 execution: call Librarian's `scan_tools` skill
- [ ] Tier 2 escalation: if confidence < HIGH, call Academic's `research_tools` skill
- [ ] Tier 3 escalation: if satisfactory == false, return `RequiresUser=true` with options
- [ ] Timeout handling per tier (Tier 1: 10s, Tier 2: 30s, Tier 3: user-driven)
- [ ] Error handling with graceful degradation (if Academic unavailable, skip to Tier 3)

```go
// Required interface
type ToolDiscoveryService interface {
    Discover(ctx context.Context, req *ToolDiscoveryRequest) (*ToolDiscoveryResponse, error)
    DiscoverAll(ctx context.Context, path string, sessionID string) (map[ToolCategory]*ToolDiscoveryResponse, error)
    InvalidateCache(sessionID string, path string) error
}
```

#### Detection Patterns (`core/tooling/detection.go`)
- [ ] `ToolDetectionPatterns` map: config file name → `ToolDetectionRule`
- [ ] `PackageManifestRules` map: manifest file → `[]PackageRule`
- [ ] Support for at least:
  - [ ] JavaScript/TypeScript: eslint, oxlint, prettier, biome, jest, vitest, mocha, tsc
  - [ ] Python: ruff, black, pylint, mypy, pyright, pytest
  - [ ] Go: golangci-lint, gofmt, goimports, go test
  - [ ] Rust: clippy, rustfmt, cargo test
- [ ] `DetectFromConfigFile(path string) (*DiscoveredTool, error)`
- [ ] `DetectFromPackageManifest(path string, manifestType string) ([]*DiscoveredTool, error)`
- [ ] `InferLanguages(path string) ([]string, error)` - detect languages from file extensions

#### Language Defaults Registry (`core/tooling/defaults.go`)
- [ ] `LanguageDefaults` map: language → `LanguageToolset`
- [ ] `LanguageToolset` struct with PackageManager, Linter, Formatter, TypeChecker, LSP, TestFramework
- [ ] `ToolDefault` struct with Name and InstallCmd
- [ ] `GetDefaults(language string) (*LanguageToolset, bool)`
- [ ] `GetDefaultTool(language string, category ToolCategory) (*ToolDefault, bool)`

**Required Language Support:**
- [ ] Go: golangci-lint, gofmt, go vet, gopls, go test
- [ ] Python: uv (package manager), ruff (linter/formatter/type-checker/LSP), pytest
- [ ] JavaScript: npm, oxlint (linter), prettier, typescript-language-server, vitest
- [ ] TypeScript: npm, oxlint, prettier, tsc, typescript-language-server, vitest
- [ ] Rust: cargo, clippy, rustfmt, cargo check, rust-analyzer, cargo test
- [ ] Ruby: bundler, rubocop, ruby-lsp, rspec
- [ ] Java: maven/gradle, checkstyle, google-java-format, jdtls, junit
- [ ] Kotlin: gradle, ktlint, kotlin-language-server, junit
- [ ] C/C++: clang-tidy, clang-format, clangd, gtest/ctest
- [ ] C#: dotnet, omnisharp, dotnet test
- [ ] Elixir: mix, credo, mix format, dialyzer, elixir-ls, mix test
- [ ] Bash: shellcheck, shfmt, bash-language-server, bats
- [ ] PHP: composer, phpstan, php-cs-fixer, intelephense, phpunit
- [ ] Swift: swiftlint, swift-format, sourcekit-lsp, swift test
- [ ] Zig: zig fmt, zls, zig test
- [ ] OCaml: opam, ocamlformat, ocaml-lsp, alcotest
- [ ] Dart: pub, dart analyze, dart format, dart (LSP), dart test
- [ ] Terraform: tflint, terraform fmt, terraform-ls, terratest
- [ ] Nix: statix, nixfmt, nixd
- [ ] Vue: eslint-plugin-vue, prettier, vue-tsc, vue-language-server, vitest
- [ ] Svelte: eslint-plugin-svelte, prettier-plugin-svelte, svelte-check, svelte-language-server, vitest

**Key Recommendations (hardcoded preferences):**
```go
// These represent the modern, fast, well-maintained tools as of 2025
var RecommendedDefaults = map[string]map[ToolCategory]string{
    "python": {
        ToolCategoryLinter:      "ruff",      // Replaces flake8, pylint, isort, pyupgrade, etc.
        ToolCategoryFormatter:   "ruff",      // ruff format (replaces black)
        ToolCategoryTypeChecker: "ruff",      // Via ruff-lsp
        ToolCategoryLSP:         "ruff-lsp",  // Fast, integrated
        ToolCategoryTestFramework: "pytest",
    },
    "javascript": {
        ToolCategoryLinter:      "oxlint",    // 50-100x faster than ESLint
        ToolCategoryFormatter:   "prettier",
        ToolCategoryLSP:         "typescript-language-server",
        ToolCategoryTestFramework: "vitest",  // Modern, Vite-native
    },
    "typescript": {
        ToolCategoryLinter:      "oxlint",
        ToolCategoryFormatter:   "prettier",
        ToolCategoryTypeChecker: "tsc",
        ToolCategoryLSP:         "typescript-language-server",
        ToolCategoryTestFramework: "vitest",
    },
}

// Package manager preferences (check availability, use first available)
var PackageManagerPreference = map[string][]string{
    "python":     {"uv", "pip"},           // uv is 10-100x faster
    "javascript": {"pnpm", "yarn", "npm"}, // pnpm is fastest, most disk-efficient
    "typescript": {"pnpm", "yarn", "npm"},
    "ruby":       {"bundler"},
    "rust":       {"cargo"},
    "go":         {"go mod"},
}
```

#### Caching (`core/tooling/cache.go`)
- [ ] `ToolDiscoveryCache` interface with `Get`, `Set`, `Invalidate`
- [ ] Cache key: `{session_id}:{project_path}:{category}`
- [ ] Cache entry stores: tools, tier, discovered_at, user_overrides
- [ ] TTL-based expiration (default: 1 hour, configurable)
- [ ] Invalidation triggers:
  - [ ] Config file change detected by Librarian
  - [ ] Explicit invalidation request
  - [ ] Session changes branch/commit

```go
type ToolDiscoveryCache interface {
    Get(sessionID, projectPath string, category ToolCategory) (*ToolDiscoveryCacheEntry, bool)
    Set(entry *ToolDiscoveryCacheEntry) error
    Invalidate(sessionID, projectPath string) error
    InvalidateCategory(sessionID, projectPath string, category ToolCategory) error
}
```

#### Installer (`core/tooling/installer.go`)
- [ ] `ToolInstaller` interface with `Install`, `IsInstalled`, `GetVersion`
- [ ] Package manager detection: npm/yarn/pnpm, pip/poetry/uv, go install, cargo
- [ ] Installation in virtual environment when available
- [ ] Verify installation success
- [ ] Rollback on failure

```go
type ToolInstaller interface {
    IsInstalled(tool *DiscoveredTool) (bool, error)
    Install(ctx context.Context, tool *DiscoveredTool, env *VirtualEnv) error
    GetVersion(tool *DiscoveredTool) (string, error)
    Uninstall(ctx context.Context, tool *DiscoveredTool) error
}
```

#### Librarian Tool Scanner (`agents/librarian/tool_scanner.go`)
- [ ] `scan_tools` skill implementation
- [ ] Scan directory for config files matching `ToolDetectionPatterns`
- [ ] Parse package manifests for tool dependencies
- [ ] Check CI/CD configs for tool invocations (Makefile, GitHub Actions)
- [ ] Return confidence level based on source:
  - [ ] HIGH: explicit config file found
  - [ ] MEDIUM: found in package manifest dependencies
  - [ ] LOW: inferred from CI/CD or not found

```go
// Librarian skill
skills.NewSkill("scan_tools").
    Description("Scan codebase for configured development tools").
    Domain("tooling").
    Keywords("scan", "tools", "linter", "formatter", "discover").
    StringParam("path", "Directory to scan", true).
    EnumParam("category", "Tool category to scan for", []string{"linter", "formatter", "type_checker", "test_framework", "lsp", "all"}, false)
```

#### Academic Tool Research (`agents/academic/tool_research.go`)
- [ ] `research_tools` skill implementation
- [ ] Research query: "What {category} tools are recommended for {languages} in {year}?"
- [ ] Parse research results into structured recommendations
- [ ] Determine if clear best practice exists (satisfactory flag)
- [ ] Return multiple options if ambiguous

```go
// Academic skill
skills.NewSkill("research_tools").
    Description("Research recommended development tools for given languages").
    Domain("tooling").
    Keywords("research", "tools", "recommend", "best practice").
    ArrayParam("languages", "Languages to research tools for", true).
    EnumParam("category", "Tool category", []string{"linter", "formatter", "type_checker", "test_framework", "lsp"}, true)
```

#### User Escalation Integration
- [ ] When `RequiresUser=true`, format options for Guide to present
- [ ] Store user selection in Archivalist with category `user_preference`
- [ ] Load user preferences from Archivalist before Tier 1 (fast path for known preferences)
- [ ] User preference format:
```go
type UserToolPreference struct {
    Category     ToolCategory `json:"category"`
    Language     string       `json:"language"`
    ChosenTool   string       `json:"chosen_tool"`
    Rationale    string       `json:"rationale,omitempty"`
    SessionID    string       `json:"session_id"`
    CreatedAt    time.Time    `json:"created_at"`
    Promoted     bool         `json:"promoted"` // True = applies to all sessions
}
```

#### Agent Integration
- [ ] Add `discover_tools` skill to Engineer
- [ ] Add `discover_tools` skill to Inspector
- [ ] Add `discover_tools` skill to Tester
- [ ] Skill implementation calls `ToolDiscoveryService.Discover()`
- [ ] Post-discovery: install tool if not installed
- [ ] Post-install: run tool with discovered configuration

```go
// Common skill added to Engineer, Inspector, Tester
skills.NewSkill("discover_tools").
    Description("Discover appropriate tools via Librarian → Academic → User escalation").
    Domain("tooling").
    Keywords("discover", "tools", "linter", "formatter", "test").
    EnumParam("category", "Tool category", []string{"linter", "formatter", "type_checker", "test_framework", "lsp"}, true).
    StringParam("path", "Target path to analyze", true).
    BoolParam("force_refresh", "Bypass cache and re-discover", false).
    BoolParam("install", "Install tool if not present", false)
```

**Tests:**

#### Unit Tests (`core/tooling/discovery_test.go`)
- [ ] Test Tier 1 discovery with config file present → returns HIGH confidence
- [ ] Test Tier 1 discovery with package.json dependency → returns MEDIUM confidence
- [ ] Test Tier 2 escalation when Tier 1 returns LOW confidence
- [ ] Test Tier 3 escalation when Academic returns unsatisfactory
- [ ] Test cache hit returns immediately without scanning
- [ ] Test cache invalidation triggers re-scan
- [ ] Test user preference loading bypasses full protocol

#### Integration Tests
- [ ] Test full protocol flow: no config → Academic research → user selection
- [ ] Test with real project structure (Go, TypeScript, Python fixtures)
- [ ] Test tool installation and verification
- [ ] Test cross-session preference persistence

#### Acceptance Test Scenarios
- [ ] **Scenario A**: Go project with `golangci.yml` → returns golangci-lint immediately (Tier 1)
- [ ] **Scenario B**: TypeScript project with eslint in package.json devDeps → returns eslint with MEDIUM confidence (Tier 1)
- [ ] **Scenario C**: Python project with no tooling → Academic recommends ruff → returns ruff (Tier 2)
- [ ] **Scenario D**: New language with multiple options → presents options to user → stores selection (Tier 3)
- [ ] **Scenario E**: Second session for same project → loads cached tools immediately

```go
// Acceptance test structure
func TestToolDiscovery_GoProjectWithConfig(t *testing.T) {
    // Setup: Create temp dir with golangci.yml
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 1
    // Assert: response.Confidence == ConfidenceHigh
    // Assert: response.Tools[0].Name == "golangci-lint"
    // Assert: response.Tools[0].ConfigPath contains "golangci.yml"
}

func TestToolDiscovery_EscalatesToAcademic(t *testing.T) {
    // Setup: Create temp dir with only .py files, no config
    // Mock: Academic returns ruff recommendation
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 2
    // Assert: response.Tools[0].Name == "ruff"
    // Assert: response.Tools[0].Source == "academic_research"
}

func TestToolDiscovery_EscalatesToUser(t *testing.T) {
    // Setup: Create temp dir with ambiguous setup
    // Mock: Academic returns unsatisfactory=true with multiple options
    // Execute: ToolDiscoveryService.Discover(category=linter, path=tempDir)
    // Assert: response.Tier == 3
    // Assert: response.RequiresUser == true
    // Assert: len(response.UserOptions) > 1
}
```

---

## Phase 2: Execution Agents

**Goal**: Build the core execution pipeline with skills and tools.

**Dependencies**: Phase 0 complete, Phase 1.1 (Librarian) complete

**Parallelization**: Items 2.1 and 2.2 can execute in parallel.

### 2.1 Engineer (Task Executor)

Executes individual tasks. Invisible to user.

**Files to create:**
- `agents/engineer/types.go`
- `agents/engineer/executor.go`
- `agents/engineer/file_ops.go`
- `agents/engineer/bash_ops.go`
- `agents/engineer/consultation.go`
- `agents/engineer/engineer.go`
- `agents/engineer/routing.go`
- `agents/engineer/skills.go`
- `agents/engineer/tools.go`
- `agents/engineer/hooks.go`
- `agents/engineer/engineer_test.go`

**Acceptance Criteria:**

#### Engineer System Prompt
- [ ] Core Principles section:
  - [ ] Dedicated and thorough in executing assigned task
  - [ ] Take ADDITIONAL time to think about clean, modular, testable, readable code
  - [ ] CODE QUALITY IMPERATIVES: minimize lines, defer to stdlib, human-readable
  - [ ] Code must be ROBUST, CORRECT, PERFORMANT, READABLE/MAINTAINABLE
- [ ] Pre-implementation checks:
  - [ ] Memory leaks (unclosed resources, unbounded growth)
  - [ ] Race conditions (shared state, concurrent access)
  - [ ] Off-by-one bugs (loop bounds, array indexing)
  - [ ] Missing error handling (all error paths covered)
  - [ ] Deadlock potential (lock ordering, resource contention)
  - [ ] Code smell (magic numbers, deep nesting, long functions)
- [ ] Scope Management section (CRITICAL):
  - [ ] Review implementation and acceptance criteria in task
  - [ ] Break down work into discrete steps/todos
  - [ ] COUNT steps required
  - [ ] IF >12 TODOS: DO NOT start, request Architect decomposition
  - [ ] MAY break into subtasks, MAY NOT implement outside scope
- [ ] Knowledge Agent Consultation section:
  - [ ] DEFAULT to consulting Librarian BEFORE implementing
  - [ ] DEFAULT to consulting Academic when unclear on optimal approach
  - [ ] Consultation format with type, from_agent, query
- [ ] Implementation Protocol (10 steps):
  - [ ] 1. SCOPE CHECK: If >12 steps, request Architect decomposition
  - [ ] 2. CONSULT LIBRARIAN: Understand existing code
  - [ ] 3. REVIEW CRITERIA: Study implementation/acceptance criteria
  - [ ] 4. CONSULT ACADEMIC: If unclear on optimal approach
  - [ ] 5. QUERY ARCHIVALIST: Check for past issues
  - [ ] 6. PRE-IMPLEMENTATION CHECK: Review for bugs/issues
  - [ ] 7. IMPLEMENT: Follow patterns, minimize code, maximize clarity
  - [ ] 8. VERIFY: Ensure acceptance criteria met
  - [ ] 9. If stuck, consult knowledge agents before asking user
  - [ ] 10. Report progress and blockers
- [ ] CRITICAL reminders: exhaust knowledge agents, 12-step limit, never sacrifice correctness

#### Task Execution
- [ ] Accept task dispatch from Orchestrator via bus
- [ ] Task structure: prompt, context, metadata, timeout
- [ ] Execute task using LLM with tools
- [ ] Return structured result (files changed, output, errors)

#### Consultation Pattern
- [ ] Librarian first: "Does solution exist in codebase?" - DEFAULT
- [ ] Archivalist second: "Have we solved this before?"
- [ ] Academic third: "What's the best practice?" - DEFAULT when unclear
- [ ] Consultation is for CONTEXT, never for skipping work

#### Signals
- [ ] Help signal: request clarification via bus → Orchestrator
- [ ] Completion signal: task done with result
- [ ] Failure signal: task failed with error
- [ ] Progress signal: intermediate status updates

#### Bus Integration
- [ ] Subscribe to `engineer.{id}.task` (per-engineer, session-scoped)
- [ ] Publish to `engineer.{id}.result`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Complete`, `Help`

#### Engineer Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// read_file - Read a file
skills.NewSkill("read_file").
    Description("Read the contents of a file").
    Domain("file_ops").
    Keywords("read", "file", "content", "show").
    StringParam("path", "File path to read", true)

// write_file - Write to a file
skills.NewSkill("write_file").
    Description("Write content to a file, creating or overwriting").
    Domain("file_ops").
    Keywords("write", "file", "create", "save").
    StringParam("path", "File path to write", true).
    StringParam("content", "Content to write", true)

// edit_file - Edit a file
skills.NewSkill("edit_file").
    Description("Edit a file by replacing content").
    Domain("file_ops").
    Keywords("edit", "replace", "modify", "change").
    StringParam("path", "File path to edit", true).
    StringParam("old_content", "Content to replace", true).
    StringParam("new_content", "Replacement content", true)

// run_command - Run a bash command
skills.NewSkill("run_command").
    Description("Run a bash command").
    Domain("bash").
    Keywords("run", "command", "bash", "shell", "execute").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)
```

**Extended Skills (loaded on demand):**
```go
// consult_librarian - Query codebase
skills.NewSkill("consult_librarian").
    Description("Query the codebase for existing implementations").
    Domain("consultation").
    Keywords("codebase", "existing", "implemented", "find").
    StringParam("query", "What to look for", true)

// consult_archivalist - Query history
skills.NewSkill("consult_archivalist").
    Description("Query historical solutions and decisions").
    Domain("consultation").
    Keywords("history", "before", "past", "previous").
    StringParam("query", "What to look for", true)

// consult_academic - Research query
skills.NewSkill("consult_academic").
    Description("Research best practices and approaches").
    Domain("consultation").
    Keywords("research", "best", "practice", "approach").
    StringParam("query", "What to research", true)

// request_help - Ask for clarification
skills.NewSkill("request_help").
    Description("Request clarification from the Architect").
    Domain("communication").
    Keywords("help", "clarify", "confused", "unclear").
    StringParam("question", "What needs clarification", true).
    StringParam("context", "Relevant context", false)

// report_progress - Report task progress
skills.NewSkill("report_progress").
    Description("Report progress on the current task").
    Domain("communication").
    Keywords("progress", "status", "update").
    StringParam("status", "Current status", true).
    IntParam("percent_complete", "Completion percentage", false)
```

**Search & Navigation Skills (dynamically configurable):**
```go
// find_files - Find files using find command
skills.NewSkill("find_files").
    Description("Find files using the find command with various criteria").
    Domain("search").
    Keywords("find", "locate", "files", "directory", "name", "type").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory, l=symlink)", false).
    StringParam("mtime", "Modified time (e.g., -7 for last 7 days)", false).
    StringParam("size", "File size (e.g., +1M for >1MB)", false).
    IntParam("maxdepth", "Maximum directory depth", false).
    ArrayParam("exec", "Command to execute on found files", false)

// ast_grep - Search code using AST patterns
skills.NewSkill("ast_grep").
    Description("Search code using AST structural patterns").
    Domain("search").
    Keywords("ast", "pattern", "structural", "syntax").
    StringParam("pattern", "AST grep pattern", true).
    StringParam("language", "Target language", true).
    StringParam("path", "Search path", false)

// glob_search - Find files by glob pattern
skills.NewSkill("glob_search").
    Description("Find files matching a glob pattern").
    Domain("search").
    Keywords("glob", "find", "files", "pattern").
    StringParam("pattern", "Glob pattern (e.g., **/*.go)", true).
    StringParam("path", "Base path", false)

// grep_search - Search file contents with regex
skills.NewSkill("grep_search").
    Description("Search file contents using regex").
    Domain("search").
    Keywords("grep", "regex", "search", "content").
    StringParam("pattern", "Regex pattern", true).
    StringParam("path", "Search path", false).
    BoolParam("case_insensitive", "Case insensitive search", false)

// cat_file - Read file contents (alias for read_file with options)
skills.NewSkill("cat_file").
    Description("Display file contents with line numbers").
    Domain("file_ops").
    Keywords("cat", "display", "show", "print").
    StringParam("path", "File path", true).
    IntParam("from_line", "Start from line", false).
    IntParam("to_line", "End at line", false)

// sed_replace - Stream editor replace
skills.NewSkill("sed_replace").
    Description("Replace text patterns in files").
    Domain("file_ops").
    Keywords("sed", "replace", "substitute", "regex").
    StringParam("pattern", "Search pattern", true).
    StringParam("replacement", "Replacement text", true).
    StringParam("path", "File path", true).
    BoolParam("global", "Replace all occurrences", false)

// awk_process - Process text with awk
skills.NewSkill("awk_process").
    Description("Process text using awk patterns").
    Domain("file_ops").
    Keywords("awk", "process", "text", "columns").
    StringParam("program", "Awk program", true).
    StringParam("path", "Input file path", true)

// ping_host - Network connectivity check
skills.NewSkill("ping_host").
    Description("Check network connectivity to a host").
    Domain("network").
    Keywords("ping", "network", "connectivity", "host").
    StringParam("host", "Host to ping", true).
    IntParam("count", "Number of pings", false)
```

**Code Quality Tools (dynamically configurable):**
```go
// run_linter - Run configured linter
skills.NewSkill("run_linter").
    Description("Run the configured linter for the language").
    Domain("quality").
    Keywords("lint", "linter", "check", "style").
    StringParam("path", "Path to lint", true).
    StringParam("linter", "Specific linter (auto-detect if not specified)", false).
    BoolParam("fix", "Auto-fix issues if supported", false)

// run_formatter - Run configured formatter
skills.NewSkill("run_formatter").
    Description("Run the configured formatter for the language").
    Domain("quality").
    Keywords("format", "formatter", "style", "prettify").
    StringParam("path", "Path to format", true).
    StringParam("formatter", "Specific formatter (auto-detect if not specified)", false).
    BoolParam("check_only", "Only check, don't modify", false)

// query_lsp - Query language server for information
skills.NewSkill("query_lsp").
    Description("Query the language server for code intelligence").
    Domain("quality").
    Keywords("lsp", "language", "server", "completion", "hover").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true).
    EnumParam("query_type", "Type of query", []string{"hover", "definition", "references", "completion"}, true)
```

**Environment & Package Skills (dynamically configurable):**
```go
// create_venv - Create virtual environment
skills.NewSkill("create_venv").
    Description("Create a virtual environment for the project").
    Domain("environment").
    Keywords("venv", "virtualenv", "environment", "isolate").
    StringParam("path", "Environment path", true).
    EnumParam("type", "Environment type", []string{"python-venv", "python-virtualenv", "node-nvm", "go-mod"}, true).
    StringParam("version", "Language/runtime version", false)

// activate_venv - Activate virtual environment
skills.NewSkill("activate_venv").
    Description("Activate a virtual environment").
    Domain("environment").
    Keywords("activate", "venv", "environment").
    StringParam("path", "Environment path", true)

// install_packages - Install packages
skills.NewSkill("install_packages").
    Description("Install packages into the environment").
    Domain("environment").
    Keywords("install", "package", "dependency", "npm", "pip", "go").
    ArrayParam("packages", "Package names to install", true).
    StringParam("manager", "Package manager (auto-detect if not specified)", false).
    BoolParam("dev", "Install as dev dependency", false)

// list_packages - List installed packages
skills.NewSkill("list_packages").
    Description("List installed packages in the environment").
    Domain("environment").
    Keywords("list", "packages", "installed", "dependencies").
    StringParam("path", "Environment or project path", false)
```

**Bash Execution (user-approved commands):**
```go
// run_approved_bash - Run pre-approved bash command
skills.NewSkill("run_approved_bash").
    Description("Run a bash command from the approved list").
    Domain("bash").
    Keywords("bash", "shell", "command", "run").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false).
    BoolParam("background", "Run in background", false)

// Engineer maintains a list of user-approved command patterns
type ApprovedCommandPatterns struct {
    Patterns  []string  // Regex patterns for allowed commands
    Blocklist []string  // Explicitly blocked commands
}

// Default approved patterns (user can extend)
var DefaultApprovedPatterns = ApprovedCommandPatterns{
    Patterns: []string{
        `^go (build|test|run|fmt|vet|mod).*`,
        `^npm (install|run|test|build).*`,
        `^pip (install|list|freeze).*`,
        `^git (status|log|diff|branch|checkout).*`,
        `^cat .*`,
        `^ls .*`,
        `^grep .*`,
        `^find .*`,
    },
    Blocklist: []string{
        `rm -rf /`,
        `sudo .*`,
        `chmod 777.*`,
    },
}
```

**Superpowers Skills (from superpowers methodology):**
```go
// test_driven_development - TDD implementation phase
// Source: superpowers/test-driven-development
skills.NewSkill("test_driven_development").
    Description("Implement code to make failing tests pass (TDD Phase 3 - GREEN)").
    Domain("implementation").
    Keywords("tdd", "green", "implement", "pass").
    ObjectParam("tests", "Tests to make pass", true).
    ObjectParam("criteria", "Success criteria from Inspector", true)

// systematic_debugging - Methodical debugging approach
// Source: superpowers/systematic-debugging
skills.NewSkill("systematic_debugging").
    Description("Debug issues systematically: reproduce, isolate, fix, verify").
    Domain("debugging").
    Keywords("debug", "fix", "issue", "systematic").
    StringParam("symptom", "What is failing/broken", true).
    BoolParam("create_reproduction", "Create minimal reproduction", false)

// receiving_code_review_impl - Implement code review feedback
// Source: superpowers/receiving-code-review
skills.NewSkill("receiving_code_review_impl").
    Description("Implement code review feedback with technical evaluation").
    Domain("implementation").
    Keywords("review", "feedback", "implement", "fix").
    ObjectParam("feedback", "Code review feedback to implement", true).
    BoolParam("verify_first", "Verify suggestion is valid before implementing", false)

// verification_before_completion - Verify implementation complete
// Source: superpowers/verification-before-completion
skills.NewSkill("verification_before_completion").
    Description("Verify implementation satisfies all criteria before marking complete").
    Domain("implementation").
    Keywords("verify", "complete", "done", "check").
    ObjectParam("criteria", "Criteria to verify against", true).
    ObjectParam("implementation", "Implementation to verify", true)
```

#### Engineer Tools
```go
var EngineerTools = []ToolDefinition{
    // Core file operations
    {Name: "read_file", Skill: "read_file"},
    {Name: "write_file", Skill: "write_file"},
    {Name: "edit_file", Skill: "edit_file"},
    {Name: "run_command", Skill: "run_command"},
    // Consultation
    {Name: "consult_librarian", Skill: "consult_librarian"},
    {Name: "consult_archivalist", Skill: "consult_archivalist"},
    {Name: "request_help", Skill: "request_help"},
    // Search & navigation
    {Name: "find_files", Skill: "find_files"},
    {Name: "ast_grep", Skill: "ast_grep"},
    {Name: "glob_search", Skill: "glob_search"},
    {Name: "grep_search", Skill: "grep_search"},
    {Name: "cat_file", Skill: "cat_file"},
    {Name: "sed_replace", Skill: "sed_replace"},
    {Name: "awk_process", Skill: "awk_process"},
    // Code quality
    {Name: "run_linter", Skill: "run_linter"},
    {Name: "run_formatter", Skill: "run_formatter"},
    {Name: "query_lsp", Skill: "query_lsp"},
    // Environment
    {Name: "create_venv", Skill: "create_venv"},
    {Name: "activate_venv", Skill: "activate_venv"},
    {Name: "install_packages", Skill: "install_packages"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Engineer Hooks
- [ ] Pre-execute hook: load relevant skills based on task
- [ ] Post-execute hook: record result in Archivalist
- [ ] Pre-file-write hook: validate file path safety
- [ ] Post-file-write hook: notify file changed event
- [ ] Pre-consultation hook: check if already consulted
- [ ] Post-consultation hook: cache consultation result

#### Memory Management & Pipeline Handoff

**Model**: Opus 4.5

**CRITICAL**: At 95%, Engineer triggers PIPELINE HANDOFF, NOT local compaction.

**Files to add:**
- `agents/engineer/memory.go`
- `agents/engineer/handoff.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 95% threshold: trigger pipeline handoff sequence
- [ ] Bundle complete handoff state: original prompt + accomplished + remaining + files changed
- [ ] Send `HANDOFF_REQUEST` to Architect (via Guide) with bundled state
- [ ] Wait for `HANDOFF_ACK` confirming new pipeline created
- [ ] Handoff state goes to Archivalist for persistence
- [ ] Retry logic: 3 attempts with exponential backoff
- [ ] Fallback: if handoff fails after retries, summarize → Archivalist → compact locally

```go
// Engineer handoff state (sent to Architect)
type EngineerHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    OriginalPrompt    string            `json:"original_prompt"`
    Accomplished      []string          `json:"accomplished"`
    FilesChanged      []FileChange      `json:"files_changed"`
    Remaining         []string          `json:"remaining"`
    ContextNotes      string            `json:"context_notes"`
    ContextUsage      float64           `json:"context_usage"`
    HandoffReason     string            `json:"handoff_reason"`
}

type FileChange struct {
    Path        string `json:"path"`
    ChangeType  string `json:"change_type"`  // created, modified, deleted
    Summary     string `json:"summary"`
}

// Engineer context monitoring
func (e *Engineer) checkContextAndHandoff() error {
    usage := e.getContextUsage()

    if usage >= 0.95 {
        // Trigger pipeline handoff (NOT local compaction)
        return e.triggerPipelineHandoff()
    }
    return nil
}

func (e *Engineer) triggerPipelineHandoff() error {
    state := e.buildHandoffState()

    // Send to Architect via Guide
    msg := &Message{
        Type:    "HANDOFF_REQUEST",
        From:    e.ID,
        To:      "architect",  // Routed through Guide
        Payload: state,
    }

    // Retry logic
    for attempt := 0; attempt < 3; attempt++ {
        if err := e.bus.Publish("guide.route", msg); err != nil {
            time.Sleep(time.Duration(1<<attempt) * time.Second)
            continue
        }

        // Wait for acknowledgment
        select {
        case ack := <-e.handoffAck:
            return nil  // Handoff successful
        case <-time.After(30 * time.Second):
            continue  // Retry
        }
    }

    // Fallback: summarize and compact locally
    return e.fallbackCompact()
}
```

**Handoff Flow:**
```
Engineer (95%) ──────────────────────────────────────────────────────────────►
    │
    ▼
[Build Handoff State]
    │ - Original prompt
    │ - Accomplished tasks
    │ - Files changed
    │ - Remaining tasks
    │ - Context notes
    │
    ▼
[HANDOFF_REQUEST] ──► Guide ──► Architect
                                   │
                                   ▼
                          [Examine state]
                          [Adjust workflow if needed]
                                   │
                                   ▼
               [CREATE_PIPELINE_WITH_STATE] ──► Guide ──► Orchestrator
                                                            │
                                                            ▼
                                                    [Create new pipeline]
                                                    [Transfer E+I+T state]
                                                    [Close old pipeline]
                                                            │
                                                            ▼
                                                    [HANDOFF_COMPLETE] ──► Architect ──► Engineer
```

**Tests:**
- [ ] Execute file read/write task
- [ ] Test consultation flow
- [ ] Test help request routing
- [ ] Test handoff trigger at 95% context
- [ ] Test handoff retry logic
- [ ] Test handoff fallback to local compaction
- [ ] Test handoff state bundling completeness

### 2.2 Orchestrator (DAG Execution Engine)

Executes DAG workflows, manages Engineers.

**Files to create:**
- `agents/orchestrator/types.go`
- `agents/orchestrator/dispatcher.go`
- `agents/orchestrator/correlator.go`
- `agents/orchestrator/engineer_pool.go`
- `agents/orchestrator/orchestrator.go`
- `agents/orchestrator/routing.go`
- `agents/orchestrator/skills.go`
- `agents/orchestrator/tools.go`
- `agents/orchestrator/hooks.go`
- `agents/orchestrator/orchestrator_test.go`

**Acceptance Criteria:**

#### DAG Execution
- [ ] Accept DAG from Architect via bus
- [ ] Use DAGExecutor from Phase 0.2
- [ ] **Create Pipelines for engineer tasks** (not bare Engineers) - see Phase 2.3
- [ ] Correlate responses by correlation ID
- [ ] Track node state and timing

#### Pipeline Management (replaces direct Engineer management)
- [ ] **Create Pipeline for each engineer task** (contains Engineer + Inspector + Tester)
- [ ] Use PipelineManager from Phase 2.3
- [ ] Wait for PIPELINE_COMPLETE or PIPELINE_FAILED
- [ ] Aggregate pipeline results for DAG node completion
- [ ] Session-scoped pipeline tracking

#### Engineer Management (for non-pipeline tasks)
- [ ] Create Engineers on demand for non-code tasks (research, context gathering)
- [ ] Pool Engineers for reuse
- [ ] Destroy idle Engineers after timeout
- [ ] Session-scoped Engineer pool

#### Status Propagation
- [ ] Status updates to Architect
- [ ] Per-node progress events
- [ ] Overall DAG progress (layers completed / total)
- [ ] Estimated time remaining

#### Quality Loop (Two-Level)

**Level 1: Pipeline-Internal (handled within Pipeline - see Phase 2.3)**
- [ ] Pipeline handles per-task Inspector validation internally
- [ ] Pipeline handles per-task Tester validation internally
- [ ] Direct feedback loops within pipeline (no Architect involvement)

**Level 2: Session-Wide (after all Pipelines complete)**
- [ ] Full validation: signal Inspector when ALL pipelines complete
- [ ] Full test suite: signal Tester when Inspector passes
- [ ] Handle corrections from session-wide Inspector/Tester via Architect
- [ ] Architect creates FIX DAG for session-wide issues → new Pipelines

#### Clarification Routing
- [ ] Engineer help requests → Architect
- [ ] Architect responses → Engineer via correlation

#### Bus Integration
- [ ] Subscribe to `orchestrator.execute` (session-scoped)
- [ ] Subscribe to `orchestrator.cancel` (session-scoped)
- [ ] Publish to `orchestrator.status`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `DAG_EXECUTE`, `DAG_STATUS`, `DAG_CANCEL`

#### Orchestrator Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// execute_dag - Execute a DAG workflow
skills.NewSkill("execute_dag").
    Description("Execute a DAG workflow").
    Domain("orchestration").
    Keywords("execute", "run", "dag", "workflow").
    ObjectParam("dag", "DAG definition", true)

// get_status - Get execution status
skills.NewSkill("get_status").
    Description("Get current execution status").
    Domain("orchestration").
    Keywords("status", "progress", "state").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false)

// cancel_execution - Cancel current execution
skills.NewSkill("cancel_execution").
    Description("Cancel the current DAG execution").
    Domain("orchestration").
    Keywords("cancel", "stop", "abort").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false)
```

**Extended Skills (loaded on demand):**
```go
// pause_execution - Pause execution
skills.NewSkill("pause_execution").
    Description("Pause the current DAG execution").
    Domain("orchestration").
    Keywords("pause", "hold", "wait").
    StringParam("dag_id", "DAG ID", false)

// resume_execution - Resume execution
skills.NewSkill("resume_execution").
    Description("Resume a paused DAG execution").
    Domain("orchestration").
    Keywords("resume", "continue", "unpause").
    StringParam("dag_id", "DAG ID", false)

// retry_node - Retry a failed node
skills.NewSkill("retry_node").
    Description("Retry a specific failed node").
    Domain("orchestration").
    Keywords("retry", "again", "rerun").
    StringParam("node_id", "Node ID to retry", true)

// skip_node - Skip a failed node
skills.NewSkill("skip_node").
    Description("Skip a failed node and continue").
    Domain("orchestration").
    Keywords("skip", "ignore", "bypass").
    StringParam("node_id", "Node ID to skip", true)

// get_node_result - Get result of a node
skills.NewSkill("get_node_result").
    Description("Get the result of a specific node").
    Domain("orchestration").
    Keywords("result", "output", "node").
    StringParam("node_id", "Node ID", true)
```

**Workflow Adjustment Skills:**
```go
// receive_workflow_update - Receive updated workflow from Architect
skills.NewSkill("receive_workflow_update").
    Description("Receive and apply an updated workflow from Architect").
    Domain("orchestration").
    Keywords("receive", "update", "workflow", "modified").
    StringParam("dag_id", "Original DAG ID being updated", true).
    ObjectParam("updated_dag", "New DAG definition", true).
    EnumParam("merge_strategy", "How to merge with current state", []string{"replace", "merge_pending", "abort_and_replace"}, false)

// receive_workflow_cancellation - Receive workflow cancellation
skills.NewSkill("receive_workflow_cancellation").
    Description("Receive and process workflow cancellation signal").
    Domain("orchestration").
    Keywords("cancel", "abort", "stop", "workflow").
    StringParam("dag_id", "DAG ID to cancel", true).
    EnumParam("cleanup_mode", "Cleanup mode", []string{"graceful", "immediate", "wait_current"}, false).
    StringParam("reason", "Cancellation reason", false)

// apply_workflow_adjustment - Apply runtime workflow adjustment
skills.NewSkill("apply_workflow_adjustment").
    Description("Apply runtime adjustments to executing workflow").
    Domain("orchestration").
    Keywords("adjust", "runtime", "modify", "live").
    StringParam("dag_id", "DAG ID to adjust", true).
    ObjectParam("adjustments", "List of adjustments", true)

// Adjustment types
type WorkflowAdjustment struct {
    Type       string // "add_node", "remove_node", "modify_node", "change_deps", "reprioritize"
    NodeID     string
    Details    map[string]any
}
```

**Plan Change Request Skills:**
```go
// request_plan_change - Request Architect to modify plan
skills.NewSkill("request_plan_change").
    Description("Request Architect to create new or modified workflow").
    Domain("orchestration").
    Keywords("request", "plan", "change", "modify", "architect").
    EnumParam("change_type", "Type of change requested", []string{"extend", "modify", "replace", "fix"}, true).
    StringParam("reason", "Reason for requested change", true).
    ObjectParam("context", "Context for the request (failed nodes, blockers, etc.)", true).
    ObjectParam("suggested_changes", "Optional suggested modifications", false)

// escalate_blocker - Escalate blocking issue to Architect
skills.NewSkill("escalate_blocker").
    Description("Escalate a blocking issue that requires plan changes").
    Domain("orchestration").
    Keywords("escalate", "blocker", "issue", "stuck").
    StringParam("node_id", "Blocked node ID", true).
    StringParam("blocker_type", "Type of blocker", true).
    StringParam("description", "Description of the blocker", true).
    ArrayParam("attempted_resolutions", "What was already tried", false)

// Request/response types for plan changes
type PlanChangeRequest struct {
    ID              string
    ChangeType      string                 // extend, modify, replace, fix
    Reason          string
    BlockedNodes    []string               // Nodes that are blocked
    FailedNodes     []string               // Nodes that failed
    Context         map[string]any         // Additional context
    SuggestedChanges []WorkflowAdjustment  // Optional suggestions
    Priority        string                 // How urgent is this
    CreatedAt       time.Time
}

type PlanChangeResponse struct {
    RequestID       string
    Approved        bool
    NewDAG          *DAG                   // New or modified DAG if approved
    Modifications   []WorkflowAdjustment   // Specific modifications if partial
    Reason          string                 // Reason if rejected
    Instructions    string                 // Additional instructions
}
```

#### Orchestrator Tools
```go
var OrchestratorTools = []ToolDefinition{
    // Core execution
    {Name: "execute_dag", Skill: "execute_dag"},
    {Name: "get_status", Skill: "get_status"},
    {Name: "cancel_execution", Skill: "cancel_execution"},
    // Workflow management
    {Name: "pause_execution", Skill: "pause_execution"},
    {Name: "resume_execution", Skill: "resume_execution"},
    {Name: "retry_node", Skill: "retry_node"},
    {Name: "skip_node", Skill: "skip_node"},
    {Name: "get_node_result", Skill: "get_node_result"},
    // Workflow adjustment
    {Name: "receive_workflow_update", Skill: "receive_workflow_update"},
    {Name: "receive_workflow_cancellation", Skill: "receive_workflow_cancellation"},
    {Name: "apply_workflow_adjustment", Skill: "apply_workflow_adjustment"},
    // Plan changes
    {Name: "request_plan_change", Skill: "request_plan_change"},
    {Name: "escalate_blocker", Skill: "escalate_blocker"},
    // Routing
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
}
```

#### Orchestrator Hooks
- [ ] Pre-execute hook: validate DAG structure
- [ ] Post-execute hook: store execution history in Archivalist
- [ ] Pre-dispatch hook: check Engineer availability
- [ ] Post-dispatch hook: record dispatch time
- [ ] Pre-complete hook: trigger Inspector validation
- [ ] Post-complete hook: notify Architect

**Tests:**
- [ ] Execute multi-layer DAG
- [ ] Test Engineer pool management
- [ ] Test clarification routing
- [ ] Test cancellation

### 2.3 Pipeline Infrastructure

Isolated TDD execution contexts implementing RED → GREEN → REFACTOR methodology.

**TDD Pipeline Flow (per task):**
1. **Phase 1 - INSPECTOR**: Defines success criteria, quality gates, constraints
2. **Phase 2 - TESTER (RED)**: Creates tests based on criteria (tests WILL FAIL - no implementation yet)
3. **Phase 3 - WORKER (GREEN)**: Implements to make tests pass
4. **Phase 4 - VALIDATION**: Both Inspector AND Tester validate in parallel
5. **LOOP**: If either fails, loop back to Phase 1 until BOTH pass

**CRITICAL**: Pipelines enable TWO-LEVEL quality assurance:
1. **Pipeline-internal (TDD)**: Inspector criteria → Tester tests (RED) → Worker implements (GREEN) → BOTH validate
2. **Session-wide (post-DAG)**: Full integration validation through Architect after all pipelines complete

**Files to create:**
- `core/pipeline/types.go`
- `core/pipeline/pipeline.go`
- `core/pipeline/bus.go`
- `core/pipeline/manager.go`
- `core/pipeline/lifecycle.go`
- `core/pipeline/pipeline_test.go`

**Dependencies**: Phase 2.1 (Engineer), Phase 2.2 (Orchestrator)

**Acceptance Criteria:**

#### Pipeline Data Model (`core/pipeline/types.go`)
- [ ] `PipelineID` type with unique generation
- [ ] `PipelineState` enum (TDD phases):
  - [ ] `Pending` - Not yet started
  - [ ] `DefiningCriteria` - Phase 1: Inspector defines success criteria
  - [ ] `CreatingTests` - Phase 2: Tester creates tests (RED - tests WILL fail)
  - [ ] `Executing` - Phase 3: Worker implements (GREEN - make tests pass)
  - [ ] `Validating` - Phase 4: Both Inspector AND Tester validate in parallel
  - [ ] `Completed` - Both Inspector AND Tester passed
  - [ ] `Failed` - Max loops exceeded
- [ ] `Pipeline` struct with:
  - [ ] ID (PipelineID), SessionID, DAGID, TaskName (human-readable, e.g. `create_dashboard`)
  - [ ] State, CreatedAt, CompletedAt
  - [ ] WorkerType (Designer OR Engineer - one per pipeline)
  - [ ] WorkerID, InspectorID, TesterID (co-located instances)
  - [ ] `PipelineContext` (shared by all three agents)
  - [ ] LoopCount, MaxLoops (TDD iteration tracking)
  - [ ] `InspectorCriteria` (Phase 1 output)
  - [ ] `TesterTests` (Phase 2 output)
  - [ ] `WorkerOutput` (Phase 3 output)
  - [ ] `InspectorResult`, `TesterResult` (Phase 4 validation)
- [ ] `InspectorCriteria` struct (Phase 1 output):
  - [ ] TaskID, SuccessCriteria[], QualityGates[], Constraints[], CreatedAt
- [ ] `SuccessCriterion` struct with ID, Description, Verifiable (bool), Priority
- [ ] `QualityGate` struct with Name, Threshold, Automated (bool)
- [ ] `Constraint` struct with Type (security/performance/a11y), Requirement, Rationale
- [ ] `TesterTests` struct (Phase 2 output):
  - [ ] TaskID, TestFiles[], InitialRun (should FAIL), BasedOnCriteria[], CreatedAt
- [ ] `TestFile` struct with Path, TestNames[], Framework
- [ ] `TestRun` struct with Timestamp, TotalTests, Passed, Failed, Skipped, Duration, AllPassed, Failures[]
- [ ] `TestFailure` struct with TestName, File, Message, Expected, Actual, StackTrace
- [ ] `PipelineContext` struct with:
  - [ ] TaskPrompt, TaskConstraints
  - [ ] UpstreamOutputs (from DAG dependencies)
  - [ ] ModifiedFiles, CreatedFiles (tracked by pipeline)
  - [ ] LoopHistory (all previous TDD iterations)
- [ ] `IsComplete()` method: returns true only when BOTH Inspector.pass AND Tester.pass
- [ ] `NeedsLoop()` method: returns true if either failed and loops remain

#### Pipeline Internal Bus (`core/pipeline/bus.go`)
- [ ] `PipelineBus` struct for TDD phase coordination (NOT routed through Guide)
- [ ] Phase transition channels:
  - [ ] `criteriaReady` - Inspector → Tester (Phase 1 → 2)
  - [ ] `testsReady` - Tester → Worker (Phase 2 → 3)
  - [ ] `implementationReady` - Worker → Validation (Phase 3 → 4)
- [ ] Validation channels:
  - [ ] `inspectorResult` - Inspector validation output
  - [ ] `testerResult` - Tester validation output
- [ ] Feedback channels (for loop iterations):
  - [ ] `loopFeedback` - Combined feedback when loop needed
- [ ] Context-based cancellation
- [ ] Graceful shutdown

```go
type PipelineBus struct {
    pipelineID          PipelineID

    // TDD Phase Transitions
    criteriaReady       chan *InspectorCriteria  // Phase 1 → 2
    testsReady          chan *TesterTests        // Phase 2 → 3
    implementationReady chan *WorkerOutput       // Phase 3 → 4

    // Validation (Phase 4 - parallel)
    inspectorResult     chan *InspectorResult
    testerResult        chan *TesterResult

    // Loop feedback (when either validation fails)
    loopFeedback        chan *LoopFeedback

    ctx                 context.Context
    cancel              context.CancelFunc
    closed              atomic.Bool
}

type LoopFeedback struct {
    LoopCount           int
    InspectorFailed     bool
    TesterFailed        bool
    InspectorIssues     []InspectorIssue
    TesterFailures      []TestFailure
    RefinementGuidance  string  // Hints for next iteration
}
```

#### Pipeline Lifecycle (`core/pipeline/lifecycle.go`)
- [ ] TDD State machine (RED → GREEN → REFACTOR):
  - [ ] `Pending` → `DefiningCriteria` (Phase 1 starts)
  - [ ] `DefiningCriteria` → `CreatingTests` (Phase 2 starts when criteria ready)
  - [ ] `CreatingTests` → `Executing` (Phase 3 starts when tests ready - RED confirmed)
  - [ ] `Executing` → `Validating` (Phase 4 starts when implementation ready)
  - [ ] `Validating` → `Completed` (ONLY when BOTH Inspector AND Tester pass)
  - [ ] `Validating` → `DefiningCriteria` (Loop if EITHER fails and loops remain)
  - [ ] Any state → `Failed` (on error or max loops exceeded)
- [ ] Phase 1: Inspector defines criteria (before any tests exist)
- [ ] Phase 2: Tester creates tests based on criteria (RED - tests WILL fail)
- [ ] Phase 3: Worker implements to make tests pass (GREEN)
- [ ] Phase 4: Parallel validation by Inspector AND Tester
- [ ] Loop condition: `!Inspector.pass || !Tester.pass` AND `loopCount < maxLoops`
- [ ] Completion condition: `Inspector.pass == true && Tester.pass == true`
- [ ] Max loops enforcement (default: 3)
- [ ] User override handling (`/task <name> ignore_inspector`, `/task <name> ignore_tester`)

```
TDD Pipeline Lifecycle:

  PENDING
     │
     ▼
  DEFINING_CRITERIA (Phase 1: Inspector)
     │ Inspector outputs: SuccessCriteria, QualityGates, Constraints
     ▼
  CREATING_TESTS (Phase 2: Tester - RED)
     │ Tester outputs: TestFiles, InitialRun (MUST FAIL - no implementation)
     ▼
  EXECUTING (Phase 3: Worker - GREEN)
     │ Worker outputs: Implementation that should make tests pass
     ▼
  VALIDATING (Phase 4: Both Inspector AND Tester in parallel)
     │
     ├── BOTH pass ────────────────────────────────────► COMPLETED
     │
     ├── EITHER fails + loops < max ────────────────┐
     │                                              │
     │   ◄──────────────────────────────────────────┘
     │   (Loop back to Phase 1 with feedback)
     │
     └── EITHER fails + loops >= max ──────────────► User prompted:
                                                    1. Increase max_loops
                                                    2. ignore_inspector
                                                    3. ignore_tester
                                                    4. Cancel → FAILED
```

#### Pipeline Manager (`core/pipeline/manager.go`)
- [ ] `PipelineManager` interface with Create, Start, Cancel
- [ ] `PipelineManager` interface with Get, GetBySession, GetByDAG, GetActive
- [ ] `PipelineManager` interface with RouteUserMessage (for /task command)
- [ ] `PipelineManager` interface with GetResult, CloseAll
- [ ] `CreatePipelineConfig` with SessionID, DAGID, TaskName (human-readable), TaskPrompt, TaskConstraints, ComplianceCriteria, UpstreamOutputs, MaxLoops
- [ ] `PipelineResult` with PipelineID, Success, EngineerResult, InspectorResult, TesterResult, ModifiedFiles, CreatedFiles, LoopsUsed, Duration
- [ ] Concurrent pipeline execution within session
- [ ] Pipeline-scoped resource allocation

```go
type PipelineManager interface {
    Create(ctx context.Context, cfg CreatePipelineConfig) (*Pipeline, error)
    Start(ctx context.Context, id PipelineID) error
    Cancel(ctx context.Context, id PipelineID) error
    Get(id PipelineID) (*Pipeline, bool)
    GetBySession(sessionID string) []*Pipeline
    GetByDAG(dagID string) []*Pipeline
    GetActive() []*Pipeline
    RouteUserMessage(pipelineID PipelineID, msg string) error
    GetResult(id PipelineID) (*PipelineResult, error)
    CloseAll() error
}
```

#### Guide Integration for /task Command
- [ ] New Guide skill: `task_interact` for routing to specific pipelines
- [ ] Actions: prompt, query, interrupt, ignore_inspector, ignore_tester, handoff, stop_handoff
- [ ] Route user messages through Guide to Pipeline to Engineer
- [ ] **Task names are human-readable, generated by Architect as DAG node keys**
- [ ] Support task name lookup (exact match or fuzzy match for convenience)

```go
// Guide skill for pipeline interaction
skills.NewSkill("task_interact").
    Description("Route user message to a specific pipeline's engineer").
    Domain("routing").
    Keywords("/task", "pipeline", "engineer").
    StringParam("task_name", "Human-readable task name (DAG node key)", true).
    EnumParam("action", "Action type", []string{"prompt", "query", "interrupt", "ignore_inspector", "ignore_tester", "handoff", "stop_handoff"}, true).
    StringParam("message", "Message content (for prompt/query)", false)

// Usage examples (task names generated by Architect):
// /task create_dashboard prompt "Focus on error handling first"
// /task setup_auth_middleware query "What files have you modified?"
// /task implement_user_model interrupt
// /task add_api_routes ignore_inspector
// /task write_unit_tests ignore_tester
// /task create_dashboard handoff         ← User-triggered pipeline handoff
// /task setup_auth_middleware stop_handoff  ← Cancel/prevent handoff
```

#### Message Types (Pipeline-Specific, TDD-Aware)

**TDD Phase Transition Messages (pipeline-internal, NOT through Guide):**
- [ ] `CRITERIA_READY` - Inspector → Tester (Phase 1 → 2, carries InspectorCriteria)
- [ ] `TESTS_READY` - Tester → Worker (Phase 2 → 3, carries TesterTests + RED confirmation)
- [ ] `IMPLEMENTATION_READY` - Worker → Validation (Phase 3 → 4, carries WorkerOutput)
- [ ] `VALIDATION_RESULT` - Inspector/Tester → Pipeline (Phase 4, carries pass/fail + details)
- [ ] `LOOP_FEEDBACK` - Pipeline → All Agents (when loop needed, carries combined feedback)

**Pipeline Lifecycle Messages:**
- [ ] `PIPELINE_COMPLETE` - Pipeline → Orchestrator (BOTH Inspector AND Tester passed)
- [ ] `PIPELINE_FAILED` - Pipeline → Orchestrator (max loops exceeded)
- [ ] `PIPELINE_LOOP` - Internal (Phase 4 → Phase 1 with feedback)

**User Interaction Messages (through Guide):**
- [ ] `USER_TASK_PROMPT` - User → Guide → Pipeline (through Guide)
- [ ] `USER_TASK_QUERY` - User → Guide → Pipeline (through Guide)
- [ ] `USER_TASK_INTERRUPT` - User → Guide → Pipeline (through Guide)
- [ ] `USER_IGNORE_INSPECTOR` - User → Guide → Pipeline (bypass Inspector in validation)
- [ ] `USER_IGNORE_TESTER` - User → Guide → Pipeline (bypass Tester in validation)
- [ ] `USER_TRIGGER_HANDOFF` - User → Guide → Pipeline → Worker (user-initiated handoff)
- [ ] `USER_STOP_HANDOFF` - User → Guide → Pipeline (cancel/prevent handoff)

#### Session Context Updates
- [ ] Add `ActivePipelines map[string]*PipelineState` to SessionContext
- [ ] Pipeline state visible in session status
- [ ] Pipeline cleanup on session close

#### Pipeline Handoff Mechanism

**CRITICAL**: When Engineer hits 95% context, triggers PIPELINE HANDOFF (not local compaction).

**Files to add:**
- `core/pipeline/handoff.go`

**Acceptance Criteria:**

##### Handoff Data Structures (TDD-Aware)
- [ ] `PipelineHandoff` struct containing:
  - [ ] OldPipelineID, NewPipelineID
  - [ ] SessionID, DAGID, TaskID
  - [ ] HandoffReason, HandoffIndex (chains are traceable)
  - [ ] Timestamp
  - [ ] CurrentTDDPhase (which phase was active at handoff)
  - [ ] LoopCount (TDD iteration at handoff)
  - [ ] WorkerHandoffState, InspectorHandoffState, TesterHandoffState
- [ ] `WorkerHandoffState` with OriginalPrompt, Accomplished, FilesChanged, Remaining, ContextNotes
- [ ] `InspectorHandoffState` (TDD-aware):
  - [ ] DefinedCriteria (Phase 1 output, if completed)
  - [ ] ValidationResult (Phase 4 output, if reached)
  - [ ] PendingCriteriaRefinements (for next loop)
- [ ] `TesterHandoffState` (TDD-aware):
  - [ ] CreatedTests (Phase 2 output, TestFiles list)
  - [ ] InitialRunResult (RED phase confirmation)
  - [ ] ValidationRunResult (Phase 4 output, if reached)
  - [ ] PendingTestUpdates (for next loop)

```go
type PipelineHandoff struct {
    OldPipelineID     PipelineID              `json:"old_pipeline_id"`
    NewPipelineID     PipelineID              `json:"new_pipeline_id"`
    SessionID         string                  `json:"session_id"`
    DAGID             string                  `json:"dag_id"`
    TaskName          string                  `json:"task_name"`  // Human-readable, e.g. "create_dashboard"
    HandoffReason     string                  `json:"handoff_reason"`
    HandoffIndex      int                     `json:"handoff_index"`  // 0 = first, chains traceable
    Timestamp         time.Time               `json:"timestamp"`

    // TDD State at Handoff
    CurrentTDDPhase   PipelineState           `json:"current_tdd_phase"`
    LoopCount         int                     `json:"loop_count"`

    // Agent States
    WorkerState       *WorkerHandoffState     `json:"worker_state"`
    InspectorState    *InspectorHandoffState  `json:"inspector_state"`
    TesterState       *TesterHandoffState     `json:"tester_state"`
}

type InspectorHandoffState struct {
    DefinedCriteria          *InspectorCriteria `json:"defined_criteria,omitempty"`
    ValidationResult         *InspectorResult   `json:"validation_result,omitempty"`
    PendingCriteriaRefinements []string         `json:"pending_refinements,omitempty"`
}

type TesterHandoffState struct {
    CreatedTests         *TesterTests   `json:"created_tests,omitempty"`
    InitialRunResult     *TestRun       `json:"initial_run,omitempty"`      // RED confirmation
    ValidationRunResult  *TestRun       `json:"validation_run,omitempty"`   // GREEN check
    PendingTestUpdates   []string       `json:"pending_updates,omitempty"`
}
```

##### Handoff Flow
- [ ] Engineer detects 95% context usage
- [ ] Engineer builds `EngineerHandoffState` (original prompt, accomplished, remaining, files)
- [ ] Engineer sends `HANDOFF_REQUEST` to Architect (via Guide)
- [ ] Architect examines handoff state
- [ ] Architect adjusts workflow if needed
- [ ] Architect sends `CREATE_PIPELINE_WITH_STATE` to Orchestrator (via Guide)
- [ ] Orchestrator creates new Pipeline with inherited state
- [ ] Inspector and Tester build their handoff states
- [ ] Old Pipeline transfers combined E+I+T state atomically
- [ ] New Pipeline receives state, starts execution
- [ ] Orchestrator closes old Pipeline
- [ ] Handoff state persisted to Archivalist

```
Handoff Flow:
Engineer (95%) ──────────────────────────────────────────────────────────────►
    │
    ▼
[Build Handoff State]
    │ - Original prompt
    │ - Accomplished tasks
    │ - Files changed
    │ - Remaining tasks
    │ - Context notes
    │
    ▼
[HANDOFF_REQUEST] ──► Guide ──► Architect
                                   │
                                   ▼
                          [Examine state]
                          [Adjust workflow if needed]
                                   │
                                   ▼
               [CREATE_PIPELINE_WITH_STATE] ──► Guide ──► Orchestrator
                                                            │
                                                            ▼
                                                    [Create new pipeline]
                                                    [Transfer E+I+T state]
                                                    [Close old pipeline]
                                                            │
                                                            ▼
                                                    [HANDOFF_COMPLETE] ──► Architect ──► Engineer
```

##### Handoff Message Types
- [ ] `HANDOFF_REQUEST` - Engineer → Guide → Architect
- [ ] `CREATE_PIPELINE_WITH_STATE` - Architect → Guide → Orchestrator
- [ ] `HANDOFF_COMPLETE` - Orchestrator → Guide → Architect → Engineer
- [ ] `HANDOFF_FAILED` - Any step can fail with reason

##### Retry and Fallback Logic
- [ ] Retry `HANDOFF_REQUEST` up to 3 times with exponential backoff
- [ ] If retries fail, fallback: summarize → Archivalist → compact locally
- [ ] Handoff failure does NOT lose work (fallback ensures persistence)

##### Handoff Chaining & User Control
- [ ] Handoffs can chain infinitely (Pipeline A → B → C → ...)
- [ ] `HandoffIndex` tracks chain depth
- [ ] User can trigger handoff manually via `/task <name> handoff`
- [ ] User can stop handoff chain via `/task <name> stop_handoff`
- [ ] Each handoff increments index for traceability
- [ ] User-triggered handoff uses same flow as automatic (Engineer 95%)

##### PipelineManager Handoff Methods
- [ ] `CreateWithState(ctx, cfg, handoffState) (*Pipeline, error)` - create pipeline with inherited state
- [ ] `InitiateHandoff(ctx, oldPipelineID) (*PipelineHandoff, error)` - start handoff process
- [ ] `CompleteHandoff(ctx, handoff *PipelineHandoff) error` - finalize handoff
- [ ] `CancelHandoff(ctx, handoff *PipelineHandoff) error` - cancel in-progress handoff

```go
// Extended PipelineManager interface
type PipelineManager interface {
    // ... existing methods ...

    // Handoff methods
    CreateWithState(ctx context.Context, cfg CreatePipelineConfig, state *PipelineHandoff) (*Pipeline, error)
    InitiateHandoff(ctx context.Context, oldPipelineID PipelineID) (*PipelineHandoff, error)
    CompleteHandoff(ctx context.Context, handoff *PipelineHandoff) error
    CancelHandoff(ctx context.Context, handoff *PipelineHandoff) error
    GetHandoffHistory(pipelineID PipelineID) ([]*PipelineHandoff, error)
}
```

**Tests:**

**TDD Pipeline Flow Tests:**
- [ ] Create pipeline, verify all three agents instantiated (Worker, Inspector, Tester)
- [ ] Execute pipeline, verify TDD flow: Inspector → Tester → Worker → Validate
- [ ] Test Phase 1: Inspector produces InspectorCriteria with success criteria, quality gates
- [ ] Test Phase 2: Tester receives criteria and creates tests that FAIL (RED)
- [ ] Test Phase 2: Verify initial test run fails (no implementation yet)
- [ ] Test Phase 3: Worker receives tests + criteria and implements (GREEN)
- [ ] Test Phase 4: Both Inspector AND Tester validate in parallel
- [ ] Test completion: Only when BOTH Inspector.pass AND Tester.pass
- [ ] Test loop: When Inspector fails but Tester passes, loop to Phase 1
- [ ] Test loop: When Tester fails but Inspector passes, loop to Phase 1
- [ ] Test loop: When BOTH fail, loop with combined feedback
- [ ] Test max loops enforcement (default: 3)
- [ ] Test user override (ignore_inspector bypasses Inspector validation)
- [ ] Test user override (ignore_tester bypasses Tester validation)

**Pipeline Interaction Tests:**
- [ ] Test /task command routing through Guide
- [ ] Test concurrent pipelines within session
- [ ] Test pipeline cancellation mid-phase

**Handoff Tests (TDD-Aware):**
- [ ] Test handoff trigger at Worker 95% context
- [ ] Test handoff preserves TDD phase (resumes at correct phase)
- [ ] Test handoff preserves loop count
- [ ] Test handoff state bundling (Worker+Inspector+Tester combined)
- [ ] Test handoff flow: Worker → Guide → Architect → Guide → Orchestrator
- [ ] Test handoff retry logic (3 attempts, exponential backoff)
- [ ] Test handoff fallback (summarize → Archivalist → compact)
- [ ] Test handoff chaining (A → B → C)
- [ ] Test user-triggered handoff (/task <name> handoff)
- [ ] Test user stop handoff (/task <name> stop_handoff)
- [ ] Test handoff state persistence to Archivalist

---

## Phase 3: Planning Agent

**Goal**: Build the user-facing planning agent with comprehensive skills.

**Dependencies**: Phase 2 complete

### 3.1 Architect (Primary User Agent)

User's main interface. Creates DAGs from abstract requests.

**Files to create:**
- `agents/architect/types.go`
- `agents/architect/planner.go`
- `agents/architect/dag_builder.go`
- `agents/architect/user_interface.go`
- `agents/architect/interrupt_handler.go`
- `agents/architect/clarification.go`
- `agents/architect/architect.go`
- `agents/architect/routing.go`
- `agents/architect/skills.go`
- `agents/architect/tools.go`
- `agents/architect/hooks.go`
- `agents/architect/architect_test.go`

**Acceptance Criteria:**

#### Request Intake (via Guide only)
- [ ] Accept user requests from Guide (implementation requests)
- [ ] Accept Academic "research paper" from Guide (research-informed requests)
- [ ] NEVER receive direct user input - all paths flow through Guide

#### Request Decomposition
- [ ] Break apart request into discrete components
- [ ] Identify explicit requirements
- [ ] Identify implicit assumptions (state them explicitly)
- [ ] Identify ambiguities requiring resolution
- [ ] Identify unknowns (missing context)
- [ ] Identify gaps (information needed but not provided)

#### Context Gathering (CRITICAL: via Guide, NOT directly to agents)
- [ ] Transform gaps into queries
- [ ] Submit ALL queries to Guide for routing (Guide routes to appropriate agent)
- [ ] Receive responses through Guide
- [ ] Integrate responses into understanding
- [ ] Iterate until all resolvable gaps are filled
- [ ] Query Librarian (via Guide) for codebase context
- [ ] Query Archivalist (via Guide) for past patterns
- [ ] Query Academic (via Guide) for research (when needed)

#### User Clarification (LAST RESORT)
- [ ] ONLY ask user after exhausting Librarian, Academic, and Archivalist
- [ ] ONLY for information that cannot be determined from agents
- [ ] ONLY for genuine ambiguities (not laziness)
- [ ] When asking, explain what was already checked
- [ ] Ask specific, actionable questions

#### Challenge User (when warranted)
- [ ] Raise concerns when design seems flawed
- [ ] Point out contradictions with codebase patterns (from Librarian)
- [ ] Suggest alternatives when approach has known failure patterns (from Archivalist)
- [ ] State concerns with evidence from agents
- [ ] Propose alternatives if available
- [ ] Accept user's final decision after presenting facts

#### Planning
- [ ] Generate implementation plan with tasks
- [ ] Convert plan to DAG with explicit execution order
- [ ] **Generate human-readable task names as DAG node keys** (e.g., `create_dashboard`, `setup_auth_middleware`)
- [ ] Task names must be unique within DAG, descriptive, snake_case
- [ ] Edge case analysis (empty inputs, boundaries, concurrency)
- [ ] Failure mode analysis (what could go wrong)
- [ ] Mitigation proposals for identified risks
- [ ] Reference specific files (from Librarian context)
- [ ] Follow existing patterns (from Librarian context)
- [ ] Avoid known pitfalls (from Archivalist context)

#### User Interaction
- [ ] Present plan to user for approval
- [ ] Handle user modifications to plan
- [ ] Handle user interruptions during execution
- [ ] Route Engineer clarifications to user
- [ ] Deliver status updates during execution
- [ ] Deliver completion summary

#### Fix Workflows
- [ ] Create fix DAGs from Inspector corrections
- [ ] Create fix DAGs from Tester corrections
- [ ] Minimal fix scope (don't re-do working parts)

#### Bus Integration
- [ ] Subscribe to `architect.requests` (session-scoped)
- [ ] Publish to `architect.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Plan`, `Status`, `Help`, `Interrupt`
- [ ] Support domains: `System`, `Agents`, `Workflow`

#### Architect Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// plan - Create implementation plan
skills.NewSkill("plan").
    Description("Create an implementation plan for a request").
    Domain("planning").
    Keywords("plan", "implement", "build", "create", "add").
    StringParam("request", "What to implement", true).
    BoolParam("with_research", "Include Academic research", false)

// status - Get current status
skills.NewSkill("status").
    Description("Get current execution status and progress").
    Domain("status").
    Keywords("status", "progress", "where", "what").
    BoolParam("detailed", "Include detailed breakdown", false)

// approve_plan - Approve a plan
skills.NewSkill("approve_plan").
    Description("Approve the current plan and begin execution").
    Domain("planning").
    Keywords("approve", "ok", "yes", "proceed", "go").
    StringParam("plan_id", "Plan ID (optional)", false)

// modify_plan - Modify the plan
skills.NewSkill("modify_plan").
    Description("Modify the current plan").
    Domain("planning").
    Keywords("modify", "change", "also", "add", "remove").
    StringParam("modification", "What to change", true)
```

**Extended Skills (loaded on demand):**
```go
// interrupt - Interrupt execution
skills.NewSkill("interrupt").
    Description("Interrupt the current execution").
    Domain("control").
    Keywords("stop", "wait", "pause", "interrupt", "hold").
    StringParam("reason", "Why interrupting", false)

// clarify - Request clarification from user
skills.NewSkill("clarify").
    Description("Request clarification from the user").
    Domain("communication").
    Keywords("clarify", "question", "unclear", "which").
    StringParam("question", "What needs clarification", true).
    ArrayParam("options", "Possible options to choose from", false)

// summarize - Summarize completed work
skills.NewSkill("summarize").
    Description("Generate a summary of completed work").
    Domain("reporting").
    Keywords("summarize", "summary", "done", "complete", "finished").
    StringParam("scope", "What to summarize", false)

// analyze_risk - Analyze implementation risks
skills.NewSkill("analyze_risk").
    Description("Analyze risks in the implementation plan").
    Domain("planning").
    Keywords("risk", "danger", "problem", "issue", "concern").
    StringParam("plan_id", "Plan ID (optional)", false)

// query_context - Query for context
skills.NewSkill("query_context").
    Description("Query Librarian, Archivalist, or Academic for context").
    Domain("context").
    Keywords("context", "find", "check", "search").
    StringParam("query", "What to look for", true).
    EnumParam("source", "Where to look", []string{"librarian", "archivalist", "academic", "all"}, false)

// create_fix - Create fix workflow
skills.NewSkill("create_fix").
    Description("Create a fix workflow for corrections").
    Domain("planning").
    Keywords("fix", "correct", "repair", "address").
    ObjectParam("corrections", "List of corrections", true)
```

**Read-Only Bash Skills (for context gathering):**
```go
// find_files - Find files for planning context
skills.NewSkill("find_files").
    Description("Find files using the find command for planning context").
    Domain("search").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// grep_search - Search file contents
skills.NewSkill("grep_search").
    Description("Search file contents using regex for planning context").
    Domain("search").
    Keywords("grep", "regex", "search", "content").
    StringParam("pattern", "Regex pattern", true).
    StringParam("path", "Search path", false).
    BoolParam("case_insensitive", "Case insensitive search", false)

// glob_search - Find files by glob pattern
skills.NewSkill("glob_search").
    Description("Find files matching a glob pattern").
    Domain("search").
    Keywords("glob", "find", "files", "pattern").
    StringParam("pattern", "Glob pattern (e.g., **/*.go)", true).
    StringParam("path", "Base path", false)

// cat_file - Read file contents
skills.NewSkill("cat_file").
    Description("Display file contents for planning context").
    Domain("read").
    Keywords("cat", "read", "show", "display", "file").
    StringParam("path", "File path", true).
    IntParam("from_line", "Start from line", false).
    IntParam("to_line", "End at line", false)

// run_readonly_bash - Run read-only bash commands
skills.NewSkill("run_readonly_bash").
    Description("Run non-destructive bash commands for context gathering").
    Domain("bash").
    Keywords("bash", "shell", "command", "read").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)

// Read-only command whitelist for Architect (excludes git write operations)
var ArchitectReadOnlyCommands = []string{
    `^cat .*`,
    `^head .*`,
    `^tail .*`,
    `^grep .*`,
    `^find .* -type [fd].*`,
    `^ls .*`,
    `^wc .*`,
    `^diff .*`,
    `^tree .*`,
    `^file .*`,
    `^stat .*`,
    `^du .*`,
    `^pwd$`,
    `^env$`,
    `^which .*`,
    `^type .*`,
    // Note: Excludes git commands (handled by Librarian)
    // Note: Excludes github/remote operations (handled by Librarian)
}
```

**Orchestrator Coordination Skills:**
```go
// schedule_work - Schedule work with orchestrator
skills.NewSkill("schedule_work").
    Description("Schedule work items with the orchestrator for execution").
    Domain("orchestration").
    Keywords("schedule", "queue", "dispatch", "orchestrator").
    ObjectParam("dag", "DAG definition to schedule", true).
    EnumParam("priority", "Execution priority", []string{"critical", "high", "normal", "low"}, false).
    ObjectParam("constraints", "Scheduling constraints (resources, timing)", false)

// adjust_workflow - Adjust executing workflow
skills.NewSkill("adjust_workflow").
    Description("Adjust or modify a currently executing workflow").
    Domain("orchestration").
    Keywords("adjust", "modify", "workflow", "change").
    StringParam("dag_id", "DAG ID to adjust", true).
    EnumParam("action", "Adjustment action", []string{"pause", "resume", "cancel", "modify", "reprioritize"}, true).
    ObjectParam("modifications", "Specific modifications (for 'modify' action)", false)

// signal_orchestrator - Send signal to orchestrator
skills.NewSkill("signal_orchestrator").
    Description("Send control signal to the orchestrator").
    Domain("orchestration").
    Keywords("signal", "orchestrator", "control", "notify").
    EnumParam("signal", "Signal type", []string{"pause_all", "resume_all", "cancel_all", "status_request", "health_check"}, true).
    StringParam("session_id", "Target session (optional)", false)

// get_workflow_status - Get detailed workflow status
skills.NewSkill("get_workflow_status").
    Description("Get detailed status of workflow execution").
    Domain("orchestration").
    Keywords("workflow", "status", "progress", "dag").
    StringParam("dag_id", "DAG ID (optional, defaults to current)", false).
    BoolParam("include_node_details", "Include per-node status", false)
```

**Superpowers Skills (from superpowers methodology):**

These skills implement proven methodologies from the superpowers project:

```go
// brainstorming_3phase - Three-phase brainstorming for complex problems
// Source: superpowers/brainstorming
skills.NewSkill("brainstorming_3phase").
    Description("Three-phase brainstorming: divergent exploration, constraint identification, convergent synthesis").
    Domain("planning").
    Keywords("brainstorm", "design", "approach", "options").
    StringParam("problem", "Problem statement to brainstorm", true).
    IntParam("exploration_breadth", "Number of initial approaches to explore", false)

// writing_plans_granular - Granular plan writing with proper detail level
// Source: superpowers/writing-plans
skills.NewSkill("writing_plans_granular").
    Description("Write plans with appropriate granularity - not too abstract, not too detailed").
    Domain("planning").
    Keywords("plan", "write", "detail", "granular").
    StringParam("objective", "What the plan should achieve", true).
    EnumParam("granularity", "Detail level", []string{"high_level", "moderate", "detailed"}, false)

// dispatching_parallel_agents - Domain-based parallel pipeline dispatch
// Source: superpowers/dispatching-parallel-agents
skills.NewSkill("dispatching_parallel_agents").
    Description("Dispatch multiple pipelines in parallel based on domain independence").
    Domain("orchestration").
    Keywords("parallel", "dispatch", "concurrent", "domain").
    ObjectParam("tasks", "Tasks to potentially parallelize", true).
    BoolParam("analyze_domains", "Analyze domain independence", false)

// using_git_worktrees - Isolated workspace creation
// Source: superpowers/using-git-worktrees
skills.NewSkill("using_git_worktrees").
    Description("Create isolated git worktrees for safe parallel development").
    Domain("git").
    Keywords("worktree", "isolate", "branch", "parallel").
    StringParam("branch_name", "Branch name for worktree", true).
    StringParam("worktree_path", "Path for worktree (optional)", false)

// finishing_development_branch - Branch completion with options
// Source: superpowers/finishing-a-development-branch
skills.NewSkill("finishing_development_branch").
    Description("Complete development branch: verify tests, present integration options").
    Domain("git").
    Keywords("finish", "branch", "merge", "pr", "complete").
    StringParam("branch_name", "Branch to finish", true).
    EnumParam("action", "Completion action", []string{"merge_local", "create_pr", "keep", "discard"}, false)

// requesting_code_review - Structured code review request
// Source: superpowers/requesting-code-review
skills.NewSkill("requesting_code_review").
    Description("Request code review with structured context and focus areas").
    Domain("review").
    Keywords("review", "request", "feedback", "code").
    StringParam("pr_or_branch", "PR number or branch name", true).
    ArrayParam("focus_areas", "Specific areas to review", false)
```

#### Architect Tools
```go
var ArchitectTools = []ToolDefinition{
    // Core planning
    {Name: "architect_plan", Skill: "plan"},
    {Name: "architect_status", Skill: "status"},
    {Name: "architect_approve_plan", Skill: "approve_plan"},
    {Name: "architect_modify_plan", Skill: "modify_plan"},
    // Context & communication
    {Name: "architect_clarify", Skill: "clarify"},
    {Name: "architect_query_context", Skill: "query_context"},
    // Control
    {Name: "architect_interrupt", Skill: "interrupt"},
    {Name: "architect_summarize", Skill: "summarize"},
    {Name: "architect_analyze_risk", Skill: "analyze_risk"},
    {Name: "architect_create_fix", Skill: "create_fix"},
    // Orchestrator coordination
    {Name: "architect_schedule_work", Skill: "schedule_work"},
    {Name: "architect_adjust_workflow", Skill: "adjust_workflow"},
    {Name: "architect_signal_orchestrator", Skill: "signal_orchestrator"},
    {Name: "architect_get_workflow_status", Skill: "get_workflow_status"},
    // Superpowers methodology (Architect-specific)
    {Name: "architect_brainstorm", Skill: "brainstorming_3phase"},
    {Name: "architect_write_plan", Skill: "writing_plans_granular"},
    {Name: "architect_dispatch_parallel", Skill: "dispatching_parallel_agents"},
    {Name: "architect_worktree", Skill: "using_git_worktrees"},
    {Name: "architect_finish_branch", Skill: "finishing_development_branch"},
    {Name: "architect_request_review", Skill: "requesting_code_review"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Architect Memory Management

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact)

**Files to create:**
- `agents/architect/memory.go`
- `agents/architect/workflow_summary.go`

**Acceptance Criteria:**

##### Context Monitoring
- [ ] Track context window usage percentage
- [ ] Trigger workflow summary at 85%
- [ ] Trigger compaction at 95%

##### Workflow Summary (at 85%)
- [ ] `ArchitectWorkflowSummary` struct (retrievable/parseable by other Architects):
  - [ ] OriginalRequest (verbatim user request)
  - [ ] ImplementationPlan (current plan state)
  - [ ] CurrentDAG (DAG summary with node counts, layers)
  - [ ] CompletedTasks (tasks finished with results)
  - [ ] PendingTasks (tasks not yet started)
  - [ ] BlockedTasks (tasks waiting on dependencies)
  - [ ] ArchitecturalDecisions (decisions made and rationale)
  - [ ] Risks (identified risks)
  - [ ] Assumptions (assumptions made)
- [ ] Submit to Archivalist with category `architect_workflow`

```go
type ArchitectWorkflowSummary struct {
    Timestamp              time.Time          `json:"timestamp"`
    SessionID              string             `json:"session_id"`
    ContextUsage           float64            `json:"context_usage"`
    OriginalRequest        string             `json:"original_request"`
    ImplementationPlan     string             `json:"implementation_plan"`
    CurrentDAG             *DAGSummary        `json:"current_dag"`
    CompletedTasks         []TaskSummary      `json:"completed_tasks"`
    PendingTasks           []TaskSummary      `json:"pending_tasks"`
    BlockedTasks           []TaskSummary      `json:"blocked_tasks"`
    ArchitecturalDecisions []Decision         `json:"architectural_decisions"`
    Risks                  []Risk             `json:"risks"`
    Assumptions            []string           `json:"assumptions"`
}
```

##### Compaction at 95%
- [ ] Create final workflow summary before compacting
- [ ] Clear verbose conversation history from context
- [ ] Retain workflow state and decisions
- [ ] Target: ~30% context usage after compaction

##### Pipeline Handoff Handling
- [ ] Receive `HANDOFF_REQUEST` from Engineer (via Guide)
- [ ] Examine handoff state, adjust workflow if needed
- [ ] Send `CREATE_PIPELINE_WITH_STATE` to Orchestrator (via Guide)
- [ ] Receive `HANDOFF_COMPLETE` confirmation

#### Architect Hooks
- [ ] Pre-plan hook: load session context from Archivalist
- [ ] Post-plan hook: store plan in Archivalist
- [ ] Pre-execute hook: confirm user approval
- [ ] Post-execute hook: update session state
- [ ] Pre-interrupt hook: pause current work gracefully
- [ ] Post-complete hook: generate and store summary
- [ ] Context-threshold hook: generate workflow summary at 85%
- [ ] Context-threshold hook: compact at 95%
- [ ] Handoff hook: handle pipeline handoff requests

**Tests:**
- [ ] Generate plan from request
- [ ] Verify DAG structure
- [ ] Test user modification flow
- [ ] Test interrupt handling
- [ ] Test clarification routing
- [ ] Test workflow summary generation at 85%
- [ ] Test compaction at 95%
- [ ] Test pipeline handoff request handling

---

## Phase 4: Quality Agents

**Goal**: Build validation and testing agents with comprehensive skills.

**Dependencies**: Phase 3 complete

**Parallelization**: Items 4.1 and 4.2 can execute in parallel.

### 4.1 Inspector (Code Validator)

Defines success criteria (TDD Phase 1) and validates implementation correctness (TDD Phase 4).

**CRITICAL**: Inspector operates in TWO TDD phases within Pipeline:
1. **Phase 1 - Criteria Definition**: Analyze task and define success criteria, quality gates, constraints BEFORE tests are written
2. **Phase 4 - Validation**: Verify Worker output satisfies criteria defined in Phase 1

**CRITICAL**: Inspector also operates in TWO modes across sessions:
1. **Pipeline-internal mode**: Task-specific criteria + validation within a Pipeline
2. **Session-wide mode**: Full validation after all Pipelines complete, feedback through Architect

**Files to create:**
- `agents/inspector/types.go`
- `agents/inspector/validators.go`
- `agents/inspector/static_analysis.go`
- `agents/inspector/pattern_matcher.go`
- `agents/inspector/deep_analyzer.go`
- `agents/inspector/inspector.go`
- `agents/inspector/routing.go`
- `agents/inspector/skills.go`
- `agents/inspector/tools.go`
- `agents/inspector/hooks.go`
- `agents/inspector/pipeline_mode.go` (NEW - pipeline-internal validation)
- `agents/inspector/inspector_test.go`

**Acceptance Criteria:**

#### Dual-Mode Operation
- [ ] `InspectorMode` enum: `PipelineInternal`, `SessionWide`
- [ ] Pipeline-internal: receive from PipelineBus, operate in TDD phases
- [ ] Session-wide: receive from Guide/Bus, send corrections through Architect
- [ ] Mode determined by instantiation context (Pipeline vs standalone)

#### TDD Phase 1 - Criteria Definition (`criteria.go`)
- [ ] Analyze task prompt and constraints to define success criteria
- [ ] Generate `InspectorCriteria` struct containing:
  - [ ] `SuccessCriteria[]` - What must be true for task to be "done"
  - [ ] `QualityGates[]` - Thresholds that must be met (coverage, complexity, etc.)
  - [ ] `Constraints[]` - Security, performance, accessibility requirements
- [ ] Criteria must be VERIFIABLE (testable by Tester or automated tools)
- [ ] Send `CRITERIA_READY` message to Tester via PipelineBus
- [ ] Skills used: `define_code_criteria` (Engineer pipeline), `define_ui_criteria` (Designer pipeline)

```go
// TDD Phase 1: Define what "done" means
func (i *Inspector) DefineCriteria(ctx context.Context, bus *PipelineBus, taskPrompt string) (*InspectorCriteria, error) {
    criteria := &InspectorCriteria{
        TaskID:          bus.PipelineID(),
        SuccessCriteria: i.analyzeTaskForCriteria(taskPrompt),
        QualityGates:    i.determineQualityGates(taskPrompt, bus.Context().WorkerType),
        Constraints:     i.identifyConstraints(taskPrompt),
        CreatedAt:       time.Now(),
    }

    // Criteria MUST be verifiable
    for _, c := range criteria.SuccessCriteria {
        if !c.Verifiable {
            return nil, fmt.Errorf("criterion %q is not verifiable", c.Description)
        }
    }

    // Send to Tester (Phase 1 → Phase 2)
    return criteria, bus.SendCriteria(criteria)
}
```

#### TDD Phase 4 - Validation (`pipeline_mode.go`)
- [ ] Receive Worker output after implementation (Phase 3 complete)
- [ ] Validate against criteria defined in Phase 1
- [ ] Run task-specific validation (lint, format, type-check on modified files only)
- [ ] Check task compliance criteria from `InspectorCriteria`
- [ ] Send `InspectorResult` to Pipeline via PipelineBus
- [ ] Pass condition: ALL success criteria met AND ALL quality gates passed
- [ ] NO routing through Guide or Architect
- [ ] Quick validation (< 5 seconds per task)

```go
// Pipeline-internal validation flow
func (i *Inspector) ValidateInPipeline(ctx context.Context, bus *PipelineBus, result *EngineerResult) (*InspectorFeedback, error) {
    // Validate only the files modified by this task
    issues := i.validateFiles(result.ModifiedFiles)

    // Check task-specific compliance
    compliance := i.checkCompliance(result, bus.Context().ComplianceCriteria)

    feedback := &InspectorFeedback{
        Loop:      bus.Context().InspectorLoops,
        Timestamp: time.Now(),
        Issues:    append(issues, compliance...),
        Passed:    len(issues) == 0 && len(compliance) == 0,
    }

    // Send directly to Engineer (NOT through Guide)
    return feedback, bus.SendInspectorFeedback(feedback)
}
```

#### Per-Task Validation (Session-Wide Mode)
- [ ] Naming conventions check
- [ ] File organization check
- [ ] Import ordering check
- [ ] Error handling patterns check
- [ ] Matches existing codebase patterns (via Librarian)
- [ ] Quick validation (< 5 seconds per task)

#### Full Validation
- [ ] Implementation vs. proposal compliance (Academic output)
- [ ] All components connected and wired correctly
- [ ] No dead code paths
- [ ] Initialization order correct
- [ ] Cleanup/shutdown handled properly
- [ ] API contracts satisfied

#### Deep Analysis
- [ ] Edge cases (empty inputs, boundary conditions, concurrent access)
- [ ] Race conditions (shared state, lock ordering, atomic operations)
- [ ] Memory leaks (unclosed resources, goroutine leaks)
- [ ] Swallowed errors (ignored returns, empty catch blocks)
- [ ] Security issues (injection, path traversal, etc.)

#### Corrections
- [ ] Generate detailed corrections list for Architect
- [ ] Severity levels: Critical, High, Medium, Low, Info
- [ ] Suggested fixes for each issue
- [ ] File and line references

#### User Override
- [ ] Support `USER_OVERRIDE` messages to ignore issues
- [ ] Track overridden issues separately
- [ ] Include overrides in final summary

#### Bus Integration
- [ ] Subscribe to `inspector.requests` (session-scoped)
- [ ] Publish to `inspector.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Check`, `Validate`

#### Inspector Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// validate_task - Validate a single task result
skills.NewSkill("validate_task").
    Description("Validate the output of a single task").
    Domain("validation").
    Keywords("validate", "check", "task", "result").
    ObjectParam("task_result", "Task result to validate", true)

// validate_full - Full implementation validation
skills.NewSkill("validate_full").
    Description("Perform full validation of the implementation").
    Domain("validation").
    Keywords("validate", "full", "complete", "all").
    ObjectParam("results", "All task results", true).
    ObjectParam("proposal", "Original proposal/plan", true)

// get_issues - Get current issues
skills.NewSkill("get_issues").
    Description("Get all issues found during validation").
    Domain("validation").
    Keywords("issues", "problems", "errors", "warnings").
    EnumParam("severity", "Filter by severity", []string{"critical", "high", "medium", "low", "all"}, false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_patterns - Check pattern compliance
skills.NewSkill("analyze_patterns").
    Description("Check code against codebase patterns").
    Domain("analysis").
    Keywords("patterns", "conventions", "style", "compliance").
    StringParam("file", "File to analyze", true)

// analyze_concurrency - Check for race conditions
skills.NewSkill("analyze_concurrency").
    Description("Analyze code for concurrency issues").
    Domain("analysis").
    Keywords("race", "concurrent", "lock", "mutex", "atomic").
    StringParam("file", "File to analyze", true)

// analyze_resources - Check for resource leaks
skills.NewSkill("analyze_resources").
    Description("Analyze code for resource leaks").
    Domain("analysis").
    Keywords("leak", "resource", "close", "defer", "cleanup").
    StringParam("file", "File to analyze", true)

// analyze_errors - Check error handling
skills.NewSkill("analyze_errors").
    Description("Analyze error handling patterns").
    Domain("analysis").
    Keywords("error", "handling", "swallowed", "ignored").
    StringParam("file", "File to analyze", true)

// override_issue - Mark issue as overridden
skills.NewSkill("override_issue").
    Description("Mark an issue as user-overridden").
    Domain("validation").
    Keywords("override", "ignore", "skip", "accept").
    StringParam("issue_id", "Issue ID to override", true).
    StringParam("reason", "Reason for override", false)
```

**Superpowers Skills (from superpowers methodology):**
```go
// define_code_criteria - TDD Phase 1 criteria definition (Engineer pipeline)
// Source: superpowers/subagent-driven-development (two-stage validation)
skills.NewSkill("define_code_criteria").
    Description("Define success criteria for code implementation (TDD Phase 1)").
    Domain("criteria").
    Keywords("criteria", "success", "define", "requirements").
    StringParam("task_prompt", "Task to define criteria for", true).
    ArrayParam("constraints", "Additional constraints", false)

// define_ui_criteria - TDD Phase 1 criteria definition (Designer pipeline)
// Source: superpowers/subagent-driven-development (two-stage validation)
skills.NewSkill("define_ui_criteria").
    Description("Define success criteria for UI implementation (TDD Phase 1)").
    Domain("criteria").
    Keywords("criteria", "success", "define", "ui", "requirements").
    StringParam("task_prompt", "Task to define criteria for", true).
    ArrayParam("a11y_requirements", "Accessibility requirements", false)

// receiving_code_review - Process code review feedback
// Source: superpowers/receiving-code-review
skills.NewSkill("receiving_code_review").
    Description("Process external code review feedback with technical evaluation").
    Domain("review").
    Keywords("review", "feedback", "receive", "evaluate").
    ObjectParam("feedback", "Code review feedback to process", true).
    BoolParam("verify_first", "Verify against codebase before implementing", false)

// verification_before_completion - Multi-step verification
// Source: superpowers/verification-before-completion
skills.NewSkill("verification_before_completion").
    Description("Perform thorough verification before marking task complete").
    Domain("validation").
    Keywords("verify", "complete", "done", "check").
    ObjectParam("task_result", "Result to verify", true).
    ArrayParam("verification_steps", "Steps to verify", false)

// subagent_two_stage_validation - Two-stage validation pattern
// Source: superpowers/subagent-driven-development
skills.NewSkill("subagent_two_stage_validation").
    Description("Two-stage validation: define expectations, then verify results").
    Domain("validation").
    Keywords("two-stage", "validate", "subagent", "expectations").
    ObjectParam("expectations", "What was expected", true).
    ObjectParam("results", "What was produced", true)
```

**Code Quality Execution (dynamically configurable):**
```go
// run_linter - Run linter for validation
skills.NewSkill("run_linter").
    Description("Run linter on code to find style issues").
    Domain("quality").
    Keywords("lint", "linter", "eslint", "golangci", "ruff", "clippy").
    StringParam("path", "Path to lint", true).
    StringParam("linter", "Specific linter (auto-detect by language)", false).
    ArrayParam("rules", "Specific rules to check", false)

// run_formatter_check - Check formatting without modifying
skills.NewSkill("run_formatter_check").
    Description("Check code formatting without modifying files").
    Domain("quality").
    Keywords("format", "check", "style", "prettier", "gofmt", "black").
    StringParam("path", "Path to check", true).
    StringParam("formatter", "Specific formatter (auto-detect by language)", false)

// run_type_checker - Run static type checker
skills.NewSkill("run_type_checker").
    Description("Run static type checker for type errors").
    Domain("quality").
    Keywords("type", "check", "typescript", "mypy", "pyright").
    StringParam("path", "Path to check", true).
    StringParam("checker", "Specific type checker (auto-detect by language)", false).
    BoolParam("strict", "Enable strict mode", false)

// query_lsp_diagnostics - Get LSP diagnostics
skills.NewSkill("query_lsp_diagnostics").
    Description("Get diagnostics from language server").
    Domain("quality").
    Keywords("lsp", "diagnostics", "errors", "warnings").
    StringParam("path", "File path", true).
    StringParam("lsp", "Specific LSP (auto-detect by language)", false)

// query_lsp_hover - Get hover information from LSP
skills.NewSkill("query_lsp_hover").
    Description("Get hover documentation from language server").
    Domain("quality").
    Keywords("lsp", "hover", "docs", "type").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true)

// query_lsp_references - Find all references via LSP
skills.NewSkill("query_lsp_references").
    Description("Find all references to a symbol via LSP").
    Domain("quality").
    Keywords("lsp", "references", "usages", "find").
    StringParam("path", "File path", true).
    IntParam("line", "Line number", true).
    IntParam("column", "Column number", true)
```

**Environment Skills (for running quality tools):**
```go
// ensure_venv - Ensure virtual environment exists
skills.NewSkill("ensure_venv").
    Description("Ensure virtual environment exists for running tools").
    Domain("environment").
    Keywords("venv", "environment", "setup").
    StringParam("path", "Project path", true).
    EnumParam("type", "Environment type", []string{"auto", "python", "node"}, false)

// install_tool - Install quality tool in environment
skills.NewSkill("install_tool").
    Description("Install a quality tool in the environment").
    Domain("environment").
    Keywords("install", "tool", "linter", "formatter").
    StringParam("tool", "Tool name", true).
    StringParam("version", "Tool version (optional)", false)

// list_available_tools - List available quality tools
skills.NewSkill("list_available_tools").
    Description("List available quality tools for detected languages").
    Domain("environment").
    Keywords("list", "tools", "available", "linters", "formatters").
    StringParam("path", "Project path", true)

// find_files - Find files for validation
skills.NewSkill("find_files").
    Description("Find files to validate using the find command").
    Domain("search").
    Keywords("find", "locate", "files", "search").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)
```

**Tool Configuration Registry (dynamically loaded):**
```go
// Inspector uses the same ToolRegistry as Librarian but for execution
type InspectorToolConfig struct {
    Name        string
    Command     string
    CheckOnly   string   // Command for check-only mode
    Languages   []string // Supported languages
    ConfigFiles []string // Config file names to look for
    Severity    string   // Default severity for issues
}

// Example configurations
var InspectorTools = map[string]InspectorToolConfig{
    "eslint": {
        Name: "eslint",
        Command: "eslint --format json",
        CheckOnly: "eslint --format json",
        Languages: []string{"javascript", "typescript"},
        ConfigFiles: []string{".eslintrc", ".eslintrc.js", ".eslintrc.json"},
        Severity: "warning",
    },
    "golangci-lint": {
        Name: "golangci-lint",
        Command: "golangci-lint run --out-format json",
        CheckOnly: "golangci-lint run --out-format json",
        Languages: []string{"go"},
        ConfigFiles: []string{".golangci.yml", ".golangci.yaml"},
        Severity: "warning",
    },
    "ruff": {
        Name: "ruff",
        Command: "ruff check --output-format json",
        CheckOnly: "ruff check --output-format json",
        Languages: []string{"python"},
        ConfigFiles: []string{"ruff.toml", "pyproject.toml"},
        Severity: "warning",
    },
    "mypy": {
        Name: "mypy",
        Command: "mypy --output json",
        CheckOnly: "mypy --output json",
        Languages: []string{"python"},
        ConfigFiles: []string{"mypy.ini", "pyproject.toml"},
        Severity: "error",
    },
    "pyright": {
        Name: "pyright",
        Command: "pyright --outputjson",
        CheckOnly: "pyright --outputjson",
        Languages: []string{"python"},
        ConfigFiles: []string{"pyrightconfig.json"},
        Severity: "error",
    },
}
```

#### Inspector Tools
```go
var InspectorTools = []ToolDefinition{
    // Core validation
    {Name: "inspector_validate_task", Skill: "validate_task"},
    {Name: "inspector_validate_full", Skill: "validate_full"},
    {Name: "inspector_get_issues", Skill: "get_issues"},
    // Analysis
    {Name: "inspector_analyze_patterns", Skill: "analyze_patterns"},
    {Name: "inspector_analyze_concurrency", Skill: "analyze_concurrency"},
    {Name: "inspector_analyze_resources", Skill: "analyze_resources"},
    {Name: "inspector_analyze_errors", Skill: "analyze_errors"},
    // Code quality execution
    {Name: "inspector_run_linter", Skill: "run_linter"},
    {Name: "inspector_run_formatter_check", Skill: "run_formatter_check"},
    {Name: "inspector_run_type_checker", Skill: "run_type_checker"},
    {Name: "inspector_query_lsp_diagnostics", Skill: "query_lsp_diagnostics"},
    // Environment
    {Name: "inspector_ensure_venv", Skill: "ensure_venv"},
    {Name: "inspector_install_tool", Skill: "install_tool"},
    // Search
    {Name: "inspector_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Inspector Hooks
- [ ] Pre-validate hook: load codebase patterns from Librarian
- [ ] Post-validate hook: store validation results in Archivalist
- [ ] Pre-deep-analysis hook: check if analysis already cached
- [ ] Post-corrections hook: format corrections for Architect

#### Memory Management (Local Compaction)

**Model**: OpenAI Codex 5.2

**NOTE**: Inspector compacts LOCALLY. It does NOT trigger pipeline handoff (only Engineer can).

**Files to add:**
- `agents/inspector/memory.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint to Archivalist
- [ ] At 95% threshold: compact locally (NOT trigger handoff)
- [ ] Checkpoint includes: findings summary, resolved issues, unresolved issues, priorities, fix references
- [ ] If Engineer triggers handoff, Inspector participates in state transfer

```go
// Inspector checkpoint summary (sent to Archivalist at 85%)
type InspectorCheckpointSummary struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    SessionID         string            `json:"session_id"`
    Timestamp         time.Time         `json:"timestamp"`
    ContextUsage      float64           `json:"context_usage"`
    CheckpointIndex   int               `json:"checkpoint_index"`

    // Validation state
    TotalIssuesFound  int               `json:"total_issues_found"`
    ResolvedIssues    []ResolvedIssue   `json:"resolved_issues"`
    UnresolvedIssues  []UnresolvedIssue `json:"unresolved_issues"`
    OverriddenIssues  []OverriddenIssue `json:"overridden_issues"`

    // Priority fixes
    CriticalFixes     []FixReference    `json:"critical_fixes"`
    HighPriorityFixes []FixReference    `json:"high_priority_fixes"`

    // Loop statistics
    LoopsCompleted    int               `json:"loops_completed"`
    AverageLoopTime   time.Duration     `json:"average_loop_time"`
}

type FixReference struct {
    IssueID     string   `json:"issue_id"`
    Severity    string   `json:"severity"`
    FilePath    string   `json:"file_path"`
    LineNumber  int      `json:"line_number"`
    Description string   `json:"description"`
    SuggestedFix string  `json:"suggested_fix"`
}

// Inspector handoff state (when Engineer triggers handoff)
type InspectorHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    CurrentFindings   []Finding         `json:"current_findings"`
    ResolvedByEngineer []ResolvedIssue  `json:"resolved_by_engineer"`
    PendingForEngineer []FixReference   `json:"pending_for_engineer"`
    ValidationHistory []ValidationRun   `json:"validation_history"`
}

func (i *Inspector) checkContextAndManage() error {
    usage := i.getContextUsage()

    if usage >= 0.95 {
        // Compact locally - DO NOT trigger handoff
        return i.compactLocally()
    }

    if usage >= 0.85 {
        // Checkpoint to Archivalist
        return i.submitCheckpoint()
    }

    return nil
}
```

**Tests:**
- [ ] Validate test code with known issues
- [ ] Verify issue detection
- [ ] Test override flow
- [ ] Test corrections generation
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Engineer triggers handoff

### 4.2 Tester (Test Planner & Executor)

Creates tests from criteria (TDD Phase 2 - RED) and validates implementation (TDD Phase 4 - GREEN check).

**CRITICAL**: Tester operates in TWO TDD phases within Pipeline:
1. **Phase 2 - Test Creation (RED)**: Receive criteria from Inspector, create tests that WILL FAIL (no implementation yet)
2. **Phase 4 - Validation (GREEN check)**: Run test suite to verify Worker's implementation makes tests pass

**CRITICAL**: Tester also operates in TWO modes across sessions:
1. **Pipeline-internal mode**: Task-specific test creation + validation within a Pipeline
2. **Session-wide mode**: Full test suite after all Pipelines complete, feedback through Architect

**Files to create:**
- `agents/tester/types.go`
- `agents/tester/planner.go`
- `agents/tester/generator.go`
- `agents/tester/executor.go`
- `agents/tester/analyzer.go`
- `agents/tester/tester.go`
- `agents/tester/routing.go`
- `agents/tester/skills.go`
- `agents/tester/tools.go`
- `agents/tester/hooks.go`
- `agents/tester/pipeline_mode.go` (NEW - pipeline-internal testing)
- `agents/tester/tester_test.go`

**Acceptance Criteria:**

#### Dual-Mode Operation
- [ ] `TesterMode` enum: `PipelineInternal`, `SessionWide`
- [ ] Pipeline-internal: receive from PipelineBus, operate in TDD phases
- [ ] Session-wide: receive from Guide/Bus, send corrections through Architect
- [ ] Mode determined by instantiation context (Pipeline vs standalone)

#### TDD Phase 2 - Test Creation RED (`test_creation.go`)
- [ ] Receive `InspectorCriteria` from Inspector via PipelineBus
- [ ] Generate tests that map to each success criterion
- [ ] Create `TesterTests` struct containing:
  - [ ] `TestFiles[]` - Generated test file paths and test names
  - [ ] `InitialRun` - Initial test execution (MUST FAIL - no implementation yet)
  - [ ] `BasedOnCriteria[]` - Links tests back to criteria IDs
- [ ] Run initial test suite to CONFIRM failure (RED phase verification)
- [ ] If tests pass (unexpected), flag as ERROR - tests are not testing the right thing
- [ ] Send `TESTS_READY` message to Worker via PipelineBus
- [ ] Skills used: `write_unit_test`, `write_integration_test`, `write_component_test`

```go
// TDD Phase 2: Create tests that WILL FAIL (RED)
func (t *Tester) CreateTests(ctx context.Context, bus *PipelineBus, criteria *InspectorCriteria) (*TesterTests, error) {
    tests := &TesterTests{
        TaskID:          criteria.TaskID,
        TestFiles:       []TestFile{},
        BasedOnCriteria: []string{},
        CreatedAt:       time.Now(),
    }

    // Generate tests for each success criterion
    for _, criterion := range criteria.SuccessCriteria {
        testFile := t.generateTestForCriterion(criterion, bus.Context().WorkerType)
        tests.TestFiles = append(tests.TestFiles, testFile)
        tests.BasedOnCriteria = append(tests.BasedOnCriteria, criterion.ID)
    }

    // Run initial tests - they MUST fail (RED phase)
    initialRun := t.runTests(tests.TestFiles)
    tests.InitialRun = initialRun

    if initialRun.AllPassed {
        return nil, fmt.Errorf("RED phase failed: tests passed without implementation - tests are not testing the right thing")
    }

    // Send to Worker (Phase 2 → Phase 3)
    return tests, bus.SendTests(tests)
}
```

#### TDD Phase 4 - Validation GREEN check (`pipeline_mode.go`)
- [ ] Receive Worker output after implementation (Phase 3 complete)
- [ ] Run test suite created in Phase 2
- [ ] Send `TesterResult` to Pipeline via PipelineBus
- [ ] Pass condition: ALL tests pass (GREEN)
- [ ] NO routing through Guide or Architect
- [ ] Focused testing (task-specific tests only)

```go
// Pipeline-internal testing flow
func (t *Tester) TestInPipeline(ctx context.Context, bus *PipelineBus, result *EngineerResult) (*TesterFeedback, error) {
    // Generate tests for this specific task
    tests := t.generateTaskTests(result, bus.Context().TaskPrompt)

    // Run only the generated tests
    testResult := t.runTests(tests)

    feedback := &TesterFeedback{
        Loop:        bus.Context().TesterLoops,
        Timestamp:   time.Now(),
        TestsRun:    testResult.Total,
        TestsPassed: testResult.Passed,
        Failures:    testResult.Failures,
        Passed:      len(testResult.Failures) == 0,
    }

    // Send directly to Engineer (NOT through Guide)
    return feedback, bus.SendTesterFeedback(feedback)
}
```

#### Test Planning (Session-Wide Mode)
- [ ] Identify test cases from implementation
  - [ ] Unit tests (per function/method)
  - [ ] Integration tests (component interaction)
  - [ ] Edge case tests (boundaries, empty, nil)
  - [ ] Failure mode tests (error paths)
- [ ] Generate test plan for user approval
- [ ] Estimate test coverage

#### Test Implementation
- [ ] Create test implementation DAG for Architect
- [ ] Generate test file structure
- [ ] Generate test cases with assertions

#### Test Execution
- [ ] Execute tests via `go test` command
- [ ] Capture test output
- [ ] Parse test results
- [ ] Track test timing

#### Failure Analysis
- [ ] Determine if TEST issue or IMPLEMENTATION issue
- [ ] Test issues: generate test fix
- [ ] Implementation issues: generate corrections for Architect
- [ ] Root cause analysis

#### User Control
- [ ] Skip condition support (--no-test-run)
- [ ] User override support (skip failing tests)
- [ ] Test subset selection

#### Bus Integration
- [ ] Subscribe to `tester.requests` (session-scoped)
- [ ] Publish to `tester.responses`
- [ ] Register with Guide via `AgentRoutingInfo`
- [ ] Support intents: `Plan`, `Execute`, `Analyze`

#### Tester Skills (Progressive Disclosure)

**Core Skills (always loaded):**
```go
// plan_tests - Create test plan
skills.NewSkill("plan_tests").
    Description("Create a test plan for the implementation").
    Domain("testing").
    Keywords("test", "plan", "cases", "coverage").
    ObjectParam("implementation", "Implementation results", true)

// run_tests - Execute tests
skills.NewSkill("run_tests").
    Description("Execute the test suite").
    Domain("testing").
    Keywords("run", "execute", "test").
    StringParam("path", "Test path (optional)", false).
    BoolParam("verbose", "Verbose output", false)

// get_results - Get test results
skills.NewSkill("get_results").
    Description("Get test execution results").
    Domain("testing").
    Keywords("results", "output", "pass", "fail").
    StringParam("run_id", "Test run ID (optional)", false)
```

**Extended Skills (loaded on demand):**
```go
// analyze_failure - Analyze a test failure
skills.NewSkill("analyze_failure").
    Description("Analyze why a test failed").
    Domain("testing").
    Keywords("analyze", "failure", "why", "failed").
    StringParam("test_name", "Name of failed test", true)

// generate_test - Generate a test case
skills.NewSkill("generate_test").
    Description("Generate a specific test case").
    Domain("testing").
    Keywords("generate", "create", "test", "case").
    StringParam("function", "Function to test", true).
    EnumParam("type", "Test type", []string{"unit", "integration", "edge", "failure"}, false)

// skip_test - Skip a test
skills.NewSkill("skip_test").
    Description("Mark a test to be skipped").
    Domain("testing").
    Keywords("skip", "ignore", "disable").
    StringParam("test_name", "Test name to skip", true).
    StringParam("reason", "Reason for skipping", true)

// coverage_report - Generate coverage report
skills.NewSkill("coverage_report").
    Description("Generate a test coverage report").
    Domain("testing").
    Keywords("coverage", "report", "covered", "percent").
    StringParam("package", "Package to report on (optional)", false)

// benchmark - Run benchmarks
skills.NewSkill("benchmark").
    Description("Run performance benchmarks").
    Domain("testing").
    Keywords("benchmark", "performance", "speed", "timing").
    StringParam("pattern", "Benchmark pattern", false)
```

**Superpowers Skills (from superpowers methodology):**
```go
// test_driven_development - TDD workflow skills
// Source: superpowers/test-driven-development
skills.NewSkill("test_driven_development").
    Description("Follow TDD workflow: create failing tests first, then implement").
    Domain("testing").
    Keywords("tdd", "red", "green", "refactor").
    ObjectParam("criteria", "Success criteria from Inspector", true).
    BoolParam("verify_red_first", "Verify tests fail before implementation", false)

// write_unit_test - Write unit test from criterion
// Source: superpowers/test-driven-development
skills.NewSkill("write_unit_test").
    Description("Write unit test based on success criterion (TDD Phase 2)").
    Domain("testing").
    Keywords("unit", "test", "write", "create").
    ObjectParam("criterion", "Success criterion to test", true).
    StringParam("target_function", "Function/method to test", false)

// write_integration_test - Write integration test from criterion
// Source: superpowers/test-driven-development
skills.NewSkill("write_integration_test").
    Description("Write integration test based on success criterion (TDD Phase 2)").
    Domain("testing").
    Keywords("integration", "test", "write", "create").
    ObjectParam("criterion", "Success criterion to test", true).
    ArrayParam("components", "Components involved in integration", false)

// write_component_test - Write component test (UI)
// Source: superpowers/test-driven-development
skills.NewSkill("write_component_test").
    Description("Write component test for UI based on criterion (TDD Phase 2)").
    Domain("testing").
    Keywords("component", "test", "ui", "write").
    ObjectParam("criterion", "Success criterion to test", true).
    StringParam("component_path", "Path to component", false)

// verification_before_completion - Verify before marking complete
// Source: superpowers/verification-before-completion
skills.NewSkill("verification_before_completion").
    Description("Thorough verification before completing test phase").
    Domain("testing").
    Keywords("verify", "complete", "done", "check").
    ObjectParam("test_results", "Results to verify", true).
    ArrayParam("verification_steps", "Steps to verify", false)
```

**Test Framework Skills (dynamically configurable):**
```go
// run_test_framework - Run specific test framework
skills.NewSkill("run_test_framework").
    Description("Run tests using a specific framework").
    Domain("testing").
    Keywords("test", "framework", "jest", "pytest", "go", "mocha").
    StringParam("framework", "Test framework (auto-detect if not specified)", false).
    StringParam("path", "Test path or pattern", false).
    ArrayParam("args", "Additional arguments", false).
    BoolParam("verbose", "Verbose output", false).
    BoolParam("coverage", "Include coverage", false)

// configure_test_framework - Configure test framework
skills.NewSkill("configure_test_framework").
    Description("Configure a test framework for the project").
    Domain("testing").
    Keywords("configure", "setup", "framework").
    StringParam("framework", "Framework to configure", true).
    ObjectParam("config", "Configuration options", false)

// list_test_frameworks - List available test frameworks
skills.NewSkill("list_test_frameworks").
    Description("List available test frameworks for detected languages").
    Domain("testing").
    Keywords("list", "frameworks", "available").
    StringParam("path", "Project path", true)
```

**Test Framework Registry (dynamically loaded):**
```go
type TestFrameworkConfig struct {
    Name         string
    Language     string
    RunCommand   string            // Command to run tests
    CoverageCmd  string            // Command for coverage
    ConfigFiles  []string          // Config file names
    OutputFormat string            // Output format (json, tap, etc.)
    ParseFunc    string            // Function name for parsing output
}

// Example configurations (user can extend/override)
var TestFrameworks = map[string]TestFrameworkConfig{
    "go-test": {
        Name: "go test",
        Language: "go",
        RunCommand: "go test -json",
        CoverageCmd: "go test -coverprofile=coverage.out -json",
        ConfigFiles: []string{},
        OutputFormat: "json",
    },
    "pytest": {
        Name: "pytest",
        Language: "python",
        RunCommand: "pytest --tb=short -q",
        CoverageCmd: "pytest --cov --cov-report=json",
        ConfigFiles: []string{"pytest.ini", "pyproject.toml", "setup.cfg"},
        OutputFormat: "pytest",
    },
    "jest": {
        Name: "jest",
        Language: "javascript",
        RunCommand: "jest --json",
        CoverageCmd: "jest --coverage --json",
        ConfigFiles: []string{"jest.config.js", "jest.config.ts", "package.json"},
        OutputFormat: "json",
    },
    "mocha": {
        Name: "mocha",
        Language: "javascript",
        RunCommand: "mocha --reporter json",
        CoverageCmd: "nyc mocha --reporter json",
        ConfigFiles: []string{".mocharc.js", ".mocharc.json"},
        OutputFormat: "json",
    },
    "vitest": {
        Name: "vitest",
        Language: "javascript",
        RunCommand: "vitest run --reporter json",
        CoverageCmd: "vitest run --coverage --reporter json",
        ConfigFiles: []string{"vitest.config.ts", "vite.config.ts"},
        OutputFormat: "json",
    },
    "cargo-test": {
        Name: "cargo test",
        Language: "rust",
        RunCommand: "cargo test -- --format json",
        CoverageCmd: "cargo tarpaulin --out json",
        ConfigFiles: []string{"Cargo.toml"},
        OutputFormat: "json",
    },
    "rspec": {
        Name: "rspec",
        Language: "ruby",
        RunCommand: "rspec --format json",
        CoverageCmd: "rspec --format json",
        ConfigFiles: []string{".rspec", "spec/spec_helper.rb"},
        OutputFormat: "json",
    },
}
```

**Environment Skills (for test execution):**
```go
// ensure_test_env - Ensure test environment exists
skills.NewSkill("ensure_test_env").
    Description("Ensure test environment is properly configured").
    Domain("environment").
    Keywords("environment", "setup", "venv", "dependencies").
    StringParam("path", "Project path", true).
    BoolParam("install_deps", "Install test dependencies", false)

// create_venv - Create virtual environment for tests
skills.NewSkill("create_venv").
    Description("Create a virtual environment for test isolation").
    Domain("environment").
    Keywords("venv", "virtualenv", "isolate").
    StringParam("path", "Environment path", true).
    EnumParam("type", "Environment type", []string{"python-venv", "node-sandbox"}, true)

// install_test_deps - Install test dependencies
skills.NewSkill("install_test_deps").
    Description("Install test dependencies").
    Domain("environment").
    Keywords("install", "dependencies", "packages", "dev").
    StringParam("path", "Project path", true).
    ArrayParam("packages", "Additional packages to install", false)

// list_test_deps - List test dependencies
skills.NewSkill("list_test_deps").
    Description("List installed test dependencies").
    Domain("environment").
    Keywords("list", "dependencies", "installed").
    StringParam("path", "Project path", true)
```

**Read-Only Bash Skills (non-write operations):**
```go
// cat_file - Read file contents
skills.NewSkill("cat_file").
    Description("Display file contents").
    Domain("read").
    Keywords("cat", "read", "show", "display").
    StringParam("path", "File path", true).
    IntParam("head", "Show first N lines", false).
    IntParam("tail", "Show last N lines", false)

// grep_output - Search test output
skills.NewSkill("grep_output").
    Description("Search test output or log files").
    Domain("read").
    Keywords("grep", "search", "find", "pattern").
    StringParam("pattern", "Search pattern", true).
    StringParam("path", "File or log path", true).
    BoolParam("case_insensitive", "Case insensitive", false)

// run_readonly_bash - Run read-only bash commands
skills.NewSkill("run_readonly_bash").
    Description("Run non-destructive bash commands for test analysis").
    Domain("bash").
    Keywords("bash", "shell", "command").
    StringParam("command", "Command to run", true).
    IntParam("timeout_ms", "Timeout in milliseconds", false)

// find_files - Find test files
skills.NewSkill("find_files").
    Description("Find test files using the find command").
    Domain("search").
    Keywords("find", "locate", "files", "test").
    StringParam("path", "Starting directory path", true).
    StringParam("name", "File name pattern (glob)", false).
    StringParam("type", "File type (f=file, d=directory)", false).
    IntParam("maxdepth", "Maximum directory depth", false)

// Read-only command whitelist for Tester
var TesterReadOnlyCommands = []string{
    `^cat .*`,
    `^head .*`,
    `^tail .*`,
    `^grep .*`,
    `^find .* -type f.*`,
    `^ls .*`,
    `^wc .*`,
    `^diff .*`,
    `^git (log|diff|status|show).*`,
    `^go test -list.*`,
    `^npm test -- --listTests.*`,
    `^pytest --collect-only.*`,
}
```

#### Tester Tools
```go
var TesterTools = []ToolDefinition{
    // Core testing
    {Name: "tester_plan_tests", Skill: "plan_tests"},
    {Name: "tester_run_tests", Skill: "run_tests"},
    {Name: "tester_get_results", Skill: "get_results"},
    // Analysis
    {Name: "tester_analyze_failure", Skill: "analyze_failure"},
    {Name: "tester_coverage", Skill: "coverage_report"},
    {Name: "tester_generate_test", Skill: "generate_test"},
    {Name: "tester_skip_test", Skill: "skip_test"},
    {Name: "tester_benchmark", Skill: "benchmark"},
    // Framework management
    {Name: "tester_run_framework", Skill: "run_test_framework"},
    {Name: "tester_configure_framework", Skill: "configure_test_framework"},
    {Name: "tester_list_frameworks", Skill: "list_test_frameworks"},
    // Environment
    {Name: "tester_ensure_env", Skill: "ensure_test_env"},
    {Name: "tester_create_venv", Skill: "create_venv"},
    {Name: "tester_install_deps", Skill: "install_test_deps"},
    // Read-only bash
    {Name: "tester_cat_file", Skill: "cat_file"},
    {Name: "tester_grep_output", Skill: "grep_output"},
    {Name: "tester_run_readonly_bash", Skill: "run_readonly_bash"},
    {Name: "tester_find_files", Skill: "find_files"},
    // Routing & commands
    {Name: "route_to", Skill: "route_to"},
    {Name: "reply_to", Skill: "reply_to"},
    {Name: "run_command", Skill: "run_command"},
}
```

#### Tester Hooks
- [ ] Pre-plan hook: load existing tests from Librarian
- [ ] Post-plan hook: store test plan in Archivalist
- [ ] Pre-run hook: check for skip conditions
- [ ] Post-run hook: store results in Archivalist
- [ ] Pre-analysis hook: load similar failures from Archivalist
- [ ] Post-corrections hook: format for Architect

#### Memory Management (Local Compaction)

**Model**: OpenAI Codex 5.2

**NOTE**: Tester compacts LOCALLY. It does NOT trigger pipeline handoff (only Engineer can).

**Files to add:**
- `agents/tester/memory.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint to Archivalist
- [ ] At 95% threshold: compact locally (NOT trigger handoff)
- [ ] Checkpoint includes: tests created, tests run, pass/fail status, failure descriptions
- [ ] If Engineer triggers handoff, Tester participates in state transfer

```go
// Tester checkpoint summary (sent to Archivalist at 85%)
type TesterCheckpointSummary struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    SessionID         string            `json:"session_id"`
    Timestamp         time.Time         `json:"timestamp"`
    ContextUsage      float64           `json:"context_usage"`
    CheckpointIndex   int               `json:"checkpoint_index"`

    // Test state
    TestsCreated      []TestInfo        `json:"tests_created"`
    TestsRun          int               `json:"tests_run"`
    TestsPassed       int               `json:"tests_passed"`
    TestsFailed       int               `json:"tests_failed"`
    TestsSkipped      int               `json:"tests_skipped"`

    // Failure details
    Failures          []TestFailure     `json:"failures"`
    FlakeyTests       []string          `json:"flakey_tests"`

    // Coverage (if available)
    CoveragePercent   float64           `json:"coverage_percent,omitempty"`
    UncoveredPaths    []string          `json:"uncovered_paths,omitempty"`

    // Loop statistics
    LoopsCompleted    int               `json:"loops_completed"`
    FixesVerified     int               `json:"fixes_verified"`
}

type TestInfo struct {
    Name        string   `json:"name"`
    FilePath    string   `json:"file_path"`
    TestType    string   `json:"test_type"`  // unit, integration, e2e
    ForTask     string   `json:"for_task"`   // task ID this test validates
}

type TestFailure struct {
    TestName    string   `json:"test_name"`
    FilePath    string   `json:"file_path"`
    ErrorMsg    string   `json:"error_msg"`
    StackTrace  string   `json:"stack_trace,omitempty"`
    FixAttempts int      `json:"fix_attempts"`
}

// Tester handoff state (when Engineer triggers handoff)
type TesterHandoffState struct {
    PipelineID        PipelineID        `json:"pipeline_id"`
    TestsCreated      []TestInfo        `json:"tests_created"`
    TestResults       *TestRunSummary   `json:"test_results"`
    PendingFailures   []TestFailure     `json:"pending_failures"`
    TestPlanRemaining []string          `json:"test_plan_remaining"`
}

func (t *Tester) checkContextAndManage() error {
    usage := t.getContextUsage()

    if usage >= 0.95 {
        // Compact locally - DO NOT trigger handoff
        return t.compactLocally()
    }

    if usage >= 0.85 {
        // Checkpoint to Archivalist
        return t.submitCheckpoint()
    }

    return nil
}
```

**Tests:**
- [ ] Generate test plan
- [ ] Execute tests
- [ ] Analyze failures
- [ ] Test skip conditions
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Engineer triggers handoff

---

## Common Agent Skills

All agents MUST implement the following common skills for inter-agent routing and communication.

### Step 0: Role-Aware Skill Decision (All Agents)

**CRITICAL: Every agent MUST perform Step 0 before processing ANY information.**

Step 0 is a role-aware decision gate where each agent asks: "Given MY specific role and the information I'm processing, SHOULD I invoke any of my skills?"

**Files to create:**
- `agents/common/step0.go`
- `agents/common/reroute.go`

**Acceptance Criteria:**

#### Step 0 Evaluation
- [ ] Receive information (request, message, data)
- [ ] Evaluate against agent's role: "What am I responsible for?"
- [ ] Evaluate against agent's domain: "Is this information within my domain?"
- [ ] Evaluate against agent's skills: "Do I have skills that apply to this?"
- [ ] Make decision: YES (invoke skills), NO (reroute), or PARTIAL (need more context)

#### Decision Handling
- [ ] **YES** → Proceed with skill execution for this request
- [ ] **NO** → Send `REROUTE_REQUEST` to Guide with reason and suggested target
- [ ] **PARTIAL** → Query for additional context before deciding

#### Reroute Request Format
- [ ] `REROUTE_REQUEST` message type implemented
- [ ] Include `source` (this agent's ID)
- [ ] Include `reason` (why this isn't the agent's domain)
- [ ] Include `suggested_target` (which agent might be better suited)
- [ ] Include `original_request` (the full original message)

```go
type RerouteRequest struct {
    Type            string      `json:"type"`  // "REROUTE_REQUEST"
    Source          string      `json:"source"`
    Reason          string      `json:"reason"`
    SuggestedTarget string      `json:"suggested_target,omitempty"`
    OriginalRequest interface{} `json:"original_request"`
}
```

#### Step 0 Base Prompt Fragment
- [ ] `AgentStep0Prompt` constant defined
- [ ] Template includes `[AGENT_ROLE_DESCRIPTION]` placeholder
- [ ] Template includes `[AGENT_DOMAIN]` placeholder
- [ ] Template includes `[AGENT_SKILL_LIST]` placeholder
- [ ] All agents inject this prompt fragment into their system prompts

#### Guide Reroute Handling
- [ ] Guide tracks reroute count per request
- [ ] First reroute: normal, proceed to suggested target
- [ ] Second reroute: warning logged, likely ambiguous request
- [ ] Third reroute: STOP, ask user for clarification
- [ ] Reroute history maintained: `{from, reason, to}` for each hop
- [ ] User clarification message explains what agents said and why

**Tests:**
- [ ] Test Step 0 decision for "YES" case (within domain)
- [ ] Test Step 0 decision for "NO" case (outside domain)
- [ ] Test Step 0 decision for "PARTIAL" case (need more context)
- [ ] Test REROUTE_REQUEST message generation
- [ ] Test Guide reroute handling with 1, 2, 3+ reroutes
- [ ] Test user clarification triggered after 2+ reroutes

### Routing Skills (All Agents)

Every agent must support routing commands via `@to:<agent>` and `@from:<agent>` patterns as specified in the Guide routing protocol.

```go
// Common routing skills - ALL agents must implement these
var CommonRoutingSkills = []skills.Skill{
    // route_to - Route message to another agent
    skills.NewSkill("route_to").
        Description("Route a message to another agent via Guide").
        Domain("routing").
        Keywords("@to", "route", "send", "forward").
        StringParam("target", "Target agent (e.g., 'architect', 'librarian')", true).
        StringParam("message", "Message content to route", true).
        ObjectParam("context", "Additional context to include", false),

    // route_from - Handle message routed from another agent
    skills.NewSkill("route_from").
        Description("Process a message routed from another agent").
        Domain("routing").
        Keywords("@from", "receive", "handle").
        StringParam("source", "Source agent that sent the message", true).
        StringParam("message", "Message content received", true).
        StringParam("correlation_id", "Correlation ID for response matching", true),

    // reply_to - Reply to a routed message
    skills.NewSkill("reply_to").
        Description("Reply to a previously received routed message").
        Domain("routing").
        Keywords("reply", "respond", "answer").
        StringParam("correlation_id", "Correlation ID of original message", true).
        StringParam("response", "Response content", true).
        BoolParam("complete", "Mark conversation as complete", false),

    // broadcast - Broadcast to multiple agents
    skills.NewSkill("broadcast").
        Description("Broadcast a message to multiple agents").
        Domain("routing").
        Keywords("broadcast", "notify", "all").
        ArrayParam("targets", "List of target agents", true).
        StringParam("message", "Message to broadcast", true),
}

// Routing pattern syntax (parsed by Guide)
// @to:architect "please plan this task"
// @to:librarian "find all files matching *.go"
// @from:engineer "task completed successfully"
// @broadcast:inspector,tester "validation complete"
```

### Slash Command Skills (Academic, Architect, Librarian, Inspector, Tester)

The Academic, Architect, Librarian, Inspector, and Tester agents should support slash commands for direct invocation of specific operations.

```go
// Slash command skill - implemented by Academic, Architect, Inspector, Tester
var SlashCommandSkill = skills.NewSkill("run_command").
    Description("Execute a slash command").
    Domain("commands").
    Keywords("/", "slash", "command").
    StringParam("command", "Slash command to execute (e.g., /research, /plan)", true).
    ArrayParam("args", "Command arguments", false).
    ObjectParam("options", "Command options", false)

// Agent-specific slash commands

// Academic slash commands
var AcademicSlashCommands = map[string]string{
    "/research":  "Research a topic and provide comprehensive analysis",
    "/compare":   "Compare approaches or technologies",
    "/ingest":    "Ingest a new source (GitHub repo, article, etc.)",
    "/cite":      "Get citations for previous research",
    "/sources":   "List available sources",
}

// Architect slash commands
var ArchitectSlashCommands = map[string]string{
    "/plan":      "Create an implementation plan",
    "/status":    "Get current execution status",
    "/approve":   "Approve the current plan",
    "/modify":    "Modify the current plan",
    "/interrupt": "Interrupt current execution",
    "/clarify":   "Request clarification",
    "/schedule":  "Schedule work with orchestrator",
    "/workflow":  "View or adjust current workflow",
}

// Librarian slash commands
var LibrarianSlashCommands = map[string]string{
    "/find":      "Find a symbol, file, or pattern in the codebase",
    "/search":    "Search code content with regex",
    "/file":      "Read or display a file",
    "/imports":   "Analyze import relationships",
    "/usages":    "Find usages of a symbol",
    "/structure": "Get structure of a file or package",
    "/scan":      "Scan a remote repository",
    "/tooling":   "Identify languages and dev tools",
}

// Inspector slash commands
var InspectorSlashCommands = map[string]string{
    "/validate":  "Validate implementation",
    "/lint":      "Run linter on code",
    "/format":    "Check code formatting",
    "/typecheck": "Run type checker",
    "/issues":    "List current issues",
    "/override":  "Override a validation issue",
    "/analyze":   "Deep analysis of code",
}

// Tester slash commands
var TesterSlashCommands = map[string]string{
    "/test":      "Run tests",
    "/coverage":  "Generate coverage report",
    "/plan":      "Create test plan",
    "/analyze":   "Analyze test failure",
    "/skip":      "Skip a test",
    "/benchmark": "Run benchmarks",
    "/watch":     "Watch mode for tests",
}
```

---

## Progressive Skill Disclosure

### Skill Loading Strategy

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         PROGRESSIVE SKILL DISCLOSURE                                 │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  TIER 1: CORE SKILLS (Always loaded, ~5-10 per agent)                               │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Essential skills for basic operation                                          │ │
│  │  Examples: read_file, write_file, query, store, plan, validate                 │ │
│  │  Token cost: ~500-1000 tokens per agent                                        │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  TIER 2: DOMAIN SKILLS (Loaded when domain activated)                               │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Loaded when: User query matches domain keywords                               │ │
│  │  Examples: analyze_imports (when discussing dependencies)                      │ │
│  │           compare_approaches (when asking "X vs Y")                            │ │
│  │  Token cost: ~200-500 tokens per domain                                        │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  TIER 3: SPECIALIZED SKILLS (Loaded on explicit request)                            │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  Loaded when: Explicit tool call or user command                               │ │
│  │  Examples: benchmark, ingest_github, generate_paper                            │ │
│  │  Token cost: ~100-300 tokens each                                              │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
│  SKILL UNLOADING:                                                                   │
│  ┌────────────────────────────────────────────────────────────────────────────────┐ │
│  │                                                                                │ │
│  │  • Unload unused skills after N turns without use                              │ │
│  │  • Unload when approaching token budget                                        │ │
│  │  • Never unload Tier 1 core skills                                             │ │
│  │  • LRU eviction for Tier 2/3 skills                                            │ │
│  │                                                                                │ │
│  └────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Agent Efficiency Techniques

### 6.21 Scratchpad Memory

Within-session working memory for agents to track notes, reasoning, and temporary state.

**Files to create:**
- `agents/common/scratchpad.go`
- `agents/common/scratchpad_test.go`
- `skills/scratchpad_skill.go`

**Acceptance Criteria:**

#### Scratchpad Data Structure
- [ ] `ScratchpadEntry` with key, value, category, timestamp, expiry
- [ ] `Scratchpad` manager with `Set()`, `Get()`, `Delete()`, `GetByCategory()`, `All()`
- [ ] Session-scoped (entries tied to session ID)
- [ ] Auto-cleanup on session end
- [ ] Thread-safe with RWMutex

```go
type ScratchpadEntry struct {
    Key       string    `json:"key"`
    Value     string    `json:"value"`
    Category  string    `json:"category"`
    CreatedAt time.Time `json:"created_at"`
    ExpiresAt time.Time `json:"expires_at,omitempty"`
}
```

#### Scratchpad Skills
- [ ] `scratchpad_set` - Store a note (key, value, category optional)
- [ ] `scratchpad_get` - Retrieve a note by key
- [ ] `scratchpad_list` - List all notes (optional category filter)
- [ ] `scratchpad_delete` - Remove a note by key

#### Common Use Cases
- [ ] "Requirements noted from user" category
- [ ] "Partial work" category for intermediate results
- [ ] "Decisions made" category for reasoning trail
- [ ] "Blockers found" category for issues

**Tests:**
- [ ] Set and get works correctly
- [ ] Category filtering works
- [ ] Expiry removes old entries
- [ ] Session isolation (can't access other session's notes)
- [ ] Thread-safe under concurrent access

---

### 6.22 Code Style Inference

Automatic detection of project code style from existing files.

**Files to create:**
- `agents/engineer/style_inference.go`
- `agents/engineer/style_inference_test.go`
- `skills/style_check_skill.go`

**Acceptance Criteria:**

#### Style Analysis
- [ ] `InferredStyle` struct with naming, formatting, patterns
- [ ] `StyleInferrer.Analyze()` reads sample files
- [ ] Detect naming conventions (camelCase, snake_case, PascalCase)
- [ ] Detect indentation (tabs vs spaces, indent width)
- [ ] Detect quote style (single vs double)
- [ ] Detect trailing comma preference
- [ ] Detect import organization pattern

```go
type InferredStyle struct {
    Language       string              `json:"language"`
    NamingConvention struct {
        Variables string `json:"variables"` // "camelCase", "snake_case"
        Functions string `json:"functions"`
        Types     string `json:"types"`
        Constants string `json:"constants"` // "SCREAMING_SNAKE"
    } `json:"naming_convention"`
    Formatting struct {
        IndentStyle string `json:"indent_style"` // "tabs", "spaces"
        IndentSize  int    `json:"indent_size"`
        QuoteStyle  string `json:"quote_style"` // "single", "double"
        TrailingComma bool `json:"trailing_comma"`
    } `json:"formatting"`
    Patterns struct {
        ErrorHandling string `json:"error_handling"` // "early_return", "nested"
        Imports       string `json:"imports"`        // "grouped", "ungrouped"
    } `json:"patterns"`
}
```

#### Style Check Skill
- [ ] `check_style` skill returns project style for language
- [ ] Caches inferred styles per project/language
- [ ] Updates when new files detected
- [ ] Returns actionable guidance

**Tests:**
- [ ] Correctly detects camelCase vs snake_case
- [ ] Correctly detects tabs vs spaces
- [ ] Handles mixed styles (reports majority)
- [ ] Cache invalidation on new files
- [ ] Multi-language project support

---

### 6.23 Component/Pattern Registry

Index of reusable UI components, hooks, utilities, and patterns in the project.

**Files to create:**
- `agents/engineer/component_registry.go`
- `agents/engineer/component_registry_test.go`
- `skills/find_component_skill.go`

**Acceptance Criteria:**

#### Registry Data Structure
- [ ] `RegisteredComponent` with name, type, file path, exports, props/params
- [ ] `ComponentRegistry` with `Register()`, `Find()`, `FindByType()`, `GetAll()`
- [ ] Auto-scan project for components on startup
- [ ] Watch for file changes and update registry

```go
type RegisteredComponent struct {
    Name        string            `json:"name"`
    Type        string            `json:"type"`  // "component", "hook", "utility", "pattern"
    FilePath    string            `json:"file_path"`
    Exports     []string          `json:"exports"`
    Props       []ComponentProp   `json:"props,omitempty"`
    Description string            `json:"description,omitempty"`
    Examples    []string          `json:"examples,omitempty"`
}

type ComponentProp struct {
    Name     string `json:"name"`
    Type     string `json:"type"`
    Required bool   `json:"required"`
    Default  string `json:"default,omitempty"`
}
```

#### Component Scanning
- [ ] Scan React/Vue/Svelte component files
- [ ] Extract exported names
- [ ] Parse props/parameters
- [ ] Detect custom hooks (use* pattern)
- [ ] Detect utility functions

#### Find Component Skill
- [ ] `find_component` - Search by name or purpose
- [ ] Returns existing components that match need
- [ ] Includes usage examples from codebase
- [ ] Warns before suggesting new component that exists

**Tests:**
- [ ] Correctly finds React components
- [ ] Extracts props from TypeScript interfaces
- [ ] Detects hooks correctly
- [ ] Search by partial name works
- [ ] Updates on file change

---

### 6.24 Mistake Memory

Cross-session learning from errors and anti-patterns.

**Files to create:**
- `agents/common/mistake_memory.go`
- `agents/common/mistake_memory_test.go`
- `skills/recall_mistakes_skill.go`

**Acceptance Criteria:**

#### Mistake Data Structure
- [ ] `RecordedMistake` with pattern, context, resolution, occurrences
- [ ] `MistakeMemory` with `Record()`, `Recall()`, `RecallByCategory()`
- [ ] Persistent storage (SQLite)
- [ ] Relevance scoring (recent + frequent = higher priority)

```go
type RecordedMistake struct {
    ID         string    `json:"id"`
    Pattern    string    `json:"pattern"`     // What went wrong
    Category   string    `json:"category"`    // "syntax", "logic", "import", "api_misuse"
    Context    string    `json:"context"`     // Where/when it happened
    Resolution string    `json:"resolution"`  // How to fix
    Occurrences int      `json:"occurrences"`
    FirstSeen  time.Time `json:"first_seen"`
    LastSeen   time.Time `json:"last_seen"`
    FilePaths  []string  `json:"file_paths"`  // Where it occurred
}
```

#### Mistake Recording
- [ ] Automatic recording from failed builds/tests
- [ ] Manual recording via skill
- [ ] Increment occurrences on repeat
- [ ] Extract patterns from error messages

#### Recall Skill
- [ ] `recall_mistakes` - Get relevant past mistakes
- [ ] Accepts optional category filter
- [ ] Returns most relevant (recent + frequent) first
- [ ] Used by Engineer before writing similar code

**Tests:**
- [ ] Records mistakes correctly
- [ ] Increments occurrences on repeat
- [ ] Persistence across restarts
- [ ] Relevance scoring works
- [ ] Category filtering works

---

### 6.25 Diff Preview

Preview changes before applying them.

**Files to create:**
- `agents/engineer/diff_preview.go`
- `agents/engineer/diff_preview_test.go`
- `skills/diff_preview_skill.go`

**Acceptance Criteria:**

#### Diff Generation
- [ ] `DiffPreview` with original, proposed, unified diff, stats
- [ ] Generate unified diff format
- [ ] Calculate stats (lines added, removed, changed)
- [ ] Support multi-file diffs
- [ ] Syntax highlighting hints

```go
type DiffPreview struct {
    FilePath    string `json:"file_path"`
    Original    string `json:"original,omitempty"`
    Proposed    string `json:"proposed,omitempty"`
    UnifiedDiff string `json:"unified_diff"`
    Stats       DiffStats `json:"stats"`
}

type DiffStats struct {
    LinesAdded   int `json:"lines_added"`
    LinesRemoved int `json:"lines_removed"`
    LinesChanged int `json:"lines_changed"`
    Hunks        int `json:"hunks"`
}
```

#### Preview Skill
- [ ] `preview_diff` - Show what changes would be made
- [ ] Accepts file path and proposed content
- [ ] Returns unified diff and stats
- [ ] Can preview multiple files at once

#### Validation
- [ ] Flag potentially dangerous changes (>100 lines removed)
- [ ] Flag changes to sensitive files (.env, credentials)
- [ ] Suggest review for large refactors

**Tests:**
- [ ] Unified diff format correct
- [ ] Stats calculation accurate
- [ ] Multi-file preview works
- [ ] Sensitive file detection works
- [ ] Large change warning triggers

---

### 6.26 User Preference Learning

Learn and apply user preferences over time.

**Files to create:**
- `agents/common/preference_learning.go`
- `agents/common/preference_learning_test.go`
- `skills/preferences_skill.go`

**Acceptance Criteria:**

#### Preference Data Structure
- [ ] `UserPreference` with domain, key, value, confidence, source
- [ ] `PreferenceStore` with `Learn()`, `Get()`, `GetAll()`
- [ ] Persistent storage (SQLite)
- [ ] Confidence increases with repeated signals

```go
type UserPreference struct {
    Domain     string    `json:"domain"`      // "code_style", "communication", "workflow"
    Key        string    `json:"key"`         // "verbosity", "test_framework"
    Value      string    `json:"value"`       // "concise", "jest"
    Confidence float64   `json:"confidence"`  // 0.0-1.0
    Source     string    `json:"source"`      // "explicit", "implicit", "inferred"
    LearnedAt  time.Time `json:"learned_at"`
    SeenCount  int       `json:"seen_count"`
}
```

#### Learning Sources
- [ ] Explicit: User says "I prefer X"
- [ ] Implicit: User accepts/rejects suggestions
- [ ] Inferred: Patterns from code edits

#### Preference Application
- [ ] `get_preferences` - Retrieve preferences for domain
- [ ] `set_preference` - Explicitly set a preference
- [ ] Preferences influence response generation
- [ ] Confidence-weighted (high confidence = strong preference)

**Tests:**
- [ ] Explicit preferences stored correctly
- [ ] Confidence increases on repeated signals
- [ ] Implicit learning from accept/reject
- [ ] Persistence across sessions
- [ ] Domain filtering works

---

### 6.27 File Snapshot Cache

Efficient caching of file states to avoid re-reading.

**Files to create:**
- `agents/common/file_snapshot.go`
- `agents/common/file_snapshot_test.go`

**Acceptance Criteria:**

#### Snapshot Data Structure
- [ ] `FileSnapshot` with path, content hash, last modified, content
- [ ] `SnapshotCache` with `Get()`, `Invalidate()`, `InvalidateAll()`
- [ ] Content-addressable (hash-based)
- [ ] LRU eviction when over capacity

```go
type FileSnapshot struct {
    Path         string    `json:"path"`
    ContentHash  string    `json:"content_hash"`
    LastModified time.Time `json:"last_modified"`
    Content      []byte    `json:"-"`
    LineCount    int       `json:"line_count"`
    Size         int64     `json:"size"`
}

type SnapshotCache struct {
    maxSize    int64
    currentSize int64
    snapshots  map[string]*FileSnapshot
    lru        *list.List
    mu         sync.RWMutex
}
```

#### Cache Operations
- [ ] Check modified time before returning cached
- [ ] Automatic invalidation on file change detection
- [ ] Pre-warm cache for frequently accessed files
- [ ] Track access patterns for eviction

#### Integration
- [ ] File read operations use cache
- [ ] Invalidate on write operations
- [ ] Stats: hits, misses, evictions

**Tests:**
- [ ] Cache hit returns correct content
- [ ] Modified file triggers re-read
- [ ] LRU eviction works correctly
- [ ] Write invalidates cache
- [ ] Concurrent access safe

---

### 6.28 Dependency Awareness

Track project dependencies and their APIs.

**Files to create:**
- `agents/engineer/dependency_awareness.go`
- `agents/engineer/dependency_awareness_test.go`
- `skills/check_dependency_skill.go`

**Acceptance Criteria:**

#### Dependency Tracking
- [ ] `TrackedDependency` with name, version, import path, common APIs
- [ ] `DependencyTracker` with `Scan()`, `Get()`, `GetAll()`
- [ ] Parse package.json, go.mod, requirements.txt, etc.
- [ ] Track which dependencies are actually used

```go
type TrackedDependency struct {
    Name        string   `json:"name"`
    Version     string   `json:"version"`
    ImportPath  string   `json:"import_path"`
    CommonAPIs  []string `json:"common_apis"`
    UsedIn      []string `json:"used_in"`      // File paths
    DevOnly     bool     `json:"dev_only"`
    Deprecated  bool     `json:"deprecated,omitempty"`
}
```

#### API Awareness
- [ ] Index common APIs from frequently-used deps
- [ ] Detect deprecated API usage
- [ ] Suggest correct imports
- [ ] Version compatibility warnings

#### Check Dependency Skill
- [ ] `check_dependency` - Get info about a dependency
- [ ] Returns version, common APIs, usage examples
- [ ] Warns about deprecated/insecure versions

**Tests:**
- [ ] Parses package.json correctly
- [ ] Parses go.mod correctly
- [ ] Tracks usage across files
- [ ] Detects deprecated APIs
- [ ] Version parsing correct

---

### 6.29 Task Continuity

Checkpoint and resume interrupted tasks.

**Files to create:**
- `agents/common/task_continuity.go`
- `agents/common/task_continuity_test.go`
- `skills/checkpoint_skill.go`

**Acceptance Criteria:**

#### Checkpoint Data Structure
- [ ] `TaskCheckpoint` with task ID, description, progress, state, files modified
- [ ] `ContinuityManager` with `Checkpoint()`, `Resume()`, `List()`, `Clear()`
- [ ] Persistent storage (JSON files or SQLite)
- [ ] Auto-checkpoint on significant progress

```go
type TaskCheckpoint struct {
    ID            string            `json:"id"`
    TaskDescription string          `json:"task_description"`
    Progress      TaskProgress      `json:"progress"`
    State         map[string]any    `json:"state"`
    FilesModified []string          `json:"files_modified"`
    CreatedAt     time.Time         `json:"created_at"`
    UpdatedAt     time.Time         `json:"updated_at"`
}

type TaskProgress struct {
    TotalSteps     int      `json:"total_steps"`
    CompletedSteps int      `json:"completed_steps"`
    CurrentStep    string   `json:"current_step"`
    NextSteps      []string `json:"next_steps"`
    Blockers       []string `json:"blockers,omitempty"`
}
```

#### Checkpoint Skills
- [ ] `checkpoint_task` - Save current progress
- [ ] `resume_task` - Resume from checkpoint
- [ ] `list_checkpoints` - Show incomplete tasks
- [ ] Auto-checkpoint on multi-step tasks

#### Resume Logic
- [ ] Restore state from checkpoint
- [ ] Validate files haven't changed significantly
- [ ] Generate handoff context for agent
- [ ] Clear checkpoint on task completion

**Tests:**
- [ ] Checkpoint saves all state
- [ ] Resume restores correctly
- [ ] File change detection works
- [ ] Persistence across restarts
- [ ] Auto-checkpoint triggers correctly

---

### 6.30 Design Token Awareness

Track and apply design system tokens.

**Files to create:**
- `agents/designer/design_tokens.go`
- `agents/designer/design_tokens_test.go`
- `skills/design_token_skill.go`

**Acceptance Criteria:**

#### Token Data Structure
- [ ] `DesignToken` with category, name, value, usage context
- [ ] `DesignTokenRegistry` with `Get()`, `GetByCategory()`, `Suggest()`
- [ ] Parse from CSS variables, Tailwind config, theme files
- [ ] Auto-scan project for design system

```go
type DesignToken struct {
    Category string `json:"category"` // "color", "spacing", "typography", "shadow"
    Name     string `json:"name"`     // "primary", "md", "heading-1"
    Value    string `json:"value"`    // "#3b82f6", "16px", "24px"
    Usage    string `json:"usage"`    // "Primary brand color for buttons and links"
    CSSVar   string `json:"css_var,omitempty"`  // "--color-primary"
    Tailwind string `json:"tailwind,omitempty"` // "text-primary"
}
```

#### Token Discovery
- [ ] Scan CSS custom properties (--var-name)
- [ ] Parse Tailwind theme config
- [ ] Parse design system JSON/JS exports
- [ ] Map values to semantic names

#### Design Token Skill
- [ ] `get_design_token` - Get token by category/name
- [ ] `suggest_token` - Find token for a use case
- [ ] Returns value, CSS var, Tailwind class if available
- [ ] Warns when using raw value instead of token

**Tests:**
- [ ] Parses CSS variables correctly
- [ ] Parses Tailwind config correctly
- [ ] Suggestion finds appropriate tokens
- [ ] Multiple design system formats supported
- [ ] Raw value warning triggers

---

## Agent Efficiency Token Savings

### Additional Savings from Efficiency Techniques

| Technique | Savings Per Use | Frequency | Monthly Impact |
|-----------|-----------------|-----------|----------------|
| **Scratchpad** | ~50 tokens (avoids re-explain) | ~20% of queries | ~2% |
| **Style Inference** | ~100 tokens (no style questions) | ~15% of edits | ~1.5% |
| **Component Registry** | ~200 tokens (avoids duplication) | ~10% of UI work | ~1% |
| **Mistake Memory** | ~150 tokens (avoids repeat errors) | ~8% of builds | ~1% |
| **Diff Preview** | ~100 tokens (confident changes) | ~25% of edits | ~1.5% |
| **Preference Learning** | ~75 tokens (no clarification) | ~30% of queries | ~2% |
| **File Snapshot** | ~50 tokens (no re-read) | ~40% of file ops | ~1.5% |
| **Dependency Awareness** | ~100 tokens (correct imports) | ~15% of edits | ~1% |
| **Task Continuity** | ~300 tokens (resume vs restart) | ~5% of sessions | ~0.5% |
| **Design Tokens** | ~75 tokens (correct values) | ~20% of UI work | ~0.5% |
| **TOTAL** | | | **~12.5%** |

### Updated Cost Projections

| Scenario | Monthly Cost | vs Baseline |
|----------|-------------|-------------|
| Baseline (no optimization) | $285/month | - |
| Cache only | $94/month | 67% savings |
| Cache + VectorGraphDB | $72/month | 75% savings |
| Cache + VectorGraphDB + XOR | $60/month | ~80% savings |
| **+ Agent Efficiency** | **$45/month** | **~85% savings** |

**Target**: 85% token reduction with all optimizations enabled.

---

## Skill/Agent Integrations

### 6.31 Designer Agent Implementation

New agent for UI/UX implementation tasks.

**Files to create:**
- `agents/designer/designer.go`
- `agents/designer/designer_test.go`
- `agents/designer/skills.go`

**Acceptance Criteria:**

#### Core Designer Agent
- [ ] Designer agent struct with standard agent interface
- [ ] Session-scoped state for design context
- [ ] Integration with Guide for routing
- [ ] LLM model selection (Sonnet for speed)

#### Designer System Prompt

**System Prompt Core Principles:**
- [ ] Identity: UI/UX Designer agent specializing in clean, modular, testable, accessible interfaces
- [ ] UI Quality Imperatives defined:
  - [ ] Clean, modular, testable component architecture
  - [ ] Smooth transitions and animations (respecting prefers-reduced-motion)
  - [ ] Excellent legibility and visibility at all viewport sizes
  - [ ] Meet a variety of user sight needs and preferences
  - [ ] Enforce framework and web standards throughout
  - [ ] Code must be ACCESSIBLE, PERFORMANT, MAINTAINABLE, and BEAUTIFUL

**Pre-Implementation Checklist (BEFORE WRITING ANY UI CODE):**
- [ ] CONSULT LIBRARIAN for existing styling standards, tooling, linting/formatting rules
- [ ] CONSULT LIBRARIAN for existing styled components and design patterns
- [ ] CONSULT ACADEMIC for best practices for the interface requirements
- [ ] CONSULT ACADEMIC for tooling/libraries/frameworks best practices
- [ ] Be STEADFAST in adhering to existing patterns and standards

**Forbidden Behaviors (YOU DO NOT):**
- [ ] Skip knowledge agent consultations
- [ ] Ignore existing styling standards or design tokens
- [ ] Sacrifice accessibility for aesthetics
- [ ] Use hardcoded values over design tokens
- [ ] Assume - always consult when uncertain
- [ ] Create components without searching for existing ones first

**Knowledge Agent Consultation (MANDATORY):**
- [ ] Librarian: MANDATORY before implementation (existing standards, components, patterns)
- [ ] Academic: MANDATORY when interface requirements are unclear
- [ ] Archivalist: Query for past UI issues with similar changes
- [ ] Emit consultation requests with proper routing (KNOWLEDGE_QUERY type)

**10-Step Implementation Protocol:**
- [ ] Step 1: CONSULT LIBRARIAN - Get existing styling standards, tooling, styled components
- [ ] Step 2: CONSULT ACADEMIC - Get best practices for interface requirements and frameworks
- [ ] Step 3: REVIEW CRITERIA - Study implementation and acceptance criteria in task
- [ ] Step 4: QUERY ARCHIVALIST - Check for past issues with similar UI changes
- [ ] Step 5: SEARCH COMPONENTS - Use designer_search_components before creating anything new
- [ ] Step 6: IMPLEMENT - Follow existing patterns, use design tokens, maximize accessibility
- [ ] Step 7: VALIDATE TOKENS - Use designer_validate_tokens before completing
- [ ] Step 8: CHECK ACCESSIBILITY - Use designer_check_accessibility before marking complete
- [ ] Step 9: VERIFY - Ensure acceptance criteria are met
- [ ] Step 10: EMIT ARTIFACTS - Provide component references, token usage, accessibility results

**Critical Reminders:**
- [ ] ALWAYS consult Librarian and Academic before implementing - MANDATORY
- [ ] NEVER sacrifice accessibility for aesthetics
- [ ] ALWAYS adhere to existing styling standards - be STEADFAST
- [ ] Take EXTRA TIME to make UIs functional, performant, and beautiful

#### Designer Skills (Tier 1 - Core)
- [ ] `create_component` - Create new UI component
- [ ] `style_component` - Apply styling to component
- [ ] `get_design_tokens` - Retrieve design system tokens
- [ ] `find_component` - Find existing UI components
- [ ] `preview_ui` - Preview UI changes in isolation
- [ ] `check_accessibility` - Check a11y compliance

#### Designer Skills (Tier 2 - Contextual)
- [ ] `analyze_layout` - Suggest layout improvements
- [ ] `suggest_animation` - Suggest appropriate animations
- [ ] `audit_consistency` - Audit design consistency
- [ ] `generate_variants` - Generate component variants
- [ ] `consult_engineer` - Consult on implementation
- [ ] `consult_academic` - Research UI/UX best practices

#### Designer Skills (Tier 3 - Specialized)
- [ ] `design_system_scaffold` - Scaffold complete design system
- [ ] `theme_migration` - Migrate between themes
- [ ] `responsive_audit` - Full responsive audit

#### Guide Routing Integration
- [ ] Route UI/UX intents to Designer
- [ ] Keywords: "component", "ui", "style", "css", "tailwind", "design", "a11y"
- [ ] Handoff patterns between Designer and Engineer

#### Designer Hooks
- [ ] Pre-execute hook: load relevant skills based on task
- [ ] Post-execute hook: record result in Archivalist
- [ ] Pre-component-create hook: search for existing similar component
- [ ] Post-component-create hook: update component registry
- [ ] Pre-styling hook: validate design token availability
- [ ] Post-styling hook: trigger accessibility check

#### Memory Management & Pipeline Handoff

**Model**: Gemini 3 Pro

**CRITICAL**: At 95%, Designer triggers PIPELINE HANDOFF, NOT local compaction.

**Files to add:**
- `agents/designer/memory.go`
- `agents/designer/handoff.go`

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 95% threshold: trigger pipeline handoff sequence
- [ ] Bundle complete handoff state: original prompt + accomplished + remaining + components created/modified
- [ ] Include design-specific state: token usage, component changes, a11y issues
- [ ] Send `HANDOFF_REQUEST` to Architect (via Guide) with bundled state
- [ ] Collect UIInspector state before handoff (token violations, a11y issues, responsive issues)
- [ ] Collect UITester state before handoff (visual tests, a11y tests, responsive tests, keyboard tests)
- [ ] Wait for `HANDOFF_ACK` confirming new pipeline created
- [ ] Handoff state goes to Archivalist for persistence (category: `designer_pipeline_handoff`)
- [ ] Retry logic: 3 attempts with exponential backoff
- [ ] Fallback: if handoff fails after retries, summarize → Archivalist → compact locally

```go
// Designer handoff state (sent to Architect)
type DesignerHandoffState struct {
    PipelineID          PipelineID          `json:"pipeline_id"`
    OriginalPrompt      string              `json:"original_prompt"`
    Accomplished        []string            `json:"accomplished"`
    ComponentsCreated   []ComponentInfo     `json:"components_created"`
    ComponentsModified  []ComponentChange   `json:"components_modified"`
    TokensUsed          []TokenUsage        `json:"tokens_used"`
    Remaining           []string            `json:"remaining"`
    ContextNotes        string              `json:"context_notes"`
    ContextUsage        float64             `json:"context_usage"`
    HandoffReason       string              `json:"handoff_reason"`
}

type ComponentInfo struct {
    Name        string   `json:"name"`
    Path        string   `json:"path"`
    Type        string   `json:"type"`        // "component", "layout", "page"
    Props       []string `json:"props"`
    Exports     []string `json:"exports"`
}

type ComponentChange struct {
    Name        string `json:"name"`
    Path        string `json:"path"`
    ChangeType  string `json:"change_type"` // "props", "styling", "structure", "a11y"
    Description string `json:"description"`
}

type TokenUsage struct {
    TokenName string `json:"token_name"`
    Category  string `json:"category"`    // "color", "spacing", "typography"
    UsedIn    string `json:"used_in"`
}

// UIInspector handoff state (when Designer triggers handoff)
type UIInspectorHandoffState struct {
    TokenViolations       []TokenViolation   `json:"token_violations"`
    A11yIssuesFound       []A11yIssue        `json:"a11y_issues_found"`
    A11yIssuesResolved    []A11yIssue        `json:"a11y_issues_resolved"`
    A11yIssuesRemaining   []A11yIssue        `json:"a11y_issues_remaining"`
    ResponsiveIssues      []ResponsiveIssue  `json:"responsive_issues"`
    ComponentReuseConcerns []ReuseConcern    `json:"component_reuse_concerns"`
    ValidationState       map[string]bool    `json:"validation_state"`
}

// UITester handoff state (when Designer triggers handoff)
type UITesterHandoffState struct {
    VisualTests      []VisualTestResult    `json:"visual_tests"`
    A11yTests        []A11yTestResult      `json:"a11y_tests"`
    ResponsiveTests  []ResponsiveTestResult `json:"responsive_tests"`
    KeyboardNavTests []KeyboardTestResult  `json:"keyboard_nav_tests"`
    ThemeTests       []ThemeTestResult     `json:"theme_tests"`
    CoverageNeeded   []string              `json:"coverage_needed"`
}

// Designer context monitoring
func (d *Designer) checkContextAndHandoff() error {
    usage := d.getContextUsage()

    if usage >= 0.95 {
        // Trigger pipeline handoff (NOT local compaction)
        return d.triggerPipelineHandoff()
    }
    return nil
}

func (d *Designer) triggerPipelineHandoff() error {
    // Collect state from UIInspector and UITester
    uiInspectorState := d.pipeline.Inspector.GetHandoffState()
    uiTesterState := d.pipeline.Tester.GetHandoffState()

    state := d.buildHandoffState()
    bundledState := &DesignerPipelineHandoff{
        DesignerState:    state,
        UIInspectorState: uiInspectorState,
        UITesterState:    uiTesterState,
    }

    // Send to Architect via Guide
    msg := &Message{
        Type:    "HANDOFF_REQUEST",
        From:    d.ID,
        To:      "architect",  // Routed through Guide
        Payload: bundledState,
    }

    // Retry logic
    for attempt := 0; attempt < 3; attempt++ {
        if err := d.bus.Publish("guide.route", msg); err != nil {
            time.Sleep(time.Duration(1<<attempt) * time.Second)
            continue
        }

        // Wait for acknowledgment
        select {
        case ack := <-d.handoffAck:
            return nil  // Handoff successful
        case <-time.After(30 * time.Second):
            continue  // Retry
        }
    }

    // Fallback: summarize and compact locally
    return d.fallbackCompact()
}
```

**Handoff Flow:**
```
Designer (95%) ──────────────────────────────────────────────────────────────►
    │
    ▼
[Build Handoff State]
    │ - Original prompt
    │ - Accomplished tasks
    │ - Components created/modified
    │ - Tokens used
    │ - Remaining tasks
    │ - Context notes
    │
    ▼
[Collect UIInspector State]
    │ - Token violations
    │ - A11y issues (found/resolved/remaining)
    │ - Responsive issues
    │ - Component reuse concerns
    │
    ▼
[Collect UITester State]
    │ - Visual test results
    │ - A11y test results
    │ - Responsive test results
    │ - Keyboard/theme test results
    │
    ▼
[HANDOFF_REQUEST] ──► Guide ──► Architect
                                   │
                                   ▼
                          [Examine state]
                          [Adjust workflow if needed]
                                   │
                                   ▼
               [CREATE_PIPELINE_WITH_STATE] ──► Guide ──► Orchestrator
                                                            │
                                                            ▼
                                                    [Create new Designer pipeline]
                                                    [Transfer D+UIInsp+UITest state]
                                                    [Close old pipeline]
                                                            │
                                                            ▼
                                                    [HANDOFF_COMPLETE] ──► Architect ──► Designer
```

#### UIInspector Memory Management

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact locally)

**NOTE**: UIInspector compacts LOCALLY. It does NOT trigger pipeline handoff (only Designer can).

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint summary to Archivalist
- [ ] At 95% threshold: summarize + send to Archivalist + compact locally
- [ ] If Designer triggers handoff, UIInspector participates in state transfer

**Checkpoint Summary:**
```go
type UIInspectorFindingsSummary struct {
    Timestamp           time.Time         `json:"timestamp"`
    SessionID           string            `json:"session_id"`
    PipelineID          string            `json:"pipeline_id,omitempty"`
    ContextUsage        float64           `json:"context_usage"`

    // Findings state
    ChecksPerformed     []string          `json:"checks_performed"`
    TokenViolationsFound int              `json:"token_violations_found"`
    TokenViolationsFixed int              `json:"token_violations_fixed"`
    TokenViolationsRemaining []TokenViolation `json:"token_violations_remaining"`
    A11yIssuesFound     int               `json:"a11y_issues_found"`
    A11yIssuesResolved  int               `json:"a11y_issues_resolved"`
    A11yIssuesRemaining []A11yIssue       `json:"a11y_issues_remaining"`
    ResponsiveIssues    []ResponsiveIssue `json:"responsive_issues"`
    ComponentReuseConcerns []ReuseConcern `json:"component_reuse_concerns"`
    ValidationState     map[string]bool   `json:"validation_state"` // category → pass
}
```

**Archivalist Category**: `ui_inspector_findings`

**Tests:**
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Designer triggers handoff

#### UITester Memory Management

**Model**: OpenAI Codex 5.2

**Thresholds**: 85% (checkpoint) | 95% (compact locally)

**NOTE**: UITester compacts LOCALLY. It does NOT trigger pipeline handoff (only Designer can).

**Acceptance Criteria:**
- [ ] Context usage monitoring (poll every response)
- [ ] At 85% threshold: checkpoint summary to Archivalist
- [ ] At 95% threshold: summarize + send to Archivalist + compact locally
- [ ] If Designer triggers handoff, UITester participates in state transfer

**Checkpoint Summary:**
```go
type UITesterSummary struct {
    Timestamp           time.Time              `json:"timestamp"`
    SessionID           string                 `json:"session_id"`
    PipelineID          string                 `json:"pipeline_id,omitempty"`
    ContextUsage        float64                `json:"context_usage"`

    // Test state
    VisualTestsRun      []VisualTestResult     `json:"visual_tests_run"`
    VisualPassCount     int                    `json:"visual_pass_count"`
    VisualFailCount     int                    `json:"visual_fail_count"`
    A11yTestsRun        []A11yTestResult       `json:"a11y_tests_run"`
    ResponsiveTestsRun  []ResponsiveTestResult `json:"responsive_tests_run"`
    KeyboardTestsRun    []KeyboardTestResult   `json:"keyboard_tests_run"`
    ThemeTestsRun       []ThemeTestResult      `json:"theme_tests_run"`
    CoverageNeeded      []string               `json:"coverage_needed"`
}
```

**Archivalist Category**: `ui_tester_summary`

**Tests:**
- [ ] Test checkpoint at 85% context
- [ ] Test local compaction at 95% context
- [ ] Test handoff state bundling when Designer triggers handoff

**Tests:**
- [ ] Designer agent responds to UI intents
- [ ] All core skills work correctly
- [ ] Guide routes to Designer appropriately
- [ ] Designer/Engineer handoff works
- [ ] Test handoff trigger at 95% context
- [ ] Test handoff retry logic
- [ ] Test handoff fallback to local compaction
- [ ] Test handoff state bundling completeness (Designer + UIInspector + UITester)

---

### 6.32 Skill/Agent Integration Matrix

Integrate efficiency techniques with appropriate agents per the mapping.

**Reference Matrix:**

| Technique              | Guide | Architect | Engineer | Inspector | Tester | Designer |
|------------------------|-------|-----------|----------|-----------|--------|----------|
| Scratchpad Memory      |   -   |     ✓     |    ✓     |     ✓     |   ✓    |    ✓     |
| Style Inference        |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Component Registry     |   -   |     -     |    ✓     |     -     |   -    |    ✓     |
| Mistake Memory         |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Diff Preview           |   -   |     -     |    ✓     |     -     |   -    |    ✓     |
| User Preferences       |   ✓   |     ✓     |    ✓     |     -     |   -    |    ✓     |
| File Snapshot Cache    |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Dependency Awareness   |   -   |     -     |    ✓     |     ✓     |   ✓    |    -     |
| Task Continuity        |   -   |     ✓     |    ✓     |     -     |   ✓    |    -     |
| Design Token Awareness |   -   |     -     |    ✓     |     ✓     |   -    |    ✓     |

---

### 6.33 Scratchpad Integration

Integrate scratchpad skills with: **Architect, Engineer, Inspector, Tester, Designer**

**Files to modify:**
- `agents/architect/skills.go` - Add scratchpad skills
- `agents/engineer/skills.go` - Add scratchpad skills
- `agents/inspector/skills.go` - Add scratchpad skills
- `agents/tester/skills.go` - Add scratchpad skills
- `agents/designer/skills.go` - Add scratchpad skills

**Acceptance Criteria:**
- [ ] `scratchpad_write` skill available to Architect
- [ ] `scratchpad_write` skill available to Engineer
- [ ] `scratchpad_write` skill available to Inspector
- [ ] `scratchpad_write` skill available to Tester
- [ ] `scratchpad_write` skill available to Designer
- [ ] `scratchpad_read` skill available to all above
- [ ] Session-scoped isolation verified
- [ ] Agent-specific suggested keys documented

**Suggested Keys by Agent:**
- **Architect**: `requirements`, `decisions`, `blockers`, `plan_state`
- **Engineer**: `current_task`, `tried_failed`, `partial_work`
- **Inspector**: `issues_found`, `review_notes`, `flagged_patterns`
- **Tester**: `test_cases`, `coverage_gaps`, `flaky_tests`
- **Designer**: `design_decisions`, `component_notes`, `accessibility_flags`

---

### 6.34 Style Inference Integration

Integrate style inference with: **Engineer, Inspector, Tester**

**Files to modify:**
- `agents/engineer/skills.go` - Add style check skill
- `agents/inspector/skills.go` - Add style check skill
- `agents/tester/skills.go` - Add style check skill

**Acceptance Criteria:**
- [ ] `check_style` skill available to Engineer
- [ ] `check_style` skill available to Inspector
- [ ] `check_style` skill available to Tester
- [ ] Engineer uses style before writing code
- [ ] Inspector compares against detected style
- [ ] Tester matches test style to project style

---

### 6.35 Component Registry Integration

Integrate component registry with: **Engineer, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add find_component skill
- `agents/designer/skills.go` - Add find_component skill

**Acceptance Criteria:**
- [ ] `find_component` skill available to Engineer
- [ ] `find_component` skill available to Designer
- [ ] Auto-suggest existing components before creating new
- [ ] Warn when creating duplicate component
- [ ] Designer priority access (primary user)

---

### 6.36 Mistake Memory Integration

Integrate mistake memory with: **Engineer, Inspector, Tester** (Archivalist as source)

**Files to modify:**
- `agents/engineer/skills.go` - Add recall_mistakes skill
- `agents/inspector/skills.go` - Add recall_mistakes skill
- `agents/tester/skills.go` - Add recall_mistakes skill
- `agents/archivalist/skills.go` - Add mistake storage/retrieval

**Acceptance Criteria:**
- [ ] `recall_mistakes` skill available to Engineer
- [ ] `recall_mistakes` skill available to Inspector
- [ ] `recall_mistakes` skill available to Tester
- [ ] Archivalist records mistakes from failed builds/tests
- [ ] Engineer queries before similar work
- [ ] Inspector flags known anti-patterns
- [ ] Tester knows common failure patterns

---

### 6.37 Diff Preview Integration

Integrate diff preview with: **Engineer, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add preview_diff skill
- `agents/designer/skills.go` - Add preview_diff skill

**Acceptance Criteria:**
- [ ] `preview_diff` skill available to Engineer
- [ ] `preview_diff` skill available to Designer
- [ ] Preview before large changes (>50 lines)
- [ ] Warn on sensitive file changes
- [ ] Stats: lines added/removed/changed

---

### 6.38 User Preferences Integration

Integrate user preferences with: **Guide, Architect, Engineer, Designer**

**Files to modify:**
- `agents/guide/skills.go` - Add preference skills
- `agents/architect/skills.go` - Add preference skills
- `agents/engineer/skills.go` - Add preference skills
- `agents/designer/skills.go` - Add preference skills

**Acceptance Criteria:**
- [ ] `get_preferences` skill available to Guide
- [ ] `get_preferences` skill available to Architect
- [ ] `get_preferences` skill available to Engineer
- [ ] `get_preferences` skill available to Designer
- [ ] Guide routes based on workflow preferences
- [ ] Architect adjusts verbosity per preference
- [ ] Engineer follows code style preferences
- [ ] Designer follows design style preferences

---

### 6.39 File Snapshot Cache Integration

Integrate file snapshot cache with: **Engineer, Inspector, Tester** (Librarian as source)

**Files to modify:**
- `agents/engineer/file_ops.go` - Use snapshot cache
- `agents/inspector/file_ops.go` - Use snapshot cache
- `agents/tester/file_ops.go` - Use snapshot cache
- `agents/librarian/indexer.go` - Populate snapshot cache

**Acceptance Criteria:**
- [ ] Engineer reads use snapshot cache
- [ ] Inspector reads use snapshot cache
- [ ] Tester reads use snapshot cache
- [ ] Librarian populates cache during indexing
- [ ] Cache invalidation on write operations
- [ ] Stats: cache hits, misses, evictions

---

### 6.40 Dependency Awareness Integration

Integrate dependency awareness with: **Engineer, Inspector, Tester** (Academic as source)

**Files to modify:**
- `agents/engineer/skills.go` - Add check_dependency skill
- `agents/inspector/skills.go` - Add check_dependency skill
- `agents/tester/skills.go` - Add check_dependency skill
- `agents/academic/skills.go` - Provide dependency best practices

**Acceptance Criteria:**
- [ ] `check_dependency` skill available to Engineer
- [ ] `check_dependency` skill available to Inspector
- [ ] `check_dependency` skill available to Tester
- [ ] Engineer uses correct imports
- [ ] Inspector flags deprecated deps
- [ ] Tester mocks dependencies correctly
- [ ] Academic provides version/security info

---

### 6.41 Task Continuity Integration

Integrate task continuity with: **Architect, Engineer, Tester**

**Files to modify:**
- `agents/architect/skills.go` - Add checkpoint skills
- `agents/engineer/skills.go` - Add checkpoint skills
- `agents/tester/skills.go` - Add checkpoint skills

**Acceptance Criteria:**
- [ ] `checkpoint_task` skill available to Architect
- [ ] `checkpoint_task` skill available to Engineer
- [ ] `checkpoint_task` skill available to Tester
- [ ] `resume_task` skill available to all above
- [ ] Auto-checkpoint on significant progress
- [ ] Resume generates handoff context

---

### 6.42 Design Token Integration

Integrate design tokens with: **Engineer, Inspector, Designer**

**Files to modify:**
- `agents/engineer/skills.go` - Add design token skills
- `agents/inspector/skills.go` - Add design token skills
- `agents/designer/skills.go` - Add design token skills

**Acceptance Criteria:**
- [ ] `get_design_tokens` skill available to Engineer
- [ ] `get_design_tokens` skill available to Inspector
- [ ] `get_design_tokens` skill available to Designer
- [ ] `find_design_token` skill available to all above
- [ ] Engineer uses tokens instead of hardcoded values
- [ ] Inspector flags hardcoded values
- [ ] Designer primary user of tokens

---

## Lazy Tool Loading

Universal meta-skill enabling on-demand tool schema loading for all agents. Reduces token usage by 50-70% per turn.

**Tier**: 3 (Agents) - Can be implemented in parallel with Pipelines (Tier 4) and Storage (Tier 5)

**Dependencies**:
- Phase 0: Infrastructure (message bus, hooks system)
- Agent Skill Definitions (tool schemas exist)

**Parallelizable with**:
- Single-Worker Pipeline System (Tier 4)
- Phase 6: VectorGraphDB (Tier 5)

---

### 6.50 Tool Registry Implementation

Core tool registry that stores manifests, schemas, and bundles.

**Files to create:**
- `core/tools/registry.go`
- `core/tools/manifest.go`
- `core/tools/bundle.go`

**Implementation:**

```go
// core/tools/registry.go
type ToolRegistry struct {
    mu        sync.RWMutex
    manifests map[string]*ToolManifest    // agentID -> manifest
    schemas   map[string]map[string]any   // toolName -> full schema
    bundles   map[string][]BundleEntry    // agentID -> bundles
}

func NewToolRegistry() *ToolRegistry
func (r *ToolRegistry) RegisterAgent(agentID string, tools []ToolDefinition, bundles []BundleEntry)
func (r *ToolRegistry) GetManifest(agentID string) *ToolManifest
func (r *ToolRegistry) GetSchema(toolName string) map[string]any
func (r *ToolRegistry) GetSchemas(toolNames []string) []map[string]any
func (r *ToolRegistry) GetBundle(agentID, bundleName string) *BundleEntry
```

**Acceptance Criteria:**
- [ ] `ToolRegistry` struct implemented with thread-safe access
- [ ] `RegisterAgent` stores manifest, schemas, and bundles
- [ ] `GetManifest` returns lightweight manifest (~20 tokens per tool)
- [ ] `GetSchema` returns full JSON schema for a tool
- [ ] `GetSchemas` batch retrieval for multiple tools
- [ ] `GetBundle` returns tools in a named bundle
- [ ] Unit tests for all registry operations
- [ ] Benchmark: manifest retrieval < 1ms

---

### 6.51 Tool Manifest Data Structures

Lightweight tool descriptors for agent bootstrap.

**Files to create:**
- `core/tools/manifest.go`

**Implementation:**

```go
// core/tools/manifest.go
type ToolManifestEntry struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`  // Max 100 chars
    Category    string   `json:"category"`
    BundleHint  string   `json:"bundle_hint"`
    Keywords    []string `json:"keywords"`
}

type ToolManifest struct {
    AgentID     string              `json:"agent_id"`
    Tools       []ToolManifestEntry `json:"tools"`
    Bundles     []BundleEntry       `json:"bundles"`
    TotalTokens int                 `json:"total_tokens"`
}

func (m *ToolManifest) HasTool(name string) bool
func (m *ToolManifest) GetToolsByCategory(category string) []ToolManifestEntry
func (m *ToolManifest) EstimateTokens() int
```

**Acceptance Criteria:**
- [ ] `ToolManifestEntry` limited to essential fields only
- [ ] Description auto-truncated to 100 characters
- [ ] `HasTool` O(1) lookup via internal map
- [ ] `EstimateTokens` accurate within 10% of actual
- [ ] JSON serialization produces compact output
- [ ] Unit tests for all manifest operations

---

### 6.52 Tool Bundle Definitions

Predefined tool groupings for efficient loading.

**Files to create:**
- `core/tools/bundle.go`
- `agents/designer/bundles.go`
- `agents/engineer/bundles.go`
- `agents/*/bundles.go` (for each agent)

**Implementation:**

```go
// core/tools/bundle.go
type BundleEntry struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Tools       []string `json:"tools"`
    UseCases    []string `json:"use_cases"`
}

// agents/designer/bundles.go
var DesignerBundles = []BundleEntry{
    {Name: "component", Tools: [...]},
    {Name: "design_system", Tools: [...]},
    {Name: "accessibility", Tools: [...]},
    {Name: "layout", Tools: [...]},
    {Name: "preview", Tools: [...]},
}
```

**Acceptance Criteria:**
- [ ] Designer bundles defined: component, design_system, accessibility, layout, preview
- [ ] Engineer bundles defined: file_ops, execution, consultation
- [ ] Archivalist bundles defined: query, store, timeline
- [ ] Inspector bundles defined: code_review, ui_review
- [ ] Tester bundles defined: test_execution, coverage
- [ ] Each bundle has clear use cases for auto-suggestion
- [ ] No tool appears in more than 2 bundles (avoid duplication)
- [ ] Bundle loading tested for each agent

---

### 6.53 Loaded Tool Set Cache

Per-session cache tracking which tools an agent has loaded.

**Files to create:**
- `core/tools/cache.go`

**Implementation:**

```go
// core/tools/cache.go
type LoadedToolSet struct {
    AgentID     string
    SessionID   string
    CoreTools   []string
    LoadedTools map[string]bool
    LoadHistory []LoadEvent
    mu          sync.RWMutex
}

type LoadEvent struct {
    Timestamp  time.Time
    Tools      []string
    TaskType   string
    Successful bool
}

type LoadedToolCache struct {
    sets   map[string]*LoadedToolSet  // "agentID:sessionID" -> set
    config ToolLoaderConfig
    mu     sync.RWMutex
}

func NewLoadedToolCache(config ToolLoaderConfig) *LoadedToolCache
func (c *LoadedToolCache) GetOrCreate(agentID, sessionID string) *LoadedToolSet
func (c *LoadedToolCache) RecordLoad(agentID, sessionID string, tools []string)
func (c *LoadedToolCache) IsLoaded(agentID, sessionID, toolName string) bool
func (c *LoadedToolCache) Cleanup(maxAge time.Duration)
```

**Acceptance Criteria:**
- [ ] `LoadedToolSet` tracks loaded tools per agent per session
- [ ] `LoadEvent` records loading history for pattern learning
- [ ] Cache automatically creates sets on first access
- [ ] `IsLoaded` O(1) lookup
- [ ] `Cleanup` removes expired sets (TTL-based)
- [ ] Thread-safe for concurrent access
- [ ] Memory usage < 1KB per session
- [ ] Unit tests for cache operations

---

### 6.54 Tool Loader Service

Main service coordinating tool loading operations.

**Files to create:**
- `core/tools/loader.go`
- `core/tools/config.go`

**Implementation:**

```go
// core/tools/loader.go
type ToolLoader struct {
    registry  *ToolRegistry
    cache     *LoadedToolCache
    suggester *IntentBasedSuggester
    config    ToolLoaderConfig
}

func NewToolLoader(registry *ToolRegistry, config ToolLoaderConfig) *ToolLoader
func (l *ToolLoader) Bootstrap(agentID, sessionID string) (*BootstrapResult, error)
func (l *ToolLoader) LoadTools(ctx context.Context, agentID, sessionID string, toolNames []string) (*LoadResult, error)
func (l *ToolLoader) LoadBundle(ctx context.Context, agentID, sessionID, bundleName string) (*LoadResult, error)
func (l *ToolLoader) SuggestTools(ctx context.Context, agentID, intent string) ([]string, error)
func (l *ToolLoader) GetLoadedTools(agentID, sessionID string) *LoadedToolSet

// core/tools/config.go
type ToolLoaderConfig struct {
    AlwaysLoadCore        bool
    CoreTools             []string
    MaxToolsPerRequest    int
    EnableBundles         bool
    EnableAutoSuggest     bool
    CacheLoadedTools      bool
    CacheTTL              time.Duration
    EnablePatternLearning bool
}

func DefaultToolLoaderConfig() ToolLoaderConfig
```

**Acceptance Criteria:**
- [ ] `Bootstrap` returns manifest + core tools for agent startup
- [ ] `LoadTools` validates tool names, loads schemas, updates cache
- [ ] `LoadBundle` expands bundle to tool list, delegates to LoadTools
- [ ] `SuggestTools` returns relevant tools based on intent
- [ ] `GetLoadedTools` returns current loaded set for injection
- [ ] Respects `MaxToolsPerRequest` limit
- [ ] Respects `CacheTTL` for expiration
- [ ] Error handling for unknown tools/bundles
- [ ] Unit tests for all loader operations
- [ ] Integration test: full load cycle

---

### 6.55 Request Tools Skill Implementation

Universal meta-skill for on-demand tool loading.

**Files to create:**
- `core/skills/request_tools.go`

**Implementation:**

```go
// core/skills/request_tools.go
var RequestToolsSkill = SkillDefinition{
    Name:        "request_tools",
    Description: "Load additional tools into your capabilities",
    Tier:        0,
    Domain:      "meta",
    Priority:    100,
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "tools":  {"type": "array", "items": {"type": "string"}},
            "bundle": {"type": "string"},
            "intent": {"type": "string"},
        },
    },
}

func HandleRequestTools(ctx context.Context, input map[string]any) (*ToolResult, error)
```

**Acceptance Criteria:**
- [ ] Skill registered as Tier 0 (always available)
- [ ] Accepts `tools` array OR `bundle` name OR `intent` string
- [ ] Validates all requested tools exist in manifest
- [ ] Returns list of loaded tool names
- [ ] Returns suggestions for related tools
- [ ] Errors clearly for unknown tools
- [ ] Errors clearly when exceeding MaxToolsPerRequest
- [ ] Unit tests for all input variations
- [ ] Integration test: skill invocation → schema injection

---

### 6.56 Tool Injection Hooks

Hooks for injecting tool manifest and loaded schemas.

**Files to create:**
- `core/tools/hooks.go`

**Implementation:**

```go
// core/tools/hooks.go

// Pre-prompt: Inject manifest + loaded tool schemas
var InjectToolsHook = Hook{
    Name:     "inject_loaded_tools",
    Type:     PrePrompt,
    Priority: HookPriorityFirst,
    Handler:  injectToolsHandler,
}

// Pre-tool: Validate request_tools calls
var ValidateToolLoadHook = Hook{
    Name:     "validate_tool_load",
    Type:     PreTool,
    Priority: HookPriorityFirst,
    Trigger:  "request_tools",
    Handler:  validateToolLoadHandler,
}

// Post-tool: Record loading for pattern learning
var RecordToolLoadHook = Hook{
    Name:     "record_tool_load",
    Type:     PostTool,
    Priority: HookPriorityNormal,
    Trigger:  "request_tools",
    Handler:  recordToolLoadHandler,
}

func RegisterToolLoadingHooks(agent Agent)
```

**Acceptance Criteria:**
- [ ] `InjectToolsHook` runs before other pre-prompt hooks
- [ ] Manifest injected on every turn (lightweight)
- [ ] Loaded tool schemas injected only when present
- [ ] `ValidateToolLoadHook` blocks invalid tool requests
- [ ] `RecordToolLoadHook` captures load events for learning
- [ ] Hooks registered for all agents via common function
- [ ] Unit tests for each hook
- [ ] Integration test: hook execution order

---

### 6.57 Intent-Based Tool Suggestion

Embedding-based tool suggestion from natural language intent.

**Files to create:**
- `core/tools/suggester.go`

**Implementation:**

```go
// core/tools/suggester.go
type IntentBasedSuggester struct {
    embedder    Embedder
    toolEmbeds  map[string][]float32
    bundleIndex map[string][]string
}

func NewIntentBasedSuggester(embedder Embedder) *IntentBasedSuggester
func (s *IntentBasedSuggester) IndexTools(manifest *ToolManifest) error
func (s *IntentBasedSuggester) SuggestTools(ctx context.Context, intent string, manifest *ToolManifest) ([]string, error)
func (s *IntentBasedSuggester) SuggestBundle(intent string, bundles []BundleEntry) *BundleEntry
```

**Acceptance Criteria:**
- [ ] Tools indexed by embedding their name + description + keywords
- [ ] `SuggestTools` returns top 5 tools above 0.7 similarity
- [ ] `SuggestBundle` matches intent against bundle use cases
- [ ] Suggestions cached for repeated intents
- [ ] Fallback to keyword matching if embedder unavailable
- [ ] Latency < 50ms for suggestion
- [ ] Unit tests with mock embedder
- [ ] Integration test: intent → suggested tools

---

### 6.58 Agent Integration - Designer

Integrate lazy tool loading with Designer agent.

**Files to modify:**
- `agents/designer/agent.go`
- `agents/designer/skills.go`
- `agents/designer/bundles.go`

**Implementation:**
- Register `request_tools` skill
- Register tool loading hooks
- Define Designer-specific bundles
- Update bootstrap to use manifest instead of full schemas

**Acceptance Criteria:**
- [ ] Designer starts with manifest + core tools only
- [ ] `request_tools` skill available and functional
- [ ] All 5 Designer bundles defined and loadable
- [ ] Tool loading works for explicit tool names
- [ ] Tool loading works for bundle names
- [ ] Tool loading works for intent-based suggestion
- [ ] Token usage reduced by 50%+ in simple tasks
- [ ] No regression in task completion capability
- [ ] Integration test: Designer task with lazy loading

---

### 6.59 Agent Integration - Engineer

Integrate lazy tool loading with Engineer agent.

**Files to modify:**
- `agents/engineer/agent.go`
- `agents/engineer/skills.go`
- `agents/engineer/bundles.go`

**Acceptance Criteria:**
- [ ] Engineer starts with manifest + core tools only
- [ ] `request_tools` skill available and functional
- [ ] Engineer bundles defined: file_ops, execution, consultation
- [ ] Tool loading works for all input types
- [ ] Token usage reduced by 50%+ in simple tasks
- [ ] No regression in task completion capability
- [ ] Integration test: Engineer task with lazy loading

---

### 6.60 Agent Integration - All Remaining Agents

Integrate lazy tool loading with all other agents.

**Files to modify:**
- `agents/archivalist/agent.go`, `bundles.go`
- `agents/librarian/agent.go`, `bundles.go`
- `agents/inspector/agent.go`, `bundles.go`
- `agents/tester/agent.go`, `bundles.go`
- `agents/architect/agent.go`, `bundles.go`
- `agents/academic/agent.go`, `bundles.go`

**Acceptance Criteria:**
- [ ] All agents have `request_tools` skill
- [ ] All agents have tool loading hooks registered
- [ ] All agents have appropriate bundles defined
- [ ] All agents bootstrap with manifest only
- [ ] Token savings verified for each agent
- [ ] No regression in any agent's capabilities
- [ ] Integration tests for each agent

---

### 6.61 Pattern Learning Implementation

Learn tool co-usage patterns to improve suggestions.

**Files to create:**
- `core/tools/patterns.go`

**Implementation:**

```go
// core/tools/patterns.go
type PatternLearner struct {
    coUsage     map[string]map[string]int  // tool -> tool -> count
    taskBundles map[string][]string        // taskType -> commonly used tools
    mu          sync.RWMutex
}

func NewPatternLearner() *PatternLearner
func (p *PatternLearner) RecordUsage(taskType string, tools []string, successful bool)
func (p *PatternLearner) GetCoUsedTools(tool string, limit int) []string
func (p *PatternLearner) GetTaskBundle(taskType string) []string
func (p *PatternLearner) Export() *PatternData
func (p *PatternLearner) Import(data *PatternData)
```

**Acceptance Criteria:**
- [ ] Records which tools are used together
- [ ] Records which tools are used for which task types
- [ ] Only records successful task completions
- [ ] `GetCoUsedTools` returns commonly co-used tools
- [ ] `GetTaskBundle` returns tools commonly used for task type
- [ ] Patterns exportable/importable for persistence
- [ ] Patterns improve suggestions over time
- [ ] Unit tests for pattern operations

---

### 6.62 Lazy Tool Loading Metrics & Monitoring

Metrics for monitoring lazy loading effectiveness.

**Note:** Sylk is a terminal CLI application, NOT a distributed system. We use in-memory metrics with CLI display and optional file export - NOT Prometheus or Grafana.

**Files to create:**
- `core/tools/metrics.go`

**Implementation:**

```go
// core/tools/metrics.go
type ToolLoadingMetrics struct {
    mu                     sync.RWMutex
    BootstrapTokensSaved   int64                    // atomic counter
    ToolsLoadedTotal       int64                    // atomic counter
    BundlesLoadedTotal     int64                    // atomic counter
    SuggestionsAccepted    int64                    // atomic counter
    SuggestionsRejected    int64                    // atomic counter
    CacheHits              int64                    // for rate calculation
    CacheMisses            int64                    // for rate calculation
    ToolsPerTask           []int                    // for average calculation
    LoadLatencies          []time.Duration          // for histogram calculation
    ByAgent                map[string]*AgentMetrics // per-agent breakdown
}

type AgentMetrics struct {
    TokensSaved   int64
    ToolsLoaded   int64
    LoadLatencies []time.Duration
}

// Global singleton
var toolMetrics = &ToolLoadingMetrics{
    ByAgent: make(map[string]*AgentMetrics),
}

func RecordToolLoad(agentID string, toolCount int, latency time.Duration)
func RecordTokenSavings(agentID string, savedTokens int)
func RecordCacheHit()
func RecordCacheMiss()
func RecordSuggestionAccepted()
func RecordSuggestionRejected()

// For CLI display
func GetMetricsSummary() *MetricsSummary
func GetAgentMetrics(agentID string) *AgentMetrics
func ExportMetricsJSON() ([]byte, error)

// Histogram calculations
func (m *ToolLoadingMetrics) LoadLatencyP50() time.Duration
func (m *ToolLoadingMetrics) LoadLatencyP95() time.Duration
func (m *ToolLoadingMetrics) LoadLatencyP99() time.Duration
func (m *ToolLoadingMetrics) CacheHitRate() float64
func (m *ToolLoadingMetrics) AverageToolsPerTask() float64
```

**Acceptance Criteria:**
- [ ] In-memory metrics tracked with atomic operations
- [ ] Token savings tracked per agent
- [ ] Tool load count tracked per agent and total
- [ ] Cache hit rate calculated from hits/misses
- [ ] Suggestion acceptance rate tracked
- [ ] Load latency percentiles calculated (p50, p95, p99)
- [ ] `sylk metrics tools` CLI command displays tool loading metrics
- [ ] `sylk metrics tools --json` exports as JSON
- [ ] Metrics persist to SQLite for cross-session analysis (optional)
- [ ] Session summary includes tool loading efficiency stats

---

### 6.63 End-to-End Testing

Comprehensive testing for lazy tool loading system.

**Files to create:**
- `core/tools/loader_test.go`
- `core/tools/integration_test.go`
- `tests/e2e/lazy_loading_test.go`

**Test Scenarios:**

1. **Bootstrap Test**: Agent starts with manifest only
2. **Explicit Load Test**: Load specific tools by name
3. **Bundle Load Test**: Load tools via bundle name
4. **Intent Load Test**: Load tools via intent description
5. **Cache Test**: Tools remain loaded across turns
6. **Expiration Test**: Tools expire after TTL
7. **Concurrent Test**: Multiple sessions load tools simultaneously
8. **Error Test**: Unknown tool names rejected
9. **Limit Test**: MaxToolsPerRequest enforced
10. **Token Savings Test**: Verify 50%+ reduction

**Acceptance Criteria:**
- [ ] All 10 test scenarios passing
- [ ] Unit test coverage > 80%
- [ ] Integration tests for each agent
- [ ] E2E test for full task completion with lazy loading
- [ ] Performance benchmark: load latency < 10ms
- [ ] Memory benchmark: < 1KB per session overhead
- [ ] Token savings benchmark: 50-70% reduction verified

---

## Git Tooling

Agent-specific git capabilities with permission enforcement.

### 6.70 Git Permission Layer

Core permission system for git operations.

**Files to create:**
- `core/git/permissions.go`
- `core/git/permission_set.go`
- `core/git/validators.go`
- `core/git/permissions_test.go`

**Acceptance Criteria:**

#### Permission Types
- [ ] `GitPermission` enum: `Read`, `Commit`, `Branch`, `History`, `Remote`, `Clone`
- [ ] `PermissionLevel` enum: `None`, `ReadOnly`, `Scoped`, `Full`
- [ ] `GitPermissionSet` struct with agent-permission mappings
- [ ] `PermissionCondition` for conditional permissions (e.g., Tester commits)

#### Permission Matrix
- [ ] Engineer: `Read`, `Commit`, `Branch` (Full)
- [ ] Designer: `Read`, `Commit`, `Branch` (Full)
- [ ] Inspector: `Read`, `History` (ReadOnly)
- [ ] Tester: `Read`, `Commit` (Conditional on test pass)
- [ ] Architect: `Read`, `Commit`, `Branch`, `History`, `Remote` (Scoped to repo)
- [ ] Librarian: `Read`, `History`, `Clone` (ReadOnly + temp clone)

#### Validation
- [ ] `HasPermission(agentID, permission)` check
- [ ] `ValidateOperation(agentID, operation)` pre-execution hook
- [ ] `EnforceRepoScope(operation)` for Architect operations
- [ ] Permission denial returns clear error with reason

---

### 6.71 Git Read Skills

Read-only git operations available to all agents.

**Files to create:**
- `skills/git/read_skills.go`
- `skills/git/read_skills_test.go`

**Acceptance Criteria:**

#### Status Operations
- [ ] `git_status` skill: Returns working tree state
- [ ] `git_diff` skill: Shows unstaged changes
- [ ] `git_diff_staged` skill: Shows staged changes
- [ ] Output formatting for agent consumption (structured JSON)

#### Log Operations
- [ ] `git_log` skill: Recent commits with configurable count
- [ ] `git_log_file` skill: History for specific file
- [ ] `git_show` skill: Details of specific commit
- [ ] Pagination support for large histories

#### Blame Operations
- [ ] `git_blame` skill: Line-by-line attribution
- [ ] `git_blame_range` skill: Blame for specific line range
- [ ] Cache blame results per session for performance

#### Branch Information
- [ ] `git_branch_list` skill: List all branches
- [ ] `git_branch_current` skill: Get current branch name
- [ ] `git_branch_compare` skill: Compare two branches

---

### 6.72 Git Commit Skills

Commit operations for authorized agents.

**Files to create:**
- `skills/git/commit_skills.go`
- `skills/git/commit_message.go`
- `skills/git/commit_skills_test.go`

**Acceptance Criteria:**

#### Staging Operations
- [ ] `git_add` skill: Stage specific files
- [ ] `git_add_all` skill: Stage all changes
- [ ] `git_reset` skill: Unstage files
- [ ] Validate file paths exist before staging

#### Commit Operations
- [ ] `git_commit` skill: Create commit with message
- [ ] Conventional commit format enforcement
- [ ] Message generation from task context
- [ ] Atomic commit validation (no partial commits)

#### Commit Message Generation
- [ ] `GenerateCommitMessage(changes, taskContext)` helper
- [ ] Type inference: `feat`, `fix`, `refactor`, `test`, `docs`, `style`
- [ ] Scope extraction from file paths
- [ ] Description generation from change summary

#### Commit Validation
- [ ] Pre-commit validation hook integration
- [ ] Empty commit prevention
- [ ] Large file warning (> 1MB)
- [ ] Binary file handling

---

### 6.73 Auto-Commit Hook (Engineer/Designer)

Automatic commits on successful task completion.

**Files to create:**
- `hooks/git/auto_commit.go`
- `hooks/git/auto_commit_test.go`

**Acceptance Criteria:**

#### Hook Configuration
- [ ] `AutoCommitHook` struct implementing `Hook` interface
- [ ] Trigger: `signal_complete` from worker
- [ ] Priority: `HookPriorityLast` (after all other hooks)
- [ ] Agent filter: Engineer and Designer only

#### Commit Logic
- [ ] Detect uncommitted changes via `git status`
- [ ] Generate commit message from completed task
- [ ] Stage only files modified during task
- [ ] Create atomic commit

#### Message Format
```
<type>(<scope>): <description>

Task: <task_id>
Agent: <agent_type>
Session: <session_id>
```

#### Error Handling
- [ ] Skip commit if no changes
- [ ] Log but don't fail if commit fails
- [ ] Retry once on transient failures
- [ ] Emit `commit_skipped` event if nothing to commit

---

### 6.74 Inspector Read-Only Skills

Git read capabilities for Inspector with enforcement.

**Files to create:**
- `skills/git/inspector_skills.go`
- `skills/git/inspector_skills_test.go`

**Acceptance Criteria:**

#### Available Skills
- [ ] All read skills from 6.71
- [ ] `git_diff_review` skill: Formatted diff for code review
- [ ] `git_history_summary` skill: Condensed recent history
- [ ] `git_change_impact` skill: Files affected by recent changes

#### Enforcement
- [ ] Reject any write operations (commit, branch create, etc.)
- [ ] Clear error message: "Inspector agents have read-only git access"
- [ ] Log attempted write operations for audit

#### Review Integration
- [ ] Diff output optimized for review comments
- [ ] Line number preservation for review anchoring
- [ ] Context lines configurable (default: 3)

---

### 6.75 Tester Conditional Commit Hook

Conditional commits based on test results.

**Files to create:**
- `hooks/git/tester_commit.go`
- `hooks/git/test_evaluation.go`
- `hooks/git/tester_commit_test.go`

**Acceptance Criteria:**

#### Commit Conditions
- [ ] `TestPassCondition`: All tests pass
- [ ] `TestImproveCondition`: Coverage improved OR failures reduced
- [ ] `TestAddedCondition`: New tests added (regardless of other test state)
- [ ] Conditions evaluated via `evaluateCommitCondition(current, previous)`

#### Evaluation Logic
```go
type CommitDecision struct {
    ShouldCommit bool
    Reason       string
    Condition    CommitConditionType
}
```

#### Test Result Tracking
- [ ] Store previous test results per session
- [ ] Compare current vs previous for improvement detection
- [ ] Track: `passed`, `failed`, `skipped`, `coverage`

#### Commit Message
- [ ] Include test result summary in commit message
- [ ] Format: `test(<scope>): <description> [pass: X, fail: Y, coverage: Z%]`

#### Rejection Handling
- [ ] Clear explanation when commit rejected
- [ ] Suggest: "Fix failing tests before committing"
- [ ] Option to force commit with `--allow-failures` flag (logged)

---

### 6.76 Architect Branch/History/Remote Skills

Extended git capabilities for Architect with repo scoping.

**Files to create:**
- `skills/git/architect_skills.go`
- `skills/git/repo_scope.go`
- `skills/git/architect_skills_test.go`

**Acceptance Criteria:**

#### Branch Operations
- [ ] `git_branch_create` skill: Create new branch
- [ ] `git_branch_delete` skill: Delete branch (with safety checks)
- [ ] `git_branch_rename` skill: Rename branch
- [ ] `git_checkout` skill: Switch branches
- [ ] `git_merge` skill: Merge branches (no force)

#### History Operations
- [ ] `git_revert` skill: Revert specific commit
- [ ] `git_cherry_pick` skill: Cherry-pick commit
- [ ] `git_stash` skill: Stash/unstash changes
- [ ] NO `git reset --hard` or destructive operations

#### Remote Operations
- [ ] `git_fetch` skill: Fetch from remote
- [ ] `git_pull` skill: Pull with rebase option
- [ ] `git_push` skill: Push to remote (current branch only)
- [ ] `git_remote_list` skill: List remotes

#### Repo Scoping
- [ ] All operations scoped via `git rev-parse --show-toplevel`
- [ ] Reject operations outside current repo
- [ ] Path validation before any file operations
- [ ] Log scope violations for audit

---

### 6.77 Librarian Read-Only + Clone Skills

Git capabilities for Librarian with temp clone support.

**Files to create:**
- `skills/git/librarian_skills.go`
- `skills/git/temp_clone.go`
- `skills/git/librarian_skills_test.go`

**Acceptance Criteria:**

#### Read Operations
- [ ] All read skills from 6.71
- [ ] `git_search_history` skill: Search commits by message/author
- [ ] `git_file_history` skill: Full history of specific file
- [ ] `git_contributors` skill: List contributors with stats

#### Clone to Temp
- [ ] `git_clone_temp` skill: Clone repo to temp directory
- [ ] Clone destination: `os.TempDir()/sylk-clone-<session_id>-<hash>`
- [ ] Shallow clone by default (`--depth 1`)
- [ ] Option for full clone with explicit flag

#### Temp Clone Management
- [ ] Track cloned repos per session
- [ ] Auto-cleanup on session end
- [ ] Max 3 active clones per session
- [ ] Clone expiration: 1 hour max lifetime

#### Clone Operations (Read-Only)
- [ ] All read operations available on cloned repos
- [ ] No write operations on clones
- [ ] Clear error: "Cloned repositories are read-only"

---

### 6.78 Permission Enforcement Hook

Pre-execution permission validation.

**Files to create:**
- `hooks/git/permission_hook.go`
- `hooks/git/permission_hook_test.go`

**Acceptance Criteria:**

#### Hook Configuration
- [ ] `GitPermissionHook` implementing `Hook` interface
- [ ] Type: `PreTool`
- [ ] Priority: `HookPriorityFirst` (before any execution)
- [ ] Applies to all git skills

#### Validation Logic
- [ ] Extract agent ID from context
- [ ] Extract operation type from skill name
- [ ] Check permission matrix
- [ ] Return `HookResultBlock` if denied

#### Denial Response
```go
type PermissionDenial struct {
    AgentID     string
    Operation   string
    Required    GitPermission
    Reason      string
    Suggestion  string
}
```

#### Condition Evaluation
- [ ] For conditional permissions (Tester), evaluate condition
- [ ] Pass context to condition evaluator
- [ ] Return denial with condition explanation if not met

---

### 6.79 Git Audit Hook

Comprehensive audit logging for git operations.

**Files to create:**
- `hooks/git/audit_hook.go`
- `core/git/audit_log.go`
- `hooks/git/audit_hook_test.go`

**Acceptance Criteria:**

#### Audit Events
- [ ] `GitOperationAttempted`: Before execution
- [ ] `GitOperationCompleted`: After successful execution
- [ ] `GitOperationFailed`: After failed execution
- [ ] `GitPermissionDenied`: When permission check fails

#### Audit Log Entry
```go
type GitAuditEntry struct {
    Timestamp   time.Time
    SessionID   string
    AgentID     string
    AgentType   string
    Operation   string
    Parameters  map[string]any
    Result      string  // "success", "failure", "denied"
    Error       string  // if applicable
    Duration    time.Duration
}
```

#### Storage
- [ ] Write to `~/.sylk/audit/git-<date>.log`
- [ ] JSON Lines format for easy parsing
- [ ] Rotate daily, retain 30 days
- [ ] Async write to avoid blocking operations

#### Query Support
- [ ] `QueryAuditLog(filter AuditFilter)` function
- [ ] Filter by: session, agent, operation, result, time range
- [ ] Support for Archivalist querying audit history

---

### 6.80 Git Tooling Integration Tests

End-to-end testing for git tooling system.

**Files to create:**
- `tests/e2e/git_tooling_test.go`
- `tests/e2e/git_permissions_test.go`
- `tests/fixtures/test_repo_setup.go`

**Test Scenarios:**

1. **Engineer Auto-Commit**: Task completion triggers atomic commit
2. **Designer Auto-Commit**: UI task completion triggers commit
3. **Inspector Read-Only**: Write operations rejected with clear error
4. **Tester Pass Commit**: Tests pass → commit allowed
5. **Tester Fail No Commit**: Tests fail → commit rejected
6. **Tester Improve Commit**: Coverage improves → commit allowed
7. **Architect Branch Ops**: Create/switch/merge branches
8. **Architect Repo Scope**: Operations outside repo rejected
9. **Librarian Clone**: Clone to temp, read ops work, write rejected
10. **Librarian Cleanup**: Temp clones removed on session end
11. **Permission Denial**: Clear error messages for all denial types
12. **Audit Trail**: All operations logged correctly

**Acceptance Criteria:**
- [ ] All 12 test scenarios passing
- [ ] Test repo setup/teardown automated
- [ ] Permission matrix exhaustively tested
- [ ] Audit log entries verified
- [ ] Error messages user-friendly
- [ ] No orphaned temp directories after tests
- [ ] Concurrent operation safety verified

---

## Analysis Skills Orchestration

Context-aware analysis skills that orchestrate existing tools without reimplementing them.

### 6.90 Analysis Skills Foundation

Core types and interfaces for analysis skill orchestration.

**Files to create:**
- `skills/analysis/types.go`
- `skills/analysis/orchestrator.go`
- `skills/analysis/context.go`
- `skills/analysis/types_test.go`

**Acceptance Criteria:**

#### Core Types
- [ ] `AnalysisContext` struct with task, session, changed files, conversation
- [ ] `PrioritizedFinding` struct with finding, priority, historical bugs, explanation
- [ ] `ChangeRiskAssessment` struct with risk score, findings, blast radius, summary
- [ ] `Priority` enum: `Critical`, `High`, `Medium`, `Low`

#### Orchestrator Interface
```go
type AnalysisOrchestrator interface {
    // Parallel tool invocation
    GatherData(ctx context.Context, files []string) (*AnalysisData, error)

    // Context injection
    EnrichWithContext(findings []Finding, ctx *AnalysisContext) []PrioritizedFinding

    // Historical correlation
    CorrelateWithHistory(findings []PrioritizedFinding, history []BugRecord) []PrioritizedFinding
}
```

- [ ] `AnalysisOrchestrator` interface defined
- [ ] `BaseOrchestrator` implementation with parallel tool calls
- [ ] Context extraction from session/task
- [ ] Error aggregation from multiple tool calls

#### Non-Redundancy Enforcement
- [ ] Skills MUST call existing skills, never external tools directly
- [ ] Lint findings come from `lint_run` skill, not `golangci-lint`
- [ ] Diagnostics come from `lsp_diagnostics` skill, not `gopls`
- [ ] Coverage comes from `test_coverage` skill, not `go test`

---

### 6.91 Inspector: Assess Change Risk Skill

Prioritized findings filtered to changed code with historical correlation.

**Files to create:**
- `skills/analysis/inspector/change_risk.go`
- `skills/analysis/inspector/change_risk_test.go`

**Acceptance Criteria:**

#### Data Gathering (Parallel)
- [ ] Call `lint_run` for lint findings
- [ ] Call `lsp_diagnostics` for type diagnostics
- [ ] Call `git_changed_lines` for changed lines map
- [ ] Call `archivalist_query_bugs` for bug history
- [ ] All calls execute in parallel via `errgroup`

#### Filtering
- [ ] Filter findings to only those in changed lines
- [ ] Map finding location to changed line ranges
- [ ] Preserve finding metadata through filter

#### Prioritization
- [ ] `Critical`: Finding matches historical bug pattern
- [ ] `High`: Finding in changed code + complexity > 10
- [ ] `Medium`: Finding in changed code
- [ ] `Low`: Finding not in changed code (excluded from results)

#### Historical Correlation
- [ ] Match finding category to past bug categories
- [ ] Match code pattern to past bug patterns
- [ ] Include bug record in finding: title, date, fix
- [ ] Explanation: "This pattern caused bug #X on DATE"

#### Output
- [ ] Return top 10 prioritized findings (not all 50+)
- [ ] Include risk score (0.0-1.0)
- [ ] Include blast radius (affected files)
- [ ] Include summary for quick review

---

### 6.92 Inspector: Explain Finding Skill

Semantic explanation with project-specific fix suggestions.

**Files to create:**
- `skills/analysis/inspector/explain_finding.go`
- `skills/analysis/inspector/explain_finding_test.go`

**Acceptance Criteria:**

#### Context Gathering
- [ ] Call `lsp_hover` for type information at finding location
- [ ] Call `archivalist_query_patterns` for project patterns in category
- [ ] Get task description from session context

#### Explanation Generation
- [ ] `What`: The lint message (from finding)
- [ ] `Why`: Why this matters (generated from finding type + context)
- [ ] `Risk`: What could go wrong (generated from finding severity)
- [ ] `HowToFix`: Project-specific fix (from patterns OR generic)
- [ ] `Example`: Code example from this project (from patterns)
- [ ] `References`: Links to relevant documentation

#### Project-Specific Fixes
- [ ] If Archivalist has patterns for this category, use them
- [ ] Include file:line reference to example in this project
- [ ] Format: "In this project, use the X pattern. See file.go:123"
- [ ] Fall back to generic fix if no project patterns

#### Output Format
```
SA5011: possible nil pointer dereference

Why: The `user` variable can be nil when GetUser() returns no result.

Risk: Runtime panic in production.

Fix: In this project, use the `MustUser()` pattern.
     See auth/session.go:45 for example.

Example:
    user := MustUser(ctx)  // Panics with clear error if nil
```

---

### 6.93 Inspector: Validate Architecture Skill

Enforce project-specific architectural rules.

**Files to create:**
- `skills/analysis/inspector/architecture.go`
- `skills/analysis/inspector/architecture_test.go`

**Acceptance Criteria:**

#### Rule Retrieval
- [ ] Call `archivalist_query_architecture_rules` for project rules
- [ ] Rules stored as: pattern, constraint, severity, description
- [ ] Example rules:
  - `handlers/ cannot import repositories/`
  - `domain/ cannot import infrastructure/`
  - `All api/ functions must have context.Context first param`

#### Import Analysis
- [ ] Call `lsp_import_graph` for import relationships
- [ ] Build dependency map: file → imported packages
- [ ] Resolve package paths to file paths

#### Rule Checking
- [ ] For each rule, check against import graph
- [ ] Collect violations with: rule, location, description
- [ ] Query Archivalist for previous violations of same rule

#### Output
- [ ] List of `ArchViolation` with severity
- [ ] Score: 1.0 - (violations * 0.1), min 0.0
- [ ] Summary: "2 architectural violations found"
- [ ] Suggestion for each violation

---

### 6.94 Inspector: Evaluate Blast Radius Skill

Identify files affected by changes.

**Files to create:**
- `skills/analysis/inspector/blast_radius.go`
- `skills/analysis/inspector/blast_radius_test.go`

**Acceptance Criteria:**

#### Symbol Extraction
- [ ] Call `lsp_document_symbols` for symbols in changed files
- [ ] Filter to exported symbols only
- [ ] Track symbol name and location

#### Reference Finding
- [ ] For each exported symbol, call `lsp_references`
- [ ] Collect unique files that reference the symbol
- [ ] Group by: direct dependents, transitive dependents

#### Risk Assessment
- [ ] `Critical`: > 20 affected files
- [ ] `High`: 10-20 affected files
- [ ] `Medium`: 5-10 affected files
- [ ] `Low`: < 5 affected files

#### Recommendations
- [ ] `Critical`/`High`: "Run full integration test suite"
- [ ] `Medium`: "Run integration tests for affected modules"
- [ ] `Low`: "Unit tests sufficient"

---

### 6.95 Tester: Prioritize Test Targets Skill

Rank what to test by combining coverage, complexity, changes, and history.

**Files to create:**
- `skills/analysis/tester/prioritize.go`
- `skills/analysis/tester/prioritize_test.go`

**Acceptance Criteria:**

#### Data Gathering (Parallel)
- [ ] Call `test_coverage` for coverage data
- [ ] Call `lint_complexity` for complexity metrics
- [ ] Call `git_changed_files` for changed files
- [ ] Call `archivalist_query_bug_density` for bug history

#### Scoring Algorithm
```
Score = 0

If file is changed:        +40 points
If complexity > 10:        +30 points
If complexity > 5:         +15 points
If bug_count > 2:          +30 points
If bug_count > 0:          +15 points
```

- [ ] Calculate score for each uncovered function
- [ ] Sort by score descending
- [ ] Include reasons array for transparency

#### Test Type Recommendation
- [ ] `unit`: Pure functions, no external deps
- [ ] `integration`: DB, HTTP, filesystem deps
- [ ] `e2e`: User flows, API endpoints
- [ ] Heuristic based on file path and dependencies

#### Output
```go
type TestPriority struct {
    File            string
    Functions       []string
    PriorityScore   float64
    Reasons         []string
    RecommendedType TestType
    CurrentCoverage float64
    Complexity      int
}
```

---

### 6.96 Tester: Assess Test Quality Skill

Evaluate test suite quality beyond coverage.

**Files to create:**
- `skills/analysis/tester/quality.go`
- `skills/analysis/tester/quality_test.go`

**Acceptance Criteria:**

#### Data Gathering
- [ ] Call `test_run` for test results
- [ ] Call `test_coverage` for coverage percentage
- [ ] Parse test files for pattern analysis

#### Pattern Analysis (via AST)
- [ ] Count assertions per test
- [ ] Identify tests with zero assertions
- [ ] Identify tests that only test happy path
- [ ] Identify functions with error returns but no error tests

#### Mutation Sampling (Optional)
- [ ] If enabled, run 10 random mutations
- [ ] Calculate kill rate (mutations caught / total)
- [ ] Low kill rate = weak assertions

#### Weakness Detection
- [ ] `no_assertions`: Test has no assertions
- [ ] `single_path`: Only tests 1 of N branches
- [ ] `no_error_test`: Function returns error but not tested

#### Grading
- [ ] A: Coverage > 80%, assertion density > 2, no weaknesses
- [ ] B: Coverage > 70%, assertion density > 1.5, < 3 weaknesses
- [ ] C: Coverage > 60%, assertion density > 1, < 5 weaknesses
- [ ] D: Coverage > 50%
- [ ] F: Coverage <= 50%

---

### 6.97 Tester: Suggest Test Cases Skill

Generate specific test suggestions from code analysis.

**Files to create:**
- `skills/analysis/tester/suggest.go`
- `skills/analysis/tester/boundaries.go`
- `skills/analysis/tester/suggest_test.go`

**Acceptance Criteria:**

#### Boundary Extraction
- [ ] Parse function body for comparisons: `<`, `<=`, `>`, `>=`, `==`
- [ ] Extract boundary values: `if age < 18` → 17, 18, 19
- [ ] Parse loops for iteration boundaries: `i < len(x)` → 0, 1, len-1, len
- [ ] Parse switch statements for case values

#### Existing Test Check
- [ ] Find test file for function
- [ ] Parse test cases (table-driven or individual)
- [ ] Check if boundary is already covered

#### Suggestion Generation
- [ ] For each uncovered boundary, generate test suggestion
- [ ] Include: name, description, inputs, expected, reason
- [ ] Priority: 1 = boundary, 2 = error case, 3 = nil case, 4 = patterns

#### Pattern Matching
- [ ] Query Archivalist for tests of similar functions
- [ ] Suggest patterns used in similar tests
- [ ] Example: "Similar function X uses table-driven tests for Y"

---

### 6.98 Tester: Identify Coverage Gaps Skill

Find uncovered code specifically in changed files.

**Files to create:**
- `skills/analysis/tester/gaps.go`
- `skills/analysis/tester/gaps_test.go`

**Acceptance Criteria:**

#### Data Gathering
- [ ] Call `test_coverage` for uncovered regions
- [ ] Call `git_changed_lines` for changed lines
- [ ] Call `lint_complexity` for complexity data

#### Gap Detection
- [ ] Intersect uncovered lines with changed lines
- [ ] Prioritize: changed AND uncovered = highest priority
- [ ] Include function name for each gap

#### Output
```go
type CoverageGap struct {
    File        string
    Function    string
    Lines       []int    // Uncovered line numbers
    IsChanged   bool     // Was this recently changed?
    Complexity  int
    Suggestion  string
}
```

#### Sorting
- [ ] Changed gaps first
- [ ] Then by complexity descending
- [ ] Return top 20 gaps

---

### 6.99 Analysis Skills Bundle Registration

Register analysis skills with lazy loading system.

**Files to create:**
- `skills/analysis/bundles.go`
- `skills/analysis/manifests.go`

**Acceptance Criteria:**

#### Manifests (~20 tokens each)
```go
var AnalysisSkillManifests = []SkillManifest{
    {Name: "assess_change_risk", Brief: "Prioritized findings in changed code"},
    {Name: "explain_finding", Brief: "Semantic explanation with project fix"},
    {Name: "validate_architecture", Brief: "Check project architectural rules"},
    {Name: "evaluate_blast_radius", Brief: "Identify affected files"},
    {Name: "prioritize_test_targets", Brief: "Rank what to test by risk"},
    {Name: "assess_test_quality", Brief: "Evaluate tests beyond coverage"},
    {Name: "suggest_test_cases", Brief: "Generate test suggestions"},
    {Name: "identify_coverage_gaps", Brief: "Find uncovered changed code"},
}
```

#### Bundles
- [ ] `inspector_analysis`: change_risk, explain, architecture, blast_radius
- [ ] `tester_analysis`: prioritize, quality, suggest, gaps
- [ ] Register bundles with lazy loading system

#### Integration
- [ ] Inspector loads `inspector_analysis` bundle on first analysis request
- [ ] Tester loads `tester_analysis` bundle on first analysis request
- [ ] Skills available via `request_tools` skill

---

### 6.100 Analysis Skills Integration Tests

End-to-end testing for analysis skills.

**Files to create:**
- `tests/e2e/analysis_skills_test.go`
- `tests/fixtures/analysis_test_repo.go`

**Test Scenarios:**

1. **Change Risk - No History**: Findings filtered to changed code only
2. **Change Risk - With History**: Historical bugs increase priority
3. **Explain Finding - With Patterns**: Project-specific fix suggested
4. **Explain Finding - No Patterns**: Generic fix provided
5. **Architecture - Violations**: Rule violations detected and reported
6. **Architecture - Clean**: No violations returns score 1.0
7. **Blast Radius - Small Change**: Low risk, unit tests recommended
8. **Blast Radius - Large Change**: High risk, integration tests recommended
9. **Prioritize Targets - Changed Code**: Changed files ranked higher
10. **Test Quality - Weak Tests**: Tests with no assertions flagged
11. **Suggest Cases - Boundaries**: Boundary test cases suggested
12. **Coverage Gaps - Changed**: Changed uncovered code identified

**Acceptance Criteria:**
- [ ] All 12 test scenarios passing
- [ ] Analysis skills call existing skills (not tools directly)
- [ ] Parallel data gathering verified (timing)
- [ ] Context injection verified
- [ ] Historical correlation verified
- [ ] Token efficiency: manifests < 25 tokens each
- [ ] No lint/LSP tool reimplementation

---

## Single-Worker Pipeline System

### 6.43 Pipeline Core Implementation

Core pipeline data structures and execution engine.

**Files to create:**
- `core/pipeline/pipeline.go`
- `core/pipeline/executor.go`
- `core/pipeline/state.go`
- `core/pipeline/pipeline_test.go`

**Acceptance Criteria:**

#### Pipeline Data Structures
- [ ] `WorkerType` enum: `designer`, `engineer`
- [ ] `PipelineStatus` enum: `pending`, `executing`, `inspecting`, `testing`, `completed`, `failed`
- [ ] `Pipeline` struct with ID, TaskID, WorkerType, Status, RetryCount, MaxRetries
- [ ] `WorkerOutput`, `InspectorResult`, `TesterResult` structs
- [ ] `InspectorMode()` method derives mode from WorkerType
- [ ] `TesterMode()` method derives mode from WorkerType

```go
type Pipeline struct {
    ID          string         `json:"id"`
    TaskID      string         `json:"task_id"`
    WorkerType  WorkerType     `json:"worker_type"`
    Status      PipelineStatus `json:"status"`
    RetryCount  int            `json:"retry_count"`
    MaxRetries  int            `json:"max_retries"`
    CreatedAt   time.Time      `json:"created_at"`
    CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

func (p *Pipeline) InspectorMode() InspectorMode {
    if p.WorkerType == WorkerDesigner {
        return InspectorUIMode
    }
    return InspectorCodeMode
}
```

#### Pipeline Executor
- [ ] `PipelineExecutor` manages pipeline lifecycle
- [ ] Spawns appropriate worker based on WorkerType
- [ ] Runs Inspector with derived mode
- [ ] Runs Tester with derived mode
- [ ] Handles failure loops (back to worker)
- [ ] Reports completion to Architect

#### State Machine
- [ ] `pending` → `executing` (worker starts)
- [ ] `executing` → `inspecting` (worker completes)
- [ ] `inspecting` → `testing` (inspector passes)
- [ ] `inspecting` → `executing` (inspector fails, retry)
- [ ] `testing` → `completed` (tester passes)
- [ ] `testing` → `executing` (tester fails, retry)
- [ ] Any state → `failed` (max retries exceeded)

**Tests:**
- [ ] Pipeline creation with correct defaults
- [ ] Mode derivation works correctly
- [ ] State transitions are valid
- [ ] Retry loop stays within pipeline
- [ ] Max retries triggers failure

---

### 6.44 Inspector Mode Implementation

Inspector agent with switchable UI/Code modes.

**Files to create:**
- `agents/inspector/modes.go`
- `agents/inspector/ui_mode.go`
- `agents/inspector/code_mode.go`
- `agents/inspector/modes_test.go`

**Acceptance Criteria:**

#### Mode Switching
- [ ] `InspectorMode` enum: `ui`, `code`
- [ ] Inspector accepts mode from pipeline
- [ ] Skills loaded based on active mode
- [ ] Mode cannot change mid-inspection

#### Code Mode Skills
- [ ] `check_style` - Code style consistency
- [ ] `check_security` - Security vulnerabilities
- [ ] `check_dependencies` - Dependency usage
- [ ] `check_error_handling` - Error handling patterns
- [ ] `check_test_coverage` - Test coverage
- [ ] `check_types` - Type safety

#### UI Mode Skills
- [ ] `check_accessibility` - WCAG compliance
- [ ] `check_design_tokens` - Design token usage
- [ ] `check_component_reuse` - Component reuse
- [ ] `check_responsive` - Responsive design
- [ ] `check_color_contrast` - Color contrast ratios
- [ ] `check_semantic_html` - Semantic HTML
- [ ] `check_focus_management` - Focus management

#### Inspection Result
- [ ] `InspectorResult` with pass/fail, issues list, mode
- [ ] Issues include severity, location, message, fix suggestion
- [ ] Clear attribution for pipeline failure routing

**Tests:**
- [ ] Code mode loads correct skills
- [ ] UI mode loads correct skills
- [ ] Mode switch rejected during inspection
- [ ] Issues correctly categorized by severity

---

### 6.45 Tester Mode Implementation

Tester agent with switchable UI/Code modes.

**Files to create:**
- `agents/tester/modes.go`
- `agents/tester/ui_mode.go`
- `agents/tester/code_mode.go`
- `agents/tester/modes_test.go`

**Acceptance Criteria:**

#### Mode Switching
- [ ] `TesterMode` enum: `ui`, `code`
- [ ] Tester accepts mode from pipeline
- [ ] Test strategies differ by mode
- [ ] Mode cannot change mid-testing

#### Code Mode Capabilities
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Run test suites (go test, jest, pytest, etc.)
- [ ] Check test coverage
- [ ] Run benchmarks

#### UI Mode Capabilities
- [ ] Write component tests (Testing Library)
- [ ] Write visual regression tests (Chromatic/Percy)
- [ ] Write accessibility tests (axe-core)
- [ ] Write interaction tests (Playwright/Cypress)
- [ ] Run Storybook stories
- [ ] Check responsive breakpoints

#### Test Result
- [ ] `TesterResult` with pass/fail, test results, mode
- [ ] Failed tests include name, error, location
- [ ] Flaky test detection and reporting

**Tests:**
- [ ] Code mode uses correct test frameworks
- [ ] UI mode uses correct test frameworks
- [ ] Test results correctly structured
- [ ] Flaky tests identified

---

### 6.46 Architect Task Decomposition

Architect capability to analyze and decompose hybrid tasks.

**Files to create:**
- `agents/architect/decomposition.go`
- `agents/architect/dependency_analysis.go`
- `agents/architect/decomposition_test.go`

**Acceptance Criteria:**

#### Task Analysis
- [ ] Identify UI signals in task description
- [ ] Identify Code signals in task description
- [ ] Classify task as: `ui_only`, `code_only`, `hybrid`, `tightly_coupled`
- [ ] Signal keywords configurable

```go
var UISignals = []string{
    "component", "modal", "button", "form",
    "style", "css", "tailwind", "styled",
    "responsive", "layout", "grid", "flex",
    "animation", "transition", "motion",
    "accessibility", "a11y", "aria", "wcag",
    "design system", "theme", "token",
    "ui", "ux", "user interface",
}

var CodeSignals = []string{
    "api", "endpoint", "backend", "server",
    "database", "query", "model", "schema",
    "algorithm", "logic", "service", "handler",
    "integration", "auth", "config", "middleware",
    "cli", "script", "migration", "deploy",
    "test", "benchmark", "performance", "optimize",
}
```

#### Dependency Analysis
- [ ] Detect if UI depends on Code output (needs data shapes, hooks)
- [ ] Detect if Code depends on UI output (needs component refs, events)
- [ ] Detect if independent (can parallelize)
- [ ] Detect if tightly coupled (single pipeline)

#### Pipeline Creation
- [ ] Create Designer pipeline for UI tasks
- [ ] Create Engineer pipeline for Code tasks
- [ ] Set pipeline dependencies for sequential execution
- [ ] Enable parallel execution for independent pipelines

#### Execution Order
- [ ] UI depends on Code → Engineer pipeline first
- [ ] Code depends on UI → Designer pipeline first
- [ ] Independent → Parallel execution
- [ ] Tightly coupled → Single pipeline, Architect picks primary worker

**Tests:**
- [ ] UI-only task creates Designer pipeline
- [ ] Code-only task creates Engineer pipeline
- [ ] Hybrid task creates multiple pipelines
- [ ] Dependencies correctly ordered
- [ ] Parallel pipelines execute concurrently

---

### 6.47 Pipeline Failure Routing

Failure handling for inside-pipeline and outside-pipeline issues.

**Files to create:**
- `core/pipeline/failure_handler.go`
- `core/pipeline/failure_handler_test.go`

**Acceptance Criteria:**

#### Inside Pipeline Failures
- [ ] Inspector failure → loop to Worker
- [ ] Tester failure → loop to Worker
- [ ] Retry count incremented
- [ ] Max retries triggers pipeline failure
- [ ] Worker receives failure context (what to fix)

#### Outside Pipeline Failures (Integration)
- [ ] Integration check after all pipelines complete
- [ ] Integration failure → route to Architect
- [ ] Architect analyzes and creates corrective task
- [ ] Corrective task may spawn new pipeline

#### Failure Context
- [ ] `FailureContext` with issue description, location, suggested fix
- [ ] Worker receives context on retry
- [ ] Context helps worker focus on specific issue

```go
type FailureContext struct {
    PipelineID  string   `json:"pipeline_id"`
    Stage       string   `json:"stage"`  // "inspector" or "tester"
    Issues      []Issue  `json:"issues"`
    RetryCount  int      `json:"retry_count"`
    Suggestions []string `json:"suggestions"`
}
```

**Tests:**
- [ ] Inspector failure loops to worker
- [ ] Tester failure loops to worker
- [ ] Max retries causes pipeline failure
- [ ] Integration failure routes to Architect
- [ ] Failure context provided to worker

---

### 6.48 Direct Task Commands

Support for `/task` commands to directly address workers.

**Files to create:**
- `agents/guide/task_command.go`
- `agents/guide/task_command_test.go`

**Acceptance Criteria:**

#### Command Parsing
- [ ] `/task designer "..."` → Designer pipeline
- [ ] `/task engineer "..."` → Engineer pipeline
- [ ] `/task "..."` → Architect analyzes

#### Command Handling
- [ ] Guide parses `/task` commands
- [ ] Explicit worker type creates direct pipeline
- [ ] No worker type routes to Architect
- [ ] Task description passed to pipeline/Architect

```go
// /task designer "Create a card component"
// /task engineer "Add retry logic"
// /task "Add settings page"  // Architect decides

type TaskCommand struct {
    WorkerType  string  // "designer", "engineer", or ""
    Description string
}

func (g *Guide) HandleTaskCommand(ctx context.Context, cmd TaskCommand) error {
    switch cmd.WorkerType {
    case "designer":
        return g.createPipeline(ctx, cmd.Description, WorkerDesigner)
    case "engineer":
        return g.createPipeline(ctx, cmd.Description, WorkerEngineer)
    default:
        return g.routeToArchitect(ctx, cmd.Description)
    }
}
```

**Tests:**
- [ ] `/task designer` creates Designer pipeline
- [ ] `/task engineer` creates Engineer pipeline
- [ ] `/task` without type routes to Architect
- [ ] Task description correctly passed

---

### 6.49 Pipeline Integration Tests

End-to-end tests for pipeline system.

**Files to create:**
- `core/pipeline/integration_test.go`

**Acceptance Criteria:**

#### Designer Pipeline E2E
- [ ] Designer receives task
- [ ] Designer produces UI output
- [ ] Inspector (UI mode) reviews
- [ ] Tester (UI mode) tests
- [ ] Pipeline completes successfully

#### Engineer Pipeline E2E
- [ ] Engineer receives task
- [ ] Engineer produces code output
- [ ] Inspector (code mode) reviews
- [ ] Tester (code mode) tests
- [ ] Pipeline completes successfully

#### Hybrid Task E2E
- [ ] Architect receives hybrid task
- [ ] Architect decomposes into pipelines
- [ ] Pipelines execute in correct order
- [ ] Integration check passes
- [ ] All pipelines complete

#### Failure Recovery E2E
- [ ] Inspector fails → worker retries → passes
- [ ] Tester fails → worker retries → passes
- [ ] Max retries → pipeline fails → Architect notified
- [ ] Integration fails → Architect creates corrective task

**Tests:**
- [ ] Full Designer pipeline flow
- [ ] Full Engineer pipeline flow
- [ ] Hybrid task decomposition and execution
- [ ] Failure and retry flow
- [ ] Parallel pipeline execution

---

## Pipeline Variants System

Enables parallel exploration of alternative implementations through isolated variant pipelines.

### 6.101 Variant Data Structures

Core data structures for variant management.

**Files to create:**
- `core/pipeline/variant_types.go`
- `core/pipeline/variant_types_test.go`

**Acceptance Criteria:**

#### Status Enums
- [ ] `VariantGroupStatus` enum: `active`, `pending`, `selected`, `cancelled`
- [ ] `VariantStatus` enum: `running`, `ready`, `selected`, `discarded`, `cancelled`, `failed`

#### VariantGroup Struct
- [ ] `VariantGroup` with ID, SessionID, OriginalStep, StartingPoint, Status, Variants map, SelectedID, timestamps
- [ ] `NewVariantGroup(sessionID, stepID, startingPoint string)` constructor
- [ ] `AddVariant(info *VariantInfo)` method
- [ ] `AllTerminal() bool` method - checks if all variants reached terminal state
- [ ] `ReadyCount() int` method - counts variants in ready state

```go
type VariantGroup struct {
    ID            string                  `json:"id"`
    SessionID     string                  `json:"session_id"`
    OriginalStep  int                     `json:"original_step"`
    StartingPoint string                  `json:"starting_point"`
    Status        VariantGroupStatus      `json:"status"`
    Variants      map[string]*VariantInfo `json:"variants"`
    SelectedID    *string                 `json:"selected_id,omitempty"`
    CreatedAt     time.Time               `json:"created_at"`
    CompletedAt   *time.Time              `json:"completed_at,omitempty"`
}
```

#### VariantInfo Struct
- [ ] `VariantInfo` with ID, PipelineID, Label, Approach, Status, timestamps, Error
- [ ] `IsTerminal() bool` method
- [ ] `MarkReady()`, `MarkFailed(error)`, `MarkCancelled()` methods

#### Pipeline Additions
- [ ] Add `StartingPoint string` field to Pipeline struct
- [ ] Add `VariantGroupID *string` field to Pipeline struct
- [ ] Add `VFS *VirtualFilesystem` field to Pipeline struct
- [ ] `IsVariant() bool` method
- [ ] `UseVFS(vfs *VirtualFilesystem)` method

**Tests:**
- [ ] VariantGroup creation with correct defaults
- [ ] Variant registration and lookup
- [ ] AllTerminal() correctly evaluates all states
- [ ] ReadyCount() accurate for mixed states
- [ ] Pipeline variant field accessors

---

### 6.102 VFS Subprocess Interception

VFS subprocess write interception via library injection.

**Files to create:**
- `core/sandbox/vfs_interception.go`
- `core/sandbox/vfs_interception_darwin.go`
- `core/sandbox/vfs_interception_linux.go`
- `core/sandbox/vfs_interception_test.go`

**Dependencies:** Existing `core/sandbox/vfs.go`

**Acceptance Criteria:**

#### Interception Library
- [ ] Shared library that intercepts file write syscalls
- [ ] Redirects writes to VFS staging directory
- [ ] Reads fall through to original path if not in staging
- [ ] Environment variable `SYLK_VFS_STAGING` sets staging root
- [ ] Environment variable `SYLK_VFS_WORKDIR` sets original workdir

#### Darwin Implementation
- [ ] Use DYLD_INSERT_LIBRARIES for injection
- [ ] Intercept: open, write, rename, unlink, mkdir
- [ ] Path translation: workdir → staging dir

#### Linux Implementation
- [ ] Use LD_PRELOAD for injection
- [ ] Same syscall interception as Darwin
- [ ] Handle /proc/self/exe special case

#### Integration
- [ ] `VFS.WrapCommand(cmd *exec.Cmd)` adds injection env vars
- [ ] Staging dir created with correct permissions
- [ ] Cleanup removes interception artifacts

**Tests:**
- [ ] Subprocess writes go to staging, not workdir
- [ ] Subprocess reads from staging when file exists
- [ ] Subprocess reads fall through to workdir
- [ ] Multiple subprocesses share same staging
- [ ] Cleanup removes all staging files

---

### 6.103 Variant Injection Sequence

Orchestrator logic for injecting variants into running pipelines.

**Files to create:**
- `core/pipeline/variant_injection.go`
- `core/pipeline/variant_injection_test.go`

**Dependencies:** 6.101 (Variant Data Structures), 6.102 (VFS Interception), existing VFS

**Acceptance Criteria:**

#### Git State Capture
- [ ] `getStartingPoint()` returns git HEAD when pipeline started
- [ ] `getChangedFiles(startingPoint string)` returns files changed since S0
- [ ] Git operations locked during injection sequence
- [ ] Handle dirty working directory (warn user, stash if needed)

#### VFS Seeding
- [ ] `seedVFS(vfs *VirtualFilesystem, files []string)` copies current disk state
- [ ] Files streamed to staging, not buffered in memory
- [ ] Only changed files seeded, unchanged files read from disk

#### Injection Sequence
- [ ] Validate original pipeline is running or pending
- [ ] Create VariantGroup, register original as first variant
- [ ] Seed original's VFS from current disk state
- [ ] Rollback working directory: `git checkout {StartingPoint}`
- [ ] Switch original pipeline to VFS mode (at tool boundary)
- [ ] Create variant pipeline with modified prompt
- [ ] Create variant's VFS staging directory
- [ ] Start variant execution
- [ ] Signal original: VARIANT_CREATED

```go
func (o *Orchestrator) InjectVariant(originalPipelineID string, modifiedPrompt string) (*VariantGroup, error) {
    original := o.pipelines[originalPipelineID]

    // 1. Get state
    changedFiles := o.git.DiffNames(original.StartingPoint, "HEAD")

    // 2. Create group
    group := NewVariantGroup(original.SessionID, original.TaskID, original.StartingPoint)
    group.AddVariant(&VariantInfo{
        ID:         "original",
        PipelineID: original.ID,
        Label:      "original",
        Status:     VariantRunning,
    })

    // 3. Seed original VFS
    originalVFS := NewVFS(o.workDir, stagingPath(group.ID, "original"))
    for _, path := range changedFiles {
        content, _ := os.ReadFile(filepath.Join(o.workDir, path))
        originalVFS.Seed(path, content)
    }

    // 4. Rollback
    o.git.Checkout(original.StartingPoint)

    // 5. Switch original to VFS
    original.UseVFS(originalVFS)

    // 6. Create variant
    variant := o.createPipeline(modifiedPrompt, original.WorkerType)
    variant.StartingPoint = original.StartingPoint
    variantVFS := NewVFS(o.workDir, stagingPath(group.ID, "variant-1"))
    variant.UseVFS(variantVFS)

    group.AddVariant(&VariantInfo{
        ID:         "variant-1",
        PipelineID: variant.ID,
        Label:      "variant-1",
        Approach:   modifiedPrompt,
        Status:     VariantRunning,
    })

    // 7. Start and signal
    variant.Start()
    o.signal(original.ID, SignalVariantCreated, group.ID)

    return group, nil
}
```

**Tests:**
- [ ] Injection creates valid VariantGroup
- [ ] Original VFS seeded with all changed files
- [ ] Working dir at StartingPoint after injection
- [ ] Variant starts with empty VFS
- [ ] Both pipelines write to separate VFS staging
- [ ] Error if original not running/pending
- [ ] Git lock prevents concurrent injection

---

### 6.104 Variant Cancellation

Independent cancellation of variants and groups.

**Files to create:**
- `core/pipeline/variant_cancel.go`
- `core/pipeline/variant_cancel_test.go`

**Dependencies:** 6.101 (Variant Data Structures), 6.103 (Variant Injection)

**Acceptance Criteria:**

#### Single Variant Cancellation
- [ ] `CancelVariant(groupID, variantID)` cancels one variant
- [ ] Pipeline receives cancel signal, stops execution
- [ ] Variant status → cancelled
- [ ] Other variants in group continue unaffected
- [ ] Group status recalculated: pending if some ready, cancelled if all terminal with none ready

#### Group Cancellation
- [ ] `CancelVariantGroup(groupID)` cancels all variants
- [ ] All running variants receive cancel signal
- [ ] Group status → cancelled
- [ ] Working dir restored if needed
- [ ] Staging directories cleaned up

#### Cleanup
- [ ] `cleanupVariantGroup(group)` removes staging dirs
- [ ] Safe removal even if some files locked
- [ ] Idempotent (safe to call multiple times)

**Tests:**
- [ ] Cancel single variant, others continue
- [ ] Cancel group, all variants stop
- [ ] Staging dirs removed on cleanup
- [ ] Group status transitions correct
- [ ] Idempotent cleanup

---

### 6.105 Variant Selection and Commit

User selection commits variant VFS to working directory.

**Files to create:**
- `core/pipeline/variant_commit.go`
- `core/pipeline/variant_commit_test.go`

**Dependencies:** 6.101, 6.103, 6.104

**Acceptance Criteria:**

#### VFS Commit
- [ ] `CommitVariant(groupID, variantID)` writes VFS to disk
- [ ] All changed files written to working directory
- [ ] Directories created as needed
- [ ] File permissions preserved

#### Status Updates
- [ ] Selected variant status → selected
- [ ] Other ready variants status → discarded
- [ ] Group status → selected
- [ ] Group SelectedID set

#### Pipeline Continuation
- [ ] Selected pipeline continues to Inspector/Tester
- [ ] Discarded pipeline resources released
- [ ] Group completion timestamp set

```go
func (o *Orchestrator) CommitVariant(groupID, variantID string) error {
    group := o.variantGroups[groupID]
    variant := group.Variants[variantID]
    pipeline := o.pipelines[variant.PipelineID]

    // Write VFS to disk
    for _, path := range pipeline.VFS.ChangedFiles() {
        content, _ := pipeline.VFS.Read(path)
        fullPath := filepath.Join(o.workDir, path)
        os.MkdirAll(filepath.Dir(fullPath), 0755)
        os.WriteFile(fullPath, content, 0644)
    }

    // Update statuses
    variant.Status = VariantSelected
    for _, v := range group.Variants {
        if v.ID != variantID && v.Status == VariantReady {
            v.Status = VariantDiscarded
        }
    }
    group.SelectedID = &variantID
    group.Status = VariantGroupSelected

    // Cleanup and continue
    o.cleanupVariantGroup(group)
    o.resumePipeline(pipeline.ID)

    return nil
}
```

**Tests:**
- [ ] VFS files written to working dir
- [ ] Directories created for nested paths
- [ ] Selected variant marked correctly
- [ ] Discarded variants marked correctly
- [ ] Pipeline resumes after commit
- [ ] Staging cleaned up

---

### 6.105B Mandatory User Selection Policy (CRITICAL)

Enforces that user MUST explicitly select variant - no automatic selection permitted.

**Files to create:**
- `core/pipeline/variant_selection_policy.go`
- `core/pipeline/variant_selection_policy_test.go`
- `core/pipeline/variant_presentation.go`
- `core/pipeline/variant_presentation_test.go`

**Dependencies:** 6.101, 6.103, 6.104, 6.105

**CRITICAL INVARIANT:** Automatic variant selection is PROHIBITED. User must always explicitly choose.

**Acceptance Criteria:**

#### Policy Enforcement
- [ ] `VariantSelectionPolicy` struct with `AllowAutoSelect` field (MUST always be false)
- [ ] `TimeoutAction` enum: `none` (wait forever) or `cancel` (discard all)
- [ ] NO `select` option in `TimeoutAction` - intentionally omitted
- [ ] `ValidatePolicy()` returns error if `AllowAutoSelect` is true
- [ ] `ValidatePolicy()` returns error if `TimeoutAction` is anything other than `none` or `cancel`
- [ ] Policy validation runs on every `WaitForUserSelection` call
- [ ] Policy embedded in `VariantGroup` struct

#### Blocking Behavior
- [ ] `WaitForUserSelection(ctx, groupID)` blocks until user decision
- [ ] Pipeline execution halts at variant gate (Inspector/Tester wait)
- [ ] No partial commits allowed
- [ ] Selection is atomic - one commits, all others discard
- [ ] Context cancellation propagates correctly
- [ ] Timeout with `cancel` action discards all variants

#### Presentation Requirements
- [ ] `presentVariantsToUser(group)` called automatically when all variants ready
- [ ] Original implementation shown FIRST (always position [1])
- [ ] Each variant displays:
  - [ ] Approach summary (from Engineer pipeline context)
  - [ ] Files modified list with (created/modified) annotations
  - [ ] Lines changed (+added, -removed)
  - [ ] Completion time
  - [ ] Status (ready/failed)
- [ ] Diff command shown: `/variants diff <group> 1 2`
- [ ] Preview command shown: `/variants preview <group> 2`
- [ ] Select command shown: `/variants select <group> 2`
- [ ] Cancel command shown: `/variants cancel <group>`

#### Informed Decision Support
- [ ] `GetVariantSummary(groupID, variantID)` returns approach description
- [ ] `GetVariantDiff(groupID, variantID)` returns diff from original
- [ ] `GetVariantFiles(groupID, variantID)` returns modified files list
- [ ] `CompareVariants(groupID, id1, id2)` returns side-by-side diff
- [ ] Time taken displayed for performance comparison

```go
// VariantSelectionPolicy enforces mandatory user selection
type VariantSelectionPolicy struct {
    // AllowAutoSelect MUST be false - automatic selection is PROHIBITED
    AllowAutoSelect bool `json:"allow_auto_select"` // ALWAYS false

    // TimeoutAction can only be CANCEL or NONE, never SELECT
    TimeoutAction   TimeoutAction `json:"timeout_action"`

    // TimeoutDuration - 0 means wait indefinitely (default)
    TimeoutDuration time.Duration `json:"timeout_duration"`
}

type TimeoutAction string

const (
    TimeoutActionNone   TimeoutAction = "none"   // Wait indefinitely (default)
    TimeoutActionCancel TimeoutAction = "cancel" // Cancel group on timeout
    // NOTE: No TimeoutActionSelect - intentionally omitted, PROHIBITED
)

// Validate ensures policy does not allow auto-select
func (p *VariantSelectionPolicy) Validate() error {
    if p.AllowAutoSelect {
        return fmt.Errorf("POLICY VIOLATION: AllowAutoSelect must be false - user selection required")
    }
    if p.TimeoutAction != TimeoutActionNone && p.TimeoutAction != TimeoutActionCancel {
        return fmt.Errorf("POLICY VIOLATION: TimeoutAction must be 'none' or 'cancel', got %q", p.TimeoutAction)
    }
    return nil
}

// VariantPresentation contains display info for user decision
type VariantPresentation struct {
    GroupID     string               `json:"group_id"`
    TaskDesc    string               `json:"task_description"`
    Status      VariantGroupStatus   `json:"status"`
    Variants    []VariantDisplayInfo `json:"variants"`
    Commands    []string             `json:"commands"`
}

type VariantDisplayInfo struct {
    Position     int      `json:"position"`      // 1 = original, 2+ = variants
    ID           string   `json:"id"`
    Label        string   `json:"label"`         // "original" or "variant-N"
    Approach     string   `json:"approach"`      // Engineer's approach description
    FilesChanged []string `json:"files_changed"`
    LinesAdded   int      `json:"lines_added"`
    LinesRemoved int      `json:"lines_removed"`
    TimeTaken    string   `json:"time_taken"`
    Status       string   `json:"status"`        // "ready" or "failed"
}
```

**Tests:**
- [ ] Policy validation fails if `AllowAutoSelect` is true
- [ ] Policy validation fails if `TimeoutAction` is not `none` or `cancel`
- [ ] `WaitForUserSelection` blocks until selection received
- [ ] `WaitForUserSelection` returns error on cancel
- [ ] `WaitForUserSelection` cancels group on timeout with `cancel` action
- [ ] `WaitForUserSelection` continues waiting on timeout with `none` action
- [ ] Presentation includes original at position [1]
- [ ] Presentation includes all variants with correct info
- [ ] Diff between variants computed correctly
- [ ] Commands displayed correctly

**Implementation Guidelines:**
1. Policy validation MUST run before any wait operation
2. No configuration option to enable auto-select (intentionally absent)
3. Default policy: `{AllowAutoSelect: false, TimeoutAction: "none", TimeoutDuration: 0}`
4. Presentation format matches ARCHITECTURE.md specification
5. Signal Guide when variants ready for notification aggregation

---

### 6.106 Lazy Diff Computation and Cache

On-demand diff computation with TTL caching.

**Files to create:**
- `core/pipeline/variant_diff.go`
- `core/pipeline/variant_diff_test.go`

**Dependencies:** 6.101 (Variant Data Structures)

**Acceptance Criteria:**

#### CachedDiff Struct
- [ ] `CachedDiff` with GroupID, VariantID, Diff string, ComputedAt, ExpiresAt
- [ ] Configurable TTL (default 30 seconds)

#### VariantDiffCache
- [ ] `VariantDiffCache` with RWMutex, diffs map, TTL
- [ ] `NewVariantDiffCache(ttl time.Duration)` constructor
- [ ] `GetDiff(groupID, variantID, computeFn)` returns cached or computes
- [ ] Expired entries recomputed
- [ ] Thread-safe access

#### Diff Computation
- [ ] `ComputeVariantDiff(groupID, variantID)` generates unified diff
- [ ] Diff between variant VFS and StartingPoint
- [ ] Uses git show for original content
- [ ] Generates standard unified diff format

#### Diff Between Variants
- [ ] `ComputeVariantComparison(groupID, variantID1, variantID2)` compares two variants
- [ ] Shows differences between two approaches

**Tests:**
- [ ] Cache hit returns existing diff
- [ ] Cache miss computes diff
- [ ] Expired entry recomputes
- [ ] Concurrent access safe
- [ ] Unified diff format correct
- [ ] Variant comparison correct

---

### 6.107 Guide Variant Signal Hub

Guide as central router for variant signals.

**Files to create:**
- `agents/guide/variant_signals.go`
- `agents/guide/variant_signals_test.go`

**Dependencies:** 6.101 (Variant Data Structures)

**Acceptance Criteria:**

#### Signal Types
- [ ] `SignalVariantRequest` - user requests variant
- [ ] `SignalVariantCreated` - variant successfully created
- [ ] `SignalVariantReady` - single variant completed
- [ ] `SignalAllReady` - all variants in group completed
- [ ] `SignalVariantFailed` - variant execution failed
- [ ] `SignalVariantSelected` - user selected a variant

#### Signal Handler
- [ ] `HandleVariantSignal(signal)` routes to appropriate handler
- [ ] VARIANT_REQUEST → route to Architect
- [ ] VARIANT_READY → add to notification aggregator
- [ ] ALL_READY → immediate notification (bypass aggregator)
- [ ] VARIANT_FAILED → add to notification aggregator

#### Intent Classification
- [ ] Extend Guide intent classifier for VARIANT_REQUEST
- [ ] Extract target_task from user message
- [ ] Extract modified_prompt from user message
- [ ] Validate target task exists and is running/pending

**Tests:**
- [ ] Signal routing correct for each type
- [ ] Intent classification extracts task reference
- [ ] Intent classification extracts approach modification
- [ ] Invalid variant requests rejected with error message

---

### 6.108 Notification Aggregation

Batches variant notifications to prevent spam.

**Files to create:**
- `agents/guide/notification_aggregator.go`
- `agents/guide/notification_aggregator_test.go`

**Dependencies:** 6.107 (Guide Variant Signal Hub)

**Acceptance Criteria:**

#### Notification Struct
- [ ] `Notification` with Type, GroupID, VariantID, Message, Timestamp
- [ ] JSON serializable

#### NotificationAggregator
- [ ] `NotificationAggregator` with mutex, pending map, flushTimer, flushDelay
- [ ] `NewNotificationAggregator(flushDelay, onFlush)` constructor
- [ ] Default flushDelay: 100ms
- [ ] `Add(sessionID, notification)` queues notification
- [ ] Timer reset on each add
- [ ] `flush()` calls onFlush for each session's batch

#### Flush Behavior
- [ ] Notifications batched per session
- [ ] Single flush delivers all pending for session
- [ ] Timer-based auto-flush after quiet period
- [ ] Batched messages combine counts: "3 variants ready"

**Tests:**
- [ ] Single notification delivered after delay
- [ ] Multiple notifications batched
- [ ] Timer reset on rapid additions
- [ ] Per-session isolation
- [ ] Batch message formatting

---

### 6.109 Variant CLI Commands

User commands for variant management.

**Files to create:**
- `cli/commands/variants.go`
- `cli/commands/variants_test.go`

**Dependencies:** 6.101-6.106

**Acceptance Criteria:**

#### /variants Command
- [ ] Lists all pending variant groups for session
- [ ] Shows: group ID, task description, variant count, status
- [ ] Sorted by creation time (newest first)

#### /variants show <group_id>
- [ ] Shows all variants in group
- [ ] Displays: label, approach, status, completion time
- [ ] Highlights ready variants

#### /variants diff <group_id> [variant1] [variant2]
- [ ] Single variant: diff from StartingPoint
- [ ] Two variants: diff between them
- [ ] Paged output for large diffs
- [ ] Syntax highlighting

#### /variants preview <group_id> <variant_id>
- [ ] Shows full file contents from variant VFS
- [ ] Lists all changed files
- [ ] Opens in pager if large

#### /variants select <group_id> <variant_id>
- [ ] Commits selected variant
- [ ] Confirms selection to user
- [ ] Shows next steps (Inspector/Tester will run)

#### /variants cancel <group_id> [variant_id]
- [ ] With variant_id: cancels single variant
- [ ] Without variant_id: cancels entire group
- [ ] Confirms cancellation

**Tests:**
- [ ] List shows correct groups
- [ ] Show displays variant details
- [ ] Diff computes and displays correctly
- [ ] Preview shows file contents
- [ ] Select commits and cleans up
- [ ] Cancel handles both cases

---

### 6.110 Status Line Variant Integration

Ambient variant status in session status line.

**Files to create:**
- `cli/status/variant_indicator.go`
- `cli/status/variant_indicator_test.go`

**Dependencies:** 6.101 (Variant Data Structures)

**Acceptance Criteria:**

#### Variant Status Component
- [ ] `VariantStatusComponent` fetches current variant state
- [ ] Shows count of pending variant groups
- [ ] Shows which task has variants

#### Display Formats
- [ ] Running variants: "Variants: 2 pending (task-4)"
- [ ] Ready variants: "Variants: 2 ready ◄── /variants"
- [ ] No variants: component hidden

#### Attention Indicators
- [ ] Ready variants highlighted (color/icon)
- [ ] Command hint shown when action needed
- [ ] Subtle when just pending

**Tests:**
- [ ] Correct display for running variants
- [ ] Correct display for ready variants
- [ ] Hidden when no variants
- [ ] Updates on status change

---

### 6.111 Layer Wait Semantics

Topological layer waits for variant selection before proceeding.

**Files to create:**
- `core/pipeline/variant_layer_wait.go`
- `core/pipeline/variant_layer_wait_test.go`

**Dependencies:** 6.101, 6.103, 6.105, existing DAG executor

**Acceptance Criteria:**

#### Layer Completion Check
- [ ] `checkLayerCompletion(layer)` considers variant groups
- [ ] Normal task: check PipelineComplete status
- [ ] Variant task: check VariantGroupSelected status
- [ ] Layer incomplete if any variant group pending

#### DAG Executor Integration
- [ ] Executor pauses layer progression for pending variants
- [ ] User notified that selection is blocking
- [ ] Selection unblocks layer progression

```go
func (o *Orchestrator) checkLayerCompletion(layer int) bool {
    for _, taskID := range o.dag.Layer(layer) {
        pipeline := o.pipelines[taskID]

        if pipeline.VariantGroupID != nil {
            group := o.variantGroups[*pipeline.VariantGroupID]
            if group.Status != VariantGroupSelected {
                return false  // Waiting for selection
            }
        } else {
            if pipeline.Status != PipelineComplete {
                return false
            }
        }
    }
    return true
}
```

**Tests:**
- [ ] Layer waits for variant selection
- [ ] Selection unblocks layer
- [ ] Normal tasks unaffected
- [ ] Multiple variant groups in layer handled
- [ ] Cancelled variants don't block (if others ready)

---

### 6.112 Variant Integration Tests

End-to-end tests for variant system.

**Files to create:**
- `tests/e2e/variant_system_test.go`
- `tests/fixtures/variant_test_scenarios.go`

**Dependencies:** 6.101-6.111

**Test Scenarios:**

#### Basic Variant Flow
- [ ] Create variant mid-execution
- [ ] Both variants complete
- [ ] User selects one
- [ ] Pipeline continues with selected

#### Cancellation Scenarios
- [ ] Cancel single variant, other completes
- [ ] Cancel entire group
- [ ] Cleanup verified

#### Edge Cases
- [ ] Variant requested at tool boundary (timing)
- [ ] Variant requested for pending task
- [ ] Multiple variant groups in same session
- [ ] Variant for task in parallel layer

#### UI/CLI Scenarios
- [ ] /variants lists groups correctly
- [ ] /variants diff shows correct output
- [ ] /variants select commits correctly
- [ ] Status line updates

**Acceptance Criteria:**
- [ ] All test scenarios passing
- [ ] VFS isolation verified (no cross-contamination)
- [ ] Git state correctly managed
- [ ] Staging cleanup complete
- [ ] No memory leaks (staging dirs cleaned)
- [ ] Concurrent execution verified

---

## Secret Management System

Foundational security layer for secret detection, redaction, and environment isolation. No dependencies on Pipeline Variants - can execute fully in parallel.

### 6.113 Secret Pattern Definitions ✅ COMPLETED

Configurable regex patterns for detecting secrets.

**Files created:**
- `core/security/patterns.go` ✅
- `core/security/patterns_test.go` ✅

**Dependencies:** None (foundational)

**Acceptance Criteria:**

#### Pattern Types
- [x] `SecretPattern` struct with Name, Pattern (regex), Severity
- [x] `SecretSeverity` enum: `low`, `medium`, `high`, `critical`
- [x] Default patterns for: API keys, passwords, tokens, private keys, AWS creds, GitHub PAT, OpenAI key, Anthropic key, connection strings
- [x] `SensitiveFilePatterns` glob list: `.env*`, `*.pem`, `*.key`, `*credentials*`, etc.

```go
type SecretPattern struct {
    Name     string
    Pattern  *regexp.Regexp
    Severity SecretSeverity
}

var SecretPatterns = []*SecretPattern{
    {Name: "api_key_generic", Pattern: regexp.MustCompile(`(?i)(api[_-]?key|apikey)['":\s]*[=:]\s*['"]?[a-zA-Z0-9_\-]{20,}`), Severity: SeverityHigh},
    {Name: "private_key", Pattern: regexp.MustCompile(`-----BEGIN\s+(RSA|DSA|EC|OPENSSH)?\s*PRIVATE KEY-----`), Severity: SeverityCritical},
    // ... additional patterns
}
```

#### Pattern Management
- [x] `LoadPatterns()` loads default + custom patterns (via NewPatternManager)
- [x] `AddPattern(pattern *SecretPattern)` adds custom pattern
- [x] `RemovePattern(name string)` removes pattern by name
- [x] Patterns compiled once at load time (performance)

**Tests:**
- [x] All default patterns detect expected secrets
- [x] No false positives on common code patterns
- [x] Custom patterns can be added/removed
- [x] Pattern matching is case-insensitive where appropriate

---

### 6.114 SecretSanitizer Service ✅ COMPLETED

Core sanitization service with context-aware handling.

**Files created:**
- `core/security/sanitizer.go` ✅
- `core/security/sanitizer_test.go` ✅

**Dependencies:** 6.113 (Secret Pattern Definitions)

**Acceptance Criteria:**

#### SecretSanitizer Struct
- [x] `SecretSanitizer` with patterns, sensitiveFiles, redactText, metrics
- [x] `NewSecretSanitizer()` constructor with defaults
- [x] Thread-safe (RWMutex for metrics)
- [x] Configurable redaction text (default: "[REDACTED]")

#### SanitizeForIndex (Librarian use)
- [x] Check file against sensitive file patterns → return stub
- [x] Stub preserves file existence: "# filename\n(contents not indexed for security)"
- [x] Scan content for secret patterns → redact in-place
- [x] Return `SanitizeResult` with counts and matched patterns
- [x] Stream-friendly (doesn't load entire file into memory)

```go
func (s *SecretSanitizer) SanitizeForIndex(path string, content []byte) ([]byte, *SanitizeResult)
```

#### CheckUserPrompt (Rejection mode)
- [x] Scan prompt for secret patterns
- [x] Return `SecretDetection` with findings
- [x] `maskContext()` shows location without revealing secret
- [x] Findings include pattern name, severity, position

#### SanitizeToolOutput (Redaction mode)
- [x] Scan output string for secrets
- [x] Redact all matches
- [x] Return sanitized string and redaction count

#### Metrics Tracking
- [x] Track redaction counts per pattern
- [x] Track skipped files per pattern
- [x] `GetMetrics()` returns current statistics

**Tests:**
- [x] SanitizeForIndex skips sensitive files with stub
- [x] SanitizeForIndex redacts secrets in normal files
- [x] CheckUserPrompt detects all pattern types
- [x] SanitizeToolOutput handles multiple secrets
- [x] Metrics accurately tracked
- [x] Thread-safe under concurrent access

---

### 6.115 Pre-Prompt Secret Detection Hook ✅ COMPLETED

Rejects user prompts containing secrets.

**Files created:**
- `core/security/hooks.go` ✅
- `core/security/hooks_test.go` ✅

**Dependencies:** 6.114 (SecretSanitizer Service)

**Acceptance Criteria:**

#### PrePromptSecretHook
- [x] Hook type: PrePrompt, Priority: First
- [x] Calls `sanitizer.CheckUserPrompt()`
- [x] If secrets detected: return `SecretDetectedError`
- [x] Error message is user-friendly, doesn't reveal secret
- [x] Logs detection via AuditLogger (pattern name + count, not values)

```go
var PrePromptSecretHook = &Hook{
    Name:     "secret_detection",
    Type:     PrePrompt,
    Priority: HookPriorityFirst,
    Handler: func(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
        // Implementation
    },
}
```

#### SecretDetectedError
- [x] Custom error type with Message, Findings
- [x] Implements `error` interface
- [x] User-facing message suggests using env vars

**Tests:**
- [x] Prompt with API key is rejected
- [x] Prompt with password is rejected
- [x] Prompt with private key is rejected
- [x] Clean prompt passes through
- [x] Error message doesn't contain the secret
- [x] Audit log entry created

---

### 6.116 Tool Output Sanitization Hook ✅ COMPLETED

Redacts secrets from tool output before LLM context.

**Files created:**
- Extended `core/security/hooks.go` ✅
- Extended `core/security/hooks_test.go` ✅

**Dependencies:** 6.114 (SecretSanitizer Service)

**Acceptance Criteria:**

#### PostToolSecretHook
- [x] Hook type: PostTool, Priority: Last
- [x] Calls `sanitizer.SanitizeToolOutput()`
- [x] Modifies `data.Output` in-place with redacted version
- [x] Logs redaction count via AuditLogger
- [x] Skips empty output (short-circuit)

```go
var PostToolSecretHook = &Hook{
    Name:     "secret_redaction_output",
    Type:     PostTool,
    Priority: HookPriorityLast,
    Handler: func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
        // Implementation
    },
}
```

**Tests:**
- [x] Tool output with secrets is redacted
- [x] Multiple secrets in output all redacted
- [x] Output without secrets unchanged
- [x] Audit log entry for redaction
- [x] Empty output handled gracefully

---

### 6.117 Environment Variable Isolation Hook ✅ COMPLETED

Prevents env vars from being passed as tool parameters.

**Files created:**
- Extended `core/security/hooks.go` ✅
- Extended `core/security/hooks_test.go` ✅

**Dependencies:** 6.113 (Secret Pattern Definitions)

**Acceptance Criteria:**

#### EnvVarPattern
- [x] Regex to detect env var references: `\$\{?([A-Z_][A-Z0-9_]*)\}?`

#### PreToolEnvVarHook
- [x] Hook type: PreTool, Priority: First
- [x] Iterates all string parameters
- [x] Checks for env var pattern + secret pattern match
- [x] If injection detected: return `EnvVarInjectionError`
- [x] Logs attempt via AuditLogger

```go
var PreToolEnvVarHook = &Hook{
    Name:     "env_var_isolation",
    Type:     PreTool,
    Priority: HookPriorityFirst,
    Handler: func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) {
        // Implementation
    },
}
```

#### EnvVarInjectionError
- [x] Custom error type with Message, ToolName, ParamName
- [x] User-facing message explains tools read env vars internally

**Tests:**
- [x] Parameter with connection string rejected
- [x] Parameter with API key value rejected
- [x] Normal string parameters allowed
- [x] File paths allowed
- [x] Audit log entry for injection attempt

---

### 6.118 Librarian Pre-Index Hook ✅ COMPLETED

Sanitizes content before vector DB storage.

**Files created:**
- `agents/librarian/index_hooks.go` ✅
- `agents/librarian/index_hooks_test.go` ✅

**Dependencies:** 6.114 (SecretSanitizer Service), existing Librarian indexing

**Acceptance Criteria:**

#### IndexHookData
- [x] `IndexHookData` struct with FilePath, Content, Metadata
- [x] Used by Librarian pre-index hooks

#### LibrarianPreIndexHook
- [x] Hook type: PreIndex (Librarian-specific), Priority: First
- [x] Calls `sanitizer.SanitizeForIndex()`
- [x] Replaces `data.Content` with sanitized version
- [x] Adds metadata: `sanitized`, `redaction_count`, `skipped`
- [x] Logs skipped files and redactions

```go
var LibrarianPreIndexHook = &Hook{
    Name:     "secret_sanitization_index",
    Type:     PreIndex,
    Priority: HookPriorityFirst,
    Handler: func(ctx context.Context, data *IndexHookData) (*IndexHookData, error) {
        // Implementation
    },
}
```

#### Librarian Integration
- [x] Hook registered in Librarian indexing pipeline
- [x] Metadata stored with indexed content
- [x] Queries can filter by `sanitized` metadata

**Tests:**
- [x] .env file replaced with stub
- [x] *.pem file replaced with stub
- [x] Config file with secrets has secrets redacted
- [x] Normal code file unchanged
- [x] Metadata correctly set
- [x] Indexing continues without errors

---

### 6.119 Inter-Agent Sanitization Hook ✅ COMPLETED

Redacts secrets in agent-to-agent communication.

**Files created:**
- `core/security/dispatch_hooks.go` ✅
- `core/security/dispatch_hooks_test.go` ✅

**Dependencies:** 6.114 (SecretSanitizer Service), existing dispatch system

**Acceptance Criteria:**

#### DispatchHookData
- [x] `DispatchHookData` struct with SourceAgent, TargetAgent, Message
- [x] Used by inter-agent dispatch hooks

#### InterAgentSecretHook
- [x] Hook type: PreDispatch, Priority: Last
- [x] Calls `sanitizer.SanitizeToolOutput()` on message
- [x] Replaces `data.Message` with sanitized version
- [x] Logs redaction with source/target agent names

```go
var InterAgentSecretHook = &Hook{
    Name:     "secret_sanitization_transit",
    Type:     PreDispatch,
    Priority: HookPriorityLast,
    Handler: func(ctx context.Context, data *DispatchHookData) (*DispatchHookData, error) {
        // Implementation
    },
}
```

**Tests:**
- [x] Message with secrets redacted before dispatch
- [x] Source and target agent logged
- [x] Clean message unchanged
- [x] All agent pairs covered

---

### 6.120 Proactive Validation Skills ✅ COMPLETED

Skills for agents to actively validate content.

**Files created:**
- `core/security/skills.go` ✅
- `core/security/skills_test.go` ✅

**Dependencies:** 6.114 (SecretSanitizer Service), skill system

**Acceptance Criteria:**

#### validate_content Skill
- [x] Domain: security
- [x] Input: content string
- [x] Output: safe (bool), findings (array), suggestion (string)
- [x] Calls `sanitizer.CheckUserPrompt()` internally
- [x] Returns actionable suggestion

#### check_file_sensitivity Skill
- [x] Domain: security
- [x] Input: path string
- [x] Output: sensitive (bool), pattern (string), handling (string)
- [x] Checks against `SensitiveFilePatterns`
- [x] Returns handling recommendation: skip|redact|normal

#### sanitize_for_display Skill
- [x] Domain: security
- [x] Input: content string
- [x] Output: sanitized (string), redaction_count (int)
- [x] Calls `sanitizer.SanitizeToolOutput()` internally
- [x] For agents that want to show content safely

**Tests:**
- [x] validate_content detects secrets
- [x] check_file_sensitivity identifies .env
- [x] sanitize_for_display redacts correctly
- [x] Skills registered in skill registry

---

### 6.121 Secret Management Integration Tests ✅ COMPLETED

End-to-end tests for secret management system.

**Files created:**
- `tests/e2e/secret_management_test.go` ✅
- `tests/fixtures/secret_test_files/` ✅

**Dependencies:** 6.113-6.120

**Test Scenarios:**

#### User Prompt Flow
- [x] Prompt with API key → rejected with helpful message
- [x] Prompt with private key → rejected
- [x] Clean prompt → passes through

#### Librarian Indexing Flow
- [x] Index .env file → stub stored, not content
- [x] Index config with secrets → secrets redacted in index
- [x] Query for .env → returns "exists but not indexed"
- [x] Normal code → indexed without changes

#### Tool Output Flow
- [x] Tool returns connection string → redacted before context
- [x] Tool returns clean output → unchanged

#### Agent-to-Agent Flow
- [x] Engineer sends message with secret → redacted in transit
- [x] Architect receives sanitized version

#### Env Var Isolation Flow
- [x] Tool called with API key param → rejected
- [x] Tool called with normal param → allowed
- [x] Tool reads from os.Getenv internally → works

**Acceptance Criteria:**
- [x] All test scenarios passing
- [x] No secrets leak to LLM context
- [x] Audit log entries for all security events
- [x] Metrics accurately tracked
- [x] No false positives on common code patterns
- [x] Performance: < 1ms per sanitization

---

## Secret Management Implementation Order

1. **6.113** (Secret Patterns) - Foundation, no dependencies
2. **6.113** → **6.114** (SecretSanitizer Service) - Core service
3. **6.114** → **6.115, 6.116, 6.117** (Hooks: prompt, tool output, env var) - Can parallelize
4. **6.114** → **6.118** (Librarian Pre-Index Hook) - Depends on sanitizer
5. **6.114** → **6.119** (Inter-Agent Hook) - Depends on sanitizer
6. **6.114** → **6.120** (Proactive Skills) - Depends on sanitizer
7. **All** → **6.121** (Integration Tests) - Validates entire system

**Parallel Execution Groups:**
- Group A: 6.113 (no dependencies)
- Group B: 6.114 (depends on A)
- Group C: 6.115, 6.116, 6.117, 6.118, 6.119, 6.120 (all depend only on B, can parallelize)
- Group D: 6.121 (depends on all)

**Cross-System Parallelism:**
Secret Management (6.113-6.121) has NO dependencies on:
- Pipeline Variants (6.101-6.112)
- VectorGraphDB (6.1-6.20)
- Agent Efficiency (6.21-6.42)
- Pipeline Core (6.43-6.49)

All can execute fully in parallel.

---

## Credential Broker System

Secure agent credential access through opaque handles and just-in-time injection. Integrates with Secret Management for defense-in-depth.

### 6.122 Credential Scope Definitions ✅ COMPLETED

Agent-to-credential permission mappings.

**Files created:**
- `core/credentials/scopes.go` ✅
- `core/credentials/scopes_test.go` ✅

**Dependencies:** None (foundational)

**Acceptance Criteria:**

#### CredentialScope Struct
- [x] `CredentialScope` with AgentType, Allowed, Denied, RequireAuth
- [x] Allowed: list of provider names agent can access
- [x] Denied: explicit denials (override allowed, supports "*")
- [x] RequireAuth: boolean for first-time user confirmation

#### Default Scopes
- [x] Librarian: openai, anthropic, voyage (NO github, aws)
- [x] Academic: openai, anthropic, google, serpapi (NO github, aws)
- [x] Archivalist: openai, anthropic (NO github, aws)
- [x] Engineer: openai, anthropic, github (RequireAuth for github)
- [x] Designer: openai, anthropic, figma (RequireAuth for figma)
- [x] Inspector/Tester: openai, anthropic (NO external mutations)
- [x] Guide/Architect: openai, anthropic only
- [x] Orchestrator: NO credentials (coordinates others)

```go
type CredentialScope struct {
    AgentType   string   `yaml:"agent_type"`
    Allowed     []string `yaml:"allowed"`
    Denied      []string `yaml:"denied"`
    RequireAuth bool     `yaml:"require_auth"`
}
```

#### Project Overrides
- [x] `ProjectCredentialOverrides` struct
- [x] Loaded from `.sylk/config.yaml`
- [x] Merges with defaults (project can restrict, not expand)

**Tests:**
- [x] Default scopes correctly defined for all agents
- [x] Deny list overrides allow list
- [x] Wildcard "*" deny blocks all
- [x] Project overrides merge correctly

---

### 6.123 Credential Handle System ✅ COMPLETED

Opaque handles for single-use credential access.

**Files created:**
- `core/credentials/handle.go` ✅
- `core/credentials/handle_test.go` ✅

**Dependencies:** 6.122 (Credential Scope Definitions)

**Acceptance Criteria:**

#### CredentialHandle Struct
- [x] ID, Provider, AgentID, AgentType, ToolCallID
- [x] CreatedAt, ExpiresAt timestamps
- [x] Used boolean (single-use enforcement)
- [x] value string (internal, never serialized)

#### Handle Lifecycle
- [x] HandleLifetime constant (default 30 seconds)
- [x] `generateHandleID()` creates unique IDs
- [x] Handles expire automatically after lifetime
- [x] Handles invalidate after single use

```go
type CredentialHandle struct {
    ID          string
    Provider    string
    AgentID     string
    AgentType   string
    ToolCallID  string
    CreatedAt   time.Time
    ExpiresAt   time.Time
    Used        bool
    value       string  // internal
}
```

**Tests:**
- [x] Handle creation with correct expiry
- [x] Handle expires after lifetime
- [x] Handle invalidates after use
- [x] Cannot reuse handle
- [x] value never appears in JSON/YAML

---

### 6.124 Credential Broker Service ✅ COMPLETED

Core broker managing credential access.

**Files created:**
- `core/credentials/broker.go` ✅
- `core/credentials/broker_test.go` ✅

**Dependencies:** 6.122, 6.123, existing CredentialManager

**Acceptance Criteria:**

#### CredentialBroker Struct
- [x] References CredentialManager (fetches actual credentials)
- [x] Scopes map with defaults + project overrides
- [x] Active handles map with mutex
- [x] Audit log reference

#### RequestCredential Method
- [x] Check scope permissions (isAllowed)
- [x] Check RequireAuth (first-time confirmation)
- [x] Fetch from CredentialManager
- [x] Create time-limited handle
- [x] Log GRANTED event
- [x] Return handle (NOT raw credential)

#### ResolveHandle Method
- [x] Validate handle exists
- [x] Check not expired
- [x] Check not already used
- [x] Mark as used
- [x] Log RESOLVED event
- [x] Return actual credential value

#### RevokeHandle Method
- [x] Remove handle from active map
- [x] Log REVOKED event

#### Cleanup Goroutine
- [x] Periodic cleanup of expired handles (every 10s)
- [x] Thread-safe cleanup

```go
func (b *CredentialBroker) RequestCredential(
    ctx context.Context,
    agentID, agentType, provider, toolCallID, reason string,
) (*CredentialHandle, error)

func (b *CredentialBroker) ResolveHandle(handleID string) (string, error)
```

**Tests:**
- [x] Request for allowed provider succeeds
- [x] Request for denied provider fails with CredentialAccessDeniedError
- [x] Request for unknown provider fails (default deny)
- [x] RequireAuth triggers user confirmation
- [x] Handle resolves to correct credential
- [x] Expired handle resolution fails
- [x] Used handle resolution fails
- [x] Concurrent requests thread-safe

---

### 6.125 Just-in-Time Injection Hooks ✅ COMPLETED

Pre/post tool hooks for credential injection.

**Files created:**
- `core/credentials/hooks.go` ✅
- `core/credentials/hooks_test.go` ✅

**Dependencies:** 6.124 (Credential Broker), hook system

**Acceptance Criteria:**

#### CredentialContext Struct
- [x] Wraps broker + handleID
- [x] `Credential()` method resolves handle
- [x] Caches resolved value for single tool execution
- [x] Clears value on cleanup

#### PreToolCredentialHook
- [x] Type: PreTool, Priority: Early (after env_var_isolation)
- [x] Look up tool metadata for RequiredCredentials
- [x] Skip if tool needs no credentials
- [x] Request handle for each required credential
- [x] Inject CredentialContext map into tool context
- [x] Fail tool call if any credential request fails

#### PostToolCredentialHook
- [x] Type: PostTool, Priority: Last
- [x] Revoke any unused handles
- [x] Clear credential values from CredentialContext
- [x] Ensure no credential leakage

#### Context Key
- [x] `credentialContextKey` for context value storage
- [x] Type-safe context accessor functions

```go
var PreToolCredentialHook = &Hook{
    Name:     "credential_injection",
    Type:     PreTool,
    Priority: HookPriorityEarly,
    Handler:  func(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, error) { ... },
}
```

**Tests:**
- [x] Tool with credential requirement gets injected context
- [x] Tool without requirements gets no injection
- [x] Multiple credentials injected correctly
- [x] Unused handles revoked on cleanup
- [x] Credential values cleared after execution
- [x] Hook ordering correct (after env_var_isolation)

---

### 6.126 Tool Credential Registry ✅ COMPLETED

Metadata declaring tool credential requirements.

**Files created:**
- `core/tools/credential_registry.go` ✅
- `core/tools/credential_registry_test.go` ✅

**Dependencies:** None (data definitions)

**Acceptance Criteria:**

#### ToolMetadata Struct
- [x] Name string
- [x] RequiredCredentials []string (provider names)

#### ToolRegistry Map
- [x] Register all tools with credential requirements
- [x] generate_embeddings: ["openai"]
- [x] create_pr: ["github"]
- [x] web_search: ["serpapi"]
- [x] read_file, write_file: [] (no credentials)

#### GetToolMetadata Function
- [x] Lookup by tool name
- [x] Returns nil for unknown tools (no credentials assumed)

#### Validation
- [x] `ValidateToolRegistry()` ensures no tool has credential-like parameters
- [x] Fails build if tool declares both credentials AND credential params

```go
var ToolRegistry = map[string]*ToolMetadata{
    "generate_embeddings": {RequiredCredentials: []string{"openai"}},
    "create_pr":           {RequiredCredentials: []string{"github"}},
}
```

**Tests:**
- [x] Registry contains all external API tools
- [x] Metadata lookup works
- [x] Unknown tool returns nil
- [x] Validation detects bad tool definitions

---

### 6.127 Credential Audit Log ✅ COMPLETED

Tamper-evident logging of all credential access.

**Files created:**
- `core/credentials/audit.go` ✅
- `core/credentials/audit_test.go` ✅

**Dependencies:** None (standalone)

**Acceptance Criteria:**

#### CredentialAuditEntry Struct
- [x] ID, Timestamp, Action, AgentID, AgentType, Provider
- [x] ToolCallID, HandleID, Reason (optional fields)
- [x] PrevHash, EntryHash (hash chain)

#### CredentialAuditAction Enum
- [x] granted, denied, resolved, revoked, pending, expired

#### CredentialAuditLog Struct
- [x] entries slice with mutex
- [x] hashChain string (running hash)
- [x] storage backend interface

#### Log Methods
- [x] LogGranted, LogDenied, LogResolved, LogRevoked, LogPending
- [x] Each computes hash chain

#### Hash Chain
- [x] SHA-256 of entry fields + previous hash
- [x] `Verify()` validates entire chain integrity
- [x] Detects tampering at any entry

#### Query Method
- [x] Filter by AgentType, Provider, Action, time range
- [x] Returns matching entries

```go
type CredentialAuditEntry struct {
    ID         string
    Timestamp  time.Time
    Action     CredentialAuditAction
    AgentID    string
    AgentType  string
    Provider   string
    PrevHash   string
    EntryHash  string
}
```

**Tests:**
- [x] Entries logged with correct action types
- [x] Hash chain computed correctly
- [x] Verify() passes for valid log
- [x] Verify() fails for tampered log
- [x] Query filters work correctly
- [x] Thread-safe logging

---

### 6.128 User Authorization Manager ✅ COMPLETED

First-time credential access confirmations.

**Files created:**
- `core/credentials/authorization.go` ✅
- `core/credentials/authorization_test.go` ✅

**Dependencies:** 6.124 (integrates with broker), Guide agent

**Acceptance Criteria:**

#### UserAuthorizationManager Struct
- [x] authorizations map (agentType:provider → bool)
- [x] storage backend for persistence
- [x] mutex for thread safety

#### RequestAuthorization Method
- [x] Check if already authorized (fast path)
- [x] Prompt user via Guide agent
- [x] Options: "Allow", "Allow for session only", "Deny"
- [x] Persist "Allow" to storage
- [x] Session-only doesn't persist
- [x] Return error on deny

#### Integration with Broker
- [x] Broker calls `hasUserAuthorization()` for RequireAuth scopes
- [x] Returns CredentialAuthRequiredError if not authorized
- [x] Error triggers authorization flow

```go
func (m *UserAuthorizationManager) RequestAuthorization(
    ctx context.Context,
    agentType, provider string,
) error
```

**Tests:**
- [x] Already authorized returns immediately
- [x] "Allow" persists authorization
- [x] "Allow for session only" doesn't persist
- [x] "Deny" returns error
- [x] User prompt shows correct message
- [x] Concurrent authorization requests serialized

---

### 6.129 Credential Broker Integration Tests ✅ COMPLETED

End-to-end tests for credential access system.

**Files created:**
- `tests/e2e/credential_broker_test.go` ✅
- `tests/fixtures/credential_test_tools/` ✅

**Dependencies:** 6.122-6.128, Secret Management (6.113-6.121)

**Test Scenarios:**

#### Basic Access Flow
- [x] Engineer calls create_pr → credential injected → success
- [x] Librarian calls generate_embeddings → credential injected → success
- [x] Agent never sees raw credential value

#### Scope Enforcement
- [x] Librarian tries github → denied (scope violation)
- [x] Orchestrator tries any credential → denied (no access)
- [x] Unknown provider → denied (default deny)

#### Handle Lifecycle
- [x] Handle expires after 30 seconds
- [x] Handle invalidates after use
- [x] Unused handles revoked on cleanup

#### User Authorization
- [x] First github access prompts user
- [x] "Allow" enables future access
- [x] "Deny" blocks access

#### Secret Management Integration
- [x] Credential in tool output → redacted
- [x] Credential in agent message → redacted
- [x] Credential as parameter → blocked by env_var_isolation

#### Audit Trail
- [x] All credential access logged
- [x] Hash chain verifies
- [x] Query returns correct entries

**Acceptance Criteria:**
- [x] All scenarios passing
- [x] No credential leakage to LLM context
- [x] Audit log complete and tamper-evident
- [x] Performance: < 5ms overhead per tool call
- [x] Concurrent access thread-safe

---

## Credential Broker Implementation Order

1. **6.122** (Credential Scopes) - Foundation, no dependencies
2. **6.123** (Handle System) - Depends on 6.122
3. **6.122** → **6.126** (Tool Registry) - Can parallelize, no deps on broker
4. **6.127** (Audit Log) - No dependencies, can parallelize
5. **6.123 + 6.127** → **6.124** (Broker Service) - Core implementation
6. **6.124** → **6.125** (Injection Hooks) - Depends on broker
7. **6.124** → **6.128** (User Authorization) - Depends on broker
8. **All** → **6.129** (Integration Tests) - Validates entire system

**Parallel Execution Groups:**
- Group A: 6.122, 6.126, 6.127 (no dependencies on each other)
- Group B: 6.123 (depends on 6.122)
- Group C: 6.124 (depends on A + B)
- Group D: 6.125, 6.128 (depend on C, can parallelize)
- Group E: 6.129 (depends on all)

**Cross-System Dependencies:**
- Credential Broker depends on Secret Management hooks (6.115, 6.116, 6.117)
- Integration tests require both systems operational
- Can develop in parallel but integration tests last

---

## Orchestrator Agent System

The Orchestrator is a read-only intelligent query box for pipeline status and workflow progress. Uses Claude Haiku 4.5 for lightweight status queries and summarization. Faithfully executes Architect's plans with no authority to refuse or modify.

### 6.130 Update Buffer Types

Per-task bounded buffers for status updates with TTL eviction and backpressure.

**Files to create:**
- `agents/orchestrator/buffer.go`
- `agents/orchestrator/buffer_test.go`
- `agents/orchestrator/registry.go`
- `agents/orchestrator/registry_test.go`

**Dependencies:** None (foundational)

**Acceptance Criteria:**

#### TaskUpdate Struct
- [ ] `TaskUpdate` with ID, Timestamp, Type, Payload, ExpiresAt
- [ ] JSON serializable for storage and transmission
- [ ] `TaskUpdateType` enum: `state_transition`, `tool_call`, `heartbeat`

```go
type TaskUpdate struct {
    ID        string          `json:"id"`
    Timestamp time.Time       `json:"timestamp"`
    Type      TaskUpdateType  `json:"type"`
    Payload   json.RawMessage `json:"payload"`
    ExpiresAt time.Time       `json:"expires_at"`
}
```

#### TaskUpdateBuffer Struct
- [ ] `TaskUpdateBuffer` with mutex, taskID, updates slice, maxSize, ttl, lastUpdate
- [ ] `Push(update)` adds update with TTL, returns false if full (backpressure)
- [ ] `Query(since, types)` returns updates matching filter
- [ ] `Clear()` removes all updates
- [ ] `evictExpired()` removes entries past TTL
- [ ] Thread-safe with RWMutex

#### Backpressure Behavior
- [ ] Evict expired entries before capacity check
- [ ] Drop NEWEST update when full (preserve history)
- [ ] Return boolean indicating acceptance

#### BufferRegistry Struct
- [ ] `BufferRegistry` with mutex, buffers map, config
- [ ] `GetOrCreate(taskID)` returns or creates buffer
- [ ] `Get(taskID)` returns buffer or nil
- [ ] `Delete(taskID)` removes buffer
- [ ] `Clear()` removes all buffers

#### BufferConfig Struct
- [ ] `BufferConfig` with DefaultMaxSize (100), DefaultTTL (5m), TaskOverrides map
- [ ] `TaskBufferConfig` with per-task MaxSize and TTL
- [ ] Configurable via terminal command

**Tests:**
- [ ] Push respects capacity limit
- [ ] Backpressure drops newest (not oldest)
- [ ] TTL eviction works correctly
- [ ] Query filters by time and type
- [ ] Thread-safe under concurrent access
- [ ] Registry creates/retrieves buffers correctly
- [ ] Per-task overrides apply correctly

---

### 6.131 Orchestrator Skills

Read-only status query and push skills for the Orchestrator.

**Files to create:**
- `agents/orchestrator/skills/query_task.go`
- `agents/orchestrator/skills/query_workflow.go`
- `agents/orchestrator/skills/push_status.go`
- `agents/orchestrator/skills/generate_summary.go`
- `agents/orchestrator/skills/report_failure.go`
- `agents/orchestrator/skills/*_test.go`

**Dependencies:** 6.130 (Update Buffer Types)

**Acceptance Criteria:**

#### query_task_status Skill
- [ ] Domain: status
- [ ] Keywords: status, task, progress, what
- [ ] Params: task_id (required), include_tool_calls (optional, default true), since (optional)
- [ ] Returns: task_id, name, status, started_at, duration, tool_calls, last_activity, updates, health
- [ ] Reads from BufferRegistry (read-only)
- [ ] Includes health assessment from HealthMonitor

#### query_workflow_status Skill
- [ ] Domain: status
- [ ] Keywords: workflow, all, tasks, progress, overview
- [ ] Params: workflow_id (optional, defaults to current), verbose (optional)
- [ ] Returns: workflow_id, plan_version, status, progress counts, current_layer, total_layers, tasks, wall_clock
- [ ] Aggregates all task buffers for workflow

#### push_status_update Skill
- [ ] Domain: notification
- [ ] Keywords: push, notify, update, user
- [ ] Params: task_id (required), update_type (enum: state_change, tool_call, error, completion), message (required), priority (optional)
- [ ] Routes via Guide to user immediately

#### generate_workflow_summary Skill
- [ ] Domain: summary
- [ ] Keywords: summary, compact, archive
- [ ] Params: workflow_id (optional), include_running (optional, default true)
- [ ] Returns: OrchestratorSummary object
- [ ] Generates structured summary for compaction

#### report_failure Skill
- [ ] Domain: failure
- [ ] Keywords: failure, error, report, architect
- [ ] Params: task_id (required), error_type (enum: timeout, error, transient_storm), error_summary (required), attempts (optional)
- [ ] Routes to Architect via Guide

#### submit_task_event Skill
- [ ] Domain: archival
- [ ] Keywords: archive, history, record, event, complete, fail
- [ ] Params: task_id (required), event_type (enum: completed, failed, cancelled), event_data (object, required)
- [ ] Returns: archived (bool), event_id (string)
- [ ] Routes to Archivalist via Guide with ORCHESTRATOR_TASK_EVENT message type
- [ ] CRITICAL: Called automatically after ANY task reaches terminal state

#### archivalist_request Skill
- [ ] Domain: archival
- [ ] Keywords: archivalist, query, store, history, pattern
- [ ] Params: request_type (enum: query, store, query_patterns, query_failures), payload (object, required), priority (optional)
- [ ] Returns: response (ArchivalistResponse object), success (bool)
- [ ] Routes to Archivalist via Guide with ORCHESTRATOR_ARCHIVALIST_REQUEST message type
- [ ] Uses synchronous request-response pattern (WaitForResponse: true)
- [ ] Enables pattern queries, failure lookups, decision history queries

**Tests:**
- [ ] query_task_status returns correct buffer contents
- [ ] query_workflow_status aggregates all tasks
- [ ] push_status_update routes via Guide
- [ ] generate_workflow_summary produces valid schema
- [ ] report_failure reaches Architect
- [ ] submit_task_event routes to Archivalist
- [ ] submit_task_event called for completed tasks
- [ ] submit_task_event called for failed tasks
- [ ] submit_task_event called for cancelled tasks
- [ ] archivalist_request synchronous response works
- [ ] archivalist_request query_failures returns patterns

---

### 6.132 Health Monitor

Observes pipeline health signals without control authority.

**Files to create:**
- `agents/orchestrator/health.go`
- `agents/orchestrator/health_test.go`

**Dependencies:** 6.130 (Update Buffer Types)

**Acceptance Criteria:**

#### HealthConfig Struct
- [ ] `HealthConfig` with DefaultTimeout (30m), HeartbeatInterval (30s), HeartbeatMissThreshold (3), ErrorRateThreshold (0.5), TransientStormThreshold (5 in 1min)
- [ ] Loaded from config file, overridable per-session

#### HealthSignal Types
- [ ] `HealthSignalType` enum: timeout, heartbeat_miss, high_error_rate, transient_storm, non_transient
- [ ] `HealthSignal` struct with TaskID, SignalType, Timestamp, Details map

```go
type HealthSignal struct {
    TaskID    string
    SignalType HealthSignalType
    Timestamp time.Time
    Details   map[string]interface{}
}
```

#### HealthMonitor Struct
- [ ] `HealthMonitor` with config reference
- [ ] `CheckHealth(buffer, taskStart)` evaluates health from buffer contents
- [ ] Returns nil if healthy, HealthSignal if issue detected

#### Health Checks
- [ ] Timeout: task duration exceeds DefaultTimeout
- [ ] Heartbeat miss: last heartbeat older than interval * threshold
- [ ] Error rate: errors/total > ErrorRateThreshold
- [ ] Transient storm: transient errors > threshold in window

#### Helper Methods
- [ ] `findLastHeartbeat(updates)` extracts most recent heartbeat
- [ ] `calculateErrorRate(updates)` computes error/total ratio
- [ ] `countTransientErrors(updates, window)` counts transients in time window

**Tests:**
- [ ] Timeout detected correctly
- [ ] Heartbeat miss detected after threshold
- [ ] High error rate triggers signal
- [ ] Transient storm detected within window
- [ ] Healthy task returns nil
- [ ] Edge cases (empty buffer, all heartbeats)

---

### 6.133 Orchestrator Summary Schema

Structured summary types for compaction and archival.

**Files to create:**
- `agents/orchestrator/summary.go`
- `agents/orchestrator/summary_test.go`

**Dependencies:** 6.130, 6.131

**Acceptance Criteria:**

#### OrchestratorSummary Struct
- [ ] `OrchestratorSummary` with Workflow, Completed, Failed, Cancelled, Pending, Running, Metrics, Compaction
- [ ] JSON serializable for Archivalist storage

#### WorkflowSummary Struct
- [ ] ID, PlanVersion, StartedAt, Status, TotalTasks, CurrentLayer, TotalLayers

#### TaskCompletionRecord Struct
- [ ] TaskID, Name, Agent, Layer, StartedAt, CompletedAt, Duration, ToolCalls, Summary

#### TaskFailureRecord Struct
- [ ] TaskID, Name, Agent, Layer, StartedAt, FailedAt, Duration, ErrorType, ErrorSummary, Dependents

#### TaskCancelRecord Struct
- [ ] TaskID, Name, Layer, CancelledAt, Reason, UpstreamTask

#### TaskPendingRecord Struct
- [ ] TaskID, Name, Agent, Layer, WaitingSince, WaitDuration, BlockedBy

#### TaskRunningRecord Struct
- [ ] TaskID, Name, Agent, Layer, StartedAt, RunDuration, ToolCalls, LastHeartbeat, LastActivity

#### SummaryMetrics Struct
- [ ] TotalCompleted, TotalFailed, TotalCancelled, TotalPending, TotalRunning
- [ ] WallClockDuration, TotalTaskTime, AvgTaskDuration
- [ ] LongestTask, LongestDuration, ErrorRate, AvgHeartbeatDelay

#### CompactionInfo Struct
- [ ] Reason, CompactedAt, PreCompactTokens, PostCompactTokens, UpdatesDropped

#### Summary Generation
- [ ] `GenerateSummary(workflow, buffers)` builds OrchestratorSummary
- [ ] Categorizes tasks by status
- [ ] Computes aggregate metrics
- [ ] Includes compaction metadata

**Tests:**
- [ ] All struct types JSON marshal/unmarshal correctly
- [ ] GenerateSummary populates all fields
- [ ] Metrics calculations accurate
- [ ] Empty workflow handled gracefully

---

### 6.134 Plan Modification Handling

Accepts all plan modifications from Architect without dispute.

**Files to create:**
- `agents/orchestrator/plan_mod.go`
- `agents/orchestrator/plan_mod_test.go`

**Dependencies:** 6.130, existing DAG executor

**Acceptance Criteria:**

#### PlanModificationType Enum
- [ ] `add_task` - Add new task to DAG
- [ ] `cancel_task` - Cancel pending task
- [ ] `modify_task` - Modify pending task parameters
- [ ] `launch_variant` - Create variant pipeline
- [ ] `change_version` - Switch to different plan version

#### PlanModification Struct
- [ ] Type, Timestamp, TaskID (optional), Payload (json.RawMessage)

```go
type PlanModification struct {
    Type      PlanModificationType `json:"type"`
    Timestamp time.Time            `json:"timestamp"`
    TaskID    string               `json:"task_id,omitempty"`
    Payload   json.RawMessage      `json:"payload"`
}
```

#### HandlePlanModification Method
- [ ] NEVER refuses - trusts Architect completely
- [ ] Routes to appropriate DAG/variant method
- [ ] Logs unknown modification types but doesn't fail
- [ ] Returns error only for execution failures (not permission)

#### Modification Handlers
- [ ] `addTask(payload)` → dagExecutor.AddTask
- [ ] `cancelTask(taskID)` → dagExecutor.CancelTask
- [ ] `modifyTask(taskID, payload)` → dagExecutor.ModifyTask
- [ ] `launchVariant(payload)` → variantManager.LaunchVariant
- [ ] `changeVersion(payload)` → loadPlanVersion

**Tests:**
- [ ] All modification types route correctly
- [ ] Unknown types logged but don't fail
- [ ] DAG executor methods called with correct params
- [ ] Variant manager integration works
- [ ] Concurrent modifications handled safely

---

### 6.135 Crash Recovery

Resume from checkpointed plan file via Architect consultation.

**Files to create:**
- `agents/orchestrator/recovery.go`
- `agents/orchestrator/recovery_test.go`

**Dependencies:** 6.130, 6.134, existing checkpoint system

**Acceptance Criteria:**

#### ConsultRequest/Response
- [ ] `ConsultRequest` with Query string
- [ ] `ConsultResponse` with PlanID, Version, additional context

#### RecoverFromCrash Method
- [ ] Consult Architect: "What plan and version were we executing?"
- [ ] Load checkpointed plan file from checkpoint directory
- [ ] Query pipeline states to determine completed tasks
- [ ] Resume DAG execution from current state

```go
func (o *Orchestrator) RecoverFromCrash(ctx context.Context) error {
    // 1. Consult Architect
    // 2. Load plan file
    // 3. Query completed pipelines
    // 4. Resume DAG
}
```

#### Checkpoint Directory Structure
- [ ] `{checkpointDir}/{planID}/{version}/plan.json`
- [ ] Plan file contains full task graph
- [ ] Versioned for plan modifications

#### Pipeline State Query
- [ ] `queryCompletedPipelines(ctx, taskIDs)` returns completed task IDs
- [ ] Checks pipeline storage for terminal states
- [ ] Handles partially completed workflows

#### DAG Resume
- [ ] `dagExecutor.ResumeFrom(plan, completedTasks)` resumes from state
- [ ] Marks completed tasks as done
- [ ] Continues with next DAG layer

**Tests:**
- [ ] Recovery consults Architect correctly
- [ ] Plan file loaded and parsed
- [ ] Completed tasks identified
- [ ] DAG resumes from correct point
- [ ] Missing checkpoint handled gracefully
- [ ] Corrupted checkpoint detected

---

### 6.136 Guide Routing Integration

Route rules for Orchestrator message types.

**Files to create:**
- `agents/orchestrator/routing.go`
- `agents/guide/orchestrator_routes.go`

**Dependencies:** 6.131, existing Guide routing system

**Acceptance Criteria:**

#### OrchestratorGuideRoutes
- [ ] `ORCHESTRATOR_STATUS_PUSH` → RouteToUser, PriorityHigh
- [ ] `ORCHESTRATOR_FAILURE_REPORT` → RouteToArchitect, PriorityUrgent
- [ ] `ORCHESTRATOR_SUMMARY` → RouteToArchivalist, PriorityNormal
- [ ] `ORCHESTRATOR_TASK_EVENT` → RouteToArchivalist, PriorityNormal (task completion/failure/cancellation)
- [ ] `ORCHESTRATOR_ARCHIVALIST_REQUEST` → RouteToArchivalist, PriorityNormal, WaitForResponse: true (direct queries)

```go
var OrchestratorGuideRoutes = []RouteRule{
    {MessageType: "ORCHESTRATOR_STATUS_PUSH", Target: RouteToUser, Priority: PriorityHigh},
    {MessageType: "ORCHESTRATOR_FAILURE_REPORT", Target: RouteToArchitect, Priority: PriorityUrgent},
    {MessageType: "ORCHESTRATOR_SUMMARY", Target: RouteToArchivalist, Priority: PriorityNormal},
    {MessageType: "ORCHESTRATOR_TASK_EVENT", Target: RouteToArchivalist, Priority: PriorityNormal},
    {MessageType: "ORCHESTRATOR_ARCHIVALIST_REQUEST", Target: RouteToArchivalist, Priority: PriorityNormal, WaitForResponse: true},
}
```

#### Route Registration
- [ ] Routes registered in Guide initialization
- [ ] Priority ordering enforced
- [ ] Unknown message types logged
- [ ] WaitForResponse routes use synchronous request-response pattern

#### Message Envelope
- [ ] Standard envelope with Type, Source, Timestamp, Payload
- [ ] Source always "orchestrator"
- [ ] Type matches route rule MessageType

#### TaskEvent Envelope (for ORCHESTRATOR_TASK_EVENT)
- [ ] EventID, EventType, WorkflowID, TaskID, TaskName, Agent, Timestamp, SessionID
- [ ] Polymorphic payload: Completion, Failure, or Cancellation record

#### ArchivalistRequest Envelope (for ORCHESTRATOR_ARCHIVALIST_REQUEST)
- [ ] RequestType, RequestID, Source, Timestamp, Payload
- [ ] Synchronous response: ArchivalistResponse with Success, Data, Error

**Tests:**
- [ ] Status push routes to user
- [ ] Failure report routes to Architect
- [ ] Summary routes to Archivalist
- [ ] Task event routes to Archivalist
- [ ] Archivalist request routes and waits for response
- [ ] Priority ordering correct
- [ ] Unknown message type handling
- [ ] WaitForResponse pattern works correctly

---

### 6.137 Orchestrator Agent Core

Main Orchestrator agent with system prompt and initialization.

**Files to create:**
- `agents/orchestrator/agent.go`
- `agents/orchestrator/agent_test.go`
- `agents/orchestrator/prompt.go`

**Dependencies:** 6.130-6.136

**Acceptance Criteria:**

#### Orchestrator System Prompt
- [ ] Identity: Claude Haiku 4.5, read-only observer
- [ ] No authority to modify, refuse, or dispute plans
- [ ] User and Architect are ULTIMATE authority
- [ ] Responsibilities:
  - [ ] Status queries (query_task_status, query_workflow_status)
  - [ ] Push updates (push_status_update)
  - [ ] Generate summaries (generate_workflow_summary)
  - [ ] Report failures (report_failure)
  - [ ] **Submit task events to Archivalist for EVERY terminal state (CRITICAL)**
  - [ ] **Query Archivalist for failure patterns when relevant**
- [ ] Non-responsibilities: modify order, retry policy, cancel tasks, allocate resources, filter events
- [ ] Communication style: concise, factual, status-focused
- [ ] Status reporting format: "Task [ID] ([name]): [status] | Duration: [time] | Last activity: [description]"
- [ ] Archivalist Integration section:
  - [ ] MANDATORY event submission on completed/failed/cancelled
  - [ ] Build TaskCompletionRecord, TaskFailureRecord, or TaskCancelRecord
  - [ ] Submit via submit_task_event (non-blocking)
  - [ ] Query patterns via archivalist_request (synchronous)
- [ ] On Task Terminal State section:
  - [ ] Detect terminal state
  - [ ] Build TaskEvent with appropriate record
  - [ ] Call submit_task_event (background, non-blocking)
  - [ ] Log failures but continue workflow
- [ ] Available Skills list: query_task_status, query_workflow_status, push_status_update, generate_workflow_summary, report_failure, submit_task_event, archivalist_request

#### Orchestrator Struct
- [ ] `Orchestrator` with bufferRegistry, healthMonitor, dagExecutor, variantManager, guide, archivalist
- [ ] Model: claude-haiku-4-5-20241230
- [ ] Implements Agent interface

#### Initialization
- [ ] `NewOrchestrator(config, deps)` constructor
- [ ] Loads buffer config
- [ ] Initializes health monitor
- [ ] Registers skills
- [ ] Registers Guide routes

#### Compaction Handling
- [ ] Triggers at 95% context window OR workflow completion
- [ ] DAG continues during compaction
- [ ] LLM waits for compaction before new prompts
- [ ] Generates OrchestratorSummary → Archivalist

#### Core Properties
- [ ] Read-only: only observes, doesn't control
- [ ] No dispute: accepts all Architect decisions
- [ ] Immediate push: state changes push via Guide
- [ ] DAG executor handles auto-cancellation

**Tests:**
- [ ] Agent initializes with all dependencies
- [ ] System prompt included in context
- [ ] Skills registered and accessible
- [ ] Guide routes registered
- [ ] Compaction triggers correctly
- [ ] Model is Haiku as specified

---

### 6.138 Orchestrator Integration Tests

End-to-end tests for Orchestrator functionality.

**Files to create:**
- `tests/e2e/orchestrator_test.go`
- `tests/fixtures/orchestrator_scenarios.go`

**Dependencies:** 6.130-6.137

**Test Scenarios:**

#### Status Query Flow
- [ ] User asks "What's the status?" → Orchestrator queries buffers → returns summary
- [ ] Task-specific status query works
- [ ] Workflow overview query works
- [ ] Empty buffer handled gracefully

#### Status Push Flow
- [ ] Pipeline state change → buffer update → immediate push to user
- [ ] Tool call → buffer update → push (if configured)
- [ ] Heartbeat → buffer update (no push by default)

#### Health Monitoring Flow
- [ ] Task exceeds timeout → health signal → report to Architect
- [ ] Missed heartbeats → health signal → report to Architect
- [ ] High error rate → health signal → report to Architect
- [ ] Transient storm → health signal → report to Architect

#### Plan Modification Flow
- [ ] Architect adds task → Orchestrator accepts → DAG updated
- [ ] Architect cancels task → Orchestrator accepts → DAG updated
- [ ] Architect launches variant → Orchestrator accepts → variant created

#### Compaction Flow
- [ ] Context reaches 95% → compaction triggers
- [ ] DAG continues during compaction
- [ ] Summary generated correctly
- [ ] Summary reaches Archivalist

#### Crash Recovery Flow
- [ ] Orchestrator crashes → restart → consult Architect → resume
- [ ] Completed tasks not re-executed
- [ ] Pending tasks resume correctly

**Acceptance Criteria:**
- [ ] All test scenarios passing
- [ ] No buffer leaks (cleared on completion)
- [ ] Health signals accurate
- [ ] Routing correct for all message types
- [ ] Compaction doesn't lose running task data
- [ ] Recovery resumes from correct state
- [ ] Performance: status query < 10ms
- [ ] Task events submitted to Archivalist on completion/failure/cancellation
- [ ] Archivalist direct request pattern works

---

### 6.139 Task Completion Event Submission

Automatic submission of task events to Archivalist after ANY task reaches terminal state.

**Files to create:**
- `agents/orchestrator/events.go`
- `agents/orchestrator/events_test.go`
- `agents/orchestrator/archivalist.go`
- `agents/orchestrator/archivalist_test.go`

**Dependencies:** 6.131 (Skills), 6.133 (Summary Schema), 6.136 (Guide Routing)

**Acceptance Criteria:**

#### TaskEvent Types
- [ ] `TaskEventType` enum: completed, failed, cancelled
- [ ] `TaskEvent` struct with EventID, EventType, WorkflowID, TaskID, TaskName, Agent, Timestamp, SessionID
- [ ] Polymorphic payload: Completion, Failure, or Cancellation record

```go
type TaskEvent struct {
    EventID      string          `json:"event_id"`
    EventType    TaskEventType   `json:"event_type"`
    WorkflowID   string          `json:"workflow_id"`
    TaskID       string          `json:"task_id"`
    TaskName     string          `json:"task_name"`
    Agent        string          `json:"agent"`
    Timestamp    time.Time       `json:"timestamp"`
    SessionID    string          `json:"session_id"`
    Completion   *TaskCompletionRecord `json:"completion,omitempty"`
    Failure      *TaskFailureRecord    `json:"failure,omitempty"`
    Cancellation *TaskCancelRecord     `json:"cancellation,omitempty"`
}
```

#### OnTaskTerminal Hook
- [ ] `OnTaskTerminal(ctx, taskID, status)` called by DAG executor
- [ ] Builds appropriate record based on status (completed/failed/cancelled)
- [ ] Submits event via Guide routing (non-blocking goroutine)
- [ ] CRITICAL: Must be called for ALL terminal states

#### Event Submission
- [ ] `submitTaskEvent(ctx, event)` sends to Guide
- [ ] Uses ORCHESTRATOR_TASK_EVENT message type
- [ ] Non-blocking (fires and forgets)
- [ ] Logs submission failures but doesn't block workflow

#### ArchivalistRequest Types
- [ ] `ArchivalistRequest` struct with RequestType, RequestID, Source, Timestamp, Payload
- [ ] `ArchivalistResponse` struct with RequestID, Success, Data, Error
- [ ] Request types: query, store, query_patterns, query_failures

#### Direct Request Pattern
- [ ] `queryFailurePatterns(ctx, taskName, approach)` for failure pattern lookup
- [ ] Uses synchronous request-response via Guide.RouteAndWait
- [ ] Returns parsed FailurePattern array

**Tests:**
- [ ] OnTaskTerminal called for completed task
- [ ] OnTaskTerminal called for failed task
- [ ] OnTaskTerminal called for cancelled task
- [ ] Event contains correct polymorphic payload
- [ ] Event submission is non-blocking
- [ ] Failed submission doesn't crash workflow
- [ ] Direct request receives response
- [ ] Failure pattern query returns results

---

## Orchestrator Implementation Order

1. **6.130** (Update Buffer Types) - Foundation, no dependencies
2. **6.132** (Health Monitor) - Depends on 6.130
3. **6.133** (Summary Schema) - Depends on 6.130
4. **6.130 + 6.132 + 6.133** → **6.131** (Orchestrator Skills) - Core functionality
5. **6.130** → **6.134** (Plan Modification) - Depends on buffer types
6. **6.134** → **6.135** (Crash Recovery) - Depends on plan handling
7. **6.131** → **6.136** (Guide Routing) - Depends on skills
8. **6.131 + 6.133 + 6.136** → **6.139** (Task Event Submission) - Archivalist integration
9. **All** → **6.137** (Agent Core) - Assembles all components
10. **6.137** → **6.138** (Integration Tests) - Validates entire system

**Parallel Execution Groups:**
- Group A: 6.130 (no dependencies)
- Group B: 6.132, 6.133 (depend only on 6.130, can parallelize)
- Group C: 6.131, 6.134 (depend on A + B)
- Group D: 6.135, 6.136 (depend on C)
- Group E: 6.139 (depends on C + D, Archivalist integration)
- Group F: 6.137 (depends on all)
- Group G: 6.138 (integration tests)

**Cross-System Dependencies:**
- Orchestrator depends on DAG Executor (Phase 0.2)
- Orchestrator depends on Guide routing (Phase 0.4)
- Orchestrator sends summaries AND task events to Archivalist (Phase 0.5)
- Orchestrator can query Archivalist for failure patterns
- Pipeline Variants depend on Orchestrator for variant management

---

## Pipeline Variants Implementation Order

1. **6.101** (Variant Data Structures) - Foundation, no dependencies
2. **6.102** (VFS Subprocess Interception) - Can parallelize with 6.101
3. **6.101 + 6.102** → **6.103** (Variant Injection) - Core injection logic
4. **6.103** → **6.104** (Variant Cancellation) - Depends on injection
5. **6.103 + 6.104** → **6.105** (Variant Selection/Commit) - Depends on both
6. **6.105** → **6.105B** (Mandatory User Selection Policy) - CRITICAL: Enforces no auto-select
7. **6.101** → **6.106** (Lazy Diff Cache) - Only needs types
8. **6.101** → **6.107** (Guide Signal Hub) - Only needs types
9. **6.107** → **6.108** (Notification Aggregation) - Depends on signals
10. **6.105B + 6.106** → **6.109** (CLI Commands) - Needs policy and diff
11. **6.101** → **6.110** (Status Line) - Only needs types
12. **6.103 + 6.105B** → **6.111** (Layer Wait) - Needs injection and policy
13. **All** → **6.112** (Integration Tests) - Validates entire system including policy enforcement

**Parallel Execution Groups:**
- Group A: 6.101, 6.102 (no dependencies)
- Group B: 6.106, 6.107, 6.110 (only depend on 6.101)
- Group C: 6.103 (depends on A)
- Group D: 6.104, 6.108 (depend on C, B respectively)
- Group E: 6.105 (depends on C, D)
- Group E2: 6.105B (depends on E) - CRITICAL: Must complete before CLI/Layer Wait
- Group F: 6.109, 6.111 (depend on E2, B)
- Group G: 6.112 (depends on all, validates policy enforcement)

---

## Execution Workflow

```
Phase 0 (Foundation)              Phase 1 (Knowledge)      Phase 2 (Execution)                   Phase 3         Phase 4 (Quality)      Phase 5
┌─────────────────────────────┐   ┌─────────────────┐      ┌─────────────────────────────┐      ┌───────────┐   ┌────────────────┐      ┌──────────────────┐
│ 0.1 Session Manager         │   │ 1.1 Librarian   │      │ 2.1 Engineer                │      │ 3.1       │   │ 4.1 Inspector  │      │ 5.1 Coordinator  │
│ 0.2 DAG Engine              │   │ 1.2 Academic    │      │ 2.2 Orchestrator (6.130-139)│      │ Architect │   │ 4.2 Tester     │      │ 5.2 Integration  │
│ 0.3 Worker Pool Enhancements│──▶│ 1.3 Tool Disc.  │─────▶│ 2.3 Pipeline Infra          │─────▶│           │──▶│                │─────▶│ 5.3 Benchmarks   │
│ 0.4 Guide Enhancements      │   │                 │      │     (E + O + I + T)         │      │           │   │                │      │                  │
│ 0.5 Archivalist Enhancements│   │                 │      │                             │      │           │   │                │      │                  │
│ 0.6 Bus Enhancements        │   │                 │      │                             │      │           │   │                │      │                  │
└─────────────────────────────┘   └─────────────────┘      └─────────────────────────────┘      └───────────┘   └────────────────┘      └──────────────────┘
```

### Phase 2.2 Orchestrator Detail

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         ORCHESTRATOR IMPLEMENTATION (Phase 2.2)                       │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  Group A (Foundation)         Group B (Parallel)          Group C (Core)            │
│  ┌───────────────────┐       ┌────────────────────┐      ┌────────────────────┐     │
│  │ 6.130             │       │ 6.132              │      │ 6.131              │     │
│  │ Update Buffer     │──────▶│ Health Monitor     │──┬──▶│ Orchestrator       │     │
│  │ Types             │       │                    │  │   │ Skills             │     │
│  └───────────────────┘       ├────────────────────┤  │   ├────────────────────┤     │
│          │                   │ 6.133              │  │   │ 6.134              │     │
│          └──────────────────▶│ Summary Schema     │──┘   │ Plan Modification  │     │
│                              └────────────────────┘      └─────────┬──────────┘     │
│                                                                    │                │
│  Group D (Dependent)          Group E (Archivalist)                │                │
│  ┌────────────────────┐      ┌────────────────────┐                │                │
│  │ 6.135              │      │ 6.139              │◀───────────────┘                │
│  │ Crash Recovery     │      │ Task Event         │                                 │
│  ├────────────────────┤      │ Submission         │                                 │
│  │ 6.136              │      │ (Archivalist Integ)│                                 │
│  │ Guide Routing      │      └─────────┬──────────┘                                 │
│  └─────────┬──────────┘                │                                            │
│            │                           │                                            │
│            └───────────────────────────┼────────────────────┐                       │
│                                        │                    │                       │
│  Group F (Assembly)                    │                    │                       │
│  ┌────────────────────┐                │                    │                       │
│  │ 6.137              │◀───────────────┴────────────────────┘                       │
│  │ Agent Core         │                                                             │
│  └─────────┬──────────┘                                                             │
│            │                                                                        │
│            ▼                                                                        │
│  Group G (Validation)                   ┌────────────────────┐                      │
│                                         │ 6.138              │                      │
│                                         │ Integration Tests  │                      │
│                                         └────────────────────┘                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Parallelization Summary

| Phase | Items | Parallel Execution |
|-------|-------|-------------------|
| 0 | 0.1, 0.2, 0.3, 0.4, 0.5, 0.6 | All six in parallel |
| 1 | 1.1, 1.2, 1.3 | 1.1 and 1.2 in parallel; 1.3 after 1.1+1.2 |
| 2 | 2.1, 2.2, 2.3 | 2.1 and 2.2 in parallel; 2.3 after 2.1+2.2 |
| 2.2 | 6.130-6.139 | See Orchestrator subphase table below |
| 3 | 3.1 | Sequential (after Phase 2) |
| 4 | 4.1, 4.2 | Both in parallel (after Phase 3) |
| 5 | 5.1, 5.2, 5.3 | Sequential (after Phase 4) |

### Orchestrator Subphase (2.2) Parallel Execution

| Group | Tasks | Dependencies | Parallel Capacity |
|-------|-------|--------------|-------------------|
| A | 6.130 (Buffer Types) | None | 1 pipeline |
| B | 6.132 (Health Monitor), 6.133 (Summary Schema) | Group A | 2 pipelines |
| C | 6.131 (Skills), 6.134 (Plan Modification) | Groups A + B | 2 pipelines |
| D | 6.135 (Recovery), 6.136 (Routing) | Group C | 2 pipelines |
| E | 6.139 (Task Event Submission) | Groups C + D | 1 pipeline |
| F | 6.137 (Agent Core) | Groups A-E | 1 pipeline |
| G | 6.138 (Integration Tests) | Group F | 1 pipeline |

**Note**: Phase 2.3 (Pipeline Infrastructure) depends on 2.1 (Engineer) and 2.2 (Orchestrator) as it combines them with Inspector and Tester instances into isolated execution contexts.

### Cross-System Task Mapping

| System | Tasks | Phase | Dependencies |
|--------|-------|-------|--------------|
| VectorGraphDB | 6.1-6.20 | Phase 6 | Phase 0, Phase 1 |
| Agent Efficiency | 6.21-6.42 | Phase 6 | Phase 6.1-6.20 |
| Pipeline Core | 6.43-6.49 | Phase 6 | Phase 2 |
| Tool Execution | 6.50-6.80 | Phase 6 | Phase 6.43-6.49 |
| Pipeline Variants | 6.101-6.112 | Phase 6 | Orchestrator (6.130-6.139) |
| Secret Management | 6.113-6.121 | Phase 6 | None (parallel with all) |
| Credential Broker | 6.122-6.129 | Phase 6 | 6.113-6.121 |
| **Orchestrator** | **6.130-6.139** | **Phase 2.2** | **Phase 0.2 (DAG), 0.4 (Guide), 0.5 (Archivalist)** |

### Critical Path

```
0.1 (Session) → 0.4 (Guide) → 6.130 (Buffer) → 6.131 (Skills) → 6.139 (Events) → 6.137 (Agent Core) → 2.3 (Pipeline) → 3.1 (Architect) → 5.1 (Coordinator)
```

### Orchestrator in Full System Context

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                         ORCHESTRATOR INTEGRATION POINTS                              │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  UPSTREAM DEPENDENCIES                    ORCHESTRATOR                              │
│  ────────────────────                    ────────────                               │
│                                                                                     │
│  ┌─────────────────┐                    ┌────────────────────────────────────────┐  │
│  │ 0.2 DAG Engine  │───executes───────▶│                                        │  │
│  └─────────────────┘                    │  Orchestrator Agent (Claude Haiku 4.5) │  │
│  ┌─────────────────┐                    │  ─────────────────────────────────────  │  │
│  │ 0.4 Guide       │◀──routes──────────│                                        │  │
│  └─────────────────┘                    │  • Status queries (6.131)              │  │
│                                         │  • Health monitoring (6.132)           │  │
│  ┌─────────────────┐  task events       │  • Plan modifications (6.134)          │  │
│  │                 │◀──────────────────│  • Crash recovery (6.135)              │  │
│  │ 0.5 Archivalist │   (6.139)          │  • Task event submission (6.139)       │  │
│  │                 │───patterns────────▶│  • Archivalist queries (6.139)         │  │
│  │                 │◀──summaries───────│                                        │  │
│  └─────────────────┘                    └────────────────────────────────────────┘  │
│        ▲                                              │                             │
│        │ BIDIRECTIONAL                               │                             │
│        │ ────────────                                │                             │
│        │ • ORCHESTRATOR_TASK_EVENT (completed/failed/cancelled)                     │
│        │ • ORCHESTRATOR_ARCHIVALIST_REQUEST (query patterns, failures)              │
│        │ • ORCHESTRATOR_SUMMARY (compaction summaries)                              │
│        │                                             │                             │
│  DOWNSTREAM DEPENDENTS                               ▼                             │
│  ────────────────────                                                              │
│                                                                                     │
│  ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐                   │
│  │ 2.3 Pipeline    │   │ 6.101-6.112     │   │ 3.1 Architect   │                   │
│  │ Infrastructure  │   │ Pipeline        │   │                 │                   │
│  │                 │   │ Variants        │   │ (receives       │                   │
│  │ (orchestrates   │   │                 │   │  failure        │                   │
│  │  task execution)│   │ (Orchestrator   │   │  reports)       │                   │
│  │                 │   │  manages        │   │                 │                   │
│  │                 │   │  variant groups)│   │                 │                   │
│  └─────────────────┘   └─────────────────┘   └─────────────────┘                   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## Phase 6: VectorGraphDB Implementation

**Goal**: Build a unified embedded database combining vector similarity search with graph-based structural queries across all three domains (Code, History, Academic).

**Dependencies**: Phase 0 (Session Manager), Phase 1 (Knowledge Agents - particularly Archivalist and Librarian foundations)

**Parallelization**: Items 6.1-6.4 can execute in parallel. Items 6.5-6.11 (mitigations) can execute in parallel after 6.1-6.4. Item 6.12 requires all prior items.

---

### 6.1 SQLite Schema & Core Types

Creates the database schema and type definitions for the VectorGraphDB.

**Files to create:**
- `core/vectorgraphdb/schema.sql`
- `core/vectorgraphdb/types.go`
- `core/vectorgraphdb/constants.go`
- `core/vectorgraphdb/db.go`
- `core/vectorgraphdb/db_test.go`

**Acceptance Criteria:**

#### Schema Creation
- [ ] Create `nodes` table with: id, domain, node_type, content_hash, metadata (JSON), created_at, updated_at, accessed_at
- [ ] Create `edges` table with: id, from_node_id, to_node_id, edge_type, weight, metadata (JSON), created_at
- [ ] Create `vectors` table with: id, node_id, embedding (BLOB - 768 float32s packed), magnitude, model_version
- [ ] Create `provenance` table with: id, node_id, source_type, source_id, confidence, verified_at, verifier
- [ ] Create `conflicts` table with: id, node_a_id, node_b_id, conflict_type, detected_at, resolution, resolved_at
- [ ] Create `hnsw_graph` table with: id, node_id, layer, neighbors (JSON array)
- [ ] Create `hnsw_metadata` table with: id, entry_point, max_level, M, ef_construct
- [ ] Create all required indexes for efficient querying
- [ ] Enable WAL mode for concurrent read/write
- [ ] Create migration support for schema versioning

#### Type Definitions
- [ ] Define `Domain` type with constants: `DomainCode`, `DomainHistory`, `DomainAcademic`
- [ ] Define `NodeType` type with all node types per domain:
  - Code: `NodeTypeFile`, `NodeTypeFunction`, `NodeTypeType`, `NodeTypePackage`, `NodeTypeImport`
  - History: `NodeTypeSession`, `NodeTypeDecision`, `NodeTypeFailure`, `NodeTypePattern`, `NodeTypeWorkflow`
  - Academic: `NodeTypeRepo`, `NodeTypeDoc`, `NodeTypeArticle`, `NodeTypeConcept`, `NodeTypeBestPractice`
- [ ] Define `EdgeType` type with all edge types:
  - Structural: `EdgeTypeCalls`, `EdgeTypeImports`, `EdgeTypeDefines`, `EdgeTypeImplements`, `EdgeTypeContains`
  - Temporal: `EdgeTypeFollows`, `EdgeTypeCauses`, `EdgeTypeResolves`
  - Cross-domain: `EdgeTypeReferences`, `EdgeTypeAppliesTo`, `EdgeTypeDocuments`, `EdgeTypeModified`
- [ ] Define `GraphNode` struct with all fields
- [ ] Define `GraphEdge` struct with all fields
- [ ] Define `VectorData` struct for embedding storage
- [ ] Define `Provenance` struct for source tracking
- [ ] Define `Conflict` struct for contradiction detection

#### Database Operations
- [ ] Implement `Open(path string) (*VectorGraphDB, error)`
- [ ] Implement `Close() error`
- [ ] Implement `Migrate() error` for schema migrations
- [ ] Implement `Vacuum() error` for database compaction
- [ ] Implement `Stats() (*DBStats, error)` for metrics

**Tests:**
- [ ] Schema creation succeeds on fresh database
- [ ] Migrations apply correctly
- [ ] Concurrent read/write operations succeed (WAL mode)
- [ ] Foreign key constraints enforced
- [ ] Index performance verified with EXPLAIN QUERY PLAN

```go
// Required types
type Domain string
const (
    DomainCode     Domain = "code"
    DomainHistory  Domain = "history"
    DomainAcademic Domain = "academic"
)

type NodeType string
// ... (full list per domain)

type EdgeType string
// ... (full list)

type GraphNode struct {
    ID          string            `json:"id"`
    Domain      Domain            `json:"domain"`
    NodeType    NodeType          `json:"node_type"`
    ContentHash string            `json:"content_hash"`
    Metadata    map[string]any    `json:"metadata"`
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
    AccessedAt  time.Time         `json:"accessed_at"`
}
```

---

### 6.2 HNSW Index Implementation

Implements Hierarchical Navigable Small World index for O(log n) approximate nearest neighbor search in pure Go.

**Files to create:**
- `core/vectorgraphdb/hnsw/index.go`
- `core/vectorgraphdb/hnsw/layer.go`
- `core/vectorgraphdb/hnsw/distance.go`
- `core/vectorgraphdb/hnsw/persistence.go`
- `core/vectorgraphdb/hnsw/hnsw_test.go`

**Acceptance Criteria:**

#### Index Structure
- [x] Implement multi-layer graph structure with exponential decay
- [x] Store vectors with magnitudes for fast cosine similarity
- [x] Track domain and node type per vector for filtered search
- [x] Thread-safe with RWMutex for concurrent operations
- [x] Configurable parameters: M (connections per node), efConstruction, efSearch

#### Insert Operation
- [x] Random level assignment using exponential distribution: `floor(-log(rand) * levelMult)`
- [x] Search for neighbors at each layer during insertion
- [x] Connect to M best neighbors per layer
- [x] Update entry point if new node has higher level
- [x] Handle insertions with existing ID (update vector)

#### Search Operation
- [x] Start from entry point at top layer
- [x] Greedy search descending through layers
- [x] Expand search at layer 0 with efSearch candidates
- [x] Return top-k results sorted by similarity
- [x] Support filtered search by domain and node type
- [x] Support multi-domain search with result merging

#### Distance Functions
- [x] Implement cosine similarity: `dot(a,b) / (mag(a) * mag(b))`
- [x] Pre-compute magnitudes on insert for performance
- [x] SIMD-optimized dot product (optional, fallback to scalar)

#### Persistence
- [x] Save index to SQLite tables (hnsw_graph, hnsw_metadata)
- [x] Load index from SQLite on startup
- [x] Incremental save support (only changed nodes)
- [x] Atomic save with transaction

#### Performance Requirements
- [x] Insert: < 1ms for index with < 100K nodes
- [x] Search: < 5ms for k=10 on index with < 100K nodes
- [x] Memory: ~100 bytes per node overhead (connections + metadata)

**Tests:**
- [x] Insert 10K random vectors, verify all retrievable
- [x] Search returns correct nearest neighbors (vs brute force)
- [x] Filtered search respects domain constraints
- [x] Concurrent insert/search operations (race detector)
- [x] Persistence: save, reload, verify identical results
- [x] Performance benchmarks for insert and search

```go
// Required interface
type HNSWIndex struct {
    mu          sync.RWMutex
    layers      []map[string][]string  // layer -> nodeID -> neighbor IDs
    vectors     map[string][]float32   // nodeID -> embedding
    magnitudes  map[string]float64     // nodeID -> pre-computed magnitude
    domains     map[string]Domain      // nodeID -> domain
    nodeTypes   map[string]NodeType    // nodeID -> node type
    M           int                    // max connections per node per layer
    efConstruct int                    // construction search width
    efSearch    int                    // search width
    levelMult   float64                // level generation multiplier
    maxLevel    int                    // current max level
    entryPoint  string                 // entry node ID
}

func (h *HNSWIndex) Insert(id string, vector []float32, domain Domain, nodeType NodeType) error
func (h *HNSWIndex) Search(query []float32, k int, filter *SearchFilter) []SearchResult
func (h *HNSWIndex) Delete(id string) error
func (h *HNSWIndex) Save(db *sql.DB) error
func (h *HNSWIndex) Load(db *sql.DB) error
```

---

### 6.3 Node & Edge Management

Implements CRUD operations for nodes and edges with cross-domain support.

**Files to create:**
- `core/vectorgraphdb/nodes.go`
- `core/vectorgraphdb/edges.go`
- `core/vectorgraphdb/batch.go`
- `core/vectorgraphdb/nodes_test.go`
- `core/vectorgraphdb/edges_test.go`

**Acceptance Criteria:**

#### Node Operations
- [x] `InsertNode(node *GraphNode, embedding []float32) error`
- [x] `GetNode(id string) (*GraphNode, error)`
- [x] `UpdateNode(node *GraphNode) error`
- [x] `DeleteNode(id string) error` (cascade to edges, vectors, provenance)
- [x] `GetNodesByType(domain Domain, nodeType NodeType, limit int) ([]*GraphNode, error)`
- [x] `GetNodesByContentHash(hash string) ([]*GraphNode, error)`
- [x] `TouchNode(id string) error` (update accessed_at)
- [x] Automatic content hash computation on insert/update
- [x] Automatic embedding insertion into HNSW index

#### Edge Operations
- [x] `InsertEdge(edge *GraphEdge) error`
- [x] `GetEdge(id string) (*GraphEdge, error)`
- [x] `GetEdgesBetween(fromID, toID string) ([]*GraphEdge, error)`
- [x] `GetOutgoingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error)`
- [x] `GetIncomingEdges(nodeID string, edgeTypes ...EdgeType) ([]*GraphEdge, error)`
- [x] `DeleteEdge(id string) error`
- [x] `DeleteEdgesBetween(fromID, toID string) error`
- [x] Validate edge endpoints exist before insert
- [x] Cross-domain edge validation (certain edge types allowed between domains)

#### Batch Operations
- [x] `BatchInsertNodes(nodes []*GraphNode, embeddings [][]float32) error`
- [x] `BatchInsertEdges(edges []*GraphEdge) error`
- [x] `BatchDeleteNodes(ids []string) error`
- [x] Transaction support for atomic batch operations
- [x] Progress callback for large batch operations

#### Cross-Domain Edge Rules
- [x] `EdgeTypeReferences`: Code ↔ Academic (references doc to code)
- [x] `EdgeTypeAppliesTo`: Academic → Code (best practice applies to file)
- [x] `EdgeTypeDocuments`: Academic → Code (article documents pattern)
- [x] `EdgeTypeModified`: History → Code (session modified file)
- [x] `EdgeTypeLedTo`: History → History (decision led to outcome)
- [x] `EdgeTypeUsedPattern`: History → Academic (session used pattern)
- [x] Reject invalid cross-domain edges

**Tests:**
- [x] Insert and retrieve nodes across all domains
- [x] Insert and retrieve edges across all types
- [x] Delete node cascades to related data
- [x] Batch insert 10K nodes in single transaction
- [x] Cross-domain edge validation enforced
- [x] Invalid edges rejected with clear error

---

### 6.4 Vector Search & Graph Traversal

Implements combined vector similarity and graph traversal queries.

**Files to create:**
- `core/vectorgraphdb/search.go`
- `core/vectorgraphdb/traversal.go`
- `core/vectorgraphdb/query.go`
- `core/vectorgraphdb/search_test.go`
- `core/vectorgraphdb/traversal_test.go`

**Acceptance Criteria:**

#### Vector Search
- [x] `VectorSearch(query []float32, k int, filter *SearchFilter) ([]*SearchResult, error)`
- [x] `VectorSearchByText(text string, k int, filter *SearchFilter) ([]*SearchResult, error)` (uses embedder)
- [x] `VectorSearchMultiDomain(query []float32, k int, domains []Domain) ([]*SearchResult, error)`
- [x] Filter by domain, node type, min similarity threshold
- [x] Return results with similarity score, node, and metadata
- [x] Support hybrid scoring (vector similarity + graph distance)

#### Graph Traversal
- [x] `GetNeighbors(nodeID string, depth int, edgeTypes ...EdgeType) ([]*GraphNode, error)`
- [x] `ShortestPath(fromID, toID string, edgeTypes ...EdgeType) ([]*GraphNode, error)`
- [x] `GetConnectedComponent(nodeID string, maxNodes int) ([]*GraphNode, error)`
- [x] `FindPath(fromID, toID string, constraints *PathConstraints) ([]*PathResult, error)`
- [x] BFS and DFS traversal options
- [x] Cycle detection and prevention

#### Combined Queries
- [x] `HybridQuery(text string, constraints *QueryConstraints) ([]*QueryResult, error)`
- [x] First: vector search to find semantic matches
- [x] Then: graph expansion to find related context
- [x] Score combination: `finalScore = α * vectorSim + (1-α) * graphScore`
- [x] Configurable alpha parameter for balance

#### Cross-Domain Queries
- [x] Find code files → related academic docs → history of changes
- [x] Find failure → resolution → similar code patterns
- [x] Find concept → implementing code → usage examples
- [x] Query result includes domain path for provenance

#### Query Optimization
- [x] Use domain hints to reduce search space
- [x] Cache frequent traversal paths
- [x] Early termination for low-relevance branches
- [x] Parallel traversal for independent subgraphs

**Tests:**
- [ ] Vector search returns semantically similar nodes
- [ ] Graph traversal respects depth limits
- [ ] Combined query returns relevant cross-domain results
- [ ] Performance: hybrid query < 50ms for 100K node graph
- [ ] Cycle detection prevents infinite loops

```go
// Query types
type SearchFilter struct {
    Domains       []Domain
    NodeTypes     []NodeType
    MinSimilarity float64
    MaxResults    int
    IncludeEdges  bool
}

type QueryConstraints struct {
    Text          string
    Domains       []Domain
    GraphDepth    int
    EdgeTypes     []EdgeType
    Alpha         float64  // vector vs graph balance
    MinScore      float64
    MaxResults    int
}

type QueryResult struct {
    Node         *GraphNode
    VectorScore  float64
    GraphScore   float64
    FinalScore   float64
    Path         []*GraphNode  // path from query origin
    Edges        []*GraphEdge  // edges traversed
}
```

---

### 6.5 Mitigation 1: Hallucination Firewall (DONE)

Prevents storage of unverified LLM outputs to avoid contaminating the knowledge base.

**Files to create:**
- `core/vectorgraphdb/mitigations/hallucination_firewall.go`
- `core/vectorgraphdb/mitigations/verification.go`
- `core/vectorgraphdb/mitigations/hallucination_firewall_test.go`

**Acceptance Criteria:**

#### Firewall Implementation
- [ ] Intercept all LLM-sourced data before storage
- [ ] Verify existence of referenced files, functions, patterns
- [ ] Cross-reference claims against existing verified data
- [ ] Assign confidence scores based on verification depth
- [ ] Block storage for unverifiable claims
- [ ] Queue ambiguous claims for human review

#### Verification Strategies
- [ ] `VerifyFileExists(path string) (bool, error)` - check file system
- [ ] `VerifyFunctionExists(file, funcName string) (bool, error)` - parse AST
- [ ] `VerifyPatternExists(pattern string) (bool, float64, error)` - search codebase
- [ ] `CrossReferenceNode(node *GraphNode) (float64, []string, error)` - check against DB
- [ ] `VerifyEdgeValid(edge *GraphEdge) (bool, error)` - validate relationship

#### Confidence Scoring
- [ ] 1.0: Directly verified (file exists, AST confirms)
- [ ] 0.8-0.99: Cross-referenced with multiple sources
- [ ] 0.6-0.79: Partial verification (some claims verified)
- [ ] 0.4-0.59: Low verification (inference from patterns)
- [ ] 0.0-0.39: Unverifiable (blocked)
- [ ] Configurable threshold for storage (default: 0.6)

#### Review Queue
- [ ] `QueueForReview(node *GraphNode, reason string) error`
- [ ] `GetReviewQueue(limit int) ([]*ReviewItem, error)`
- [ ] `ApproveReview(id string, reviewer string) error`
- [ ] `RejectReview(id string, reason string) error`
- [ ] Automatic expiration of stale review items

#### Metrics
- [ ] Track verified vs rejected claims
- [ ] Track verification failure reasons
- [ ] Track review queue depth and resolution time

**Tests:**
- [ ] Valid file references pass verification
- [ ] Invalid file references blocked
- [ ] Cross-referenced data gets high confidence
- [ ] Ambiguous data queued for review
- [ ] Review workflow completes correctly

```go
type HallucinationFirewall struct {
    db                *VectorGraphDB
    librarian         LibrarianClient  // for file/code verification
    minConfidence     float64
    reviewQueue       chan *ReviewItem
    verificationCache *VerificationCache
}

func (f *HallucinationFirewall) Verify(ctx context.Context, node *GraphNode, source SourceType) (*VerificationResult, error)
func (f *HallucinationFirewall) Store(ctx context.Context, node *GraphNode, verification *VerificationResult) error

type VerificationResult struct {
    Verified     bool
    Confidence   float64
    Checks       []VerificationCheck
    FailedChecks []string
    ShouldQueue  bool
    QueueReason  string
}
```

---

### 6.6 Mitigation 2: Freshness Tracking & Decay (DONE)

Tracks data freshness and applies temporal decay to prevent stale data from polluting results.

**Files to create:**
- `core/vectorgraphdb/mitigations/freshness.go`
- `core/vectorgraphdb/mitigations/decay.go`
- `core/vectorgraphdb/mitigations/freshness_test.go`

**Acceptance Criteria:**

#### Freshness Tracking
- [ ] Track last_verified timestamp for each node
- [ ] Track source_modified timestamp (e.g., file mtime)
- [ ] Detect stale nodes: node.updated_at < source.modified_at
- [ ] Mark nodes requiring re-verification
- [ ] Automatic staleness detection on access

#### Decay Functions
- [ ] Exponential decay: `score * exp(-λ * age_hours)`
- [ ] Linear decay with cliff: `max(0, score - age_days * decay_rate)`
- [ ] Domain-specific decay rates:
  - Code: λ = 0.01 (slow decay, code changes less frequently)
  - History: λ = 0.05 (moderate decay)
  - Academic: λ = 0.001 (very slow decay, docs are stable)
- [ ] Apply decay to search result scores

#### Freshness Score Calculation
- [ ] `ComputeFreshnessScore(node *GraphNode) float64`
- [ ] Consider: time since update, time since access, source freshness
- [ ] Weight formula: `freshness = base * decay(age) * accessBoost(recency)`
- [ ] Configurable weights per domain

#### Staleness Resolution
- [ ] `FindStaleNodes(domain Domain, threshold time.Duration) ([]*GraphNode, error)`
- [ ] `MarkStale(nodeID string) error`
- [ ] `RefreshNode(nodeID string) error` (re-verify and update)
- [ ] Batch refresh for bulk staleness resolution
- [ ] Background staleness scanner (configurable interval)

#### Integration with Search
- [ ] Apply decay before returning search results
- [ ] Filter out nodes below freshness threshold
- [ ] Return freshness score in result metadata
- [ ] Option to include stale results with warning flag

**Tests:**
- [ ] Fresh nodes have freshness score near 1.0
- [ ] Old nodes have decayed freshness score
- [ ] Stale node detection works correctly
- [ ] Search results respect freshness thresholds
- [ ] Background scanner identifies stale nodes

```go
type FreshnessTracker struct {
    db          *VectorGraphDB
    decayRates  map[Domain]float64
    scanner     *StalenessScannerConfig
    refreshChan chan string
}

func (f *FreshnessTracker) GetFreshness(nodeID string) (*FreshnessInfo, error)
func (f *FreshnessTracker) ApplyDecay(results []*SearchResult) []*SearchResult
func (f *FreshnessTracker) MarkStale(nodeID string) error
func (f *FreshnessTracker) RefreshNode(ctx context.Context, nodeID string) error
func (f *FreshnessTracker) StartScanner(ctx context.Context) error

type FreshnessInfo struct {
    NodeID          string
    LastVerified    time.Time
    SourceModified  time.Time
    IsStale         bool
    FreshnessScore  float64
    DecayRate       float64
    NextScanAt      time.Time
}
```

---

### 6.7 Mitigation 3: Source Attribution & Provenance (DONE)

Tracks the origin and verification chain for all stored information.

**Files to create:**
- `core/vectorgraphdb/mitigations/provenance.go`
- `core/vectorgraphdb/mitigations/attribution.go`
- `core/vectorgraphdb/mitigations/provenance_test.go`

**Acceptance Criteria:**

#### Provenance Record
- [ ] Store source type: `verified_code`, `llm_inference`, `user_input`, `academic_source`, `cross_reference`
- [ ] Store source ID: file path, document URL, session ID, etc.
- [ ] Store confidence score from verification
- [ ] Store verifier: human, automated, cross-reference
- [ ] Store verification chain (what verified what)

#### Source Types Hierarchy
- [ ] `SourceTypeCode` (1.0 base trust) - directly from codebase
- [ ] `SourceTypeUser` (0.95 base trust) - explicit user input
- [ ] `SourceTypeAcademic` (0.85 base trust) - documentation, articles
- [ ] `SourceTypeCrossRef` (0.75 base trust) - inferred from multiple sources
- [ ] `SourceTypeLLM` (0.5 base trust) - LLM inference (requires verification)

#### Attribution Operations
- [ ] `RecordProvenance(nodeID string, prov *Provenance) error`
- [ ] `GetProvenance(nodeID string) ([]*Provenance, error)`
- [ ] `GetProvenanceChain(nodeID string, depth int) ([]*ProvenanceChain, error)`
- [ ] `FindBySource(sourceType SourceType, sourceID string) ([]*GraphNode, error)`
- [ ] `InvalidateBySource(sourceID string) error` (mark all from source as stale)

#### Verification Chain
- [ ] Track what data verified other data
- [ ] Build verification DAG for complex claims
- [ ] Compute transitive confidence: `conf = prod(chain_confs) * base_conf`
- [ ] Detect circular verification (reject)

#### Citation Generation
- [ ] `GenerateCitation(nodeID string) (string, error)`
- [ ] Include source, verification status, timestamp
- [ ] Format for LLM context injection
- [ ] Support multiple citation formats (inline, footnote, structured)

**Tests:**
- [ ] Provenance recorded correctly for all source types
- [ ] Provenance chain retrieved correctly
- [ ] Confidence correctly propagated through chain
- [ ] Circular verification detected and rejected
- [ ] Citation generation produces valid output

```go
type ProvenanceTracker struct {
    db *VectorGraphDB
}

type Provenance struct {
    ID          string     `json:"id"`
    NodeID      string     `json:"node_id"`
    SourceType  SourceType `json:"source_type"`
    SourceID    string     `json:"source_id"`
    Confidence  float64    `json:"confidence"`
    VerifiedAt  time.Time  `json:"verified_at"`
    Verifier    string     `json:"verifier"`  // "human", "automated", "cross_ref"
    VerifiedBy  []string   `json:"verified_by"`  // IDs of verifying nodes
}

type ProvenanceChain struct {
    Node             *GraphNode
    DirectProvenance *Provenance
    Chain            []*Provenance
    TransitiveConf   float64
}
```

---

### 6.8 Mitigation 4: Trust Hierarchy (DONE)

Implements a trust scoring system that weights information by source reliability.

**Files to create:**
- `core/vectorgraphdb/mitigations/trust.go`
- `core/vectorgraphdb/mitigations/trust_scoring.go`
- `core/vectorgraphdb/mitigations/trust_test.go`

**Acceptance Criteria:**

#### Trust Levels
- [ ] Level 6 - Verified Code (1.0): AST-parsed, type-checked
- [ ] Level 5 - Recent History (0.9): Last 24h decisions/outcomes
- [ ] Level 4 - Official Docs (0.8): README, API docs, comments
- [ ] Level 3 - Old History (0.7): >24h ago, verified outcomes
- [ ] Level 2 - External Articles (0.5): Blogs, tutorials, StackOverflow
- [ ] Level 1 - LLM Inference (0.3): Unverified LLM output
- [ ] Level 0 - Unknown (0.1): No provenance

#### Trust Score Computation
- [ ] `ComputeTrustScore(node *GraphNode) (float64, error)`
- [ ] Consider: source type, age, verification status, cross-references
- [ ] Formula: `trust = baseTrust * freshnessDecay * verificationBoost * crossRefBoost`
- [ ] Cross-reference boost: +0.1 per independent verification (max +0.3)

#### Trust-Weighted Search
- [ ] Apply trust scores to search results
- [ ] Option to filter by minimum trust level
- [ ] Sort by: `finalScore = similarity * trustWeight`
- [ ] Return trust metadata with results

#### Trust Promotion/Demotion
- [ ] `PromoteTrust(nodeID string, reason string) error` (human verification)
- [ ] `DemoteTrust(nodeID string, reason string) error` (contradicted)
- [ ] Automatic demotion on staleness
- [ ] Automatic promotion on cross-reference verification

#### Trust Audit Log
- [ ] Log all trust changes with timestamp, reason, actor
- [ ] Query trust history for a node
- [ ] Export trust report for analysis

**Tests:**
- [ ] Trust levels assigned correctly by source
- [ ] Trust decay applied correctly over time
- [ ] Search results weighted by trust
- [ ] Promotion/demotion changes trust correctly
- [ ] Audit log records all changes

```go
type TrustHierarchy struct {
    db         *VectorGraphDB
    levels     map[SourceType]TrustLevel
    decayRates map[TrustLevel]float64
}

type TrustLevel int
const (
    TrustUnknown TrustLevel = iota
    TrustLLMInference
    TrustExternalArticle
    TrustOldHistory
    TrustOfficialDocs
    TrustRecentHistory
    TrustVerifiedCode
)

type TrustInfo struct {
    NodeID          string
    TrustLevel      TrustLevel
    TrustScore      float64
    BaseScore       float64
    FreshnessBoost  float64
    VerifyBoost     float64
    CrossRefBoost   float64
    EffectiveScore  float64
}

func (t *TrustHierarchy) GetTrustInfo(nodeID string) (*TrustInfo, error)
func (t *TrustHierarchy) ApplyTrust(results []*SearchResult) []*SearchResult
func (t *TrustHierarchy) Promote(nodeID string, reason string, actor string) error
func (t *TrustHierarchy) Demote(nodeID string, reason string, actor string) error
```

---

### 6.9 Mitigation 5: Conflict Detection (DONE)

Detects and tracks contradictions between stored information.

**Files to create:**
- `core/vectorgraphdb/mitigations/conflicts.go`
- `core/vectorgraphdb/mitigations/contradiction.go`
- `core/vectorgraphdb/mitigations/conflicts_test.go`

**Acceptance Criteria:**

#### Conflict Detection
- [ ] Detect semantic contradictions using embedding similarity + content analysis
- [ ] Detect temporal contradictions (newer data contradicts older)
- [ ] Detect structural contradictions (graph inconsistencies)
- [ ] Run detection on insert and on query

#### Conflict Types
- [ ] `ConflictSemantic`: Similar embeddings, contradictory content
- [ ] `ConflictTemporal`: Same topic, different answers at different times
- [ ] `ConflictStructural`: Graph edges that shouldn't coexist
- [ ] `ConflictProvenance`: Same source, different claims

#### Detection Algorithms
- [ ] `DetectOnInsert(node *GraphNode) ([]*Conflict, error)` - check against existing
- [ ] `DetectInResults(results []*QueryResult) ([]*Conflict, error)` - check within results
- [ ] `ScanForConflicts(domain Domain) ([]*Conflict, error)` - batch scan
- [ ] Semantic comparison: high similarity (>0.9) + low content match (<0.5) = potential conflict
- [ ] Temporal comparison: same subject, different values, temporal gap

#### Conflict Resolution
- [ ] `ResolveConflict(id string, resolution Resolution) error`
- [ ] Resolution options: `KeepNewer`, `KeepTrusted`, `KeepBoth`, `MarkBothStale`, `HumanReview`
- [ ] Automatic resolution for clear cases (much newer + higher trust)
- [ ] Queue ambiguous conflicts for human review

#### Conflict Reporting
- [ ] `GetActiveConflicts(limit int) ([]*Conflict, error)`
- [ ] `GetConflictsForNode(nodeID string) ([]*Conflict, error)`
- [ ] `GetConflictStats() (*ConflictStats, error)`
- [ ] Include conflicts in search result metadata

**Tests:**
- [ ] Semantic conflicts detected correctly
- [ ] Temporal conflicts detected correctly
- [ ] Automatic resolution works for clear cases
- [ ] Ambiguous conflicts queued for review
- [ ] Resolved conflicts marked correctly

```go
type ConflictDetector struct {
    db              *VectorGraphDB
    hnsw            *HNSWIndex
    semanticThresh  float64  // similarity threshold for potential conflict
    contentAnalyzer ContentAnalyzer
}

type Conflict struct {
    ID           string       `json:"id"`
    NodeAID      string       `json:"node_a_id"`
    NodeBID      string       `json:"node_b_id"`
    ConflictType ConflictType `json:"conflict_type"`
    Similarity   float64      `json:"similarity"`
    Details      string       `json:"details"`
    DetectedAt   time.Time    `json:"detected_at"`
    Resolution   *Resolution  `json:"resolution,omitempty"`
    ResolvedAt   *time.Time   `json:"resolved_at,omitempty"`
}

func (d *ConflictDetector) DetectOnInsert(ctx context.Context, node *GraphNode, embedding []float32) ([]*Conflict, error)
func (d *ConflictDetector) Resolve(conflictID string, resolution Resolution) error
func (d *ConflictDetector) AnnotateResults(results []*QueryResult) []*QueryResult
```

---

### 6.10 Mitigation 6: Context Quality Scoring (DONE)

Optimizes context selection to maximize information density while minimizing token usage.

**Files to create:**
- `core/vectorgraphdb/mitigations/quality.go`
- `core/vectorgraphdb/mitigations/scoring.go`
- `core/vectorgraphdb/mitigations/quality_test.go`

**Acceptance Criteria:**

#### Quality Metrics
- [ ] `Relevance`: Vector similarity to query
- [ ] `Freshness`: Temporal decay score
- [ ] `Trust`: Trust hierarchy score
- [ ] `Density`: Information per token (unique content / token count)
- [ ] `Diversity`: Penalty for redundant information

#### Quality Score Formula
- [ ] `quality = w1*relevance + w2*freshness + w3*trust + w4*density - w5*redundancy`
- [ ] Default weights: relevance=0.35, freshness=0.20, trust=0.25, density=0.15, redundancy=0.05
- [ ] Configurable weights per use case

#### Token Estimation
- [ ] `EstimateTokens(node *GraphNode) int` - estimate token count for node
- [ ] Use tiktoken-compatible estimation for accuracy
- [ ] Cache estimates for performance
- [ ] Account for formatting overhead

#### Context Selection
- [ ] `SelectContext(results []*QueryResult, tokenBudget int) ([]*ContextItem, error)`
- [ ] Greedy selection: highest quality-per-token first
- [ ] Respect token budget strictly
- [ ] Ensure diversity (don't select redundant items)
- [ ] Return selection rationale

#### Redundancy Detection
- [ ] Detect overlapping content between nodes
- [ ] Compute content similarity matrix
- [ ] Apply diversity penalty for similar selections
- [ ] Prefer unique perspectives

#### Budget Optimization
- [ ] Given budget B, maximize sum of quality scores
- [ ] Knapsack-style optimization (approximation OK for speed)
- [ ] Return unused budget for caller

**Tests:**
- [ ] Quality scores computed correctly
- [ ] Token estimation matches actual
- [ ] Context selection respects budget
- [ ] Diversity penalty applied correctly
- [ ] Selection maximizes quality within budget

```go
type ContextQualityScorer struct {
    db           *VectorGraphDB
    weights      QualityWeights
    tokenizer    Tokenizer
    embedder     Embedder
}

type QualityWeights struct {
    Relevance   float64
    Freshness   float64
    Trust       float64
    Density     float64
    Redundancy  float64
}

type ContextItem struct {
    Node          *GraphNode
    QualityScore  float64
    TokenCount    int
    ScorePerToken float64
    Components    QualityComponents
    Selected      bool
    Reason        string
}

type QualityComponents struct {
    Relevance   float64
    Freshness   float64
    Trust       float64
    Density     float64
    Redundancy  float64
}

func (s *ContextQualityScorer) Score(node *GraphNode, query []float32) (*ContextItem, error)
func (s *ContextQualityScorer) SelectContext(results []*QueryResult, budget int) ([]*ContextItem, int, error)
```

---

### 6.11 Mitigation 7: LLM Prompt Engineering (DONE)

Implements structured context building for LLM prompts with explicit trust and conflict information.

**Files to create:**
- `core/vectorgraphdb/mitigations/prompt.go`
- `core/vectorgraphdb/mitigations/context_builder.go`
- `core/vectorgraphdb/mitigations/prompt_test.go`

**Acceptance Criteria:**

#### Context Structure
- [ ] Group context by domain (Code, History, Academic)
- [ ] Include trust level annotation for each item
- [ ] Include freshness indication (verified date)
- [ ] Include conflict warnings where applicable
- [ ] Include provenance summary

#### Prompt Template
- [ ] Inject structured preamble explaining trust levels
- [ ] Format context items with clear delineation
- [ ] Include explicit instructions about handling conflicts
- [ ] Include instructions about trusting recent code over old docs

#### LLM Context Builder
- [ ] `BuildContext(items []*ContextItem) (*LLMContext, error)`
- [ ] Generate structured markdown for context injection
- [ ] Generate JSON for structured API calls
- [ ] Generate plain text for simple models

#### Trust Instructions
- [ ] "Code from the codebase is authoritative"
- [ ] "Recent session decisions (last 24h) reflect current intent"
- [ ] "Older documentation may be outdated"
- [ ] "If information conflicts, prefer higher trust sources"
- [ ] "Flag any unresolved conflicts in your response"

#### Conflict Handling
- [ ] Annotate conflicting items in context
- [ ] Include resolution guidance
- [ ] Request explicit acknowledgment of conflicts in response

#### Output Formats
- [ ] `FormatAsMarkdown() string` - for human-readable
- [ ] `FormatAsJSON() string` - for structured API
- [ ] `FormatAsXML() string` - for specific models
- [ ] `EstimateTokens() int` - for budget checking

**Tests:**
- [ ] Context built correctly with trust annotations
- [ ] Conflict warnings included
- [ ] Output formats valid
- [ ] Token estimate accurate
- [ ] Preamble instructions present

```go
type LLMContextBuilder struct {
    scorer     *ContextQualityScorer
    trust      *TrustHierarchy
    conflicts  *ConflictDetector
    provenance *ProvenanceTracker
}

type LLMContext struct {
    Preamble     string
    CodeContext  []*AnnotatedItem
    HistContext  []*AnnotatedItem
    AcadContext  []*AnnotatedItem
    Conflicts    []*ConflictSummary
    TotalTokens  int
}

type AnnotatedItem struct {
    Content     string
    Domain      Domain
    TrustLevel  TrustLevel
    TrustScore  float64
    Freshness   time.Time
    Source      string
    Conflicts   []string  // IDs of conflicting items
}

func (b *LLMContextBuilder) Build(ctx context.Context, items []*ContextItem) (*LLMContext, error)
func (c *LLMContext) FormatAsMarkdown() string
func (c *LLMContext) FormatAsJSON() string
func (c *LLMContext) FormatAsSystemPrompt() string
```

---

### 6.12 Unified Query Resolution (DONE)

Integrates all components into a unified query resolution pipeline that **agents invoke via skills**.

**IMPORTANT**: The Unified Resolver is NOT a replacement for agent (LLM) reasoning. It is a **skill implementation** that agents call when they decide they need VectorGraphDB context. The agent always runs first, decides what context it needs, then invokes resolver skills.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    RESOLVER IN CONTEXT                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   1. User query arrives                                                     │
│   2. Intent cache checked (if hit → return cached, 0 tokens)               │
│   3. Cache miss → Agent (LLM) runs                                         │
│   4. Agent decides: "I need code context about auth"                       │
│   5. Agent invokes: search_code("authentication handler")                  │
│      └─── This skill calls UnifiedResolver internally                      │
│   6. UnifiedResolver returns curated context (~800 tokens)                 │
│   7. Agent synthesizes response with curated context                       │
│                                                                             │
│   The agent ALWAYS runs on cache miss. Resolver provides curated context.  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Files to create:**
- `core/vectorgraphdb/resolver.go`
- `core/vectorgraphdb/pipeline.go`
- `core/vectorgraphdb/resolver_test.go`

**Acceptance Criteria:**

#### Resolution Pipeline (called by skills, not directly by users)
1. [ ] Parse query intent (code, history, academic, hybrid)
2. [ ] Check intent cache for exact/similar match
3. [ ] Generate query embedding
4. [ ] Execute HNSW search (domain-filtered)
5. [ ] Graph expansion for context
6. [ ] Apply freshness decay
7. [ ] Apply trust weighting
8. [ ] Detect conflicts in results
9. [ ] Score context quality
10. [ ] Select within token budget
11. [ ] Format results for agent consumption (NOT raw LLM prompt)
12. [ ] Cache result for future queries

#### Unified Resolver
- [ ] `Resolve(ctx context.Context, query string, opts *ResolveOptions) (*Resolution, error)`
- [ ] Single entry point for all queries
- [ ] Automatic domain detection from query text
- [ ] Configurable pipeline stages

#### Resolution Options
- [ ] `Domains []Domain` - restrict to specific domains
- [ ] `TokenBudget int` - max tokens for context
- [ ] `MinTrust TrustLevel` - minimum trust threshold
- [ ] `MinFreshness time.Duration` - max age for results
- [ ] `IncludeConflicts bool` - include conflicting data with warnings
- [ ] `MaxGraphDepth int` - limit graph expansion
- [ ] `CacheResult bool` - whether to cache this resolution

#### Resolution Result
- [ ] Selected context items with annotations
- [ ] Formatted LLM prompt
- [ ] Pipeline metrics (timing per stage)
- [ ] Conflict report
- [ ] Token usage summary
- [ ] Cache hit/miss info

#### Performance Requirements
- [ ] Full resolution < 100ms for typical query
- [ ] Cache hit < 10ms
- [ ] Pipeline stage timing tracked

**Tests:**
- [ ] End-to-end resolution works
- [ ] All pipeline stages execute correctly
- [ ] Cache integration works
- [ ] Options respected
- [ ] Performance targets met

```go
type UnifiedResolver struct {
    db          *VectorGraphDB
    hnsw        *HNSWIndex
    embedder    Embedder
    intentCache *IntentCache
    firewall    *HallucinationFirewall
    freshness   *FreshnessTracker
    provenance  *ProvenanceTracker
    trust       *TrustHierarchy
    conflicts   *ConflictDetector
    scorer      *ContextQualityScorer
    prompter    *LLMContextBuilder
}

type ResolveOptions struct {
    Domains         []Domain
    TokenBudget     int
    MinTrust        TrustLevel
    MinFreshness    time.Duration
    IncludeConflicts bool
    MaxGraphDepth   int
    CacheResult     bool
}

type Resolution struct {
    Query           string
    Context         *LLMContext
    Items           []*ContextItem
    Conflicts       []*Conflict
    Metrics         *PipelineMetrics
    TokensUsed      int
    TokenBudget     int
    CacheHit        bool
}

type PipelineMetrics struct {
    IntentDetect   time.Duration
    CacheCheck     time.Duration
    Embedding      time.Duration
    VectorSearch   time.Duration
    GraphExpand    time.Duration
    Freshness      time.Duration
    Trust          time.Duration
    Conflict       time.Duration
    Scoring        time.Duration
    ContextBuild   time.Duration
    Total          time.Duration
}

func (r *UnifiedResolver) Resolve(ctx context.Context, query string, opts *ResolveOptions) (*Resolution, error)
```

---

### 6.13 Agent Integration: Librarian

Integrates VectorGraphDB with the Librarian agent for code domain. The Librarian agent (LLM) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Skills return curated context to the agent, not raw files. Target ~800 tokens of context per skill invocation vs ~2,000 tokens of raw file content.

**Files to create:**
- `agents/librarian/vectorgraphdb.go`
- `agents/librarian/code_indexer.go`
- `agents/librarian/skills_vectorgraphdb.go`
- `agents/librarian/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### Code Indexing
- [ ] Index files as `NodeTypeFile` with content embeddings
- [ ] Index functions as `NodeTypeFunction` with signature + doc embeddings
- [ ] Index types as `NodeTypeType` with definition embeddings
- [ ] Index packages as `NodeTypePackage`
- [ ] Create structural edges: `Calls`, `Imports`, `Defines`, `Implements`, `Contains`
- [ ] Incremental indexing on file change

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not raw files)
- [ ] Results include relevant snippets, not entire file contents
- [ ] Target: ~800 tokens of context per skill invocation

#### Skills (Agent Decides When to Invoke)
- [ ] `search_code` - Semantic search, returns scored snippets (~10 results max)
- [ ] `get_symbol` - Get specific symbol details with source
- [ ] `get_dependencies` - Graph traversal for what symbol depends on
- [ ] `get_dependents` - Graph traversal for what depends on symbol
- [ ] `find_similar_symbols` - Find semantically similar code
- [ ] `get_file_symbols` - All symbols in a file
- [ ] `get_history_for_code` - Cross-domain: history for code (via edges)

**Tests:**
- [ ] File indexing creates correct nodes/edges
- [ ] Semantic search finds relevant code
- [ ] Graph queries return correct relationships
- [ ] Incremental indexing updates correctly

---

### 6.14 Agent Integration: Archivalist

Integrates VectorGraphDB with the Archivalist agent for history domain. The Archivalist agent (LLM) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Skills return curated context to the agent, not full history dumps. Target ~800 tokens of context per skill invocation.

**Files to create:**
- `agents/archivalist/vectorgraphdb.go`
- `agents/archivalist/history_indexer.go`
- `agents/archivalist/skills_vectorgraphdb.go`
- `agents/archivalist/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### History Indexing
- [ ] Index sessions as `NodeTypeSession`
- [ ] Index decisions as `NodeTypeDecision` with context embeddings
- [ ] Index failures as `NodeTypeFailure` with error + resolution embeddings
- [ ] Index patterns as `NodeTypePattern`
- [ ] Index workflows as `NodeTypeWorkflow`
- [ ] Create temporal edges: `Follows`, `Causes`, `Resolves`
- [ ] Create cross-domain edges: `Modified` (to code files)

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not full history)
- [ ] Results include relevant context, not entire session logs
- [ ] Target: ~800 tokens of context per skill invocation

#### Skills (Agent Decides When to Invoke)
- [ ] `store_summary` - Store summaries from other agents (creates cross-domain edges)
- [ ] `search_history` - Semantic search over historical context
- [ ] `find_patterns` - Find recurring patterns with min occurrence threshold
- [ ] `find_failures` - Find similar failures with resolutions
- [ ] `get_session_history` - Get history for specific session
- [ ] `get_code_for_history` - Cross-domain: code referenced in history (via edges)
- [ ] `get_decisions` - Find past architectural/design decisions

**Tests:**
- [ ] History entries indexed correctly
- [ ] Similar failure search works
- [ ] Pattern matching finds relevant patterns
- [ ] Cross-domain edges to code created

---

### 6.15 Agent Integration: Academic

Integrates VectorGraphDB with the Academic agent for academic domain. The Academic agent (Opus 4.5 for complex reasoning) **decides** when to invoke these skills based on the query.

**IMPORTANT**: Academic uses Opus 4.5 for complex reasoning tasks like research synthesis and approach comparison. Skills return curated context; the agent does the heavy reasoning.

**Files to create:**
- `agents/academic/vectorgraphdb.go`
- `agents/academic/knowledge_indexer.go`
- `agents/academic/skills_vectorgraphdb.go`
- `agents/academic/vectorgraphdb_test.go`

**Acceptance Criteria:**

#### Knowledge Indexing
- [ ] Index GitHub repos as `NodeTypeRepo`
- [ ] Index documentation as `NodeTypeDoc`
- [ ] Index articles as `NodeTypeArticle`
- [ ] Index concepts as `NodeTypeConcept`
- [ ] Index best practices as `NodeTypeBestPractice`
- [ ] Create semantic edges between related concepts
- [ ] Create cross-domain edges: `References`, `AppliesTo`, `Documents`

#### Query Integration
- [ ] VectorGraphDB skills available for agent to invoke (agent decides when)
- [ ] Skills return scored, curated results (not full documents)
- [ ] Agent (Opus 4.5) synthesizes and reasons over curated context
- [ ] Target: ~1,200 tokens of context per skill invocation (higher for research)

#### Skills (Agent Decides When to Invoke)
- [ ] `research` - Deep research with configurable depth (agent synthesizes)
- [ ] `find_best_practices` - Find established best practices
- [ ] `find_papers` - Find academic papers with abstracts
- [ ] `compare_approaches` - Get context for comparing approaches (agent reasons)
- [ ] `synthesize_with_codebase` - Cross-domain: theory vs practice (via edges)
- [ ] `get_code_for_academic` - Cross-domain: code implementing concepts (via edges)
- [ ] `get_history_for_academic` - Cross-domain: history related to concepts (via edges)

**Tests:**
- [ ] Academic content indexed correctly
- [ ] Concept search finds relevant knowledge
- [ ] Cross-domain queries work (academic → code)
- [ ] Best practice recommendations work

---

### 6.16 Cross-Domain Query Integration

Implements unified cross-domain queries across all three agents.

**Files to create:**
- `core/vectorgraphdb/crossdomain.go`
- `core/vectorgraphdb/crossdomain_test.go`

**Acceptance Criteria:**

#### Unified Query Interface
- [ ] Single query entry point for all domains
- [ ] Automatic domain routing based on query intent
- [ ] Result merging from multiple domains
- [ ] Cross-domain path discovery

#### Cross-Domain Scenarios
- [ ] "How do I implement X?" → Academic (patterns) → Code (examples) → History (past attempts)
- [ ] "Why did this fail?" → History (failure) → Code (file) → Academic (known issues)
- [ ] "Best way to do Y?" → Academic (best practices) → Code (existing impl) → History (outcomes)

#### Result Aggregation
- [ ] Merge results from multiple domains
- [ ] De-duplicate overlapping information
- [ ] Order by combined relevance + trust + freshness
- [ ] Annotate with domain source

**Tests:**
- [ ] Cross-domain queries return results from all relevant domains
- [ ] Result merging works correctly
- [ ] Domain paths tracked correctly

---

### 6.17 Performance Benchmarks

Creates benchmarks to validate performance targets.

**Files to create:**
- `core/vectorgraphdb/benchmark_test.go`

**Acceptance Criteria:**

#### Insert Benchmarks
- [ ] Insert 10K nodes: < 10 seconds
- [ ] Insert 100K nodes: < 2 minutes
- [ ] Insert single node: < 1ms average

#### Search Benchmarks
- [ ] Vector search k=10, 10K nodes: < 5ms
- [ ] Vector search k=10, 100K nodes: < 20ms
- [ ] Graph traversal depth=3, 10K nodes: < 10ms
- [ ] Hybrid query, 10K nodes: < 50ms

#### Full Pipeline Benchmarks
- [ ] Unified resolution (cache miss): < 100ms
- [ ] Unified resolution (cache hit): < 10ms
- [ ] Context building: < 20ms

#### Memory Benchmarks
- [ ] Memory per 10K nodes: < 50MB
- [ ] Memory per 100K nodes: < 500MB
- [ ] No memory leaks over 1M operations

**Tests:**
- [ ] All benchmarks pass performance targets
- [ ] Results logged for regression tracking

---

### 6.18 Integration Tests

Creates comprehensive integration tests for the full VectorGraphDB system.

**Files to create:**
- `tests/integration/vectorgraphdb_test.go`
- `tests/integration/crossdomain_test.go`
- `tests/integration/mitigations_test.go`

**Acceptance Criteria:**

#### End-to-End Tests
- [ ] Index codebase, query for function, get results with context
- [ ] Store session history, query for similar failure, get resolution
- [ ] Ingest documentation, query for best practice, get recommendations
- [ ] Cross-domain query spanning all three domains

#### Mitigation Tests
- [ ] Hallucination firewall blocks unverified LLM output
- [ ] Freshness decay affects search results correctly
- [ ] Trust hierarchy weights results correctly
- [ ] Conflict detection finds contradictions
- [ ] Quality scoring maximizes information density

#### Failure Mode Tests
- [ ] Graceful degradation on embedder failure
- [ ] Graceful degradation on SQLite failure
- [ ] Recovery after crash (WAL replay)
- [ ] Concurrent access stress test

**Tests:**
- [ ] All integration tests pass
- [ ] No race conditions detected
- [ ] Recovery tests pass

---

### 6.19 XOR Filter Internal Optimization

Implements XOR filters as **internal optimizations** within search skills, NOT as separate LLM tools. This is critical for actual token savings.

**IMPORTANT**: XOR filters are NOT exposed to LLMs. They are used internally by search skills to return "no_matches" quickly when content doesn't exist.

**Files to create:**
- `core/vectorgraphdb/xorfilter.go`
- `core/vectorgraphdb/xorfilter_manager.go`
- `core/vectorgraphdb/xorfilter_test.go`

**Acceptance Criteria:**

#### XOR Filter Manager
- [ ] `XORFilterManager` struct with filters per domain (code, history, academic)
- [ ] `Contains(domain, topicHash) bool` - checks XOR filter + pending
- [ ] `NotifyAdd(domain, keys)` - adds to pending bloom filter after VDB write
- [ ] `Rebuild(ctx) error` - rebuilds all filters from VectorGraphDB
- [ ] Background goroutine rebuilds periodically (configurable interval)
- [ ] Pending bloom filter for recent additions (zero staleness)
- [ ] Thread-safe with RWMutex for filter access

#### XOR Filter Data Structure
- [ ] Use `github.com/FastFilter/xorfilter` or equivalent
- [ ] Xor8 filters for ~0.3% false positive rate
- [ ] Topic key hashing: consistent hash function for query terms
- [ ] Filter size tracking and metrics

#### Integration with Search Skills
- [ ] `search_code` uses XOR internally for early exit
- [ ] `search_history` uses XOR internally for early exit
- [ ] `search_academic` uses XOR internally for early exit
- [ ] `search_all` uses XOR per-domain for selective searching
- [ ] Early exit returns `{"status": "no_matches", "hint": "..."}` (~30 tokens vs ~800)

#### Multi-Domain Search Skill
- [ ] `search_all` skill searches multiple domains in ONE call
- [ ] Internal XOR check per domain before searching
- [ ] Skips domains with no matches (minimal tokens)
- [ ] Returns results grouped by domain
- [ ] Reduces 3 tool calls to 1 (saves ~1,500 tokens/cross-domain query)

```go
// search_all response format
type MultiDomainResponse struct {
    Query   string                      `json:"query"`
    Results map[string]DomainResult     `json:"results"`
}

type DomainResult struct {
    Status  string         `json:"status"`  // "found" or "no_matches"
    Results []SearchResult `json:"results,omitempty"`
    Count   int            `json:"count"`
}
```

#### Inline Domain Hints
- [ ] Search results include `also_exists_in` field
- [ ] XOR probes other domains after primary search (internal, fast)
- [ ] Agent learns about related domains without extra tool calls
- [ ] Suggestions field with actionable hints

```go
type SearchResponse struct {
    Results      []SearchResult `json:"results"`
    AlsoExistsIn []string       `json:"also_exists_in,omitempty"`
    Suggestions  []string       `json:"suggestions,omitempty"`
}
```

#### Smart Search with Budget Control
- [ ] `smart_search` skill with token budget parameter
- [ ] Internal progressive retrieval within budget
- [ ] Thoroughness levels: quick, moderate, thorough
- [ ] Returns `tokens_used` and `budget` in response
- [ ] Agent doesn't manage retrieval depth - skill handles it

#### Pending Set for Zero Staleness
- [ ] Bloom filter for pending additions (fast, mutable)
- [ ] Updated atomically with VectorGraphDB writes
- [ ] Cleared during XOR rebuild
- [ ] `Contains()` checks both XOR and pending

```go
func (m *XORFilterManager) Contains(domain string, key uint64) bool {
    // Check immutable XOR filter (stable content)
    if m.filters[domain].Contains(key) {
        return true
    }
    // Check pending bloom filter (recent additions)
    if m.pending[domain].Test(key) {
        return true
    }
    return false
}
```

#### Topic Key Extraction
- [ ] Extract topic keys from node content during indexing
- [ ] Consistent hashing for query terms
- [ ] Handle multi-word topics (n-grams or word-level)
- [ ] Normalize: lowercase, stemming optional

**Tests:**
- [ ] XOR filter contains returns correct results
- [ ] Pending set catches recent additions
- [ ] Rebuild correctly merges pending into XOR
- [ ] Early exit returns minimal tokens
- [ ] Multi-domain search reduces token usage
- [ ] Inline hints probe other domains correctly
- [ ] Budget control stays within limits
- [ ] No false negatives (pending + XOR covers all)

---

### 6.20 Architect Planning Preflight

Special planning-oriented preflight for Architect agent to scope work before detailed planning.

**Files to create:**
- `agents/architect/planning_preflight.go`
- `agents/architect/planning_preflight_test.go`

**Acceptance Criteria:**

#### Planning Preflight Skill
- [ ] `plan_preflight` skill for Architect only
- [ ] Checks all 3 domains in one call
- [ ] Returns existence flags per domain
- [ ] Includes planning hints based on what exists
- [ ] Uses XOR filters internally (fast)

```go
// plan_preflight response
type PlanningPreflight struct {
    Topic         string                  `json:"topic"`
    Domains       map[string]DomainCheck  `json:"domains"`
    PlanningHints []string                `json:"planning_hints"`
}

type DomainCheck struct {
    Exists bool   `json:"exists"`
    Hint   string `json:"hint"`
}
```

#### Planning Hints Generation
- [ ] "Greenfield work" when code domain is empty
- [ ] "Historical context available" when history has matches
- [ ] "Best practices exist" when academic has matches
- [ ] "Suggested agent order" based on what exists

**Example response:**
```json
{
  "topic": "retry logic",
  "domains": {
    "code": {"exists": false, "hint": "No existing implementation"},
    "history": {"exists": true, "hint": "Past solutions available"},
    "academic": {"exists": true, "hint": "Best practices available"}
  },
  "planning_hints": [
    "This is greenfield work (no existing code)",
    "Historical context available - learn from past",
    "Best practices exist - follow standards",
    "Suggested: Archivalist → Academic → Engineer"
  ]
}
```

**Tests:**
- [ ] Preflight returns correct domain flags
- [ ] Planning hints are actionable
- [ ] Fast response (<50ms)

---

## Phase 5: Integration

**Goal**: Wire everything together, integration tests.

**Dependencies**: Phases 0-4 complete

### 5.1 Agent Coordinator

Coordinates agent lifecycle and inter-agent communication.

**Files to create:**
- `core/coordinator/coordinator.go`
- `core/coordinator/lifecycle.go`
- `core/coordinator/health.go`
- `core/coordinator/coordinator_test.go`

**Acceptance Criteria:**
- [ ] Start all agents in correct order
- [ ] Register all agents with Guide
- [ ] Handle agent failures with graceful degradation
- [ ] Shutdown all agents in correct order
- [ ] Health monitoring across all agents
- [ ] Metrics collection from all agents
- [ ] Session-scoped agent instances
- [ ] Shared agent instances (Librarian, Academic, Archivalist)

### 5.2 Full Workflow Integration Tests

**Files to create:**
- `tests/integration/workflow_test.go`
- `tests/integration/multi_session_test.go`
- `tests/integration/session_isolation_test.go`
- `tests/integration/session_switching_test.go`
- `tests/integration/failure_test.go`
- `tests/integration/quality_loop_test.go`
- `tests/integration/skill_loading_test.go`
- `tests/integration/pipeline_test.go` (NEW)
- `tests/integration/two_level_qa_test.go` (NEW)

**Acceptance Criteria:**

#### Basic Workflow
- [ ] Test: User request → Architect → DAG → Pipelines → Complete
- [ ] Test: Multiple sessions executing concurrently
- [ ] Test: Session isolation (no context pollution)
- [ ] Test: Session switching with state preservation
- [ ] Test: Engineer clarification loop
- [ ] Test: User interruption during execution
- [ ] Test: Agent failure recovery
- [ ] Test: Session cleanup on completion
- [ ] Test: Skill progressive loading
- [ ] Test: Cross-session Archivalist queries

#### Pipeline-Specific Tests (`pipeline_test.go`)
- [ ] Test: Pipeline creation with Engineer + Inspector + Tester
- [ ] Test: Pipeline-internal Inspector feedback loop
- [ ] Test: Pipeline-internal Tester feedback loop
- [ ] Test: Pipeline max loops enforcement
- [ ] Test: /task command routing through Guide to Pipeline
- [ ] Test: User override (ignore_inspector) in Pipeline
- [ ] Test: User override (ignore_tester) in Pipeline
- [ ] Test: Concurrent Pipelines within same DAG
- [ ] Test: Pipeline cancellation mid-execution
- [ ] Test: Pipeline failure propagation to Orchestrator

#### Two-Level QA Tests (`two_level_qa_test.go`)
- [ ] Test: Pipeline-internal Inspector pass → Pipeline-internal Tester pass → Pipeline complete
- [ ] Test: Pipeline-internal Inspector fail → direct feedback → Engineer fixes → re-validate
- [ ] Test: Pipeline-internal Tester fail → direct feedback → Engineer fixes → re-test
- [ ] Test: All Pipelines complete → Session-wide Inspector triggers
- [ ] Test: Session-wide Inspector fail → Architect creates FIX DAG → new Pipelines
- [ ] Test: Session-wide Tester fail → Architect creates FIX DAG → new Pipelines
- [ ] Test: Full flow: Pipelines → Session Inspector → Session Tester → Complete

### 5.3 Performance Benchmarks

**Files to create:**
- `tests/benchmark/session_bench_test.go`
- `tests/benchmark/dag_bench_test.go`
- `tests/benchmark/throughput_bench_test.go`
- `tests/benchmark/skill_loading_bench_test.go`

**Acceptance Criteria:**
- [ ] Benchmark: 10 concurrent sessions with 50 Engineers each
- [ ] Benchmark: DAG execution with 100 nodes
- [ ] Benchmark: Message throughput through Guide
- [ ] Benchmark: Worker pool fairness under load
- [ ] Benchmark: Skill loading latency
- [ ] Benchmark: Session switch latency
- [ ] Target: < 10ms routing latency (cache hit)
- [ ] Target: < 100ms routing latency (LLM classification)
- [ ] Target: < 50ms skill loading latency
- [ ] Target: < 100ms session switch latency
- [ ] Target: Linear scaling to 100 sessions

---

## Token Savings Targets

### CRITICAL: How VectorGraphDB Saves Tokens

**VectorGraphDB does NOT replace LLM calls.** The agent (LLM) always runs on cache misses and **decides** when to invoke VectorGraphDB skills. Savings come from **context reduction**, not from avoiding LLM reasoning.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    CORRECT ARCHITECTURE                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   L1: INTENT CACHE                    L2: AGENT (LLM)                       │
│   ─────────────────                   ───────────────                       │
│   • 0 tokens on hit                   • Agent ALWAYS runs on cache miss    │
│   • ~68% of queries                   • Agent DECIDES to call VDB skills   │
│                                       • Agent processes curated results    │
│                                       • ~32% of queries                    │
│                                                                             │
│   Example flow:                                                             │
│   1. Cache miss → Agent runs (~300 tokens for reasoning)                   │
│   2. Agent invokes: search_code("auth handler", limit=10)                  │
│   3. VectorGraphDB returns curated results (~800 tokens vs ~2000 raw)      │
│   4. Agent synthesizes response (~700 tokens)                              │
│   5. Total: ~1,800 tokens (vs ~3,000 baseline)                             │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Token Savings Breakdown

| Source | Savings | How It Works |
|--------|---------|--------------|
| **Intent Cache** | ~67% | Similar queries hit cache (0 tokens) |
| **Context Reduction** | ~24% additional | VDB returns curated ~800 tokens vs raw ~2,000 tokens |
| **Combined** | **~75%** | Total savings vs baseline |

### Per-Query Token Estimates

| Component | Without VDB | With VDB | Savings |
|-----------|-------------|----------|---------|
| Query understanding | 300 tokens | 300 tokens | 0% |
| Context from search | 2,000 tokens | 800 tokens | **60%** |
| Response generation | 700 tokens | 700 tokens | 0% |
| **Total per query** | 3,000 tokens | 1,800 tokens | **40%** |

### Agent Token Estimates (per 100 queries)

| Agent | Baseline | Cache Only | +VectorGraphDB | +XOR Internal | Total Savings |
|-------|----------|------------|----------------|---------------|---------------|
| **Librarian** | 300,000 | 90,000 | 54,000 | 48,000 | **84%** |
| **Archivalist** | 250,000 | 62,500 | 37,500 | 33,000 | **87%** |
| **Academic** | 400,000 | 160,000 | 96,000 | 82,000 | **80%** |
| **Cross-Domain** | N/A | N/A | 50,000 | 35,000 | **~65% vs non-XOR** |
| **TOTAL** | 950,000 | 312,500 | 237,500 | 198,000 | **~80%** |

**XOR Internal Optimization Savings:**
- Empty search early exit: ~770 tokens saved per empty search
- Multi-domain `search_all`: ~1,500 tokens saved per cross-domain query
- Inline domain hints: ~100 tokens saved per query (avoids extra probe calls)
- Architect planning preflight: ~750 tokens saved per planning decision

### Monthly Cost Targets

| Scenario | Monthly Cost | vs Baseline |
|----------|-------------|-------------|
| Baseline (no cache, no VDB) | $285/month | - |
| Cache only | $94/month | 67% savings |
| Cache + VectorGraphDB | $72/month | 75% savings |
| **Cache + VectorGraphDB + XOR** | **$60/month** | **~80% savings** |

**Overall Target**: 80% token reduction across all agents (with XOR optimization).

### Implementation Guidelines

**DO:**
- Design skills that return curated, scored results
- Return ~5-10 relevant results, not 50
- Include only necessary metadata in results
- Let the agent decide retrieval depth (progressive retrieval)
- Cache skill results where appropriate
- Use XOR filters INTERNALLY in search skills for early exit
- Return "no_matches" quickly when XOR says content doesn't exist
- Include `also_exists_in` hints in search responses
- Use `search_all` for multi-domain queries (1 call vs 3)

**DON'T:**
- Assume VectorGraphDB calls are "free" (agent still runs)
- Return raw file contents in skill results
- Return all matching results (use limits)
- Bypass the agent with "graph-only" resolution
- Expose XOR filters as separate LLM tools (adds tool call overhead)
- Make agents call existence checks before searches (baked into search internally)

---

## Latency Targets

| Operation | Target | P95 |
|-----------|--------|-----|
| Vector insert | < 1ms | < 5ms |
| Vector search (k=10) | < 5ms | < 20ms |
| Graph traversal (depth=3) | < 10ms | < 30ms |
| Hybrid query | < 50ms | < 100ms |
| Full resolution (cache miss) | < 100ms | < 200ms |
| Full resolution (cache hit) | < 10ms | < 25ms |

---

## Memory Targets

| Scale | RAM Usage | SQLite Size |
|-------|-----------|-------------|
| 10K nodes | ~50MB | ~100MB |
| 50K nodes | ~200MB | ~500MB |
| 100K nodes | ~400MB | ~1GB |

---

## Testing Strategy

### Unit Tests
Each component has `*_test.go` with:
- Constructor tests
- Method tests
- Error handling tests
- Concurrency tests (race detector)
- Skill loading tests

### Integration Tests
```
tests/integration/
├── workflow_test.go          # Full workflow end-to-end
├── multi_session_test.go     # Concurrent sessions
├── session_isolation_test.go # No context pollution
├── session_switching_test.go # State preservation
├── failure_test.go           # Failure recovery
├── quality_loop_test.go      # Inspector + Tester loop
└── skill_loading_test.go     # Progressive disclosure
```

### Running Tests
```bash
go test ./... -race
go test ./tests/integration/... -v -timeout 10m
go test ./tests/benchmark/... -bench=. -benchmem
```

---

## Acceptance Verification

Before marking a phase complete:

1. **Build passes**: `go build ./...`
2. **Vet passes**: `go vet ./...`
3. **Tests pass**: `go test ./... -race`
4. **Coverage > 70%**: `go test ./... -cover`
5. **No race conditions**: Tests pass with `-race` flag
6. **Integration works**: Component integrates with existing system
7. **Skills defined**: All skills documented and registered
8. **Hooks implemented**: All lifecycle hooks in place
9. **Session-aware**: All state operations include session context

---

## Implementation Order

1. **Week 1-2**: 6.1 (Schema), 6.2 (HNSW) - can parallelize
2. **Week 3**: 6.3 (Nodes/Edges), 6.4 (Search/Traversal) - can parallelize
3. **Week 4-5**: 6.5-6.11 (All Mitigations) - can parallelize
4. **Week 6**: 6.12 (Unified Resolver)
5. **Week 7-8**: 6.13-6.16 (Agent Integrations) - can parallelize
6. **Week 9**: 6.19 (XOR Filter Optimization) - integrates with 6.13-6.16 search skills
7. **Week 10**: 6.20 (Architect Planning Preflight)
8. **Week 11-12**: 6.21-6.24 (Scratchpad, Style Inference, Component Registry, Mistake Memory) - can parallelize
9. **Week 13-14**: 6.25-6.28 (Diff Preview, Preferences, File Snapshot, Dependency Awareness) - can parallelize
10. **Week 15**: 6.29-6.30 (Task Continuity, Design Tokens)
11. **Week 16**: 6.31 (Designer Agent Implementation)
12. **Week 17-18**: 6.43-6.45 (Pipeline Core, Inspector Modes, Tester Modes)
13. **Week 19**: 6.46-6.48 (Architect Decomposition, Failure Routing, Task Commands)
14. **Week 20**: 6.32-6.42 (Skill/Agent Integrations) - can parallelize
15. **Week 21**: 6.49, 6.17-6.18 (Pipeline Integration Tests, Benchmarks) - validates all

**Note**: Weeks are relative units of work, not calendar estimates.

**XOR Filter Integration Points:**
- 6.19 must be integrated with search skills from 6.13-6.16
- All search skills gain internal XOR early exit
- `search_all` multi-domain skill added
- Inline domain hints added to responses

**Agent Efficiency Integration Points:**
- 6.31 (Designer) can be parallelized with 6.21-6.30
- 6.33-6.42 depend on 6.21-6.30 (skills must exist before integration)
- See section 6.32 for full agent-to-technique mapping

**Pipeline System Integration Points:**
- 6.43 (Pipeline Core) is foundation for all pipeline work
- 6.44-6.45 (Inspector/Tester Modes) can parallelize
- 6.46-6.48 depend on 6.43
- 6.49 validates entire pipeline system

---

## Updated Implementation Order

1. **Week 1-2**: 6.1 (Schema), 6.2 (HNSW) - can parallelize
2. **Week 3**: 6.3 (Nodes/Edges), 6.4 (Search/Traversal) - can parallelize
3. **Week 4-5**: 6.5-6.11 (All Mitigations) - can parallelize
4. **Week 6**: 6.12 (Unified Resolver)
5. **Week 7-8**: 6.13-6.16 (Agent Integrations) - can parallelize
6. **Week 9**: 6.19 (XOR Filter Optimization)
7. **Week 10**: 6.20 (Architect Planning Preflight)
8. **Week 11-12**: 6.21-6.24 (Scratchpad, Style Inference, Component Registry, Mistake Memory)
9. **Week 13-14**: 6.25-6.28 (Diff Preview, Preferences, File Snapshot, Dependency Awareness)
10. **Week 15**: 6.29-6.30 (Task Continuity, Design Tokens)
11. **Week 16**: 6.31 (Designer Agent Implementation)
12. **Week 17-18**: 6.32-6.42 (Skill/Agent Integrations) - can parallelize
13. **Week 19**: 6.17-6.18 (Benchmarks, Integration Tests) - validates all optimizations

**Note**: Weeks are relative units of work, not calendar estimates.

**Integration Dependencies:**
- 6.33-6.42 depend on 6.21-6.30 (skills must exist before integration)
- 6.31 (Designer) can be parallelized with 6.21-6.30
- 6.32 (Matrix) is a reference, not implementation
- 6.42 (Design Tokens) depends on 6.31 (Designer agent)

---

## Updated Implementation Order (with Pipelines)

1. **Week 1-2**: 6.1 (Schema), 6.2 (HNSW) - can parallelize
2. **Week 3**: 6.3 (Nodes/Edges), 6.4 (Search/Traversal) - can parallelize
3. **Week 4-5**: 6.5-6.11 (All Mitigations) - can parallelize
4. **Week 6**: 6.12 (Unified Resolver)
5. **Week 7-8**: 6.13-6.16 (Agent Integrations) - can parallelize
6. **Week 9**: 6.19 (XOR Filter Optimization)
7. **Week 10**: 6.20 (Architect Planning Preflight)
8. **Week 11-12**: 6.21-6.24 (Scratchpad, Style Inference, Component Registry, Mistake Memory)
9. **Week 13-14**: 6.25-6.28 (Diff Preview, Preferences, File Snapshot, Dependency Awareness)
10. **Week 15**: 6.29-6.30 (Task Continuity, Design Tokens)
11. **Week 16**: 6.31 (Designer Agent Implementation)
12. **Week 17-18**: 6.43-6.45 (Pipeline Core, Inspector Modes, Tester Modes)
13. **Week 19**: 6.46-6.48 (Architect Decomposition, Failure Routing, Task Commands)
14. **Week 20**: 6.32-6.42 (Skill/Agent Integrations) - can parallelize
15. **Week 21**: 6.49, 6.17-6.18 (Pipeline Integration Tests, Benchmarks) - validates all

**Pipeline Dependencies:**
- 6.43 (Pipeline Core) must be complete before 6.44-6.45
- 6.44-6.45 (Inspector/Tester Modes) can parallelize
- 6.46 (Architect Decomposition) depends on 6.43
- 6.47 (Failure Routing) depends on 6.43
- 6.48 (Task Commands) depends on 6.43
- 6.49 (Integration Tests) depends on all pipeline sections

---

## Comprehensive Parallel Execution Order (Agent-Based)

This section defines the optimal parallel execution order for ALL work areas, designed for agent-based implementation where agents can spawn subagents for parallel work.

### Execution Model

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                        AGENT-BASED PARALLEL EXECUTION MODEL                          │
├─────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                     │
│  ARCHITECT (Primary Coordinator)                                                    │
│       │                                                                             │
│       ├──► ORCHESTRATOR manages phases/waves                                        │
│       │         │                                                                   │
│       │         ├──► ENGINEER POOL (N_CPU_CORES concurrent)                         │
│       │         │         │                                                         │
│       │         │         ├──► Engineer-1: Phase 0 Core Infrastructure              │
│       │         │         ├──► Engineer-2: Phase 0 Core Infrastructure              │
│       │         │         ├──► Engineer-3: Phase 0 Core Infrastructure              │
│       │         │         └──► ... (parallelized within phase)                      │
│       │         │                                                                   │
│       │         └──► INSPECTOR + TESTER validate each completed section             │
│       │                                                                             │
│       └──► LIBRARIAN tracks dependencies and completion state                       │
│                                                                                     │
│  Principle: Maximize parallelization, respect dependencies, validate continuously   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Wave-Based Execution

**WAVE 0: Foundation (No Dependencies)**
All items in this wave have zero dependencies and can execute in full parallel.

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 0: FOUNDATION                                                                  │
│ ══════════════════                                                                  │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 0A: Core Types & Interfaces                                       ││
│ │ • 0.7 Provider Interface (DONE)                                                  ││
│ │ • 0.10a.1 StreamChunk Types (DONE)                                               ││
│ │ • 0.26 Error Type System (DONE)                                                  ││
│ │ • 1.1 Academic Types (DONE)                                                      ││
│ │ • 2.1 Engineer Types (DONE)                                                      ││
│ │ • 4.1 Inspector Types (DONE)                                                     ││
│ │ • 6.1 VectorGraphDB Schema (DONE)                                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 0B: Detection & Utility Libraries                                 ││
│ │ • core/detect/which.go (binary detection) (DONE)                                 ││
│ │ • core/detect/files.go (file detection) (DONE)                                   ││
│ │ • core/detect/dependencies.go (package detection) (DONE)                         ││
│ │ • core/format/types.go (formatter types) (DONE)                                  ││
│ │ • core/lsp/types.go (LSP types) (DONE)                                           ││
│ │ • core/test/types.go (test framework types) (DONE)                               ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 0C: Provider Adapters                                             ││
│ │ • 0.7 Anthropic Adapter (DONE)                                                   ││
│ │ • 0.7 OpenAI Adapter (DONE)                                                      ││
│ │ • 0.7 Google Adapter (DONE)                                                      ││
│ │ • 0.8 Provider Config (DONE)                                                     ││
│ │ • 0.9 Priority Queue (DONE)                                                      ││
│ │ • 0.10 Rate Limiter (DONE)                                                       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 15-20 parallel engineer pipelines                              │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 1: Core Infrastructure (Depends on Wave 0)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 1: CORE INFRASTRUCTURE                                                         │
│ ═══════════════════════════                                                         │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 1A: Messaging & Events (DONE)                                     ││
│ │ • 0.6 Bus Enhancements (DONE)                                                    ││
│ │ • 0.11 Signal Bus & Workflow Control (DONE)                                      ││
│ │ • 0.10a.2 Event Bus Message Wrapping (DONE)                                      ││
│ │ • 0.10a.3 Stream Bridge Service (DONE)                                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 1B: Session & State Management (DONE)                             ││
│ │ • 0.1 Session Manager (DONE)                                                     ││
│ │ • 0.12 Agent Signal Handler & Checkpointing (DONE)                               ││
│ │ • 0.22 Write-Ahead Log (DONE)                                                    ││
│ │ • 0.23 Checkpointer (DONE)                                                       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 1C: Resource Management (DONE)                                    ││
│ │ • 0.13 Token Budget Manager (DONE)                                               ││
│ │ • 0.14 Context Window Manager (DONE)                                             ││
│ │ • 0.33 Memory Monitor (DONE)                                                     ││
│ │ • 0.34 Resource Pools (DONE)                                                     ││
│ │ • 0.35 Disk Quota Manager (DONE)                                                 ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 1D: VectorGraphDB Core (DONE)                                     ││
│ │ • 6.2 HNSW Vector Index (DONE)                                                   ││
│ │ • 6.3 Nodes & Edges (DONE)                                                       ││
│ │ • 6.4 Search & Traversal (DONE)                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 12-16 parallel engineer pipelines                              │
│ DEPENDENCIES: All items from Wave 0                                                 │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 2: Execution Framework (Depends on Wave 1)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 2: EXECUTION FRAMEWORK                                                         │
│ ═══════════════════════════                                                         │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 2A: DAG & Pipeline Execution (DONE)                               ││
│ │ • 0.2 DAG Engine (DONE)                                                          ││
│ │ • 0.3 Worker Pool Enhancements (DONE)                                            ││
│ │ • 0.17 Goroutine Model & Agent Lifecycle (DONE)                                  ││
│ │ • 0.18 Pipeline Scheduler (DONE)                                                 ││
│ │ • 0.21 Adaptive Channels (DONE)                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 2B: LLM Integration (DONE)                                         ││
│ │ • 0.15 LLM Request Gate (Integration) (DONE)                                     ││
│ │ • 0.19 Dual Queue LLM Gate (DONE)                                                ││
│ │ • 0.10a.4 Stream Metrics Collection (DONE)                                       ││
│ │ • 0.10a.5 Stream Timeout Watchdog (DONE)                                         ││
│ │ • 0.7 Token Counter (DONE)                                                       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 2C: Error Handling & Recovery (DONE)                              ││
│ │ • 0.24 Recovery Manager (DONE)                                                   ││
│ │ • 0.27 Transient Error Tracker (DONE)                                            ││
│ │ • 0.28 Retry Policies (DONE)                                                     ││
│ │ • 0.29 Circuit Breaker (DONE)                                                    ││
│ │ • 0.30 Escalation Chain (DONE)                                                   ││
│ │ • 0.31 Retry Briefing System (DONE)                                              ││
│ │ • 0.32 Rollback Manager (DONE)                                                   ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 2D: VectorGraphDB Mitigations (DONE)                              ││
│ │ • 6.5-6.11 All Mitigations (7 items, can parallelize) (DONE)                     ││
│ │ • 6.12 Unified Resolver (depends on 6.5-6.11) (DONE)                             ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 18-22 parallel engineer pipelines                              │
│ DEPENDENCIES: Wave 1 complete                                                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 3: Tool Execution & File Management (Depends on Wave 2)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 3: TOOL EXECUTION & FILE MANAGEMENT                                            │
│ ════════════════════════════════════════                                            │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 3A: Tool System (DONE)                                            ││
│ │ • 0.39 Session Registry (DONE)                                                   ││
│ │ • 0.40 Fair Share Calculator (DONE)                                              ││
│ │ • 0.41 Signal Dispatcher (DONE)                                                  ││
│ │ • 0.42 Cross-Session Resource Pool (DONE)                                        ││
│ │ • 0.43 Tool Executor (DONE)                                                      ││
│ │ • 0.44 Adaptive Timeout (DONE)                                                   ││
│ │ • 0.45 Kill Sequence Manager (DONE)                                              ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 3B: Tool Output & Parsing (DONE)                                  ││
│ │ • 0.46 Output Handler (DONE)                                                     ││
│ │ • 0.47 Tool Output Parsers (DONE)                                                ││
│ │ • 0.48 Parse Template Cache (DONE)                                               ││
│ │ • 0.50 Tool Cancellation Manager (DONE)                                          ││
│ │ • 0.51 Tool Output Cache (DONE)                                                  ││
│ │ • 0.52 Tool Invocation Batcher (DONE)                                            ││
│ │ • 0.53 Streaming Output Parser (DONE)                                            ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 3C: Filesystem & Storage (DONE)                                   ││
│ │ • 0.20 Staging Manager (DONE)                                                    ││
│ │ • 0.36 Resource Broker (DONE)                                                    ││
│ │ • 0.37 Graceful Degradation Controller (DONE)                                    ││
│ │ • 0.49 Filesystem Manager (DONE)                                                 ││
│ │ • 0.61 Directory Manager (DONE)                                                  ││
│ │ • 0.62 Configuration Manager (DONE)                                              ││
│ │ • 0.63 Credential Manager (DONE)                                                 ││
│ │ • 0.64 Database Manager (DONE)                                                   ││
│ │ • 0.65 Backup Manager (DONE)                                                     ││
│ │ • 0.66 Integrity Monitor (DONE)                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 3D: FILESYSTEM Versioning Foundation (FS Phases 1-2) (DONE)       ││
│ │ ** NEW: MVCC + OT + AST-Aware VFS Foundation **                                  ││
│ │                                                                                  ││
│ │ PHASE 1 (All parallel - no interdependencies): (DONE)                            ││
│ │ • FS.1.1 VersionID and Content-Addressable Hashing (DONE)                        ││
│ │ • FS.1.2 Vector Clock for Causality Tracking (DONE)                              ││
│ │ • FS.1.3 OperationID and Operation Types (DONE)                                  ││
│ │ • FS.1.4 AST-Aware Target (DONE)                                                 ││
│ │ • FS.1.5 FileVersion (DONE)                                                      ││
│ │                                                                                  ││
│ │ PHASE 2 (After Phase 1, items parallel): (DONE)                                  ││
│ │ • FS.2.1 Content-Addressable Blob Store (DONE)                                   ││
│ │ • FS.2.2 Operation Log (DONE)                                                    ││
│ │ • FS.2.3 Version DAG Store (DONE)                                                ││
│ │ • FS.2.4 Write-Ahead Log (for versioning) (DONE)                                 ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/versioning/version_id.go, vector_clock.go, operation.go,                  ││
│ │   target.go, file_version.go, blob_store.go, operation_log.go,                   ││
│ │   dag_store.go, wal.go                                                           ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   Phase 1 (all parallel) → Phase 2 (all parallel)                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 3E: Tree-Sitter Parsing Infrastructure (TS Phases 1-2) (DONE)    ││
│ │ ** NEW: Purego-based tree-sitter for universal AST parsing **                   ││
│ │                                                                                  ││
│ │ TS PHASE 1 (All parallel - foundation): (DONE)                                  ││
│ │ • TS.1.1 Purego Bindings (libtree-sitter dynamic loading) (DONE)                ││
│ │ • TS.1.2 High-Level Go Types (Parser, Tree, Node, Query) (DONE)                 ││
│ │ • TS.1.3 Grammar Registry (30+ languages, extensions map) (DONE)                ││
│ │                                                                                  ││
│ │ TS PHASE 2 (After Phase 1, all parallel): (DONE)                                ││
│ │ • TS.2.1 Parser Implementation (incremental parsing) (DONE)                     ││
│ │ • TS.2.2 Tree/Node Implementation (stable paths) (DONE)                         ││
│ │ • TS.2.3 Query Engine (S-expression patterns) (DONE)                            ││
│ │ • TS.2.4 Grammar Downloader (prebuilt + compile fallback) (DONE)                ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/treesitter/bindings.go, types.go, parser.go, tree.go,                    ││
│ │   query.go, registry.go, downloader.go                                          ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   TS Phase 1 (all parallel) → TS Phase 2 (all parallel)                         ││
│ │   Provides: AST foundation for FS.1.4 (AST-Aware Target)                        ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 34-42 parallel engineer pipelines (increased for FS + TS)      │
│ DEPENDENCIES: Wave 2 complete                                                       │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 4: Security & Multi-Session (Depends on Wave 3)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 4: SECURITY & MULTI-SESSION                                                    │
│ ════════════════════════════════                                                    │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 4A: Security Model ✅ COMPLETED                                   ││
│ │ • 0.68 Permission Manager ✅                                                     ││
│ │ • 0.69 Sandbox Manager ✅                                                        ││
│ │ • 0.70 Audit Logger ✅                                                           ││
│ │ • 0.71 Audit Query Interface ✅                                                  ││
│ │ • 0.72 Session Credential Manager ✅                                             ││
│ │ • Secret Management System (6.113-6.121) ✅                                      ││
│ │ • Credential Broker System (6.122-6.129) ✅                                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 4B: Multi-Session Coordination ✅ COMPLETED                       ││
│ │ • 0.55 Global Subscription Tracker ✅                                            ││
│ │ • 0.56 Cross-Session Dual Queue Gate ✅                                          ││
│ │ • 0.57 Global Pipeline Scheduler ✅                                              ││
│ │ • 0.58 Multi-Session WAL Manager ✅                                              ││
│ │ • 0.59 Global Circuit Breaker Registry ✅                                        ││
│ │ • 0.73 Session Knowledge Manager ✅                                              ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 4C: FILESYSTEM Versioning Core (FS Phases 3-5) ✅ COMPLETE        ││
│ │ ** NEW: VFS, OT Engine, Central Version Store **                                 ││
│ │                                                                                  ││
│ │ PHASE 3 - VFS (After Wave 3D Phase 2):                                           ││
│ │ • FS.3.1 VFS (Virtual File System) with security integration ✅                  ││
│ │ • FS.3.2 Pipeline VFS Manager (variant group support) ✅                         ││
│ │                                                                                  ││
│ │ PHASE 4 - OT Engine (Can parallel with Phase 3):                                 ││
│ │ • FS.4.1 Diff Algorithm (Myers + AST-aware) ✅                                   ││
│ │ • FS.4.2 Operational Transformation Engine ✅                                    ││
│ │ • FS.4.3 Conflict Resolution UI Integration (Guide routing) ✅                   ││
│ │                                                                                  ││
│ │ PHASE 5 - CVS (After Phases 3-4):                                                ││
│ │ • FS.5.1 Central Version Store (main coordinator) ✅                             ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/versioning/vfs.go, vfs_manager.go, diff.go, ot.go,                        ││
│ │   conflict_ui.go, cvs.go                                                         ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   Phase 3 & 4 (parallel) → Phase 5 (CVS coordinates all)                         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 4D: FILESYSTEM Cross-Session Coordination (FS Phase 7) ✅ COMPLETE││
│ │ ** NEW: File change signals and lock coordination **                             ││
│ │                                                                                  ││
│ │ PHASE 7 (After Phase 5 CVS):                                                     ││
│ │ • FS.7.1 Signal Dispatcher for File Changes ✅                                   ││
│ │   - FILE_CREATED, FILE_MODIFIED, FILE_DELETED signals                            ││
│ │   - FILE_LOCKED, FILE_UNLOCKED, MERGE_CONFLICT signals                           ││
│ │   - Pattern-based subscriptions                                                  ││
│ │ • FS.7.2 Cross-Session Pool for File Coordination ✅                             ││
│ │   - Read/write lock acquisition                                                  ││
│ │   - Coordinated multi-file operations                                            ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/session/signal_dispatcher_vfs.go, cross_session_pool_vfs.go               ││
│ │                                                                                  ││
│ │ EXTENDS:                                                                         ││
│ │   - Existing SignalDispatcher (0.41)                                             ││
│ │   - Existing CrossSessionPool (0.42)                                             ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 4E: Tree-Sitter CVS Integration (TS Phases 3-4) ✅ COMPLETE      ││
│ │ ** NEW: CVS integration and agent tool interface **                              ││
│ │                                                                                  ││
│ │ TS PHASE 3 (After Wave 3E, depends on FS.5.1 CVS):                              ││
│ │ • TS.3.1 TreeSitterManager (CVS integration) ✅                                 ││
│ │   - ComputeNodePath, ResolveNodePath                                            ││
│ │   - ParseIncremental for FileVersion                                            ││
│ │   - Integration with Target struct from FS.1.4                                  ││
│ │                                                                                  ││
│ │ TS PHASE 4 (After Phase 3, all parallel):                                       ││
│ │ • TS.4.1 TreeSitterTool (base agent interface) ✅                               ││
│ │ • TS.4.2 Librarian Skills (ts_parse, ts_query, ts_find_*) ✅                    ││
│ │ • TS.4.3 Engineer Skills (ts_rename, ts_extract, ts_find_targets) ✅            ││
│ │ • TS.4.4 Inspector Skills (ts_complexity, ts_smells, ts_validate) ✅            ││
│ │ • TS.4.5 Tester Skills (ts_discover_tests, ts_find_testable) ✅                 ││
│ │ • TS.4.6 Designer Skills (ts_components, ts_jsx, ts_styles) ✅                  ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/treesitter/manager.go, tool.go, skills/librarian.go,                     ││
│ │   skills/engineer.go, skills/inspector.go, skills/tester.go,                    ││
│ │   skills/designer.go                                                            ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   TS.3.1 → TS.4.* (all parallel)                                                ││
│ │   Depends on: Wave 3E (TS Phases 1-2), FS.5.1 (CVS)                             ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 26-32 parallel engineer pipelines (increased for FS + TS)      │
│ DEPENDENCIES: Wave 3 complete (including 3D FILESYSTEM and 3E Tree-Sitter)         │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 5: Knowledge Agents (Depends on Wave 1, partially Wave 2)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 5: KNOWLEDGE AGENTS                                                            │
│ ════════════════════════                                                            │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5A: Knowledge Agent Core                                          ││
│ │ • 0.5 Archivalist Enhancements + System Prompt                                   ││
│ │ • 1.1 Librarian Agent Implementation + System Prompt                             ││
│ │ • 1.2 Academic Agent Implementation + System Prompt                              ││
│ │ • Librarian Tool Discovery Skills                                                ││
│ │                                                                                  ││
│ │ LIBRARIAN SYSTEM PROMPT HIGHLIGHTS (1.1):                                        ││
│ │   - Model: Claude Sonnet 4.5 1 Million token (fast code search)                  ││
│ │   - SINGLE SOURCE OF TRUTH: formatters, linters, test frameworks, patterns       ││
│ │   - Health Assessment: DISCIPLINED/TRANSITIONAL/LEGACY/GREENFIELD maturity       ││
│ │   - Query Classification: LOCATE, PATTERN, EXPLAIN, GENERAL                      ││
│ │   - Inspector/Tester MUST consult before executing tools                         ││
│ │   - ALWAYS include confidence scores for pattern detection                       ││
│ │                                                                                  ││
│ │ ACADEMIC SYSTEM PROMPT HIGHLIGHTS (1.2):                                         ││
│ │   - Model: Claude Opus 4.5 (complex reasoning and synthesis)                     ││
│ │   - Research Discipline: ALWAYS validate vs codebase reality via Librarian       ││
│ │   - Applicability: DIRECT/ADAPTABLE/INCOMPATIBLE classification required         ││
│ │   - Maturity-Aware: Recommendations adjust based on codebase maturity            ││
│ │   - Outcome Tracking: Query past success/failure before recommending             ││
│ │   - MANDATORY: Librarian consultation before finalizing recommendations          ││
│ │                                                                                  ││
│ │ ARCHIVALIST SYSTEM PROMPT HIGHLIGHTS (0.5):                                      ││
│ │   - Model: Claude Sonnet 4.5 1 Million token (pattern matching, history queries) ││
│ │   - Failure Memory: Track failures, warn on similar approaches (>= 2 recurrence) ││
│ │   - Cross-Session Learning: Failure in session A warns session B                 ││
│ │   - Retrieval Accuracy: Self-healing on STALE/IRRELEVANT/INCOMPLETE issues       ││
│ │   - Storage Verification: Read-after-write verification required                 ││
│ │   - NEVER suppress failure warnings to seem helpful                              ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5B: VectorGraphDB Agent Integration                               ││
│ │ • 6.13 Librarian Integration                                                     ││
│ │ • 6.14 Archivalist Integration                                                   ││
│ │ • 6.15 Academic Integration                                                      ││
│ │ • 6.16 Guide Integration                                                         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5C: Discipline Protocols                                          ││
│ │ • Librarian: Codebase Health Assessment                                          ││
│ │ • Librarian: Context Quality Feedback                                            ││
│ │ • Archivalist: Failure Pattern Memory                                            ││
│ │ • Archivalist: Retrieval Accuracy Tracking                                       ││
│ │ • Academic: Research Discipline Protocol                                         ││
│ │ • Academic: Recommendation Outcome Tracking                                      ││
│ │ • Guide: Intent Gate Classification                                              ││
│ │ • Guide: Routing Failure Tracking                                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5D: Guide Session Recording & Replay (NEW)                        ││
│ │ ** FROM ARCHITECTURE.md lines 17096-17279 **                                     ││
│ │                                                                                  ││
│ │ SKILLS TO IMPLEMENT:                                                             ││
│ │ • start_recording - Begin capturing all queries with metadata                    ││
│ │ • stop_recording - Save recording to Archivalist with tags                       ││
│ │ • replay_session - Replay recorded session (exact/interactive/dry_run modes)     ││
│ │ • replay_query - Replay single query from history (by ID or offset)              ││
│ │ • list_recordings - List available session recordings with filters               ││
│ │ • get_recording_info - Get details about specific recording                      ││
│ │                                                                                  ││
│ │ DATA MODEL (Session Recording):                                                  ││
│ │   - id, name, session_id, created_at, duration, query_count, tags                ││
│ │   - queries[]: raw_query, classified_intent, routed_to, response, success        ││
│ │                                                                                  ││
│ │ REPLAY MODES:                                                                    ││
│ │   - EXACT: Re-execute without pauses (automation/scripting)                      ││
│ │   - INTERACTIVE: Pause between queries (learning/teaching)                       ││
│ │   - DRY_RUN: Preview without executing (verification)                            ││
│ │                                                                                  ││
│ │ USE CASES:                                                                       ││
│ │   - Tutorial creation, context restoration, repeatable workflows                 ││
│ │   - Modified replay, session archaeology                                         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5E: Direct Consultation Skills (NEW)                              ││
│ │ ** FROM ARCHITECTURE.md lines 16912-16953 **                                     ││
│ │                                                                                  ││
│ │ UNIVERSAL PATTERN: consult_<target_agent> for all agents                         ││
│ │                                                                                  ││
│ │ TYPE DEFINITIONS:                                                                ││
│ │ • ConsultationSkill struct (Name, Description, Domain, Target, Keywords, Params) ││
│ │ • Standard consultation params (question, context, priority, max_tokens)         ││
│ │ • ConsultationRequest/ConsultationResponse types                                 ││
│ │                                                                                  ││
│ │ SKILLS BY AGENT (consult_* skills):                                              ││
│ │ • Guide: ALL agents (architect, engineer, designer, inspector, tester,           ││
│ │          librarian, archivalist, academic) - 8 skills                            ││
│ │ • Architect: engineer, designer, inspector, tester, librarian, archivalist,      ││
│ │              academic - 7 skills                                                 ││
│ │ • Engineer: architect, designer, inspector, tester, librarian, archivalist,      ││
│ │             academic - 7 skills                                                  ││
│ │ • Designer: architect, engineer, inspector, tester, librarian, archivalist,      ││
│ │             academic - 7 skills                                                  ││
│ │ • Inspector: architect, engineer, designer, tester, librarian, archivalist       ││
│ │              - 6 skills                                                          ││
│ │ • Tester: architect, engineer, designer, inspector, librarian, archivalist       ││
│ │           - 6 skills                                                             ││
│ │ • Librarian: archivalist, academic - 2 skills                                    ││
│ │ • Archivalist: librarian, academic - 2 skills                                    ││
│ │ • Academic: librarian, archivalist - 2 skills                                    ││
│ │                                                                                  ││
│ │ TOTAL: 47 consultation skills across all agents                                  ││
│ │ TOKEN SAVINGS: Bypasses Guide routing for known targets                          ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5F: Guide Intent Classification Enhancements (NEW)                ││
│ │ ** FROM ARCHITECTURE.md lines 2775-3005 **                                       ││
│ │                                                                                  ││
│ │ LIBRARIAN INTENT TYPES:                                                          ││
│ │ • LOCATE: Find where something is (file, function, struct)                       ││
│ │ • PATTERN: Ask about patterns, strategies, conventions                           ││
│ │ • EXPLAIN: Understand how something works                                        ││
│ │ • IMPLEMENT: Need example implementation to follow                               ││
│ │ • DIVERGENCE: Ask about pattern variations or inconsistencies                    ││
│ │ • GENERAL: Other codebase questions                                              ││
│ │                                                                                  ││
│ │ ARCHIVALIST INTENT TYPES:                                                        ││
│ │ • HISTORICAL: "What did we do before for X?", past solutions                     ││
│ │ • ACTIVITY: "What files changed?", "What happened in last task?"                 ││
│ │ • OUTCOME: "Did tests pass?", "What was the result?"                             ││
│ │ • SIMILAR: "Have we seen this error before?", similar past decisions             ││
│ │ • RESUME: "Where did we leave off?", current status                              ││
│ │ • GENERAL: Other history questions                                               ││
│ │                                                                                  ││
│ │ IMPLEMENTATION:                                                                  ││
│ │ • Guide routing prompt enhancements for Librarian                                ││
│ │ • Guide routing prompt enhancements for Archivalist                              ││
│ │ • Intent-aware cache key generation                                              ││
│ │ • Zero additional token cost (reuse existing classification)                     ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 5G: Archivalist Extended Skills (NEW)                             ││
│ │ ** FROM ARCHITECTURE.md lines 17478-17600 **                                     ││
│ │                                                                                  ││
│ │ TIER 3 (SPECIALIZED) SKILLS:                                                     ││
│ │ • fact_extraction - Extract facts from unstructured context                      ││
│ │ • conflict_resolution - Resolve contradictory stored information                 ││
│ │                                                                                  ││
│ │ SESSION RECORDING DATA MODEL:                                                    ││
│ │ • SessionRecording struct (ID, Name, SessionID, CreatedAt, Duration, Queries)    ││
│ │ • RecordedQuery struct (Index, Timestamp, RawQuery, Intent, RoutedTo, Response)  ││
│ │ • Archivalist storage integration for recordings                                 ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   Group 5A (Archivalist core) → Group 5G (extended skills)                       ││
│ │   Group 5D (Guide recording) → Group 5G (recording storage)                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ NOTE: Wave 5 can START after Wave 1 complete, runs in parallel with Waves 2-4      │
│ ESTIMATED CAPACITY: 14-18 parallel engineer pipelines (increased for new groups)   │
│ INTERNAL DEPENDENCIES:                                                             │
│   5A → 5B, 5C (parallel) → 5D, 5E, 5F (parallel) → 5G                              │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 6: Execution Agents (Depends on Wave 2-3)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 6: EXECUTION AGENTS                                                            │
│ ════════════════════════                                                            │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6A: Worker Agent Core                                             ││
│ │ • 2.1 Engineer Agent Implementation + System Prompt                              ││
│ │ • 2.2 Designer Agent Implementation                                              ││
│ │ • Engineer: Failure Recovery Protocol                                            ││
│ │ • Designer: Failure Recovery Protocol                                            ││
│ │                                                                                  ││
│ │ ENGINEER SYSTEM PROMPT HIGHLIGHTS (2.1):                                         ││
│ │   - Core Principles: clean, modular, testable, readable code                     ││
│ │   - CODE QUALITY: ROBUST, CORRECT, PERFORMANT, READABLE/MAINTAINABLE             ││
│ │   - Pre-implementation checks: memory leaks, race conditions, deadlocks,         ││
│ │     off-by-one bugs, missing error handling, code smell                          ││
│ │   - SCOPE LIMIT: If >12 todos required, STOP and request Architect decomposition ││
│ │   - DEFAULT consultations: Librarian (before), Academic (when unclear)           ││
│ │   - 10-step Implementation Protocol with knowledge agent consultation            ││
│ │                                                                                  ││
│ │ DESIGNER SYSTEM PROMPT HIGHLIGHTS (6.31):                                        ││
│ │   - Core Principles: clean, modular, testable, ACCESSIBLE interfaces             ││
│ │   - UI QUALITY: ACCESSIBLE, PERFORMANT, MAINTAINABLE, BEAUTIFUL                  ││
│ │   - Pre-implementation checklist: MANDATORY Librarian + Academic consultation    ││
│ │   - Smooth transitions, excellent legibility, user sight preferences             ││
│ │   - Be STEADFAST in adhering to existing patterns and standards                  ││
│ │   - MANDATORY: Librarian (standards/components), Academic (best practices)       ││
│ │   - 10-step Implementation Protocol with design token validation + a11y checks   ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6B: Orchestrator Agent System (6.130-6.139)                       ││
│ │ • 6.130 Update Buffer Types (Foundation)                                         ││
│ │ • 6.131 Orchestrator Skills (query_task, query_workflow, push_status,            ││
│ │         generate_summary, report_failure, submit_task_event, archivalist_request)││
│ │ • 6.132 Health Monitor (timeout, heartbeat, error rate, transient storm)         ││
│ │ • 6.133 Orchestrator Summary Schema (OrchestratorSummary, task records)          ││
│ │ • 6.134 Plan Modification Handling (add/cancel/modify tasks, variants)           ││
│ │ • 6.135 Crash Recovery (checkpoint resume via Architect consultation)            ││
│ │ • 6.136 Guide Routing Integration (5 route rules incl. Archivalist)              ││
│ │ • 6.137 Orchestrator Agent Core + System Prompt (Claude Haiku 4.5, Archivalist   ││
│ │         integration, 7 skills, mandatory task event submission)                  ││
│ │ • 6.138 Orchestrator Integration Tests                                           ││
│ │ • 6.139 Task Completion Event Submission (Archivalist integration)               ││
│ │                                                                                  ││
│ │ SYSTEM PROMPT HIGHLIGHTS (6.137):                                                ││
│ │   - Identity: Claude Haiku 4.5, read-only observer                               ││
│ │   - CRITICAL: Submit task events to Archivalist for ALL terminal states          ││
│ │   - Query Archivalist for failure patterns                                       ││
│ │   - 7 skills available (incl. submit_task_event, archivalist_request)            ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   6.130 → 6.132, 6.133 (parallel) → 6.131, 6.134 (parallel) →                    ││
│ │   6.135, 6.136 (parallel) → 6.139 → 6.137 → 6.138                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6C: Enhanced Agent Skills                                         ││
│ │ ** DEPENDS ON: Wave 4E (Tree-Sitter CVS Integration) for AST skills **           ││
│ │                                                                                  ││
│ │ • Engineer: Multi-Edit & Structural Refactoring                                  ││
│ │   - Uses: ts_rename_symbol, ts_extract_function, ts_find_edit_targets           ││
│ │ • Designer: Vision & Multimodal Skills                                           ││
│ │   - Uses: ts_extract_components, ts_analyze_jsx, ts_find_styles                 ││
│ │ • Librarian: Enhanced Search & AST Skills                                        ││
│ │   - Uses: ts_parse, ts_query, ts_find_*, ts_extract_symbols                     ││
│ │ • Academic: Web Research & Documentation Skills                                  ││
│ │ • Inspector: LSP & AST Validation Skills                                         ││
│ │   - Uses: ts_complexity_analysis, ts_find_code_smells, ts_validate_structure    ││
│ │ • Tester: AST-Aware Test Discovery                                               ││
│ │   - Uses: ts_discover_tests, ts_find_testable_functions, ts_match_tests         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6D: Git Tooling System (EXPANDED)                                 ││
│ │ ** FROM ARCHITECTURE.md lines 30068-30989 **                                     ││
│ │                                                                                  ││
│ │ GIT PERMISSION MATRIX:                                                           ││
│ │ • Engineer: git_status, git_diff, git_log, git_show, git_blame, git_ls_files,   ││
│ │             git_add, git_commit, git_stash (9 skills, conditional commit)        ││
│ │ • Designer: Same as Engineer (9 skills, conditional commit)                      ││
│ │ • Inspector: git_diff, git_log, git_show, git_blame (4 skills, read-only)       ││
│ │ • Tester: git_diff, git_log, git_show, git_commit (4 skills, conditional)       ││
│ │ • Architect: Full access including git_branch, git_merge, git_rebase,           ││
│ │              git_push, git_fetch (11 skills)                                     ││
│ │ • Librarian: git_log, git_show, git_diff, git_blame, git_ls_files (5 skills)    ││
│ │                                                                                  ││
│ │ HOOK IMPLEMENTATIONS:                                                            ││
│ │ • Auto-Commit Hook (for Engineer/Designer when tests pass)                       ││
│ │ • Permission Enforcement Hook (validate agent permissions)                       ││
│ │ • Git Operation Audit Hook (log all git operations to Archivalist)               ││
│ │ • Conditional Commit Hooks (Tester: only if tests pass)                          ││
│ │                                                                                  ││
│ │ TOTAL: 42+ Git skills across agents, 4 hooks                                     ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6E: Analysis Skills Orchestration (EXPANDED)                      ││
│ │ ** FROM ARCHITECTURE.md lines 31032-31850 **                                     ││
│ │                                                                                  ││
│ │ INSPECTOR ANALYSIS SKILLS:                                                       ││
│ │ • assess_change_risk - Prioritized findings with context + history               ││
│ │ • explain_finding - Semantic explanation with project-specific fix               ││
│ │ • validate_architecture - Enforce project-specific arch rules                    ││
│ │ • evaluate_blast_radius - Identify affected files when changing code             ││
│ │                                                                                  ││
│ │ TESTER ANALYSIS SKILLS:                                                          ││
│ │ • prioritize_test_targets - Coverage + complexity + changes + bug history        ││
│ │ • assess_test_quality - Evaluate tests beyond coverage (assertions, mutations)   ││
│ │ • suggest_test_cases - Generate specific test cases from code analysis           ││
│ │ • identify_coverage_gaps - Find uncovered code in changed lines                  ││
│ │                                                                                  ││
│ │ NON-REDUNDANCY PRINCIPLE:                                                        ││
│ │ • Analysis skills ORCHESTRATE existing tools (LSP, lint, coverage)               ││
│ │ • They ADD CONTEXT that static tools lack (task, history, patterns)              ││
│ │ • They do NOT reimplement what tools already do                                  ││
│ │                                                                                  ││
│ │ SKILL BUNDLES for Lazy Loading:                                                  ││
│ │ • inspector_analysis: 4 skills                                                   ││
│ │ • tester_analysis: 4 skills                                                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6F: Lazy Tool Loading System (EXPANDED)                           ││
│ │ ** FROM ARCHITECTURE.md lines 29341-30056 **                                     ││
│ │                                                                                  ││
│ │ ARCHITECTURE:                                                                    ││
│ │ • ToolManifest: metadata, dependencies, categories, bundles                      ││
│ │ • LazyToolRegistry: thread-safe registry with load-on-demand                     ││
│ │ • request_tools skill: Universal meta-skill for on-demand loading                ││
│ │                                                                                  ││
│ │ DESIGNER TOOL BUNDLES:                                                           ││
│ │ • component: component_search, component_create, component_modify                ││
│ │ • design_system: token_validate, token_suggest, palette_generate                 ││
│ │ • accessibility: a11y_audit, a11y_fix_suggest, contrast_check                    ││
│ │ • layout: grid_analyze, responsive_check, spacing_audit                          ││
│ │ • preview: screenshot, visual_diff, device_preview                               ││
│ │                                                                                  ││
│ │ ENGINEER TOOL BUNDLES:                                                           ││
│ │ • file_ops: read_file, write_file, edit_file, glob, grep                         ││
│ │ • execution: run_command, run_tests, build_project                               ││
│ │ • refactoring: rename_symbol, extract_function, inline_function                  ││
│ │                                                                                  ││
│ │ HOOK INTEGRATION:                                                                ││
│ │ • tool_access_hook: Inject tools based on detected needs                         ││
│ │ • auto_suggest: Pattern learning for bundle recommendations                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6G: Hook System Foundation (NEW)                                  ││
│ │ ** FROM ARCHITECTURE.md lines 27856-27916 **                                     ││
│ │                                                                                  ││
│ │ HOOK TYPES:                                                                      ││
│ │ • HookPriority: HIGHEST=0, HIGH=25, NORMAL=50, LOW=75, LOWEST=100               ││
│ │ • PromptHookFunc: Modify system prompt before LLM call                           ││
│ │ • ToolCallHookFunc: Intercept/modify tool calls                                  ││
│ │                                                                                  ││
│ │ EXECUTION FLOW:                                                                  ││
│ │ • Pre-Prompt Hooks: Context injection, cache lookup                              ││
│ │ • Post-Prompt Hooks: Response logging, cache storage                             ││
│ │ • Pre-Tool Hooks: Permission validation, operation tracking                      ││
│ │ • Post-Tool Hooks: Result processing, audit logging                              ││
│ │                                                                                  ││
│ │ HOOK REGISTRY:                                                                   ││
│ │ • Register by agent type + execution phase                                       ││
│ │ • Priority-ordered execution                                                     ││
│ │ • Chain-of-responsibility pattern                                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6H: Per-Agent Hooks (NEW)                                         ││
│ │ ** FROM ARCHITECTURE.md lines 27920-28787 **                                     ││
│ │                                                                                  ││
│ │ GUIDE HOOKS:                                                                     ││
│ │ • session_context_injection - Inject session state into prompt                   ││
│ │ • route_cache_lookup - Check cache before classification                         ││
│ │ • route_cache_store - Store classification in cache                              ││
│ │                                                                                  ││
│ │ ARCHIVALIST HOOKS:                                                               ││
│ │ • cross_session_query_handler - Query across sessions                            ││
│ │ • entry_session_tagging - Auto-tag entries with session ID                       ││
│ │ • pattern_promotion_check - Check for cross-session promotion                    ││
│ │                                                                                  ││
│ │ ENGINEER HOOKS:                                                                  ││
│ │ • file_operation_tracking - Track all file ops for Archivalist                   ││
│ │ • consultation_logging - Log all agent consultations                             ││
│ │ • failure_counter - Track consecutive failures                                   ││
│ │ • failure_protocol_injection - Inject recovery protocol on failure               ││
│ │ • checkpoint_creation - Create checkpoints before risky operations               ││
│ │                                                                                  ││
│ │ DESIGNER HOOKS:                                                                  ││
│ │ • inject_pipeline_context - Add task/history context to prompt                   ││
│ │ • inject_design_tokens - Inject current design token values                      ││
│ │ • inject_component_index - Inject existing component list                        ││
│ │ • validate_component_search_first - Block creation without search                ││
│ │ • validate_accessibility_required - Block completion without a11y check          ││
│ │ • validate_token_usage - Verify all values use tokens                            ││
│ │ • auto_accessibility_check - Run a11y check on tool completion                   ││
│ │ • record_component_creation - Record new components to Archivalist               ││
│ │                                                                                  ││
│ │ TOTAL: 19+ agent-specific hooks                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6I: Dynamic Tool Discovery Protocol (NEW)                         ││
│ │ ** FROM ARCHITECTURE.md lines 28791-29340 **                                     ││
│ │                                                                                  ││
│ │ THREE-TIER DISCOVERY ESCALATION:                                                 ││
│ │ • Tier 1: Librarian Detection (fast, local)                                      ││
│ │   - Package manifest parsing (package.json, pyproject.toml, go.mod, etc.)        ││
│ │   - Config file detection (.eslintrc, .prettierrc, tsconfig.json, etc.)          ││
│ │   - Binary existence check (/usr/local/bin, ./node_modules/.bin, etc.)           ││
│ │                                                                                  ││
│ │ • Tier 2: Academic Research (if Librarian can't find)                            ││
│ │   - Web search for language-specific tools                                       ││
│ │   - Documentation lookup for alternatives                                        ││
│ │   - Recommendation with install commands                                         ││
│ │                                                                                  ││
│ │ • Tier 3: User Prompt (if tool requires installation)                            ││
│ │   - Present options with install commands                                        ││
│ │   - Allow user to select or provide custom tool                                  ││
│ │                                                                                  ││
│ │ LANGUAGE DEFAULTS REGISTRY:                                                      ││
│ │ • Go: gofmt, golangci-lint, gopls, go test                                       ││
│ │ • TypeScript/JavaScript: prettier, eslint, typescript-language-server, jest     ││
│ │ • Python: black, ruff, pyright, pytest                                           ││
│ │ • Rust: rustfmt, clippy, rust-analyzer, cargo test                               ││
│ │                                                                                  ││
│ │ TREE-SITTER GRAMMAR AUTO-INSTALLATION:                                           ││
│ │ • Detect file types in codebase                                                  ││
│ │ • Auto-download missing grammars from GitHub releases                            ││
│ │ • Fallback to compilation if no prebuilt available                               ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 6J: Core Agent Skill Gaps (Foundational)                          ││
│ │ All items in this group can execute in parallel - no interdependencies.          ││
│ │                                                                                  ││
│ │ • 6.150 Architect: File & Search Operations (read_file, glob, grep)              ││
│ │ • 6.151 Designer: File Operations & Search (read/write/edit, glob, grep)         ││
│ │ • 6.152 Inspector: File Ops, Search & Execution (run_command, auto_fix)          ││
│ │ • 6.153 Tester: File Ops, Search & Execution (run_command, coverage_report)      ││
│ │ • 6.154 Librarian: File Read & Git History (read_file, git_log/blame/show/diff)  ││
│ │ • 6.155 Engineer: Search & Git WIP (glob, grep, git_stash)                       ││
│ │ • 6.156 Engineer: Debugging Capabilities (debug_print/breakpoint/run/inspect)    ││
│ │                                                                                  ││
│ │ RATIONALE: These are foundational skills missing from original agent definitions.││
│ │ Without these, agents cannot perform basic operations required by their roles.   ││
│ │ Specs added to ARCHITECTURE.md - implementation creates the actual skills.       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 25-32 parallel engineer pipelines (increased for new groups)   │
│ DEPENDENCIES: Wave 2-3 for tool execution, Wave 5 for knowledge consultation       │
│ NOTE: Group 6B (Orchestrator) required before Pipeline Variants (6.101-6.112)      │
│ NOTE: Group 6J items are prerequisites for Groups 6C-6I effectiveness              │
│ INTERNAL DEPENDENCIES:                                                             │
│   6A, 6B → 6C, 6J (parallel) → 6D, 6E, 6F (parallel) → 6G → 6H, 6I (parallel)      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 7: Quality & Planning Agents (Depends on Wave 6)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 7: QUALITY & PLANNING AGENTS                                                   │
│ ═════════════════════════════════                                                   │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7A: Quality Agent Core                                            ││
│ │ • 4.1 Inspector Agent Implementation                                             ││
│ │ • 4.2 Tester Agent Implementation                                                ││
│ │ • Inspector: Completion Evidence Protocol                                        ││
│ │ • Tester: Completion Evidence Protocol                                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7B: Inspector 8-Phase Validation System                           ││
│ │ • ValidationPhase interface (foundation)                                         ││
│ │                                                                                  ││
│ │ PHASE IMPLEMENTATIONS (Can parallelize, no inter-phase dependencies):            ││
│ │ • Phase 1: Spec Compliance Validation                                            ││
│ │ • Phase 2: Concurrency & Safety Validation                                       ││
│ │ • Phase 3: Memory & Resource Validation                                          ││
│ │ • Phase 4: Type Safety & Error Handling                                          ││
│ │ • Phase 5: Code Structure & Complexity                                           ││
│ │ • Phase 6: Language Idioms & Modern Features                                     ││
│ │ • Phase 7: Documentation & Readability                                           ││
│ │ • Phase 8: Design & Architecture Smells                                          ││
│ │                                                                                  ││
│ │ ORCHESTRATION (After all phases):                                                ││
│ │ • ValidationOrchestrator (depends on all phases)                                 ││
│ │ • ValidationReport schema (depends on orchestrator)                              ││
│ │ • Integration tests (depends on orchestrator)                                    ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   ValidationPhase interface → Phases 1-8 (parallel) → Orchestrator → Tests       ││
│ │                                                                                  ││
│ │ VALIDATION PHILOSOPHY:                                                           ││
│ │   - RUTHLESS: No benefit of doubt. Suspicious = FAIL                             ││
│ │   - SPECIFIC: Exact file paths, line numbers, code snippets                      ││
│ │   - DETAILED: WHY it's a problem, HOW to fix it                                  ││
│ │   - UNCOMPROMISING: "Good enough" is not acceptable                              ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7C: Tester 6-Category Test System                                 ││
│ │ • TestCategory interface (foundation)                                            ││
│ │                                                                                  ││
│ │ CATEGORY IMPLEMENTATIONS (Can parallelize, no inter-category dependencies):      ││
│ │ • Category 1: Happy Path Tests                                                   ││
│ │ • Category 2: Negative Path Tests                                                ││
│ │ • Category 3: Error Handling Tests                                               ││
│ │ • Category 4: Edge Case Tests                                                    ││
│ │ • Category 5: Concurrency Tests                                                  ││
│ │ • Category 6: Integration Tests                                                  ││
│ │                                                                                  ││
│ │ QUALITY & ORCHESTRATION (After all categories):                                  ││
│ │ • TestQualityChecker (depends on all categories)                                 ││
│ │ • JunkTestDetector (depends on quality checker)                                  ││
│ │ • TestOrchestrator (depends on all above)                                        ││
│ │ • TestReport schema (depends on orchestrator)                                    ││
│ │ • Integration tests (depends on orchestrator)                                    ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   TestCategory interface → Categories 1-6 (parallel) → Quality/Junk →            ││
│ │   Orchestrator → Tests                                                           ││
│ │                                                                                  ││
│ │ TESTING PHILOSOPHY:                                                              ││
│ │   - THOROUGH: Leave no stone unturned. If it can fail, test it                   ││
│ │   - DISCERNING: Quality over quantity. No junk tests                             ││
│ │   - REALISTIC: Test real use cases, not implementation details                   ││
│ │   - PERFORMANT: Fast tests. Slow tests are bad tests                             ││
│ │   - INFORMATIVE: Clear failure messages. Engineers know exactly why              ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7D: Architect Atomic Task Generation System                       ││
│ │ • DecompositionEngine (foundation)                                               ││
│ │                                                                                  ││
│ │ DECOMPOSITION COMPONENTS (Can parallelize):                                      ││
│ │ • Deep Decomposition Engine (requirements, assumptions, edge cases)              ││
│ │ • Iterative Clarification Protocol (query until complete)                        ││
│ │ • AtomicTask schema (all 7 required sections)                                    ││
│ │ • Task Validator (atomic, independent, testable, scoped)                         ││
│ │                                                                                  ││
│ │ WORKFLOW GENERATION (After decomposition):                                       ││
│ │ • WorkflowDAG Generator (topological sort, parallelization)                      ││
│ │ • Wave Organization (Foundation → Core → Integration → Validation)               ││
│ │ • Workflow Output Format (JSON for Orchestrator)                                 ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   DecompositionEngine → Clarification + Schema + Validator (parallel) →          ││
│ │   DAG Generator → Wave Org → Output                                              ││
│ │                                                                                  ││
│ │ ARCHITECT PHILOSOPHY:                                                            ││
│ │   - PROFESSIONALLY CRITICAL: Challenge flawed approaches with evidence           ││
│ │   - INQUISITIVE: Ask until COMPLETE clarity. Default to asking                   ││
│ │   - DETAIL-ORIENTED: Every edge case, failure mode, mitigation                   ││
│ │   - SYSTEMIC: Understand how work fits into larger system                        ││
│ │   - RELENTLESS: Pursue clarity before committing to design                       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7E: Planning Agent Integration                                    ││
│ │ • 3.1 Architect Agent Implementation (with Task Generation)                      ││
│ │ • Architect: Pre-Delegation Planning Protocol                                    ││
│ │ • Orchestrator: Execution Discipline Protocol                                    ││
│ │ • Plan Mode Implementation                                                       ││
│ │ • Research Paper Implementation                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7F: Agent Discipline Protocols (NEW)                              ││
│ │ Behavioral protocols ensuring consistency, quality, and learning.                ││
│ │                                                                                  ││
│ │ GUIDE PROTOCOLS (Can parallelize):                                               ││
│ │ • Guide: Ambiguity Resolution Discipline                                         ││
│ │ • Guide: Pre-Routing Enrichment Protocol                                         ││
│ │ • Guide: Skill Match Verification Protocol                                       ││
│ │                                                                                  ││
│ │ ACADEMIC PROTOCOLS (Can parallelize):                                            ││
│ │ • Academic: Source Citation Discipline                                           ││
│ │ • Academic: Research Depth Calibration Protocol                                  ││
│ │ • Academic: Cross-Agent Research Synthesis Protocol                              ││
│ │                                                                                  ││
│ │ ARCHITECT PROTOCOLS (Can parallelize):                                           ││
│ │ • Architect: Plan Complexity Discipline                                          ││
│ │ • Architect: Assumption Documentation Protocol                                   ││
│ │ • Architect: Historical Failure Integration Protocol                             ││
│ │ • Architect: Scope Creep Detection Protocol                                      ││
│ │                                                                                  ││
│ │ ENGINEER PROTOCOLS (Can parallelize):                                            ││
│ │ • Engineer: Pre-Implementation Verification Protocol                             ││
│ │ • Engineer: Incremental Validation Protocol                                      ││
│ │ • Engineer: Pattern Adherence Discipline                                         ││
│ │ • Engineer: Rollback Documentation Protocol                                      ││
│ │                                                                                  ││
│ │ DESIGNER PROTOCOLS (Can parallelize):                                            ││
│ │ • Designer: Visual Regression Awareness Protocol                                 ││
│ │ • Designer: Token Validation Gate Protocol                                       ││
│ │ • Designer: Responsive Testing Discipline                                        ││
│ │ • Designer: Animation Performance Protocol                                       ││
│ │                                                                                  ││
│ │ INSPECTOR PROTOCOLS (Can parallelize):                                           ││
│ │ • Inspector: Historical Issue Cross-Reference Protocol                           ││
│ │ • Inspector: Validation Evidence Documentation Protocol                          ││
│ │ • Inspector: Progressive Validation Protocol                                     ││
│ │ • Inspector: False Positive Tracking Protocol                                    ││
│ │                                                                                  ││
│ │ TESTER PROTOCOLS (Can parallelize):                                              ││
│ │ • Tester: Existing Test Integration Protocol                                     ││
│ │ • Tester: Flaky Test Prevention Protocol                                         ││
│ │ • Tester: Coverage Gap Prioritization Protocol                                   ││
│ │ • Tester: Test Maintenance Burden Protocol                                       ││
│ │                                                                                  ││
│ │ LIBRARIAN PROTOCOLS (Can parallelize):                                           ││
│ │ • Librarian: Staleness Detection Discipline                                      ││
│ │ • Librarian: Confidence Calibration Protocol                                     ││
│ │ • Librarian: Pattern Evolution Tracking Protocol                                 ││
│ │ • Librarian: Index Health Monitoring Protocol                                    ││
│ │                                                                                  ││
│ │ ARCHIVALIST PROTOCOLS (Can parallelize):                                         ││
│ │ • Archivalist: Knowledge Decay Protocol                                          ││
│ │ • Archivalist: Cross-Session Promotion Protocol                                  ││
│ │ • Archivalist: Conflict Detection & Resolution Protocol                          ││
│ │ • Archivalist: Retrieval Quality Feedback Loop Protocol                          ││
│ │                                                                                  ││
│ │ ORCHESTRATOR PROTOCOLS (Can parallelize):                                        ││
│ │ • Orchestrator: Execution Anomaly Detection Protocol                             ││
│ │ • Orchestrator: Pipeline Health Monitoring Protocol                              ││
│ │ • Orchestrator: Completion Verification Protocol                                 ││
│ │ • Orchestrator: Graceful Degradation Protocol                                    ││
│ │                                                                                  ││
│ │ DISCIPLINE PROTOCOL PHILOSOPHY:                                                  ││
│ │   - HISTORICAL AWARENESS: Learn from past failures                               ││
│ │   - VERIFICATION GATES: Don't proceed without validation                         ││
│ │   - CONFIDENCE CALIBRATION: Know when you don't know                             ││
│ │   - CONSISTENCY: Match existing patterns, document deviations                    ││
│ │   - CONTINUOUS IMPROVEMENT: Track outcomes, adjust behavior                      ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   All protocols within an agent can parallelize.                                 ││
│ │   Protocols depend on base agent implementation from Groups 7A-7E.               ││
│ │   Archivalist protocols depend on Archivalist agent (Wave 5).                    ││
│ │   Librarian protocols depend on Librarian agent (Wave 5).                        ││
│ │                                                                                  ││
│ │ ESTIMATED ITEMS: 36 protocol implementations (can parallelize by agent)          ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 7G: Tree-Sitter CLI & Setup (TS Phase 5)                         ││
│ │ ** NEW: User-facing grammar management and setup integration **                  ││
│ │                                                                                  ││
│ │ TS PHASE 5 (Can parallel with 7A-7F):                                           ││
│ │ • TS.5.1 CLI Commands                                                           ││
│ │   - sylk grammar list (installed + available)                                   ││
│ │   - sylk grammar install <name>                                                 ││
│ │   - sylk grammar add <repo-url> (custom grammars)                               ││
│ │   - sylk grammar validate <path>                                                ││
│ │   - sylk grammar remove <name>                                                  ││
│ │   - sylk grammar info <name>                                                    ││
│ │ • TS.5.2 Setup Integration                                                      ││
│ │   - libtree-sitter download for platform                                        ││
│ │   - Default grammar set installation (Go, TS, Python, Rust)                     ││
│ │   - User grammar directory creation (~/.sylk/grammars/)                         ││
│ │   - Configuration file generation (grammar.yaml template)                       ││
│ │                                                                                  ││
│ │ TS PHASE 6 (After TS.5.*, parallel):                                            ││
│ │ • TS.6.1 User-Defined Grammar Support                                           ││
│ │   - grammar.yaml parsing and validation                                         ││
│ │   - Hot-reload support for grammar changes                                      ││
│ │ • TS.6.2 Testing and Documentation                                              ││
│ │   - Unit tests, integration tests, benchmarks                                   ││
│ │   - Documentation: Adding new language support                                  ││
│ │   - Query pattern cookbook                                                      ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   cmd/sylk/grammar.go, core/treesitter/setup.go,                                ││
│ │   core/treesitter/user_grammar.go, docs/treesitter.md                           ││
│ │                                                                                  ││
│ │ INTERNAL DEPENDENCIES:                                                           ││
│ │   TS.5.1 & TS.5.2 (parallel) → TS.6.1 & TS.6.2 (parallel)                       ││
│ │   Depends on: Wave 4E (Tree-Sitter agent skills)                                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 32-40 parallel engineer pipelines (increased for protocols+TS) │
│ DEPENDENCIES: Wave 6 for execution agents, Wave 5 for knowledge, Wave 4E for TS    │
│ NOTE: Groups 7B/7C/7D can execute in parallel, orchestrators sequential after      │
│ NOTE: Group 7F can parallelize with 7B/7C/7D after agent cores are ready           │
│ NOTE: Group 7G can parallelize with 7A-7F (independent CLI/setup work)             │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 8: Pipeline System (Depends on Waves 5-7, Orchestrator 6.130-6.139)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 8: PIPELINE SYSTEM                                                             │
│ ═══════════════════════                                                             │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 8A: Pipeline Core                                                 ││
│ │ • 6.43 Single-Worker Pipeline System                                             ││
│ │ • Pipeline Variants System Core (6.101-6.112) - REQUIRES Orchestrator (6.130-39) ││
│ │ • 6.105B Mandatory User Selection Policy (CRITICAL)                              ││
│ │   - NO auto-select: TimeoutAction can only be 'none' or 'cancel'                ││
│ │   - User MUST explicitly choose variant via /variants select                    ││
│ │   - Presentation: original + all variants with approach/files/diff              ││
│ │   - Blocking: Pipeline halts at variant gate until user decision                ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 8B: Pipeline Agent Modes (after 8A)                               ││
│ │ • 6.44 Inspector Pipeline Modes                                                  ││
│ │ • 6.45 Tester Pipeline Modes                                                     ││
│ │ • Engineer Pipeline Modes                                                        ││
│ │ • Designer Pipeline Modes                                                        ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 8C: Pipeline Integration (after 8B)                               ││
│ │ • 6.46 Architect Decomposition Strategy                                          ││
│ │ • 6.47 Pipeline Failure Routing                                                  ││
│ │ • 6.48 Task Completion Commands                                                  ││
│ │ • Memory Management: Handoff Protocols                                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 8D: FILESYSTEM Pipeline VFS Integration (FS Phase 6)              ││
│ │ ** NEW: VFS integration with pipeline execution and scheduling **                ││
│ │                                                                                  ││
│ │ PHASE 6 (After CVS from Wave 4C):                                                ││
│ │ • FS.6.1 Integrate VFS with Pipeline Runner                                      ││
│ │   - PipelineVFSIntegration struct                                                ││
│ │   - SetupPipeline creates VFS on pipeline creation                               ││
│ │   - TeardownPipeline commits/rolls back based on success                         ││
│ │   - Engineer file operations route through VFS                                   ││
│ │ • FS.6.2 Integrate with Pipeline Scheduler                                       ││
│ │   - PipelineSchedulerVFS extends existing scheduler                              ││
│ │   - File dependency tracking per task                                            ││
│ │   - Write-write conflict detection                                               ││
│ │   - Conflicting tasks serialized (not parallel)                                  ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/pipeline/vfs_integration.go, core/session/pipeline_scheduler_vfs.go       ││
│ │                                                                                  ││
│ │ INTEGRATES WITH:                                                                 ││
│ │   - Pipeline Variants (6.101-6.112) - variant VFS isolation                      ││
│ │   - Pipeline Runner - automatic VFS lifecycle                                    ││
│ │   - Existing PipelineScheduler (0.18, 0.57)                                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 8E: FILESYSTEM AST Parsers (FS Phase 8)                           ││
│ │ ** NEW: Language-specific AST parsers for stable targeting **                    ││
│ │                                                                                  ││
│ │ PHASE 8 (All parallel - no interdependencies):                                   ││
│ │ • FS.8.1 Go AST Parser (using go/ast stdlib)                                     ││
│ │ • FS.8.2 TypeScript/JavaScript Parser (.ts, .tsx, .js, .jsx)                     ││
│ │ • FS.8.3 Python Parser (.py, .pyw)                                               ││
│ │ • FS.8.4 Parser Registry (extension → parser mapping)                            ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/versioning/parser_go.go, parser_typescript.go,                            ││
│ │   parser_python.go, parser_registry.go                                           ││
│ │                                                                                  ││
│ │ NOTE: Can run in parallel with Groups 8A-8D                                      ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 14-20 parallel engineer pipelines (increased for FS)           │
│ DEPENDENCIES: All agent implementations complete, Wave 4C-4D FILESYSTEM complete   │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 9: Agent Efficiency & Direct Consultation (Depends on Wave 8)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 9: AGENT EFFICIENCY & DIRECT CONSULTATION                                      │
│ ══════════════════════════════════════════════                                      │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 9A: Efficiency Techniques                                         ││
│ │ • 6.19 XOR Filter Optimization                                                   ││
│ │ • 6.20 Architect Planning Preflight                                              ││
│ │ • 6.21-6.24 Scratchpad, Style Inference, Component Registry, Mistake Memory      ││
│ │ • 6.25-6.28 Diff Preview, Preferences, File Snapshot, Dependency Awareness       ││
│ │ • 6.29-6.30 Task Continuity, Design Tokens                                       ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 9B: Direct Consultation Protocol                                  ││
│ │ • Guide: Direct Consultation Skills                                              ││
│ │ • Architect: Direct Consultation Skills                                          ││
│ │ • Engineer: Direct Consultation Skills                                           ││
│ │ • Designer: Direct Consultation Skills                                           ││
│ │ • Inspector: Direct Consultation Skills                                          ││
│ │ • Tester: Direct Consultation Skills                                             ││
│ │ • Librarian: Direct Consultation Skills                                          ││
│ │ • Archivalist: Direct Consultation Skills                                        ││
│ │ • Academic: Direct Consultation Skills                                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 9C: Skill/Agent Integrations                                      ││
│ │ • 6.33-6.42 All Agent Efficiency Integrations                                    ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 9E: FILESYSTEM Versioning Skills & Hooks (FS Phase 9)             ││
│ │ ** NEW: Version-aware file operations and Archivalist integration **             ││
│ │                                                                                  ││
│ │ SKILLS (Can parallel - extend/replace existing file skills):                     ││
│ │ • FS.9.1 Versioning Skills for Engineers                                         ││
│ │   - versioned_read_file (read at version or HEAD)                                ││
│ │   - versioned_write_file (creates new version with message)                      ││
│ │   - versioned_file_history (returns version list)                                ││
│ │   - versioned_file_diff (diff between versions)                                  ││
│ │   - versioned_rollback (rollback to previous version)                            ││
│ │   - All skills respect AgentRole permissions                                     ││
│ │                                                                                  ││
│ │ HOOKS (Integrates with existing hook system):                                    ││
│ │ • FS.9.2 File Operation Hooks                                                    ││
│ │   - PreWriteHook (validates permissions, sanitizes secrets, checks locks)        ││
│ │   - PostWriteHook (records to Archivalist, emits signals)                        ││
│ │   - ConflictDetectionHook (checks for concurrent modification)                   ││
│ │   - Updates SessionContext.ModifiedFiles                                         ││
│ │                                                                                  ││
│ │ FILES:                                                                           ││
│ │   core/versioning/skills.go, core/versioning/hooks.go                            ││
│ │                                                                                  ││
│ │ INTEGRATES WITH:                                                                 ││
│ │   - Existing engineer file skills (read_file, write_file, edit_file)             ││
│ │   - Hook system from ARCHITECTURE.md (pre-tool, post-tool hooks)                 ││
│ │   - Archivalist for file change tracking                                         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ ✓ COMPLETE: GROUP 9D: Inter-Agent Communication Gaps (6.160-6.165)              ││
│ │ • 6.160 Inspector: Academic Consultation ✓                                       ││
│ │ • 6.161 Tester: Academic Consultation ✓                                          ││
│ │ • 6.162 Orchestrator: Architect Signaling ✓                                      ││
│ │ • 6.163 Engineer: Pipeline Context & Artifact Discovery ✓                        ││
│ │ • 6.164 Architect: Pre-Delegation Validation Hook ✓                              ││
│ │ • 6.165 DAG Executor: Signaling Layer ✓                                          ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 15-20 parallel engineer pipelines                              │
│ DEPENDENCIES: Pipeline system complete, all agents operational                      │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**WAVE 10: Integration Testing & Validation (Final Wave)**

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ WAVE 10: INTEGRATION TESTING & VALIDATION                                           │
│ ═════════════════════════════════════════                                           │
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 10A: Component Integration Tests                                  ││
│ │ • 0.25 Concurrency Integration Tests                                             ││
│ │ • 0.38 Error & Resource Integration Tests                                        ││
│ │ • 0.54 Tool Execution Integration Tests                                          ││
│ │ • 0.60 Tier 1 Multi-Session Integration Tests                                    ││
│ │ • 0.67 Tier 3 Integration Tests                                                  ││
│ │ • 0.74 Tier 4 Integration Tests                                                  ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 10B: System-Wide Integration                                      ││
│ │ • 6.17 Performance Benchmarks                                                    ││
│ │ • 6.18 Integration Tests                                                         ││
│ │ • 6.49 Pipeline Integration Tests                                                ││
│ │ • Cross-Agent Discipline Integration                                             ││
│ │ • Knowledge Agent Feedback Loops                                                 ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 10C: FILESYSTEM Versioning Tests (FS Phases 10-11)                ││
│ │ ** NEW: Comprehensive versioning system tests **                                 ││
│ │                                                                                  ││
│ │ INTEGRATION TESTS:                                                               ││
│ │ • FS.10.1 Single Pipeline Workflow (end-to-end)                                  ││
│ │ • FS.10.1 Concurrent Pipelines (file conflict handling)                          ││
│ │ • FS.10.1 Variant Workflow (create, execute, select)                             ││
│ │ • FS.10.1 Cross-Session Coordination (file changes, locks)                       ││
│ │ • FS.10.1 Conflict Resolution (OT merge, UI interaction)                         ││
│ │ • FS.10.1 Crash Recovery (WAL replay)                                            ││
│ │                                                                                  ││
│ │ STRESS TESTS:                                                                    ││
│ │ • FS.10.2 High Concurrency (10+ sessions, 100+ pipelines)                        ││
│ │ • FS.10.2 Large Files (10MB+ without OOM)                                        ││
│ │ • FS.10.2 Many Versions (1000+ per file, stable performance)                     ││
│ │ • FS.10.2 Benchmarks (Write, Read, Transform, GetHistory)                        ││
│ │                                                                                  ││
│ │ TARGET: 90%+ code coverage for core/versioning package                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ PARALLEL GROUP 10D: Tree-Sitter Tests (TS.6.2)                                  ││
│ │ ** NEW: Comprehensive tree-sitter parsing tests **                               ││
│ │                                                                                  ││
│ │ UNIT TESTS:                                                                      ││
│ │ • Purego bindings (all registered functions)                                     ││
│ │ • Parser lifecycle (create, set language, parse, delete)                         ││
│ │ • Tree traversal (children, siblings, parent, cursor)                            ││
│ │ • Query execution (patterns, captures, predicates)                               ││
│ │ • Grammar Registry (lookup, list, detect language)                               ││
│ │ • Grammar Downloader (download, verify, fallback to compile)                     ││
│ │                                                                                  ││
│ │ INTEGRATION TESTS:                                                               ││
│ │ • Parse all supported languages (30+ grammar files)                              ││
│ │ • Incremental parsing vs full parse consistency                                  ││
│ │ • CVS integration (NodePath stability across versions)                           ││
│ │ • Agent skill execution (Librarian, Engineer, Inspector, Tester, Designer)       ││
│ │ • User-defined grammar loading (custom grammar.yaml)                             ││
│ │ • CLI commands (grammar list/install/add/remove)                                 ││
│ │                                                                                  ││
│ │ STRESS TESTS:                                                                    ││
│ │ • Large file parsing (10MB+ without OOM)                                         ││
│ │ • Concurrent parsing (multiple parsers, multiple languages)                      ││
│ │ • Memory leak detection (repeated parse/delete cycles)                           ││
│ │                                                                                  ││
│ │ BENCHMARKS:                                                                      ││
│ │ • Parse time vs file size (linear scaling expected)                              ││
│ │ • Incremental parse time vs edit size                                            ││
│ │ • Query execution time vs AST size                                               ││
│ │                                                                                  ││
│ │ TARGET: 90%+ code coverage for core/treesitter package                           ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ┌─────────────────────────────────────────────────────────────────────────────────┐│
│ │ SEQUENTIAL: Acceptance Verification                                              ││
│ │ • Token Savings Targets Validation                                               ││
│ │ • Latency Targets Validation                                                     ││
│ │ • Memory Targets Validation                                                      ││
│ │ • Full System Acceptance                                                         ││
│ └─────────────────────────────────────────────────────────────────────────────────┘│
│                                                                                     │
│ ESTIMATED CAPACITY: 14-18 parallel engineer pipelines (increased for TS tests)     │
│ DEPENDENCIES: All implementation waves complete                                     │
│                                                                                     │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### Wave Dependency Graph

```
                              ┌──────────┐
                              │  WAVE 0  │
                              │Foundation│
                              └────┬─────┘
                                   │
                              ┌────▼─────┐
                              │  WAVE 1  │
                              │Core Infra│
                              └────┬─────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                    │
         ┌────▼─────┐         ┌────▼─────┐        ┌────▼─────┐
         │  WAVE 2  │         │  WAVE 5  │        │ (parallel)│
         │ Exec Frm │         │Knowledge │        │           │
         └────┬─────┘         │  Agents  │        │           │
              │               └────┬─────┘        │           │
         ┌────▼─────┐              │               │           │
         │  WAVE 3  │              │               │           │
         │Tool/File │              │               │           │
         │  +FS 1-2 │              │               │           │
         │  +TS 1-2 │◄──── Tree-Sitter Foundation ─┤           │
         └────┬─────┘              │               │           │
              │                    │               │           │
         ┌────▼─────┐              │               │           │
         │  WAVE 4  │              │               │           │
         │Security  │              │               │           │
         │  +FS 3-7 │              │               │           │
         │  +TS 3-4 │◄──── TS CVS + Agent Skills ──┤           │
         └────┬─────┘              │               │           │
              │                    │               │           │
              └──────────┬─────────┘               │           │
                         │                         │           │
                    ┌────▼─────┐                   │           │
                    │  WAVE 6  │◄──────────────────┘           │
                    │Exec Agts │                               │
                    │ +TS Agnt │◄──── Enhanced with TS skills  │
                    └────┬─────┘                               │
                         │                                     │
                    ┌────▼─────┐                               │
                    │  WAVE 7  │                               │
                    │Quality/  │                               │
                    │Planning  │                               │
                    │  +TS CLI │◄──── Grammar CLI + Setup      │
                    └────┬─────┘                               │
                         │                                     │
                    ┌────▼─────┐                               │
                    │  WAVE 8  │                               │
                    │ Pipeline │                               │
                    └────┬─────┘                               │
                         │                                     │
                    ┌────▼─────┐                               │
                    │  WAVE 9  │◄──────────────────────────────┘
                    │Efficiency│
                    └────┬─────┘
                         │
                    ┌────▼─────┐
                    │ WAVE 10  │
                    │Validation│
                    │  +TS Tst │◄──── Tree-Sitter Tests
                    └──────────┘
```

**Tree-Sitter Dependency Flow:**
```
Wave 3 (TS 1-2)    Wave 4 (TS 3-4)    Wave 6        Wave 7 (TS 5-6)    Wave 10
┌────────────┐    ┌─────────────┐    ┌─────────┐    ┌────────────┐    ┌─────────┐
│TS.1.1-1.3  │───►│TS.3.1 CVS  │───►│Agent    │───►│TS.5.1 CLI  │───►│TS.6.2   │
│TS.2.1-2.4  │    │ Integration │    │Skills   │    │TS.5.2 Setup│    │Tests    │
│Foundation  │    │             │    │Enhanced │    │TS.6.1 User │    │         │
└────────────┘    │TS.4.1-4.6  │    │         │    │Grammars    │    │         │
                  │Agent Skills │    │         │    │            │    │         │
                  └─────────────┘    └─────────┘    └────────────┘    └─────────┘
```

### Parallel Execution Summary

| Wave | Name | Parallel Items | Dependencies | Est. Capacity |
|------|------|----------------|--------------|---------------|
| 0 | Foundation | 15-20 | None | 15-20 pipelines |
| 1 | Core Infrastructure | 12-16 | Wave 0 | 12-16 pipelines |
| 2 | Execution Framework | 18-22 | Wave 1 | 18-22 pipelines |
| 3 | Tool Execution + **FS Foundation** + **TS Phases 1-2** | 34-42 | Wave 2 | 34-42 pipelines |
| 4 | Security/Multi-Session + **FS Core** + **TS Phases 3-4** | 26-32 | Wave 3 | 26-32 pipelines |
| 5 | Knowledge Agents | 10-12 | Wave 1 (partial Wave 2) | 10-12 pipelines |
| 6 | Execution Agents + **TS Agent Skills** | 22-28 | Wave 2-3, Wave 5, Wave 4E | 22-28 pipelines |
| 7 | Quality/Planning + Discipline Protocols + **TS CLI** | 48-52 | Wave 6, Wave 5, Wave 4E | 32-40 pipelines |
| 8 | Pipeline System + **FS Pipeline/Parsers** | 14-20 | Waves 5-7, Wave 4 FS | 14-20 pipelines |
| 9 | Agent Efficiency + **FS Skills/Hooks** | 18-24 | Wave 8 | 18-24 pipelines | **Group 9D COMPLETE** |
| 10 | Validation + **FS Tests** + **TS Tests** | 16-22 | All Waves | 16-22 pipelines |

**NEW: Tree-Sitter Distribution:**
| Wave | TS Phase | Items | Description |
|------|----------|-------|-------------|
| 3 | TS 1-2 | 7 | Purego bindings, Go types, Registry, Parser, Tree, Query, Downloader |
| 4 | TS 3-4 | 7 | CVS Manager + 6 agent-specific skill sets |
| 7 | TS 5-6 | 4 | CLI commands, Setup integration, User grammars, Testing/Docs |

**NEW: FILESYSTEM Versioning Distribution:**
| Wave | FS Phase | Items | Description |
|------|----------|-------|-------------|
| 3 | FS 1-2 | 9 | Foundation types, Storage layer |
| 4 | FS 3-5, 7 | 8 | VFS, OT Engine, CVS, Cross-Session |
| 8 | FS 6, 8 | 6 | Pipeline VFS Integration, AST Parsers |
| 9 | FS 9 | 2 | Versioning Skills, File Hooks |
| 10 | FS 10-11 | 4 | Integration Tests, Stress Tests |

**Wave 6 Orchestrator Breakdown (6.130-6.139):**
| Group | Tasks | Internal Dependencies |
|-------|-------|----------------------|
| A | 6.130 (Buffer Types) | None |
| B | 6.132, 6.133 (Health, Summary) | Group A |
| C | 6.131, 6.134 (Skills, Plan Mod) | Groups A+B |
| D | 6.135, 6.136 (Recovery, Routing) | Group C |
| E | 6.139 (Task Events/Archivalist) | Groups C+D |
| F | 6.137 (Agent Core) | Groups A-E |
| G | 6.138 (Integration Tests) | Group F |

**Key Parallelization Opportunities:**
1. Wave 5 (Knowledge Agents) can START after Wave 1, running parallel with Waves 2-4
2. Within each wave, groups A/B/C/D execute in parallel
3. Agent efficiency techniques (Wave 9) highly parallelizable across all 9 agents
4. Integration tests (Wave 10) can parallelize by subsystem
5. **Orchestrator (6.130-6.139)** has 7 internal parallel groups, can parallelize within Wave 6
6. **Discipline Protocols (Group 7F)** adds 36 parallelizable items by agent - can run with 7B/7C/7D
7. **NEW: FILESYSTEM Phases 1-2** can fully parallelize within Wave 3 Group 3D
8. **NEW: FILESYSTEM Phases 3-4** can run parallel in Wave 4 (VFS and OT are independent)
9. **NEW: FILESYSTEM Parsers (Phase 8)** can fully parallelize in Wave 8 Group 8E

**Critical Path:**
Wave 0 → Wave 1 → Wave 2 → Wave 3 (incl. FS 1-2) → Wave 4 (incl. FS 3-5, 7) → Wave 6 (Orchestrator) → Wave 7 → Wave 8 (incl. FS 6, 8) → Wave 9 (incl. FS 9) → Wave 10 (incl. FS 10-11)

**FILESYSTEM Critical Path (within Waves):**
FS.1.* (W3, parallel) → FS.2.* (W3, parallel) → FS.3-4 (W4, parallel) → FS.5 CVS (W4) → FS.7 Cross-Session (W4) → FS.6 Pipeline (W8) → FS.8 Parsers (W8, parallel) → FS.9 Skills (W9) → FS.10-11 Tests (W10)

**Orchestrator Critical Path (within Wave 6):**
6.130 → 6.132/6.133 → 6.131/6.134 → 6.135/6.136 → 6.139 → 6.137 → 6.138

**Optimization Note:** Waves 5, 6, 7, 8, 9 have significant cross-dependencies but can overlap if carefully scheduled. The Orchestrator (once implemented in Wave 6) monitors completion status and eagerly starts downstream work as soon as dependencies are satisfied. Pipeline Variants (6.101-6.112) in Wave 8 specifically requires Orchestrator completion. **NEW:** FILESYSTEM VFS integration (FS.6) should integrate with Pipeline Variants for variant VFS isolation.
