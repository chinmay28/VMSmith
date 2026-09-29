// Shared picker for VM disk storage locations (GET /host/storage-locations).
// Used by the New VM modal (placement) and the VM detail "Move disk" modal.

export function formatLocationBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let current = value;
  let index = 0;
  while (current >= 1024 && index < units.length - 1) {
    current /= 1024;
    index += 1;
  }
  const decimals = current >= 10 || index === 0 ? 0 : 1;
  return `${current.toFixed(decimals)} ${units[index]}`;
}

export function storageLocationLabel(loc) {
  const name = loc.default ? `${loc.name} (default)` : loc.name;
  if (!loc.available) return `${name} — unavailable`;
  return `${name} — ${formatLocationBytes(loc.free_bytes)} free of ${formatLocationBytes(loc.total_bytes)}`;
}

// value is a location name ('' is treated as 'default'). Unavailable
// locations (missing directory / unmounted drive) and the name in `exclude`
// are shown but disabled so operators can see why they can't pick them.
export function StorageLocationSelect({ locations, value, onChange, exclude = '', testId = 'input-storage-location' }) {
  const current = value || 'default';
  return (
    <select className="input" value={current} onChange={e => onChange(e.target.value)} data-testid={testId}>
      {locations.map(loc => (
        <option
          key={loc.name}
          value={loc.name}
          disabled={!loc.available || loc.name === exclude}
          title={loc.error || loc.path}
        >
          {storageLocationLabel(loc)}{loc.name === exclude ? ' (current)' : ''}
        </option>
      ))}
    </select>
  );
}
