package markdown

import (
	"strings"
	"unicode"

	"github.com/incu6us/markdex/internal/domain"
)

const (
	defaultMaxRunes = 2000
	fenceMarker     = "```"
	tildeMarker     = "~~~"
)

type Splitter struct {
	maxRunes int
	overlap  int
}

func NewSplitter(maxRunes, overlap int) *Splitter {
	if maxRunes < 1 {
		maxRunes = defaultMaxRunes
	}
	if overlap < 0 || overlap >= maxRunes {
		overlap = maxRunes / 4
	}
	return &Splitter{maxRunes: maxRunes, overlap: overlap}
}

type lineInfo struct {
	text    string
	heading bool
	level   int
	title   string
}

type piece struct {
	title       string
	headingPath string
	content     string
}

func (s *Splitter) Split(doc domain.Document) ([]domain.Chunk, error) {
	lines := scan(doc.Content())
	pieces := s.splitTopics(lines)

	chunks := make([]domain.Chunk, 0, len(pieces))
	for _, p := range pieces {
		chunk, err := domain.NewChunk(domain.ChunkParams{
			SourceID:    doc.Path(),
			Index:       len(chunks),
			Title:       p.title,
			HeadingPath: p.headingPath,
			Content:     p.content,
		})
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (s *Splitter) splitTopics(lines []lineInfo) []piece {
	var pieces []piece
	emit := func(lo, hi int) {
		if lo >= hi {
			return
		}
		if head := lines[lo]; head.heading && head.level == 1 {
			pieces = append(pieces, s.splitRange(lines, lo, hi, head.title, slug(head.title), 1)...)
			return
		}
		pieces = append(pieces, s.splitRange(lines, lo, hi, "", "", 1)...)
	}

	segStart := 0
	for i := range lines {
		if i == segStart || !lines[i].heading || lines[i].level != 1 {
			continue
		}
		emit(segStart, i)
		segStart = i
	}
	emit(segStart, len(lines))
	return pieces
}

func (s *Splitter) splitRange(lines []lineInfo, lo, hi int, title, headingPath string, ownLevel int) []piece {
	text := strings.TrimSpace(join(lines, lo, hi))
	if text == "" || !hasBody(lines, lo, hi) {
		return nil
	}
	if runeCount(text) <= s.maxRunes {
		return []piece{{title: title, headingPath: headingPath, content: text}}
	}

	childLevel := shallowestHeading(lines, lo, hi, ownLevel)
	if childLevel == 0 {
		return s.window(text, title, headingPath)
	}

	var pieces []piece
	segStart := lo
	for i := lo; i < hi; i++ {
		if i == segStart || !lines[i].heading || lines[i].level != childLevel {
			continue
		}
		pieces = append(pieces, s.splitSegment(lines, segStart, i, title, headingPath, ownLevel)...)
		segStart = i
	}
	pieces = append(pieces, s.splitSegment(lines, segStart, hi, title, headingPath, ownLevel)...)
	return pieces
}

func (s *Splitter) splitSegment(lines []lineInfo, lo, hi int, title, headingPath string, ownLevel int) []piece {
	if lo >= hi {
		return nil
	}
	head := lines[lo]
	if !head.heading || head.level == ownLevel {
		return s.splitRange(lines, lo, hi, title, headingPath, ownLevel)
	}

	segTitle := title
	if head.level == 1 {
		segTitle = head.title
	}
	return s.splitRange(lines, lo, hi, segTitle, appendSlug(headingPath, slug(head.title)), head.level)
}

func (s *Splitter) window(text, title, headingPath string) []piece {
	runes := []rune(text)
	step := s.maxRunes - s.overlap
	if step < 1 {
		step = s.maxRunes
	}

	var pieces []piece
	for start := 0; start < len(runes); start += step {
		end := min(start+s.maxRunes, len(runes))
		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			pieces = append(pieces, piece{title: title, headingPath: headingPath, content: content})
		}
		if end == len(runes) {
			break
		}
	}
	return pieces
}

func scan(content string) []lineInfo {
	raw := strings.Split(content, "\n")
	lines := make([]lineInfo, len(raw))
	inFence := false
	for i, text := range raw {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, fenceMarker) || strings.HasPrefix(trimmed, tildeMarker) {
			inFence = !inFence
			lines[i] = lineInfo{text: text}
			continue
		}
		if inFence {
			lines[i] = lineInfo{text: text}
			continue
		}
		level, headingTitle, ok := parseHeading(text)
		lines[i] = lineInfo{text: text, heading: ok, level: level, title: headingTitle}
	}
	return lines
}

func parseHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	if level >= len(trimmed) || (trimmed[level] != ' ' && trimmed[level] != '\t') {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

func shallowestHeading(lines []lineInfo, lo, hi, ownLevel int) int {
	level := 0
	for i := lo; i < hi; i++ {
		if !lines[i].heading || lines[i].level <= ownLevel {
			continue
		}
		if level == 0 || lines[i].level < level {
			level = lines[i].level
		}
	}
	return level
}

func hasBody(lines []lineInfo, lo, hi int) bool {
	for i := lo; i < hi; i++ {
		if !lines[i].heading && strings.TrimSpace(lines[i].text) != "" {
			return true
		}
	}
	return false
}

func join(lines []lineInfo, lo, hi int) string {
	parts := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		parts = append(parts, lines[i].text)
	}
	return strings.Join(parts, "\n")
}

func runeCount(s string) int {
	return len([]rune(s))
}

func appendSlug(prefix, s string) string {
	switch {
	case s == "":
		return prefix
	case prefix == "":
		return s
	default:
		return prefix + "/" + s
	}
}

func slug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
