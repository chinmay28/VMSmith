import { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Layers, Trash2, Pencil, Search, X, Plus } from 'lucide-react';
import { templates as templatesApi, images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls, FilterPanel } from '../components/Shared';
import { safeArray } from '../utils/normalize';

const DEFAULT_PER_PAGE = 25;

export default function TemplateList() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [editing, setEditing] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  const [tagFilter, setTagFilter] = useState('');
  const [searchInput, setSearchInput] = useState(searchParams.get('search') || '');
  const [searchFilter, setSearchFilter] = useState(searchParams.get('search') || '');
  const [imageInput, setImageInput] = useState(searchParams.get('image') || '');
  const [imageFilter, setImageFilter] = useState(searchParams.get('image') || '');
  const [defaultUserInput, setDefaultUserInput] = useState(searchParams.get('default_user') || '');
  const [defaultUserFilter, setDefaultUserFilter] = useState(searchParams.get('default_user') || '');
  const [osTypeFilter, setOsTypeFilter] = useState(searchParams.get('os_type') || '');
  const [osVariantFilter, setOsVariantFilter] = useState(searchParams.get('os_variant') || '');
  const [networkInput, setNetworkInput] = useState(searchParams.get('network') || '');
  const [networkFilter, setNetworkFilter] = useState(searchParams.get('network') || '');
  const [prefixInput, setPrefixInput] = useState(searchParams.get('prefix') || '');
  const [prefixFilter, setPrefixFilter] = useState(searchParams.get('prefix') || '');
  const [since, setSince] = useState(searchParams.get('since') || '');
  const [until, setUntil] = useState(searchParams.get('until') || '');
  const [minCpusInput, setMinCpusInput] = useState(searchParams.get('min_cpus') || '');
  const [minCpusFilter, setMinCpusFilter] = useState(searchParams.get('min_cpus') || '');
  const [maxCpusInput, setMaxCpusInput] = useState(searchParams.get('max_cpus') || '');
  const [maxCpusFilter, setMaxCpusFilter] = useState(searchParams.get('max_cpus') || '');
  const [minRamInput, setMinRamInput] = useState(searchParams.get('min_ram_mb') || '');
  const [minRamFilter, setMinRamFilter] = useState(searchParams.get('min_ram_mb') || '');
  const [maxRamInput, setMaxRamInput] = useState(searchParams.get('max_ram_mb') || '');
  const [maxRamFilter, setMaxRamFilter] = useState(searchParams.get('max_ram_mb') || '');
  const [minDiskGbInput, setMinDiskGbInput] = useState(searchParams.get('min_disk_gb') || '');
  const [minDiskGbFilter, setMinDiskGbFilter] = useState(searchParams.get('min_disk_gb') || '');
  const [maxDiskGbInput, setMaxDiskGbInput] = useState(searchParams.get('max_disk_gb') || '');
  const [maxDiskGbFilter, setMaxDiskGbFilter] = useState(searchParams.get('max_disk_gb') || '');
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const [sort, setSort] = useState(searchParams.get('sort') || 'id');
  const [order, setOrder] = useState(searchParams.get('order') || 'asc');
  const [selected, setSelected] = useState(() => new Set());
  const [bulkResult, setBulkResult] = useState(null);

  const { data: response, loading, error, refresh } = useFetch(
    () => templatesApi.list({ page, perPage, tag: tagFilter, search: searchFilter, image: imageFilter, defaultUser: defaultUserFilter, osType: osTypeFilter, osVariant: osVariantFilter, network: networkFilter, prefix: prefixFilter, since, until, minCpus: minCpusFilter, maxCpus: maxCpusFilter, minRamMb: minRamFilter, maxRamMb: maxRamFilter, minDiskGb: minDiskGbFilter, maxDiskGb: maxDiskGbFilter, sort, order }),
    [page, perPage, tagFilter, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, networkFilter, prefixFilter, since, until, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskGbFilter, maxDiskGbFilter, sort, order],
    10000,
  );
  const deleteMut = useMutation(templatesApi.delete);
  const bulkMut = useMutation(templatesApi.bulkDelete);

  const templateList = response?.data || [];
  const totalTemplates = response?.meta?.totalCount ?? templateList.length;
  const allTags = useMemo(
    () => [...new Set(templateList.flatMap(tpl => tpl.tags || []))].sort(),
    [templateList],
  );

  useEffect(() => { setPage(1); }, [tagFilter, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, networkFilter, prefixFilter, since, until, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskGbFilter, maxDiskGbFilter, sort, order]);

  // Debounce the free-text search box.
  useEffect(() => {
    const trimmed = searchInput.trim();
    const id = setTimeout(() => setSearchFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [searchInput]);

  // Debounce the image filter input.
  useEffect(() => {
    const trimmed = imageInput.trim();
    const id = setTimeout(() => setImageFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [imageInput]);

  // Debounce the default-user filter input.
  useEffect(() => {
    const trimmed = defaultUserInput.trim();
    const id = setTimeout(() => setDefaultUserFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [defaultUserInput]);

  // Debounce the network filter input.
  useEffect(() => {
    const trimmed = networkInput.trim();
    const id = setTimeout(() => setNetworkFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [networkInput]);

  // Debounce the name-prefix filter input (5.4.78). No `.toLowerCase()` —
  // matches the case-sensitive `strings.HasPrefix` semantics on the API.
  useEffect(() => {
    const trimmed = prefixInput.trim();
    const id = setTimeout(() => setPrefixFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [prefixInput]);

  // Debounce the min-cpus / max-cpus inputs (5.4.51).
  useEffect(() => {
    const trimmed = minCpusInput.trim();
    const id = setTimeout(() => setMinCpusFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [minCpusInput]);
  useEffect(() => {
    const trimmed = maxCpusInput.trim();
    const id = setTimeout(() => setMaxCpusFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [maxCpusInput]);

  // Debounce the min-ram / max-ram inputs (5.4.52).
  useEffect(() => {
    const trimmed = minRamInput.trim();
    const id = setTimeout(() => setMinRamFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [minRamInput]);
  useEffect(() => {
    const trimmed = maxRamInput.trim();
    const id = setTimeout(() => setMaxRamFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [maxRamInput]);

  // Debounce the min-disk-gb / max-disk-gb inputs (5.4.53).
  useEffect(() => {
    const trimmed = minDiskGbInput.trim();
    const id = setTimeout(() => setMinDiskGbFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [minDiskGbInput]);
  useEffect(() => {
    const trimmed = maxDiskGbInput.trim();
    const id = setTimeout(() => setMaxDiskGbFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [maxDiskGbInput]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    if (sort && sort !== 'id') next.set('sort', sort); else next.delete('sort');
    if (order && order !== 'asc') next.set('order', order); else next.delete('order');
    if (searchFilter) next.set('search', searchFilter); else next.delete('search');
    if (imageFilter) next.set('image', imageFilter); else next.delete('image');
    if (defaultUserFilter) next.set('default_user', defaultUserFilter); else next.delete('default_user');
    if (osTypeFilter) next.set('os_type', osTypeFilter); else next.delete('os_type');
    if (osVariantFilter) next.set('os_variant', osVariantFilter); else next.delete('os_variant');
    if (networkFilter) next.set('network', networkFilter); else next.delete('network');
    if (prefixFilter) next.set('prefix', prefixFilter); else next.delete('prefix');
    if (since) next.set('since', since); else next.delete('since');
    if (until) next.set('until', until); else next.delete('until');
    if (minCpusFilter) next.set('min_cpus', minCpusFilter); else next.delete('min_cpus');
    if (maxCpusFilter) next.set('max_cpus', maxCpusFilter); else next.delete('max_cpus');
    if (minRamFilter) next.set('min_ram_mb', minRamFilter); else next.delete('min_ram_mb');
    if (maxRamFilter) next.set('max_ram_mb', maxRamFilter); else next.delete('max_ram_mb');
    if (minDiskGbFilter) next.set('min_disk_gb', minDiskGbFilter); else next.delete('min_disk_gb');
    if (maxDiskGbFilter) next.set('max_disk_gb', maxDiskGbFilter); else next.delete('max_disk_gb');
    setSearchParams(next, { replace: true });
  }, [sort, order, searchFilter, imageFilter, defaultUserFilter, osTypeFilter, osVariantFilter, networkFilter, prefixFilter, since, until, minCpusFilter, maxCpusFilter, minRamFilter, maxRamFilter, minDiskGbFilter, maxDiskGbFilter]); // eslint-disable-line react-hooks/exhaustive-deps

  // Drop selections that are no longer visible (page/filter/refresh churn).
  useEffect(() => {
    if (!templateList.length) {
      if (selected.size) setSelected(new Set());
      return;
    }
    const existing = new Set(templateList.map(t => t.id));
    let changed = false;
    const next = new Set();
    selected.forEach(id => {
      if (existing.has(id)) next.add(id);
      else changed = true;
    });
    if (changed) setSelected(next);
  }, [templateList]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleDelete = async (id, name) => {
    if (!window.confirm(`Delete template "${name}"?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

  const clearAllFilters = () => {
    setTagFilter('');
    setImageInput(''); setImageFilter('');
    setDefaultUserInput(''); setDefaultUserFilter('');
    setOsTypeFilter(''); setOsVariantFilter('');
    setNetworkInput(''); setNetworkFilter('');
    setPrefixInput(''); setPrefixFilter('');
    setSince(''); setUntil('');
    setMinCpusInput(''); setMinCpusFilter(''); setMaxCpusInput(''); setMaxCpusFilter('');
    setMinRamInput(''); setMinRamFilter(''); setMaxRamInput(''); setMaxRamFilter('');
    setMinDiskGbInput(''); setMinDiskGbFilter(''); setMaxDiskGbInput(''); setMaxDiskGbFilter('');
  };

  const activeFilterCount = [
    tagFilter, imageInput, defaultUserInput, osTypeFilter, osVariantFilter, networkInput,
    prefixInput, since, until, minCpusInput, maxCpusInput, minRamInput, maxRamInput,
    minDiskGbInput, maxDiskGbInput,
  ].filter(v => String(v ?? '').trim() !== '').length;

  const allSelected = selected.size > 0 && selected.size === templateList.length;
  const someSelected = selected.size > 0 && !allSelected;
  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(templateList.map(t => t.id)));
  };
  const toggleOne = (id) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };
  const handleBulkDelete = async () => {
    if (!selected.size) return;
    const result = await bulkMut.execute({ ids: Array.from(selected) });
    setBulkResult(result);
    setSelected(new Set());
    refresh();
  };
  const dismissBulkResult = () => setBulkResult(null);

  return (
    <div data-testid="template-list-page">
      <PageHeader
        title="Templates"
        subtitle={`${totalTemplates} reusable VM template${totalTemplates === 1 ? '' : 's'}`}
        actions={
          <button className="btn-primary" onClick={() => setShowCreate(true)} data-testid="btn-new-template">
            <Plus size={15} /> New Template
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      <EditTemplateModal template={editing} onClose={() => setEditing(null)} onSaved={refresh} />
      <CreateTemplateModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={refresh} />

      <div className="mb-3">
        <div className="relative max-w-md">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
          <input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search by name, description, or tag…"
            className="input w-full pl-8 pr-8 py-2 text-sm"
            data-testid="template-list-search"
            aria-label="Search templates"
          />
          {searchInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setSearchInput('')}
              data-testid="template-list-search-clear"
              aria-label="Clear search"
            >
              <X size={13} />
            </button>
          )}
        </div>
      </div>

      {allTags.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-3" data-testid="template-tag-filter">
          <button
            className={`btn-ghost text-xs ${tagFilter === '' ? 'text-forge-400' : ''}`}
            onClick={() => setTagFilter('')}
            data-testid="template-tag-filter-all"
          >
            All
          </button>
          {allTags.map(tag => (
            <button
              key={tag}
              className={`badge ${tagFilter === tag ? 'badge-running' : 'bg-steel-800/60 text-steel-300 border-steel-700/40'}`}
              onClick={() => setTagFilter(tag)}
              data-testid={`template-tag-filter-${tag}`}
            >
              #{tag}
            </button>
          ))}
        </div>
      )}

      <FilterPanel activeCount={activeFilterCount} onClear={clearAllFilters} testId="template-list-filters">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-64">
          <input
            type="text"
            value={imageInput}
            onChange={(e) => setImageInput(e.target.value)}
            placeholder="Filter by image…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="template-list-image-filter"
            aria-label="Filter templates by image"
          />
          {imageInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setImageInput('')}
              data-testid="template-list-image-filter-clear"
              aria-label="Clear image filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="relative w-64">
          <input
            type="text"
            value={defaultUserInput}
            onChange={(e) => setDefaultUserInput(e.target.value)}
            placeholder="Filter by default user…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="template-list-default-user-filter"
            aria-label="Filter templates by default user"
          />
          {defaultUserInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setDefaultUserInput('')}
              data-testid="template-list-default-user-filter-clear"
              aria-label="Clear default user filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="w-40">
          <select
            value={osTypeFilter}
            onChange={(e) => setOsTypeFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="template-list-os-type-filter"
            aria-label="Filter templates by guest OS family"
          >
            <option value="">All OSes</option>
            <option value="linux">Linux</option>
            <option value="windows">Windows</option>
          </select>
        </div>
        <div className="w-52">
          <select
            value={osVariantFilter}
            onChange={(e) => setOsVariantFilter(e.target.value)}
            className="input w-full py-1.5 text-sm"
            data-testid="template-list-os-variant-filter"
            aria-label="Filter templates by Windows variant"
          >
            <option value="">All variants</option>
            <option value="windows-10">Windows 10</option>
            <option value="windows-11">Windows 11</option>
            <option value="windows-server-2019">Windows Server 2019</option>
            <option value="windows-server-2022">Windows Server 2022</option>
            <option value="windows-server-2025">Windows Server 2025</option>
          </select>
        </div>
        <div className="relative w-64">
          <input
            type="text"
            value={networkInput}
            onChange={(e) => setNetworkInput(e.target.value)}
            placeholder="Filter by network…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="template-list-network-filter"
            aria-label="Filter templates by network"
          />
          {networkInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setNetworkInput('')}
              data-testid="template-list-network-filter-clear"
              aria-label="Clear network filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="relative w-64">
          <input
            type="text"
            value={prefixInput}
            onChange={(e) => setPrefixInput(e.target.value)}
            placeholder="Filter by name prefix…"
            className="input w-full pr-8 py-1.5 text-sm"
            data-testid="template-list-prefix-filter"
            aria-label="Filter templates by name prefix (case-sensitive)"
          />
          {prefixInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setPrefixInput('')}
              data-testid="template-list-prefix-filter-clear"
              aria-label="Clear name prefix filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-steel-400">
          <label className="flex items-center gap-1">
            <span>Since</span>
            <input
              type="datetime-local"
              value={since}
              onChange={(e) => setSince(e.target.value ? `${e.target.value}:00Z` : '')}
              data-testid="template-list-since"
              aria-label="Templates created on or after"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200"
            />
          </label>
          <label className="flex items-center gap-1">
            <span>Until</span>
            <input
              type="datetime-local"
              value={until}
              onChange={(e) => setUntil(e.target.value ? `${e.target.value}:00Z` : '')}
              data-testid="template-list-until"
              aria-label="Templates created on or before"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200"
            />
          </label>
          {(since || until) && (
            <button
              type="button"
              className="text-steel-500 hover:text-steel-200"
              onClick={() => { setSince(''); setUntil(''); }}
              data-testid="template-list-time-range-clear"
              aria-label="Clear template time range"
            >
              Clear range
            </button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-steel-400">
          <label className="flex items-center gap-1">
            <span>Min CPUs</span>
            <input
              type="number"
              min="0"
              value={minCpusInput}
              onChange={(e) => setMinCpusInput(e.target.value)}
              data-testid="template-list-min-cpus"
              aria-label="Minimum vCPUs"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-20"
            />
          </label>
          <label className="flex items-center gap-1">
            <span>Max CPUs</span>
            <input
              type="number"
              min="0"
              value={maxCpusInput}
              onChange={(e) => setMaxCpusInput(e.target.value)}
              data-testid="template-list-max-cpus"
              aria-label="Maximum vCPUs"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-20"
            />
          </label>
          {(minCpusInput || maxCpusInput) && (
            <button
              type="button"
              className="text-steel-500 hover:text-steel-200"
              onClick={() => { setMinCpusInput(''); setMaxCpusInput(''); }}
              data-testid="template-list-cpu-range-clear"
              aria-label="Clear template CPU range"
            >
              Clear CPUs
            </button>
          )}
          <label className="flex items-center gap-1">
            <span>Min RAM (MB)</span>
            <input
              type="number"
              min="0"
              value={minRamInput}
              onChange={(e) => setMinRamInput(e.target.value)}
              data-testid="template-list-min-ram-mb"
              aria-label="Minimum RAM in MB"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-24"
            />
          </label>
          <label className="flex items-center gap-1">
            <span>Max RAM (MB)</span>
            <input
              type="number"
              min="0"
              value={maxRamInput}
              onChange={(e) => setMaxRamInput(e.target.value)}
              data-testid="template-list-max-ram-mb"
              aria-label="Maximum RAM in MB"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-24"
            />
          </label>
          {(minRamInput || maxRamInput) && (
            <button
              type="button"
              className="text-steel-500 hover:text-steel-200"
              onClick={() => { setMinRamInput(''); setMaxRamInput(''); }}
              data-testid="template-list-ram-range-clear"
              aria-label="Clear template RAM range"
            >
              Clear RAM
            </button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-steel-400">
          <label className="flex items-center gap-1">
            <span>Min disk (GB)</span>
            <input
              type="number"
              min="0"
              value={minDiskGbInput}
              onChange={(e) => setMinDiskGbInput(e.target.value)}
              data-testid="template-list-min-disk-gb"
              aria-label="Minimum disk GB"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-20"
            />
          </label>
          <label className="flex items-center gap-1">
            <span>Max disk (GB)</span>
            <input
              type="number"
              min="0"
              value={maxDiskGbInput}
              onChange={(e) => setMaxDiskGbInput(e.target.value)}
              data-testid="template-list-max-disk-gb"
              aria-label="Maximum disk GB"
              className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200 w-20"
            />
          </label>
          {(minDiskGbInput || maxDiskGbInput) && (
            <button
              type="button"
              className="text-steel-500 hover:text-steel-200"
              onClick={() => { setMinDiskGbInput(''); setMaxDiskGbInput(''); }}
              data-testid="template-list-disk-range-clear"
              aria-label="Clear template disk range"
            >
              Clear disk
            </button>
          )}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 mt-3 pt-3 border-t border-steel-800/40 text-xs text-steel-400" data-testid="template-list-sort-controls">
        <span>Sort by</span>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="template-list-sort-field"
        >
          <option value="id">ID</option>
          <option value="name">Name</option>
          <option value="created_at">Created</option>
          <option value="cpus">vCPUs</option>
          <option value="ram_mb">RAM (MB)</option>
          <option value="disk_gb">Disk (GB)</option>
          <option value="image">Image</option>
          <option value="default_user">Default user</option>
        </select>
        <select
          value={order}
          onChange={(e) => setOrder(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="template-list-sort-order"
        >
          <option value="asc">Ascending</option>
          <option value="desc">Descending</option>
        </select>
      </div>
      </FilterPanel>

      {loading && !templateList.length ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : !templateList.length ? (
        <div className="card">
          <EmptyState
            icon={Layers}
            title="No templates"
            description={
              searchFilter
                ? `No templates match "${searchFilter}".`
                : imageFilter
                ? `No templates use image "${imageFilter}".`
                : defaultUserFilter
                ? `No templates use default user "${defaultUserFilter}".`
                : networkFilter
                ? `No templates attach network "${networkFilter}".`
                : prefixFilter
                ? `No templates start with "${prefixFilter}".`
                : (since || until)
                ? 'No templates were created in the selected time range.'
                : (minCpusFilter || maxCpusFilter)
                ? 'No templates match the selected CPU range.'
                : (minRamFilter || maxRamFilter)
                ? 'No templates match the selected RAM range.'
                : (minDiskGbFilter || maxDiskGbFilter)
                ? 'No templates match the selected disk range.'
                : tagFilter
                ? `No templates carry tag "${tagFilter}".`
                : 'Create a template to save reusable VM defaults.'
            }
            action={
              !searchFilter && !imageFilter && !defaultUserFilter && !networkFilter && !prefixFilter && !since && !until &&
              !minCpusFilter && !maxCpusFilter && !minRamFilter && !maxRamFilter && !minDiskGbFilter && !maxDiskGbFilter && !tagFilter ? (
                <button className="btn-primary" onClick={() => setShowCreate(true)} data-testid="btn-new-template-empty">
                  <Plus size={15} /> New Template
                </button>
              ) : null
            }
          />
        </div>
      ) : (
        <div className="card overflow-hidden">
          <div className="flex items-center justify-between px-4 py-1.5 border-b border-steel-800/40 bg-steel-900/40">
            <label className="flex items-center gap-2 text-xs text-steel-400 cursor-pointer">
              <input
                type="checkbox"
                checked={allSelected}
                ref={(el) => { if (el) el.indeterminate = someSelected; }}
                onChange={toggleAll}
                data-testid="template-select-all"
              />
              {selected.size > 0 ? `${selected.size} selected` : 'Select all'}
            </label>
            <button
              className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40 disabled:cursor-not-allowed"
              onClick={handleBulkDelete}
              disabled={!selected.size || bulkMut.loading}
              data-testid="btn-bulk-delete-templates"
            >
              <Trash2 size={12} /> Delete selected
            </button>
          </div>
          {bulkResult && (
            <div
              className="px-4 py-2 border-b border-steel-800/40 bg-steel-900/40 text-xs text-steel-400 flex items-center justify-between"
              data-testid="template-bulk-result"
            >
              <span>
                {(bulkResult.results || []).filter(r => r.success).length} of {(bulkResult.results || []).length} succeeded
                {bulkResult.results?.some(r => !r.success) && (
                  <span className="text-red-400">
                    {' '}· {bulkResult.results.filter(r => !r.success).length} failed
                  </span>
                )}
              </span>
              <button className="btn-ghost text-xs" onClick={dismissBulkResult}>Dismiss</button>
            </div>
          )}
          <table className="w-full" data-testid="template-table">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell w-8"></th>
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Image</th>
                <th className="table-header table-cell">Resources</th>
                <th className="table-header table-cell">Tags</th>
                <th className="table-header table-cell">Created</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {templateList.map(tpl => (
                <tr key={tpl.id} className="hover:bg-steel-800/20 transition-colors" data-testid={`template-row-${tpl.name}`}>
                  <td className="table-cell">
                    <input
                      type="checkbox"
                      checked={selected.has(tpl.id)}
                      onChange={() => toggleOne(tpl.id)}
                      data-testid={`template-checkbox-${tpl.name}`}
                    />
                  </td>
                  <td className="table-cell">
                    <div className="flex items-center gap-2.5">
                      <div className="w-7 h-7 rounded bg-steel-800/60 border border-steel-700/30 flex items-center justify-center">
                        <Layers size={13} className="text-steel-500" />
                      </div>
                      <div>
                        <span className="font-mono text-sm text-steel-100">{tpl.name}</span>
                        {tpl.description && (
                          <p
                            className="text-[11px] text-steel-400 mt-0.5 max-w-[260px] truncate"
                            title={tpl.description}
                            data-testid={`template-description-${tpl.name}`}
                          >
                            {tpl.description}
                          </p>
                        )}
                      </div>
                    </div>
                  </td>
                  <td className="table-cell font-mono text-xs text-steel-400">
                    {tpl.image || '—'}
                  </td>
                  <td className="table-cell text-xs text-steel-400">
                    <span className="font-mono">
                      {tpl.cpus || '?'} CPU · {tpl.ram_mb ? `${tpl.ram_mb} MB` : '? MB'} · {tpl.disk_gb ? `${tpl.disk_gb} GB` : '? GB'}
                    </span>
                  </td>
                  <td className="table-cell">
                    {tpl.tags?.length > 0 ? (
                      <div className="flex flex-wrap gap-1" data-testid={`template-tags-${tpl.name}`}>
                        {tpl.tags.map(tag => (
                          <span key={tag} className="badge bg-steel-800/60 text-steel-300 border-steel-700/40">
                            #{tag}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-steel-600">—</span>
                    )}
                  </td>
                  <td className="table-cell text-xs text-steel-500">
                    {tpl.created_at ? new Date(tpl.created_at).toLocaleDateString() : '—'}
                  </td>
                  <td className="table-cell text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        className="btn-ghost text-xs text-steel-400 hover:text-steel-200"
                        onClick={() => setEditing(tpl)}
                        data-testid={`btn-edit-template-${tpl.name}`}
                      >
                        <Pencil size={13} /> Edit
                      </button>
                      <button
                        className="btn-ghost text-xs text-red-400 hover:text-red-300"
                        onClick={() => handleDelete(tpl.id, tpl.name)}
                        data-testid={`btn-delete-template-${tpl.name}`}
                      >
                        <Trash2 size={13} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!!templateList.length && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={totalTemplates}
          itemLabel="templates"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}
    </div>
  );
}

function EditTemplateModal({ template, onClose, onSaved }) {
  const [description, setDescription] = useState('');
  const [tagsField, setTagsField] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (template) {
      setDescription(template.description || '');
      setTagsField((template.tags || []).join(','));
      setError('');
    }
  }, [template]);

  if (!template) return null;

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      // PATCH semantics on the daemon: empty `description` = leave unchanged,
      // nil `tags` = leave unchanged, explicit `[]` clears the tag set. We
      // diff against the current values so a no-op edit closes silently.
      const patch = {};
      const trimmedDescription = description.trim();
      if (trimmedDescription !== (template.description || '')) {
        patch.description = trimmedDescription;
      }
      const newTags = tagsField.split(',').map(t => t.trim()).filter(Boolean);
      const currentTags = (template.tags || []).join(',');
      if (newTags.join(',') !== currentTags) {
        patch.tags = newTags;
      }
      if (Object.keys(patch).length === 0) {
        onClose();
        return;
      }
      await templatesApi.update(template.id, patch);
      onSaved();
      onClose();
    } catch (e) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!template} onClose={onClose} title={`Edit Template — ${template.name}`}>
      <div className="space-y-4" data-testid="edit-template-modal">
        <div>
          <label className="label">Description</label>
          <input
            className="input"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="edit-template-description"
          />
        </div>
        <div>
          <label className="label">Tags</label>
          <input
            className="input"
            placeholder="comma,separated,tags"
            value={tagsField}
            onChange={e => setTagsField(e.target.value)}
            data-testid="edit-template-tags"
          />
        </div>
        {error && <p className="text-sm text-red-400">Error: {error}</p>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onClose} disabled={saving}>Cancel</button>
          <button className="btn-primary" onClick={handleSave} disabled={saving} data-testid="btn-save-template">
            {saving ? <Spinner size={14} /> : null}
            Save
          </button>
        </div>
      </div>
    </Modal>
  );
}

// CreateTemplateModal collects the full template spec — name, image, resource
// sizing, default user, description, and tags — and POSTs it to /templates.
// Unlike EditTemplateModal (which only PATCHes description + tags), every field
// here is settable because image / resources / name are immutable post-create.
function CreateTemplateModal({ open, onClose, onCreated }) {
  const emptyForm = { name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, default_user: '', description: '', tags: '' };
  const [form, setForm] = useState(emptyForm);
  const [error, setError] = useState('');
  const createMut = useMutation(templatesApi.create);
  const { data: imageResponse } = useFetch(() => imagesApi.list(), [], 0);
  const imageList = safeArray(imageResponse?.data || imageResponse);

  // Reset the form each time the modal is (re)opened so a prior aborted draft
  // never leaks into the next create.
  useEffect(() => {
    if (open) {
      setForm(emptyForm);
      setError('');
    }
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));
  const updateNum = (field) => (e) => setForm(f => ({ ...f, [field]: parseInt(e.target.value, 10) || 0 }));

  const humanSize = (bytes) => {
    if (!bytes) return '';
    if (bytes >= 1073741824) return ` · ${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return ` · ${(bytes / 1048576).toFixed(1)} MB`;
    return ` · ${bytes} B`;
  };

  const handleSubmit = async () => {
    setError('');
    const spec = {
      name: form.name.trim(),
      image: form.image.trim(),
      cpus: form.cpus,
      ram_mb: form.ram_mb,
      disk_gb: form.disk_gb,
    };
    const defaultUser = form.default_user.trim();
    if (defaultUser) spec.default_user = defaultUser;
    const description = form.description.trim();
    if (description) spec.description = description;
    const tags = form.tags.split(',').map(t => t.trim()).filter(Boolean);
    if (tags.length) spec.tags = tags;
    try {
      await createMut.execute(spec);
      onCreated();
      onClose();
    } catch (e) {
      setError(e.message);
    }
  };

  const noImages = imageList.length === 0;

  return (
    <Modal open={open} onClose={onClose} title="Create Template">
      <div className="space-y-4" data-testid="create-template-modal">
        <div>
          <label className="label">Name</label>
          <input
            className="input"
            placeholder="rocky9-base"
            value={form.name}
            onChange={update('name')}
            autoFocus
            data-testid="create-template-name"
          />
        </div>
        <div>
          <label className="label">Base Image</label>
          {noImages ? (
            <input
              className="input"
              placeholder="/images/rocky9.qcow2"
              value={form.image}
              onChange={update('image')}
              data-testid="create-template-image"
            />
          ) : (
            <select className="input" value={form.image} onChange={update('image')} data-testid="create-template-image">
              <option value="">Select an image…</option>
              {imageList.map(img => (
                <option key={img.id} value={img.path}>
                  {img.name}{humanSize(img.size_bytes)}
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="label">vCPUs</label>
            <input type="number" min="1" className="input" value={form.cpus} onChange={updateNum('cpus')} data-testid="create-template-cpus" />
          </div>
          <div>
            <label className="label">RAM (MB)</label>
            <input type="number" min="128" className="input" value={form.ram_mb} onChange={updateNum('ram_mb')} data-testid="create-template-ram" />
          </div>
          <div>
            <label className="label">Disk (GB)</label>
            <input type="number" min="1" className="input" value={form.disk_gb} onChange={updateNum('disk_gb')} data-testid="create-template-disk" />
          </div>
        </div>
        <div>
          <label className="label">Default User <span className="text-steel-500 font-normal">(optional)</span></label>
          <input
            className="input"
            placeholder="leave blank for root / image default"
            value={form.default_user}
            onChange={update('default_user')}
            data-testid="create-template-default-user"
          />
        </div>
        <div>
          <label className="label">Description <span className="text-steel-500 font-normal">(optional)</span></label>
          <input className="input" value={form.description} onChange={update('description')} data-testid="create-template-description" />
        </div>
        <div>
          <label className="label">Tags <span className="text-steel-500 font-normal">(optional)</span></label>
          <input className="input" placeholder="comma,separated,tags" value={form.tags} onChange={update('tags')} data-testid="create-template-tags" />
        </div>
        {(error || createMut.error) && <p className="text-sm text-red-400">Error: {error || createMut.error}</p>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onClose} disabled={createMut.loading}>Cancel</button>
          <button
            className="btn-primary"
            onClick={handleSubmit}
            disabled={createMut.loading || !form.name.trim() || !form.image.trim()}
            data-testid="btn-submit-create-template"
          >
            {createMut.loading ? <Spinner size={14} /> : <Plus size={15} />}
            Create
          </button>
        </div>
      </div>
    </Modal>
  );
}
