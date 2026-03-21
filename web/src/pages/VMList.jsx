import { useState, useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Plus, Server, Play, Square, Trash2, MoreVertical, Network, X } from 'lucide-react';
import { vms, images as imagesApi, host as hostApi } from '../api/client';
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
  const emptyForm = { name: '', image: '', cpus: 2, ram_mb: 2048, disk_gb: 20, ssh_pub_key: '', nat_static_ip: '', nat_gateway: '' };
  const [form, setForm] = useState(emptyForm);
  const [networks, setNetworks] = useState([]);
  const createMut = useMutation(vms.create);
  const { data: imageList } = useFetch(() => imagesApi.list(), [], 0);
  const { data: hostIfaces } = useFetch(() => hostApi.interfaces(), [], 0);

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));
  const updateNum = (field) => (e) => setForm(f => ({ ...f, [field]: parseInt(e.target.value, 10) || 0 }));

  const humanSize = (bytes) => {
    if (!bytes) return '';
    if (bytes >= 1073741824) return ` · ${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return ` · ${(bytes / 1048576).toFixed(1)} MB`;
    return ` · ${bytes} B`;
  };

  const addNetwork = () => setNetworks(n => [...n, { mode: 'macvtap', host_interface: '', static_ip: '', gateway: '' }]);
  const removeNetwork = (i) => setNetworks(n => n.filter((_, idx) => idx !== i));
  const updateNet = (i, field, val) => setNetworks(n => n.map((net, idx) => idx === i ? { ...net, [field]: val } : net));

  const handleSubmit = async () => {
    const spec = { ...form };
    if (!spec.nat_static_ip) delete spec.nat_static_ip;
    if (!spec.nat_gateway)   delete spec.nat_gateway;
    if (networks.length > 0) {
      spec.networks = networks.map(n => {
        const att = { mode: n.mode };
        if (n.mode === 'bridge') att.bridge = n.host_interface;
        else att.host_interface = n.host_interface;
        if (n.static_ip) att.static_ip = n.static_ip;
        if (n.gateway)   att.gateway   = n.gateway;
        return att;
      });
    }
    try {
      await createMut.execute(spec);
      onCreated();
      onClose();
      setForm(emptyForm);
      setNetworks([]);
    } catch { /* error displayed via mutation */ }
  };

  const noImages = imageList && imageList.length === 0;
  // Physical interfaces only, for macvtap mode
  const physIfaces = (hostIfaces || []).filter(i => i.is_physical && i.is_up);
  // vmsmith NAT bridge interface (informational)
  const natIface = (hostIfaces || []).find(i => i.name === 'vmsmith0');

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

        {/* Primary NAT network */}
        <div>
          <div className="flex items-center justify-between mb-2">
            <label className="label mb-0">Primary Network (NAT)</label>
            {natIface && (
              <span className="text-xs font-mono text-steel-500">
                {natIface.name}{natIface.ips?.length ? ` · ${natIface.ips[0]}` : ''}{natIface.is_up ? '' : ' · down'}
              </span>
            )}
          </div>
          <div className="p-2 rounded border border-steel-700/40 bg-steel-900/40">
            <p className="text-xs text-steel-500 mb-2">
              vmsmith-net (192.168.100.0/24) — leave blank for DHCP, or set a static IP.
            </p>
            <div className="grid grid-cols-2 gap-2">
              <div>
                <label className="label text-[10px]">Static IP (optional)</label>
                <input
                  className="input py-1 text-xs font-mono"
                  placeholder="192.168.100.50/24"
                  value={form.nat_static_ip}
                  onChange={update('nat_static_ip')}
                />
              </div>
              <div>
                <label className="label text-[10px]">Gateway (optional)</label>
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

        {/* Extra network attachments */}
        <div>
          <div className="flex items-center justify-between mb-2">
            <label className="label mb-0">Extra Networks</label>
            <button className="btn-ghost text-xs" type="button" onClick={addNetwork}>
              <Plus size={12} /> Add
            </button>
          </div>
          {networks.length === 0 ? (
            <p className="text-xs text-steel-500 px-1">
              No extra interfaces. The NAT network above is always attached.
            </p>
          ) : (
            <div className="space-y-2">
              {networks.map((net, i) => (
                <div key={i} className="flex items-start gap-2 p-2 rounded border border-steel-700/40 bg-steel-900/40">
                  <Network size={13} className="text-steel-500 mt-2 shrink-0" />
                  <div className="flex-1 grid grid-cols-2 gap-2">
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
                    <div>
                      <label className="label text-[10px]">Static IP (optional)</label>
                      <input
                        className="input py-1 text-xs font-mono"
                        placeholder="10.0.0.2/24"
                        value={net.static_ip}
                        onChange={e => updateNet(i, 'static_ip', e.target.value)}
                      />
                    </div>
                    <div>
                      <label className="label text-[10px]">Gateway (optional)</label>
                      <input
                        className="input py-1 text-xs font-mono"
                        placeholder="10.0.0.1"
                        value={net.gateway}
                        onChange={e => updateNet(i, 'gateway', e.target.value)}
                      />
                    </div>
                  </div>
                  <button className="btn-ghost text-red-400 hover:text-red-300 mt-1 shrink-0" onClick={() => removeNetwork(i)}>
                    <X size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}
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
