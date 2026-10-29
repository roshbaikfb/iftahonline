// Storefront enhancements (ADR-0018). Every page works without this
// file; it only adds the live countdown and the variant picker's price.
// Loaded as a module from our origin (CSP script-src 'self'). What only
// checkout needs is in checkout.js, loaded by that page alone.

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
      price.classList.toggle("sale", !!opt.dataset.compare);
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
  const m = document.cookie.match(/(?:^|; )cart_n=(\d+)/);
  if (!m || m[1] === "0") return;
  for (const badge of document.querySelectorAll("[data-cart-count]")) {
    badge.textContent = m[1];
    badge.hidden = false;
  }
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
      for (const name of ["governorate", "city", "variant", "qty", "pay_method", "coupon"]) {
        const el = form.elements.namedItem(name); // a radio group reads as its checked value
        if (el && el.value) body.set(name, el.value);
      }
      if (!body.get("governorate")) {
        if (!out.hasAttribute("data-totals")) out.hidden = true;
        return;
      }
      const mine = ++seq;
      try {
        const res = await fetch(out.dataset.quote, { method: "POST", body, credentials: "same-origin" });
        if (!res.ok || mine !== seq) return;
        const q = await res.json();
        if (out.hasAttribute("data-totals")) {
          // checkout.js fills the totals card: from the event, or from
          // out.quote if it loads after the answer.
          out.quote = q;
          out.dispatchEvent(new CustomEvent("quote"));
          return;
        }
        out.textContent = q.text;
        out.hidden = !q.text;
        out.classList.toggle("bad", !q.deliverable);
      } catch {
        // offline or busy: the order still shows its total after placing
      }
    };
    for (const name of ["governorate", "city", "variant", "qty", "pay_method", "coupon"]) {
      for (const el of form.querySelectorAll(`[name="${name}"]`)) el.addEventListener("change", update);
    }
    out.addEventListener("requote", update);
    update();
  }
}

// formatMoney writes minor units the way the server's Locale.Price does:
// Western digits, thousands separators, decimals only when there are any,
// the symbol after the amount in Arabic and before it in English.
function formatMoney(minor, f) {
  const div = 10 ** f.units;
  const neg = minor < 0;
  const abs = Math.abs(minor);
  let s = String(Math.floor(abs / div)).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const frac = abs % div;
  if (frac) s += "." + String(frac).padStart(f.units, "0");
  if (neg) s = "-" + s;
  return f.after ? `${s} ${f.sym}` : `${f.sym} ${s}`;
}

// productForms follows the shopper's choice on the product page: the
// chosen value beside each option, which values cannot be bought with the
// others chosen, the price, saving and "only N left" lines, the buttons
// and their total, and the WhatsApp message. The form itself posts the
// product and its choices, so all of this is a convenience.
function productForms() {
  for (const form of document.querySelectorAll("form[data-product]")) {
    let variants, money;
    try {
      variants = JSON.parse(form.dataset.variants);
      money = JSON.parse(form.dataset.money);
    } catch {
      continue;
    }
    const n = form.querySelectorAll("fieldset.opt").length;
    const qty = form.querySelector("[name=qty]");
    const hidden = form.querySelector("[data-variant]");
    const adds = document.querySelectorAll("[data-add]");
    const total = document.querySelector("[data-total]");
    const wa = document.querySelector("[data-wa]");
    const addLabel = form.dataset.addLabel;
    const soldLabel = form.dataset.soldLabel;
    const chosen = () => {
      const vals = [];
      for (let i = 0; i < n; i++) {
        const el = form.querySelector(`[name="o${i}"]:checked`);
        vals.push(el ? el.value : null);
      }
      return vals;
    };
    const find = (vals) => variants.find((v) => v.values.length === vals.length && v.values.every((x, i) => x === vals[i]));
    const setLine = (sel, text) => {
      const el = document.querySelector(sel);
      if (!el) return;
      el.textContent = text || "";
      el.hidden = !text;
    };
    const update = () => {
      const vals = chosen();
      const v = n === 0 ? variants[0] : find(vals);
      for (let i = 0; i < n; i++) {
        const out = form.querySelector(`[data-chosen="${i}"]`);
        if (out) out.textContent = vals[i] || "";
      }
      for (const input of form.querySelectorAll("[data-opt]")) {
        const test = vals.slice();
        test[Number(input.dataset.opt)] = input.value;
        const m = find(test);
        input.closest("label").classList.toggle("out", !m || !m.available);
      }
      const q = Math.max(1, Math.min(20, Number(qty.value) || 1));
      const ok = !!v && v.available;
      if (v) {
        const now = document.querySelector("[data-now]");
        if (now) {
          now.textContent = v.price;
          now.classList.toggle("sale", !!v.compare);
        }
        setLine("[data-was]", v.compare);
        setLine("[data-save]", v.save);
        setLine("[data-left]", v.left);
        const pct = document.querySelector("[data-pct]");
        if (pct) pct.hidden = !v.pct;
      }
      if (hidden) {
        hidden.value = v ? v.id : "";
        hidden.disabled = !v;
      }
      for (const b of adds) {
        b.disabled = !ok;
        if (b !== total) b.textContent = ok ? addLabel : soldLabel;
      }
      if (total) total.textContent = ok ? `${addLabel} · ${formatMoney(v.amount * q, money)}` : soldLabel;
      if (wa && v) {
        const url = new URL(wa.href);
        const lines = (url.searchParams.get("text") || "").split("\n");
        const what = form.closest(".pd-info").querySelector("h1").textContent + (v.values.length ? ` (${v.values.join(" / ")})` : "");
        lines[0] = lines[0].replace(/:.*$/, `: ${what} × ${q}`);
        url.searchParams.set("text", lines.join("\n"));
        wa.href = url.toString();
      }
      document.dispatchEvent(new CustomEvent("product:choice", { detail: { variant: v ? v.id : "", qty: q } }));
    };
    for (const b of form.querySelectorAll("[data-step]")) {
      b.hidden = false;
      b.addEventListener("click", () => {
        qty.value = String(Math.max(1, Math.min(20, (Number(qty.value) || 1) + Number(b.dataset.step))));
        update();
      });
    }
    form.addEventListener("change", update);
    qty.addEventListener("input", update);
    update();
  }
}

// deliveryQuote fills the product page's delivery box once the shopper
// picks a governorate: the same quote checkout uses, for the variant and
// quantity chosen. Only the governorate and the choice are sent.
function deliveryQuote() {
  const form = document.querySelector("form[data-delivery]");
  if (!form) return;
  const select = form.querySelector("select");
  const line = form.querySelector("[data-deliv-line]");
  const ship = form.querySelector("[data-ship-line]");
  let choice = { variant: "", qty: 1 };
  let seq = 0;
  const update = async () => {
    if (!select.value || !choice.variant) {
      line.hidden = ship.hidden = true;
      return;
    }
    const body = new URLSearchParams({ governorate: select.value, variant: choice.variant, qty: String(choice.qty) });
    const mine = ++seq;
    try {
      const res = await fetch(form.dataset.delivery, { method: "POST", body, credentials: "same-origin" });
      if (!res.ok || mine !== seq) return;
      const q = await res.json();
      line.textContent = q.delivery || "";
      line.hidden = !q.delivery;
      line.classList.toggle("bad", !q.deliverable);
      ship.textContent = q.shipping || "";
      ship.hidden = !q.shipping;
    } catch {
      // offline or busy: checkout still shows the charges
    }
  };
  form.addEventListener("submit", (e) => e.preventDefault());
  select.addEventListener("change", update);
  document.addEventListener("product:choice", (e) => {
    choice = e.detail;
    update();
  });
}

// share opens the phone's share sheet, or copies the link where there is
// none (desktop browsers) and says so.
function share() {
  const toast = document.querySelector("[data-toast]");
  for (const b of document.querySelectorAll("[data-share]")) {
    if (!navigator.share && !navigator.clipboard) continue;
    b.hidden = false;
    b.addEventListener("click", async () => {
      const url = b.dataset.share;
      try {
        if (navigator.share) {
          await navigator.share({ title: b.dataset.title, url });
          return;
        }
        await navigator.clipboard.writeText(url);
        if (toast) {
          toast.hidden = false;
          setTimeout(() => { toast.hidden = true; }, 2500);
        }
      } catch {
        // the shopper closed the sheet
      }
    });
  }
}

// galleries keep the dots and thumbnails on the picture in view as the
// shopper swipes.
function galleries() {
  for (const track of document.querySelectorAll("[data-track]")) {
    const root = track.parentElement;
    const marks = [...root.querySelectorAll(".dots span")];
    const thumbs = [...root.querySelectorAll(".thumbs a")];
    if (!marks.length || !("IntersectionObserver" in window)) continue;
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        const i = [...track.children].indexOf(e.target);
        marks.forEach((m, j) => m.classList.toggle("on", i === j));
        thumbs.forEach((t, j) => t.classList.toggle("on", i === j));
      }
    }, { root: track, threshold: 0.6 });
    for (const slide of track.children) io.observe(slide);
  }
}

// visitSource remembers the campaign that brought the shopper (the UTM
// parameters and ad click IDs of the landing URL) for 30 days, for the
// order (ADR-0043): pages are cached, so the server never sees the URL
// a shopper landed on. A later campaign's link replaces it.
function visitSource() {
  const q = new URLSearchParams(location.search);
  const keep = new URLSearchParams();
  for (const k of ["utm_source", "utm_medium", "utm_campaign", "utm_content", "utm_term", "gclid", "ttclid", "ScCid", "fbclid"]) {
    const v = q.get(k);
    if (v) keep.set(k, v.slice(0, 255));
  }
  if (!keep.toString()) return;
  if (keep.has("fbclid")) keep.set("ts", String(Date.now())); // Meta's fbc names the click's time
  const secure = location.protocol === "https:" ? "; Secure" : "";
  document.cookie = `ift_src=${encodeURIComponent(keep.toString())}; Max-Age=2592000; Path=/; SameSite=Lax${secure}`;
}

visitSource();
countdowns();
variantPickers();
cartBadge();
orderForms();
orderQuotes();
productForms();
deliveryQuote();
share();
galleries();
