; JSON symbol extraction query
; Top-level object keys
(document
  (object
    (pair
      key: (string) @key.name
      value: (_) @key.value))) @top_level_pair

; Nested object keys (first level only for performance)
(object
  (pair
    key: (string) @nested_key.name)) @nested_pair

; Arrays with objects
(array
  (object
    (pair
      key: (string) @array_obj_key.name))) @array_object
