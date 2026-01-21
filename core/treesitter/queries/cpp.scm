; C++ symbol extraction query (extends C)
; Functions
(function_definition
  declarator: (function_declarator
    declarator: (_) @func.name
    parameters: (parameter_list) @func.params)) @func

; Classes
(class_specifier
  name: (type_identifier) @class.name
  body: (field_declaration_list)? @class.body) @class

; Structs
(struct_specifier
  name: (type_identifier) @struct.name) @struct

; Templates
(template_declaration
  (function_definition
    declarator: (function_declarator
      declarator: (_) @template_func.name))) @template_func

(template_declaration
  (class_specifier
    name: (type_identifier) @template_class.name)) @template_class

; Namespaces
(namespace_definition
  name: (identifier) @namespace.name) @namespace

; Using declarations
(using_declaration
  (scoped_identifier) @using.name) @using

; Includes
(preproc_include
  path: (_) @include.path) @include

; Enums (including enum class)
(enum_specifier
  name: (type_identifier) @enum.name) @enum

; Typedefs
(type_definition
  declarator: (type_identifier) @typedef.name) @typedef

; Type aliases (using =)
(alias_declaration
  name: (type_identifier) @alias.name) @alias

; Constructors/Destructors (inside class)
(function_definition
  declarator: (function_declarator
    declarator: (destructor_name) @destructor.name)) @destructor
