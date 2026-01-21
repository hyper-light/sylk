; Python symbol extraction query
; Functions
(function_definition
  name: (identifier) @func.name
  parameters: (parameters) @func.params
  return_type: (type)? @func.return_type) @func

; Classes
(class_definition
  name: (identifier) @class.name
  superclasses: (argument_list)? @class.bases) @class

; Methods (inside class body)
(class_definition
  body: (block
    (function_definition
      name: (identifier) @method.name
      parameters: (parameters) @method.params) @method))

; Decorated functions
(decorated_definition
  (decorator) @decorator
  definition: (function_definition
    name: (identifier) @decorated_func.name) @decorated_func)

; Import statements
(import_statement
  name: (dotted_name) @import.module) @import

(import_statement
  name: (aliased_import
    name: (dotted_name) @import.module
    alias: (identifier) @import.alias)) @import_aliased

; From imports
(import_from_statement
  module_name: (dotted_name) @from_import.module
  name: (dotted_name) @from_import.name) @from_import

(import_from_statement
  module_name: (dotted_name) @from_import_alias.module
  name: (aliased_import
    name: (dotted_name) @from_import_alias.name
    alias: (identifier) @from_import_alias.alias)) @from_import_aliased

; Global assignments (constants/variables)
(assignment
  left: (identifier) @assignment.name
  right: (_) @assignment.value) @assignment
