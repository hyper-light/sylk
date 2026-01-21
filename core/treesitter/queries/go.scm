; Go symbol extraction query
; Functions
(function_declaration
  name: (identifier) @func.name) @func

; Methods
(method_declaration
  receiver: (parameter_list) @method.receiver
  name: (field_identifier) @method.name) @method

; Type declarations
(type_declaration
  (type_spec
    name: (type_identifier) @type.name
    type: (_) @type.kind)) @type

; Import declarations
(import_declaration
  (import_spec
    path: (interpreted_string_literal) @import.path
    name: (package_identifier)? @import.alias)) @import

; Struct fields (for detailed extraction)
(field_declaration
  name: (field_identifier) @field.name
  type: (_) @field.type) @field

; Interface methods
(method_elem
  name: (field_identifier) @interface_method.name
  parameters: (parameter_list)? @interface_method.params) @interface_method
