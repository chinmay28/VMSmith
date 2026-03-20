import { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  ArrowLeft, Play, Square, Trash2, Camera, Network,
  Plus, RotateCcw, Download, Clock
} from 'lucide-react';
import { vms, snapshots, ports, images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { StatusBadge, Modal, Spinner, ErrorBanner, EmptyState } from '../components/Shared';

export default function VMDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { data: vm, loading, error, refresh } = useFetch(() => vms.get(id), [id], 5000);
  const { data: snapList, refresh: refreshSnaps } = useFetch(() => snapshots.list(id), [id], 10000);
  const { data: portList, refresh: refreshPorts } = useFetch(() => ports.list(id), [id], 10000);

  const [showSnapModal, setShowSnapModal] = useState(false);
  const [showPortModal, setShowPortModal] = useState(false);
  const [showImageModal, setShowImageModal] = useState(false);

  const startMut  = useMutation(vms.start);
  const stopMut   = useMutation(vms.stop);
  const deleteMut = useMutation(vms.delete);

  if (loading && !vm) return <div className="flex justify-center py-20"><Spinner size={20} /></div>;
  if (error) return <ErrorBanner message={error} />;
  if (!vm) return null;

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
          <button className="btn-ghost -ml-2" onClick={() => navigate('/vms')}>
            <ArrowLeft size={16} />
          </button>
          <div>
            <div className="flex items-center gap-2.5">
              <h1 className="font-display font-bold text-2xl text-steel-100 tracking-tight">{vm.name}</h1>
              <StatusBadge state={vm.state} />
            </div>
            <p className="text-xs font-mono text-steel-500 mt-0.5">{vm.id}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {vm.state === 'stopped' && (
            <button className="btn-primary" onClick={() => { startMut.execute(id).then(refresh); }}>
              <Play size={14} /> Start
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { stopMut.execute(id).then(refresh); }}>
              <Square size={14} /> Stop
            </button>
          )}
          <button className="btn-danger" onClick={handleDelete}>
            <Trash2 size={14} /> Delete
          </button>
        </div>
      </div>

      {/* Info grid */}
      <div className="grid grid-cols-2 gap-3 mb-6">
        <InfoCard label="IP Address" value={vm.ip || 'Not assigned'} mono />
        <InfoCard label="Image" value={vm.spec.image} mono />
        <InfoCard label="Resources" value={`${vm.spec.cpus} vCPU · ${vm.spec.ram_mb} MB RAM · ${vm.spec.disk_gb} GB disk`} />
        <InfoCard label="Created" value={new Date(vm.created_at).toLocaleString()} />
        {vm.spec.default_user && (
          <InfoCard
            label="SSH"
            value={vm.ip ? `ssh ${vm.spec.default_user}@${vm.ip}` : `user: ${vm.spec.default_user}`}
            mono
          />
        )}
      </div>

      {/* Attached Networks */}
      {vm.spec?.networks?.length > 0 && (
        <div className="card mb-4">
          <div className="flex items-center gap-2 px-4 py-3 border-b border-steel-800/40">
            <Network size={14} className="text-steel-500" />
            <h2 className="text-sm font-display font-semibold text-steel-300">Extra Networks</h2>
          </div>
          <div className="divide-y divide-steel-800/40">
            {vm.spec.networks.map((net, i) => (
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
            <button className="btn-ghost text-xs" onClick={() => setShowSnapModal(true)}>
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

      {/* Modals */}
      <CreateSnapshotModal vmId={id} open={showSnapModal} onClose={() => setShowSnapModal(false)} onCreated={refreshSnaps} />
      <AddPortModal vmId={id} open={showPortModal} onClose={() => setShowPortModal(false)} onCreated={refreshPorts} />
      <ExportImageModal vmId={id} open={showImageModal} onClose={() => setShowImageModal(false)} />
    </div>
  );
}

function InfoCard({ label, value, mono }) {
  return (
    <div className="card px-4 py-3">
      <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
      <p className={`text-sm text-steel-200 mt-0.5 ${mono ? 'font-mono' : ''}`}>{value}</p>
    </div>
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
        <div key={snap.name} className="flex items-center justify-between px-4 py-2.5 hover:bg-steel-800/20 transition-colors">
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
          <input className="input" placeholder="before-update" value={name} onChange={e => setName(e.target.value)} autoFocus />
        </div>
        {createMut.error && <p className="text-sm text-red-400">{createMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" onClick={handleSubmit} disabled={!name || createMut.loading}>
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
