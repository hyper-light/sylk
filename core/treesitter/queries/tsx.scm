; TSX symbol extraction query (TypeScript + JSX)
; Include all TypeScript patterns
(function_declaration
  name: (identifier) @func.name
  parameters: (formal_parameters) @func.params) @func

(lexical_declaration
  (variable_declarator
    name: (identifier) @component.name
    value: (arrow_function) @component.body)) @component

(class_declaration
  name: (type_identifier) @class.name) @class

(method_definition
  name: (property_identifier) @method.name) @method

(interface_declaration
  name: (type_identifier) @interface.name) @interface

(type_alias_declaration
  name: (type_identifier) @type_alias.name) @type_alias

(import_statement
  source: (string) @import.source) @import

; JSX elements (component usage tracking)
(jsx_element
  open_tag: (jsx_opening_element
    name: (_) @jsx.component)) @jsx

(jsx_self_closing_element
  name: (_) @jsx_self.component) @jsx_self
