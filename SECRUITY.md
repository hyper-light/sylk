Examine Comprehensive Parallel Execution Order section of TODO.md WAVE 3. Verify Parallel Group 3C can indeed be executed in parallel, then find the matching architecture description for the item in ARCHITECTURE.md. Implement according to ARCHITECTURE.md spec and use sub agents to accomplish the work required maximizing parallelism. After *EACH* submitem is created YOU MUST ALWAYS AND FOREVER:
- Generate a commit.
- Examine the cyclomatic complexity or the code your just wrote and ENSURE it is 4 or less.
- Generate test files per-file with THOROUGH testing that MUST cover happy path, negative path, failure, any race condition or deadlock handling, and edge cases.
- YOU MUST RUN THESE TESTS AND ALL TESTS MUST PASS DO NOT JUST FIX THE TESTS TO PASS YOU MUST FIX THE ACTUAL IMPLEMENTATION CODE.
- Generate another commit.
- You MUST mark EACH item done in TODO.md AFTER IT IS COMPLETED ALWAYS
- DO NOT SKIP ANY ITEM EVER FOR ANY REASON DO NOT DEFER 
- YOU MUST CHECK FOR RACE CONDITIONS, DEADLOCKS, AND MEMORY LEAKS. Do NOT accept any of these
- IF YOU ENCOUNTER A BUILD OR TEST FAILURE, EVEN IF PRE-EXISTING, YOU FIX IT.
- DO NOT generate your own mocks - specify interfaces for structs and use mockery - https://github.com/vektra/mockery to generate them.


Now for each item in Comprehensive Parallel Execution Order Wave 2, find its matching description in ARCHITECTURE.md. Then validate that our code implmentation FULLY complies with the ARCHITECTURE.md spec,have tests for all files, and that the code meets our coding standards (cyclomatic complexity of 4 or less, minimal or no nested if statements, proper non-circiular imports). Also validate and check for race conditions or deadlocks, memory leaks, poor implementations that could impact performance, proper closure/shutdown, etc.

- DO NOT generate your own mocks - specify interfaces for structs and use mockery - https://github.com/vektra/mockery to generate them.
