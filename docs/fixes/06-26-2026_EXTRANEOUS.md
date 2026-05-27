  Extraneous Layers

  1. Direct Guide Routing Inside Consults
     consult_peer posts a consultation claim, then also routes a synchronous RouteRequest
     through Guide in runLegacyConsultWait (agents/shared/cross_pipeline_skills.go:573, agents/
     shared/cross_pipeline_skills.go:676). That makes Guide transport a second authority beside
     the board. If Guide finishes but the claim path does not, or vice versa, UI hangs.
  2. Inter-Agent Branch Projection
     WithInterAgentBranchMessage and InterAgentToolEvent manufacture nested rows from tool
     args/metadata (agents/shared/inter_agent_tool_event.go:26). That is why errant self rows
     appear: the UI is rendering inferred routing metadata, not the claim graph.
  3. Started/Completed Peer Invocation Artifacts
     EmitPeerInteractionStarted creates consult_started / challenge_started artifacts (agents/
     shared/peer_interaction_artifact.go:25). The bridge then maps child claim IDs to started
     artifact IDs and separately completes rows via completePeerInvocationForClaim (ui/bridge/
     claims.go:1033, ui/bridge/claims.go:1088). That is a shadow lifecycle over the claim
     lifecycle.
  4. Tool Runtime Lifecycle
     The tool runtime records tool_started / tool_completed artifacts (core/toolruntime/
     runtime.go:387, core/toolruntime/runtime.go:418). Yielded tools intentionally skip the
     normal completion artifact (core/toolruntime/runtime.go:518), so another helper has to
     synthesize completion later (agents/shared/consult_resume_completion.go:17). Miss that
     helper and the row spins forever.
  5. Continuation Store
     AwaitConsultsOrYield persists another claim type, another index, another deadline watcher,
     and another orphan map (agents/shared/consult_continuations.go:316, agents/shared/
     consult_continuations.go:1305). It waits on ConsultResolvedDelta, not directly on the
     consultation claim’s validations/testaments. That is the wrong key.
  6. Synthetic Resolution Deltas
     The system converts testament/validation outcomes into ConsultResolvedDelta so waiting
     agents can resume (agents/shared/claims_intake.go:384). This is another completion signal.
     It can be dropped, orphaned, cancelled, or delivered without the UI row closing.
  7. Activity Fabric Consult/Challenge Events
     consult_emitted, challenge_emitted, consult_response, and related ambient-context events
     are useful observations, but they are also being treated as workflow state. They should
     inform agents, not govern terminal semantics.
  8. UI Cycle Resolver And Orphan Heuristics
     The bridge tracks open cycles by open claims plus in-flight started artifacts (ui/bridge/
     cycle_resolver.go:12). The renderer then guesses orphaned pending rows from age and
     sibling completion (ui/chat/tool_render.go:1279). That is compensating for missing
     authoritative terminal events.
  9. Prompt-Enforced Process
     Architect prompts force recall/consult sequencing before planning (prompts/architect/
     system.md:35, prompts/architect/system_protocol.md:3). This makes the model repeat
     consults or refuse phases when the real state machine is unclear. Prompts are being used
     as process control.
  10. User-Facing Architect Uses Legacy Blocking
     The architect conversation path explicitly avoids continuations and falls back to legacy
     synchronous waits (agents/architect/planner_conversation.go:207). So if Guide routing or
     the peer response path stalls, the user-facing planning phase stalls too.