package genui

import (
	"regexp"
	"strings"
)

// A screen travels inside a model's chat reply as a fenced block:
//
//	```openui
//	root = Stack([...])
//	...
//	```
//
// Text before and after the block stays ordinary chat text.

// FenceTag is the info string that marks a screen block.
const FenceTag = "openui"

var fenceRe = regexp.MustCompile("(?s)```" + FenceTag + "[ \t]*\r?\n(.*?)(?:\r?\n)?```")

// Extract returns the source of every screen block in a reply, in order,
// and the reply text with the blocks removed. An unclosed block at the end
// counts as a block: the model was cut off, the lines it wrote are kept.
func Extract(content string) (sources []string, rest string) {
	for _, m := range fenceRe.FindAllStringSubmatch(content, -1) {
		sources = append(sources, m[1])
	}
	rest = fenceRe.ReplaceAllString(content, "")
	if i := strings.Index(rest, "```"+FenceTag); i >= 0 {
		tail := rest[i+len("```"+FenceTag):]
		if nl := strings.IndexByte(tail, '\n'); nl >= 0 {
			sources = append(sources, tail[nl+1:])
		}
		rest = rest[:i]
	}
	return sources, strings.TrimSpace(rest)
}

// Stream follows a reply as it arrives in chunks and yields every finished
// line inside a screen block (§10.2 step 2). A line is finished by its
// newline; the closing fence ends the block. Not safe for concurrent use.
type Stream struct {
	buf    strings.Builder
	in     bool
	lineNo int // line number inside the current block, from 1
	block  int // index of the current block, from 0
}

// StreamLine is one finished line of a screen block.
type StreamLine struct {
	Block int
	Line  int
	Text  string
}

// Feed adds a chunk and returns the lines it finished.
func (s *Stream) Feed(chunk string) []StreamLine {
	s.buf.WriteString(chunk)
	data := s.buf.String()
	var out []StreamLine
	for {
		nl := strings.IndexByte(data, '\n')
		if nl < 0 {
			break
		}
		line := strings.TrimRight(data[:nl], "\r")
		data = data[nl+1:]
		trimmed := strings.TrimSpace(line)
		switch {
		case !s.in && strings.HasPrefix(trimmed, "```"+FenceTag) && strings.TrimSpace(trimmed[len("```"+FenceTag):]) == "":
			s.in, s.lineNo = true, 0
		case s.in && strings.HasPrefix(trimmed, "```"):
			s.in = false
			s.block++
		case s.in:
			s.lineNo++
			out = append(out, StreamLine{Block: s.block, Line: s.lineNo, Text: line})
		}
	}
	s.buf.Reset()
	s.buf.WriteString(data)
	return out
}

// InBlock reports whether the stream is inside an open screen block.
func (s *Stream) InBlock() bool { return s.in }

// StripForDisplay removes screen blocks — closed or still open — from text
// that is shown as plain chat (Telegram partials and the final reply).
func StripForDisplay(content string) string {
	_, rest := Extract(content)
	return rest
}
