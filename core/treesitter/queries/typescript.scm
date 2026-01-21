; TypeScript symbol extraction query (extends JavaScript)
; Function declarations
(function_declaration
  name: (identifier) @func.name
  parameters: (formal_parameters) @func.params
  return_type: (type_annotation)? @func.return_type) @func

; Arrow functions
(lexical_declaration
  (variable_declarator
    name: (identifier) @arrow.name
    value: (arrow_function
      parameters: (_) @arrow.params
      return_type: (type_annotation)? @arrow.return_type))) @arrow

; Class declarations
(class_declaration
  name: (type_identifier) @class.name) @class

; Class methods
(method_definition
  name: (property_identifier) @method.name
  parameters: (formal_parameters) @method.params
  return_type: (type_annotation)? @method.return_type) @method

; Interfaces
(interface_declaration
  name: (type_identifier) @interface.name
  body: (interface_body)? @interface.body) @interface

; Type aliases
(type_alias_declaration
  name: (type_identifier) @type_alias.name
  value: (_) @type_alias.value) @type_alias

; Enums
(enum_declaration
  name: (identifier) @enum.name
  body: (enum_body) @enum.body) @enum

; Import statements
(import_statement
  source: (string) @import.source) @import

; Export statements
(export_statement
  declaration: (_) @export.declaration) @export

; Namespace/Module declarations
(module
  name: (identifier) @namespace.name) @namespace
