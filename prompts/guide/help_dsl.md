# Guide DSL Syntax

The Guide supports multiple DSL formats for routing:

---

## Quick Reference

| Command | Purpose | Example |
|---------|---------|---------|
| @guide <query> | Intent-based routing | @guide What history did we use? |
| @to:<agent> <query> | Direct route to agent | @to:arch What patterns? |
| @from:<agent> <response> | Response from agent | @from:arch {results} |
| @archive <query> | Direct to Archivalist | @archive What errors? |
| @agent:intent:domain | Full DSL | @arch:recall:history |

---

## 1. Intent-Based Routing

```
@guide <natural language query>
```

Uses LLM classification to determine the best agent and intent.

**Examples:**
```
@guide What history have we used for authentication?
@guide Log this failure: timeout on API call
@guide What agents are registered?
```

---

## 2. Direct Routing

```
@to:<agent> <query>
```

Routes directly to the specified agent without classification.

**Examples:**
```
@to:arch What history did we use?
@to:archivalist Store this failure
@to:guide What agents are available?
```

---

## 3. Response Routing

```
@from:<agent> <response>
```

Routes a response from an agent back to the requester.

**Examples:**
```
@from:arch {"history": [...]}
@from:guide {"agents": ["archivalist", "guide"]}
```

---

## 4. Action Shortcuts

```
@archive <query>
```

Shortcut for direct routing to the Archivalist.

**Examples:**
```
@archive What history did we use for auth?
@archive Log failure: connection timeout
```

---

## 5. Full DSL

```
@<agent>:<intent>:<domain>[?<params>][{<data>}]
```

Explicit agent, intent, and domain specification.

| Component | Required | Description |
|-----------|----------|-------------|
| @<agent> | Yes | Target agent (arch, guide) |
| :<intent> | Yes | What to do |
| :<domain> | Yes | Category |
| ?<params> | No | Key=value pairs |
| {<data>} | No | JSON payload |

**Examples:**
```
@arch:recall:history?scope=auth&limit=5
@arch:store:history{approach:"X",outcome:"Y"}
@guide:status:agents
```

---

## Shortcuts

### Agent Shortcuts
| Short | Full |
|-------|------|
| arch | archivalist |
| g | guide |
| lib | librarian |

### Intent Shortcuts
| Short | Full |
|-------|------|
| r | recall |
| s | store |
| c | check |
| d | declare |
| f | find |
| l | locate |
| ? | help |
| ! | status |

### Domain Shortcuts
| Short | Full |
|-------|------|
| h, hist | history |
| p, plan | planning |
| dec | decisions |
| l, learn | learnings |
| sys | system |