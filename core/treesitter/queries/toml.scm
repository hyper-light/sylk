; TOML symbol extraction query
; Tables (sections)
(table
  (bare_key) @table.name) @table

(table
  (dotted_key) @table.dotted_name) @table_dotted

; Array of tables
(table_array_element
  (bare_key) @array_table.name) @array_table

(table_array_element
  (dotted_key) @array_table.dotted_name) @array_table_dotted

; Key-value pairs
(pair
  (bare_key) @pair.key
  ((_) @pair.value)) @pair

(pair
  (dotted_key) @pair.dotted_key
  ((_) @pair.value)) @pair_dotted

; Inline tables
(inline_table
  (pair
    (bare_key) @inline.key)) @inline_table
