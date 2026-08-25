// One-time cleanup worker for stale localhost service workers from other apps.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => {
	event.waitUntil(
		(async () => {
			const keys = await caches.keys();
			await Promise.all(keys.map((key) => caches.delete(key)));
			await self.registration.unregister();
			const windows = await self.clients.matchAll({ type: "window" });
			windows.forEach((client) => client.navigate(client.url));
		})(),
	);
});
self.addEventListener("fetch", (event) => {
	event.respondWith(fetch(event.request, { cache: "no-store" }));
});
