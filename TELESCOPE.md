  Already Covered                                                                                       
  ┌───────────────────────────┬───────────────────────────────────────────────────┐                     
  │     Telescope Picker      │                  Sylk Equivalent                  │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ find_files                │ Ctrl+P command palette (fuzzy file finder)        │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ live_grep                 │ Alt+F multi-file content search in file tree      │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ buffers                   │ Tab listing/filtering in file tree (Alt+T)        │
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ current_buffer_fuzzy_find │ Alt+F in-editor find bar                          │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ lsp_references            │ gr / Alt+R / :references                          │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤                     
  │ lsp_definitions           │ gd                                                │                     
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ lsp_document_symbols      │ :symbols / Alt+Shift+.                            │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ diagnostics               │ Gutter signs + inline display                     │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ marks                     │ Full mark system (a-z, A-Z, specials)             │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ registers                 │ Full register system (named, numbered, clipboard) │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ help_tags                 │ Field manual (Alt+H) with search                  │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ keymaps                   │ Field manual documents all bindings               │
  ├───────────────────────────┼───────────────────────────────────────────────────┤
  │ command_history           │ Input history with ring buffer                    │
  └───────────────────────────┴───────────────────────────────────────────────────┘

Gaps — Features We Don't Have
  ┌───────────────────────┬──────────────────────────────────────────┬─────────────────────────────────┐
  │   Telescope Picker    │               What It Does               │              Value              │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ grep_string           │ Search for the word under cursor across  │ High — one-keypress "find all   │
  │                       │ the project                              │ usages" without LSP             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ git_commits           │ Browse commit log with diff preview      │ Medium — visual git log with    │
  │                       │                                          │ preview                         │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ git_branches          │ List/checkout/create/delete/merge        │ Medium — branch management      │
  │                       │ branches                                 │ without leaving editor          │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ git_status (as        │ Interactive staged/unstaged file picker  │ Medium — we show status in tree │
  │ picker)               │ with diff preview                        │  but can't stage/diff from it   │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ git_stash             │ Browse and apply stashed changes         │ Low                             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ oldfiles              │ Recently opened files (across sessions)  │ High — quick access to recent   │
  │                       │                                          │ files you've closed             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ lsp_workspace_symbols │ Fuzzy search symbols across entire       │ High — :symbols is              │
  │                       │ project                                  │ document-only                   │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ lsp_implementations   │ Find interface implementations           │ Medium — depends on language    │
  │                       │                                          │ server support                  │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ lsp_type_definitions  │ Go to type definition                    │ Medium                          │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │                       │                                          │ Medium — we show diagnostics    │
  │ quickfix / loclist    │ Navigate structured error/result lists   │ inline but no unified list to   │
  │                       │                                          │ jump through                    │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ colorscheme           │ Live preview theme switching             │ Low                             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ treesitter            │ Browse functions/vars via tree-sitter    │ Low — overlaps with LSP symbols │
  │                       │ queries                                  │                                 │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ builtin               │ Meta-picker: pick a picker               │ Low — nice discovery UX         │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ man_pages             │ Browse system man pages                  │ Low                             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ vim_options           │ Browse/edit vim settings                 │ Low                             │
  ├───────────────────────┼──────────────────────────────────────────┼─────────────────────────────────┤
  │ search_history        │ Browse previous search patterns          │ Low                             │
  └───────────────────────┴──────────────────────────────────────────┴─────────────────────────────────┘

  High-Value Gaps Worth Considering

  1. grep_string (word under cursor) — Press a key in normal mode, instantly search the whole project
  for that symbol. Complements LSP references (works without a language server).
  2. oldfiles (recent files) — Persistent recent file history across sessions. Huge for workflow
  continuity — "what was I working on yesterday?"
  3. lsp_workspace_symbols — Fuzzy-search any symbol in the project by name. Current :symbols is limited
   to the current document.
  4. git_commits / git_branches / git_status as pickers — The file tree shows git status, but there's no
   way to browse commits, switch branches, or stage files interactively.
  5. Diagnostics list (quickfix) — We show diagnostics inline per-line, but there's no unified "show me
  all errors in this project" picker to jump through them.
