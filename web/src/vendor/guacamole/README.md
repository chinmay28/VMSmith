# Vendored guacamole-common-js

- **Package:** `guacamole-common-js`
- **Version:** 1.5.0 (pinned)
- **License:** Apache-2.0 (see `LICENSE`, copied verbatim from the upstream
  package)

`guacamole.js` is the upstream ESM build (`dist/esm/guacamole-common.js`)
copied verbatim — it is already a native ES module with a default
`Guacamole` export, so no bundling step is required:

```bash
npm pack guacamole-common-js@1.5.0
tar xzf guacamole-common-js-1.5.0.tgz
cp package/dist/esm/guacamole-common.js guacamole.js
cp package/LICENSE LICENSE
```

Powers the VMConsole RDP tab (roadmap 5.6.13): `Guacamole.Client` over a
`Guacamole.WebSocketTunnel` that dials the daemon's console websocket with
`intent=rdp`, which the daemon bridges to the configured guacd instance.

To upgrade: bump the version in the commands above and update this README.
