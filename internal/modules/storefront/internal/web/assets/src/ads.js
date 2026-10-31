// Google AdSense in free stores (ADR-0062): loaded only on pages with a
// unit, only once the shopper nears one, and non-personalized (approved:
// no tracking of the shopper, so no consent dialog). The publisher comes
// from this script's own tag; the units are the page's <ins> elements.

const me = document.querySelector("script[data-client]"); // a module has no currentScript
const client = me && me.dataset.client;
let loaded = false;

function load() {
  if (loaded || !client) return;
  loaded = true;
  const q = (window.adsbygoogle = window.adsbygoogle || []);
  q.requestNonPersonalizedAds = 1;
  const s = document.createElement("script");
  s.async = true;
  s.crossOrigin = "anonymous";
  s.src = "https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client=" + encodeURIComponent(client);
  document.head.appendChild(s);
}

function fill(ins) {
  load();
  (window.adsbygoogle = window.adsbygoogle || []).push({});
  ins.dataset.filled = "1";
}

const units = document.querySelectorAll("ins.adsbygoogle");
if ("IntersectionObserver" in window) {
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting && !e.target.dataset.filled) {
          io.unobserve(e.target);
          fill(e.target);
        }
      }
    },
    { rootMargin: "400px 0px" },
  );
  units.forEach((u) => io.observe(u));
} else {
  units.forEach(fill);
}
