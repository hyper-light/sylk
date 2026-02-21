• Purpose Gap (From ARCHITECTURE.md)
  ARCHITECTURE.md defines Architect as the central planner/coordinator: decompose deeply, consult knowledge
  agents via Guide, ask user only as last resort, emit pre-delegation declarations, run plan mode, hand off
  to Orchestrator, monitor execution, and manage revisions (ARCHITECTURE.md:21740, ARCHITECTURE.md:21953,
  ARCHITECTURE.md:22000, ARCHITECTURE.md:24485, ARCHITECTURE.md:24662, ARCHITECTURE.md:52509,
  ARCHITECTURE.md:53459, ARCHITECTURE.md:55297).

  Current code is a thin in-memory planner with many stubs (agents/architect/architect.go, agents/
  architect/skills.go) and does not implement most of that contract.
• Purpose Gap (From ARCHITECTURE.md)
  ARCHITECTURE.md defines Architect as the central planner/coordinator: decompose deeply, consult knowledge
  agents via Guide, ask user only as last resort, emit pre-delegation declarations, run plan mode, hand off
  to Orchestrator, monitor execution, and manage revisions (ARCHITECTURE.md:21740, ARCHITECTURE.md:21953,
  ARCHITECTURE.md:22000, ARCHITECTURE.md:24485, ARCHITECTURE.md:24662, ARCHITECTURE.md:52509,
  ARCHITECTURE.md:53459, ARCHITECTURE.md:55297).

  Current code is a thin in-memory planner with many stubs (agents/architect/architect.go, agents/
  architect/skills.go) and does not implement most of that contract.

  What’s missing to be maximally robust/correct/performant

  1. Mandatory consultation protocol before planning/delegation is missing (only optional Librarian call,
     no Archivalist/Academic gate, no declaration persistence/validation): agents/architect/
     architect.go:539, agents/architect/architect.go:643, ARCHITECTURE.md:24485.
  2. Plan mode state machine/persistence/versioning is missing (core/plan + plan files + approval lifecycle
     in doc are not implemented in codebase): ARCHITECTURE.md:52509, ARCHITECTURE.md:54260.
  3. Execution oversight loop is missing (no step completion handler, no sync back to plan file, no
     recovery workflow engine): ARCHITECTURE.md:53459.
  4. Research-paper proposal ingestion path is missing (documented handleProposal/read_research_paper flow
     is not present): ARCHITECTURE.md:55297.
  5. Orchestrator handoff/execution integration is missing from Architect implementation (no dispatch path
     in agents/architect): ARCHITECTURE.md:22311.
  6. Cross-domain context is not actually used in Architect request handling (Guide can attach it,
     Architect ignores it): agents/guide/guide.go:440, agents/architect/architect.go:337.
  7. Cross-domain querying in Architect is stubbed (returns empty content, source "architect"): agents/
     architect/architect.go:932.
  8. Consultation response handling is missing (Architect logs responses but never correlates/uses them):
     agents/architect/architect.go:422.
  9. Skills/tools are effectively unavailable at runtime due load-order bug: loader is created before
     skills are registered, so core skills are not marked loaded; GetToolDefinitions() returns loaded only:
     agents/architect/architect.go:144, agents/architect/architect.go:155, agents/architect/
     architect.go:1065, core/skills/skills.go:520.
     I verified this with a runtime check: all 6, loaded 0, tooldefs=0.
  10. Architect documented toolset is largely missing (read/glob/grep/git/lsp/ast/plan-mode skills are not
     implemented in agents/architect): ARCHITECTURE.md:24083, ARCHITECTURE.md:24599.
  11. Architect skillfiles directory is missing despite documented plan-mode SKILL.md workflow:
     ARCHITECTURE.md:54766.
  12. Correctness bug: substring matching is broken; recall/check matches any non-empty strings
     (containsIgnoreCase is a stub): agents/architect/architect.go:1080.
  13. Correctness bug: task dependencies copy component dependency names directly, but task IDs are task_n,
     causing invalid DAG dependencies unless input is pre-normalized: agents/architect/architect.go:701,
     agents/architect/architect.go:706.
  14. Correctness bug: generated agent type may be unsupported (documenter), which is not a registered
     target in Guide routing targets: agents/architect/architect.go:741, agents/guide/types.go:220.
  15. Correctness bug: planning errors are converted into “successful” return with failed plan payload
     (error swallowed as data), reducing failure signaling integrity: agents/architect/architect.go:531,
     agents/architect/architect.go:534, agents/architect/architect.go:488.
  16. Session correctness gap: Architect drops session identity on forwarded requests and consultations
     (SessionID: ""), blocking session-isolated planning and archival semantics: agents/architect/
     architect.go:368, agents/architect/architect.go:649.
  17. Thread safety gaps: shared maps (activePlans, knownAgents) are accessed from async bus handlers and
     API methods without locking: agents/architect/architect.go:43, agents/architect/architect.go:46,
     agents/architect/architect.go:438, agents/architect/architect.go:449, agents/architect/
     architect.go:582, agents/architect/architect.go:907.
  18. Performance/quality gap in synthesis: lexical Jaccard-based dedup/conflict detection is naive and
     non-semantic; conflict logic is weak for real contradictions: agents/architect/synthesis.go:189,
     agents/architect/synthesis.go:245.
  19. Cross-domain robustness gap: goroutines in domain dispatch have no panic recovery and semaphore
     acquisition is not cancellation-aware, which can stall/terminate badly under pathological handlers:
     agents/architect/crossdomain.go:137, agents/architect/crossdomain.go:160.
  20. Runtime wiring gap: TUI bootstrap currently creates Guide/bus/session infra but does not instantiate/
     start/register Architect (or Orchestrator), so architect path is not end-to-end active in app runtime:
     cmd/tui.go:74, cmd/tui.go:85, agents/guide/registry.go:151.

  Validation status

  - go test ./agents/architect passes.
  - go test -race ./agents/architect passes.
  - Coverage command failed in this environment (package testmain: cannot find package), so I could not
    produce coverage percentage.

