
  1. Staging Environment - Most Robust

  Virtual Filesystem Layer with Copy-on-Write

  ┌─────────────────────────────────────────────────────────────────────────────────────┐
  │                         VIRTUAL FILESYSTEM LAYER                                     │
  ├─────────────────────────────────────────────────────────────────────────────────────┤
  │                                                                                     │
  │  ALL agent file operations go through VFS abstraction:                              │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │                            VFS Layer (Go)                                    │   │
  │  │                                                                              │   │
  │  │  Read(path) ──────────────────────────────────────────────────────────────┐ │   │
  │  │       │                                                                   │ │   │
  │  │       ├── Check write buffer (in-memory) ─► HIT: return buffered         │ │   │
  │  │       │                                                                   │ │   │
  │  │       └── MISS: pass through to real filesystem ─────────────────────────┘ │   │
  │  │                                                                              │   │
  │  │  Write(path, data) ───────────────────────────────────────────────────────┐ │   │
  │  │       │                                                                   │ │   │
  │  │       ├── Store original content (for 3-way merge later)                  │ │   │
  │  │       │                                                                   │ │   │
  │  │       ├── Buffer write in memory                                          │ │   │
  │  │       │       │                                                           │ │   │
  │  │       │       └── If >threshold: spill to temp disk                       │ │   │
  │  │       │                                                                   │ │   │
  │  │       └── Track: file path, original hash, new content ──────────────────┘ │   │
  │  │                                                                              │   │
  │  │  Delete(path) ────────────────────────────────────────────────────────────┐ │   │
  │  │       │                                                                   │ │   │
  │  │       └── Mark as tombstone in buffer (don't delete real file yet) ──────┘ │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  │  On Merge (pipeline complete + approved):                                           │
  │  ─────────────────────────────────────────                                          │
  │  For each changed file:                                                             │
  │  1. Read current working tree version (may have changed since pipeline started)     │
  │  2. 3-way merge: original (base) ↔ pipeline changes ↔ current working tree          │
  │  3. Conflict? → Present to user with diff view                                      │
  │  4. No conflict? → Apply atomically (write temp, rename)                            │
  │  5. Verify: re-run tests on merged result if any conflicts were resolved            │
  │                                                                                     │
  │  Benefits:                                                                          │
  │  • Zero disk writes until approved                                                  │
  │  • Full rollback: just discard buffer                                               │
  │  • Cross-platform: pure Go, no OS dependencies                                      │
  │  • Precise tracking: know exactly what changed                                      │
  │  • Memory-efficient: spill large files to temp                                      │
  │  • Safe concurrent pipelines: each has own VFS buffer                               │
  │                                                                                     │
  └─────────────────────────────────────────────────────────────────────────────────────┘

  2. Process Isolation - Most Robust

  Platform-Native Sandboxing
  Platform: Linux
  Mechanism: User namespaces + seccomp + cgroups
  Capabilities: Full isolation without root. Filesystem, network, PID, resource limits.
  ────────────────────────────────────────
  Platform: macOS
  Mechanism: sandbox-exec (Seatbelt)
  Capabilities: Filesystem restrictions, network policies, no IPC.
  ────────────────────────────────────────
  Platform: Windows
  Mechanism: Job Objects + Restricted Tokens + AppContainer
  Capabilities: Process limits, filesystem virtualization, network restrictions.
  ┌─────────────────────────────────────────────────────────────────────────────────────┐
  │                         PROCESS SANDBOX                                              │
  ├─────────────────────────────────────────────────────────────────────────────────────┤
  │                                                                                     │
  │  Every subprocess spawned by agents runs inside sandbox:                            │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │  LINUX (user namespaces - no root required)                                  │   │
  │  │                                                                              │   │
  │  │  • Mount namespace: read-only root, writable overlay for project dir         │   │
  │  │  • Network namespace: loopback only OR proxy through Sylk                    │   │
  │  │  • PID namespace: isolated process tree                                      │   │
  │  │  • cgroups v2: CPU, memory, IO limits                                        │   │
  │  │  • seccomp: block dangerous syscalls (ptrace, mount, etc.)                   │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │  MACOS (sandbox-exec)                                                        │   │
  │  │                                                                              │   │
  │  │  • Seatbelt profile: allow read project, write staging only                  │   │
  │  │  • Network: allow loopback, allowlisted domains only                         │   │
  │  │  • No file-write-* outside staging                                           │   │
  │  │  • No process-exec-* for setuid binaries                                     │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │  WINDOWS (Job Objects + AppContainer)                                        │   │
  │  │                                                                              │   │
  │  │  • Job Object: CPU/memory limits, no breakaway, kill on close                │   │
  │  │  • Restricted Token: remove admin privileges                                 │   │
  │  │  • AppContainer: filesystem/registry virtualization                          │   │
  │  │  • Windows Firewall: outbound rules per-process                              │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  └─────────────────────────────────────────────────────────────────────────────────────┘

  3. Network Isolation - Most Robust

  Proxy All Network Through Sylk

  ┌─────────────────────────────────────────────────────────────────────────────────────┐
  │                         NETWORK PROXY                                                │
  ├─────────────────────────────────────────────────────────────────────────────────────┤
  │                                                                                     │
  │  Sandboxed processes have NO direct network access.                                 │
  │  All traffic routed through Sylk's network proxy:                                   │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │  Subprocess ──► HTTP_PROXY=localhost:$PORT ──► Sylk Proxy                    │   │
  │  │                                                                              │   │
  │  │  Sylk Proxy:                                                                 │   │
  │  │  1. Extract destination domain                                               │   │
  │  │  2. Check allowlist (.sylk/local/permissions.yaml)                           │   │
  │  │     ├── Allowed: forward request, log                                        │   │
  │  │     └── Not allowed: block, prompt user "Allow {domain}?"                    │   │
  │  │  3. User approves → add to allowlist → forward                               │   │
  │  │  4. All traffic logged for audit                                             │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  │  Benefits:                                                                          │
  │  • No surprise external calls                                                       │
  │  • Full traffic audit log                                                           │
  │  • Domain allowlist persisted per-project                                           │
  │  • Can inspect/modify requests if needed                                            │
  │  • Works with HTTP, HTTPS (MITM with generated CA), and CONNECT tunneling           │
  │                                                                                     │
  └─────────────────────────────────────────────────────────────────────────────────────┘

  4. User Commands - Most Robust

  Sandbox with Instant Auto-Merge

  User commands SHOULD be sandboxed for protection, but with different UX:

  ┌─────────────────────────────────────────────────────────────────────────────────────┐
  │                         USER COMMAND SANDBOXING                                      │
  ├─────────────────────────────────────────────────────────────────────────────────────┤
  │                                                                                     │
  │  User runs: "npm install lodash"                                                    │
  │                                                                                     │
  │  ┌─────────────────────────────────────────────────────────────────────────────┐   │
  │  │  1. Create ephemeral sandbox (VFS layer)                                     │   │
  │  │  2. Run command in sandbox                                                   │   │
  │  │  3. Command completes                                                        │   │
  │  │  4. Show user what changed:                                                  │   │
  │  │     "npm install lodash will modify:"                                        │   │
  │  │     • package.json (+1 line)                                                 │   │
  │  │     • package-lock.json (+247 lines)                                         │   │
  │  │     • node_modules/ (+142 files)                                             │   │
  │  │     [Apply] [Cancel] [Show Diff]                                             │   │
  │  │                                                                              │   │
  │  │  5. User clicks Apply → instant merge to working tree                        │   │
  │  │  6. User clicks Cancel → discard sandbox, no changes                         │   │
  │  │                                                                              │   │
  │  └─────────────────────────────────────────────────────────────────────────────┘   │
  │                                                                                     │
  │  Configurable behavior:                                                             │
  │  ──────────────────────                                                             │
  │  user_commands:                                                                     │
  │    sandbox: true                    # Enable sandboxing                             │
  │    auto_apply: false                # Require confirmation                          │
  │    auto_apply_trusted: true         # Auto-apply for allowlisted commands           │
  │    show_diff_threshold: 10          # Show diff if >N files changed                 │
  │                                                                                     │
  │  Benefits:                                                                          │
  │  • Protection from "rm -rf /" mistakes                                              │
  │  • User sees exactly what will change before it happens                             │
  │  • Can trust allowlisted commands for smooth UX                                     │
  │  • Full audit trail                                                                 │
  │                                                                                     │
  │  Addressing concerns:                                                               │
  │  • Conflicts: User commands get exclusive access (pipelines pause)                  │
  │  • Race conditions: Sequential, synchronous execution                               │
  │  • Reliability: Fresh sandbox per command, instant merge                            │
  │  • Performance: Sandbox overhead ~10ms. Acceptable for safety.                      │
  │                                                                                     │
  └─────────────────────────────────────────────────────────────────────────────────────┘

  Question 6: Do you want this full sandboxing model?

  - VFS layer for all file operations
  - Platform-native process isolation
  - Network proxy with domain allowlisting
  - User command sandboxing with preview before apply