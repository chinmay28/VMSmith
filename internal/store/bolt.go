package store

import (
	"encoding/json"
	"fmt"

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
		buckets := []string{BucketVMs, BucketImages, BucketSnapshots, BucketPortForwards, BucketConfig}
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
