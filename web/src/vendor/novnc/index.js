// Thin ESM wrapper around the vendored noVNC RFB bundle. The upstream npm
// artifact is Babel-compiled CJS, so the esbuild ESM conversion exposes the
// module's exports object as the default export; unwrap the inner `.default`
// (the RFB class) so callers can `import RFB from '../vendor/novnc'`.
import rfbModule from './rfb.js';

const RFB = rfbModule && rfbModule.__esModule ? rfbModule.default : rfbModule;

export default RFB;
