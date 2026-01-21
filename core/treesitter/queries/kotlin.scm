; Kotlin symbol extraction query
; Functions
(function_declaration
  (simple_identifier) @func.name
  (function_value_parameters) @func.params) @func

; Classes
(class_declaration
  (type_identifier) @class.name) @class

; Objects (singletons)
(object_declaration
  (type_identifier) @object.name) @object

; Interfaces
(interface_declaration
  (type_identifier) @interface.name) @interface

; Properties
(property_declaration
  (variable_declaration
    (simple_identifier) @property.name)) @property

; Companion objects
(companion_object
  (type_identifier)? @companion.name) @companion

; Data classes
(class_declaration
  (modifiers
    (class_modifier) @data_modifier)
  (type_identifier) @data_class.name) @data_class

; Import statements
(import_header
  (identifier) @import.path) @import

; Package declaration
(package_header
  (identifier) @package.name) @package

; Type aliases
(type_alias
  (type_identifier) @typealias.name) @typealias

; Enum entries
(enum_entry
  (simple_identifier) @enum_entry.name) @enum_entry
