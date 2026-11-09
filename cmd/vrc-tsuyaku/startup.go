package main

import (
	"path"
	"strings"
)

func insideWindowsTemp(dir, temp string) bool {
	if dir == "" || temp == "" {
		return false
	}
	dir, temp = cleanWindowsPath(dir), strings.TrimRight(cleanWindowsPath(temp), "/")
	return strings.EqualFold(dir, temp) ||
		(len(dir) > len(temp) && dir[len(temp)] == '/' && strings.EqualFold(dir[:len(temp)], temp))
}

func cleanWindowsPath(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "//?/") {
		name = name[4:]
		if len(name) >= 4 && strings.EqualFold(name[:4], "UNC/") {
			name = "//" + name[4:]
		}
	}
	return path.Clean(name)
}

func temporaryFolderMessage(languages []string) string {
	if len(languages) > 0 {
		language, _, _ := strings.Cut(languages[0], "-")
		if strings.EqualFold(language, "ja") {
			return "VRC Tsuyakuは、zipファイルの中や一時フォルダーからは起動できません。zipファイルを右クリックして「すべて展開」を選び、展開したフォルダーのvrc-tsuyaku.exeを起動してください。"
		}
	}
	return `VRC Tsuyaku cannot run from inside a zip file or from a temporary folder. Right-click the zip file, choose "Extract All", and then start vrc-tsuyaku.exe in the extracted folder.`
}
