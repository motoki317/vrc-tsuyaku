package main

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const repository = "https://github.com/motoki317/vrc-tsuyaku"

type appInfo struct {
	Launch     string `json:"launch"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
	Revision   string `json:"revision"`
	Time       string `json:"time"`
	Modified   bool   `json:"modified"`
	rawVersion string
}

func appBuildInfo(build *debug.BuildInfo) appInfo {
	info := appInfo{Repository: repository, rawVersion: "(devel)"}
	if build == nil {
		return info
	}
	info.rawVersion = build.Main.Version
	version := strings.TrimSuffix(info.rawVersion, "+dirty")
	if semver.IsValid(version) && !module.IsPseudoVersion(version) {
		info.Version = version
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.time":
			info.Time = setting.Value
		case "vcs.modified":
			info.Modified = setting.Value == "true"
		}
	}
	return info
}
