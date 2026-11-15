package main

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/errcode"
	"github.com/motoki317/vrc-tsuyaku/internal/instance"
)

type instanceState struct {
	State  string        `json:"state"`
	Others []instanceRow `json:"others,omitempty"`
}

type instanceRow struct {
	Slot    int          `json:"slot"`
	URL     string       `json:"url"`
	PID     int          `json:"pid"`
	Host    string       `json:"host"`
	Started time.Time    `json:"started"`
	State   string       `json:"state"`
	Error   string       `json:"error"`
	Code    errcode.Code `json:"code"`
}

var (
	errInstancePending  = errcode.With(errcode.InstancePending, errors.New("choose whether to stop the earlier instance or start beside it"))
	errInstanceNotFound = errors.New("instance slot is not listed")
)

func (a *app) joinInstance(url string) error {
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	membership, others, err := instance.Join(a.dir, instance.Info{URL: url, PID: os.Getpid(), Host: host, Started: time.Now()})
	if err != nil {
		return err
	}
	a.membership = membership
	a.instanceHost = host
	if len(others) != 0 {
		a.instance = instanceState{State: "waiting", Others: instanceRows(others, nil)}
	}
	return nil
}

func instanceRows(infos []instance.Info, previous []instanceRow) []instanceRow {
	rows := make([]instanceRow, 0, len(infos))
	for _, info := range infos {
		row := instanceRow{Slot: info.Slot, URL: info.URL, PID: info.PID, Host: info.Host, Started: info.Started}
		for _, old := range previous {
			if old.Slot == info.Slot && old.PID == info.PID {
				row.State, row.Error, row.Code = old.State, old.Error, old.Code
				break
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *app) watchInstances() {
	a.instanceWG.Go(func() {
		ticker := time.NewTicker(a.instancePoll)
		defer ticker.Stop()
		lastError := ""
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				if err := a.refreshInstances(); err != nil {
					if text := err.Error(); text != lastError {
						a.emitError("", err)
						lastError = text
					}
				} else {
					lastError = ""
				}
				a.mu.Lock()
				waiting := a.instance.State == "waiting"
				a.mu.Unlock()
				if !waiting {
					return
				}
			}
		}
	})
}

func (a *app) refreshInstances() error {
	others, err := a.membership.Others()
	if err != nil {
		return err
	}
	if len(others) == 0 {
		a.startInstance()
		return nil
	}
	a.mu.Lock()
	if a.instance.State != "waiting" {
		a.mu.Unlock()
		return nil
	}
	rows := instanceRows(others, a.instance.Others)
	changed := !slices.Equal(rows, a.instance.Others)
	a.instance.Others = rows
	a.mu.Unlock()
	if changed {
		a.emit("", "state", "waiting")
	}
	return nil
}

func (a *app) startInstance() bool {
	a.mu.Lock()
	if a.instance.State != "waiting" || a.ctx.Err() != nil {
		a.mu.Unlock()
		return false
	}
	a.instance = instanceState{}
	a.mu.Unlock()
	a.startEnabled()
	a.emit("", "state", "")
	return true
}

func (a *app) stopInstance(slot int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.instance.State != "waiting" {
		return errors.New("this instance is not waiting")
	}
	for i, row := range a.instance.Others {
		if row.Slot != slot {
			continue
		}
		if row.State == "stopping" {
			return errors.New("instance is already stopping")
		}
		if err := a.ctx.Err(); err != nil {
			return err
		}
		row.State, row.Error, row.Code = "stopping", "", ""
		a.instance.Others[i] = row
		a.emit("", "state", "waiting")
		a.instanceWG.Go(func() { a.stopOther(row) })
		return nil
	}
	return errInstanceNotFound
}

func instanceURL(url string) bool {
	port, ok := strings.CutPrefix(url, "http://127.0.0.1:")
	if !ok || !strings.HasSuffix(port, "/") {
		return false
	}
	port = strings.TrimSuffix(port, "/")
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0
}

func (a *app) stopOther(row instanceRow) {
	if a.instanceHost == "" || row.Host != a.instanceHost {
		a.failInstanceStop(row, nil)
		return
	}
	deadline := time.Now().Add(a.stopGrace)
	if instanceURL(row.URL) {
		req, err := http.NewRequestWithContext(a.ctx, http.MethodPost, row.URL+"api/exit", nil)
		if err == nil {
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			if response, err := client.Do(req); err == nil {
				_ = response.Body.Close()
			}
		}
	}
	free, err := a.waitInstance(row, time.Until(deadline))
	var killErr error
	if !free && err == nil {
		// Refresh under the registry guard immediately before trusting a PID.
		var others []instance.Info
		others, err = a.membership.Others()
		if err == nil {
			free = !holdsInstance(others, row)
			if !free && row.PID > 0 && row.PID != os.Getpid() && a.ctx.Err() == nil {
				killErr = a.kill(row.PID)
			}
			if !free {
				free, err = a.waitInstance(row, a.killGrace)
			}
		}
	}
	if a.ctx.Err() != nil {
		return
	}
	if free {
		if err := a.refreshInstances(); err != nil {
			a.emitError("", err)
		}
		return
	}
	a.failInstanceStop(row, errors.Join(err, killErr))
}

func (a *app) failInstanceStop(row instanceRow, err error) {
	if a.ctx.Err() != nil {
		return
	}
	err = errors.Join(errors.New("instance did not stop: close its console window"), err)
	a.mu.Lock()
	for i, current := range a.instance.Others {
		if current.Slot == row.Slot && current.PID == row.PID && current.Host == row.Host {
			a.instance.Others[i].State = ""
			a.instance.Others[i].Error = err.Error()
			a.instance.Others[i].Code = errcode.InstanceNotStopped
		}
	}
	a.mu.Unlock()
	a.emit("", "state", "waiting")
}

func holdsInstance(others []instance.Info, row instanceRow) bool {
	return slices.ContainsFunc(others, func(info instance.Info) bool {
		return info.Slot == row.Slot && info.PID == row.PID && info.Host == row.Host
	})
}

func (a *app) waitInstance(row instanceRow, grace time.Duration) (bool, error) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(a.instancePoll)
	defer ticker.Stop()
	for {
		if err := a.ctx.Err(); err != nil {
			return false, err
		}
		others, err := a.membership.Others()
		if err != nil {
			return false, err
		}
		if !holdsInstance(others, row) {
			return true, nil
		}
		select {
		case <-a.ctx.Done():
			return false, a.ctx.Err()
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func killProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer process.Release()
	return process.Kill()
}
