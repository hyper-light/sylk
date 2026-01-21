# Claude Code Project Rules

## Sylk - Multi-Agent Coding Application

### BANNED: SQLite Extensions (Requires Explicit User Authorization)

**THE FOLLOWING ARE BANNED. DO NOT implement, install, suggest, or add ANY of the following UNLESS the user EXPLICITLY authorizes them in the conversation:**

1. **FTS5** (Full-Text Search 5) - SQLite virtual tables using `CREATE VIRTUAL TABLE ... USING fts5`
2. **FTS4** - Legacy full-text search
3. **FTS3** - Legacy full-text search
4. **R-Tree** - Spatial indexing extension
5. **JSON1** - JSON functions (unless already bundled with driver)
6. **Any external SQLite extension** - Including but not limited to:
   - sqlite-vec
   - sqlite-vss
   - spatialite
   - Custom loadable extensions via `load_extension()`

### Authorized Search Technologies

| Technology | Purpose | Status |
|------------|---------|--------|
| **Bleve** | Full-text search | PLANNED (not yet implemented) |
| **VectorDB (HNSW)** | Semantic/vector search | IMPLEMENTED |
| **SQLite** | Pure relational storage | IMPLEMENTED |

### Storage Architecture

```
Full-Text Search  →  Bleve (github.com/blevesearch/bleve/v2)
Semantic Search   →  VectorDB with HNSW (core/vectorgraphdb/)
Relational Data   →  SQLite (pure, no extensions)
```

### Rationale

- SQLite is used for **relational storage only**
- Full-text search is handled by **Bleve** (dedicated search engine)
- Semantic search is handled by **custom HNSW implementation**
- This separation provides better performance, maintainability, and control

### Violation Response

If you encounter a need for FTS5 or SQLite extensions:
1. **STOP** - Do not implement, add, or suggest adding them
2. **ASK** - Explicitly ask the user: "This would require [FTS5/extension]. Do you explicitly authorize this?"
3. **WAIT** - Do not proceed until the user explicitly says "yes" or "authorized"
4. **REMOVE** - If you find existing FTS5/extension code, flag it for removal


When implementing:
1. **NEVER** Use magic numbers, we derive from the data
2. **ALWAYS** Keep cyclomatic complexity less than 4
3. **NEVER** Allow for untracked goroutines
4. **NEVER** Allow for unbounded growth
5. **NEVER** Allow for drops, memory-leaks, or race conditions
6. **ALWAYS** Implement the most correct, robust, performant implementation and DO NOT WEIGHT COMPLEXITY
7. **ALWAYS** Use modern go structures (Go 1.25+).

**"Explicit authorization" means the user must specifically say they authorize the extension. Implied or assumed permission is NOT sufficient.**

---

*Last updated: 2025-01-18*
