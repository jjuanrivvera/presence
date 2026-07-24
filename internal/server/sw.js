// plexus PWA service worker: keep the app installable/offline-capable, but never let the
// cache mask live data or a fresh deploy. The shell HTML is fetched network-first so a new
// /ui (e.g. a changed attach link) propagates without a manual cache bump; only the static
// assets (icon, manifest) are cache-first. Bump CACHE on breaking shell changes.
const CACHE = "plexus-shell-v3";
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
  // Live or proxied endpoints always hit the network — never cache the Plexus view, the
  // login, or a session's web terminal.
  if (url.pathname === "/list" || url.pathname === "/login" || url.pathname.startsWith("/attach/")) return;
  // Shell HTML: network-first so deploys show up immediately; fall back to cache offline.
  if (url.pathname === "/ui" || url.pathname === "/") {
    e.respondWith(
      fetch(e.request)
        .then((r) => { const copy = r.clone(); caches.open(CACHE).then((c) => c.put("/ui", copy)); return r; })
        .catch(() => caches.match("/ui"))
    );
    return;
  }
  // Static assets: cache-first.
  e.respondWith(caches.match(e.request).then((r) => r || fetch(e.request)));
});
