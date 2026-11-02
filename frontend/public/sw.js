// RailPulse Service Worker: Network-first with automatic cache bust
const CACHE_NAME = 'railpulse-v2.1.0';
const ASSETS_TO_CACHE = [
  '/',
  '/text',
  '/ivr',
  '/manifest.json'
];

self.addEventListener('install', (event) => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys.map((key) => caches.delete(key)) // Purge all legacy caches
      );
    })
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  // Always network-first for fresh UI & API
  event.respondWith(
    fetch(event.request)
      .then((response) => {
        if (response.status === 200) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(event.request, clone));
        }
        return response;
      })
      .catch(() => caches.match(event.request))
  );
});
