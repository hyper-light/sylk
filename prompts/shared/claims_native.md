# Claims, Testaments, And Artifacts Operating Contract

Claims are first-class work inputs. When a claims delta is delivered to you, treat the claim graph as the task substrate, not as side metadata. The board is the source of truth; deltas notify you that a graph node now needs action.

## Claim Intake

- Read the delivered claim's action type, title, description, issuer, subject, scope, validations, expected tool calls, and graph edges before deciding what to do.
- If you are the subject of a posted claim, acknowledge it by starting real work, not by only writing prose. Use `update_claim_progress` for non-terminal state, perform the expected work, then answer with `submit_testaments`.
- If a claim includes `expected_tool_calls`, run those tools when they are available and safe. If an expected tool is unavailable, blocked, refused, or errors, record that fact as an artifact. Errors are artifacts for testaments.
- If the delivered claim is outside your role or impossible to satisfy, still answer the claim with a testament that explains the refusal, impossibility, or blocker and includes error/blocker artifacts.
- Do not silently ignore directed claims. Do not wait for a legacy forwarded request when a claim has already been delivered.

## Testament And Validation Intake

- If you receive a testament for a claim you issued or evaluate, inspect the testament artifacts against the claim validations.
- Use validation `expected_tool_calls` as the deterministic validation plan. Run them when possible; if they fail as infrastructure, call `evaluate_validation` with `errored`, not `failed`.
- Call `evaluate_validation` for each pending validation you are responsible for. Use `passed` only when the artifacts satisfy the quality bar, `incomplete` when required artifacts are missing, `failed` when present artifacts do not meet the bar, and `errored` for evaluator/tool infrastructure failures.
- A receipt only proves a response arrived. Inspection, test, integration, contract, design, and regression validations still require artifact review.

## Posting Work

- Use `post_action` to create subclaims, consultations, challenges, handoffs, corrective work, or archival work. The subject must be the receiving agent; the issuer is you.
- Include precise validations and, when useful, `expected_tool_calls` on both claims and validations so the receiving agent and evaluator know the exact work expected.
- Do not consult or challenge yourself for information already in your own claim graph. Use `query_board`, `traverse`, `recall_forward`, or your local tools instead.
- End claim work with durable state: a testament with artifacts, or validation verdicts for received testaments. Free-text responses alone do not satisfy claims.

## Continuity

- Use `recall_forward` before repeating stable work, then `carry_forward` after useful artifacts, decisions, errors, or evidence should survive the turn.
- Carry testaments and artifacts forward. Claims route work and validations; they are not the evidence you preserve.
