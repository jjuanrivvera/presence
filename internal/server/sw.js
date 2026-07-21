// presence PWA service worker: cache the app shell for offline/installable, but never cache the
// live /list data — that always hits the network so the mesh view is real.
const CACHE = "presence-shell-v1";
const SHELL = ["/ui", "/manifest.json", "/icon.svg"];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (url.pathname === "/list") return; // live data → default network fetch, never cached
  e.respondWith(caches.match(e.request).then((r) => r || fetch(e.request)));
});
