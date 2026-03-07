import { useState, useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Plus, Server, Play, Square, Trash2, MoreVertical } from 'lucide-react';
import { vms, images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, StatusBadge, Modal, EmptyState, Spinner, ErrorBanner } from '../components/Shared';

export default function VMList() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showCreate, setShowCreate] = useState(searchParams.get('create') === '1');
  const [actionMenu, setActionMenu] = useState(null);
  const { data: vmList, loading, error, refresh } = useFetch(() => vms.list(), [], 5000);
  const navigate = useNavigate();

  useEffect(() => {
    if (searchParams.get('create') === '1') {
      setShowCreate(true);
      setSearchParams({});
    }
  }, [searchParams, setSearchParams]);

  return (
    <div>
      <PageHeader
        title="Machines"
        subtitle={`${vmList?.length || 0} total`}
        actions={
          <button className="btn-primary" onClick={() => setShowCreate(true)}>
            <Plus size={15} /> New Machine
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

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
        <div className="space-y-2">
          {vmList.map(vm => (
            <VMRow
              key={vm.id}
              vm={vm}
              onNavigate={() => navigate(`/vms/${vm.id}`)}
              actionMenu={actionMenu}
              setActionMenu={setActionMenu}
              onRefresh={refresh}
            />
          ))}
        </div>
      )}

      <CreateVMModal open={showCreate} onClose={() => setShowCreate(false)} onCreated={refresh} />
    </div>
  );
}

function VMRow({ vm, onNavigate, actionMenu, setActionMenu, onRefresh }) {
  const startMut = useMutation(vms.start);
  const stopMut  = useMutation(vms.stop);
  const delMut   = useMutation(vms.delete);

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
    <div className="card-hover flex items-center gap-4 px-4 py-3 group" onClick={onNavigate}>
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
          {vm.spec.cpus} vCPU · {vm.spec.ram_mb} MB · {vm.spec.disk_gb} GB
          {vm.ip && <> · {vm.ip}</>}
        </p>
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
  const [form, setForm] = useState({ name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, ssh_pub_key: '' });
  const createMut = useMutation(vms.create);
  const { data: imageList } = useFetch(() => imagesApi.list(), [], 0);

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));
  const updateNum = (field) => (e) => setForm(f => ({ ...f, [field]: parseInt(e.target.value, 10) || 0 }));

  const humanSize = (bytes) => {
    if (!bytes) return '';
    if (bytes >= 1073741824) return ` · ${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return ` · ${(bytes / 1048576).toFixed(1)} MB`;
    return ` · ${bytes} B`;
  };

  const handleSubmit = async () => {
    try {
      await createMut.execute(form);
      onCreated();
      onClose();
      setForm({ name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, ssh_pub_key: '' });
    } catch { /* error displayed via mutation */ }
  };

  const noImages = imageList && imageList.length === 0;

  return (
    <Modal open={open} onClose={onClose} title="Create Machine" wide>
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Name</label>
            <input className="input" placeholder="my-server" value={form.name} onChange={update('name')} />
          </div>
          <div>
            <label className="label">Base Image</label>
            {noImages ? (
              <div className="input flex items-center text-steel-500 text-xs">
                No images available — upload one in the Images section first.
              </div>
            ) : (
              <select className="input" value={form.image} onChange={update('image')}>
                <option value="">Select an image…</option>
                {(imageList || []).map(img => (
                  <option key={img.id} value={img.path}>
                    {img.name}{humanSize(img.size_bytes)}
                  </option>
                ))}
              </select>
            )}
          </div>
        </div>

        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="label">vCPUs</label>
            <input className="input" type="number" min={1} value={form.cpus} onChange={updateNum('cpus')} />
          </div>
          <div>
            <label className="label">RAM (MB)</label>
            <input className="input" type="number" min={256} step={256} value={form.ram_mb} onChange={updateNum('ram_mb')} />
          </div>
          <div>
            <label className="label">Disk (GB)</label>
            <input className="input" type="number" min={1} value={form.disk_gb} onChange={updateNum('disk_gb')} />
          </div>
        </div>

        <div>
          <label className="label">SSH Public Key (optional)</label>
          <textarea
            className="input h-20 resize-none"
            placeholder="ssh-rsa AAAA..."
            value={form.ssh_pub_key}
            onChange={update('ssh_pub_key')}
          />
        </div>

        {createMut.error && (
          <p className="text-sm text-red-400">Error: {createMut.error}</p>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" onClick={handleSubmit} disabled={createMut.loading || !form.name || !form.image}>
            {createMut.loading ? <Spinner size={14} /> : <Plus size={15} />}
            Create
          </button>
        </div>
      </div>
    </Modal>
  );
}
