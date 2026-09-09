// Shared script for the shop pages. Vanilla JS, no build step.
// Loads after i18n.js, so t() and lang() are available.

// DISPLAY-ONLY catalog. The authoritative price table lives in main.go on
// the server — when the buyer clicks "Beli", the browser sends only the
// item_id, and the server looks the price up itself. If you edit a price
// here in devtools, you change the label, not what anyone pays.
// Line-art product illustrations, inline SVG (our own strings, safe for
// innerHTML). One consistent stroke style, colored by the theme's accent.
const ART = {
  tee: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M28 16l-14 8 6 12 6-3v27h24V33l6 3 6-12-14-8a10 10 0 0 1-20 0Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/><path d="M32 40h12M32 47h8" stroke="var(--accent)" stroke-width="2" stroke-linecap="round"/></svg>`,
  teePlain: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M28 16l-14 8 6 12 6-3v27h24V33l6 3 6-12-14-8a10 10 0 0 1-20 0Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/></svg>`,
  hoodie: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M28 18l-14 8 6 12 6-3v25h24V35l6 3 6-12-14-8a10 10 0 0 1-20 0Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/><path d="M30 18c0 6 3.6 9 8 9s8-3 8-9" stroke="var(--accent)" stroke-width="2.4"/><path d="M28 22c4 8 6 14 10 14s6-6 10-14" stroke="var(--accent)" stroke-width="2" stroke-linecap="round"/><rect x="31" y="48" width="14" height="9" rx="2" stroke="var(--accent)" stroke-width="2"/></svg>`,
  cap: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M18 44c0-13 9-22 20-22s20 9 20 22" stroke="var(--accent)" stroke-width="2.4"/><path d="M14 44h56c2 0 3 2 1.6 3.4L64 52H18l-4-4.6C12.6 46 13 44 14 44Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/><path d="M38 22v22" stroke="var(--accent)" stroke-width="2"/></svg>`,
  socks: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M30 14h18v26l8 9a10 10 0 0 1-14 14L30 51V14Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/><path d="M30 22h18M30 28h18" stroke="var(--accent)" stroke-width="2" stroke-linecap="round"/></svg>`,
  tote: `<svg width="76" height="76" viewBox="0 0 76 76" fill="none"><path d="M20 30h36l-4 32H24l-4-32Z" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/><path d="M28 30v-6a10 10 0 0 1 20 0v6" stroke="var(--accent)" stroke-width="2.4"/><path d="M28 42h20" stroke="var(--accent)" stroke-width="2" stroke-linecap="round"/></svg>`,
};

const ITEMS = [
  { id: "tee-batik",     name: "Batik Print Tee",       price: 189000, art: ART.tee },
  { id: "hoodie-kasera", name: "Kasera Threads Hoodie", price: 429000, art: ART.hoodie },
  { id: "cap-canvas",    name: "Canvas Cap",            price: 119000, art: ART.cap },
  { id: "tote-denim",    name: "Denim Tote Bag",        price: 159000, art: ART.tote },
  { id: "tee-plain",     name: "Heavyweight Plain Tee", price: 149000, art: ART.teePlain },
  { id: "socks-3pack",   name: "Socks (3 pack)",        price: 89000,  art: ART.socks },
];

// IDR is written in whole rupiah with dot separators: Rp 189.000
function formatRupiah(n) {
  return "Rp " + Number(n).toLocaleString("id-ID");
}

// Bold the nav link that matches the current page + case, so you always
// know which of the four flows you are looking at.
function markCurrentNav() {
  const here = location.pathname.split("/").pop() + location.search;
  document.querySelectorAll("header nav a").forEach((a) => {
    if (a.getAttribute("href") === here) a.setAttribute("aria-current", "page");
  });
}
markCurrentNav();

// Theme toggle. The inline <script> in each page's <head> already set
// data-theme before first paint (saved choice wins, else the OS
// preference); this button flips it and saves the choice. The label names
// the theme you would switch TO, in the current language.
const themeBtn = document.getElementById("theme-toggle");
function paintThemeBtn() {
  const dark = document.documentElement.dataset.theme === "dark";
  themeBtn.textContent = dark ? t("theme.light") : t("theme.dark");
}
themeBtn.addEventListener("click", () => {
  const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  localStorage.setItem("theme", next);
  paintThemeBtn();
});
document.addEventListener("langchange", paintThemeBtn);
paintThemeBtn();
