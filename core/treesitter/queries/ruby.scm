; Ruby symbol extraction query
; Methods
(method
  name: (identifier) @method.name
  parameters: (method_parameters)? @method.params) @method

; Singleton methods (class methods)
(singleton_method
  object: (_) @singleton.object
  name: (identifier) @singleton.name) @singleton

; Classes
(class
  name: (constant) @class.name
  superclass: (superclass)? @class.superclass) @class

; Modules
(module
  name: (constant) @module.name) @module

; Blocks with names (define_method, etc)
(call
  method: (identifier) @block_def.method
  arguments: (argument_list
    (simple_symbol) @block_def.name)
  block: (block)? @block_def.block) @block_def

; Require/require_relative
(call
  method: (identifier) @require.method
  arguments: (argument_list
    (string) @require.path)) @require

; Constants
(assignment
  left: (constant) @const.name
  right: (_) @const.value) @const

; Attr accessors
(call
  method: (identifier) @attr.type
  arguments: (argument_list
    (simple_symbol) @attr.name)) @attr

; Instance variables
(instance_variable) @ivar
