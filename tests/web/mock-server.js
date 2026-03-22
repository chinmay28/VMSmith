const http = require("http");
const fs = require("fs");
const path = require("path");

let vmCounter = 0;
const vms = new Map();
const snapshots = new Map();
const images = new Map();
const portForwards = new Map();

function seed() {
  const vm1 = createVM({ name: "web-server", image: "ubuntu-22.04", cpus: 2, ram_mb: 4096, disk_gb: 40 });
  vm1.ip = "192.168.100.10";
  const vm2 = createVM({ name: "db-server", image: "rocky-9", cpus: 4, ram_mb: 8192, disk_gb: 100 });
  vm2.state = "stopped";
  vm2.ip = "192.168.100.11";
  snapshots.set(vm1.id, [
    { id: `${vm1.id}/before-deploy`, vm_id: vm1.id, name: "before-deploy", created_at: new Date().toISOString() },
  ]);
  images.set("img-1", {
    id: "img-1", name: "ubuntu-base", path: "/images/ubuntu-base.qcow2",
    size_bytes: 1073741824, format: "qcow2", source_vm: vm1.id, created_at: new Date().toISOString(),
  });
}

function createVM(spec) {
  vmCounter++;
  const id = `vm-${vmCounter}`;
  const vm = {
    id, name: spec.name,
    spec: { name: spec.name, image: spec.image || "ubuntu", cpus: spec.cpus || 2, ram_mb: spec.ram_mb || 2048, disk_gb: spec.disk_gb || 20, ssh_pub_key: spec.ssh_pub_key || "", default_user: spec.default_user || "", networks: spec.networks || [] },
    state: "running", ip: "", disk_path: `/var/lib/vmsmith/vms/${id}/disk.qcow2`,
    created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  };
  vms.set(id, vm);
  snapshots.set(id, snapshots.get(id) || []);
  portForwards.set(id, []);
  return vm;
}

function parseBody(req) {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (chunk) => (data += chunk));
    req.on("end", () => { try { resolve(JSON.parse(data)); } catch { resolve({}); } });
  });
}

function json(res, status, data) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(data));
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const p = url.pathname;
  const method = req.method;

  // API routes
  if (p === "/api/v1/vms" && method === "GET") return json(res, 200, [...vms.values()]);
  if (p === "/api/v1/vms" && method === "POST") {
    const spec = await parseBody(req);
    const vm = createVM(spec);
    vm.ip = `192.168.100.${10 + vmCounter}`;
    return json(res, 201, vm);
  }

  let m;
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "GET") {
    const vm = vms.get(m[1]);
    return vm ? json(res, 200, vm) : json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "DELETE") {
    vms.delete(m[1]); res.writeHead(204); return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/start$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (vm) { vm.state = "running"; return json(res, 200, { status: "started" }); }
    return json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/stop$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (vm) { vm.state = "stopped"; return json(res, 200, { status: "stopped" }); }
    return json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "GET") {
    return json(res, 200, snapshots.get(m[1]) || []);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "POST") {
    const body = await parseBody(req);
    const vmId = m[1];
    const snap = { id: `${vmId}/${body.name}`, vm_id: vmId, name: body.name, created_at: new Date().toISOString() };
    const list = snapshots.get(vmId) || [];
    list.push(snap); snapshots.set(vmId, list);
    return json(res, 201, snap);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)\/restore$/)) && method === "POST") {
    return json(res, 200, { status: "restored" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)$/)) && method === "DELETE") {
    const list = (snapshots.get(m[1]) || []).filter(s => s.name !== m[2]);
    snapshots.set(m[1], list); res.writeHead(204); return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "GET") {
    return json(res, 200, portForwards.get(m[1]) || []);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "POST") {
    const body = await parseBody(req);
    const vm = vms.get(m[1]);
    const pf = { id: `pf-${Date.now()}`, vm_id: m[1], host_port: body.host_port, guest_port: body.guest_port, guest_ip: vm?.ip || "192.168.100.10", protocol: body.protocol || "tcp" };
    const list = portForwards.get(m[1]) || []; list.push(pf); portForwards.set(m[1], list);
    return json(res, 201, pf);
  }
  if (p === "/api/v1/images" && method === "GET") return json(res, 200, [...images.values()]);
  if (p === "/api/v1/logs" && method === "GET") {
    const entries = [
      { ts: new Date().toISOString(), level: "info", source: "daemon", msg: "vmSmith daemon listening", fields: { addr: "0.0.0.0:8080" } },
      { ts: new Date().toISOString(), level: "info", source: "api", msg: "GET /api/v1/vms", fields: { status_code: "200", duration_ms: "1" } },
      { ts: new Date().toISOString(), level: "info", source: "cli", msg: "vm list", fields: {} },
      { ts: new Date().toISOString(), level: "warn", source: "daemon", msg: "port forward restore skipped", fields: { error: "iptables not available" } },
      { ts: new Date().toISOString(), level: "error", source: "api", msg: "POST /api/v1/vms", fields: { status_code: "500", duration_ms: "5" } },
    ];
    const level = url.searchParams.get("level") || "debug";
    const limit = parseInt(url.searchParams.get("limit") || "200", 10);
    const source = url.searchParams.get("source") || "";
    const levelOrder = { debug: 0, info: 1, warn: 2, error: 3 };
    const minLevel = levelOrder[level] ?? 0;
    let filtered = entries.filter(e => (levelOrder[e.level] ?? 0) >= minLevel);
    if (source) filtered = filtered.filter(e => e.source === source);
    if (filtered.length > limit) filtered = filtered.slice(filtered.length - limit);
    return json(res, 200, { entries: filtered, total: filtered.length });
  }
  if (p === "/api/v1/host/interfaces" && method === "GET") {
    return json(res, 200, [
      { name: "eth0", ips: ["10.21.100.101/24"], mac: "52:54:00:00:00:01", is_up: true, is_physical: true },
      { name: "eth1", ips: ["192.168.1.16/24"], mac: "52:54:00:00:00:02", is_up: true, is_physical: true },
    ]);
  }

  // Serve test GUI HTML
  if (!p.startsWith("/api/")) {
    const htmlPath = path.join(__dirname, "test-gui.html");
    if (fs.existsSync(htmlPath)) {
      res.writeHead(200, { "Content-Type": "text/html" });
      return res.end(fs.readFileSync(htmlPath, "utf8"));
    }
  }

  json(res, 404, { error: `not found: ${method} ${p}` });
});

seed();
const PORT = parseInt(process.env.PORT || "4173", 10);
server.listen(PORT, () => console.log(`Mock vmSmith server on http://localhost:${PORT}`));
