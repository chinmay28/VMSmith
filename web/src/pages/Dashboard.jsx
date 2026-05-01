import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Server, HardDrive, Activity, Plus, Cpu, MemoryStick, Database, TrendingUp } from 'lucide-react';
import { vms, images as imagesApi, quotas as quotasApi, host as hostApi } from '../api/client';
import { useFetch } from '../hooks/useFetch';
import { useEventStream } from '../hooks/useEventStream';
import { PageHeader, StatCard, StatusBadge, Spinner, ErrorBanner, EmptyState, LiveIndicator } from '../components/Shared';
import { listData, normalizeVMList } from '../utils/normalize';

const TOP_METRICS = [
  { value: 'cpu', label: 'CPU', format: (v) => `${v.toFixed(1)}%` },
  { value: 'mem', label: 'Memory', format: (v) => `${Math.round(v).toLocaleString()} MB` },
  { value: 'disk_read', label: 'Disk Read', format: (v) => formatBps(v) },
  { value: 'disk_write', label: 'Disk Write', format: (v) => formatBps(v) },
  { value: 'net_rx', label: 'Net RX', format: (v) => formatBps(v) },
  { value: 'net_tx', label: 'Net TX', format: (v) => formatBps(v) },
];

function formatBps(value) {
  if (!Number.isFinite(value) || value <= 0) return '0 B/s';
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  let current = value;
  let i = 0;
  while (current >= 1024 && i < units.length - 1) {
    current /= 1024;
    i += 1;
  }
  const decimals = current >= 10 || i === 0 ? 0 : 1;
  return `${current.toFixed(decimals)} ${units[i]}`;
}

function totalCount(response) {
  if (Array.isArray(response)) return response.length;
  const explicitTotal = response?.meta?.totalCount;
  if (Number.isFinite(explicitTotal) && explicitTotal > 0) return explicitTotal;
  return response?.data?.length ?? 0;
}

// Event types whose arrival should immediately refresh dashboard counters.
const VM_LIFECYCLE_TYPES = new Set([
  'vm.created', 'vm.cloned', 'vm.deleted', 'vm.updated',
  'vm.started', 'vm.stopped', 'vm.crashed', 'vm.shutdown',
]);
const IMAGE_TYPES = new Set(['image.uploaded', 'image.created', 'image.deleted']);

export default function Dashboard() {
  const [topMetric, setTopMetric] = useState('cpu');
  const { data: vmResponse, loading: vmLoading, error: vmError, refresh: refreshVMs } = useFetch(() => vms.list(), [], 30000);
  const { data: imageResponse, loading: imgLoading, refresh: refreshImages } = useFetch(() => imagesApi.list(), [], 30000);
  const { data: quotaUsage, loading: quotaLoading, refresh: refreshQuotas } = useFetch(() => quotasApi.usage(), [], 30000);
  const { data: hostStats, loading: hostLoading, error: hostError } = useFetch(() => hostApi.stats(), [], 10000);
  const { data: topVMsResponse, loading: topLoading, error: topError } = useFetch(
    () => vms.top({ metric: topMetric, limit: 5 }),
    [topMetric],
    10000,
  );
  const navigate = useNavigate();

  const handleEvent = useCallback((evt) => {
    if (!evt?.type) return;
    if (VM_LIFECYCLE_TYPES.has(evt.type)) {
      refreshVMs();
      refreshQuotas();
    } else if (IMAGE_TYPES.has(evt.type)) {
      refreshImages();
    }
  }, [refreshVMs, refreshImages, refreshQuotas]);

  const { status: liveStatus } = useEventStream({ onEvent: handleEvent });

  const vmList = normalizeVMList(vmResponse);
  const imageList = listData(imageResponse);
  const runningCount = vmList.filter(v => v.state === 'running').length;
  const hasVMCountFallback = totalCount(vmResponse) > 0 || vmList.length > 0;
  const totalVMCount = hostStats?.vm_count ?? totalCount(vmResponse);
  const totalImageCount = totalCount(imageResponse) || imageList.length;
  const showHostError = hostError && !hasVMCountFallback;

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle="System overview"
        actions={
          <>
            <LiveIndicator status={liveStatus} />
            {hostStats?.active_sse_streams > 0 && (
              <span
                className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500"
                title="Open Server-Sent Events streams (events + metrics)"
                data-testid="active-sse-streams"
              >
                {hostStats.active_sse_streams} stream{hostStats.active_sse_streams === 1 ? '' : 's'}
              </span>
            )}
            <button className="btn-primary" onClick={() => navigate('/vms?create=1')}>
              <Plus size={15} /> New Machine
            </button>
          </>
        }
      />

      <div className="grid grid-cols-3 gap-3 mb-6">
        <StatCard label="Total Machines" value={vmLoading && !hostStats ? '—' : totalVMCount} icon={Server} testId="stat-total" />
        <StatCard label="Running" value={vmLoading ? '—' : runningCount} icon={Activity} accent testId="stat-running" />
        <StatCard label="Images" value={imgLoading ? '—' : totalImageCount} icon={HardDrive} testId="stat-images" />
      </div>

      {showHostError && <div className="mb-4"><ErrorBanner message={hostError} /></div>}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-3 mb-6">
        <HostUsageCard label="Host CPU" resource={hostStats?.cpu} icon={Cpu} loading={hostLoading} formatValue={(resource) => `${resource?.percentage ?? 0}%`} />
        <HostUsageCard label="Host RAM" resource={hostStats?.ram} icon={MemoryStick} loading={hostLoading} formatSubtitle={(resource) => `${formatBytes(resource?.used)} / ${formatBytes(resource?.total)} used`} />
        <HostUsageCard label="Host Disk" resource={hostStats?.disk} icon={Database} loading={hostLoading} formatSubtitle={(resource) => `${formatBytes(resource?.used)} / ${formatBytes(resource?.total)} used`} />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-3 mb-6">
        <QuotaCard label="Machines allocated" resource={quotaUsage?.vms} unit="VMs" icon={Server} loading={quotaLoading} />
        <QuotaCard label="vCPUs allocated" resource={quotaUsage?.cpus} unit="vCPU" icon={Cpu} loading={quotaLoading} />
        <QuotaCard label="RAM allocated" resource={quotaUsage?.ram_mb} unit="MB" icon={MemoryStick} loading={quotaLoading} />
        <QuotaCard label="Disk allocated" resource={quotaUsage?.disk_gb} unit="GB" icon={Database} loading={quotaLoading} />
      </div>

      <TopVMsCard
        metric={topMetric}
        onMetricChange={setTopMetric}
        response={topVMsResponse}
        loading={topLoading}
        error={topError}
        onSelectVM={(id) => navigate(`/vms/${id}`)}
      />

      <div className="card">
        <div className="px-4 py-3 border-b border-steel-800/40">
          <h2 className="text-sm font-display font-semibold text-steel-300">Machines</h2>
        </div>

        {vmError && <div className="p-4"><ErrorBanner message={vmError} /></div>}

        {vmLoading ? (
          <div className="flex justify-center py-12"><Spinner size={20} /></div>
        ) : !vmList.length ? (
          <EmptyState
            icon={Server}
            title="No machines yet"
            description="Create your first virtual machine to get started."
            action={
              <button className="btn-primary" onClick={() => navigate('/vms?create=1')}>
                <Plus size={15} /> Create Machine
              </button>
            }
          />
        ) : (
          <table className="w-full" data-testid="dashboard-vm-table">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Status</th>
                <th className="table-header table-cell">IP</th>
                <th className="table-header table-cell">CPU / RAM</th>
              </tr>
            </thead>
            <tbody>
              {vmList.map(vm => {
                const spec = vm.spec || {};
                const cpuText = Number.isFinite(spec.cpus) ? spec.cpus : '—';
                const ramText = Number.isFinite(spec.ram_mb) ? spec.ram_mb : '—';

                return (
                  <tr
                    key={vm.id}
                    className="cursor-pointer hover:bg-steel-800/30 transition-colors"
                    onClick={() => navigate(`/vms/${vm.id}`)}
                    data-testid={`vm-row-${vm.name}`}
                  >
                    <td className="table-cell">
                      <span className="font-mono text-steel-100 text-sm">{vm.name}</span>
                    </td>
                    <td className="table-cell"><StatusBadge state={vm.state} /></td>
                    <td className="table-cell font-mono text-xs text-steel-400">
                      {vm.ip || '—'}
                    </td>
                    <td className="table-cell font-mono text-xs text-steel-400">
                      {cpuText} vCPU · {ramText} MB
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) {
    current /= 1024;
    index += 1;
  }
  const decimals = current >= 10 || index === 0 ? 0 : 1;
  return `${current.toFixed(decimals)} ${units[index]}`;
}

function HostUsageCard({ label, resource, icon: Icon, loading, formatValue, formatSubtitle }) {
  if (loading) {
    return <StatCard label={label} value="—" icon={Icon} />;
  }

  const value = formatValue ? formatValue(resource) : `${resource?.percentage ?? 0}%`;
  const subtitle = formatSubtitle
    ? formatSubtitle(resource)
    : `${formatBytes(resource?.available)} free of ${formatBytes(resource?.total)}`;

  return (
    <div className="card-hover px-4 py-3.5">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
        {Icon && <Icon size={14} className="text-steel-600" />}
      </div>
      <p className="font-display font-bold text-2xl text-steel-100">{value}</p>
      <p className="text-xs text-steel-500 mt-1">{subtitle}</p>
    </div>
  );
}

function TopVMsCard({ metric, onMetricChange, response, loading, error, onSelectVM }) {
  const items = Array.isArray(response?.items) ? response.items : [];
  const config = TOP_METRICS.find(m => m.value === metric) ?? TOP_METRICS[0];

  return (
    <div className="card mb-6" data-testid="top-vms-card">
      <div className="px-4 py-3 border-b border-steel-800/40 flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2">
          <TrendingUp size={14} className="text-steel-500" />
          <h2 className="text-sm font-display font-semibold text-steel-300">Top 5 Machines by {config.label}</h2>
        </div>
        <select
          className="bg-steel-900 border border-steel-800/60 rounded text-xs text-steel-200 px-2 py-1 font-mono"
          value={metric}
          onChange={(e) => onMetricChange(e.target.value)}
          aria-label="Top VMs metric selector"
          data-testid="top-vms-metric"
        >
          {TOP_METRICS.map(m => (
            <option key={m.value} value={m.value}>{m.label}</option>
          ))}
        </select>
      </div>

      {error && <div className="p-4"><ErrorBanner message={error} /></div>}

      {loading && !response ? (
        <div className="flex justify-center py-6"><Spinner size={16} /></div>
      ) : items.length === 0 ? (
        <div className="px-4 py-6 text-center text-xs text-steel-500" data-testid="top-vms-empty">
          No samples yet for {config.label.toLowerCase()}.
        </div>
      ) : (
        <table className="w-full" data-testid="top-vms-table">
          <thead>
            <tr className="border-b border-steel-800/40">
              <th className="table-header table-cell w-10">#</th>
              <th className="table-header table-cell">Name</th>
              <th className="table-header table-cell">Status</th>
              <th className="table-header table-cell text-right">{config.label}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item, idx) => (
              <tr
                key={item.vm_id}
                className="cursor-pointer hover:bg-steel-800/30 transition-colors"
                onClick={() => onSelectVM(item.vm_id)}
                data-testid={`top-vm-row-${item.name}`}
              >
                <td className="table-cell text-xs font-mono text-steel-500">{idx + 1}</td>
                <td className="table-cell">
                  <span className="font-mono text-steel-100 text-sm">{item.name}</span>
                </td>
                <td className="table-cell"><StatusBadge state={item.state} /></td>
                <td className="table-cell text-right font-mono text-sm text-steel-200">
                  {config.format(Number(item.value) || 0)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function QuotaCard({ label, resource, unit, icon: Icon, loading }) {
  if (loading) {
    return <StatCard label={label} value="—" icon={Icon} />;
  }

  const used = resource?.used ?? 0;
  const limit = resource?.limit ?? 0;
  const value = limit > 0 ? `${used}/${limit}` : `${used}`;
  const subtitle = limit > 0 ? `${used} of ${limit} ${unit}` : `${used} ${unit} allocated`;

  return (
    <div className="card-hover px-4 py-3.5">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-mono uppercase tracking-[0.15em] text-steel-500">{label}</span>
        {Icon && <Icon size={14} className="text-steel-600" />}
      </div>
      <p className="font-display font-bold text-2xl text-steel-100">{value}</p>
      <p className="text-xs text-steel-500 mt-1">{subtitle}</p>
    </div>
  );
}
