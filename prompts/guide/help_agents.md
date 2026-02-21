# Available Agents

Agents register with the Guide declaring their capabilities (what they handle) and constraints (what they require).

---

## Archivalist (@arch)

**ID**: archivalist
**Aliases**: arch
**Priority**: 100

### Capabilities

**Supported Intents**:
- recall: Query historical data
- store: Record new data
- check: Verify against history
- declare: Announce work intentions
- complete: Mark work as done

**Supported Domains**:
- history: Historical records, architectural patterns
- planning: Task breakdown, errors encountered
- decisions: Design decisions, choices made
- files: File states, modifications
- learnings: Lessons learned, insights
- intents: Work intentions, declarations

### Constraints

- **RetrospectiveOnly**: true
- **TemporalFocus**: past

The Archivalist ONLY handles queries about the PAST. Prospective queries (about what should/will be done) will be rejected.

### Example Queries

```
@arch:recall:history?scope=auth          # Get auth history
@arch:store:history{...}                  # Log a failure
"What history did we use for auth?"       # Natural language (past)
"What errors have we seen?"                # Natural language (past)
```

---

## Guide (@guide)

**ID**: guide
**Aliases**: (none)
**Priority**: 50

### Capabilities

**Supported Intents**:
- help: Request assistance
- status: Query system state

**Supported Domains**:
- system: System status, health
- agents: Agent registry, status

### Constraints

None - handles any temporal focus for its domains.

### Example Queries

```
@guide:status:agents                       # List registered agents
@guide:help:system                         # Get help
"What agents are registered?"              # Natural language
"How do I use the DSL?"                    # Natural language
```

---

## Registering New Agents

Agents register with:

```go
guide.RegisterAgent(&AgentRegistration{
    ID:      "my-agent",
    Name:    "My Agent",
    Aliases: []string{"ma"},
    Capabilities: AgentCapabilities{
        Intents: []Intent{IntentRecall, IntentStore},
        Domains: []Domain{DomainHistory},
    },
    Constraints: AgentConstraints{
        MinConfidence: 0.8,
    },
    Description: "Handles specific history",
    Priority:    75,
})
```