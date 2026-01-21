; JavaScript/TypeScript symbol extraction query
; Function declarations
(function_declaration
  name: (identifier) @func.name
  parameters: (formal_parameters) @func.params) @func

; Generator functions
(generator_function_declaration
  name: (identifier) @generator.name
  parameters: (formal_parameters) @generator.params) @generator

; Arrow functions assigned to variables
(lexical_declaration
  (variable_declarator
    name: (identifier) @arrow.name
    value: (arrow_function
      parameters: (_) @arrow.params))) @arrow

; Function expressions assigned to variables
(variable_declaration
  (variable_declarator
    name: (identifier) @func_expr.name
    value: (function
      parameters: (formal_parameters) @func_expr.params))) @func_expr

; Class declarations
(class_declaration
  name: (identifier) @class.name) @class

; Class methods
(class_declaration
  body: (class_body
    (method_definition
      name: (property_identifier) @method.name
      parameters: (formal_parameters) @method.params) @method))

; Import statements
(import_statement
  source: (string) @import.source) @import

; Export statements with declarations
(export_statement
  declaration: (function_declaration
    name: (identifier) @export_func.name)) @export_func

(export_statement
  declaration: (class_declaration
    name: (identifier) @export_class.name)) @export_class

; TypeScript interfaces
(interface_declaration
  name: (type_identifier) @interface.name) @interface

; TypeScript type aliases
(type_alias_declaration
  name: (type_identifier) @type_alias.name) @type_alias

; TypeScript enums
(enum_declaration
  name: (identifier) @enum.name) @enum
