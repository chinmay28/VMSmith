package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
	bolt "go.etcd.io/bbolt"
)

// Store provides persistent metadata storage backed by bbolt.
type Store struct {
	db *bolt.DB
}

// New opens or creates a bbolt database at the given path.
func New(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("opening bolt db: %w", err)
	}

	// Ensure all buckets exist
	err = db.Update(func(tx *bolt.Tx) error {
		buckets := []string{BucketVMs, BucketImages, BucketTemplates, BucketSnapshots, BucketPortForwards, BucketConfig, BucketEvents}
		for _, b := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing buckets: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- VM operations ---

// PutVM stores a VM record.
func (s *Store) PutVM(vm *types.VM) error {
	return s.put(BucketVMs, vm.ID, vm)
}

// GetVM retrieves a VM by ID.
func (s *Store) GetVM(id string) (*types.VM, error) {
	var vm types.VM
	if err := s.get(BucketVMs, id, &vm); err != nil {
		return nil, err
	}
	return &vm, nil
}

// ListVMs returns all stored VMs.
func (s *Store) ListVMs() ([]*types.VM, error) {
	var vms []*types.VM
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketVMs))
		return b.ForEach(func(k, v []byte) error {
			var vm types.VM
			if err := json.Unmarshal(v, &vm); err != nil {
				return err
			}
			vms = append(vms, &vm)
			return nil
		})
	})
	return vms, err
}

// DeleteVM removes a VM record.
func (s *Store) DeleteVM(id string) error {
	return s.delete(BucketVMs, id)
}

// --- Image operations ---

// PutImage stores an image record.
func (s *Store) PutImage(img *types.Image) error {
	return s.put(BucketImages, img.ID, img)
}

// GetImage retrieves an image by ID.
func (s *Store) GetImage(id string) (*types.Image, error) {
	var img types.Image
	if err := s.get(BucketImages, id, &img); err != nil {
		return nil, err
	}
	return &img, nil
}

// ListImages returns all stored images.
func (s *Store) ListImages() ([]*types.Image, error) {
	var imgs []*types.Image
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketImages))
		return b.ForEach(func(k, v []byte) error {
			var img types.Image
			if err := json.Unmarshal(v, &img); err != nil {
				return err
			}
			imgs = append(imgs, &img)
			return nil
		})
	})
	return imgs, err
}

// DeleteImage removes an image record.
func (s *Store) DeleteImage(id string) error {
	return s.delete(BucketImages, id)
}

// --- Template operations ---

// PutTemplate stores a VM template record.
func (s *Store) PutTemplate(tpl *types.VMTemplate) error {
	return s.put(BucketTemplates, tpl.ID, tpl)
}

// GetTemplate retrieves a VM template by ID.
func (s *Store) GetTemplate(id string) (*types.VMTemplate, error) {
	var tpl types.VMTemplate
	if err := s.get(BucketTemplates, id, &tpl); err != nil {
		return nil, err
	}
	return &tpl, nil
}

// ListTemplates returns all stored VM templates.
func (s *Store) ListTemplates() ([]*types.VMTemplate, error) {
	var templates []*types.VMTemplate
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketTemplates))
		return b.ForEach(func(k, v []byte) error {
			var tpl types.VMTemplate
			if err := json.Unmarshal(v, &tpl); err != nil {
				return err
			}
			templates = append(templates, &tpl)
			return nil
		})
	})
	return templates, err
}

// DeleteTemplate removes a VM template record.
func (s *Store) DeleteTemplate(id string) error {
	return s.delete(BucketTemplates, id)
}

// --- Port forward operations ---

// PutPortForward stores a port forward rule.
func (s *Store) PutPortForward(pf *types.PortForward) error {
	return s.put(BucketPortForwards, pf.ID, pf)
}

// ListPortForwards returns all port forwards, optionally filtered by VM ID.
func (s *Store) ListPortForwards(vmID string) ([]*types.PortForward, error) {
	var pfs []*types.PortForward
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketPortForwards))
		return b.ForEach(func(k, v []byte) error {
			var pf types.PortForward
			if err := json.Unmarshal(v, &pf); err != nil {
				return err
			}
			if vmID == "" || pf.VMID == vmID {
				pfs = append(pfs, &pf)
			}
			return nil
		})
	})
	return pfs, err
}

// DeletePortForward removes a port forward rule.
func (s *Store) DeletePortForward(id string) error {
	return s.delete(BucketPortForwards, id)
}

// --- generic helpers ---

func (s *Store) put(bucket, key string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func (s *Store) get(bucket, key string, dest any) error {
	return s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		v := b.Get([]byte(key))
		if v == nil {
			return fmt.Errorf("%s/%s: not found", bucket, key)
		}
		return json.Unmarshal(v, dest)
	})
}

func (s *Store) delete(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		return b.Delete([]byte(key))
	})
}

// --- Events ---

// PutEvent stores an event using its string ID as the key.
// Deprecated: use AppendEvent for new code — it uses ordered uint64 keys.
func (s *Store) PutEvent(event *types.Event) error {
	return s.put(BucketEvents, event.ID, event)
}

// AppendEvent assigns the next monotonic sequence number to the event (via
// bbolt's per-bucket NextSequence), writes it with a big-endian uint64 key
// (ensuring chronological order on forward cursor scan), and returns the
// assigned sequence number.  The event's ID field is set to the string form
// of the sequence number before writing.
func (s *Store) AppendEvent(event *types.Event) (uint64, error) {
	var seq uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))
		var err error
		seq, err = b.NextSequence()
		if err != nil {
			return err
		}
		event.ID = fmt.Sprintf("%d", seq)
		key := encodeSeqKey(seq)
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return b.Put(key[:], data)
	})
	return seq, err
}

// EventFilter controls which events ListEventsFiltered returns.
//
// Since may be either a time.Time (lower bound on OccurredAt) or a uint64
// sequence ID (lower bound on event sequence, exclusive).  Zero values are
// treated as no bound.
type EventFilter struct {
	VMID     string
	Type     string
	Source   string
	Severity string
	Since    interface{} // time.Time or uint64 seq; zero/0 = no lower bound
	UntilSeq uint64      // exclusive upper bound on seq ID; 0 = no upper bound
	Page     int
	PerPage  int
}

// ListEventsFiltered returns events matching filter, ordered newest-first.
// Only events written by AppendEvent (big-endian uint64 keys) are returned.
// Returns the filtered slice and the total matching count (before pagination).
func (s *Store) ListEventsFiltered(filter EventFilter) ([]*types.Event, int, error) {
	var all []*types.Event

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))
		c := b.Cursor()

		// Walk in reverse order (newest first).
		// Start from UntilSeq-1 if set, otherwise from the last key.
		var startKey []byte
		if filter.UntilSeq > 0 {
			k := encodeSeqKey(filter.UntilSeq - 1)
			startKey = k[:]
		}

		var k, v []byte
		if startKey != nil {
			// SeekLT: seek to the largest key ≤ startKey.
			k, v = c.Seek(startKey)
			if k == nil {
				k, v = c.Last()
			} else if string(k) != string(startKey) {
				k, v = c.Prev()
			}
		} else {
			k, v = c.Last()
		}

		for ; k != nil; k, v = c.Prev() {
			// Skip non-uint64 keys (legacy string-keyed events).
			if len(k) != 8 {
				continue
			}
			seq := decodeSeqKey(k)

			// Seq-based Since: stop walking once we drop below sinceSeq
			// (because the cursor walks newest → oldest).
			if sinceSeq, ok := filter.Since.(uint64); ok && sinceSeq > 0 {
				if seq <= sinceSeq {
					break
				}
			}

			var evt types.Event
			if err := json.Unmarshal(v, &evt); err != nil {
				continue // skip corrupt entries
			}

			// Apply filters.
			if filter.VMID != "" && evt.VMID != filter.VMID {
				continue
			}
			if filter.Type != "" && evt.Type != filter.Type {
				continue
			}
			if filter.Source != "" && evt.Source != filter.Source {
				continue
			}
			if filter.Severity != "" && evt.Severity != filter.Severity {
				continue
			}
			// Time-based Since: filter individual events by OccurredAt.
			if sinceTime, ok := filter.Since.(time.Time); ok && !sinceTime.IsZero() {
				ts := evt.OccurredAt
				if ts.IsZero() {
					ts = evt.CreatedAt
				}
				if !ts.After(sinceTime) {
					continue
				}
			}

			all = append(all, &evt)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	total := len(all)

	// Apply pagination.
	if filter.PerPage > 0 {
		page := filter.Page
		if page < 1 {
			page = 1
		}
		start := (page - 1) * filter.PerPage
		if start >= len(all) {
			all = all[:0]
		} else {
			end := start + filter.PerPage
			if end > len(all) {
				end = len(all)
			}
			all = all[start:end]
		}
	}

	return all, total, nil
}

// ListEventsAfterSeq returns up to limit events with seq ID strictly greater
// than afterSeq, in chronological order (oldest first).  Used for SSE replay.
func (s *Store) ListEventsAfterSeq(afterSeq uint64, limit int) ([]*types.Event, error) {
	var events []*types.Event
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))
		c := b.Cursor()

		// Seek to afterSeq+1.
		seekKey := encodeSeqKey(afterSeq + 1)
		k, v := c.Seek(seekKey[:])
		for ; k != nil; k, v = c.Next() {
			if len(k) != 8 {
				break // hit legacy string keys, stop
			}
			var evt types.Event
			if err := json.Unmarshal(v, &evt); err != nil {
				continue
			}
			events = append(events, &evt)
			if limit > 0 && len(events) >= limit {
				break
			}
		}
		return nil
	})
	return events, err
}

// CountEvents returns the total number of events stored with uint64 keys.
func (s *Store) CountEvents() (int, error) {
	count := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))
		return b.ForEach(func(k, _ []byte) error {
			if len(k) == 8 {
				count++
			}
			return nil
		})
	})
	return count, err
}

// PruneEvents deletes the oldest events (uint64-keyed) until the total count
// is at or below maxRecords.  Returns the number of events deleted.
func (s *Store) PruneEvents(maxRecords int) (int, error) {
	deleted := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))

		// Count all uint64-keyed events.
		var keys [][]byte
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if len(k) == 8 {
				cp := make([]byte, 8)
				copy(cp, k)
				keys = append(keys, cp)
			}
		}

		excess := len(keys) - maxRecords
		if excess <= 0 {
			return nil
		}
		// Delete the oldest (first) excess keys.
		const maxPerSweep = 5000
		if excess > maxPerSweep {
			excess = maxPerSweep
		}
		for _, k := range keys[:excess] {
			if err := b.Delete(k); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return deleted, err
}

// ListEvents returns all events (legacy API — includes both string-keyed and
// uint64-keyed events).  New code should use ListEventsFiltered instead.
func (s *Store) ListEvents() ([]*types.Event, error) {
	var events []*types.Event
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketEvents))
		return b.ForEach(func(k, v []byte) error {
			var event types.Event
			if err := json.Unmarshal(v, &event); err != nil {
				return err
			}
			events = append(events, &event)
			return nil
		})
	})
	return events, err
}

// encodeSeqKey converts a uint64 sequence number to an 8-byte big-endian key.
// Big-endian encoding ensures lexicographic order equals chronological order.
func encodeSeqKey(seq uint64) [8]byte {
	var key [8]byte
	key[0] = byte(seq >> 56)
	key[1] = byte(seq >> 48)
	key[2] = byte(seq >> 40)
	key[3] = byte(seq >> 32)
	key[4] = byte(seq >> 24)
	key[5] = byte(seq >> 16)
	key[6] = byte(seq >> 8)
	key[7] = byte(seq)
	return key
}

// decodeSeqKey converts an 8-byte big-endian key back to uint64.
func decodeSeqKey(key []byte) uint64 {
	return uint64(key[0])<<56 | uint64(key[1])<<48 | uint64(key[2])<<40 |
		uint64(key[3])<<32 | uint64(key[4])<<24 | uint64(key[5])<<16 |
		uint64(key[6])<<8 | uint64(key[7])
}
