; YAML symbol extraction query
; Block mapping pairs (top level)
(block_mapping_pair
  key: (flow_node) @key.name
  value: (_) @key.value) @pair

; Flow mapping pairs
(flow_pair
  key: (flow_node) @flow_key.name
  value: (_) @flow_key.value) @flow_pair

; Block sequence items
(block_sequence
  (block_sequence_item) @sequence_item)

; Anchors
(anchor
  (anchor_name) @anchor.name) @anchor

; Aliases
(alias
  (alias_name) @alias.name) @alias

; Tags
(tag) @tag
