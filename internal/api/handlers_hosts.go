package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Multi-host overview endpoint (roadmap 5.5.4).

// HostConnectivityReporter is implemented by managers that track a live
// connection per host (vm.MultiHostManager). Optional: when the VM manager
// does not implement it, per-host reachability is omitted from the
// response.
type HostConnectivityReporter interface {
	HostReachable(ctx context.Context, name string) bool
}

// ListHosts handles GET /api/v1/hosts: one row per configured libvirt
// host (the implicit "local" host first) with the aggregate resources
// allocated to VMs placed on it.
func (s *Server) ListHosts(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	type agg struct {
		count, cpus, ram, disk, gpus int
	}
	perHost := make(map[string]*agg)
	bucket := func(name string) *agg {
		if perHost[name] == nil {
			perHost[name] = &agg{}
		}
		return perHost[name]
	}
	for _, vmRec := range vms {
		host := strings.TrimSpace(vmRec.Spec.Host)
		if host == "" {
			host = config.LocalHostName
		}
		b := bucket(host)
		b.count++
		b.cpus += vmRec.Spec.CPUs
		b.ram += vmRec.Spec.RAMMB
		b.disk += vmRec.Spec.DiskGB
		b.gpus += len(vmRec.Spec.ResolvedGPUs())
	}

	reporter := s.hostReporter
	hasReporter := reporter != nil

	statuses := []types.HostStatus{}
	appendHost := func(name, uri, description string, isDefault bool) {
		st := types.HostStatus{
			Name:        name,
			URI:         uri,
			Description: description,
			Default:     isDefault,
		}
		if b := perHost[name]; b != nil {
			st.VMCount, st.CPUs, st.RAMMB, st.DiskGB, st.GPUs = b.count, b.cpus, b.ram, b.disk, b.gpus
		}
		if hasReporter {
			reachable := reporter.HostReachable(r.Context(), name)
			st.Reachable = &reachable
		}
		statuses = append(statuses, st)
	}

	appendHost(config.LocalHostName, s.localLibvirtURI, "", true)
	for _, h := range s.hostsConfig {
		appendHost(h.Name, h.URI, h.Description, false)
	}

	writeJSON(w, http.StatusOK, statuses)
}

// validSpecHost reports whether the create spec's host is known to this
// daemon: empty (default/local) or one of the configured host names.
func (s *Server) validSpecHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == config.LocalHostName {
		return true
	}
	for _, h := range s.hostsConfig {
		if h.Name == host {
			return true
		}
	}
	return false
}
