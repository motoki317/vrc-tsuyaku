package main

import (
	"fmt"
	"io"
)

const (
	consoleBrowserOpened = "The page is open in your browser. If you do not see it, open this address in a browser:\n" +
		"操作画面をブラウザーで開きました。表示されないときは、次のアドレスをブラウザーで開いてください。"
	consoleBrowserFailed = "The app could not open a browser. Open this address in a browser:\n" +
		"ブラウザーを開けませんでした。次のアドレスをブラウザーで開いてください。"
	consoleExitHint = "The app exits when you close the browser tab or this window.\n" +
		"ブラウザーのタブか、このウィンドウを閉じると、終了します。"
	consoleExiting = "Exiting...\n終了しています..."
)

func printStartup(out io.Writer, version, url string, browserOpened bool) {
	name := "VRC Tsuyaku"
	if version != "" {
		name += " " + version
	}
	message := consoleBrowserOpened
	if !browserOpened {
		message = consoleBrowserFailed
	}
	fmt.Fprintf(out, "%s\n%s\n\n    %s\n\n%s\n", name, message, url, consoleExitHint)
}
