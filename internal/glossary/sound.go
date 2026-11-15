package glossary

import "strings"

func asciiLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func soundWordRune(r rune) bool {
	return asciiLetter(r) || r == '\''
}

func soundKey(word string) string {
	word = strings.Map(func(r rune) rune {
		if asciiLetter(r) {
			return r
		}
		return -1
	}, strings.ToLower(word))
	letters := []byte(word)
	for i, letter := range letters {
		if letter == 'c' && i+1 < len(letters) && strings.ContainsRune("eiy", rune(letters[i+1])) {
			letters[i] = 's'
		}
	}
	word = string(letters)
	for _, pair := range [][2]string{{"ph", "f"}, {"ck", "k"}, {"c", "k"}, {"y", "i"}, {"z", "s"}, {"ee", "i"}, {"ea", "i"}, {"oo", "u"}, {"ou", "u"}} {
		word = strings.ReplaceAll(word, pair[0], pair[1])
	}
	letters = []byte(word)
	length := 0
	for _, letter := range letters {
		if length == 0 || letters[length-1] != letter {
			letters[length] = letter
			length++
		}
	}
	return string(letters[:length])
}
