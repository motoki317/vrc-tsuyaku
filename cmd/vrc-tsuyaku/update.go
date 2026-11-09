package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

var latestReleaseURL = "https://api.github.com/repos/" + strings.TrimPrefix(repository, "https://github.com/") + "/releases/latest"

type updateState struct {
	State   string `json:"state"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

func (a *app) checkUpdate(releaseURL string, client *http.Client) {
	a.updateWG.Go(func() {
		update := fetchUpdate(a.ctx, releaseURL, client, a.info.rawVersion)
		a.mu.Lock()
		a.update = update
		a.mu.Unlock()
		a.emit("", "update", update.State)
	})
}

func fetchUpdate(ctx context.Context, releaseURL string, client *http.Client, version string) updateState {
	failed := updateState{State: "failed"}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return failed
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vrc-tsuyaku/"+version)
	requestClient := *client
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := requestClient.Do(req)
	if err != nil {
		return failed
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return updateState{State: "current"}
	}
	if resp.StatusCode != http.StatusOK {
		return failed
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return failed
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil || !semver.IsValid(release.TagName) {
		return failed
	}
	version = strings.TrimSuffix(version, "+dirty")
	if !semver.IsValid(version) {
		version = "v0.0.0"
	}
	if semver.Compare(release.TagName, version) > 0 {
		return updateState{State: "available", Version: release.TagName, URL: release.HTMLURL}
	}
	return updateState{State: "current"}
}
