  The Architecture: Sonnet 4.5 as Reasoning Memory + SQLite as Extended Storage

  The Mental Model:

  ┌─────────────────────────────────────────────────────────────────────┐
  │                         AGENT QUERIES                                │
  │   "What patterns for rate limiting?"  "How did Session 2 fix X?"    │
  └─────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │                      QUERY SIMILARITY CACHE                          │
  │                                                                      │
  │   Is this query similar to one we've answered recently?             │
  │   YES (>95% similar) → Return cached response (HUGE token savings)  │
  │   NO → Continue to retrieval                                        │
  └─────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │                     CONTEXT RETRIEVAL (The Librarian)               │
  │                                                                      │
  │   SQLite + Embeddings = The Library Stacks                          │
  │   Fetch relevant "books": patterns, failures, insights, file states │
  │   Semantic search, not just category matching                       │
  │   Rank by: relevance × recency × importance                         │
  └─────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │                    SONNET 4.5 (The Reasoning Brain)                  │
  │                                                                      │
  │   1M token context window = "Books on your desk"                    │
  │   Receives: Query + Retrieved Context                               │
  │   Synthesizes: Relevant, coherent response                          │
  │   Understands nuance, connects dots, reasons about context          │
  └─────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
  ┌─────────────────────────────────────────────────────────────────────┐
  │                      RESPONSE CACHE                                  │
  │                                                                      │
  │   Cache (query_embedding → response)                                │
  │   Future similar queries get instant response                       │
  │   Invalidate when underlying context changes                        │
  └─────────────────────────────────────────────────────────────────────┘

  The Library Metaphor
  ┌─────────────────────┬──────────────────────────────────┬────────────────────────────────────────────┐
  │      Component      │         Library Analogy          │                  Function                  │
  ├─────────────────────┼──────────────────────────────────┼────────────────────────────────────────────┤
  │ Sonnet 4.5 context  │ Books on your desk               │ Currently "active" memory, can reason over │
  ├─────────────────────┼──────────────────────────────────┼────────────────────────────────────────────┤
  │ SQLite + embeddings │ Library stacks                   │ Long-term storage, retrieve when needed    │
  ├─────────────────────┼──────────────────────────────────┼────────────────────────────────────────────┤
  │ Query embedding     │ Book request slip                │ "I need information about X"               │
  ├─────────────────────┼──────────────────────────────────┼────────────────────────────────────────────┤
  │ Semantic retrieval  │ Librarian fetching books         │ Find relevant context from stacks          │
  ├─────────────────────┼──────────────────────────────────┼────────────────────────────────────────────┤
  │ Response cache      │ Frequently-asked-questions board │ Common queries get instant answers         │
  └─────────────────────┴──────────────────────────────────┴────────────────────────────────────────────┘
  The Flow for Real Queries

  Repeated Query (90% of cases):
  Agent: "What patterns exist for Django views?"

  1. Embed query
  2. Check cache: Found similar query from 10 minutes ago (98% similarity)
  3. Return cached response immediately

  Token cost: ~50 tokens (just the query)
  Latency: <10ms

  Novel Query:
  Agent: "How should I handle JWT refresh tokens in the context of our
          PostgreSQL migration, given the race condition Session 1 found?"

  1. Embed query
  2. Check cache: No similar query found
  3. Retrieve from SQLite:
     - JWT patterns from Session 1
     - PostgreSQL migration insights from Session 2
     - Race condition failure entry and resolution
     - Related file states
  4. Send to Sonnet 4.5:
     - "Here's the context: [retrieved chunks]"
     - "Question: [agent's query]"
  5. Sonnet synthesizes response connecting all the dots
  6. Cache response for similar future queries
  7. Return to agent

  Token cost: ~2000 tokens (retrieval + synthesis)
  Latency: ~500ms
  But: Next similar query costs ~50 tokens

  Token Savings Sources
  ┌───────────────────────┬───────────────────────────────────┬───────────────────────────────────┐
  │        Source         │             Mechanism             │              Savings              │
  ├───────────────────────┼───────────────────────────────────┼───────────────────────────────────┤
  │ Query caching         │ Similar queries → cached response │ 90%+ of queries                   │
  ├───────────────────────┼───────────────────────────────────┼───────────────────────────────────┤
  │ Semantic retrieval    │ Only fetch relevant context       │ 10x smaller context               │
  ├───────────────────────┼───────────────────────────────────┼───────────────────────────────────┤
  │ Cross-session sharing │ One agent learns, all benefit     │ Eliminates duplicate discovery    │
  ├───────────────────────┼───────────────────────────────────┼───────────────────────────────────┤
  │ Failure prevention    │ Proactive warnings                │ Avoid entire debugging cycles     │
  ├───────────────────────┼───────────────────────────────────┼───────────────────────────────────┤
  │ Summarization         │ Store summaries, not raw          │ Smaller storage, faster retrieval │
  └───────────────────────┴───────────────────────────────────┴───────────────────────────────────┘
  Memory Swapping: The Key Innovation

  When Sonnet's "desk" gets full:

  Sonnet context approaching limit...

  1. Identify "cold" memories:
     - Patterns not accessed in last N queries
     - Failures that have been resolved
     - Session insights that have been propagated

  2. Summarize and archive:
     - Generate summary of cold memories
     - Store full content in SQLite with embeddings
     - Keep summary in hot memory as "pointer"

  3. Swap in on demand:
     - Query comes in that needs archived context
     - Retrieve from SQLite
     - Swap into Sonnet's context
     - Process query
     - Can swap back out after

  Like virtual memory:
  - Hot pages (frequently accessed) stay in RAM
  - Cold pages get swapped to disk
  - Page fault (query needs cold data) triggers swap-in

  What This Means for the Architecture

  Components Needed:

  1. Query Embedding + Similarity Cache
    - Embed all queries
    - Cache (embedding → response) with similarity matching
    - Invalidation triggers when underlying data changes
  2. Semantic Retrieval Layer
    - SQLite with FTS5 for text search
    - Embedding vectors for semantic search
    - Hybrid ranking: BM25 + cosine similarity
  3. Sonnet 4.5 Integration
    - Not just for summarization
    - The reasoning engine that synthesizes responses
    - Maintains conversational context within its window
  4. Memory Management
    - Track "hotness" of each memory
    - LRU-like swapping to SQLite
    - Smart prefetching for likely-needed context
  5. Response Cache
    - Query → Response mapping
    - Similarity-based matching (not exact)
    - TTL + invalidation on context change

  The Critical Insight

  The archivalist is not a database - it's a RAG system with Sonnet as the brain.

  - Database stores raw memories
  - Embeddings enable semantic retrieval
  - Sonnet reasons over retrieved context
  - Caching eliminates repeated reasoning

  Token savings come from:
  1. Not repeating queries (cache)
  2. Not retrieving irrelevant context (semantic search)
  3. Not re-discovering patterns (cross-session sharing)
  4. Not re-debugging failures (failure propagation)
  5. Not reasoning twice about the same thing (response cache)
