// Renders the <pre class="mermaid"> sequence diagrams on pages that have
// them. Loaded only by those pages, after i18n.js and their own scripts.
//
// Mermaid comes from a pinned jsdelivr CDN build — the single sanctioned
// exception to this repo's no-dependency rule (KAS-2338 decision). No npm,
// no build step: the diagram source sits readable in the page markup, and
// if the CDN is unreachable the <pre> simply stays as monospaced mermaid
// source — degraded but honest, never a blank box.
//
// Each <pre> holds the English source; a <template class="mermaid-id">
// immediately after it holds the Indonesian one. We pick by lang() (from
// i18n.js) and re-render on the "langchange" event applyI18n() dispatches.
// A theme switch also re-renders: mermaid bakes colors into the SVG.
(async () => {
  const pres = [...document.querySelectorAll("pre.mermaid")];
  if (!pres.length) return;
  for (const p of pres) {
    p.dataset.srcEn = p.textContent;
    const t = p.nextElementSibling;
    p.dataset.srcId =
      t?.matches("template.mermaid-id") ? t.content.textContent : p.textContent;
  }
  const src = (p) => (lang() === "id" ? p.dataset.srcId : p.dataset.srcEn);

  // Show the current language's source — the CDN-failure fallback, and the
  // reset mermaid.run() re-parses from.
  function showSource() {
    for (const p of pres) {
      p.removeAttribute("data-processed"); // let mermaid.run pick it up again
      p.textContent = src(p);
    }
  }
  showSource();

  let mermaid;
  try {
    ({ default: mermaid } = await import(
      "https://cdn.jsdelivr.net/npm/mermaid@11.16.1/dist/mermaid.esm.min.mjs"
    ));
  } catch {
    // CDN down or blocked: keep the readable source in the right language.
    document.addEventListener("langchange", showSource);
    return;
  }

  async function render() {
    const dark = document.documentElement.dataset.theme === "dark";
    mermaid.initialize({
      startOnLoad: false,
      theme: dark ? "dark" : "neutral",
      fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif',
    });
    showSource();
    await mermaid.run({ nodes: pres });
  }

  await render();
  document.addEventListener("langchange", render);
  // The theme toggle (store.js) flips data-theme on <html>; re-render then.
  new MutationObserver(render).observe(document.documentElement, {
    attributeFilter: ["data-theme"],
  });
})();
