; CSS symbol extraction query
; Rule sets with selectors
(rule_set
  (selectors) @rule.selectors
  (block) @rule.block) @rule

; Class selectors
(class_selector
  (class_name) @class.name) @class

; ID selectors
(id_selector
  (id_name) @id.name) @id

; Element selectors
(tag_name) @element.name

; Pseudo-class selectors
(pseudo_class_selector
  (class_name) @pseudo_class.name) @pseudo_class

; Pseudo-element selectors
(pseudo_element_selector
  (tag_name) @pseudo_element.name) @pseudo_element

; At-rules
(at_rule
  (at_keyword) @at_rule.keyword
  (keyword_query)? @at_rule.query) @at_rule

; Media queries
(media_statement
  (keyword_query)? @media.query
  (block) @media.block) @media

; Keyframes
(keyframes_statement
  (keyframes_name) @keyframes.name) @keyframes

; CSS variables (custom properties)
(declaration
  (property_name) @property.name
  ((_)) @property.value) @declaration
