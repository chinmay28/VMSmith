import React, { useState, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  ArrowLeft, Play, Square, Trash2, Camera, Network,
  Plus, RotateCcw, RefreshCw, Download, Clock, Pencil, Copy, Zap, Pause, Search, X
} from 'lucide-react';
import { vms, snapshots, ports, images as imagesApi, schedules as schedulesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { useVMStats, STATS_STATE_LOADING, STATS_STATE_ERROR } from '../hooks/useVMStats';
import { useOperationProgress, ProgressReadout } from '../hooks/useOperationProgress.jsx';
import { buildChartData } from '../hooks/vmStatsHelpers.js';
import { StatusBadge, Modal, Spinner, ErrorBanner, EmptyState, LiveIndicator, PaginationControls, ProgressBar, OperationProgress } from '../components/Shared';
import MetricChart from '../components/MetricChart';
import { normalizeSpec, safeArray } from '../utils/normalize';
import Activity from './Activity';

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

export default function VMDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { data: vm, loading, error, refresh } = useFetch(() => vms.get(id), [id], 5000);
  const [snapSort, setSnapSort] = useState('id');
  const [snapOrder, setSnapOrder] = useState('asc');
  const [snapSearchInput, setSnapSearchInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_search') || '';
  });
  const [snapSearch, setSnapSearch] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_search') || '';
  });
  const [snapSince, setSnapSince] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_since') || '';
  });
  const [snapUntil, setSnapUntil] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_until') || '';
  });
  const [snapPrefixInput, setSnapPrefixInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_prefix') || '';
  });
  const [snapPrefix, setSnapPrefix] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('snap_prefix') || '';
  });
  const { data: snapList, refresh: refreshSnaps } = useFetch(
    () => snapshots.list(id, { sort: snapSort, order: snapOrder, search: snapSearch, prefix: snapPrefix, since: snapSince, until: snapUntil }),
    [id, snapSort, snapOrder, snapSearch, snapPrefix, snapSince, snapUntil],
    10000,
  );

  // Debounce the snapshot search box. `snapSearchInput` is the live value the
  // user types; `snapSearch` is the committed query that drives the API call.
  useEffect(() => {
    const trimmed = snapSearchInput.trim();
    const t = setTimeout(() => setSnapSearch(trimmed), 250);
    return () => clearTimeout(t);
  }, [snapSearchInput]);

  // Same debounce shape for the snapshot prefix input. Case-sensitive
  // HasPrefix on the API side, so we preserve input case but trim.
  useEffect(() => {
    const trimmed = snapPrefixInput.trim();
    const t = setTimeout(() => setSnapPrefix(trimmed), 250);
    return () => clearTimeout(t);
  }, [snapPrefixInput]);
  const [portSort, setPortSort] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_sort') || 'id';
  });
  const [portOrder, setPortOrder] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_order') || 'asc';
  });
  const [portSearchInput, setPortSearchInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_search') || '';
  });
  const [portSearch, setPortSearch] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_search') || '';
  });
  const [portProtocol, setPortProtocol] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_protocol') || '';
  });
  const [portMinHostPortInput, setPortMinHostPortInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_min_host') || '';
  });
  const [portMinHostPort, setPortMinHostPort] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_min_host') || '';
  });
  const [portMaxHostPortInput, setPortMaxHostPortInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_max_host') || '';
  });
  const [portMaxHostPort, setPortMaxHostPort] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_max_host') || '';
  });
  const [portMinGuestPortInput, setPortMinGuestPortInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_min_guest') || '';
  });
  const [portMinGuestPort, setPortMinGuestPort] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_min_guest') || '';
  });
  const [portMaxGuestPortInput, setPortMaxGuestPortInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_max_guest') || '';
  });
  const [portMaxGuestPort, setPortMaxGuestPort] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_max_guest') || '';
  });
  const [portGuestIPInput, setPortGuestIPInput] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_guest_ip') || '';
  });
  const [portGuestIP, setPortGuestIP] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    return sp.get('port_guest_ip') || '';
  });
  const [portPage, setPortPage] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    const raw = Number.parseInt(sp.get('port_page') || '', 10);
    return Number.isFinite(raw) && raw > 0 ? raw : 1;
  });
  const [portPerPage, setPortPerPage] = useState(() => {
    const sp = new URLSearchParams(window.location.search);
    const raw = Number.parseInt(sp.get('port_per_page') || '', 10);
    return Number.isFinite(raw) && raw > 0 ? raw : 25;
  });
  const { data: portResponse, refresh: refreshPorts } = useFetch(
    () => ports.list(id, { sort: portSort, order: portOrder, search: portSearch, protocol: portProtocol, minHostPort: portMinHostPort, maxHostPort: portMaxHostPort, minGuestPort: portMinGuestPort, maxGuestPort: portMaxGuestPort, guestIp: portGuestIP, page: portPage, perPage: portPerPage }),
    [id, portSort, portOrder, portSearch, portProtocol, portMinHostPort, portMaxHostPort, portMinGuestPort, portMaxGuestPort, portGuestIP, portPage, portPerPage],
    10000,
  );
  const portList = portResponse?.data || [];
  const portTotal = portResponse?.meta?.totalCount ?? portList.length;
  // Whenever filter / sort / search changes, snap back to page 1 so the user
  // never lands beyond the post-filter page count.
  useEffect(() => { setPortPage(1); }, [portSort, portOrder, portSearch, portProtocol, portMinHostPort, portMaxHostPort, portMinGuestPort, portMaxGuestPort, portGuestIP, portPerPage]);

  // Debounce the port-forward search box. `portSearchInput` is what the user
  // types; `portSearch` is the committed query that drives the API call.
  useEffect(() => {
    const trimmed = portSearchInput.trim();
    const t = setTimeout(() => setPortSearch(trimmed), 250);
    return () => clearTimeout(t);
  }, [portSearchInput]);

  // Debounce the host-port range inputs the same way as the search box so the
  // committed values that drive the API call settle after the operator stops
  // typing.
  useEffect(() => {
    const trimmed = portMinHostPortInput.trim();
    const t = setTimeout(() => setPortMinHostPort(trimmed), 250);
    return () => clearTimeout(t);
  }, [portMinHostPortInput]);
  useEffect(() => {
    const trimmed = portMaxHostPortInput.trim();
    const t = setTimeout(() => setPortMaxHostPort(trimmed), 250);
    return () => clearTimeout(t);
  }, [portMaxHostPortInput]);
  useEffect(() => {
    const trimmed = portMinGuestPortInput.trim();
    const t = setTimeout(() => setPortMinGuestPort(trimmed), 250);
    return () => clearTimeout(t);
  }, [portMinGuestPortInput]);
  useEffect(() => {
    const trimmed = portMaxGuestPortInput.trim();
    const t = setTimeout(() => setPortMaxGuestPort(trimmed), 250);
    return () => clearTimeout(t);
  }, [portMaxGuestPortInput]);
  useEffect(() => {
    const trimmed = portGuestIPInput.trim();
    const t = setTimeout(() => setPortGuestIP(trimmed), 250);
    return () => clearTimeout(t);
  }, [portGuestIPInput]);

  useEffect(() => {
    const sp = new URLSearchParams(window.location.search);
    if (portSort !== 'id') sp.set('port_sort', portSort); else sp.delete('port_sort');
    if (portOrder !== 'asc') sp.set('port_order', portOrder); else sp.delete('port_order');
    if (portSearch) sp.set('port_search', portSearch); else sp.delete('port_search');
    if (portProtocol) sp.set('port_protocol', portProtocol); else sp.delete('port_protocol');
    if (portMinHostPort) sp.set('port_min_host', portMinHostPort); else sp.delete('port_min_host');
    if (portMaxHostPort) sp.set('port_max_host', portMaxHostPort); else sp.delete('port_max_host');
    if (portMinGuestPort) sp.set('port_min_guest', portMinGuestPort); else sp.delete('port_min_guest');
    if (portMaxGuestPort) sp.set('port_max_guest', portMaxGuestPort); else sp.delete('port_max_guest');
    if (portGuestIP) sp.set('port_guest_ip', portGuestIP); else sp.delete('port_guest_ip');
    if (portPage > 1) sp.set('port_page', String(portPage)); else sp.delete('port_page');
    if (portPerPage !== 25) sp.set('port_per_page', String(portPerPage)); else sp.delete('port_per_page');
    if (snapSearch) sp.set('snap_search', snapSearch); else sp.delete('snap_search');
    if (snapPrefix) sp.set('snap_prefix', snapPrefix); else sp.delete('snap_prefix');
    if (snapSince) sp.set('snap_since', snapSince); else sp.delete('snap_since');
    if (snapUntil) sp.set('snap_until', snapUntil); else sp.delete('snap_until');
    const qs = sp.toString();
    const next = window.location.pathname + (qs ? `?${qs}` : '');
    window.history.replaceState(null, '', next);
  }, [portSort, portOrder, portSearch, portProtocol, portMinHostPort, portMaxHostPort, portMinGuestPort, portMaxGuestPort, portGuestIP, portPage, portPerPage, snapSearch, snapPrefix, snapSince, snapUntil]);

  const [showSnapModal, setShowSnapModal] = useState(false);
  const [showPortModal, setShowPortModal] = useState(false);
  const [showImageModal, setShowImageModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [showCloneModal, setShowCloneModal] = useState(false);
  const [activeTab, setActiveTab] = useState('overview');

  const { data: scheduleResponse } = useFetch(
    () => schedulesApi.list({ perPage: 100, sort: 'next_fire_at', order: 'asc' }),
    [],
    15000,
  );

  const startMut     = useMutation(vms.start);
  const stopMut      = useMutation(vms.stop);
  const forceStopMut = useMutation(vms.forceStop);
  const restartMut   = useMutation(vms.restart);
  const rebootMut    = useMutation(vms.reboot);
  const suspendMut   = useMutation(vms.suspend);
  const resumeMut    = useMutation(vms.resume);
  const deleteMut    = useMutation(vms.delete);

  // Readiness progress: the start / restart POST returns as soon as the domain
  // boots, but the guest is not usable until it is network-reachable. The
  // daemon streams "boot" progress frames over the operation-progress SSE
  // channel; subscribe here so the operator sees a bar until the VM is pingable
  // instead of refreshing the page by hand.
  const bootProgress = useOperationProgress(id, 'boot');
  const [booting, setBooting] = useState(false);

  // When a readiness wait finishes, refresh the VM so the discovered IP shows
  // up, then clear the bar.
  useEffect(() => {
    if (booting && bootProgress.done) {
      refresh();
      setBooting(false);
      bootProgress.reset();
    }
  }, [booting, bootProgress.done]);

  // runWithReadiness fires a lifecycle mutation, then begins streaming readiness
  // progress so the bar persists past the (fast) POST until the guest is up.
  const runWithReadiness = async (mut) => {
    bootProgress.start();
    setBooting(true);
    try {
      await mut.execute(id);
      refresh();
    } catch {
      // The mutation surfaced its own error; stop the readiness bar.
      setBooting(false);
      bootProgress.reset();
    }
  };

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
  const osType = resolveOsType(spec);
  const sshUser = spec.default_user || (osType === 'windows' ? 'Administrator' : 'root');
  const rdpForward = portList.find((port) => Number(port.guest_port) === 3389 && String(port.protocol || '').toLowerCase() === 'tcp')
    || portList.find((port) => Number(port.guest_port) === 3389);
  const hasRDPForward = !!rdpForward;
  const vmTagSet = new Set(tags.map((tag) => String(tag).toLowerCase()));
  const vmScheduleList = (scheduleResponse?.data || []).filter((schedule) => {
    if (schedule.vm_id && schedule.vm_id === id) return true;
    if (schedule.vm_id) return false;
    const selector = Array.isArray(schedule.tag_selector)
      ? schedule.tag_selector.map((tag) => String(tag).toLowerCase())
      : [];
    if (selector.length === 0) return true;
    return selector.some((tag) => vmTagSet.has(tag));
  });

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
            <div className="flex items-center gap-2.5 flex-wrap">
              <h1 className="font-display font-bold text-2xl text-steel-100 tracking-tight" data-testid="vm-detail-name">{vm.name}</h1>
              <span data-testid="vm-detail-state"><StatusBadge state={vm.state} /></span>
              <span
                className={`badge ${osType === 'windows' ? 'bg-sky-500/10 text-sky-300 border-sky-500/20' : 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20'}`}
                data-testid="vm-detail-os-badge"
              >
                {osType === 'windows' ? (spec.os_variant ? titleCaseWords(String(spec.os_variant).replace(/^windows-/, 'Windows ').replace(/-/g, ' ')) : 'Windows') : 'Linux'}
              </span>
            </div>
            <p className="text-xs font-mono text-steel-500 mt-0.5">{vm.id}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {vm.state === 'stopped' && (
            <button className="btn-primary" onClick={() => { runWithReadiness(startMut); }} data-testid="btn-start">
              <Play size={14} /> Start
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { stopMut.execute(id).then(refresh); }} data-testid="btn-stop">
              <Square size={14} /> Stop
            </button>
          )}
          {vm.state === 'running' && (
            <button
              className="btn-secondary"
              onClick={() => {
                if (!window.confirm(`Force-stop ${vm.name}? This skips graceful shutdown and may cause data loss.`)) return;
                forceStopMut.execute(id).then(refresh);
              }}
              data-testid="btn-force-stop"
              title="Immediate destroy — skips ACPI shutdown"
            >
              <Zap size={14} /> Force Stop
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { runWithReadiness(restartMut); }} data-testid="btn-restart" title="Graceful stop and start">
              <RotateCcw size={14} /> Restart
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { rebootMut.execute(id).then(refresh); }} data-testid="btn-reboot" title="Reboot the guest OS in-place (preserves IP/MAC, no power cycle)">
              <RefreshCw size={14} /> Reboot
            </button>
          )}
          {vm.state === 'running' && (
            <button className="btn-secondary" onClick={() => { suspendMut.execute(id).then(refresh); }} data-testid="btn-suspend" title="Pause CPU and memory; resume later without rebooting">
              <Pause size={14} /> Suspend
            </button>
          )}
          {vm.state === 'paused' && (
            <button className="btn-primary" onClick={() => { resumeMut.execute(id).then(refresh); }} data-testid="btn-resume" title="Unpause and continue running">
              <Play size={14} /> Resume
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

      {(startMut.loading || stopMut.loading || forceStopMut.loading || restartMut.loading || rebootMut.loading || suspendMut.loading || resumeMut.loading || deleteMut.loading) && (
        <div className="mb-4">
          <OperationProgress
            active
            label={
              startMut.loading ? 'Starting machine…'
                : stopMut.loading ? 'Stopping machine…'
                : forceStopMut.loading ? 'Force-stopping machine…'
                : restartMut.loading ? 'Restarting machine…'
                : rebootMut.loading ? 'Rebooting guest OS…'
                : suspendMut.loading ? 'Suspending machine…'
                : resumeMut.loading ? 'Resuming machine…'
                : 'Deleting machine…'
            }
            testId="vm-lifecycle-progress"
          />
        </div>
      )}

      {booting && !startMut.loading && !restartMut.loading && (
        <div className="mb-4">
          <ProgressReadout
            active
            percent={bootProgress.percent}
            label="Waiting for the machine to become reachable…"
            testId="vm-readiness-progress"
          />
        </div>
      )}

      {/* Tabs */}
      <div className="flex items-center gap-1 mb-4 border-b border-steel-800/60">
        <TabButton active={activeTab === 'overview'} onClick={() => setActiveTab('overview')} testId="tab-overview">
          Overview
        </TabButton>
        <TabButton active={activeTab === 'snapshots'} onClick={() => setActiveTab('snapshots')} testId="tab-snapshots">
          Snapshots
        </TabButton>
        <TabButton active={activeTab === 'ports'} onClick={() => setActiveTab('ports')} testId="tab-ports">
          Port Forwards
        </TabButton>
        <TabButton active={activeTab === 'schedules'} onClick={() => setActiveTab('schedules')} testId="tab-schedules">
          Schedules
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
      {activeTab === 'overview' && (
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
          label={osType === 'windows' ? 'Access' : 'SSH'}
          value={vm.ip ? `${osType === 'windows' ? `RDP ${vm.ip} · user: ${sshUser}` : `ssh ${sshUser}@${vm.ip}`}` : `user: ${sshUser}`}
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

      {osType === 'windows' && hasRDPForward && (
        <div className="card mb-4 border-sky-500/20" data-testid="vm-detail-rdp-hint">
          <div className="px-4 py-3 text-sm text-sky-200">
            Connect via RDP: <span className="font-mono">localhost:{rdpForward.host_port}</span>
          </div>
        </div>
      )}

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

      </>
      )}
      {activeTab === 'schedules' && (
      <div className="card mb-4" data-testid="vm-detail-schedules">
        <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
          <div className="flex items-center gap-2">
            <Clock size={14} className="text-steel-500" />
            <h2 className="text-sm font-display font-semibold text-steel-300">Schedules</h2>
          </div>
          <button
            className="btn-ghost text-xs"
            onClick={() => navigate(`/schedules?open=create&prefill_vm_id=${encodeURIComponent(id)}&prefill_name=${encodeURIComponent(`${vm?.name || id}-schedule`)}`)}
            data-testid="btn-add-schedule-from-vm"
          >
            <Plus size={13} /> Add schedule
          </button>
        </div>
        {vmScheduleList.length === 0 ? (
          <div className="px-4 py-5 text-sm text-steel-500" data-testid="vm-detail-schedules-empty">
            No schedules target this VM yet.
          </div>
        ) : (
          <div className="divide-y divide-steel-800/30">
            {vmScheduleList.map((schedule) => (
              <div key={schedule.id} className="px-4 py-3 flex items-start justify-between gap-4" data-testid={`vm-detail-schedule-${schedule.id}`}>
                <div>
                  <div className="flex items-center gap-2">
                    <span className="text-sm text-steel-200">{schedule.name}</span>
                    <span className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-steel-800/60 text-steel-300 border border-steel-700/30">{schedule.action}</span>
                    {!schedule.enabled && <span className="text-[10px] font-mono text-amber-300">disabled</span>}
                  </div>
                  <div className="mt-1 text-[11px] font-mono text-steel-500">
                    {schedule.vm_id === id
                      ? 'Direct VM schedule'
                      : Array.isArray(schedule.tag_selector) && schedule.tag_selector.length > 0
                        ? `Matches tags: ${schedule.tag_selector.join(', ')}`
                        : 'All VMs'}
                  </div>
                </div>
                <div className="text-right text-[11px] font-mono text-steel-400">
                  <div>{schedule.cron_spec}</div>
                  <div data-testid={`vm-detail-schedule-next-fire-${schedule.id}`}>Next: {schedule.next_fire_at ? new Date(schedule.next_fire_at).toLocaleString() : '—'}</div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      )}

      {activeTab === 'snapshots' && (
        <div className="card">
          <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
            <div className="flex items-center gap-2">
              <Camera size={14} className="text-steel-500" />
              <h2 className="text-sm font-display font-semibold text-steel-300">Snapshots</h2>
            </div>
            <div className="flex items-center gap-2">
              <select
                value={snapSort}
                onChange={(e) => setSnapSort(e.target.value)}
                className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-xs text-steel-200"
                data-testid="snap-sort-field"
                aria-label="Sort snapshots by"
              >
                <option value="id">ID</option>
                <option value="name">Name</option>
                <option value="created_at">Created</option>
              </select>
              <select
                value={snapOrder}
                onChange={(e) => setSnapOrder(e.target.value)}
                className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-xs text-steel-200"
                data-testid="snap-sort-order"
                aria-label="Sort order"
              >
                <option value="asc">Asc</option>
                <option value="desc">Desc</option>
              </select>
              <button className="btn-ghost text-xs" onClick={() => setShowSnapModal(true)} data-testid="btn-new-snapshot">
                <Plus size={13} /> New
              </button>
            </div>
          </div>
          <div className="px-4 pt-3">
            <div className="relative">
              <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
              <input
                type="search"
                value={snapSearchInput}
                onChange={(e) => setSnapSearchInput(e.target.value)}
                placeholder="Search by name or description…"
                className="input w-full pl-8 pr-7 py-1.5 text-xs"
                data-testid="snap-list-search"
                aria-label="Search snapshots"
              />
              {snapSearchInput && (
                <button
                  type="button"
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
                  onClick={() => setSnapSearchInput('')}
                  data-testid="snap-list-search-clear"
                  aria-label="Clear snapshot search"
                >
                  <X size={12} />
                </button>
              )}
            </div>
            <div className="relative mt-2">
              <input
                type="search"
                value={snapPrefixInput}
                onChange={(e) => setSnapPrefixInput(e.target.value)}
                placeholder="Filter by name prefix (e.g. auto-nightly-)…"
                className="input w-full pr-7 py-1.5 text-xs"
                data-testid="snap-list-prefix"
                aria-label="Filter snapshots by name prefix"
              />
              {snapPrefixInput && (
                <button
                  type="button"
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
                  onClick={() => setSnapPrefixInput('')}
                  data-testid="snap-list-prefix-clear"
                  aria-label="Clear snapshot prefix filter"
                >
                  <X size={12} />
                </button>
              )}
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-steel-400">
              <label className="flex items-center gap-1">
                <span>Since</span>
                <input
                  type="datetime-local"
                  value={snapSince}
                  onChange={(e) => setSnapSince(e.target.value ? `${e.target.value}:00Z` : '')}
                  data-testid="snap-list-since"
                  aria-label="Snapshots created on or after"
                  className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200"
                />
              </label>
              <label className="flex items-center gap-1">
                <span>Until</span>
                <input
                  type="datetime-local"
                  value={snapUntil}
                  onChange={(e) => setSnapUntil(e.target.value ? `${e.target.value}:00Z` : '')}
                  data-testid="snap-list-until"
                  aria-label="Snapshots created on or before"
                  className="bg-steel-900/60 border border-steel-700/60 rounded px-1 py-1 text-steel-200"
                />
              </label>
              {(snapSince || snapUntil) && (
                <button
                  type="button"
                  className="text-steel-500 hover:text-steel-200"
                  onClick={() => { setSnapSince(''); setSnapUntil(''); }}
                  data-testid="snap-list-time-range-clear"
                  aria-label="Clear snapshot time range"
                >
                  Clear range
                </button>
              )}
            </div>
          </div>
          <SnapshotList vmId={id} snapList={snapList} refreshSnaps={refreshSnaps} snapSearch={snapSearch} />
        </div>
      )}

      {activeTab === 'ports' && (
        <div className="card">
          <div className="flex items-center justify-between px-4 py-3 border-b border-steel-800/40">
            <div className="flex items-center gap-2">
              <Network size={14} className="text-steel-500" />
              <h2 className="text-sm font-display font-semibold text-steel-300">Port Forwards</h2>
            </div>
            <div className="flex items-center gap-2">
              <select
                className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-xs text-steel-200"
                value={portSort}
                onChange={(e) => setPortSort(e.target.value)}
                data-testid="port-sort-field"
                aria-label="Sort port forwards by"
              >
                <option value="id">ID</option>
                <option value="host_port">Host port</option>
                <option value="guest_port">Guest port</option>
                <option value="protocol">Protocol</option>
                <option value="description">Description</option>
                <option value="guest_ip">Guest IP</option>
              </select>
              <select
                className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-xs text-steel-200"
                value={portOrder}
                onChange={(e) => setPortOrder(e.target.value)}
                data-testid="port-sort-order"
                aria-label="Sort port forwards order"
              >
                <option value="asc">Asc</option>
                <option value="desc">Desc</option>
              </select>
              <select
                className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-xs text-steel-200"
                value={portProtocol}
                onChange={(e) => setPortProtocol(e.target.value)}
                data-testid="port-protocol-filter"
                aria-label="Filter port forwards by protocol"
              >
                <option value="">Any protocol</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              <button className="btn-ghost text-xs" onClick={() => setShowPortModal(true)} data-testid="btn-new-port">
                <Plus size={13} /> Add
              </button>
            </div>
          </div>
          <div className="px-4 pt-3">
            <div className="relative">
              <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
              <input
                type="search"
                value={portSearchInput}
                onChange={(e) => setPortSearchInput(e.target.value)}
                placeholder="Search by description, protocol, or port…"
                className="input w-full pl-8 pr-7 py-1.5 text-xs"
                data-testid="port-list-search"
                aria-label="Search port forwards"
              />
              {portSearchInput && (
                <button
                  type="button"
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
                  onClick={() => setPortSearchInput('')}
                  data-testid="port-list-search-clear"
                  aria-label="Clear port forward search"
                >
                  <X size={12} />
                </button>
              )}
            </div>
            <div className="mt-2 flex items-center gap-2">
              <input
                type="number"
                min="0"
                value={portMinHostPortInput}
                onChange={(e) => setPortMinHostPortInput(e.target.value)}
                placeholder="Min host port"
                className="input w-36 py-1.5 text-xs"
                data-testid="port-min-host-port"
                aria-label="Minimum host port"
              />
              <span className="text-xs text-steel-500">–</span>
              <input
                type="number"
                min="0"
                value={portMaxHostPortInput}
                onChange={(e) => setPortMaxHostPortInput(e.target.value)}
                placeholder="Max host port"
                className="input w-36 py-1.5 text-xs"
                data-testid="port-max-host-port"
                aria-label="Maximum host port"
              />
              {(portMinHostPortInput || portMaxHostPortInput) && (
                <button
                  type="button"
                  className="btn-ghost text-xs"
                  onClick={() => { setPortMinHostPortInput(''); setPortMaxHostPortInput(''); }}
                  data-testid="port-host-port-clear"
                  aria-label="Clear host port range"
                >
                  <X size={12} /> Clear ports
                </button>
              )}
            </div>
            <div className="mt-2 flex items-center gap-2">
              <input
                type="number"
                min="0"
                value={portMinGuestPortInput}
                onChange={(e) => setPortMinGuestPortInput(e.target.value)}
                placeholder="Min guest port"
                className="input w-36 py-1.5 text-xs"
                data-testid="port-min-guest-port"
                aria-label="Minimum guest port"
              />
              <span className="text-xs text-steel-500">–</span>
              <input
                type="number"
                min="0"
                value={portMaxGuestPortInput}
                onChange={(e) => setPortMaxGuestPortInput(e.target.value)}
                placeholder="Max guest port"
                className="input w-36 py-1.5 text-xs"
                data-testid="port-max-guest-port"
                aria-label="Maximum guest port"
              />
              {(portMinGuestPortInput || portMaxGuestPortInput) && (
                <button
                  type="button"
                  className="btn-ghost text-xs"
                  onClick={() => { setPortMinGuestPortInput(''); setPortMaxGuestPortInput(''); }}
                  data-testid="port-guest-port-clear"
                  aria-label="Clear guest port range"
                >
                  <X size={12} /> Clear guest ports
                </button>
              )}
            </div>
            <div className="mt-2 flex items-center gap-2">
              <input
                type="text"
                value={portGuestIPInput}
                onChange={(e) => setPortGuestIPInput(e.target.value)}
                placeholder="Filter by guest IP"
                className="input w-72 py-1.5 text-xs"
                data-testid="port-guest-ip-filter"
                aria-label="Filter port forwards by guest IP"
              />
              {portGuestIPInput && (
                <button
                  type="button"
                  className="btn-ghost text-xs"
                  onClick={() => setPortGuestIPInput('')}
                  data-testid="port-guest-ip-clear"
                  aria-label="Clear guest IP filter"
                >
                  <X size={12} /> Clear guest IP
                </button>
              )}
            </div>
          </div>
          <PortList vmId={id} portList={portList} refreshPorts={refreshPorts} portSearch={portSearch} />
          {portTotal > 0 && (
            <div className="px-4 pb-3" data-testid="port-pagination">
              <PaginationControls
                page={portPage}
                perPage={portPerPage}
                total={portTotal}
                perPageOptions={[10, 25, 50, 100]}
                itemLabel="rules"
                onPageChange={setPortPage}
                onPerPageChange={(n) => { setPortPerPage(n); setPortPage(1); }}
              />
            </div>
          )}
        </div>
      )}

      {activeTab === 'overview' && (
        <div className="mt-4">
          <button className="btn-secondary" onClick={() => setShowImageModal(true)}>
            <Download size={14} /> Export as Image
          </button>
        </div>
      )}
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
  // Clone progress is keyed by the source VM and the cloned name the daemon
  // echoes back in each frame.
  const progress = useOperationProgress(vm?.id, 'clone', name.trim());

  useEffect(() => {
    if (open && vm) {
      setName(`${vm.name}-clone`);
      cloneMut.reset();
    }
  }, [open, vm, cloneMut.reset]);

  const handleClose = () => {
    progress.reset();
    cloneMut.reset();
    setName('');
    onClose();
  };

  const handleSubmit = async () => {
    progress.start();
    try {
      const cloned = await cloneMut.execute(name.trim());
      progress.finish();
      handleClose();
      if (cloned?.id) {
        navigate(`/vms/${cloned.id}`);
      }
    } catch {
      // Error shown inline.
    } finally {
      progress.stop();
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
        <ProgressReadout active={cloneMut.loading} percent={progress.percent} label="Cloning machine…" testId="clone-vm-progress" />
        {cloneMut.error && <p className="text-sm text-red-400">{cloneMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button data-testid="btn-cancel-clone" className="btn-secondary" onClick={handleClose} disabled={cloneMut.loading}>Cancel</button>
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
  const [diskBus, setDiskBus] = useState('');
  const [nicModel, setNicModel] = useState('');
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
    setDiskBus(spec.disk_bus || '');
    setNicModel(spec.nic_model || '');
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

    // Roadmap 5.6.12 — disk_bus / nic_model are mutable on PATCH. Pointer
    // semantics: send the new value (including empty string to clear) only
    // when the user actually changed it.
    const currentDiskBus = spec.disk_bus || '';
    const currentNicModel = spec.nic_model || '';
    if (diskBus !== currentDiskBus) {
      patch.disk_bus = diskBus;
    }
    if (nicModel !== currentNicModel) {
      patch.nic_model = nicModel;
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

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label flex items-center justify-between">
              <span>Disk bus</span>
              <button
                type="button"
                data-testid="btn-edit-switch-virtio"
                className="text-[10px] text-bronze-400 hover:text-bronze-300 underline"
                onClick={() => { setDiskBus('virtio'); setNicModel('virtio'); }}
                title="Switch both disk bus and NIC to virtio (roadmap 5.6.12)"
              >
                Switch to virtio
              </button>
            </label>
            <select
              data-testid="select-edit-disk-bus"
              className="input"
              value={diskBus}
              onChange={e => setDiskBus(e.target.value)}
            >
              <option value="">default</option>
              <option value="virtio">virtio</option>
              <option value="sata">sata</option>
            </select>
          </div>
          <div>
            <label className="label">NIC model</label>
            <select
              data-testid="select-edit-nic-model"
              className="input"
              value={nicModel}
              onChange={e => setNicModel(e.target.value)}
            >
              <option value="">default</option>
              <option value="virtio">virtio</option>
              <option value="e1000e">e1000e</option>
            </select>
          </div>
        </div>

        <OperationProgress active={updateMut.loading} label="Applying changes — the machine restarts to take effect…" testId="edit-vm-progress" />
        {updateMut.error && <p className="text-sm text-red-400">Error: {updateMut.error}</p>}

        <div className="flex justify-end gap-2">
          <button data-testid="btn-cancel-edit" className="btn-secondary" onClick={onClose} disabled={updateMut.loading}>Cancel</button>
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
function SnapshotList({ vmId, snapList, refreshSnaps, snapSearch }) {
  const restoreMut = useMutation((name) => snapshots.restore(vmId, name));
  const deleteMut  = useMutation((name) => snapshots.delete(vmId, name));
  const bulkMut    = useMutation((body) => snapshots.bulkDelete(vmId, body));
  const [selected, setSelected] = useState(() => new Set());
  const [bulkResult, setBulkResult] = useState(null);
  const [editing, setEditing] = useState(null);
  const [restoringName, setRestoringName] = useState(null);
  const [deletingName, setDeletingName] = useState(null);

  // Drop selections that no longer exist (e.g., after a deletion or list refresh).
  React.useEffect(() => {
    if (!snapList?.length) {
      if (selected.size) setSelected(new Set());
      return;
    }
    const existing = new Set(snapList.map(s => s.name));
    let changed = false;
    const next = new Set();
    selected.forEach(n => {
      if (existing.has(n)) next.add(n);
      else changed = true;
    });
    if (changed) setSelected(next);
  }, [snapList]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!snapList?.length) {
    return (
      <EmptyState
        icon={Camera}
        title="No snapshots"
        description={
          snapSearch
            ? `No snapshots match "${snapSearch}".`
            : 'Capture the VM state at any point.'
        }
      />
    );
  }

  const allSelected = selected.size > 0 && selected.size === snapList.length;
  const someSelected = selected.size > 0 && !allSelected;
  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(snapList.map(s => s.name)));
  };
  const toggleOne = (name) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name); else next.add(name);
      return next;
    });
  };
  const handleBulkDelete = async () => {
    if (!selected.size) return;
    const result = await bulkMut.execute({ names: Array.from(selected) });
    setBulkResult(result);
    setSelected(new Set());
    refreshSnaps();
  };

  return (
    <div>
      <div className="flex items-center justify-between px-4 py-1.5 border-b border-steel-800/40 bg-steel-900/40">
        <label className="flex items-center gap-2 text-xs text-steel-400 cursor-pointer">
          <input
            type="checkbox"
            checked={allSelected}
            ref={(el) => { if (el) el.indeterminate = someSelected; }}
            onChange={toggleAll}
            data-testid="snap-select-all"
          />
          {selected.size > 0 ? `${selected.size} selected` : 'Select all'}
        </label>
        <button
          className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40 disabled:cursor-not-allowed"
          onClick={handleBulkDelete}
          disabled={!selected.size || bulkMut.loading}
          data-testid="btn-bulk-delete-snaps"
        >
          <Trash2 size={12} /> Delete selected
        </button>
      </div>
      {(restoringName || deletingName || bulkMut.loading) && (
        <div className="px-4 py-2.5 border-b border-steel-800/40 bg-steel-900/30">
          <OperationProgress
            active
            label={
              restoringName
                ? `Restoring snapshot "${restoringName}"…`
                : bulkMut.loading
                ? `Deleting ${selected.size || ''} snapshot(s)…`
                : `Deleting snapshot "${deletingName}"…`
            }
            testId="snapshot-op-progress"
          />
        </div>
      )}
      <div className="divide-y divide-steel-800/40">
        {snapList.map(snap => (
          <div key={snap.name} className="flex items-start justify-between px-4 py-2.5 hover:bg-steel-800/20 transition-colors gap-3" data-testid={`snap-${snap.name}`}>
            <div className="flex items-start gap-2 min-w-0 flex-1">
              <input
                type="checkbox"
                checked={selected.has(snap.name)}
                onChange={() => toggleOne(snap.name)}
                data-testid={`snap-checkbox-${snap.name}`}
                className="mt-1"
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <Clock size={12} className="text-steel-600 flex-shrink-0" />
                  <span className="font-mono text-sm text-steel-200 truncate">{snap.name}</span>
                </div>
                {snap.created_at ? (
                  <p className="text-[11px] font-mono text-steel-500 mt-0.5 ml-5" data-testid={`snap-created-${snap.name}`}>
                    {new Date(snap.created_at).toLocaleString()}
                  </p>
                ) : null}
                {snap.description ? (
                  <p className="text-xs text-steel-500 mt-1 ml-5 line-clamp-2" data-testid={`snap-desc-${snap.name}`}>{snap.description}</p>
                ) : null}
                {snap.tags && snap.tags.length > 0 ? (
                  <div className="flex flex-wrap gap-1 mt-1 ml-5" data-testid={`snap-tags-${snap.name}`}>
                    {snap.tags.map((tag) => (
                      <span key={tag} className="badge bg-blue-500/10 text-blue-300 border-blue-500/20">#{tag}</span>
                    ))}
                  </div>
                ) : null}
              </div>
            </div>
            <div className="flex items-center gap-1 flex-shrink-0">
              <button
                className="btn-ghost text-xs text-steel-400 hover:text-steel-200"
                onClick={() => setEditing(snap)}
                data-testid={`btn-edit-snap-${snap.name}`}
                title="Edit description"
              >
                <Pencil size={12} />
              </button>
              <button
                className="btn-ghost text-xs text-blue-400 hover:text-blue-300 disabled:opacity-40"
                disabled={restoreMut.loading || deleteMut.loading}
                onClick={async () => {
                  setRestoringName(snap.name);
                  try { await restoreMut.execute(snap.name); refreshSnaps(); }
                  finally { setRestoringName(null); }
                }}
              >
                <RotateCcw size={12} /> Restore
              </button>
              <button
                className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40"
                disabled={restoreMut.loading || deleteMut.loading}
                onClick={async () => {
                  setDeletingName(snap.name);
                  try { await deleteMut.execute(snap.name); refreshSnaps(); }
                  finally { setDeletingName(null); }
                }}
                data-testid={`btn-delete-snap-${snap.name}`}
              >
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        ))}
      </div>
      {bulkResult && (
        <div className="px-4 py-2 text-xs text-steel-400" data-testid="snap-bulk-result">
          {(() => {
            const total = bulkResult.results.length;
            const ok = bulkResult.results.filter(r => r.success).length;
            return `Bulk delete: ${ok} of ${total} succeeded`;
          })()}
        </div>
      )}
      <EditSnapshotModal
        vmId={vmId}
        snapshot={editing}
        onClose={() => setEditing(null)}
        onSaved={() => { setEditing(null); refreshSnaps(); }}
      />
    </div>
  );
}

// --- Edit Snapshot Modal ---
function EditSnapshotModal({ vmId, snapshot, onClose, onSaved }) {
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const updateMut = useMutation((args) =>
    snapshots.update(vmId, args.name, args.spec),
  );

  React.useEffect(() => {
    if (snapshot) {
      setDescription(snapshot.description || '');
      setTagsInput((snapshot.tags || []).join(', '));
    }
  }, [snapshot]);

  if (!snapshot) return null;

  const handleSubmit = async () => {
    const spec = {};
    const trimmedDesc = description;
    if (trimmedDesc !== (snapshot.description || '')) {
      spec.description = trimmedDesc;
    }
    // Tag pointer semantics: nil = no change, [] = clear. Compare the
    // typed list against the stored list order-independently (lowercase
    // + sort both sides) so re-submitting a permutation is treated as a
    // no-op and skips the round-trip.
    const nextTags = tagsInput.split(',').map((t) => t.trim()).filter(Boolean);
    const normalisedNext = nextTags.map((t) => t.toLowerCase()).sort();
    const normalisedCurrent = (snapshot.tags || []).map((t) => t.toLowerCase()).sort();
    const sameTags = normalisedNext.length === normalisedCurrent.length
      && normalisedNext.every((t, i) => t === normalisedCurrent[i]);
    if (!sameTags) {
      spec.tags = nextTags;
    }
    if (Object.keys(spec).length === 0) {
      onSaved();
      return;
    }
    await updateMut.execute({ name: snapshot.name, spec });
    onSaved();
  };

  return (
    <Modal open={!!snapshot} onClose={onClose} title={`Edit ${snapshot.name}`}>
      <div className="space-y-4">
        <div>
          <label className="label">Description</label>
          <textarea
            className="input"
            rows={3}
            maxLength={1024}
            placeholder="What is this snapshot for?"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="input-edit-snap-description"
            autoFocus
          />
          <p className="mt-1 text-xs text-steel-500">{description.length}/1024 characters</p>
        </div>
        <div>
          <label className="label">Tags <span className="text-steel-500 font-normal">(comma-separated; clear to remove)</span></label>
          <input
            className="input font-mono"
            placeholder="audit, production"
            value={tagsInput}
            onChange={e => setTagsInput(e.target.value)}
            data-testid="input-edit-snap-tags"
          />
        </div>
        {updateMut.error && <p className="text-sm text-red-400">{updateMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button
            className="btn-primary"
            onClick={handleSubmit}
            disabled={updateMut.loading}
            data-testid="btn-submit-edit-snap"
          >
            {updateMut.loading ? <Spinner size={14} /> : <Pencil size={14} />} Save
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Port List ---
function PortList({ vmId, portList, refreshPorts, portSearch }) {
  const removeMut = useMutation((portId) => ports.remove(vmId, portId));
  const bulkMut   = useMutation((body) => ports.bulkDelete(vmId, body));
  const [selected, setSelected] = useState(() => new Set());
  const [bulkResult, setBulkResult] = useState(null);
  const [editing, setEditing] = useState(null);

  // Drop selections for rows that no longer exist (e.g., after a delete or refresh).
  React.useEffect(() => {
    if (!portList?.length) {
      if (selected.size) setSelected(new Set());
      return;
    }
    const existing = new Set(portList.map(p => p.id));
    let changed = false;
    const next = new Set();
    selected.forEach(id => {
      if (existing.has(id)) next.add(id);
      else changed = true;
    });
    if (changed) setSelected(next);
  }, [portList]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!portList?.length) {
    if (portSearch) {
      return (
        <EmptyState
          icon={Network}
          title={`No port forwards match "${portSearch}"`}
          description="Try a different keyword, or clear the search to see every rule."
        />
      );
    }
    return <EmptyState icon={Network} title="No port forwards" description="Expose VM services to the network." />;
  }

  const allSelected = selected.size > 0 && selected.size === portList.length;
  const someSelected = selected.size > 0 && !allSelected;
  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(portList.map(p => p.id)));
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
    refreshPorts();
  };

  return (
    <div>
      <div className="flex items-center justify-between px-4 py-1.5 border-b border-steel-800/40 bg-steel-900/40">
        <label className="flex items-center gap-2 text-xs text-steel-400 cursor-pointer">
          <input
            type="checkbox"
            checked={allSelected}
            ref={(el) => { if (el) el.indeterminate = someSelected; }}
            onChange={toggleAll}
            data-testid="port-select-all"
          />
          {selected.size > 0 ? `${selected.size} selected` : 'Select all'}
        </label>
        <button
          className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40 disabled:cursor-not-allowed"
          onClick={handleBulkDelete}
          disabled={!selected.size || bulkMut.loading}
          data-testid="btn-bulk-delete-ports"
        >
          <Trash2 size={12} /> Delete selected
        </button>
      </div>
      <div className="divide-y divide-steel-800/40">
        {portList.map(pf => (
          <div key={pf.id} className="flex items-center justify-between px-4 py-2.5 hover:bg-steel-800/20 transition-colors" data-testid={`port-row-${pf.id}`}>
            <div className="flex items-center gap-2 min-w-0 flex-1">
              <input
                type="checkbox"
                checked={selected.has(pf.id)}
                onChange={() => toggleOne(pf.id)}
                data-testid={`port-checkbox-${pf.id}`}
              />
              <div>
                <div>
                  <span className="font-mono text-sm text-steel-200">
                    :{pf.host_port} → {pf.guest_ip}:{pf.guest_port}
                  </span>
                  <span className="ml-2 badge bg-steel-800/60 text-steel-500 border-steel-700/40">{pf.protocol}</span>
                </div>
                {pf.description && (
                  <div className="mt-0.5 text-xs text-steel-400" data-testid={`port-description-${pf.id}`}>
                    {pf.description}
                  </div>
                )}
                {Array.isArray(pf.tags) && pf.tags.length > 0 && (
                  <div className="mt-1 flex flex-wrap gap-1" data-testid={`port-tags-${pf.id}`}>
                    {pf.tags.map((t) => (
                      <span
                        key={t}
                        className="badge bg-steel-800/60 text-steel-300 border-steel-700/40"
                      >
                        {t}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            </div>
            <div className="flex items-center gap-1">
              <button
                className="btn-ghost text-xs text-steel-400 hover:text-steel-200"
                onClick={() => setEditing(pf)}
                data-testid={`btn-edit-port-${pf.id}`}
                title="Edit description"
              >
                <Pencil size={12} />
              </button>
              <button
                className="btn-ghost text-xs text-red-400 hover:text-red-300"
                onClick={async () => { await removeMut.execute(pf.id); refreshPorts(); }}
              >
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        ))}
      </div>
      {bulkResult && (
        <div className="px-4 py-2 text-xs text-steel-400" data-testid="port-bulk-result">
          {(() => {
            const total = bulkResult.results.length;
            const ok = bulkResult.results.filter(r => r.success).length;
            return `Bulk delete: ${ok} of ${total} succeeded`;
          })()}
        </div>
      )}
      <EditPortModal
        vmId={vmId}
        portForward={editing}
        onClose={() => setEditing(null)}
        onSaved={() => { setEditing(null); refreshPorts(); }}
      />
    </div>
  );
}

// --- Edit Port Forward Modal ---
function EditPortModal({ vmId, portForward, onClose, onSaved }) {
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const updateMut = useMutation((args) => {
    const patch = { description: args.description };
    if (args.tagsChanged) {
      patch.tags = args.tags;
    }
    return ports.update(vmId, args.id, patch);
  });

  React.useEffect(() => {
    if (portForward) {
      setDescription(portForward.description || '');
      setTagsInput((portForward.tags || []).join(', '));
    }
  }, [portForward]);

  if (!portForward) return null;

  const handleSubmit = async () => {
    const parsedTags = tagsInput
      .split(',')
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    const currentTags = portForward.tags || [];
    const sortedParsed = [...parsedTags].sort();
    const sortedCurrent = [...currentTags].sort();
    const tagsChanged = JSON.stringify(sortedParsed) !== JSON.stringify(sortedCurrent);
    await updateMut.execute({
      id: portForward.id,
      description,
      tags: parsedTags,
      tagsChanged,
    });
    onSaved();
  };

  return (
    <Modal open={!!portForward} onClose={onClose} title={`Edit port forward :${portForward.host_port}`}>
      <div className="space-y-4">
        <div className="text-xs text-steel-500 font-mono">
          :{portForward.host_port} → {portForward.guest_ip}:{portForward.guest_port}/{portForward.protocol}
        </div>
        <div>
          <label className="label">Description</label>
          <input
            className="input"
            type="text"
            maxLength={256}
            placeholder="e.g. ssh-jumpbox, metrics scrape"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="input-edit-port-description"
            autoFocus
          />
          <p className="mt-1 text-xs text-steel-500">{description.length}/256 characters</p>
        </div>
        <div>
          <label className="label">Tags <span className="text-steel-500 font-normal">(comma-separated; empty to clear)</span></label>
          <input
            className="input"
            type="text"
            placeholder="production, web"
            value={tagsInput}
            onChange={e => setTagsInput(e.target.value)}
            data-testid="input-edit-port-tags"
          />
        </div>
        {updateMut.error && <p className="text-sm text-red-400">{updateMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button
            className="btn-primary"
            onClick={handleSubmit}
            disabled={updateMut.loading}
            data-testid="btn-submit-edit-port"
          >
            {updateMut.loading ? <Spinner size={14} /> : <Pencil size={14} />} Save
          </button>
        </div>
      </div>
    </Modal>
  );
}

// --- Create Snapshot Modal ---
function CreateSnapshotModal({ vmId, open, onClose, onCreated }) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const createMut = useMutation((args) => snapshots.create(vmId, args.name, args.description, args.tags));

  const handleSubmit = async () => {
    const tags = tagsInput.split(',').map((t) => t.trim()).filter(Boolean);
    await createMut.execute({ name, description: description.trim(), tags });
    onCreated();
    onClose();
    setName('');
    setDescription('');
    setTagsInput('');
  };

  return (
    <Modal open={open} onClose={onClose} title="Create Snapshot">
      <div className="space-y-4">
        <div>
          <label className="label">Snapshot Name</label>
          <input className="input" placeholder="before-update" value={name} onChange={e => setName(e.target.value)} autoFocus data-testid="input-snap-name" />
        </div>
        <div>
          <label className="label">Description <span className="text-steel-500 font-normal">(optional)</span></label>
          <textarea
            className="input"
            rows={2}
            maxLength={1024}
            placeholder="Why this snapshot? e.g. before applying May patch"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="input-snap-description"
          />
        </div>
        <div>
          <label className="label">Tags <span className="text-steel-500 font-normal">(comma-separated, optional)</span></label>
          <input
            className="input font-mono"
            placeholder="audit, production, before-patch"
            value={tagsInput}
            onChange={e => setTagsInput(e.target.value)}
            data-testid="input-snap-tags"
          />
        </div>
        <OperationProgress active={createMut.loading} label="Creating snapshot…" testId="snapshot-create-progress" />
        {createMut.error && <p className="text-sm text-red-400">{createMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose} disabled={createMut.loading}>Cancel</button>
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
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const addMut = useMutation(() => {
    const tags = tagsInput
      .split(',')
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    return ports.add(
      vmId,
      parseInt(hostPort),
      parseInt(guestPort),
      protocol,
      description.trim() || undefined,
      tags.length > 0 ? tags : undefined,
    );
  });

  const handleSubmit = async () => {
    await addMut.execute();
    onCreated();
    onClose();
    setHostPort('');
    setGuestPort('');
    setDescription('');
    setTagsInput('');
  };

  return (
    <Modal open={open} onClose={onClose} title="Add Port Forward">
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="label">Host Port</label>
            <input className="input" type="number" placeholder="2222" value={hostPort} onChange={e => setHostPort(e.target.value)} autoFocus data-testid="input-host-port" />
          </div>
          <div>
            <label className="label">Guest Port</label>
            <input className="input" type="number" placeholder="22" value={guestPort} onChange={e => setGuestPort(e.target.value)} data-testid="input-guest-port" />
          </div>
        </div>
        <div>
          <label className="label">Protocol</label>
          <select className="input" value={protocol} onChange={e => setProtocol(e.target.value)}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </div>
        <div>
          <label className="label">Description <span className="text-steel-500">(optional)</span></label>
          <input
            className="input"
            type="text"
            placeholder="e.g. ssh-jumpbox"
            maxLength={256}
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="input-port-description"
          />
        </div>
        <div>
          <label className="label">Tags <span className="text-steel-500">(comma-separated, optional)</span></label>
          <input
            className="input"
            type="text"
            placeholder="production, web"
            value={tagsInput}
            onChange={e => setTagsInput(e.target.value)}
            data-testid="input-port-tags"
          />
        </div>
        {addMut.error && <p className="text-sm text-red-400">{addMut.error}</p>}
        <div className="flex justify-end gap-2">
          <button className="btn-secondary" onClick={onClose}>Cancel</button>
          <button
            className="btn-primary"
            onClick={handleSubmit}
            disabled={!hostPort || !guestPort || addMut.loading}
            data-testid="btn-submit-port"
          >
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
  const progress = useOperationProgress(vmId, 'export', name);

  const handleSubmit = async () => {
    progress.start();
    try {
      await createMut.execute(name);
      progress.finish();
      setDone(true);
    } finally {
      progress.stop();
    }
  };

  const handleClose = () => {
    progress.reset();
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
          <ProgressReadout active={createMut.loading} percent={progress.percent} label="Exporting disk image…" testId="export-image-progress" />
          {createMut.error && <p className="text-sm text-red-400">{createMut.error}</p>}
          <div className="flex justify-end gap-2">
            <button className="btn-secondary" onClick={handleClose} disabled={createMut.loading}>Cancel</button>
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
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 px-4 py-4 border-b border-steel-800/40">
              <MetricGauge
                label="CPU"
                value={typeof cur.cpu_percent === 'number' ? cur.cpu_percent : 0}
                max={100}
                display={fmtPercent(cur.cpu_percent)}
                testId="metric-gauge-cpu"
              />
              <MetricGauge
                label="Memory"
                value={memPercent(cur)}
                max={100}
                display={`${fmtMB(cur.mem_used_mb)}${memTotal(cur) ? ` / ${fmtMB(memTotal(cur))}` : ''}`}
                testId="metric-gauge-mem"
              />
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


function MetricGauge({ label, value, max, display, testId }) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
  return (
    <div data-testid={testId}>
      <div className="flex items-baseline justify-between mb-1.5">
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
        <span className="font-mono text-sm text-steel-100" data-testid={`${testId}-value`}>{display}</span>
      </div>
      <ProgressBar value={value} max={max} height="h-2" />
      <p className="text-[10px] font-mono text-steel-500 mt-1">{Math.round(pct)}% utilised</p>
    </div>
  );
}

// Total guest memory inferred from the current sample (used + available). Both
// fields come from the same /stats snapshot so the sum is the visible total.
function memTotal(cur) {
  const used = typeof cur?.mem_used_mb === 'number' ? cur.mem_used_mb : 0;
  const avail = typeof cur?.mem_avail_mb === 'number' ? cur.mem_avail_mb : 0;
  return used + avail;
}

function memPercent(cur) {
  const total = memTotal(cur);
  if (!total) return 0;
  return ((cur?.mem_used_mb || 0) / total) * 100;
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
