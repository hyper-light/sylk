; HTML symbol extraction query
; Elements with id
(element
  (start_tag
    (tag_name) @element.tag
    (attribute
      (attribute_name) @attr.name
      (quoted_attribute_value) @attr.value))) @element

; Script tags
(script_element
  (start_tag) @script.start
  (raw_text)? @script.content) @script

; Style tags
(style_element
  (start_tag) @style.start
  (raw_text)? @style.content) @style

; Links
(element
  (start_tag
    (tag_name) @link.tag
    (attribute
      (attribute_name) @link.attr
      (quoted_attribute_value) @link.href))) @link

; Meta tags
(element
  (start_tag
    (tag_name) @meta.tag
    (attribute)* @meta.attrs)) @meta

; Form elements
(element
  (start_tag
    (tag_name) @form.tag)) @form

; Custom elements (web components)
(element
  (start_tag
    (tag_name) @custom.tag)) @custom
