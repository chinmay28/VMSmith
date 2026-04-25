import { useState, useEffect, useMemo } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Plus, Server, Play, Square, Trash2, MoreVertical, Network, X, CheckSquare } from 'lucide-react';
import { vms, images as imagesApi, templates as templatesApi, host as hostApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, StatusBadge, Modal, EmptyState, Spinner, ErrorBanner, PaginationControls } from '../components/Shared';

const DEFAULT_PER_PAGE = 25;

export default function VMList() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showCreate, setShowCreate] = useState(searchParams.get('create') === '1');
  const [actionMenu, setActionMenu] = useState(null);
  const [tagFilter, setTagFilter] = useState('');
  const [selectedIds, setSelectedIds] = useState([]);
  const [bulkMessage, setBulkMessage] = useState(null);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const { data: vmResponse, loading, error, refresh } = useFetch(
    () => vms.list({ tag: tagFilter, page, perPage }),
    [tagFilter, page, perPage],
    5000,
  );
  const navigate = useNavigate();
  const vmList = vmResponse?.data || [];
  const totalVMs = vmResponse?.meta?.totalCount ?? vmList.length;
  const allTags = [...new Set((vmList || []).flatMap(vm => vm.tags || []))].sort();

  const visibleVMs = vmList || [];
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
  }, [tagFilter]);

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
          <button className="btn-primary" onClick={() => setShowCreate(true)} data-testid="btn-new-vm">
            <Plus size={15} /> New Machine
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      {bulkMessage && (
        <div className="mb-4">
          <ErrorBanner message={bulkMessage} onRetry={() => setBulkMessage(null)} />
        </div>
      )}

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

      <CreateVMModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={refresh} />
    </div>
  );
}

function BulkActionBar({ selectedVMs, totalVisible, allSelected, onToggleSelectAll, onClearSelection, onDone }) {
  const startMut = useMutation(vms.start);
  const stopMut = useMutation(vms.stop);
  const deleteMut = useMutation(vms.delete);

  const runningCount = selectedVMs.filter(vm => vm.state === 'running').length;
  const stoppedCount = selectedVMs.filter(vm => vm.state === 'stopped').length;
  const hasSelection = selectedVMs.length > 0;
  const mutationError = startMut.error || stopMut.error || deleteMut.error;
  const busy = startMut.loading || stopMut.loading || deleteMut.loading;

  const executeBulk = async (action) => {
    if (!hasSelection || busy) return;

    if (action === 'delete') {
      const names = selectedVMs.map(vm => vm.name).join(', ');
      if (!window.confirm(`Delete ${selectedVMs.length} machine(s)?\n\n${names}`)) return;
    }

    const targets = selectedVMs.filter(vm => {
      if (action === 'start') return vm.state === 'stopped';
      if (action === 'stop') return vm.state === 'running';
      return true;
    });

    const skipped = selectedVMs.length - targets.length;
    if (targets.length === 0) {
      if (action === 'start') onDone('Nothing to start — selected machines are already running.');
      if (action === 'stop') onDone('Nothing to stop — selected machines are already stopped.');
      return;
    }

    let success = 0;
    let failure = 0;

    for (const vm of targets) {
      try {
        if (action === 'start') await startMut.execute(vm.id);
        if (action === 'stop') await stopMut.execute(vm.id);
        if (action === 'delete') await deleteMut.execute(vm.id);
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
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm text-steel-100 truncate">{vm.name}</span>
          <StatusBadge state={vm.state} />
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

function CreateVMModal({ open, onClose, onCreated }) {
  const emptyForm = { name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, description: '', tags: '', ssh_pub_key: '', default_user: '', nat_static_ip: '', nat_gateway: '', template_id: '' };
  const [form, setForm] = useState(emptyForm);
  const [networks, setNetworks] = useState([]);
  const [activeTab, setActiveTab] = useState('basic');
  const createMut = useMutation(vms.create);
  const { data: imageResponse } = useFetch(() => imagesApi.list(), [], 0);
  const { data: templateResponse } = useFetch(() => templatesApi.list(), [], 0);
  const { data: hostIfaces } = useFetch(() => hostApi.interfaces(), [], 0);
  const imageList = imageResponse?.data || imageResponse || [];
  const templates = templateResponse?.data || templateResponse || [];

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

  const handleSubmit = async () => {
    const spec = { ...form };
    spec.tags = form.tags.split(',').map(tag => tag.trim()).filter(Boolean);
    if (!spec.description) delete spec.description;
    if (spec.tags.length === 0) delete spec.tags;
    if (!spec.nat_static_ip) delete spec.nat_static_ip;
    if (!spec.nat_gateway)   delete spec.nat_gateway;
    if (!spec.template_id) delete spec.template_id;
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
      await createMut.execute(spec);
      onCreated();
      onClose();
      setForm(emptyForm);
      setNetworks([]);
      setActiveTab('basic');
    } catch { /* error displayed via mutation */ }
  };

  const noImages = imageList.length === 0;
  const physIfaces = (hostIfaces || []).filter(i => i.is_physical && i.is_up);
  const natIface = (hostIfaces || []).find(i => i.name === 'vmsmith0');

  const advancedCount = [
    form.description, form.tags, form.ssh_pub_key, form.default_user, form.nat_static_ip, form.nat_gateway,
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
                  <select className="input" value={form.template_id} onChange={e => applyTemplate(e.target.value)} data-testid="input-vm-template">
                    <option value="">No template</option>
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
                <div className="flex items-end">
                  {form.template_id ? (
                    <p className="text-xs text-steel-500 pb-2" data-testid="template-hint">
                      Template defaults are prefilled below, and anything you change here overrides them.
                    </p>
                  ) : <div />}
                </div>
              </div>

              <div className="grid grid-cols-3 gap-4">
                <div>
                  <label className="label">vCPUs</label>
                  <input className="input" type="number" min={1} value={form.cpus} onChange={updateNum('cpus')} data-testid="input-vm-cpus" />
                </div>
                <div>
                  <label className="label">RAM (MB)</label>
                  <input className="input" type="number" min={256} step={256} value={form.ram_mb} onChange={updateNum('ram_mb')} data-testid="input-vm-ram" />
                </div>
                <div>
                  <label className="label">Disk (GB)</label>
                  <input className="input" type="number" min={1} value={form.disk_gb} onChange={updateNum('disk_gb')} data-testid="input-vm-disk" />
                </div>
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
                    <label className="label">Default SSH User <span className="text-steel-500 font-normal">(blank = root)</span></label>
                    <input className="input font-mono" placeholder="root" value={form.default_user} onChange={update('default_user')} data-testid="input-vm-default-user" />
                  </div>
                  <div>
                    <label className="label">SSH Public Key</label>
                    <input className="input font-mono" placeholder="ssh-rsa AAAA…" value={form.ssh_pub_key} onChange={update('ssh_pub_key')} data-testid="input-vm-ssh-key" />
                  </div>
                </div>
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
