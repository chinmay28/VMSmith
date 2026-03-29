# Running VMSmith in a Container

VMSmith can run inside a Linux container for testing and disposable lab setups.

> This is mainly useful for CI-style experiments and local testing. libvirt, KVM, and iptables all need elevated host access, so this is not a production-hardening story.

## Build

```bash
docker build -t vmsmith:dev .
```

## Run

```bash
docker run --rm -it \
  --name vmsmith \
  --privileged \
  --network host \
  -v vmsmith-data:/var/lib/vmsmith \
  -v vmsmith-libvirt:/var/lib/libvirt \
  -p 8080:8080 \
  vmsmith:dev
```

The image entrypoint starts `libvirtd` automatically if the libvirt socket is not already present, then launches:

```bash
vmsmith daemon start --config /etc/vmsmith/config.yaml
```

Open <http://localhost:8080> for the GUI.

## Notes

- The container image targets **Linux x86_64 / amd64**.
- `--privileged` is required because VMSmith needs access to libvirt, KVM, and iptables.
- `--network host` keeps NAT/libvirt behavior simple for local testing.
- Persistent state lives under `/var/lib/vmsmith` and `/var/lib/libvirt`.
- The bundled config is copied from `vmsmith.yaml.example` and rewrites the DB path to `/var/lib/vmsmith/vmsmith.db`.

## Override the command

```bash
docker run --rm -it --privileged --network host vmsmith:dev --help
docker run --rm -it --privileged --network host vmsmith:dev vm list --config /etc/vmsmith/config.yaml
```
