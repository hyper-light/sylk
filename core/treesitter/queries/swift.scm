; Swift symbol extraction query
; Functions
(function_declaration
  name: (simple_identifier) @func.name
  (parameter) @func.params) @func

; Classes
(class_declaration
  name: (type_identifier) @class.name) @class

; Structs
(struct_declaration
  name: (type_identifier) @struct.name) @struct

; Enums
(enum_declaration
  name: (type_identifier) @enum.name) @enum

; Protocols
(protocol_declaration
  name: (type_identifier) @protocol.name) @protocol

; Extensions
(extension_declaration
  (type_identifier) @extension.type) @extension

; Properties
(property_declaration
  (pattern
    (simple_identifier) @property.name)) @property

; Initializers
(initializer_declaration) @init

; Import statements
(import_declaration
  (identifier) @import.module) @import

; Type aliases
(typealias_declaration
  name: (type_identifier) @typealias.name) @typealias

; Associated types
(associatedtype_declaration
  name: (type_identifier) @associatedtype.name) @associatedtype
