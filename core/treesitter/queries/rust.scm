; Rust symbol extraction query
; Functions
(function_item
  name: (identifier) @func.name
  parameters: (parameters) @func.params
  return_type: (_)? @func.return_type) @func

; Structs
(struct_item
  name: (type_identifier) @struct.name) @struct

; Enums
(enum_item
  name: (type_identifier) @enum.name) @enum

; Traits
(trait_item
  name: (type_identifier) @trait.name) @trait

; Type aliases
(type_item
  name: (type_identifier) @type_alias.name) @type_alias

; Impl blocks
(impl_item
  type: (_) @impl.type
  body: (declaration_list) @impl.body) @impl

; Methods in impl blocks
(impl_item
  body: (declaration_list
    (function_item
      name: (identifier) @impl_method.name
      parameters: (parameters) @impl_method.params) @impl_method))

; Use declarations
(use_declaration
  argument: (_) @use.path) @use

; Modules
(mod_item
  name: (identifier) @mod.name) @mod

; Constants
(const_item
  name: (identifier) @const.name
  type: (_) @const.type) @const

; Statics
(static_item
  name: (identifier) @static.name
  type: (_) @static.type) @static

; Macros
(macro_definition
  name: (identifier) @macro.name) @macro
