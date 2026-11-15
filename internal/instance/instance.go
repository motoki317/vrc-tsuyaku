// Package instance records live instances in locked files under a data directory.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const slotCount = 8

// Info identifies the owner of a locked slot. Slot comes from the file name.
type Info struct {
	Slot    int       `json:"-"`
	URL     string    `json:"url"`
	PID     int       `json:"pid"`
	Host    string    `json:"host"`
	Started time.Time `json:"started"`
}

// Membership holds one slot until Close or process exit. Do not copy it.
type Membership struct {
	mu   sync.Mutex
	dir  string
	slot int
	file *os.File
}

// Join takes a free slot in dir/instances, records info in it, and returns the
// other live instances. On error it holds no slot.
func Join(dir string, info Info) (*Membership, []Info, error) {
	dir = filepath.Join(dir, "instances")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	m := &Membership{dir: dir, slot: -1}
	others, err := m.scan(&info)
	if err != nil {
		return nil, nil, errors.Join(err, m.Close())
	}
	return m, others, nil
}

// Others lists the other live instances now. After Close it returns os.ErrClosed.
func (m *Membership) Others() ([]Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil, os.ErrClosed
	}
	return m.scan(nil)
}

// Close releases the slot. Repeated calls do nothing.
func (m *Membership) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil
	}
	err := unlockClose(m.file)
	m.file = nil
	return err
}

// The guard keeps probes of free slots from looking like lasting owners. It also
// keeps a new owner's incomplete JSON out of another instance's listing.
func (m *Membership) scan(claim *Info) (others []Info, err error) {
	guard, err := os.OpenFile(filepath.Join(m.dir, "guard.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := lockFile(guard, true); err != nil {
		return nil, errors.Join(err, guard.Close())
	}
	defer func() {
		// Release a failed claim before another scan can read its incomplete JSON.
		if err != nil && claim != nil {
			err = errors.Join(err, m.Close())
		}
		err = errors.Join(err, unlockClose(guard))
	}()

	for slot := range slotCount {
		if slot == m.slot {
			continue
		}
		flags := os.O_RDWR
		if claim != nil && m.file == nil {
			flags |= os.O_CREATE
		}
		file, err := os.OpenFile(filepath.Join(m.dir, strconv.Itoa(slot)+".lock"), flags, 0o600)
		if errors.Is(err, os.ErrNotExist) && flags&os.O_CREATE == 0 {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked, err := lockFile(file, false)
		if err != nil {
			return nil, errors.Join(err, file.Close())
		}
		if !locked {
			var info Info
			err := json.NewDecoder(file).Decode(&info)
			err = errors.Join(err, file.Close())
			if err != nil {
				return nil, fmt.Errorf("read instance slot %d: %w", slot, err)
			}
			info.Slot = slot
			others = append(others, info)
		} else if claim != nil && m.file == nil {
			m.slot, m.file = slot, file
			if err := file.Truncate(0); err != nil {
				return nil, err
			}
			if err := json.NewEncoder(file).Encode(claim); err != nil {
				return nil, fmt.Errorf("write instance slot %d: %w", slot, err)
			}
		} else if err := unlockClose(file); err != nil {
			return nil, err
		}
	}
	if claim != nil && m.file == nil {
		return nil, fmt.Errorf("instance registry is full (%d slots)", slotCount)
	}
	return others, nil
}

func unlockClose(file *os.File) error {
	return errors.Join(unlockFile(file), file.Close())
}
