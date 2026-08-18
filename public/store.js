// Shared script for the shop pages. Vanilla JS, no build step.
// Loads after i18n.js, so t() and lang() are available.

// DISPLAY-ONLY catalog. The authoritative price table lives in main.go on
// the server — when the buyer clicks "Beli", the browser sends only the
// item_id, and the server looks the price up itself. If you edit a price
// here in devtools, you change the label, not what anyone pays.
const ITEMS = [
  { id: "tee-batik",     name: "Batik Print Tee",       price: 189000 },
  { id: "tee-plain",     name: "Heavyweight Plain Tee", price: 149000 },
  { id: "hoodie-kasera", name: "Kasera Threads Hoodie", price: 429000 },
  { id: "cap-canvas",    name: "Canvas Cap",            price: 119000 },
  { id: "socks-3pack",   name: "Socks (3 pack)",        price: 89000 },
  { id: "tote-denim",    name: "Denim Tote Bag",        price: 159000 },
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
