// Cross-origin isolation for the debugger, which needs SharedArrayBuffer.
// GitHub Pages cannot send the headers it requires, so this service worker
// adds them (Cross-Origin-Opener-Policy and Cross-Origin-Embedder-Policy)
// to every response of the playground, all of which are same-origin. The
// page registers it and reloads once; afterwards it is isolated.
if (typeof window === "undefined") {
  // The service worker.
  self.addEventListener("install", () => self.skipWaiting());
  self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));
  self.addEventListener("fetch", (e) => {
    const req = e.request;
    if (req.cache === "only-if-cached" && req.mode !== "same-origin") return;
    e.respondWith(
      fetch(req).then((res) => {
        if (res.status === 0) return res;
        const headers = new Headers(res.headers);
        headers.set("Cross-Origin-Opener-Policy", "same-origin");
        headers.set("Cross-Origin-Embedder-Policy", "require-corp");
        headers.set("Cross-Origin-Resource-Policy", "same-origin");
        return new Response(res.body, { status: res.status, statusText: res.statusText, headers });
      }),
    );
  });
} else if (!window.crossOriginIsolated && window.isSecureContext && "serviceWorker" in navigator) {
  // The page: register the worker and reload once it controls the page.
  const key = "starc-coi-reload";
  navigator.serviceWorker.register(document.currentScript.src).then((reg) => {
    const reload = () => {
      if (sessionStorage.getItem(key)) return; // not isolated after all: give up
      sessionStorage.setItem(key, "1");
      location.reload();
    };
    if (navigator.serviceWorker.controller) reload();
    else navigator.serviceWorker.addEventListener("controllerchange", reload);
  });
} else if (window.crossOriginIsolated) {
  sessionStorage.removeItem("starc-coi-reload");
}
