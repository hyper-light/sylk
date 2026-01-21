; Java symbol extraction query
; Classes
(class_declaration
  name: (identifier) @class.name
  superclass: (superclass)? @class.extends
  interfaces: (super_interfaces)? @class.implements) @class

; Interfaces
(interface_declaration
  name: (identifier) @interface.name) @interface

; Enums
(enum_declaration
  name: (identifier) @enum.name) @enum

; Methods
(method_declaration
  type: (_) @method.return_type
  name: (identifier) @method.name
  parameters: (formal_parameters) @method.params) @method

; Constructors
(constructor_declaration
  name: (identifier) @constructor.name
  parameters: (formal_parameters) @constructor.params) @constructor

; Fields
(field_declaration
  type: (_) @field.type
  declarator: (variable_declarator
    name: (identifier) @field.name)) @field

; Import declarations
(import_declaration
  (scoped_identifier) @import.path) @import

; Package declaration
(package_declaration
  (scoped_identifier) @package.name) @package

; Annotations
(annotation
  name: (identifier) @annotation.name) @annotation

; Record declarations (Java 14+)
(record_declaration
  name: (identifier) @record.name
  parameters: (formal_parameters) @record.params) @record
