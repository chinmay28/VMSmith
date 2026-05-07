import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  ArrowLeft, Play, Square, Trash2, Camera, Network,
  Plus, RotateCcw, Download, Clock, Pencil, Copy
} from 'lucide-react';
import { vms, snapshots, ports, images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { useVMStats, STATS_STATE_LOADING, STATS_STATE_ERROR } from '../hooks/useVMStats';
import { buildChartData } from '../hooks/vmStatsHelpers.js';
import { StatusBadge, Modal, Spinner, ErrorBanner, EmptyState, LiveIndicator } from '../components/Shared';
import MetricChart from '../components/MetricChart';
import { normalizeSpec, safeArray } from '../utils/normalize';
import Activity from './Activity';

export default function VMDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { data: vm, loading, error, refresh } = useFetch(() => vms.get(id), [id], 5000);
  const { data: snapList, refresh: refreshSnaps } = useFetch(() => snapshots.list(id), [id], 10000);
  const { data: portList, refresh: refreshPorts } = useFetch(() => ports.list(id), [id], 10000);

  const [showSnapModal, setShowSnapModal] = useState(false);
  const [showPortModal, setShowPortModal] = useState(false);
  const [showImageModal, setShowImageModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [showCloneModal, setShowCloneModal] = useState(false);
  const [activeTab, setActiveTab] = useState('overview');

  const startMut   = useMutation(vms.start);
  const stopMut    = useMutation(vms.stop);
  const restartMut = useMutation(vms.restart);
  const deleteMut  = useMutation(vms.delete);

  if (loading && !vm) return <div className="flex justify-center py-20"><Spinner size={20} /></div>;
  if (error) return <ErrorBanner message={error} />;
  if (!vm) return null;

  const spec = normalizeSpec(vm.spec);
  const tags = safeArray(vm.tags);
  const networks = safeArray(spec.networks);
  const cpuText = Number.isFinite(spec.cpus) ? spec.cpus : '—';
  const ramText = Number.isFinite(spec.ram_mb) ? spec.ram_mb : '—';
  const diskText = Number.isFinite(spec.disk_gb) ? spec.disk_gb : '—';
  const createdText = vm.created_at ? new Date(vm.created_at).toLocaleString() : '—';
  const sshUser = spec.default_user || 'root';

  const handleDelete = async () => {
    if (!window.confirm(`Delete ${vm.name}? This cannot be undone.`)) return;
    await deleteMut.execute(id);
    navigate('/vms');
  };

  return (
    <div className="animate-fade-in">
      {/* Header */}
      <div className="flex items-start justify-between mb-6">
        <div className="flex items-center gap-3">
          <button className="btn-ghost -ml-2" onClick={() => navigate('/vms')} data-testid="back-link">
            <ArrowLeft size={16} />
          </button>
          <div>
            <div className="flex items-center gap-2.5">
              <h1 className="font-display font-bold text-2xl text-steel-100 tracking-tight" data-testid="vm-detail-name">{vm.name}</h1>
              <span data-testid="vm-detail-state"><StatusBadge state={vm.state} /></span>
            </div>
            <p className="text-xs font-mono text-steel-500 mt-0.5">{vm.id}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {vm.state === 'stopped' && (
            <button className="btn-primary" onClick={() => { startMut.execute(id).then(refresh); }} data-testid="btn-start">
              <Play size={14} /> Start
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { stopMut.execute(id).then(refresh); }} data-testid="btn-stop">
              <Square size={14} /> Stop
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { restartMut.execute(id).then(refresh); }} data-testid="btn-restart" title="Graceful stop and start">
              <RotateCcw size={14} /> Restart
            </button>
          )}
          <button data-testid="btn-edit-vm" className="btn-secondary" onClick={() => setShowEditModal(true)} title="Edit resources">
            <Pencil size={14} /> Edit
          </button>
          <button data-testid="btn-clone-vm" className="btn-secondary" onClick={() => setShowCloneModal(true)} title="Clone VM">
            <Copy size={14} /> Clone
          </button>
          <button className="btn-danger" onClick={handleDelete} data-testid="btn-delete">
            <Trash2 size={14} /> Delete
          </button>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 mb-4 border-b border-steel-800/60">
        <TabButton active={activeTab === 'overview'} onClick={() => setActiveTab('overview')} testId="tab-overview">
          Overview
        </TabButton>
        <TabButton active={activeTab === 'metrics'} onClick={() => setActiveTab('metrics')} testId="tab-metrics">
          Metrics
        </TabButton>
        <TabButton active={activeTab === 'activity'} onClick={() => setActiveTab('activity')} testId="tab-activity">
          Activity
        </TabButton>
      </div>

      {activeTab === 'activity' ? (
        <div className="min-h-[300px]" data-testid="vm-detail-activity">
          <Activity vmId={id} embedded />
        </div>
      ) : activeTab === 'metrics' ? (
        <div className="min-h-[300px]" data-testid="vm-detail-metrics">
          <VMMetrics vmId={id} />
        </div>
      ) : (
      <>
      {/* Info grid */}
      <div className="grid grid-cols-2 gap-3 mb-6">
        <InfoCard label="IP Address" value={vm.ip || 'Not assigned'} mono testId="vm-detail-ip" />
        <InfoCard label="Image" value={spec.image || '—'} mono testId="vm-detail-image" />
        <InfoCard label="Resources" value={`${cpuText} vCPU · ${ramText} MB RAM · ${diskText} GB disk`} testId="vm-detail-resources" />
        <InfoCard label="Created" value={createdText} />
        <InfoCard label="Description" value={vm.description || '—'} />
        <InfoCard label="Tags" value={tags.length ? tags.map(tag => `#${tag}`).join(' · ') : '—'} mono />
        <InfoCard
          label="SSH"
          value={vm.ip ? `ssh ${sshUser}@${vm.ip}` : `user: ${sshUser}`}
          mono
        />
        <InfoCard
          label="Auto-start at boot"
          value={spec.auto_start ? 'On' : 'Off'}
          testId="vm-detail-auto-start"
        />
        <InfoCard
          label="Delete protection"
          value={spec.locked ? 'Locked' : 'Unlocked'}
          testId="vm-detail-locked"
        />
      </div>

      {/* Attached Networks */}
      {networks.length > 0 && (
        <div className="card mb-4">
          <div className="flex items-center gap-2 px-4 py-3 border-b border-steel-800/40">
            <Network size={14} className="text-steel-500" />
            <h2 className="text-sm font-display font-semibold text-steel-300">Extra Networks</h2>
          </div>
          <div className="divide-y divide-steel-800/40">
            {networks.map((net, i) => (
              <div key={i} className="flex items-center gap-3 px-4 py-2.5">
                <span className="font-mono text-xs text-steel-500 w-10">eth{i + 1}</span>
                <span className="badge bg-steel-800/60 text-steel-400 border-steel-700/40">{net.mode}</span>
                <span className="font-mono text-sm text-steel-200">
                  {net.host_interface || net.bridge}
                </span>
                {net.static_ip ? (
                  <span className="font-mono text-xs text-emerald-400">{net.static_ip}</span>
                ) : (
                  <span className="text-xs text-steel-500">DHCP</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Snapshots + Ports side by side */}
      <div className="grid grid-cols-2 gap-4">
        {/* Snapshots */}
        <div className="card">
          <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
            <div className="flex items-center gap-2">
              <Camera size={14} className="text-steel-500" />
              <h2 className="text-sm font-display font-semibold text-steel-300">Snapshots</h2>
            </div>
            <button className="btn-ghost text-xs" onClick={() => setShowSnapModal(true)} data-testid="btn-new-snapshot">
              <Plus size={13} /> New
            </button>
          </div>
          <SnapshotList vmId={id} snapList={snapList} refreshSnaps={refreshSnaps} />
        </div>

        {/* Port Forwards */}
        <div className="card">
          <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
            <div className="flex items-center gap-2">
              <Network size={14} className="text-steel-500" />
              <h2 className="text-sm font-display font-semibold text-steel-300">Port Forwards</h2>
            </div>
            <button className="btn-ghost text-xs" onClick={() => setShowPortModal(true)}>
              <Plus size={13} /> Add
            </button>
          </div>
          <PortList vmId={id} portList={portList} refreshPorts={refreshPorts} />
        </div>
      </div>

      {/* Export to image */}
      <div className="mt-4">
        <button className="btn-secondary" onClick={() => setShowImageModal(true)}>
          <Download size={14} /> Export as Image
        </button>
      </div>
      </>
      )}

      {/* Modals */}
      <EditVMModal vm={vm} open={showEditModal} onClose={() => setShowEditModal(false)} onUpdated={refresh} />
      <CloneVMModal vm={vm} open={showCloneModal} onClose={() => setShowCloneModal(false)} />
      <CreateSnapshotModal vmId={id} open={showSnapModal} onClose={() => setShowSnapModal(false)} onCreated={refreshSnaps} />
      <AddPortModal vmId={id} open={showPortModal} onClose={() => setShowPortModal(false)} onCreated={refreshPorts} />
      <ExportImageModal vmId={id} open={showImageModal} onClose={() => setShowImageModal(false)} />
    </div>
  );
}

function InfoCard({ label, value, mono, testId }) {
  return (
    <div className="card px-4 py-3">
      <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
      <p className={`text-sm text-steel-200 mt-0.5 ${mono ? 'font-mono' : ''}`} data-testid={testId}>{value}</p>
    </div>
  );
}

function TabButton({ active, onClick, children, testId }) {
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      className={`px-4 py-2 text-sm transition-colors border-b-2 -mb-px ${
        active
          ? 'border-forge-500 text-forge-300'
          : 'border-transparent text-steel-400 hover:text-steel-200'
      }`}
    >
      {children}
    </button>
  );
}

function CloneVMModal({ vm, open, onClose }) {
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const cloneMut = useMutation((cloneName) => vms.clone(vm.id, cloneName));

  useEffect(() => {
    if (open && vm) {
      setName(`${vm.name}-clone`);
      cloneMut.reset();
    }
  }, [open, vm, cloneMut.reset]);

  const handleClose = () => {
    cloneMut.reset();
    setName('');
    onClose();
  };

  const handleSubmit = async () => {
    try {
      const cloned = await cloneMut.execute(name.trim());
      handleClose();
      if (cloned?.id) {
        navigate(`/vms/${cloned.id}`);
      }
    } catch {
      // Error shown inline.
    }
  };

  return (
    <Modal open={open} onClose={handleClose} title="Clone Machine">
      <div className="space-y-4">
        <p className="text-xs text-steel-500">
          Create a copy of this VM with a new name. The source VM should be stopped before cloning.
        </p>
        <div>
          <label className="label">New VM Name</label>
          <input
            data-testid="input-clone-name"
            className="input font-mono"
            type="text"
            placeholder="my-vm-clone"
            value={name}
            onChange={e => setName(e.target.value)}
            autoFocus
          />
        </div>
        {cloneMut.error && <p className="text-sm text-red-400">{cloneMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button data-testid="btn-cancel-clone" className="btn-secondary" onClick={handleClose}>Cancel</button>
          <button data-testid="btn-submit-clone" className="btn-primary" onClick={handleSubmit} disabled={!name.trim() || cloneMut.loading}>
            {cloneMut.loading ? <Spinner size={14} /> : <Copy size={14} />} Clone VM
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Edit VM Modal ---
function EditVMModal({ vm, open, onClose, onUpdated }) {
  const [cpus, setCpus] = useState('');
  const [ramMb, setRamMb] = useState('');
  const [diskGb, setDiskGb] = useState('');
  const [description, setDescription] = useState('');
  const [tags, setTags] = useState('');
  const [natIP, setNatIP] = useState('');
  const [autoStart, setAutoStart] = useState(false);
  const [locked, setLocked] = useState(false);
  const updateMut = useMutation((patch) => vms.update(vm.id, patch));
  const spec = normalizeSpec(vm.spec);
  const currentCpus = Number.isFinite(spec.cpus) ? spec.cpus : 0;
  const currentRamMb = Number.isFinite(spec.ram_mb) ? spec.ram_mb : 0;
  const currentDiskGb = Number.isFinite(spec.disk_gb) ? spec.disk_gb : 0;

  // Current IP shown as a plain address; strip /24 suffix if present
  const currentIP = vm.ip || (spec.nat_static_ip ? spec.nat_static_ip.replace(/\/\d+$/, '') : '');

  // Initialize form fields only on open. Including `vm` in the deps would cause
  // the parent's 5s polling refresh to overwrite the user's in-progress edits
  // mid-typing, making CPU/RAM changes silently fall back to current values.
  useEffect(() => {
    if (!open || !vm) return;
    setCpus(currentCpus > 0 ? String(currentCpus) : '');
    setRamMb(currentRamMb > 0 ? String(currentRamMb) : '');
    setDiskGb(currentDiskGb > 0 ? String(currentDiskGb) : '');
    setDescription(vm.description || '');
    setTags(safeArray(vm.tags).join(', '));
    setNatIP(currentIP);
    setAutoStart(!!spec.auto_start);
    setLocked(!!spec.locked);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const handleSubmit = async () => {
    const patch = {};
    const newCpus = parseInt(cpus, 10);
    const newRam  = parseInt(ramMb, 10);
    const newDisk = parseInt(diskGb, 10);
    if (newCpus !== currentCpus)    patch.cpus    = newCpus;
    if (newRam  !== currentRamMb)   patch.ram_mb  = newRam;
    if (newDisk !== currentDiskGb)  patch.disk_gb = newDisk;
    if (description.trim() !== (vm.description || '')) patch.description = description.trim();
    const nextTags = tags.split(',').map(tag => tag.trim()).filter(Boolean);
    if (nextTags.join(',') !== safeArray(vm.tags).join(',')) patch.tags = nextTags;

    // Normalise the IP: accept bare IP and append /24 for the API
    const trimmedIP = natIP.trim();
    if (trimmedIP && trimmedIP !== currentIP) {
      patch.nat_static_ip = trimmedIP.includes('/') ? trimmedIP : `${trimmedIP}/24`;
    }

    if (autoStart !== !!spec.auto_start) {
      patch.auto_start = autoStart;
    }

    if (locked !== !!spec.locked) {
      patch.locked = locked;
    }

    if (Object.keys(patch).length === 0) { onClose(); return; }

    try {
      await updateMut.execute(patch);
      onUpdated();
      onClose();
    } catch { /* error shown via mutation */ }
  };

  return (
    <Modal open={open} onClose={onClose} title="Edit Machine">
      <div className="space-y-4">
        <p className="text-xs text-steel-500">
          The VM will be powered off, changes applied, then powered back on.
          Disk size can only increase. Changing the IP updates the DHCP
          reservation and regenerates the cloud-init config — the new address
          takes effect on restart.
        </p>

        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="label">vCPUs</label>
            <input
              data-testid="input-edit-cpus"
              className="input"
              type="number"
              min={1}
              value={cpus}
              onChange={e => setCpus(e.target.value)}
            />
            <p className="text-[10px] text-steel-600 mt-1">current: {currentCpus || '—'}</p>
          </div>
          <div>
            <label className="label">RAM (MB)</label>
            <input
              data-testid="input-edit-ram"
              className="input"
              type="number"
              min={256}
              step={256}
              value={ramMb}
              onChange={e => setRamMb(e.target.value)}
            />
            <p className="text-[10px] text-steel-600 mt-1">current: {currentRamMb || '—'}</p>
          </div>
          <div>
            <label className="label">Disk (GB)</label>
            <input
              data-testid="input-edit-disk"
              className="input"
              type="number"
              min={Math.max(currentDiskGb, 1)}
              value={diskGb}
              onChange={e => setDiskGb(e.target.value)}
            />
            <p className="text-[10px] text-steel-600 mt-1">current: {currentDiskGb || '—'} · grow only</p>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Description</label>
            <input className="input" type="text" placeholder="What this VM is for" value={description} onChange={e => setDescription(e.target.value)} />
          </div>
          <div>
            <label className="label">Tags</label>
            <input className="input font-mono" type="text" placeholder="prod,web" value={tags} onChange={e => setTags(e.target.value)} />
          </div>
        </div>

        <div>
          <label className="label">NAT IP Address</label>
          <input
            data-testid="input-edit-nat-ip"
            className="input font-mono"
            type="text"
            placeholder="192.168.100.50"
            value={natIP}
            onChange={e => setNatIP(e.target.value)}
          />
          <p className="text-[10px] text-steel-600 mt-1">
            current: {currentIP || 'not assigned'} · plain IP or CIDR (e.g. 192.168.100.50)
          </p>
        </div>

        <label className="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            data-testid="input-edit-auto-start"
            className="mt-1"
            checked={autoStart}
            onChange={(e) => setAutoStart(e.target.checked)}
          />
          <span className="text-xs">
            <span className="text-steel-200 font-medium">Auto-start at daemon boot</span>
            <span className="block text-steel-500 mt-1">
              The daemon will start this VM automatically when vmsmith starts up.
            </span>
          </span>
        </label>

        <label className="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            data-testid="input-edit-locked"
            className="mt-1"
            checked={locked}
            onChange={(e) => setLocked(e.target.checked)}
          />
          <span className="text-xs">
            <span className="text-steel-200 font-medium">Lock VM (delete-protected)</span>
            <span className="block text-steel-500 mt-1">
              When locked, the VM rejects deletion. Stop, start, and restart still work.
            </span>
          </span>
        </label>

        {updateMut.error && <p className="text-sm text-red-400">Error: {updateMut.error}</p>}

        <div className="flex justify-end gap-2">
          <button data-testid="btn-cancel-edit" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button data-testid="btn-submit-edit" className="btn-primary" onClick={handleSubmit} disabled={updateMut.loading}>
            {updateMut.loading ? <Spinner size={14} /> : <Pencil size={14} />}
            {updateMut.loading ? 'Applying…' : 'Apply Changes'}
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Snapshot List ---
function SnapshotList({ vmId, snapList, refreshSnaps }) {
  const restoreMut = useMutation((name) => snapshots.restore(vmId, name));
  const deleteMut  = useMutation((name) => snapshots.delete(vmId, name));

  if (!snapList?.length) {
    return <EmptyState icon={Camera} title="No snapshots" description="Capture the VM state at any point." />;
  }

  return (
    <div className="divide-y divide-steel-800/40">
      {snapList.map(snap => (
        <div key={snap.name} className="flex items-center justify-between px-4 py-2.5 hover:bg-steel-800/20 transition-colors" data-testid={`snap-${snap.name}`}>
          <div className="flex items-center gap-2">
            <Clock size={12} className="text-steel-600" />
            <span className="font-mono text-sm text-steel-200">{snap.name}</span>
          </div>
          <div className="flex items-center gap-1">
            <button
              className="btn-ghost text-xs text-blue-400 hover:text-blue-300"
              onClick={async () => { await restoreMut.execute(snap.name); refreshSnaps(); }}
            >
              <RotateCcw size={12} /> Restore
            </button>
            <button
              className="btn-ghost text-xs text-red-400 hover:text-red-300"
              onClick={async () => { await deleteMut.execute(snap.name); refreshSnaps(); }}
              data-testid={`btn-delete-snap-${snap.name}`}
            >
              <Trash2 size={12} />
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}

// --- Port List ---
function PortList({ vmId, portList, refreshPorts }) {
  const removeMut = useMutation((portId) => ports.remove(vmId, portId));

  if (!portList?.length) {
    return <EmptyState icon={Network} title="No port forwards" description="Expose VM services to the network." />;
  }

  return (
    <div className="divide-y divide-steel-800/40">
      {portList.map(pf => (
        <div key={pf.id} className="flex items-center justify-between px-4 py-2.5 hover:bg-steel-800/20 transition-colors">
          <div>
            <span className="font-mono text-sm text-steel-200">
              :{pf.host_port} → {pf.guest_ip}:{pf.guest_port}
            </span>
            <span className="ml-2 badge bg-steel-800/60 text-steel-500 border-steel-700/40">{pf.protocol}</span>
          </div>
          <button
            className="btn-ghost text-xs text-red-400 hover:text-red-300"
            onClick={async () => { await removeMut.execute(pf.id); refreshPorts(); }}
          >
            <Trash2 size={12} />
          </button>
        </div>
      ))}
    </div>
  );
}

// --- Create Snapshot Modal ---
function CreateSnapshotModal({ vmId, open, onClose, onCreated }) {
  const [name, setName] = useState('');
  const createMut = useMutation((n) => snapshots.create(vmId, n));

  const handleSubmit = async () => {
    await createMut.execute(name);
    onCreated();
    onClose();
    setName('');
  };

  return (
    <Modal open={open} onClose={onClose} title="Create Snapshot">
      <div className="space-y-4">
        <div>
          <label className="label">Snapshot Name</label>
          <input className="input" placeholder="before-update" value={name} onChange={e => setName(e.target.value)} autoFocus data-testid="input-snap-name" />
        </div>
        {createMut.error && <p className="text-sm text-red-400">{createMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" onClick={handleSubmit} disabled={!name || createMut.loading} data-testid="btn-submit-snapshot">
            {createMut.loading ? <Spinner size={14} /> : <Camera size={14} />} Create
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Add Port Modal ---
function AddPortModal({ vmId, open, onClose, onCreated }) {
  const [hostPort, setHostPort] = useState('');
  const [guestPort, setGuestPort] = useState('');
  const [protocol, setProtocol] = useState('tcp');
  const addMut = useMutation(() => ports.add(vmId, parseInt(hostPort), parseInt(guestPort), protocol));

  const handleSubmit = async () => {
    await addMut.execute();
    onCreated();
    onClose();
    setHostPort('');
    setGuestPort('');
  };

  return (
    <Modal open={open} onClose={onClose} title="Add Port Forward">
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Host Port</label>
            <input className="input" type="number" placeholder="2222" value={hostPort} onChange={e => setHostPort(e.target.value)} autoFocus />
          </div>
          <div>
            <label className="label">Guest Port</label>
            <input className="input" type="number" placeholder="22" value={guestPort} onChange={e => setGuestPort(e.target.value)} />
          </div>
        </div>
        <div>
          <label className="label">Protocol</label>
          <select className="input" value={protocol} onChange={e => setProtocol(e.target.value)}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </div>
        {addMut.error && <p className="text-sm text-red-400">{addMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" onClick={handleSubmit} disabled={!hostPort || !guestPort || addMut.loading}>
            {addMut.loading ? <Spinner size={14} /> : <Plus size={14} />} Add
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Export Image Modal ---
function ExportImageModal({ vmId, open, onClose }) {
  const [name, setName] = useState('');
  const createMut = useMutation((n) => imagesApi.create(vmId, n));
  const [done, setDone] = useState(false);

  const handleSubmit = async () => {
    await createMut.execute(name);
    setDone(true);
  };

  const handleClose = () => {
    onClose();
    setName('');
    setDone(false);
  };

  return (
    <Modal open={open} onClose={handleClose} title="Export as Image">
      {done ? (
        <div className="text-center py-4">
          <p className="text-sm text-emerald-300 mb-3">Image created successfully.</p>
          <button className="btn-primary" onClick={handleClose}>Done</button>
        </div>
      ) : (
        <div className="space-y-4">
          <div>
            <label className="label">Image Name</label>
            <input className="input" placeholder="my-golden-image" value={name} onChange={e => setName(e.target.value)} autoFocus />
          </div>
          <p className="text-xs text-steel-500">This flattens the VM disk into a standalone portable qcow2 image. May take several minutes for large disks.</p>
          {createMut.error && <p className="text-sm text-red-400">{createMut.error}</p>}
          <div className="flex justify-end gap-2">
            <button className="btn-secondary" onClick={handleClose}>Cancel</button>
            <button className="btn-primary" onClick={handleSubmit} disabled={!name || createMut.loading}>
              {createMut.loading ? <Spinner size={14} /> : <Download size={14} />} Export
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}

// --- VM Metrics ---
function VMMetrics({ vmId }) {
  const { snapshot: snap, history, current: cur, status, error } = useVMStats(vmId);

  if (status === STATS_STATE_LOADING && !snap) return <div className="flex justify-center py-10"><Spinner size={20} /></div>;
  if (status === STATS_STATE_ERROR && error) return <ErrorBanner message={error} />;
  if (!snap) return null;

  // Compute 5-minute averages from the (rolling) history. This grows live as
  // new samples arrive over the SSE stream, so the average is always anchored
  // to the most recent window.
  const fiveMinAgo = Date.now() - 5 * 60 * 1000;
  const recent = history.filter((s) => {
    const ts = s?.timestamp ? Date.parse(s.timestamp) : NaN;
    return Number.isFinite(ts) && ts >= fiveMinAgo;
  });

  const avgFloat = (field) => {
    let sum = 0, n = 0;
    for (const s of recent) {
      const v = s?.[field];
      if (typeof v === 'number') { sum += v; n += 1; }
    }
    return n === 0 ? null : sum / n;
  };
  const avgInt = avgFloat;

  const lastSampled = snap.last_sampled_at ? new Date(snap.last_sampled_at).toLocaleString() : '—';

  return (
    <div className="space-y-4" data-testid="vm-metrics-content">
      <div className="grid grid-cols-3 gap-3">
        <InfoCard label="State" value={snap.state || '—'} testId="metrics-state" />
        <InfoCard label="Last Sampled" value={lastSampled} testId="metrics-last-sampled" />
        <InfoCard label="History" value={`${history.length}/${snap.history_size || 0} samples · ${snap.interval_seconds || 0}s interval`} testId="metrics-history-meta" />
      </div>

      {!cur ? (
        <EmptyState
          title="No metrics yet"
          description={
            snap.state === 'running'
              ? 'No samples collected yet. The first sample appears within one interval.'
              : 'VM is not running — metrics resume after start.'
          }
        />
      ) : (
        <>
          <div className="card overflow-hidden">
            <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
              <h2 className="text-sm font-display font-semibold text-steel-300">Resource Utilization</h2>
              <div className="flex items-center gap-3">
                <LiveIndicator status={status} />
                <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500" data-testid="metrics-live-status">{status}</span>
              </div>
            </div>
            <table className="w-full text-sm" data-testid="metrics-table">
              <thead className="text-left text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500 border-b border-steel-800/40">
                <tr>
                  <th className="px-4 py-2">Metric</th>
                  <th className="px-4 py-2">Current</th>
                  <th className="px-4 py-2">5-min avg</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-steel-800/40">
                <MetricRow label="CPU %" testId="metric-cpu" cur={fmtPercent(cur.cpu_percent)} avg={fmtPercent(avgFloat('cpu_percent'))} />
                <MetricRow label="Memory used" testId="metric-mem-used" cur={fmtMB(cur.mem_used_mb)} avg={fmtMB(avgInt('mem_used_mb'))} />
                <MetricRow label="Memory available" testId="metric-mem-avail" cur={fmtMB(cur.mem_avail_mb)} avg={fmtMB(avgInt('mem_avail_mb'))} />
                <MetricRow label="Disk read" testId="metric-disk-read" cur={fmtBps(cur.disk_read_bps)} avg={fmtBps(avgInt('disk_read_bps'))} />
                <MetricRow label="Disk write" testId="metric-disk-write" cur={fmtBps(cur.disk_write_bps)} avg={fmtBps(avgInt('disk_write_bps'))} />
                <MetricRow label="Network RX" testId="metric-net-rx" cur={fmtBps(cur.net_rx_bps)} avg={fmtBps(avgInt('net_rx_bps'))} />
                <MetricRow label="Network TX" testId="metric-net-tx" cur={fmtBps(cur.net_tx_bps)} avg={fmtBps(avgInt('net_tx_bps'))} />
              </tbody>
            </table>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
            <MetricChart
              title="CPU"
              series={[{ label: 'CPU %', color: '#38bdf8', format: 'percent' }]}
              data={buildChartData(history, ['cpu_percent'])}
            />
            <MetricChart
              title="Memory"
              series={[{ label: 'Used MB', color: '#a78bfa', format: 'mb' }]}
              data={buildChartData(history, ['mem_used_mb'])}
            />
            <MetricChart
              title="Disk I/O"
              series={[
                { label: 'Read', color: '#34d399', format: 'bps' },
                { label: 'Write', color: '#f59e0b', format: 'bps' },
              ]}
              data={buildChartData(history, ['disk_read_bps', 'disk_write_bps'])}
            />
            <MetricChart
              title="Network"
              series={[
                { label: 'RX', color: '#60a5fa', format: 'bps' },
                { label: 'TX', color: '#f472b6', format: 'bps' },
              ]}
              data={buildChartData(history, ['net_rx_bps', 'net_tx_bps'])}
            />
          </div>
        </>
      )}
    </div>
  );
}


function MetricRow({ label, cur, avg, testId }) {
  return (
    <tr data-testid={testId}>
      <td className="px-4 py-2 text-steel-300">{label}</td>
      <td className="px-4 py-2 font-mono text-steel-100" data-testid={`${testId}-current`}>{cur}</td>
      <td className="px-4 py-2 font-mono text-steel-400" data-testid={`${testId}-avg`}>{avg}</td>
    </tr>
  );
}

function fmtPercent(v) {
  return typeof v === 'number' ? `${v.toFixed(1)}%` : 'n/a';
}

function fmtMB(v) {
  return typeof v === 'number' ? `${Math.round(v).toLocaleString()} MB` : 'n/a';
}

function fmtBps(v) {
  if (typeof v !== 'number') return 'n/a';
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB/s`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MB/s`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(1)} KB/s`;
  return `${Math.round(v)} B/s`;
}
