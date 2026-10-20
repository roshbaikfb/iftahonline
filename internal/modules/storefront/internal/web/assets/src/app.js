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
      price.replaceChildren(opt.dataset.price);
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

countdowns();
variantPickers();
