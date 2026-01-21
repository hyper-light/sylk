; Markdown symbol extraction query
; ATX headings (# style)
(atx_heading
  (atx_h1_marker) @h1.marker
  (inline) @h1.content) @h1

(atx_heading
  (atx_h2_marker) @h2.marker
  (inline) @h2.content) @h2

(atx_heading
  (atx_h3_marker) @h3.marker
  (inline) @h3.content) @h3

(atx_heading
  (atx_h4_marker) @h4.marker
  (inline) @h4.content) @h4

(atx_heading
  (atx_h5_marker) @h5.marker
  (inline) @h5.content) @h5

(atx_heading
  (atx_h6_marker) @h6.marker
  (inline) @h6.content) @h6

; Setext headings (underline style)
(setext_heading
  (paragraph) @setext.content
  (setext_h1_underline)) @setext_h1

(setext_heading
  (paragraph) @setext.content
  (setext_h2_underline)) @setext_h2

; Code blocks
(fenced_code_block
  (info_string)? @code.language
  (code_fence_content) @code.content) @code_block

(indented_code_block) @indented_code

; Links
(inline_link
  (link_text) @link.text
  (link_destination) @link.url) @link

; Reference links
(link_reference_definition
  (link_label) @ref.label
  (link_destination) @ref.url) @reference

; Images
(image
  (image_description) @image.alt
  (link_destination) @image.url) @image

; Block quotes
(block_quote) @quote

; Lists
(list
  (list_item) @list_item) @list
