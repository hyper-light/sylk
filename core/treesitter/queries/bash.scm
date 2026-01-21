; Bash symbol extraction query
; Function definitions
(function_definition
  name: (word) @func.name
  body: (compound_statement) @func.body) @func

; Variable assignments
(variable_assignment
  name: (variable_name) @var.name
  value: (_) @var.value) @var

; Export statements
(declaration_command
  (variable_assignment
    name: (variable_name) @export.name)) @export

; Alias definitions
(alias
  name: (word) @alias.name
  value: (word) @alias.value) @alias

; Source/dot commands (imports)
(command
  name: (command_name) @source.cmd
  argument: (word) @source.file) @source

; Shebang
(comment) @shebang

; Case statements
(case_statement
  value: (_) @case.value
  (case_item
    value: (_) @case_item.pattern)) @case

; For loops
(for_statement
  variable: (variable_name) @for.var) @for

; While/until loops
(while_statement
  (command) @while.condition) @while

; Array declarations
(array
  (word) @array.element) @array
