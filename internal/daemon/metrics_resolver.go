package daemon

import (
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/metrics"
	"github.com/vmsmith/vmsmith/internal/store"
)

// storeNameResolver maps libvirt domain names → vmsmith VM IDs by listing
// the store, with a short-lived cache so the metrics sampler does not pay
// a bbolt scan on every libvirt domain in every sample round.
type storeNameResolver struct {
	store *store.Store
	ttl   time.Duration

	mu       sync.RWMutex
	cache    map[string]string
	cachedAt time.Time
}

// resolverCacheTTL is how long the name → ID cache is reused before a refresh.
// Short enough that newly-created VMs surface within a sample interval.
const resolverCacheTTL = 30 * time.Second

func newStoreNameResolver(s *store.Store) metrics.NameToIDFunc {
	r := &storeNameResolver{store: s, ttl: resolverCacheTTL}
	return r.NameToID
}

func (r *storeNameResolver) NameToID(name string) (string, bool) {
	r.mu.RLock()
	if r.cache != nil && time.Since(r.cachedAt) < r.ttl {
		id, ok := r.cache[name]
		r.mu.RUnlock()
		return id, ok
	}
	r.mu.RUnlock()

	r.refresh()

	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.cache[name]
	return id, ok
}

func (r *storeNameResolver) refresh() {
	vms, err := r.store.ListVMs()
	if err != nil {
		// On error keep the existing cache so we don't drop everything.
		return
	}
	cache := make(map[string]string, len(vms))
	for _, vm := range vms {
		cache[vm.Name] = vm.ID
	}
	r.mu.Lock()
	r.cache = cache
	r.cachedAt = time.Now()
	r.mu.Unlock()
}
