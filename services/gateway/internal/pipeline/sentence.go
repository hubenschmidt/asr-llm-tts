package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

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

// splitAtSentence finds the last sentence or clause boundary in text.
// Primary boundaries: .!? followed by whitespace.
// Secondary boundaries: semicolons, em-dashes (—), and commas after >15 words.
// Returns (completeSentences, remainder). If no boundary, returns ("", text).
func splitAtSentence(text string) (string, string) {
	lastIdx := -1
	for i := range len(text) - 1 {
		if sentenceEnders[text[i]] && isWordBoundary(text[i+1]) {
			lastIdx = i + 1
		}
	}
	if lastIdx >= 0 {
		return strings.TrimSpace(text[:lastIdx]), text[lastIdx:]
	}

	// Secondary: semicolons and em-dashes
	lastIdx = findClauseBoundary(text)
	if lastIdx >= 0 {
		return strings.TrimSpace(text[:lastIdx]), text[lastIdx:]
	}

	// Tertiary: comma after >15 words
	lastIdx = findLongCommaClause(text)
	if lastIdx >= 0 {
		return strings.TrimSpace(text[:lastIdx+1]), text[lastIdx+1:]
	}

	return "", text
}

func isWordBoundary(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\t'
}

// findClauseBoundary returns the split index after a semicolon or em-dash followed by space.
func findClauseBoundary(text string) int {
	lastIdx := -1
	for i := range len(text) - 1 {
		ch := text[i]
		if (ch == ';' || isEmDash(text, i)) && isWordBoundary(text[i+1]) {
			lastIdx = i + 1
		}
	}
	return lastIdx
}

// isEmDash checks for a UTF-8 em-dash (U+2014: 0xE2 0x80 0x94) at position i.
func isEmDash(text string, i int) bool {
	return i+2 < len(text) && text[i] == 0xE2 && text[i+1] == 0x80 && text[i+2] == 0x94
}

// findLongCommaClause returns the index of the last comma where the preceding text has >15 words.
func findLongCommaClause(text string) int {
	lastIdx := -1
	words := 0
	for i := range len(text) {
		if text[i] == ' ' {
			words++
		}
		if text[i] == ',' && words > 15 {
			lastIdx = i
		}
	}
	return lastIdx
}

var (
	mdBoldItalic = regexp.MustCompile(`\*{1,3}|_{1,3}`)
	mdHeading    = regexp.MustCompile(`(?m)^#{1,6}\s*`)
	mdLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdImage      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdInlineCode = regexp.MustCompile("`([^`]*)`")
	mdStrike     = regexp.MustCompile(`~~(.*?)~~`)
	mdBullet     = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	mdNumbered   = regexp.MustCompile(`(?m)^\s*\d+\.\s+`)
	mdBlockquote = regexp.MustCompile(`(?m)^>\s*`)
	mdHRule      = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
)

// StripMarkdown removes common markdown formatting so TTS reads clean text.
func StripMarkdown(s string) string {
	s = mdHRule.ReplaceAllString(s, "")
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdStrike.ReplaceAllString(s, "$1")
	s = mdInlineCode.ReplaceAllString(s, "$1")
	s = mdBoldItalic.ReplaceAllString(s, "")
	s = mdHeading.ReplaceAllString(s, "")
	s = mdBullet.ReplaceAllString(s, "")
	s = mdNumbered.ReplaceAllString(s, "")
	s = mdBlockquote.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var (
	normCurrency  = regexp.MustCompile(`\$(\d+)\.(\d{2})`)
	normPercent   = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	normLargeNum  = regexp.MustCompile(`\b(\d{1,3}(?:,\d{3})+)\b`)
	normNumber    = regexp.MustCompile(`\b(\d+)\b`)
)

var abbreviations = map[string]string{
	"Dr.": "Doctor", "Mr.": "Mister", "Mrs.": "Misses", "Ms.": "Ms",
	"Jr.": "Junior", "Sr.": "Senior", "St.": "Saint",
	"vs.": "versus", "etc.": "etcetera", "approx.": "approximately",
	"dept.": "department", "govt.": "government",
}

var onesWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
var tensWords = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

func spokenNumber(n int) string {
	if n < 0 {
		return "negative " + spokenNumber(-n)
	}
	if n < 20 {
		return onesWords[n]
	}
	if n < 100 {
		w := tensWords[n/10]
		if n%10 != 0 {
			w += " " + onesWords[n%10]
		}
		return w
	}
	if n < 1000 {
		w := onesWords[n/100] + " hundred"
		if n%100 != 0 {
			w += " " + spokenNumber(n%100)
		}
		return w
	}
	if n < 1000000 {
		w := spokenNumber(n/1000) + " thousand"
		if n%1000 != 0 {
			w += " " + spokenNumber(n%1000)
		}
		return w
	}
	if n < 1000000000 {
		w := spokenNumber(n/1000000) + " million"
		if n%1000000 != 0 {
			w += " " + spokenNumber(n%1000000)
		}
		return w
	}
	w := spokenNumber(n/1000000000) + " billion"
	if n%1000000000 != 0 {
		w += " " + spokenNumber(n%1000000000)
	}
	return w
}

// NormalizeForSpeech expands numbers, currency, and abbreviations for natural TTS output.
func NormalizeForSpeech(s string) string {
	// Abbreviations
	for abbr, expanded := range abbreviations {
		s = strings.ReplaceAll(s, abbr, expanded)
	}

	// Currency: $12.50 → twelve dollars and fifty cents
	s = normCurrency.ReplaceAllStringFunc(s, func(m string) string {
		parts := normCurrency.FindStringSubmatch(m)
		dollars, _ := parseInt(parts[1])
		cents, _ := parseInt(parts[2])
		result := spokenNumber(dollars) + " dollars"
		if cents > 0 {
			result += " and " + spokenNumber(cents) + " cents"
		}
		return result
	})

	// Percentages: 45.5% → forty five point five percent
	s = normPercent.ReplaceAllStringFunc(s, func(m string) string {
		parts := normPercent.FindStringSubmatch(m)
		numStr := parts[1]
		if idx := strings.Index(numStr, "."); idx >= 0 {
			whole, _ := parseInt(numStr[:idx])
			return spokenNumber(whole) + " point " + numStr[idx+1:] + " percent"
		}
		n, _ := parseInt(numStr)
		return spokenNumber(n) + " percent"
	})

	// Comma-separated numbers: 1,000,000 → 1000000, then number expansion handles it
	s = normLargeNum.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ReplaceAll(m, ",", "")
	})

	// Plain numbers (up to 999 billion)
	s = normNumber.ReplaceAllStringFunc(s, func(m string) string {
		n, err := parseInt(m)
		if err != nil || n > 999999999999 {
			return m
		}
		return spokenNumber(n)
	})

	return s
}

func parseInt(s string) (int, error) {
	var n int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}

// codeFilter strips markdown code fences (```) from a token stream.
// Text inside fences is omitted; text outside is returned verbatim.
type codeFilter struct {
	inBlock   bool
	pending   int // consecutive backticks seen so far
}

// Filter returns the portion of token that is outside code fences.
func (f *codeFilter) Filter(token string) string {
	var out strings.Builder
	for i := range len(token) {
		ch := token[i]
		if ch == '`' {
			f.pending++
			if f.pending == 3 {
				f.inBlock = !f.inBlock
				f.pending = 0
			}
			continue
		}
		// flush non-fence backticks (e.g. inline code)
		if f.pending > 0 && !f.inBlock {
			for range f.pending {
				out.WriteByte('`')
			}
		}
		f.pending = 0
		if f.inBlock {
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}
