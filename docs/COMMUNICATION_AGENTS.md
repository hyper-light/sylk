  Principle                                                                 
                                                                            
  Decisions are emergent from work, not prerequisites for it.               
                                                                            
  Every pipeline agent already makes typed decisions through skills they    
  routinely invoke (detect_test_harness picks a framework,
  discover_project_tools infers a build backend, component_create commits to
   a UI framework, define_criteria shapes a validation strategy). The       
  manifest's job is to make those implicit decisions loud — to peers, to the
   inspector, and to the audit trail — without ever telling an agent "no,
  you can't do your work."

  The current JIT gate violates this principle. It treats the manifest as a 
  contract the agent must satisfy before acting. The redesign treats the
  manifest as a coordination surface the agent populates by acting — and as 
  an invitation surface the agent uses to collaborate or challenge.

  How each agent gets taught

  Three things need to happen in concert: (1) skills auto-publish typed     
  decisions as side effects of their normal work, (2) prompts teach the
  collaboration/challenge cycle around the manifest, (3) the existing       
  peer-challenge primitive carries decision context so disagreements are
  concrete and resolvable.

  Pipeline tester

  Auto-publish points (no new skills; instrument existing ones):            
   
  Existing skill: detect_test_harness                                       
  What it already infers: harness.FrameworkID, harness.RecommendedOutputs
  Auto-declares as: test_framework={pytest,go-test,…} and                   
    test_layout={tests/, alongside-source, …} at scope {language, path},
    confidence Tentative
  ────────────────────────────────────────                                  
  Existing skill: plan_tests                                                
  What it already infers: structured plan with fixture pattern, mock usage  
  Auto-declares as: fixture_strategy, mock_library at the planned-output    
    scope, confidence Tentative
  ────────────────────────────────────────
  Existing skill: write_test                                                
  What it already infers: the file actually got written
  Auto-declares as: promotes the existing test_framework declaration to     
    Committed (work has happened; intent is now reality)
  ────────────────────────────────────────
  Existing skill: finalize_pipeline                                         
  What it already infers: the verification artifact ratifies the framework
  Auto-declares as: promotes to Consensus when the artifact accepts (or     
    surfaces failure if the artifact and the declared framework diverge)

  No gate. write_test always succeeds. The manifest just gets richer as the 
  tester does its job. By the time finalize_pipeline runs, the manifest has
  a full audit trail: tester planned with X, wrote with X, suite ratified X.
                  
  Prompt teaching — replaces the current "query first / declare second /    
  write third" ritual with:
                                                                            
  ▎ The system records your tooling choices automatically as you work. Other
  ▎  parallel pipelines can see what you've chosen and align with it; you 
  ▎ can see what they've chosen via query_decisions.                        
  ▎               
  ▎ Collaborate: before picking a test framework or layout, run             
  ▎ query_decisions(domain="test_framework", scope={…}) to see if a peer has
  ▎  already committed to one for this surface. If they have, adopt it —    
  ▎ that's how parallel pipelines stay coherent.
  ▎
  ▎ Challenge: if query_decisions returns a winner you genuinely disagree   
  ▎ with (because of new evidence, a project convention they missed, etc.), 
  ▎ use challenge_agent against the decision's author with the decision ID  
  ▎ in your evidence. Be specific about why the alternative is better. The 
  ▎ challenge target receives your decision context and can yield, defend,
  ▎ or escalate.
  ▎
  ▎ You don't need to call declare_decision for routine tooling — the system
  ▎  does it for you when you actually use the framework. Use 
  ▎ declare_decision directly only when you want to broadcast an intent     
  ▎ before you've started writing code (e.g., a planning-only turn that 
  ▎ hasn't yet authored tests).

  Engineer

  Auto-publish points (every one of these is an existing skill with an      
  inferred-but-undeclared typed decision):
                                                                            
  Existing skill: discover_project_tools                                    
  What it already infers: the existing build backend, package manager, type
    system                                                                  
  Auto-declares as: build_backend, package_manager, type_system at scope
    {language, path}, confidence Hint (this is observation of  state, not a
    commitment)                                                             
  ────────────────────────────────────────
  Existing skill: discover_code_patterns                                    
  What it already infers: the project's module layout and import strategy
  Auto-declares as: module_layout, import_strategy at {path}, confidence    
  Hint            
  ────────────────────────────────────────
  Existing skill: format (when first applied)                               
  What it already infers: the formatter that ran
  Auto-declares as: code_style at {language, path}, confidence Committed    
    (formatter actually mutated files)
  ────────────────────────────────────────
  Existing skill: lint                                                      
  What it already infers: the linter backend invoked
  Auto-declares as: linter_backend, confidence Committed                    
  ────────────────────────────────────────
  Existing skill: write_pipeline_file                                       
  What it already infers: the file's location encodes module layout
  Auto-declares as: promotes any prior module_layout Hint to Committed      
  ────────────────────────────────────────
  Existing skill: handoff/finalize                                          
  What it already infers: the implementation artifact ratifies all of the
    above                                                                   
  Auto-declares as: promotes to Consensus at acceptance

  No gate anywhere. The engineer can write code freely. The manifest        
  accumulates the ground truth.
                                                                            
  Prompt teaching — add a short section to the engineer's system prompt:    
   
  ▎ Your discovery skills (discover_project_tools, discover_code_patterns)  
  ▎ automatically broadcast what you observed about the project's tooling 
  ▎ and layout to parallel pipelines. Your mutation skills (format, lint,   
  ▎ write_pipeline_file) automatically broadcast what you committed to.
  ▎
  ▎ Collaborate: before adding a new dependency or restructuring code,      
  ▎ query_decisions(domain="build_backend"|"module_layout"|…) to confirm 
  ▎ peer pipelines haven't already committed to a different choice. Adoption
  ▎  is the default; divergence requires justification.
  ▎
  ▎ Challenge: if a peer (typically the architect's plan, or another        
  ▎ engineer pipeline) committed to a tool/layout you believe is wrong for 
  ▎ this codebase, use challenge_agent against the declaration's author.    
  ▎ Carry the decision ID + your concrete evidence (file paths, prior 
  ▎ conventions, build failures). The challenge is structured negotiation,
  ▎ not freeform disagreement.

  Designer

  Auto-publish points:                                                      
   
  ┌──────────────────────┬──────────────────────────────────────────────┐   
  │   Existing skill    │               Auto-declares as                │ 
  ├─────────────────────┼───────────────────────────────────────────────┤ 
  │ component_search    │ discovered ui_framework, component_library at │ 
  │                     │  {path}, confidence Hint                      │ 
  ├─────────────────────┼───────────────────────────────────────────────┤   
  │                     │ ui_framework, state_management,               │ 
  │ component_create    │ design_token_source, component_structure at   │   
  │                     │ {path}, confidence Committed                  │ 
  ├─────────────────────┼───────────────────────────────────────────────┤   
  │ token_validate /    │ confirms or promotes design_token_source      │
  │ token_suggest       │                                               │   
  ├─────────────────────┼───────────────────────────────────────────────┤
  │ a11y_audit /        │ accessibility_baseline (resolved from         │   
  │ contrast_check      │ criteria + observation), confidence Committed │   
  └─────────────────────┴───────────────────────────────────────────────┘
                                                                            
  Prompt teaching mirrors the engineer's, scoped to UI domains. Designers   
  running in parallel pipelines on the same product will see each other's
  framework + state-management commitments and avoid building one feature in
   React-with-Redux while another peer builds in React-with-Context.

  Pipeline inspector                                                        
   
  Auto-publish points — the inspector is special because it's the audit     
  authority; its declarations carry weight:
                                                                            
  ┌────────────────────┬─────────────────────────────────────────────────┐
  │   Existing skill   │                Auto-declares as                 │
  ├────────────────────┼─────────────────────────────────────────────────┤
  │                    │ validation_strategy, acceptance_criteria_format │
  │ define_criteria    │  at {task scope}, confidence Committed (or      │
  │                    │ Consensus if architect-charted)                 │  
  ├────────────────────┼─────────────────────────────────────────────────┤
  │                    │ (consumes — queries test_framework,             │  
  │ validate_criteria  │ module_layout, etc., to check that what was     │  
  │                    │ built matches what was declared)                │
  ├────────────────────┼─────────────────────────────────────────────────┤  
  │ grade_task_quality │ (consumes — checks for unresolved Tentative     │
  │                    │ decisions blocking acceptance)                  │  
  └────────────────────┴─────────────────────────────────────────────────┘
                                                                            
  Inspector also gets a new audit-time skill (the only genuinely new skill  
  in the proposal): inspect_decision_conflicts(scope). It returns all
  in-flight decision conflicts in the inspected scope so the inspector can  
  decide whether to:

  - Accept — divergent peers are working on different subprojects, conflict 
  is benign.
  - Request correction (existing skill) — one pipeline must reconcile to the
   other; the request now carries decision IDs.                             
  - Escalate — both peers are wrong; route to architect for charter
  ratification.                                                             
                  
  Prompt teaching for the inspector:                                        
                  
  ▎ Before grading or accepting a pipeline's work, call                     
  ▎ inspect_decision_conflicts(scope). Any unresolved cross-pipeline 
  ▎ decision conflicts in your scope are quality issues — even if the work  
  ▎ itself looks good in isolation, it may be incompatible with a parallel 
  ▎ pipeline's commitments. Use request_correction with the decision ID(s)
  ▎ in the evidence to drive resolution.

  How collaboration and challenge are made first-class                      
   
  Two existing primitives gain decision-awareness:                          
                  
  challenge_agent carries decision context                                  
                  
  The skill's existing parameters are reason, request, required_output,     
  references. Add an optional targeting_decision_id field. When present, the
   receiving agent's tool loop treats this as "the challenger believes my   
  prior decision is wrong" — not a generic peer challenge but a structured
  ask to defend or yield.

  The challenged agent's validate_work response gets a corresponding        
  optional field: decision_resolution ∈ {defend, yield, escalate}. Defend
  means "I have evidence the decision still stands, here's what." Yield     
  means "you're right, I'm withdrawing — go declare your alternative as
  Committed and I'll align." Escalate means "we both have evidence; this
  needs the architect or human."

  This makes peer disagreement structured negotiation about a typed         
  decision, not vague back-and-forth. Two parallel testers picking different
   frameworks would now have a precise, fast resolution path: tester B      
  challenges decision A, tester A reads the evidence, A yields, B's
  framework becomes Committed, A's pipeline adopts it on next manifest read.

  Prompts make collaboration and challenge symmetric

  Every pipeline agent's prompt gains a short "Cross-pipeline coordination" 
  section with the same shape:
                                                                            
  ▎ The system surfaces what your peer pipelines have decided. Two          
  ▎ responsibilities:
  ▎                                                                         
  ▎ Collaborate: query the manifest before making a decision in a           
  ▎ coordinable domain. Adopt peer decisions whenever they're compatible 
  ▎ with your task. Adoption is cheap; divergence has integration cost.     
  ▎               
  ▎ Challenge: when you genuinely disagree with a peer decision (because of 
  ▎ evidence they didn't have, a project convention they missed, or a 
  ▎ downstream constraint they didn't model), use challenge_agent against   
  ▎ the decision's author with the decision ID. State your alternative and 
  ▎ your evidence. Don't go silent and diverge — divergence without
  ▎ disclosure breaks the project later.

  The inspector's prompt adds a third clause specific to its role:          
   
  ▎ Audit: at finalize time, call inspect_decision_conflicts(scope). Open   
  ▎ conflicts in your scope are blocking quality issues. Drive resolution 
  ▎ via request_correction with the decision ID, or request_override to the 
  ▎ architect when both peers have valid grounds.

  Cleanup of the broken gate                                                
   
  Two surgical changes that ship before any of the above lands:             
                  
  1. Delete requireTestFrameworkDecision and its write_test call site. The  
  race condition disappears because the gate disappears. The 543-second
  freeze you observed becomes impossible by construction.                   
  2. Update prompts/tester/pipeline_task_system.md — remove the "query first
   / declare second / write third" ritual section. Replace with the         
  collaborate/challenge teaching above.
                                                                            
  After those two, the manifest is purely additive: agents can use it via   
  the existing query_decisions / declare_decision skills, and conflicts
  surface through the existing peer-challenge primitive (newly augmented    
  with decision context). No agent is ever blocked on its primary work.

  What the audit trail looks like end-to-end

  Pipeline 1's tester runs detect_test_harness for a Python project at      
  services/api/. Side-effect: manifest gains test_framework=pytest @ 
  {language: python, path: services/api/} confidence Tentative, author      
  tester-pipeline-1. Then plan_tests runs — manifest gains
  fixture_strategy=pytest-fixtures confidence Tentative. Then write_test
  runs — the existing test_framework declaration promotes to Committed. Then
   finalize_pipeline accepts the artifact — Committed promotes to Consensus.

  Pipeline 2's tester starts 30 seconds later for services/billing/. Its    
  detect_test_harness queries the manifest first (the prompt taught it to).
  It sees pipeline 1's pytest at services/api/, recognizes the prefix       
  doesn't match services/billing/, proceeds. Picks pytest independently
  because the project conventions favor it. Auto-declares; the system
  detects Equivalent (matching value at non-overlapping scope), records
  corroboration without conflict. Both pipelines proceed coherently.

  Pipeline 3's tester for services/api/v2/ has a strong reason to use       
  unittest instead. Queries the manifest, sees the Committed pytest. Doesn't
   silently diverge. Issues challenge_agent against pipeline 1's tester with
   the decision ID and concrete evidence ("v2 must run in a sandbox that
  lacks pytest's plugin loader"). Pipeline 1's tester reads the challenge,
  examines the evidence, decides to yield: declares the v2 path scope
  explicitly with unittest, narrows its own pytest declaration to exclude
  services/api/v2/. Both pipelines now produce coherent test suites.

  This is the agentic interaction model — no gates, lots of declarations,   
  structured peer dispute when it matters.
                                                                            
  What I'd ship if you confirm                                              
   
  In strict order:                                                          
                  
  1. Remove the broken gate (delete requireTestFrameworkDecision, drop the  
  write_test precheck, drop the gate-related prompt section). The race goes
  away.                                                                     
  2. Auto-publish in the four pipeline agents' existing skills:
  detect_test_harness, plan_tests, write_test, discover_project_tools,      
  discover_code_patterns, format, lint, write_pipeline_file,
  component_search, component_create, token_validate/a11y_audit,            
  define_criteria. Each gets a small post-success block that constructs a
  typed scope from inputs already in hand and calls manifest.Declare with
  the appropriate confidence. Race-safe because there's no gate to fail; the
   declaration is fire-and-forget.
  3. Augment challenge_agent and validate_work with targeting_decision_id
  and decision_resolution fields. Wire through pipeline_protocol.go's       
  payload structs. Update prompts to mention the new fields when relevant.
  4. Add inspect_decision_conflicts(scope) to the inspector's skill set.    
  Returns the open conflicts, sorted by scope specificity.                  
  5. Update each pipeline agent's system prompt with the standardized
  "Cross-pipeline coordination" section: collaborate (query before deciding,
   adopt by default), challenge (specific peer disagreement is structured),
  audit (inspector only).                                                   
  6. Tighten the cache-flush race as a defensive measure: even though no
  gate depends on it anymore, the query_decisions skill could still race.   
  Switch the cache from "the gate" to "the eviction signal" — keep SQLite
  authoritative, use Ristretto only to decide when to drop a Tentative row  
  from query results, with an explicit tentativeAlive.Wait() after each
  Declare to make declarations immediately visible to subsequent queries
  within the same agent's loop.
