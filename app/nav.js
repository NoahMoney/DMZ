// DMZ nav.js — how the navigation behaves; its markup is in index.html. Five primary destinations and an overflow:
// More is a disclosure, not a menu of everything. A destination another file provides is offered only once its
// route exists, so the bar never holds a button that leads nowhere.
(function () {
  const V = window.Veil, U = V.ui, { $, $$, router } = U;
  window.addEventListener("DOMContentLoaded", () => {
    // Every script has run by now, so every route that will exist is registered. Wrap is primary: it opens Deposit &
    // withdraw (route "move", another file's) when that wrap hub exists, and the Wrap screen stays reachable from
    // More; with no hub, the primary tab is the Wrap screen itself and More does not repeat it.
    for (const t of $$(".tab[data-need]")) if (!router.handlers[t.dataset.tab]) t.remove();
    const w = $("#tabWrap"), dupe = $("#navMore .tab[data-dupe]");
    if (w && router.handlers.move) { w.dataset.tab = "move"; w.dataset.also = "wrap"; } else if (dupe) dupe.remove();
    // Launch opens the launch flow's chooser directly when this build has it (V.forms.create, checked at the click),
    // over the Launch screen. Registered before app.js binds the tabs, so it runs first and stops that binding.
    const lt = $('.tabs .tab[data-tab="launch"]');
    if (lt) lt.addEventListener("click", (e) => { if (V.forms && typeof V.forms.create === "function") { e.stopImmediatePropagation(); router.go("launch"); V.forms.create(); } });
    const btn = $("#tabMore"), box = $("#navMore");
    if (!btn || !box) return;
    const wide = () => !!(window.matchMedia && window.matchMedia("(min-width:960px)").matches);
    const set = (open) => { box.hidden = !open; btn.setAttribute("aria-expanded", String(open)); };
    btn.onclick = () => set(box.hidden);
    // More is lit while one of its destinations is on screen; in the desktop rail it then stays open, so the
    // lit item is visible, and on a phone it closes behind the navigation
    const mark = () => {
      const n = router.current && router.current.name, inside = !!n && !!box.querySelector('.tab[data-tab="' + n + '"]');
      btn.classList.toggle("on", inside); set(inside && wide());
    };
    window.addEventListener("hashchange", mark);
    document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !box.hidden && !wide()) { set(false); btn.focus(); } });
    document.addEventListener("click", (e) => { if (!box.hidden && !wide() && !box.contains(e.target) && !btn.contains(e.target)) set(false); });
    setTimeout(mark, 0);
  });
})();
