export function safeArray(value) {
  return Array.isArray(value) ? value : [];
}

export function listData(response) {
  if (Array.isArray(response)) return response;
  return safeArray(response?.data);
}

export function normalizeSpec(spec) {
  return spec && typeof spec === 'object' ? spec : {};
}

export function normalizeVM(vm, index) {
  if (!vm || typeof vm !== 'object') return null;
  return {
    ...vm,
    id: vm.id || `invalid-vm-${index}`,
    name: vm.name || 'unnamed-vm',
    state: vm.state || 'unknown',
    tags: safeArray(vm.tags),
    spec: normalizeSpec(vm.spec),
  };
}

export function normalizeVMList(response) {
  return listData(response)
    .map(normalizeVM)
    .filter(Boolean);
}
