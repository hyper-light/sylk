# Consultation Depth And Deliberation Control

## Overview

Sylk now applies explicit deliberation pressure to nested inter-agent consults and to Academic's native `web_search` behavior.

The goal is not to hide work, flatten work, or prevent deep reasoning outright. The goal is to make additional expansion progressively harder to justify when it is no longer producing enough marginal value.

This document describes the **current shipped implementation**:

- the consultation admission model in `agents/shared/consultation_deliberation.go`
- the Academic native web-search learner in `agents/academic/web_search_learning.go`
- the search episode accounting that feeds the learner in `agents/academic/execute_research.go`

It intentionally distinguishes between:

- what is already implemented
- what is still prior-shaped / heuristic
- what is not yet covered

## Design Goals

The system is trying to prevent three failure modes:

1. Recursive consult chains that keep broadening instead of synthesizing.
2. Repeated consults to the same target with only superficial question changes.
3. Academic native search thrash, where the agent keeps issuing search after search without increasing grounded evidence.

The controlling principle is:

> Allow more expansion only when the expected marginal value of the next action is at least as large as its deliberation cost.

## Scope

### Currently Covered

The shared consult admission gate is currently wired into consult entrypoints for:

- Global Inspector
- Architect
- Academic
- Engineer
- Librarian
- Archivalist

Academic also has an adaptive native `web_search` learner.

### Not Currently Covered

- Pipeline Inspector consult/challenge/correction paths are not yet routed through this consult gate.
- Pipeline protocol actions such as `challenge_agent`, `request_correction`, `handoff_next`, and `handoff_to_ot` are intentionally not treated as ordinary consults.
- The consult admission model is adaptive, but it is not yet a fully learned hierarchical posterior the way Academic `web_search` is. Today it combines learned branch reward with fixed structural priors.

## Shared Consultation Admission

### State Model

The consult gate uses two layers of state.

### 1. Route-local snapshot

Each consult-carrying route can propagate a metadata snapshot under the key:

- `consultation_deliberation`

That snapshot currently contains:

- `root_correlation_id`
- `root_agent_id`
- `root_agent_type`
- `current_depth`
- `research_depth`

This is how nested consult depth and root ownership propagate from parent to child.

### 2. Root-correlation ledger

In process memory, the system keeps a ledger per `root_correlation_id`.

Each ledger tracks:

- total consult count
- cumulative reward
- per-target consult count
- per-target reward
- a recent window of consult observations

Important implementation details:

- the ledger is currently process-local via `sync.Map`
- it is keyed by root correlation
- the recent observation window is capped at the last `24` observations

### Inputs To Admission

When `AdmitConsultation(...)` runs, it computes:

- `target`
- `query`
- effective `research_depth`
- current nested depth from metadata
- root-branch historical stats from the ledger

The query is normalized by:

- lowercasing
- stripping punctuation to spaces
- dropping one-character tokens
- deduplicating tokens
- sorting tokens lexically

The resulting canonical token set is used for Jaccard similarity against prior recent observations for the same target.

### Research Depth Credit

Consult admission currently gives a fixed prior credit based on effective research depth:

| Research Depth | Credit |
| --- | ---: |
| `minimal` | `0` |
| `quick` | `1` |
| `standard` | `sqrt(2)` |
| `deep` | `sqrt(3)` |
| `comprehensive` | `2` |

This is implemented in `consultationDepthCredit(...)`.

Interpretation:

- deeper requested research does not remove penalties
- it enlarges the tolerated budget before penalties begin to accumulate

### Similarity And Novelty

For the next consult attempt:

- `similarity` = maximum Jaccard similarity against recent same-target consult fingerprints
- `novelty = clamp01(1 - similarity)`

Where:

- identical or near-identical question fingerprints push novelty toward `0`
- materially new questions push novelty toward `1`

### Learned Reward Terms

The ledger accumulates reward from prior successful consults on the same root branch.

Two reward terms are used:

- `globalReward`
- `targetReward`

These are updated only when a consult:

- was allowed
- completed successfully
- produced non-empty signal

The recorded reward equals the observation's novelty for that attempt.

So the current reward model is:

- novel consults that produce useful output increase future tolerance
- repetitive consults do not earn more budget

### Budget Equations

For the next consult attempt:

- `nextDepth = currentDepth + 1`
- `consultCount = totalConsultsSoFar + 1`
- `targetCount = priorConsultsToSameTarget + 1`

The current budgets are:

```text
depthBudget  = 1 + priorCredit + globalReward
volumeBudget = 1 + priorCredit + distinctTargets + globalReward
repeatBudget = 1 + (priorCredit / 2) + targetReward
```

Interpretation:

- depth budget expands with deeper requested research and with demonstrated branch value
- volume budget also expands when the branch is genuinely consulting multiple distinct targets
- repeat budget is intentionally tighter than the others

### Penalty Equations

The model computes excess over each budget:

```text
depthExcess  = max(0, nextDepth    - depthBudget)
volumeExcess = max(0, consultCount - volumeBudget)
repeatExcess = max(0, targetCount  - repeatBudget)
```

Question redundancy adds an additional penalty:

```text
questionPenalty = similarity * targetCount
```

The total current penalty is:

```text
penalty =
    depthExcess^2 +
    volumeExcess^2 +
    repeatExcess^2 +
    questionPenalty
```

This gives the model a convex shape:

- going slightly beyond budget is tolerated
- repeatedly exceeding budget becomes expensive quickly
- same-target repetition gets worse faster because it pays both repeat excess and question redundancy

### Expected Gain

The current expected-gain estimate is:

```text
expectedGain = novelty * (1 + priorCredit + globalReward + targetReward)
```

Interpretation:

- the next consult must be both novel and plausibly valuable
- deeper research can justify more exploration
- a branch that has historically produced useful answers earns more headroom
- a target that has historically produced useful answers earns more headroom

### Admission Rule

The current admission decision is:

```text
allow if penalty <= expectedGain
block if penalty > expectedGain
```

If blocked, the system returns guidance that tells the agent to do one of:

- synthesize current evidence
- materially change the question
- switch to a more informative target

In other words, the model is trying to make overthinking economically irrational rather than structurally impossible.

### Outcome Learning

When a consult finishes, the ledger updates only if the outcome produced signal.

The current signal test is intentionally simple:

- non-empty string
- non-empty list
- non-empty map
- or any other non-nil structured payload

If the consult was successful and produced signal:

- reward = the novelty that was recorded at admission time

Otherwise:

- reward = `0`

This means the current consultation learner is conservative:

- it does not try to estimate subtle quality
- it only learns whether novel consults tended to produce something real

## Academic Native Web Search

Academic's native `web_search` uses a separate online marginal-value learner.

This is not the same as the consult gate.

It is a depth-conditioned, ordinal-search learner that tries to answer:

> Given the requested research depth and the current evidence frontier, is the next native search still worth it?

### Learner Structure

The learner keeps two layers of ordinal reward statistics:

- global stats by search ordinal
- per-research-depth stats by search ordinal

So for ordinal `n`, the predictor can combine:

- a prior
- the global history for ordinal `n`
- the depth-specific history for ordinal `n`

This is a lightweight hierarchical model.

### Research Depth Rank

Academic maps effective research depth to an integer rank:

| Research Depth | Rank |
| --- | ---: |
| `minimal` | `0` |
| `quick` | `1` |
| `standard` | `2` |
| `deep` | `3` |
| `comprehensive` | `4` |

This rank shapes both the prior reward expectation and the marginal cost curve.

### Prior Mean

For the next search ordinal `ordinal`, the prior mean is:

```text
priorMean = clamp01((rank + 1) / (rank + ordinal + 1))
```

Properties:

- early searches have higher prior value
- deeper requested research widens the useful plateau
- later searches decay in expected value

### Frontier Value

Academic does not judge the next search from ordinal alone. It also scores the current evidence frontier.

The current frontier tracks:

- `TotalSearches`
- `RepeatedSearches`
- `UniqueResults`
- `UniqueDomains`
- `GroundedSources`
- `LocalRewardMean`

Here `RepeatedSearches` is not a generic duplicate counter. In the current implementation, a search episode is marked repeated only when the same normalized search fingerprint has been seen before **and** the branch has not increased its grounded-source count since that earlier attempt.

The frontier value is:

```text
novelUnits        = max(UniqueResults, UniqueDomains)
noveltyRate       = clamp01(novelUnits / TotalSearches)
authorityGap      = 1 / (1 + GroundedSources)
redundancyPenalty = 1 / (1 + RepeatedSearches)
localReward       = clamp01(LocalRewardMean)

frontierValue =
    clamp01(((noveltyRate + authorityGap + localReward) / 3) * redundancyPenalty)
```

Interpretation:

- more unique results and domains increase the chance that more searching is still worthwhile
- more grounded sources reduce the need for further exploratory search
- repeated searches reduce the value of more search
- local reward reflects whether recent search episodes actually paid off

### Predicted Marginal Value

For a given depth and ordinal:

```text
predictedReward =
    (priorMean * priorWeight + globalReward + depthReward) /
    (priorWeight + globalCount + depthCount)

predictedMarginalValue = clamp01(predictedReward * frontierValue)
```

With the current implementation:

- `priorWeight = 1`
- `globalReward` and `depthReward` are cumulative observed rewards at that ordinal

### Marginal Cost

The cost of the next search is:

```text
marginalCost = clamp01(ordinal / (ordinal + rank + 1))
```

Properties:

- later searches are more expensive
- deeper research lowers cost more gradually, not infinitely

### Search Admission Rule

Academic allows the next native search only if:

```text
predictedMarginalValue >= marginalCost
```

Each turn, Academic computes how many additional native searches remain justified. It then:

- reduces `web_search.MaxUses`, or
- removes `web_search` from the tool list entirely

So the model does not merely warn. It changes the available tool budget.

### Search Episode Reward

The learner is trained online from concrete search episodes.

Each episode records:

- ordinal
- normalized search fingerprint
- query
- action
- URL
- whether it repeated without new grounding
- novel result count
- novel domain count
- grounded count

An episode currently gets reward `1` if it produced any of:

- a grounded source
- a novel domain
- a novel result

Otherwise reward is `0`.

That reward is then observed at the corresponding ordinal for:

- the global learner
- the effective research depth learner

## Why These Shapes

The current system uses different control shapes for different failure modes.

### Consults

Consults are controlled by a convex excess-over-budget penalty because the main failure mode is recursive over-expansion.

That shape is appropriate because:

- first and second consults can be legitimate
- repeated escalation past the branch budget should become expensive quickly
- same-target repetition should hurt more than breadth

### Academic Native Search

Academic native search is controlled by marginal value versus marginal cost because the main failure mode is search thrash rather than pure nesting.

That shape is appropriate because:

- deeper requested research should justify more search
- repeated productive search can remain allowed
- repeated unproductive search should shut itself down

## Current Limitations

The current implementation is intentionally honest about its limits.

1. The shared consult gate is adaptive, but it is not yet a fully learned hierarchical posterior.
   It still uses fixed structural priors such as research-depth credit and squared excess penalties.

2. The root ledger is process-local.
   Today it is not persisted durably across process restarts.

3. Consultation reward is intentionally simple.
   It currently uses novelty-backed signal detection, not deeper post hoc answer quality evaluation.

4. Similarity uses token-level Jaccard over normalized fingerprints.
   This is fast and robust, but it is not a full semantic redundancy model.

5. Academic's learner is currently specific to native `web_search`.
   It does not yet govern every other expansion behavior.

6. Pipeline consult/challenge routes are still separate.
   The ordinary consult gate is not yet centrally enforced across every possible inter-agent route kind.

## Practical Consequences

With the current system:

- the first genuinely novel consult on a branch is usually easy to admit
- deeper consults remain possible when the branch is high-value and still novel
- repeated same-target consults get blocked much sooner
- Academic can still search broadly for deeper research, but only while the learner believes the next search is still paying off
- search thrash without novelty, domains, or grounding will remove `web_search` from the available tool set

## Files

- `agents/shared/consultation_deliberation.go`
- `agents/shared/consultation.go`
- `agents/inspector/global/consultation_skills.go`
- `agents/academic/web_search_learning.go`
- `agents/academic/execute_research.go`
- `agents/academic/tool_loop.go`
- `agents/architect/consultation_bus.go`
- `agents/engineer/consultation.go`
- `agents/librarian/consultation.go`
- `agents/archivalist/consultation.go`
