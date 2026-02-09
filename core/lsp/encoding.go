package lsp

// RuneOffsetToUTF16 converts a 0-based rune offset within a line to the
// corresponding 0-based UTF-16 code unit offset. Characters outside the
// Basic Multilingual Plane (> U+FFFF) occupy 2 UTF-16 code units (a
// surrogate pair) but only 1 rune.
func RuneOffsetToUTF16(line string, runeOffset int) int {
	utf16Offset := 0
	i := 0
	for _, r := range line {
		if i >= runeOffset {
			break
		}
		i++
		if r >= 0x10000 {
			utf16Offset += 2 // surrogate pair
		} else {
			utf16Offset++
		}
	}
	return utf16Offset
}

// UTF16ToRuneOffset converts a 0-based UTF-16 code unit offset within a
// line to the corresponding 0-based rune offset.
func UTF16ToRuneOffset(line string, utf16Offset int) int {
	runeOffset := 0
	remaining := utf16Offset
	for _, r := range line {
		if remaining <= 0 {
			break
		}
		if r >= 0x10000 {
			remaining -= 2
		} else {
			remaining--
		}
		runeOffset++
	}
	return runeOffset
}
