Examine ARCHITECTURE.md and find the section with "Comprehensive Parallel Execution Order", then Wave 4 , Parallel Group 4K. For each item, in parallel (using sub-agents) find the matching architecture description for the item in ARCHITECTURE.md. Examine and understand it thoroughly. Implement according to ARCHITECTURE.md spec and use sub agents to accomplish the work required maximizing parallelism. After *EACH* submitem is created YOU MUST ALWAYS AND FOREVER:
- Generate a commit.
- Examine the cyclomatic complexity or the code your just wrote and ENSURE it is 4 or less.
- We do NOT allow functions over 100 lines. EVER.
- Generate test files per-file with THOROUGH testing that MUST cover happy path, negative path, failure, any race condition or deadlock handling, and edge cases.
- YOU MUST RUN THESE TESTS AND ALL TESTS MUST PASS DO NOT JUST FIX THE TESTS TO PASS YOU MUST FIX THE ACTUAL IMPLEMENTATION CODE.
- Generate another commit.
- You MUST mark EACH item done in TODO.md AFTER IT IS COMPLETED ALWAYS
- DO NOT SKIP ANY ITEM EVER FOR ANY REASON DO NOT DEFER 
- YOU MUST CHECK FOR RACE CONDITIONS, DEADLOCKS, AND MEMORY LEAKS. Do NOT accept any of these
- IF YOU ENCOUNTER A BUILD OR TEST FAILURE, EVEN IF PRE-EXISTING, YOU FIX IT.
- DO NOT generate your own mocks - specify interfaces for structs and use mockery - https://github.com/vektra/mockery to generate them.
- We do NOT defer, bypass, or skip work. EVER.
- If code or an existing implementation exists and does not match spec, we ALWAYS just modify it and DO NOT attempt to preserve legacy behavior.
- You MUST integrate correctly and fully with the security apis we specify in ARCHITECTURE.md, both for if sandboxing is enabled OR disabled.

Examine ARCHITECTURE.md and find the section with "Comprehensive Parallel Execution Order", then Wave 2 , Parallel Group 2A. Find its matching description in ARCHITECTURE.md. Then convert these into concrete, actionable, atomic tasks with explicit maximally robust and correct implementation examples, explicit and thorough acceptance criteria, references to existing code locations if updates or modifications need to occur. These tasks need to be explicit to the point any AI agent could follow them and product the maximially spec compliant, robust, correct, and performant result.


Examine ARCHITECTURE.md and find the section with "Comprehensive Parallel Execution Order", then Wave 4. For each item in each Parallel Group section, find its matching description in ARCHITECTURE.md. Then validate that our code implmentation FULLY complies with the ARCHITECTURE.md spec,have tests for all files, and that the code meets our coding standards (cyclomatic complexity of 4 or less, minimal or no nested if statements, proper non-circiular imports). Also validate and check for race conditions or deadlocks, memory leaks, poor implementations that could impact performance, proper closure/shutdown, etc. If the LSP complains, we fix it - ALWAYS.

- DO NOT generate your own mocks - specify interfaces for structs and use mockery - https://github.com/vektra/mockery to generate them.

- AdaptiveChannel in code differs from ARCH spec (spec mentions adaptive loop, send timeout and overflow behavior; code uses resize on send/receive and optional overflow, no background adaptLoop). Determine if acceptable or mismatch.