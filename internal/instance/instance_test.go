package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testInfo(n int) Info {
	return Info{
		URL:     fmt.Sprintf("http://127.0.0.1:%d/", 50000+n),
		PID:     1000 + n,
		Host:    "test-host",
		Started: time.Date(2026, 11, 15, 12, 0, n, 123456789, time.UTC),
	}
}

func join(t *testing.T, dir string, info Info) (*Membership, []Info) {
	t.Helper()
	m, others, err := Join(dir, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m, others
}

func wantOthers(t *testing.T, m *Membership, want ...Info) {
	t.Helper()
	got, err := m.Others()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Others() = %+v, want %+v", got, want)
	}
}

func readSlot(t *testing.T, dir string, slot int) Info {
	t.Helper()
	path := filepath.Join(dir, "instances", strconv.Itoa(slot)+".lock")
	// This separate handle must read the JSON even on Windows, where byte-range
	// locks deny reads of the bytes they cover.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["url"] == nil || fields["pid"] == nil || fields["host"] == nil || fields["started"] == nil {
		t.Fatalf("slot JSON fields = %v, want url, pid, host and started", fields)
	}
	info.Slot = slot
	return info
}

func TestRegistryCreatesOnlyClaimedSlots(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wantFiles := func(want ...string) {
		t.Helper()
		files, err := os.ReadDir(filepath.Join(dir, "instances"))
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(files))
		for i, file := range files {
			names[i] = file.Name()
		}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("registry files = %v, want %v", names, want)
		}
	}
	first, _ := join(t, dir, testInfo(0))
	wantFiles("0.lock", "guard.lock")
	wantOthers(t, first)
	wantFiles("0.lock", "guard.lock")
	secondInfo := testInfo(1)
	secondInfo.Slot = 1
	second, _ := join(t, dir, secondInfo)
	wantFiles("0.lock", "1.lock", "guard.lock")
	wantOthers(t, first, secondInfo)
	wantOthers(t, second, testInfo(0))
	wantFiles("0.lock", "1.lock", "guard.lock")
}

func TestMembershipLifecycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	firstInfo := testInfo(0)
	firstInfo.Slot = 99
	firstInfo.URL += strings.Repeat("long", 100)
	first, others := join(t, dir, firstInfo)
	if first.slot != 0 || len(others) != 0 {
		t.Fatalf("first Join: slot = %d, others = %+v", first.slot, others)
	}
	firstInfo.Slot = 0
	secondInfo := testInfo(1)
	second, others := join(t, dir, secondInfo)
	if second.slot != 1 || !reflect.DeepEqual(others, []Info{firstInfo}) {
		t.Fatalf("second Join: slot = %d, others = %+v", second.slot, others)
	}
	secondInfo.Slot = 1
	wantOthers(t, first, secondInfo)
	wantOthers(t, second, firstInfo)
	if got := readSlot(t, dir, 0); got != firstInfo {
		t.Fatalf("held slot JSON = %+v, want %+v", got, firstInfo)
	}
	// Join and Others both probe the first slot. Both must leave its owner's lock held.
	probe, err := os.OpenFile(filepath.Join(dir, "instances", "0.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := lockFile(probe, false)
	if locked {
		err = errors.Join(err, unlockFile(probe))
	}
	err = errors.Join(err, probe.Close())
	if err != nil || locked {
		t.Fatalf("held slot probe: locked = %t, err = %v", locked, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if _, err := first.Others(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Others after Close: %v", err)
	}
	wantOthers(t, second)
	replacementInfo := testInfo(2)
	replacement, others := join(t, dir, replacementInfo)
	if replacement.slot != 0 || !reflect.DeepEqual(others, []Info{secondInfo}) {
		t.Fatalf("replacement Join: slot = %d, others = %+v", replacement.slot, others)
	}
	wantOthers(t, second, replacementInfo)
	if got := readSlot(t, dir, 0); got != replacementInfo {
		t.Fatalf("reused slot JSON = %+v, want %+v", got, replacementInfo)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(dir, "instances"))
	if err != nil || len(files) != 3 {
		t.Fatalf("registry retains guard and claimed slots: files = %v, err = %v", files, err)
	}
}

func TestRegistryFull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var members []*Membership
	for i := range 8 {
		m, others := join(t, dir, testInfo(i))
		members = append(members, m)
		if m.slot != i || len(others) != i {
			t.Fatalf("Join %d: slot = %d, others = %+v", i, m.slot, others)
		}
	}
	m, others, err := Join(dir, testInfo(8))
	if err == nil || m != nil || others != nil {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("ninth Join: membership = %v, others = %+v, err = %v", m, others, err)
	}
	if err := members[3].Close(); err != nil {
		t.Fatal(err)
	}
	m, others = join(t, dir, testInfo(8))
	if m.slot != 3 || len(others) != 7 {
		t.Fatalf("Join after full: slot = %d, others = %+v", m.slot, others)
	}
}

func TestJoinErrorsReleaseSlot(t *testing.T) {
	t.Parallel()
	for _, blocker := range []string{"instances", "guard.lock", "1.lock", "invalid held JSON"} {
		t.Run(blocker, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			registry := filepath.Join(dir, "instances")
			info := testInfo(0)
			var unblock func()
			if blocker == "instances" {
				if err := os.WriteFile(registry, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				unblock = func() { t.Helper(); mustRemove(t, registry) }
			} else {
				if err := os.Mkdir(registry, 0o700); err != nil {
					t.Fatal(err)
				}
				switch blocker {
				case "invalid held JSON":
					file, err := os.OpenFile(filepath.Join(registry, "1.lock"), os.O_CREATE|os.O_RDWR, 0o600)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = file.Close() })
					if _, err := file.WriteString("incomplete JSON"); err != nil {
						t.Fatal(err)
					}
					if locked, err := lockFile(file, false); err != nil || !locked {
						t.Fatalf("lock malformed slot: %t, %v", locked, err)
					}
					unblock = func() {
						if err := unlockClose(file); err != nil {
							t.Fatal(err)
						}
					}
				default:
					path := filepath.Join(registry, blocker)
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					unblock = func() { t.Helper(); mustRemove(t, path) }
				}
			}
			m, others, err := Join(dir, info)
			if err == nil || m != nil || others != nil {
				if m != nil {
					_ = m.Close()
				}
				t.Fatalf("Join with %s: membership = %v, others = %+v, err = %v", blocker, m, others, err)
			}
			unblock()
			m, others = join(t, dir, testInfo(1))
			if m.slot != 0 || len(others) != 0 {
				t.Fatalf("failed Join retained a slot: slot = %d, others = %+v", m.slot, others)
			}
		})
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestFailedPublicationDoesNotExposePartialJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	owner, _ := join(t, dir, testInfo(0))
	info := testInfo(1)
	info.Started = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	failed := &Membership{dir: filepath.Join(dir, "instances"), slot: -1}
	t.Cleanup(func() { _ = failed.Close() })
	// Stop at scan's return, before Join's fallback cleanup. Another scan can
	// now acquire the guard, so the failed publication must already be free.
	if _, err := failed.scan(&info); err == nil {
		t.Fatal("scan accepted a timestamp that JSON cannot encode")
	}
	wantOthers(t, owner)
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}
	m, others, err := Join(dir, info)
	if m != nil {
		t.Cleanup(func() { _ = m.Close() })
	}
	if err == nil || m != nil || others != nil {
		t.Fatalf("Join with invalid info: membership = %v, others = %+v, err = %v", m, others, err)
	}
	wantOthers(t, owner)
}

func TestConcurrentJoin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	type result struct {
		member *Membership
		info   Info
		others []Info
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	for i := range 8 {
		go func() {
			<-start
			info := testInfo(i)
			member, others, err := Join(dir, info)
			results <- result{member, info, others, err}
		}()
	}
	close(start)
	bySlot := make(map[int]Info)
	var joined []result
	for range 8 {
		r := <-results
		if r.err != nil {
			t.Error(r.err)
			continue
		}
		t.Cleanup(func() {
			if err := r.member.Close(); err != nil {
				t.Error(err)
			}
		})
		r.info.Slot = r.member.slot
		if _, exists := bySlot[r.info.Slot]; exists {
			t.Errorf("two owners of slot %d", r.info.Slot)
		}
		bySlot[r.info.Slot] = r.info
		joined = append(joined, r)
	}
	for _, r := range joined {
		if len(r.others) != r.info.Slot {
			t.Errorf("slot %d Join saw %d owners, want %d", r.info.Slot, len(r.others), r.info.Slot)
		}
		for _, info := range r.others {
			if want := bySlot[info.Slot]; info != want {
				t.Errorf("Join read incomplete owner: got %+v, want %+v", info, want)
			}
		}
		others, err := r.member.Others()
		if err != nil || len(others) != 7 {
			t.Errorf("Others after concurrent Join = %+v, %v", others, err)
		}
	}
}

func TestGuardBlocksScan(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"Join", "Others"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			m, _ := join(t, dir, testInfo(0))
			guard, err := os.OpenFile(filepath.Join(dir, "instances", "guard.lock"), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = guard.Close() })
			if locked, err := lockFile(guard, true); err != nil || !locked {
				t.Fatalf("take guard: %t, %v", locked, err)
			}
			done := make(chan error, 1)
			go func() {
				if operation == "Others" {
					_, err := m.Others()
					done <- err
					return
				}
				other, _, err := Join(dir, testInfo(1))
				if other != nil {
					err = errors.Join(err, other.Close())
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Errorf("%s returned while another handle held the guard: %v", operation, err)
				if err := unlockClose(guard); err != nil {
					t.Fatal(err)
				}
				return
			case <-time.After(25 * time.Millisecond):
			}
			if err := unlockClose(guard); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scan did not resume after the guard was released")
			}
		})
	}
}
