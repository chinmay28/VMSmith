import { useState } from 'react';

// Shared picker for VM disk storage locations (GET /host/storage-locations).
// Used by the New VM modal (placement) and the VM detail "Move disk" modal.
//
// Locations come in three kinds: the implicit `default` (storage.base_dir),
// named `configured` entries (storage.disk_locations), and `discovered`
// directories (allowed roots such as /mnt or /var, and mounted drives under
// them) that are named by their absolute path. Any other existing directory
// under storage.disk_location_roots can be typed in as a custom path.

export const CUSTOM_LOCATION = '__custom__';

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
  let name = loc.name;
  if (loc.default) name = `${loc.name} (default)`;
  else if (loc.kind !== 'discovered' && loc.path) name = `${loc.name} (${loc.path})`;
  if (!loc.available) return `${name} — unavailable`;
  const warn = loc.warning ? ' ⚠' : '';
  return `${name} — ${formatLocationBytes(loc.free_bytes)} free of ${formatLocationBytes(loc.total_bytes)}${warn}`;
}

// isPathLocation reports whether a location value is an absolute path
// (an ad-hoc location) rather than a configured name.
export function isPathLocation(value) {
  return typeof value === 'string' && value.trim().startsWith('/');
}

// value is a location name or absolute path ('' is treated as 'default').
// Unavailable locations (missing directory / unmounted drive) and the value
// in `exclude` are shown but disabled so operators can see why they can't
// pick them. Choosing "Custom path…" reveals a text input whose contents
// become the value.
export function StorageLocationSelect({ locations, value, onChange, exclude = '', testId = 'input-storage-location' }) {
  const current = value || 'default';
  const known = locations.some(l => l.name === current);
  const [custom, setCustom] = useState(!known && isPathLocation(current));
  const selected = custom ? CUSTOM_LOCATION : current;
  const selectedLoc = custom ? null : locations.find(l => l.name === current);

  const configured = locations.filter(l => l.kind !== 'discovered');
  const discovered = locations.filter(l => l.kind === 'discovered');

  const option = loc => (
    <option
      key={loc.name}
      value={loc.name}
      disabled={!loc.available || loc.name === exclude}
      title={loc.warning || loc.error || loc.path}
    >
      {storageLocationLabel(loc)}{loc.name === exclude ? ' (current)' : ''}
    </option>
  );

  const handleSelect = (next) => {
    if (next === CUSTOM_LOCATION) {
      setCustom(true);
      onChange(isPathLocation(value) ? value : '');
      return;
    }
    setCustom(false);
    onChange(next);
  };

  return (
    <div className="space-y-2">
      <select className="input" value={selected} onChange={e => handleSelect(e.target.value)} data-testid={testId}>
        {discovered.length > 0 ? (
          <>
            <optgroup label="Configured locations">{configured.map(option)}</optgroup>
            <optgroup label="Drives & directories">{discovered.map(option)}</optgroup>
          </>
        ) : configured.map(option)}
        <option value={CUSTOM_LOCATION}>Custom path…</option>
      </select>
      {custom && (
        <input
          className="input font-mono"
          placeholder="/mnt/nvme/vms"
          value={value || ''}
          onChange={e => onChange(e.target.value)}
          data-testid={`${testId}-custom`}
          autoFocus
        />
      )}
      {custom && (
        <p className="text-[11px] text-steel-500">
          Any existing directory under the daemon&apos;s <span className="font-mono">storage.disk_location_roots</span>
          {' '}(by default /mnt, /media, /var, /srv, /opt, /data, /home). System directories are refused.
        </p>
      )}
      {selectedLoc?.warning && (
        <p className="text-[11px] text-amber-400" data-testid={`${testId}-warning`}>{selectedLoc.warning}</p>
      )}
    </div>
  );
}
