package main

import "testing"

func TestInsideWindowsTemp(t *testing.T) {
	const temp = `C:\Users\Player Name\AppData\Local\Temp\`
	for _, tc := range []struct {
		name, dir, temp string
		want            bool
	}{
		{"inside", `C:\Users\Player Name\AppData\Local\Temp\Temp1_archive`, temp, true},
		{"different case", `c:\users\PLAYER NAME\appdata\LOCAL\temp\Temp1_archive`, temp, true},
		// GetLongPathName resolves C:\Users\PLAYER~1 before the comparison.
		{"resolved 8.3 form", `C:\Users\Player Name\AppData\Local\Temp\Temp1_archive`, `C:\Users\Player Name\AppData\Local\Temp`, true},
		{"sibling with shared prefix", `C:\Users\Player Name\AppData\Local\Temporary`, temp, false},
		{"outside", `C:\Users\Player Name\Desktop\VRC Tsuyaku`, temp, false},
		{"temp itself", `C:\Users\Player Name\AppData\Local\Temp`, temp, true},
		{"different drive", `D:\Users\Player Name\AppData\Local\Temp\app`, temp, false},
		{"forward slashes", `C:/Users/Player Name/AppData/Local/Temp/app/`, temp, true},
		{"dot segments outside", `C:\Users\Player Name\AppData\Local\Temp\..\app`, temp, false},
		{"extended path", `\\?\C:\Users\Player Name\AppData\Local\Temp\app`, temp, true},
		{"extended temp", `C:\Users\Player Name\AppData\Local\Temp\app`, `\\?\C:\Users\Player Name\AppData\Local\Temp\`, true},
		{"UNC", `\\server\share\temp\app`, `\\SERVER\share\temp\`, true},
		{"extended UNC", `\\?\UNC\server\share\temp\app`, `\\server\share\temp`, true},
		{"drive root", `C:\app`, `C:\`, true},
		{"empty path", "", temp, false},
		{"empty temp", `C:\app`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := insideWindowsTemp(tc.dir, tc.temp); got != tc.want {
				t.Fatalf("insideWindowsTemp(%q, %q) = %v, want %v", tc.dir, tc.temp, got, tc.want)
			}
		})
	}
}

func TestTemporaryFolderMessage(t *testing.T) {
	const english = `VRC Tsuyaku cannot run from inside a zip file or from a temporary folder. Right-click the zip file, choose "Extract All", and then start vrc-tsuyaku.exe in the extracted folder.`
	const japanese = "VRC Tsuyakuは、zipファイルの中や一時フォルダーからは起動できません。zipファイルを右クリックして「すべて展開」を選び、展開したフォルダーのvrc-tsuyaku.exeを起動してください。"
	for _, tc := range []struct {
		name      string
		languages []string
		want      string
	}{
		{"Japanese", []string{"ja-JP", "en-US"}, japanese},
		{"Japanese neutral", []string{"ja"}, japanese},
		{"Japanese different case", []string{"JA-jp"}, japanese},
		{"English before Japanese", []string{"en-US", "ja-JP"}, english},
		{"other language", []string{"fr-FR"}, english},
		{"not a Japanese tag", []string{"japanese"}, english},
		{"empty first language", []string{"", "ja-JP"}, english},
		{"no languages", nil, english},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := temporaryFolderMessage(tc.languages); got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
	}
}
