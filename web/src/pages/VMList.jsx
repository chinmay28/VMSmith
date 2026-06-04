import { useState, useEffect, useMemo, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Plus, Server, Play, Square, Trash2, MoreVertical, Network, X, CheckSquare, Lock, RotateCcw, RefreshCw, Pause, Zap, Search } from 'lucide-react';
import { vms, images as imagesApi, templates as templatesApi, host as hostApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { useEventStream } from '../hooks/useEventStream';
import { PageHeader, StatusBadge, Modal, EmptyState, Spinner, ErrorBanner, PaginationControls, LiveIndicator } from '../components/Shared';
import { normalizeVMList, safeArray } from '../utils/normalize';

const WINDOWS_MIN_RAM_MB = 4096;
const WINDOWS_MIN_DISK_GB = 64;

function resolveOsType(spec = {}) {
  return String(spec.os_type || '').trim().toLowerCase() === 'windows' ? 'windows' : 'linux';
}

function titleCaseWords(value) {
  return String(value)
    .split(/\s+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

function osBadgeLabel(spec = {}) {
  const osType = resolveOsType(spec);
  if (osType === 'windows') {
    if (spec.os_variant) return titleCaseWords(String(spec.os_variant).replace(/^windows-/, 'Windows ').replace(/-/g, ' '));
    return 'Windows';
  }
  return 'Linux';
}

const VM_LIFECYCLE_TYPES = new Set([
  'vm.created', 'vm.cloned', 'vm.deleted', 'vm.updated',
  'vm.started', 'vm.stopped', 'vm.crashed', 'vm.shutdown',
]);

const DEFAULT_PER_PAGE = 25;

// `<input type="datetime-local">` returns a naive local-time string
// (`YYYY-MM-DDTHH:MM`). Convert to RFC3339 in UTC so the daemon's
// `parseTimeRangeParam` accepts it. Empty / invalid input → empty string
// so the API client drops the param.
function datetimeLocalToISO(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toISOString();
}

export default function VMList() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showCreate, setShowCreate] = useState(searchParams.get('create') === '1');
  // Captured create response when the daemon returned a one-time generated
  // Administrator password. Cleared after the operator dismisses the modal.
  const [generatedPassword, setGeneratedPassword] = useState(null);
  const [actionMenu, setActionMenu] = useState(null);
  const [tagFilter, setTagFilter] = useState('');
  const [searchInput, setSearchInput] = useState(searchParams.get('search') || '');
  const [searchFilter, setSearchFilter] = useState(searchParams.get('search') || '');
  const [imageInput, setImageInput] = useState(searchParams.get('image') || '');
  const [imageFilter, setImageFilter] = useState(searchParams.get('image') || '');
  const [defaultUserInput, setDefaultUserInput] = useState(searchParams.get('default_user') || '');
  const [defaultUserFilter, setDefaultUserFilter] = useState(searchParams.get('default_user') || '');
  const [osTypeFilter, setOsTypeFilter] = useState(searchParams.get('os_type') || '');
  const [osVariantFilter, setOsVariantFilter] = useState(searchParams.get('os_variant') || '');
  const [firmwareFilter, setFirmwareFilter] = useState(searchParams.get('firmware') || '');
  const [diskBusFilter, setDiskBusFilter] = useState(searchParams.get('disk_bus') || '');
  const [networkInput, setNetworkInput] = useState(searchParams.get('network') || '');
  const [networkFilter, setNetworkFilter] = useState(searchParams.get('network') || '');
  const [autoStartFilter, setAutoStartFilter] = useState(searchParams.get('auto_start') || '');
  const [lockedFilter, setLockedFilter] = useState(searchParams.get('locked') || '');
  const [sinceFilter, setSinceFilter] = useState(searchParams.get('since') || '');
  const [untilFilter, setUntilFilter] = useState(searchParams.get('until') || '');
  const [minCpusInput, setMinCpusInput] = useState(searchParams.get('min_cpus') || '');
  const [minCpusFilter, setMinCpusFilter] = useState(searchParams.get('min_cpus') || '');
  const [maxCpusInput, setMaxCpusInput] = useState(searchParams.get('max_cpus') || '');
  const [maxCpusFilter, setMaxCpusFilter] = useState(searchParams.get('max_cpus') || '');
  const [minRamInput, setMinRamInput] = useState(searchParams.get('min_ram_mb') || '');
  const [minRamFilter, setMinRamFilter] = useState(searchParams.get('min_ram_mb') || '');
  const [maxRamInput, setMaxRamInput] = useState(searchParams.get('max_ram_mb') || '');
  const [maxRamFilter, setMaxRamFilter] = useState(searchParams.get('max_ram_mb') || '');
  const [minDiskInput, setMinDiskInput] = useState(searchParams.get('min_disk_gb') || '');
  const [minDiskFilter, setMinDiskFilter] = useState(searchParams.get('min_disk_gb') || '');
  const [maxDiskInput, setMaxDiskInput] = useState(searchParams.get('max_disk_gb') || '');
  const [maxDiskFilter, setMaxDiskFilter] = useState(searchParams.get('max_disk_gb') || '');
  const [selectedIds, setSelectedIds] = useState([]);
  const [bulkMessage, setBulkMessage] = useState(null);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const [sort, setSort] = useState(searchParams.get('sort') || 'id');
  const [order, setOrder] = useState(searchParams.get('order') || 'asc');
  const sinceParam = useMemo(() => datetimeLocalToISO(sinceFilter), [sinceFilter]);
  const untilParam = useMemo(() => datetimeLocalToISO(untilFilter), [untilFilter]);
  const { data: vmResponse, loading, error, refresh } = useFetch(
    () => vms.list({ tag: tagFilter, search: searchFilter, image: imageFilter, defaultUser: defaultUserFilter, osType: osTypeFilter, osVariant: osVariantFilter, firmware: firmwareFilter, diskBus: diskBusFilter, network: networkFilter, autoStart: autoStartFilter, locked: lockedFilter, since: sinceParam, until: untilParam, minCpus: minCpusFilter, maxCpus: maxCpusFilter, minRamMb: minRamFilter, maxRamMb: maxRamFilter, minDiskGb: minDiskFilter, maxDiskGb: maxDiskFilter, sort, order, page, perPage }),
    [tagFilter, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, firmwareFilter, diskBusFilter, networkFilter, autoStartFilter, lockedFilter, sinceParam, untilParam, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskFilter, maxDiskFilter, sort, order, page, perPage],
    30000,
  );
  const handleEvent = useCallback((evt) => {
    if (evt?.type && VM_LIFECYCLE_TYPES.has(evt.type)) {
      refresh();
    }
  }, [refresh]);
  const { status: liveStatus } = useEventStream({ onEvent: handleEvent });
  const navigate = useNavigate();
  const vmList = normalizeVMList(vmResponse);
  const totalVMs = vmResponse?.meta?.totalCount ?? vmList.length;
  const allTags = [...new Set(vmList.flatMap(vm => vm.tags))].sort();

  const visibleVMs = vmList;
  const selectedVMs = useMemo(
    () => visibleVMs.filter(vm => selectedIds.includes(vm.id)),
    [visibleVMs, selectedIds],
  );

  useEffect(() => {
    if (searchParams.get('create') === '1') {
      setShowCreate(true);
      setSearchParams({});
    }
  }, [searchParams, setSearchParams]);

  useEffect(() => {
    setSelectedIds(prev => prev.filter(id => visibleVMs.some(vm => vm.id === id)));
  }, [visibleVMs]);

  useEffect(() => {
    setPage(1);
  }, [tagFilter, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, firmwareFilter, diskBusFilter, networkFilter, autoStartFilter, lockedFilter, sinceParam, untilParam, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskFilter, maxDiskFilter, sort, order]);

  // Debounce the free-text search box. The committed `searchFilter` drives the
  // useFetch dependency above; `searchInput` is what the user types.
  useEffect(() => {
    const trimmed = searchInput.trim();
    const handle = setTimeout(() => {
      setSearchFilter(trimmed);
    }, 250);
    return () => clearTimeout(handle);
  }, [searchInput]);

  // Same debounce shape for the image-filter input — `imageFilter` is what
  // drives the useFetch above, `imageInput` is what the user types.
  useEffect(() => {
    const trimmed = imageInput.trim();
    const handle = setTimeout(() => {
      setImageFilter(trimmed);
    }, 250);
    return () => clearTimeout(handle);
  }, [imageInput]);

  useEffect(() => {
    const trimmed = defaultUserInput.trim();
    const handle = setTimeout(() => {
      setDefaultUserFilter(trimmed);
    }, 250);
    return () => clearTimeout(handle);
  }, [defaultUserInput]);

  // Same debounce shape for the network-filter input.
  useEffect(() => {
    const trimmed = networkInput.trim();
    const handle = setTimeout(() => {
      setNetworkFilter(trimmed);
    }, 250);
    return () => clearTimeout(handle);
  }, [networkInput]);

  // Debounce the vCPU range inputs — the committed filters drive useFetch.
  useEffect(() => {
    const trimmed = minCpusInput.trim();
    const handle = setTimeout(() => setMinCpusFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [minCpusInput]);

  useEffect(() => {
    const trimmed = maxCpusInput.trim();
    const handle = setTimeout(() => setMaxCpusFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [maxCpusInput]);

  // Debounce the RAM range inputs — the committed filters drive useFetch.
  useEffect(() => {
    const trimmed = minRamInput.trim();
    const handle = setTimeout(() => setMinRamFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [minRamInput]);

  useEffect(() => {
    const trimmed = maxRamInput.trim();
    const handle = setTimeout(() => setMaxRamFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [maxRamInput]);

  // Debounce the disk range inputs — the committed filters drive useFetch.
  useEffect(() => {
    const trimmed = minDiskInput.trim();
    const handle = setTimeout(() => setMinDiskFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [minDiskInput]);

  useEffect(() => {
    const trimmed = maxDiskInput.trim();
    const handle = setTimeout(() => setMaxDiskFilter(trimmed), 250);
    return () => clearTimeout(handle);
  }, [maxDiskInput]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    if (sort && sort !== 'id') next.set('sort', sort); else next.delete('sort');
    if (order && order !== 'asc') next.set('order', order); else next.delete('order');
    if (searchFilter) next.set('search', searchFilter); else next.delete('search');
    if (imageFilter) next.set('image', imageFilter); else next.delete('image');
    if (defaultUserFilter) next.set('default_user', defaultUserFilter); else next.delete('default_user');
    if (osTypeFilter) next.set('os_type', osTypeFilter); else next.delete('os_type');
    if (osVariantFilter) next.set('os_variant', osVariantFilter); else next.delete('os_variant');
    if (firmwareFilter) next.set('firmware', firmwareFilter); else next.delete('firmware');
    if (diskBusFilter) next.set('disk_bus', diskBusFilter); else next.delete('disk_bus');
    if (networkFilter) next.set('network', networkFilter); else next.delete('network');
    if (autoStartFilter) next.set('auto_start', autoStartFilter); else next.delete('auto_start');
    if (lockedFilter) next.set('locked', lockedFilter); else next.delete('locked');
    if (sinceFilter) next.set('since', sinceFilter); else next.delete('since');
    if (untilFilter) next.set('until', untilFilter); else next.delete('until');
    if (minCpusFilter) next.set('min_cpus', minCpusFilter); else next.delete('min_cpus');
    if (maxCpusFilter) next.set('max_cpus', maxCpusFilter); else next.delete('max_cpus');
    if (minRamFilter) next.set('min_ram_mb', minRamFilter); else next.delete('min_ram_mb');
    if (maxRamFilter) next.set('max_ram_mb', maxRamFilter); else next.delete('max_ram_mb');
    if (minDiskFilter) next.set('min_disk_gb', minDiskFilter); else next.delete('min_disk_gb');
    if (maxDiskFilter) next.set('max_disk_gb', maxDiskFilter); else next.delete('max_disk_gb');
    setSearchParams(next, { replace: true });
  }, [sort, order, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, firmwareFilter, diskBusFilter, networkFilter, autoStartFilter, lockedFilter, sinceFilter, untilFilter, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskFilter, maxDiskFilter]); // eslint-disable-line react-hooks/exhaustive-deps

  const toggleSelected = (vmId) => {
    setSelectedIds(prev => prev.includes(vmId) ? prev.filter(id => id !== vmId) : [...prev, vmId]);
  };

  const toggleSelectAll = () => {
    if (selectedVMs.length === visibleVMs.length) {
      setSelectedIds([]);
      return;
    }
    setSelectedIds(visibleVMs.map(vm => vm.id));
  };

  const clearSelection = () => setSelectedIds([]);

  return (
    <div>
      <PageHeader
        title="Machines"
        subtitle={`${totalVMs} total`}
        actions={
          <>
            <LiveIndicator status={liveStatus} />
            <button className="btn-primary" onClick={() => setShowCreate(true)} data-testid="btn-new-vm">
              <Plus size={15} /> New Machine
            </button>
          </>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      {bulkMessage && (
        <div className="mb-4">
          <ErrorBanner message={bulkMessage} onRetry={() => setBulkMessage(null)} />
        </div>
      )}

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="relative flex-1 max-w-md">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
          <input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search by name, description, or tag…"
            className="input w-full pl-8 pr-8 py-1.5 text-sm"
            data-testid="vm-list-search"
            aria-label="Search machines"
          />
          {searchInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setSearchInput('')}
              data-testid="vm-list-search-clear"
              aria-label="Clear search"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="relative w-64">
          <input
            type="search"
            value={imageInput}
            onChange={(e) => setImageInput(e.target.value)}
            placeholder="Filter by image…"
            className="input w-full pl-3 pr-8 py-1.5 text-sm"
            data-testid="vm-list-image-filter"
            aria-label="Filter by base image"
          />
          {imageInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setImageInput('')}
              data-testid="vm-list-image-filter-clear"
              aria-label="Clear image filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="relative w-full sm:w-60">
          <input
            type="search"
            value={defaultUserInput}
            onChange={(e) => setDefaultUserInput(e.target.value)}
            placeholder="Filter by default user…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="vm-list-default-user-filter"
            aria-label="Filter by default user"
          />
          {defaultUserInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setDefaultUserInput('')}
              data-testid="vm-list-default-user-filter-clear"
              aria-label="Clear default user filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="w-full sm:w-40">
          <select
            value={osTypeFilter}
            onChange={(e) => setOsTypeFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="vm-list-os-type-filter"
            aria-label="Filter by guest OS family"
          >
            <option value="">All OSes</option>
            <option value="linux">Linux</option>
            <option value="windows">Windows</option>
          </select>
        </div>
        <div className="w-full sm:w-52">
          <select
            value={osVariantFilter}
            onChange={(e) => setOsVariantFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="vm-list-os-variant-filter"
            aria-label="Filter by Windows variant"
          >
            <option value="">All variants</option>
            <option value="windows-10">Windows 10</option>
            <option value="windows-11">Windows 11</option>
            <option value="windows-server-2019">Windows Server 2019</option>
            <option value="windows-server-2022">Windows Server 2022</option>
            <option value="windows-server-2025">Windows Server 2025</option>
          </select>
        </div>
        <div className="w-full sm:w-40">
          <select
            value={firmwareFilter}
            onChange={(e) => setFirmwareFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="vm-list-firmware-filter"
            aria-label="Filter by firmware"
          >
            <option value="">All firmware</option>
            <option value="bios">BIOS</option>
            <option value="uefi">UEFI</option>
            <option value="ovmf">OVMF</option>
          </select>
        </div>
        <div className="w-full sm:w-40">
          <select
            value={diskBusFilter}
            onChange={(e) => setDiskBusFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="vm-list-disk-bus-filter"
            aria-label="Filter by disk bus"
          >
            <option value="">All disk buses</option>
            <option value="virtio">virtio</option>
            <option value="sata">SATA</option>
          </select>
        </div>
        <div className="relative w-full sm:w-60">
          <input
            type="search"
            value={networkInput}
            onChange={(e) => setNetworkInput(e.target.value)}
            placeholder="Filter by network…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="vm-list-network-filter"
            aria-label="Filter by network"
          />
          {networkInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setNetworkInput('')}
              data-testid="vm-list-network-filter-clear"
              aria-label="Clear network filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Min vCPUs</span>
          <input
            type="number"
            min="0"
            value={minCpusInput}
            onChange={(e) => setMinCpusInput(e.target.value)}
            placeholder="0"
            className="input w-20 py-1.5 text-sm"
            data-testid="vm-list-min-cpus"
            aria-label="Minimum vCPUs"
          />
        </label>
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Max vCPUs</span>
          <input
            type="number"
            min="0"
            value={maxCpusInput}
            onChange={(e) => setMaxCpusInput(e.target.value)}
            placeholder="∞"
            className="input w-20 py-1.5 text-sm"
            data-testid="vm-list-max-cpus"
            aria-label="Maximum vCPUs"
          />
        </label>
        {(minCpusInput || maxCpusInput) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setMinCpusInput(''); setMaxCpusInput(''); }}
            data-testid="vm-list-cpus-filter-clear"
            aria-label="Clear vCPU filter"
          >
            Clear vCPUs
          </button>
        )}
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Min RAM (MB)</span>
          <input
            type="number"
            min="0"
            value={minRamInput}
            onChange={(e) => setMinRamInput(e.target.value)}
            placeholder="0"
            className="input w-24 py-1.5 text-sm"
            data-testid="vm-list-min-ram"
            aria-label="Minimum RAM in MB"
          />
        </label>
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Max RAM (MB)</span>
          <input
            type="number"
            min="0"
            value={maxRamInput}
            onChange={(e) => setMaxRamInput(e.target.value)}
            placeholder="∞"
            className="input w-24 py-1.5 text-sm"
            data-testid="vm-list-max-ram"
            aria-label="Maximum RAM in MB"
          />
        </label>
        {(minRamInput || maxRamInput) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setMinRamInput(''); setMaxRamInput(''); }}
            data-testid="vm-list-ram-filter-clear"
            aria-label="Clear RAM filter"
          >
            Clear RAM
          </button>
        )}
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Min disk (GB)</span>
          <input
            type="number"
            min="0"
            value={minDiskInput}
            onChange={(e) => setMinDiskInput(e.target.value)}
            placeholder="0"
            className="input w-24 py-1.5 text-sm"
            data-testid="vm-list-min-disk"
            aria-label="Minimum disk in GB"
          />
        </label>
        <label className="flex items-center gap-1 text-xs text-steel-400">
          <span>Max disk (GB)</span>
          <input
            type="number"
            min="0"
            value={maxDiskInput}
            onChange={(e) => setMaxDiskInput(e.target.value)}
            placeholder="∞"
            className="input w-24 py-1.5 text-sm"
            data-testid="vm-list-max-disk"
            aria-label="Maximum disk in GB"
          />
        </label>
        {(minDiskInput || maxDiskInput) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setMinDiskInput(''); setMaxDiskInput(''); }}
            data-testid="vm-list-disk-filter-clear"
            aria-label="Clear disk filter"
          >
            Clear disk
          </button>
        )}
      </div>

      {allTags.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-4">
          <button className={`btn-ghost text-xs ${tagFilter === '' ? 'text-blue-400' : ''}`} onClick={() => setTagFilter('')}>All</button>
          {allTags.map(tag => (
            <button key={tag} className={`badge ${tagFilter === tag ? 'badge-running' : 'bg-steel-800/60 text-steel-300 border-steel-700/40'}`} onClick={() => setTagFilter(tag)}>
              #{tag}
            </button>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 mb-4 text-xs text-steel-400" data-testid="vm-list-sort-controls">
        <span>Sort by</span>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-sort-field"
        >
          <option value="id">ID</option>
          <option value="name">Name</option>
          <option value="created_at">Created</option>
          <option value="state">State</option>
          <option value="cpus">vCPUs</option>
          <option value="ram_mb">RAM (MB)</option>
          <option value="disk_gb">Disk (GB)</option>
        </select>
        <select
          value={order}
          onChange={(e) => setOrder(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-sort-order"
        >
          <option value="asc">Ascending</option>
          <option value="desc">Descending</option>
        </select>
        <span className="ml-2">Auto-start</span>
        <select
          value={autoStartFilter}
          onChange={(e) => setAutoStartFilter(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-auto-start-filter"
          aria-label="Filter by auto-start"
        >
          <option value="">Any</option>
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
        <span className="ml-2">Locked</span>
        <select
          value={lockedFilter}
          onChange={(e) => setLockedFilter(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-locked-filter"
          aria-label="Filter by locked"
        >
          <option value="">Any</option>
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
        <span className="ml-2">Created since</span>
        <input
          type="datetime-local"
          value={sinceFilter}
          onChange={(e) => setSinceFilter(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-since-filter"
          aria-label="Filter by created since"
        />
        <span className="ml-2">until</span>
        <input
          type="datetime-local"
          value={untilFilter}
          onChange={(e) => setUntilFilter(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="vm-list-until-filter"
          aria-label="Filter by created until"
        />
        {(sinceFilter || untilFilter) && (
          <button
            type="button"
            className="btn-ghost text-xs"
            onClick={() => { setSinceFilter(''); setUntilFilter(''); }}
            data-testid="vm-list-time-range-clear"
            aria-label="Clear created-at range"
          >
            Clear range
          </button>
        )}
      </div>

      {!!visibleVMs.length && (
        <BulkActionBar
          selectedVMs={selectedVMs}
          totalVisible={visibleVMs.length}
          allSelected={selectedVMs.length > 0 && selectedVMs.length === visibleVMs.length}
          onToggleSelectAll={toggleSelectAll}
          onClearSelection={clearSelection}
          onDone={(message) => {
            setBulkMessage(message);
            refresh();
          }}
        />
      )}

      {loading && !vmList ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : !vmList?.length ? (
        <div className="card">
          <EmptyState
            icon={Server}
            title="No machines"
            description="Deploy your first virtual machine."
            action={<button className="btn-primary" onClick={() => setShowCreate(true)}><Plus size={15} /> Create</button>}
          />
        </div>
      ) : (
        <div className="space-y-2" data-testid="vm-list">
          {vmList.map(vm => (
            <VMRow
              key={vm.id}
              vm={vm}
              selected={selectedIds.includes(vm.id)}
              onToggleSelected={() => toggleSelected(vm.id)}
              onNavigate={() => navigate(`/vms/${vm.id}`)}
              actionMenu={actionMenu}
              setActionMenu={setActionMenu}
              onRefresh={refresh}
            />
          ))}
        </div>
      )}

      {!!vmList?.length && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={totalVMs}
          itemLabel="machines"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}

      <CreateVMModal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onCreated={refresh}
        onPasswordGenerated={setGeneratedPassword}
      />
      <GeneratedAdminPasswordModal
        info={generatedPassword}
        onClose={() => setGeneratedPassword(null)}
      />
    </div>
  );
}

// GeneratedAdminPasswordModal renders the one-time-reveal banner for a Windows
// VM whose Administrator password vmsmith generated. It shows the password,
// offers a copy button, and warns that the value cannot be recovered later.
function GeneratedAdminPasswordModal({ info, onClose }) {
  const [copied, setCopied] = useState(false);
  if (!info || !info.generated_admin_password) return null;
  const handleCopy = async () => {
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(info.generated_admin_password);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard may be unavailable in some environments — the password is
      // still visible in the field so the operator can copy manually.
    }
  };
  return (
    <Modal open={!!info} onClose={onClose} title="Generated Administrator password">
      <div className="space-y-4" data-testid="generated-admin-password-modal">
        <p className="text-sm text-amber-300">
          vmsmith generated a Windows Administrator password for{' '}
          <span className="font-mono">{info.name || info.id}</span>. This value
          is <strong>shown once and never stored</strong> — copy it now.
        </p>
        <div className="flex items-center gap-2">
          <code
            className="input flex-1 font-mono select-all"
            data-testid="generated-admin-password-value"
          >
            {info.generated_admin_password}
          </code>
          <button
            type="button"
            className="btn-secondary text-xs"
            onClick={handleCopy}
            data-testid="generated-admin-password-copy"
          >
            {copied ? 'Copied' : 'Copy'}
          </button>
        </div>
        <p className="text-xs text-steel-500">
          Save it in your password manager. Reloading the VM list, opening{' '}
          <span className="font-mono">vm get</span>, or refreshing the GUI will
          not show this value again. To rotate the password later, re-create
          the VM with an explicit <span className="font-mono">admin_password</span>.
        </p>
        <div className="flex justify-end">
          <button
            type="button"
            className="btn-primary"
            onClick={onClose}
            data-testid="generated-admin-password-dismiss"
          >
            I've saved it
          </button>
        </div>
      </div>
    </Modal>
  );
}

function BulkActionBar({ selectedVMs, totalVisible, allSelected, onToggleSelectAll, onClearSelection, onDone }) {
  const startMut = useMutation(vms.start);
  const stopMut = useMutation(vms.stop);
  const restartMut = useMutation(vms.restart);
  const forceStopMut = useMutation(vms.forceStop);
  const rebootMut = useMutation(vms.reboot);
  const suspendMut = useMutation(vms.suspend);
  const resumeMut = useMutation(vms.resume);
  const deleteMut = useMutation(vms.delete);

  const runningCount = selectedVMs.filter(vm => vm.state === 'running').length;
  const stoppedCount = selectedVMs.filter(vm => vm.state === 'stopped').length;
  const pausedCount = selectedVMs.filter(vm => vm.state === 'paused').length;
  const hasSelection = selectedVMs.length > 0;
  const mutationError = (
    startMut.error || stopMut.error || restartMut.error || forceStopMut.error ||
    rebootMut.error || suspendMut.error || resumeMut.error || deleteMut.error
  );
  const busy = (
    startMut.loading || stopMut.loading || restartMut.loading || forceStopMut.loading ||
    rebootMut.loading || suspendMut.loading || resumeMut.loading || deleteMut.loading
  );

  // Per-action eligibility filter and the empty-selection message shown when
  // no selected VM matches the action's required state.  Adding a new bulk
  // verb here is a one-row change.
  const bulkActions = {
    start:        { mut: startMut,     eligible: vm => vm.state === 'stopped', emptyMsg: 'Nothing to start — selected machines are already running.' },
    stop:         { mut: stopMut,      eligible: vm => vm.state === 'running', emptyMsg: 'Nothing to stop — selected machines are already stopped.' },
    restart:      { mut: restartMut,   eligible: vm => vm.state === 'running', emptyMsg: 'Nothing to restart — selected machines are not running.' },
    'force-stop': { mut: forceStopMut, eligible: vm => vm.state === 'running', emptyMsg: 'Nothing to force-stop — selected machines are not running.' },
    reboot:       { mut: rebootMut,    eligible: vm => vm.state === 'running', emptyMsg: 'Nothing to reboot — selected machines are not running.' },
    suspend:      { mut: suspendMut,   eligible: vm => vm.state === 'running', emptyMsg: 'Nothing to suspend — selected machines are not running.' },
    resume:       { mut: resumeMut,    eligible: vm => vm.state === 'paused',  emptyMsg: 'Nothing to resume — no selected machines are paused.' },
    delete:       { mut: deleteMut,    eligible: () => true, emptyMsg: '' },
  };

  const executeBulk = async (action) => {
    if (!hasSelection || busy) return;
    const spec = bulkActions[action];
    if (!spec) return;

    if (action === 'delete') {
      const names = selectedVMs.map(vm => vm.name).join(', ');
      if (!window.confirm(`Delete ${selectedVMs.length} machine(s)?\n\n${names}`)) return;
    } else if (action === 'force-stop') {
      const names = selectedVMs.filter(spec.eligible).map(vm => vm.name).join(', ');
      if (names && !window.confirm(`Force-stop ${selectedVMs.filter(spec.eligible).length} machine(s)? This skips ACPI shutdown and may cause data loss.\n\n${names}`)) return;
    }

    const targets = selectedVMs.filter(spec.eligible);
    const skipped = selectedVMs.length - targets.length;
    if (targets.length === 0) {
      if (spec.emptyMsg) onDone(spec.emptyMsg);
      return;
    }

    let success = 0;
    let failure = 0;

    for (const vm of targets) {
      try {
        await spec.mut.execute(vm.id);
        success += 1;
      } catch {
        failure += 1;
      }
    }

    const parts = [];
    parts.push(`${success} ${action}${success === 1 ? '' : 's'} succeeded`);
    if (failure > 0) parts.push(`${failure} failed`);
    if (skipped > 0) parts.push(`${skipped} skipped`);
    onClearSelection();
    onDone(parts.join(' · '));
  };

  return (
    <div className="mb-4 card border-steel-700/60 px-4 py-3" data-testid="bulk-action-bar">
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div className="flex flex-wrap items-center gap-3">
          <label className="inline-flex items-center gap-2 text-sm text-steel-300 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={allSelected}
              onChange={onToggleSelectAll}
              className="h-4 w-4 rounded border-steel-600 bg-steel-900 text-forge-500 focus:ring-forge-500/40"
              data-testid="checkbox-select-all-vms"
            />
            <span className="inline-flex items-center gap-2">
              <CheckSquare size={14} className="text-forge-400" />
              {hasSelection ? `${selectedVMs.length} selected` : `Select all ${totalVisible}`}
            </span>
          </label>
          {hasSelection && (
            <div className="flex flex-wrap items-center gap-2 text-xs text-steel-500">
              {runningCount > 0 && <span>{runningCount} running</span>}
              {stoppedCount > 0 && <span>{stoppedCount} stopped</span>}
              {pausedCount > 0 && <span>{pausedCount} paused</span>}
            </div>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || stoppedCount === 0}
            onClick={() => executeBulk('start')}
            data-testid="btn-bulk-start"
          >
            {startMut.loading ? <Spinner size={14} /> : <Play size={14} />}
            Start
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || runningCount === 0}
            onClick={() => executeBulk('stop')}
            data-testid="btn-bulk-stop"
          >
            {stopMut.loading ? <Spinner size={14} /> : <Square size={14} />}
            Stop
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || runningCount === 0}
            onClick={() => executeBulk('restart')}
            data-testid="btn-bulk-restart"
            title="Graceful stop and start"
          >
            {restartMut.loading ? <Spinner size={14} /> : <RotateCcw size={14} />}
            Restart
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || runningCount === 0}
            onClick={() => executeBulk('reboot')}
            data-testid="btn-bulk-reboot"
            title="In-guest ACPI reboot — preserves IP/MAC, no power cycle"
          >
            {rebootMut.loading ? <Spinner size={14} /> : <RefreshCw size={14} />}
            Reboot
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || runningCount === 0}
            onClick={() => executeBulk('force-stop')}
            data-testid="btn-bulk-force-stop"
            title="Immediate destroy — skips ACPI shutdown"
          >
            {forceStopMut.loading ? <Spinner size={14} /> : <Zap size={14} />}
            Force Stop
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || runningCount === 0}
            onClick={() => executeBulk('suspend')}
            data-testid="btn-bulk-suspend"
            title="Pause CPU + memory; resume later without rebooting"
          >
            {suspendMut.loading ? <Spinner size={14} /> : <Pause size={14} />}
            Suspend
          </button>
          <button
            className="btn-secondary"
            disabled={!hasSelection || busy || pausedCount === 0}
            onClick={() => executeBulk('resume')}
            data-testid="btn-bulk-resume"
            title="Unpause selected paused VMs"
          >
            {resumeMut.loading ? <Spinner size={14} /> : <Play size={14} />}
            Resume
          </button>
          <button
            className="btn-danger"
            disabled={!hasSelection || busy}
            onClick={() => executeBulk('delete')}
            data-testid="btn-bulk-delete"
          >
            {deleteMut.loading ? <Spinner size={14} /> : <Trash2 size={14} />}
            Delete
          </button>
          <button className="btn-ghost" disabled={!hasSelection || busy} onClick={onClearSelection} data-testid="btn-clear-selection">
            Clear
          </button>
        </div>
      </div>
      {mutationError && <p className="mt-3 text-sm text-red-400">Bulk action error: {mutationError}</p>}
    </div>
  );
}

function VMRow({ vm, selected, onToggleSelected, onNavigate, actionMenu, setActionMenu, onRefresh }) {
  const startMut = useMutation(vms.start);
  const stopMut  = useMutation(vms.stop);
  const delMut   = useMutation(vms.delete);
  const spec = vm.spec || {};
  const osType = resolveOsType(spec);
  const cpuText = Number.isFinite(spec.cpus) ? spec.cpus : '—';
  const ramText = Number.isFinite(spec.ram_mb) ? spec.ram_mb : '—';
  const diskText = Number.isFinite(spec.disk_gb) ? spec.disk_gb : '—';

  const handleAction = async (action) => {
    setActionMenu(null);
    try {
      if (action === 'start')  await startMut.execute(vm.id);
      if (action === 'stop')   await stopMut.execute(vm.id);
      if (action === 'delete') { if (window.confirm(`Delete ${vm.name}?`)) await delMut.execute(vm.id); }
      onRefresh();
    } catch { /* error shown in mutation */ }
  };

  const isMenuOpen = actionMenu === vm.id;

  return (
    <div className="card-hover flex items-center gap-4 px-4 py-3 group" onClick={onNavigate} data-testid={`vm-card-${vm.name}`}>
      <div className="shrink-0" onClick={e => e.stopPropagation()}>
        <input
          type="checkbox"
          checked={selected}
          onChange={onToggleSelected}
          className="h-4 w-4 rounded border-steel-600 bg-steel-900 text-forge-500 focus:ring-forge-500/40"
          aria-label={`Select ${vm.name}`}
          data-testid={`checkbox-select-vm-${vm.name}`}
        />
      </div>

      {/* Icon */}
      <div className={`w-9 h-9 rounded-lg flex items-center justify-center shrink-0 ${
        vm.state === 'running' ? 'bg-emerald-900/40 border border-emerald-700/30' : 'bg-steel-800/60 border border-steel-700/30'
      }`}>
        <Server size={16} className={vm.state === 'running' ? 'text-emerald-400' : 'text-steel-500'} />
      </div>

      {/* Info */}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2.5 flex-wrap">
          <span className="font-mono text-sm text-steel-100 truncate">{vm.name}</span>
          <StatusBadge state={vm.state} />
          <span
            className={`badge ${osType === 'windows' ? 'bg-sky-500/10 text-sky-300 border-sky-500/20' : 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20'}`}
            data-testid={`badge-os-${vm.name}`}
          >
            {osBadgeLabel(spec)}
          </span>
          {spec.locked && (
            <span
              className="badge bg-amber-500/10 text-amber-300 border-amber-500/20 inline-flex items-center gap-1"
              data-testid={`badge-locked-${vm.name}`}
              title="Delete-protected: unlock from the edit form before deleting"
            >
              <Lock size={10} /> locked
            </span>
          )}
        </div>
        <p className="text-xs font-mono text-steel-500 mt-0.5">
          {cpuText} vCPU · {ramText} MB · {diskText} GB
          {vm.ip && <> · {vm.ip}</>}
        </p>
        {vm.description && <p className="text-xs text-steel-400 mt-1 truncate">{vm.description}</p>}
        {vm.tags?.length > 0 && (
          <div className="flex flex-wrap gap-1 mt-2">
            {vm.tags.map(tag => (
              <span key={tag} className="badge bg-blue-500/10 text-blue-300 border-blue-500/20">#{tag}</span>
            ))}
          </div>
        )}
      </div>

      {/* Quick actions */}
      <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity" onClick={e => e.stopPropagation()}>
        {vm.state === 'stopped' && (
          <button className="btn-ghost text-emerald-400 hover:text-emerald-300" onClick={() => handleAction('start')} title="Start">
            <Play size={14} />
          </button>
        )}
        {vm.state === 'running' && (
          <button className="btn-ghost text-steel-400" onClick={() => handleAction('stop')} title="Stop">
            <Square size={14} />
          </button>
        )}
        <div className="relative">
          <button className="btn-ghost" onClick={() => setActionMenu(isMenuOpen ? null : vm.id)}>
            <MoreVertical size={14} />
          </button>
          {isMenuOpen && (
            <div className="absolute right-0 top-8 z-20 card border-steel-700/60 shadow-xl py-1 w-36 animate-fade-in">
              <button className="w-full text-left px-3 py-1.5 text-sm text-red-400 hover:bg-red-900/20 flex items-center gap-2"
                onClick={() => handleAction('delete')}>
                <Trash2 size={13} /> Delete
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function CreateVMModal({ open, onClose, onCreated, onPasswordGenerated }) {
  const emptyForm = { name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, description: '', tags: '', ssh_pub_key: '', default_user: '', nat_static_ip: '', nat_gateway: '', template_id: '', auto_start: false, os_type: 'linux', os_variant: '', admin_password: '', disk_bus: '', nic_model: '', machine: '', firmware: '', virtio_win_iso: '' };
  const [form, setForm] = useState(emptyForm);
  const [networks, setNetworks] = useState([]);
  const [activeTab, setActiveTab] = useState('basic');
  const [templateSearchInput, setTemplateSearchInput] = useState('');
  const [templateSearch, setTemplateSearch] = useState('');
  const createMut = useMutation(vms.create);
  const { data: imageResponse } = useFetch(() => imagesApi.list(), [], 0);
  const { data: templateResponse } = useFetch(
    () => templatesApi.list({ sort: 'name', search: templateSearch }),
    [templateSearch],
    0,
  );
  const { data: hostIfaces } = useFetch(() => hostApi.interfaces(), [], 0);
  const imageList = safeArray(imageResponse?.data || imageResponse);
  const templates = safeArray(templateResponse?.data || templateResponse);

  // Debounce the template-selector search box. `templateSearchInput` is the
  // live value the user types; `templateSearch` is the committed query that
  // drives the API call.
  useEffect(() => {
    const trimmed = templateSearchInput.trim();
    const id = setTimeout(() => setTemplateSearch(trimmed), 250);
    return () => clearTimeout(id);
  }, [templateSearchInput]);

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));
  const updateNum = (field) => (e) => setForm(f => ({ ...f, [field]: parseInt(e.target.value, 10) || 0 }));

  const applyTemplate = (templateId) => {
    const template = templates.find(t => t.id === templateId);
    setForm(f => {
      if (!template) return { ...f, template_id: '' };
      return {
        ...f,
        template_id: template.id,
        image: template.image || f.image,
        cpus: template.cpus || f.cpus,
        ram_mb: template.ram_mb || f.ram_mb,
        disk_gb: template.disk_gb || f.disk_gb,
        description: template.description || f.description,
        tags: (template.tags || []).join(', '),
        default_user: template.default_user || f.default_user,
        os_type: template.os_type || f.os_type,
        os_variant: template.os_variant || f.os_variant,
      };
    });
    setNetworks(template?.networks?.map(net => ({
      mode: net.mode || 'macvtap',
      host_interface: net.host_interface || net.bridge || '',
      static_ip: net.static_ip || '',
      gateway: net.gateway || '',
      dhcp: !net.static_ip,
    })) || []);
  };

  const humanSize = (bytes) => {
    if (!bytes) return '';
    if (bytes >= 1073741824) return ` · ${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return ` · ${(bytes / 1048576).toFixed(1)} MB`;
    return ` · ${bytes} B`;
  };

  const addNetwork = () => setNetworks(n => [...n, { mode: 'macvtap', host_interface: '', static_ip: '', gateway: '', dhcp: false }]);
  const removeNetwork = (i) => setNetworks(n => n.filter((_, idx) => idx !== i));
  const updateNet = (i, field, val) => setNetworks(n => n.map((net, idx) => idx === i ? { ...net, [field]: val } : net));
  const isWindows = resolveOsType(form) === 'windows';

  useEffect(() => {
    if (!isWindows) {
      setForm((f) => {
        if (!f.os_variant && !f.admin_password) return f;
        return { ...f, os_variant: '', admin_password: '' };
      });
    }
  }, [isWindows]);

  useEffect(() => {
    if (!isWindows) return;
    setForm(f => ({
      ...f,
      ram_mb: Math.max(Number(f.ram_mb) || 0, WINDOWS_MIN_RAM_MB),
      disk_gb: Math.max(Number(f.disk_gb) || 0, WINDOWS_MIN_DISK_GB),
    }));
  }, [isWindows]);

  const handleSubmit = async () => {
    const spec = { ...form };
    spec.tags = form.tags.split(',').map(tag => tag.trim()).filter(Boolean);
    if (!spec.description) delete spec.description;
    if (spec.tags.length === 0) delete spec.tags;
    if (!spec.nat_static_ip) delete spec.nat_static_ip;
    if (!spec.nat_gateway)   delete spec.nat_gateway;
    if (!spec.template_id) delete spec.template_id;
    if (!spec.auto_start) delete spec.auto_start;
    if (!spec.os_variant) delete spec.os_variant;
    if (!spec.admin_password) delete spec.admin_password;
    if (resolveOsType(spec) !== 'windows') {
      delete spec.os_variant;
      delete spec.admin_password;
    }
    // Per-VM device overrides (5.6.15) — only send keys the operator
    // actually filled in so the daemon resolves the OS-family default.
    if (!spec.disk_bus) delete spec.disk_bus;
    if (!spec.nic_model) delete spec.nic_model;
    if (!spec.machine) delete spec.machine;
    if (!spec.firmware) delete spec.firmware;
    if (!spec.virtio_win_iso) delete spec.virtio_win_iso;
    if (networks.length > 0) {
      spec.networks = networks.map(n => {
        const att = { mode: n.mode };
        if (n.mode === 'bridge') att.bridge = n.host_interface;
        else att.host_interface = n.host_interface;
        if (!n.dhcp && n.static_ip) att.static_ip = n.static_ip;
        if (!n.dhcp && n.gateway)   att.gateway   = n.gateway;
        return att;
      });
    }
    try {
      const created = await createMut.execute(spec);
      // Surface a one-time-reveal modal if the daemon auto-generated a
      // Windows Administrator password (Windows guest, no admin_password
      // supplied). The value is shown here exactly once — there is no
      // re-read path.
      if (created?.generated_admin_password && typeof onPasswordGenerated === 'function') {
        onPasswordGenerated(created);
      }
      onCreated();
      onClose();
      setForm(emptyForm);
      setNetworks([]);
      setActiveTab('basic');
    } catch { /* error displayed via mutation */ }
  };

  const noImages = imageList.length === 0;
  const hostInterfaces = safeArray(hostIfaces);
  const physIfaces = hostInterfaces.filter(i => i && i.is_physical && i.is_up);
  const natIface = hostInterfaces.find(i => i && i.name === 'vmsmith0');

  const advancedCount = [
    form.description, form.tags, form.ssh_pub_key, form.default_user, form.nat_static_ip, form.nat_gateway,
    form.disk_bus, form.nic_model, form.machine, form.firmware, form.virtio_win_iso,
    networks.length > 0 ? 'x' : ''
  ].filter(Boolean).length;

  return (
    <Modal open={open} onClose={onClose} title="Create Machine" wide>
      <div className="flex flex-col max-h-[75vh]">

        {/* Tabs */}
        <div className="flex gap-1 mb-4 border-b border-steel-800/60 -mt-1">
          <button
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
              activeTab === 'basic'
                ? 'border-blue-500 text-blue-400'
                : 'border-transparent text-steel-500 hover:text-steel-300'
            }`}
            onClick={() => setActiveTab('basic')}
            data-testid="tab-basic"
          >
            Basic
          </button>
          <button
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5 ${
              activeTab === 'advanced'
                ? 'border-blue-500 text-blue-400'
                : 'border-transparent text-steel-500 hover:text-steel-300'
            }`}
            onClick={() => setActiveTab('advanced')}
            data-testid="tab-advanced"
          >
            Advanced
            {advancedCount > 0 && (
              <span className="text-[10px] bg-blue-500/20 text-blue-400 border border-blue-500/30 rounded-full px-1.5 py-0 leading-4">
                {advancedCount}
              </span>
            )}
          </button>
        </div>

        <div className="overflow-y-auto flex-1 space-y-4 pr-1 pb-1">

          {/* Basic Tab */}
          {activeTab === 'basic' && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="label">Name</label>
                  <input className="input" placeholder="my-server" value={form.name} onChange={update('name')} autoFocus data-testid="input-vm-name" />
                </div>
                <div>
                  <label className="label">Template <span className="text-steel-500 font-normal">(optional)</span></label>
                  <div className="relative mb-1.5">
                    <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
                    <input
                      type="search"
                      value={templateSearchInput}
                      onChange={e => setTemplateSearchInput(e.target.value)}
                      placeholder="Search templates…"
                      className="input w-full pl-7 pr-7 py-1 text-xs"
                      data-testid="template-search-input"
                      aria-label="Search templates"
                    />
                    {templateSearchInput && (
                      <button
                        type="button"
                        className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
                        onClick={() => setTemplateSearchInput('')}
                        data-testid="template-search-clear"
                        aria-label="Clear template search"
                      >
                        <X size={12} />
                      </button>
                    )}
                  </div>
                  <select className="input" value={form.template_id} onChange={e => applyTemplate(e.target.value)} data-testid="input-vm-template">
                    <option value="">{templateSearch && templates.length === 0 ? `No templates match "${templateSearch}"` : 'No template'}</option>
                    {templates.map(template => (
                      <option key={template.id} value={template.id}>{template.name}</option>
                    ))}
                  </select>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="label">Base Image</label>
                  {noImages && !form.template_id ? (
                    <div className="input flex items-center text-steel-500 text-xs">
                      No images available — upload one in the Images section first.
                    </div>
                  ) : (
                    <select className="input" value={form.image} onChange={update('image')} data-testid="input-vm-image">
                      <option value="">{form.template_id ? 'Use template default image…' : 'Select an image…'}</option>
                      {(imageList || []).map(img => (
                        <option key={img.id} value={img.path}>
                          {img.name}{humanSize(img.size_bytes)}
                        </option>
                      ))}
                    </select>
                  )}
                </div>
                <div className="flex flex-col items-stretch justify-end pb-2">
                  {form.template_id ? (() => {
                    const selected = templates.find(t => t.id === form.template_id);
                    const selectedTags = selected?.tags || [];
                    const selectedDesc = selected?.description || '';
                    return (
                      <>
                        <p className="text-xs text-steel-500" data-testid="template-hint">
                          Template defaults are prefilled below, and anything you change here overrides them.
                        </p>
                        {selectedDesc && (
                          <p className="mt-1 text-xs text-steel-400 italic" data-testid="template-description">
                            {selectedDesc}
                          </p>
                        )}
                        {selectedTags.length > 0 && (
                          <div className="mt-1 flex flex-wrap gap-1" data-testid="template-tag-chips">
                            {selectedTags.map(t => (
                              <span key={t} className="inline-flex items-center rounded bg-steel-700/40 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-steel-200">
                                {t}
                              </span>
                            ))}
                          </div>
                        )}
                      </>
                    );
                  })() : <div />}
                </div>
              </div>

              <div className="grid grid-cols-3 gap-4">
                <div>
                  <label className="label">Guest OS</label>
                  <select className="input" value={form.os_type} onChange={update('os_type')} data-testid="input-vm-os-type">
                    <option value="linux">Linux</option>
                    <option value="windows">Windows</option>
                  </select>
                </div>
                <div>
                  <label className="label">vCPUs</label>
                  <input className="input" type="number" min={1} value={form.cpus} onChange={updateNum('cpus')} data-testid="input-vm-cpus" />
                </div>
                <div>
                  <label className="label">RAM (MB)</label>
                  <input className="input" type="number" min={isWindows ? WINDOWS_MIN_RAM_MB : 256} step={256} value={form.ram_mb} onChange={updateNum('ram_mb')} data-testid="input-vm-ram" />
                  {isWindows && <p className="mt-1 text-[11px] text-steel-500">Windows minimum: {WINDOWS_MIN_RAM_MB} MB</p>}
                </div>
              </div>

              <div className="grid grid-cols-3 gap-4">
                {isWindows ? (
                  <div>
                    <label className="label">Windows variant</label>
                    <select className="input" value={form.os_variant} onChange={update('os_variant')} data-testid="input-vm-os-variant">
                      <option value="">Select variant…</option>
                      <option value="windows-10">Windows 10</option>
                      <option value="windows-11">Windows 11</option>
                      <option value="windows-server-2019">Windows Server 2019</option>
                      <option value="windows-server-2022">Windows Server 2022</option>
                      <option value="windows-server-2025">Windows Server 2025</option>
                    </select>
                  </div>
                ) : <div />}
                <div>
                  <label className="label">Disk (GB)</label>
                  <input className="input" type="number" min={isWindows ? WINDOWS_MIN_DISK_GB : 1} value={form.disk_gb} onChange={updateNum('disk_gb')} data-testid="input-vm-disk" />
                  {isWindows && <p className="mt-1 text-[11px] text-steel-500">Windows minimum: {WINDOWS_MIN_DISK_GB} GB</p>}
                </div>
                {isWindows ? (
                  <div>
                    <label className="label">Administrator password <span className="text-steel-500 font-normal">(optional)</span></label>
                    <input className="input font-mono" type="password" placeholder="Set once at first boot" value={form.admin_password} onChange={update('admin_password')} data-testid="input-vm-admin-password" />
                  </div>
                ) : <div />}
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="label">Description</label>
                  <input className="input" placeholder="What this VM is for" value={form.description} onChange={update('description')} />
                </div>
                <div>
                  <label className="label">Tags</label>
                  <input className="input font-mono" placeholder="prod,web,customer-a" value={form.tags} onChange={update('tags')} />
                </div>
              </div>
            </>
          )}

          {/* Advanced Tab */}
          {activeTab === 'advanced' && (
            <>
              {/* SSH */}
              <div>
                <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider mb-3">Access</h3>
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="label">Default SSH User <span className="text-steel-500 font-normal">(blank = {isWindows ? 'Administrator' : 'root'})</span></label>
                    <input className="input font-mono" placeholder={isWindows ? 'Administrator' : 'root'} value={form.default_user} onChange={update('default_user')} data-testid="input-vm-default-user" />
                  </div>
                  <div>
                    <label className="label">SSH Public Key</label>
                    <input className="input font-mono" placeholder="ssh-rsa AAAA…" value={form.ssh_pub_key} onChange={update('ssh_pub_key')} data-testid="input-vm-ssh-key" />
                  </div>
                </div>
              </div>

              {/* Lifecycle */}
              <div>
                <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider mb-3">Lifecycle</h3>
                <label className="flex items-start gap-3 p-3 rounded border border-steel-700/40 bg-steel-900/40 cursor-pointer">
                  <input
                    type="checkbox"
                    className="mt-1"
                    checked={!!form.auto_start}
                    onChange={(e) => setForm(f => ({ ...f, auto_start: e.target.checked }))}
                    data-testid="input-vm-auto-start"
                  />
                  <span className="text-xs">
                    <span className="text-steel-200 font-medium">Auto-start at daemon boot</span>
                    <span className="block text-steel-500 mt-1">
                      The daemon will start this VM automatically when vmsmith starts up.
                    </span>
                  </span>
                </label>
              </div>

              {/* Primary NAT network */}
              <div>
                <div className="flex items-center justify-between mb-3">
                  <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider">Primary Network (NAT)</h3>
                  {natIface && (
                    <span className="text-xs font-mono text-steel-500">
                      {natIface.name}{natIface.ips?.length ? ` · ${natIface.ips[0]}` : ''}{natIface.is_up ? '' : ' · down'}
                    </span>
                  )}
                </div>
                <div className="p-3 rounded border border-steel-700/40 bg-steel-900/40">
                  <p className="text-xs text-steel-500 mb-3">
                    vmsmith-net (192.168.100.0/24) — blank for DHCP, or set a static IP.
                  </p>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="label text-[10px]">Static IP (CIDR)</label>
                      <input
                        className="input py-1 text-xs font-mono"
                        placeholder="192.168.100.50/24"
                        value={form.nat_static_ip}
                        onChange={update('nat_static_ip')}
                      />
                    </div>
                    <div>
                      <label className="label text-[10px]">Gateway</label>
                      <input
                        className="input py-1 text-xs font-mono"
                        placeholder="192.168.100.1"
                        value={form.nat_gateway}
                        onChange={update('nat_gateway')}
                      />
                    </div>
                  </div>
                </div>
              </div>

              {/* Device tuning (5.6.15) */}
              <div>
                <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider mb-3">
                  Device Tuning <span className="text-steel-500 font-normal normal-case">(advanced — overrides OS-family defaults)</span>
                </h3>
                <div className="p-3 rounded border border-steel-700/40 bg-steel-900/40 space-y-3">
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="label text-[10px]">Disk Bus</label>
                      <select
                        className="input py-1 text-xs"
                        value={form.disk_bus}
                        onChange={update('disk_bus')}
                        data-testid="input-vm-disk-bus"
                      >
                        <option value="">Default (linux=virtio, windows=sata)</option>
                        <option value="virtio">virtio</option>
                        <option value="sata">sata</option>
                      </select>
                    </div>
                    <div>
                      <label className="label text-[10px]">NIC Model</label>
                      <select
                        className="input py-1 text-xs"
                        value={form.nic_model}
                        onChange={update('nic_model')}
                        data-testid="input-vm-nic-model"
                      >
                        <option value="">Default (linux=virtio, windows=e1000e)</option>
                        <option value="virtio">virtio</option>
                        <option value="e1000e">e1000e</option>
                      </select>
                    </div>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="label text-[10px]">Firmware</label>
                      <select
                        className="input py-1 text-xs"
                        value={form.firmware}
                        onChange={update('firmware')}
                        data-testid="input-vm-firmware"
                      >
                        <option value="">Default (bios)</option>
                        <option value="bios">bios</option>
                        <option value="uefi">uefi (required for Windows 11)</option>
                        <option value="ovmf">ovmf (alias for uefi)</option>
                      </select>
                    </div>
                    <div>
                      <label className="label text-[10px]">Machine Type</label>
                      <input
                        className="input py-1 text-xs font-mono"
                        placeholder="pc-q35-6.2 (default)"
                        value={form.machine}
                        onChange={update('machine')}
                        data-testid="input-vm-machine"
                      />
                    </div>
                  </div>
                  <div>
                    <label className="label text-[10px]">Virtio-Win ISO (Windows only)</label>
                    <input
                      className="input py-1 text-xs font-mono"
                      placeholder="/usr/share/virtio-win/virtio-win.iso"
                      value={form.virtio_win_iso}
                      onChange={update('virtio_win_iso')}
                      data-testid="input-vm-virtio-win-iso"
                    />
                    <p className="text-[10px] text-steel-500 mt-1">
                      Overrides the daemon-wide <span className="font-mono">storage.virtio_win_iso</span> for this VM only.
                    </p>
                  </div>
                </div>
              </div>

              {/* Extra networks */}
              <div>
                <div className="flex items-center justify-between mb-3">
                  <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider">Extra Networks</h3>
                  <button className="btn-ghost text-xs" type="button" onClick={addNetwork} data-testid="btn-add-network">
                    <Plus size={12} /> Add Interface
                  </button>
                </div>
                {networks.length === 0 ? (
                  <p className="text-xs text-steel-500 px-1">
                    No extra interfaces. The NAT network above is always attached.
                  </p>
                ) : (
                  <div className="space-y-2">
                    {networks.map((net, i) => (
                      <div key={i} className="flex items-start gap-2 p-3 rounded border border-steel-700/40 bg-steel-900/40">
                        <Network size={13} className="text-steel-500 mt-2 shrink-0" />
                        <div className="flex-1 space-y-2">
                          <div className="grid grid-cols-2 gap-2">
                            <div>
                              <label className="label text-[10px]">Mode</label>
                              <select className="input py-1 text-xs" value={net.mode} onChange={e => updateNet(i, 'mode', e.target.value)}>
                                <option value="macvtap">macvtap (direct)</option>
                                <option value="bridge">bridge</option>
                              </select>
                            </div>
                            <div>
                              <label className="label text-[10px]">
                                {net.mode === 'bridge' ? 'Bridge Name' : 'Host Interface'}
                              </label>
                              {net.mode === 'macvtap' && physIfaces.length > 0 ? (
                                <select className="input py-1 text-xs" value={net.host_interface} onChange={e => updateNet(i, 'host_interface', e.target.value)}>
                                  <option value="">Select…</option>
                                  {physIfaces.map(iface => (
                                    <option key={iface.name} value={iface.name}>
                                      {iface.name}{iface.ips?.length ? ` (${iface.ips[0]})` : ''}
                                    </option>
                                  ))}
                                </select>
                              ) : (
                                <input
                                  className="input py-1 text-xs font-mono"
                                  placeholder={net.mode === 'bridge' ? 'br-data' : 'eth1'}
                                  value={net.host_interface}
                                  onChange={e => updateNet(i, 'host_interface', e.target.value)}
                                />
                              )}
                            </div>
                          </div>
                          {/* DHCP toggle */}
                          <label className="flex items-center gap-2 cursor-pointer select-none">
                            <input
                              type="checkbox"
                              className="rounded border-steel-600 bg-steel-800 text-blue-500 focus:ring-blue-500/30"
                              checked={net.dhcp}
                              onChange={e => updateNet(i, 'dhcp', e.target.checked)}
                              data-testid={`checkbox-net-${i}-dhcp`}
                            />
                            <span className="text-xs text-steel-400">Use DHCP (no static IP)</span>
                          </label>
                          {!net.dhcp && (
                            <div className="grid grid-cols-2 gap-2">
                              <div>
                                <label className="label text-[10px]">Static IP (CIDR)</label>
                                <input
                                  className="input py-1 text-xs font-mono"
                                  placeholder="10.0.0.2/24"
                                  value={net.static_ip}
                                  onChange={e => updateNet(i, 'static_ip', e.target.value)}
                                  data-testid={`input-net-${i}-static-ip`}
                                />
                              </div>
                              <div>
                                <label className="label text-[10px]">Gateway</label>
                                <input
                                  className="input py-1 text-xs font-mono"
                                  placeholder="10.0.0.1"
                                  value={net.gateway}
                                  onChange={e => updateNet(i, 'gateway', e.target.value)}
                                  data-testid={`input-net-${i}-gateway`}
                                />
                              </div>
                            </div>
                          )}
                        </div>
                        <button className="btn-ghost text-red-400 hover:text-red-300 mt-1 shrink-0" onClick={() => removeNetwork(i)}>
                          <X size={13} />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </>
          )}

        </div>

        {/* Pinned footer */}
        <div className="shrink-0 pt-3 mt-1 border-t border-steel-800/60">
          {createMut.error && (
            <p className="text-sm text-red-400 mb-2">Error: {createMut.error}</p>
          )}
          <div className="flex justify-end gap-2">
            <button className="btn-secondary" onClick={onClose} data-testid="btn-cancel-create">Cancel</button>
            <button className="btn-primary" onClick={handleSubmit} disabled={createMut.loading || !form.name || (!form.image && !form.template_id)} data-testid="btn-submit-create">
              {createMut.loading ? <Spinner size={14} /> : <Plus size={15} />}
              Create
            </button>
          </div>
        </div>
      </div>
    </Modal>
  );
}
