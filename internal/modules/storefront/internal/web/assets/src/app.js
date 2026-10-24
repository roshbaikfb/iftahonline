// Storefront enhancements (ADR-0018). Every page works without this
// file; it only adds the live countdown and the variant picker's price.
// Loaded as a module from our origin (CSP script-src 'self').

const pad = (n) => String(n).padStart(2, "0");

function countdowns() {
  const els = document.querySelectorAll("[data-countdown]");
  if (!els.length) return;
  const tick = () => {
    const now = Date.now();
    for (const el of els) {
      const left = Math.max(0, Date.parse(el.dataset.countdown) - now);
      if (left === 0 && el.dataset.hide === "1") {
        el.hidden = true;
        continue;
      }
      const s = Math.floor(left / 1000);
      const parts = [Math.floor(s / 86400), Math.floor(s / 3600) % 24, Math.floor(s / 60) % 60, s % 60];
      el.querySelectorAll("[data-unit]").forEach((u, i) => {
        u.textContent = pad(parts[i]);
      });
    }
  };
  tick();
  setInterval(tick, 1000);
}

function variantPickers() {
  for (const form of document.querySelectorAll("form[data-buy]")) {
    const select = form.querySelector("select[name=variant]");
    const price = form.querySelector("[data-price]");
    const button = form.querySelector("button[type=submit]");
    if (!select || !price || !button) continue;
    const update = () => {
      const opt = select.selectedOptions[0];
      if (!opt) return;
      price.replaceChildren(opt.dataset.amount);
      if (opt.dataset.compare) {
        const s = document.createElement("s");
        s.textContent = opt.dataset.compare;
        price.append(" ", s);
      }
      button.disabled = opt.disabled;
    };
    select.addEventListener("change", update);
    update();
  }
}

// cartBadge shows how many units the cart holds, from the cart_n cookie
// the server sets on every cart change: pages are cached for everyone,
// so the count cannot be in them, and asking the origin on every view
// would undo the edge cache.
function cartBadge() {
  const badge = document.querySelector("[data-cart-count]");
  const m = document.cookie.match(/(?:^|; )cart_n=(\d+)/);
  if (!badge || !m || m[1] === "0") return;
  badge.textContent = m[1];
  badge.hidden = false;
}

// orderForms gives each one-step order form an idempotency key, so a
// double submit or a retry on a weak network places one order
// (ADR-0022): the page is cached for everyone, so the key cannot come
// from the server. It also stops a second click while the first is sent.
function orderForms() {
  for (const form of document.querySelectorAll("form[data-order]")) {
    const key = form.querySelector("[data-order-key]");
    if (key && !key.value && crypto.randomUUID) key.value = crypto.randomUUID();
    form.addEventListener("submit", () => {
      const button = form.querySelector("button[type=submit]");
      if (button) setTimeout(() => { button.disabled = true; }, 0);
    });
  }
}

// orderQuotes shows what the order costs delivered (shipping, COD fee,
// total) once the shopper picks a governorate (ADR-0025). Only the
// address, the product and the payment method are sent, never the name
// or phone; the server answers with the line to show, in the page's
// language.
function orderQuotes() {
  for (const form of document.querySelectorAll("form[data-order]")) {
    const out = form.querySelector("[data-quote]");
    if (!out) continue;
    let seq = 0;
    const update = async () => {
      const body = new URLSearchParams();
      for (const name of ["governorate", "city", "variant", "qty", "pay_method"]) {
        const el = form.elements.namedItem(name); // a radio group reads as its checked value
        if (el && el.value) body.set(name, el.value);
      }
      if (!body.get("governorate")) { out.hidden = true; return; }
      const mine = ++seq;
      try {
        const res = await fetch(out.dataset.quote, { method: "POST", body, credentials: "same-origin" });
        if (!res.ok || mine !== seq) return;
        const q = await res.json();
        out.textContent = q.text;
        out.hidden = !q.text;
        out.classList.toggle("bad", !q.deliverable);
      } catch {
        // offline or busy: the order still shows its total after placing
      }
    };
    for (const name of ["governorate", "city", "variant", "qty", "pay_method"]) {
      for (const el of form.querySelectorAll(`[name="${name}"]`)) el.addEventListener("change", update);
    }
    update();
  }
}

countdowns();
variantPickers();
cartBadge();
orderForms();
orderQuotes();
