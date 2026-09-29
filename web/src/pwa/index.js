import { useSyncExternalStore } from 'react';
import { createPwaController } from './controller';

// App-wide singleton bound to the real browser globals. main.jsx calls
// pwa.start() in production builds only: a service worker in `vite dev`
// would fight Vite's HMR module graph.
export const pwa = createPwaController({
  win: typeof window !== 'undefined' ? window : undefined,
  nav: typeof navigator !== 'undefined' ? navigator : undefined,
});

export function usePwa() {
  return useSyncExternalStore(pwa.subscribe, pwa.getState, pwa.getState);
}
