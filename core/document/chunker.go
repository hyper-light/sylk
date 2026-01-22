package document

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

type Chunk struct {
	ID           string
	DocumentID   string
	Index        int
	Text         string
	StartOffset  int
	EndOffset    int
	ContentHash  string
	OverlapStart int
}

type ChunkerConfig struct {
	TargetChunkSize int
	OverlapSize     int
	MinChunkSize    int
}

func DefaultChunkerConfig() ChunkerConfig {
	return ChunkerConfig{
		TargetChunkSize: 512,
		OverlapSize:     64,
		MinChunkSize:    50,
	}
}

type Chunker struct {
	config ChunkerConfig
}

func NewChunker(config ChunkerConfig) *Chunker {
	if config.TargetChunkSize <= 0 {
		config.TargetChunkSize = 512
	}
	if config.OverlapSize < 0 {
		config.OverlapSize = 0
	}
	if config.MinChunkSize <= 0 {
		config.MinChunkSize = 50
	}
	return &Chunker{config: config}
}

func (c *Chunker) Chunk(documentID string, text string) []Chunk {
	text = normalizeWhitespace(text)
	if len(text) < c.config.MinChunkSize {
		return nil
	}

	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return nil
	}

	var chunks []Chunk
	var currentChunk strings.Builder
	chunkStart := 0
	currentOffset := 0

	flushChunk := func(endOffset int) {
		content := strings.TrimSpace(currentChunk.String())
		if len(content) >= c.config.MinChunkSize {
			chunk := Chunk{
				ID:          generateChunkID(documentID, len(chunks)),
				DocumentID:  documentID,
				Index:       len(chunks),
				Text:        content,
				StartOffset: chunkStart,
				EndOffset:   endOffset,
				ContentHash: hashContent(content),
			}
			chunks = append(chunks, chunk)
		}
		currentChunk.Reset()
	}

	for _, sentence := range sentences {
		sentenceLen := len(sentence)

		if currentChunk.Len()+sentenceLen > c.config.TargetChunkSize && currentChunk.Len() > 0 {
			flushChunk(currentOffset)

			overlapText := getOverlapText(chunks, c.config.OverlapSize)
			if len(overlapText) > 0 {
				currentChunk.WriteString(overlapText)
				currentChunk.WriteString(" ")
			}
			chunkStart = currentOffset - len(overlapText)
		}

		if currentChunk.Len() > 0 {
			currentChunk.WriteString(" ")
		}
		currentChunk.WriteString(sentence)
		currentOffset += sentenceLen + 1
	}

	if currentChunk.Len() > 0 {
		flushChunk(currentOffset)
	}

	return chunks
}

func normalizeWhitespace(text string) string {
	var result strings.Builder
	result.Grow(len(text))

	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		} else {
			result.WriteRune(r)
			prevSpace = false
		}
	}

	return strings.TrimSpace(result.String())
}

func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])

		if isSentenceEnd(runes, i) {
			sentence := strings.TrimSpace(current.String())
			if len(sentence) > 0 {
				sentences = append(sentences, sentence)
			}
			current.Reset()
		}
	}

	if current.Len() > 0 {
		sentence := strings.TrimSpace(current.String())
		if len(sentence) > 0 {
			sentences = append(sentences, sentence)
		}
	}

	return sentences
}

func isSentenceEnd(runes []rune, i int) bool {
	r := runes[i]
	if r != '.' && r != '!' && r != '?' {
		return false
	}

	if i+1 >= len(runes) {
		return true
	}

	next := runes[i+1]
	if unicode.IsSpace(next) {
		if i+2 < len(runes) && unicode.IsUpper(runes[i+2]) {
			return true
		}
		if i+2 >= len(runes) {
			return true
		}
	}

	return false
}

func getOverlapText(chunks []Chunk, overlapSize int) string {
	if len(chunks) == 0 || overlapSize <= 0 {
		return ""
	}

	lastChunk := chunks[len(chunks)-1]
	text := lastChunk.Text

	if len(text) <= overlapSize {
		return text
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var overlap strings.Builder
	for i := len(words) - 1; i >= 0; i-- {
		word := words[i]
		if overlap.Len()+len(word)+1 > overlapSize {
			break
		}
		if overlap.Len() > 0 {
			overlap.WriteString(" " + word)
		} else {
			overlap.WriteString(word)
		}
	}

	result := overlap.String()
	words = strings.Fields(result)
	for i, j := 0, len(words)-1; i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, " ")
}

func generateChunkID(documentID string, index int) string {
	h := sha256.New()
	h.Write([]byte(documentID))
	h.Write([]byte{byte(index >> 24), byte(index >> 16), byte(index >> 8), byte(index)})
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])[:16]
}
