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

// latestReleaseURL points to upstream even when the app was built from a fork.
var latestReleaseURL = "https://api.github.com/repos/" + strings.TrimPrefix(repository, "https://github.com/") + "/releases/latest"

// updateCheckTimeout bounds how long GitHub can leave the update state unresolved.
const updateCheckTimeout = 10 * time.Second

type updateStatus string

const (
	// updateChecking means the update check has not finished.
	updateChecking updateStatus = "checking"
	// updateCurrent means there is no newer release.
	updateCurrent updateStatus = "current"
	// updateAvailable means a newer release is available.
	updateAvailable updateStatus = "available"
	// updateFailed means the update check failed to determine the latest release.
	updateFailed updateStatus = "failed"
)

type updateState struct {
	State   updateStatus `json:"state"`
	Version string       `json:"version"`
	URL     string       `json:"url"`
}

func (a *app) checkUpdate(releaseURL string, client *http.Client) {
	a.updateWG.Go(func() {
		update := fetchUpdate(a.ctx, releaseURL, client, a.info.rawVersion)
		a.mu.Lock()
		a.update = update
		a.mu.Unlock()
		a.emit("", kindUpdate, string(update.State))
	})
}

func fetchUpdate(ctx context.Context, releaseURL string, client *http.Client, version string) updateState {
	failed := updateState{State: updateFailed}
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
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
	// GitHub returns 404 for the latest-release request when a repository has no release.
	if resp.StatusCode == http.StatusNotFound {
		return updateState{State: updateCurrent}
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
		return updateState{State: updateAvailable, Version: release.TagName, URL: release.HTMLURL}
	}
	return updateState{State: updateCurrent}
}
