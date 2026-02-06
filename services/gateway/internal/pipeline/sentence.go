package pipeline

import "strings"

// sentenceBuffer accumulates streamed tokens and splits at sentence boundaries.
type sentenceBuffer struct {
	buf strings.Builder
}

// Add appends a token and returns any complete sentence ready for TTS.
// Returns empty string if no sentence boundary detected yet.
func (s *sentenceBuffer) Add(token string) string {
	s.buf.WriteString(token)
	text := s.buf.String()
	complete, remainder := splitAtSentence(text)
	if complete == "" {
		return ""
	}
	s.buf.Reset()
	s.buf.WriteString(remainder)
	return complete
}

// Flush returns any remaining text in the buffer.
func (s *sentenceBuffer) Flush() string {
	text := strings.TrimSpace(s.buf.String())
	s.buf.Reset()
	return text
}

var sentenceEnders = map[byte]bool{'.': true, '!': true, '?': true}

// splitAtSentence finds the last sentence boundary in text.
// A boundary is a sentence ender (.!?) followed by whitespace.
// Returns (completeSentences, remainder). If no boundary, returns ("", text).
func splitAtSentence(text string) (string, string) {
	lastIdx := -1
	for i := range len(text) - 1 {
		if sentenceEnders[text[i]] && isWordBoundary(text[i+1]) {
			lastIdx = i + 1
		}
	}
	if lastIdx < 0 {
		return "", text
	}
	return strings.TrimSpace(text[:lastIdx]), text[lastIdx:]
}

func isWordBoundary(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\t'
}
