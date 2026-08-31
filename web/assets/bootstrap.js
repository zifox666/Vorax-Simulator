(() => {
  let started = false, fallbackStarted = false;
  const boot = () => { if (!window.Vue || started) return; started = true; import('/assets/app.mjs').catch(fail); };
  const fail = () => { document.getElementById('boot-status').textContent = '界面加载失败，请检查连接后刷新页面。'; };
  const fallback = () => { if (started || fallbackStarted) return; fallbackStarted = true; const s = document.createElement('script'); s.src = '/assets/vue.global.prod.js'; s.onload = boot; s.onerror = fail; document.head.append(s); };
  const script = document.createElement('script');
  script.src = 'https://cdn.jsdelivr.net/npm/vue@3.5.13/dist/vue.global.prod.js';
  script.onload = boot; script.onerror = fallback; document.head.append(script);
  setTimeout(fallback, 2000);
})();
