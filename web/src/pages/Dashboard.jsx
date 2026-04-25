import { useNavigate } from 'react-router-dom';
import { Server, HardDrive, Activity, Plus, Cpu, MemoryStick, Database } from 'lucide-react';
import { vms, images as imagesApi, quotas as quotasApi, host as hostApi } from '../api/client';
import { useFetch } from '../hooks/useFetch';
import { PageHeader, StatCard, StatusBadge, Spinner, ErrorBanner, EmptyState } from '../components/Shared';

function listData(response) {
  if (Array.isArray(response)) return response;
  return response?.data || [];
}

function totalCount(response) {
  if (Array.isArray(response)) return response.length;
  const explicitTotal = response?.meta?.totalCount;
  if (Number.isFinite(explicitTotal) && explicitTotal > 0) return explicitTotal;
  return response?.data?.length ?? 0;
}

export default function Dashboard() {
  const { data: vmResponse, loading: vmLoading, error: vmError } = useFetch(() => vms.list(), [], 5000);
  const { data: imageResponse, loading: imgLoading } = useFetch(() => imagesApi.list(), [], 10000);
  const { data: quotaUsage, loading: quotaLoading } = useFetch(() => quotasApi.usage(), [], 5000);
  const { data: hostStats, loading: hostLoading, error: hostError } = useFetch(() => hostApi.stats(), [], 10000);
  const navigate = useNavigate();

  const vmList = listData(vmResponse);
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
          <button className="btn-primary" onClick={() => navigate('/vms?create=1')}>
            <Plus size={15} /> New Machine
          </button>
        }
      />

      <div className="grid grid-cols-3 gap-3 mb-6">
        <StatCard label="Total Machines" value={vmLoading && !hostStats ? '—' : totalVMCount} icon={Server} />
        <StatCard label="Running" value={vmLoading ? '—' : runningCount} icon={Activity} accent />
        <StatCard label="Images" value={imgLoading ? '—' : totalImageCount} icon={HardDrive} />
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
          <table className="w-full">
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
