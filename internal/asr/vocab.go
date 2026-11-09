// Reads the onnx-asr vocabulary format (MIT), https://github.com/istupakov/onnx-asr,
// and reimplements SentencePiece decoding (Apache-2.0),
// https://github.com/google/sentencepiece. No upstream source code is copied.
// See ../../THIRD_PARTY_NOTICES.md.

package asr

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type vocabulary []string

func readVocabulary(r io.Reader) (vocabulary, error) {
	entries := make(map[int]string)
	s := bufio.NewScanner(r)
	for line := 1; s.Scan(); line++ {
		text := s.Text()
		sep := strings.LastIndexByte(text, ' ')
		if sep <= 0 || !utf8.ValidString(text) {
			return nil, fmt.Errorf("vocabulary line %d: expected <piece> <id>", line)
		}
		piece := text[:sep]
		id, err := strconv.Atoi(text[sep+1:])
		if err != nil || id < 0 || strings.ContainsFunc(piece, unicode.IsSpace) {
			return nil, fmt.Errorf("vocabulary line %d: invalid piece or ID", line)
		}
		if _, exists := entries[id]; exists {
			return nil, fmt.Errorf("vocabulary line %d: duplicate ID %d", line, id)
		}
		entries[id] = piece
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("read vocabulary: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("vocabulary is empty")
	}
	v := make(vocabulary, len(entries))
	for id := range v {
		piece, ok := entries[id]
		if !ok {
			return nil, fmt.Errorf("vocabulary is missing ID %d", id)
		}
		v[id] = piece
	}
	return v, nil
}

func (v vocabulary) decode(ids []int) (string, error) {
	var b strings.Builder
	for _, id := range ids {
		if id < 0 || id >= len(v) {
			return "", fmt.Errorf("token ID %d outside vocabulary of size %d", id, len(v))
		}
		piece := v[id]
		if len(piece) == 6 && strings.HasPrefix(piece, "<0x") && piece[5] == '>' {
			value, err := strconv.ParseUint(piece[3:5], 16, 8)
			if err == nil {
				b.WriteByte(byte(value))
				continue
			}
		}
		if piece == "<unk>" {
			b.WriteString(" ⁇ ")
		} else {
			b.WriteString(strings.ReplaceAll(piece, "▁", " "))
		}
	}
	// Rune conversion emits one replacement character for each invalid byte.
	return strings.TrimFunc(string([]rune(b.String())), func(r rune) bool {
		// Python's strip also treats these four C0 separators as whitespace.
		return unicode.IsSpace(r) || (r >= '\u001c' && r <= '\u001f')
	}), nil
}
