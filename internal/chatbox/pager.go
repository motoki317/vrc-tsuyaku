// The join in pages reimplements the defaults from VRCT (MIT):
// SEND_MESSAGE_FORMAT_PARTS.translation_first: False (original before translation)
// and its top-level separator: "\n". Paging and sentence breaking are not from VRCT.
// No upstream source code is copied.
// https://github.com/misyaguziya/VRCT/blob/9427520c0ae39406a17ba0f280b1ad2735395d51/src-python/controller.py
// https://github.com/misyaguziya/VRCT/blob/9427520c0ae39406a17ba0f280b1ad2735395d51/src-python/config.py
// See ../../THIRD_PARTY_NOTICES.md.

package chatbox

import (
	"strings"
	"unicode"
)

const (
	maxRunes = 144
	maxLines = 9
)

func pages(original, translation string) []string {
	text := []rune(strings.TrimSpace(original + "\n" + translation))
	var result []string
	for len(text) > 0 {
		end := min(len(text), maxRunes)
		lines := 1
		for i, r := range text[:end] {
			if r == '\n' {
				lines++
				if lines > maxLines {
					end = i
					break
				}
			}
		}
		if end < len(text) {
			sentence, space := 0, 0
			for i, r := range text[:end] {
				switch r {
				case '。', '！', '？', '!', '?':
					sentence = i + 1
				case '.':
					if i+1 == len(text) || unicode.IsSpace(text[i+1]) {
						sentence = i + 1
					}
				}
				if unicode.IsSpace(r) {
					space = i + 1
				}
			}
			if unicode.IsSpace(text[end]) {
				space = end
			}
			if sentence > 0 {
				end = sentence
			} else if space > 0 {
				end = space
			}
		}
		result = append(result, strings.TrimSpace(string(text[:end])))
		text = text[end:]
		for len(text) > 0 && unicode.IsSpace(text[0]) {
			text = text[1:]
		}
	}
	return result
}
