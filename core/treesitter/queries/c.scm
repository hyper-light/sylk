; C symbol extraction query
; Function definitions
(function_definition
  declarator: (function_declarator
    declarator: (_) @func.name
    parameters: (parameter_list) @func.params)) @func

; Function declarations
(declaration
  declarator: (function_declarator
    declarator: (_) @func_decl.name
    parameters: (parameter_list) @func_decl.params)) @func_decl

; Struct definitions
(struct_specifier
  name: (type_identifier) @struct.name
  body: (field_declaration_list)? @struct.body) @struct

; Union definitions
(union_specifier
  name: (type_identifier) @union.name) @union

; Enum definitions
(enum_specifier
  name: (type_identifier) @enum.name
  body: (enumerator_list)? @enum.body) @enum

; Typedefs
(type_definition
  type: (_) @typedef.type
  declarator: (type_identifier) @typedef.name) @typedef

; Includes
(preproc_include
  path: (_) @include.path) @include

; Defines
(preproc_def
  name: (identifier) @define.name
  value: (_)? @define.value) @define

; Macro functions
(preproc_function_def
  name: (identifier) @macro_func.name
  parameters: (preproc_params) @macro_func.params) @macro_func

; Global variables
(declaration
  declarator: (init_declarator
    declarator: (identifier) @global.name)) @global
