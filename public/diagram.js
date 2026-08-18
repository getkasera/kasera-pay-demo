// Renders the <pre class="mermaid"> sequence diagrams on pages that have
// them. Loaded only by those pages, after their own scripts.
//
// Mermaid comes from a pinned jsdelivr CDN build — the single sanctioned
// exception to this repo's no-dependency rule (KAS-2338 decision). No npm,
// no build step: the diagram source sits readable in the page markup, and
// if the CDN is unreachable the <pre> simply stays as monospaced mermaid
// source — degraded but honest, never a blank box.
//
// Labels inside the diagrams are deliberately language-neutral (Buyer,
// Shop backend, Kasera — terms Indonesian developer docs use as-is), so a
// language switch needs no re-render. A theme switch does: mermaid bakes
// colors into the SVG, so we keep the original source and re-run.
(async () => {
  const pres = [...document.querySelectorAll("pre.mermaid")];
  if (!pres.length) return;
  for (const p of pres) p.dataset.src = p.textContent; // keep source for re-renders

  let mermaid;
  try {
    ({ default: mermaid } = await import(
      "https://cdn.jsdelivr.net/npm/mermaid@11.16.1/dist/mermaid.esm.min.mjs"
    ));
  } catch {
    return; // CDN down or blocked: the readable source text stays put
  }

  async function render() {
    const dark = document.documentElement.dataset.theme === "dark";
    mermaid.initialize({
      startOnLoad: false,
      theme: dark ? "dark" : "neutral",
      fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif',
    });
    for (const p of pres) {
      p.removeAttribute("data-processed"); // let mermaid.run pick it up again
      p.textContent = p.dataset.src;
    }
    await mermaid.run({ nodes: pres });
  }

  await render();
  // The theme toggle (store.js) flips data-theme on <html>; re-render then.
  new MutationObserver(render).observe(document.documentElement, {
    attributeFilter: ["data-theme"],
  });
})();
