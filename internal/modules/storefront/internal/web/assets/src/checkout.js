// Checkout's enhancements, loaded by the checkout page alone so the pages
// shoppers land on carry none of it (ADR-0018). app.js asks for the quote;
// this fills the totals card from it, applies the coupon and keeps the
// button in step with the payment method. The page works without it.

// fillTotals writes checkout's totals card from the quote: each amount
// on its line, the cash fee's row only when there is one, the summary's
// total, and the cash choice's note with its fee.
function fillTotals(form, card, q) {
  const bad = card.querySelector("[data-not-delivered]");
  bad.textContent = q.deliverable ? "" : q.text;
  bad.hidden = q.deliverable;
  const a = q.amounts;
  if (!a) return;
  for (const el of card.querySelectorAll("[data-amt]")) {
    const v = a[el.dataset.amt];
    if (v !== undefined) el.textContent = v;
  }
  card.querySelector("[data-cod-row]").hidden = !a.cod_fee;
  const off = card.querySelector("[data-discount-row]");
  if (off) off.hidden = !a.discount;
  const msg = form.querySelector("[data-coupon-msg]");
  if (msg) {
    msg.textContent = q.coupon ? q.coupon.message : "";
    msg.hidden = !q.coupon;
    msg.classList.toggle("ok", !!q.coupon && q.coupon.ok);
    msg.classList.toggle("bad", !!q.coupon && !q.coupon.ok);
  }
  const sum = document.querySelector("[data-sum-total]");
  if (sum) sum.textContent = a.total;
  const note = form.querySelector("[data-cod-note]");
  if (note && a.cod_note) note.textContent = a.cod_note;
}

// totals fills the card on each quote app.js receives, including one
// that came before this file ran.
function totals() {
  for (const form of document.querySelectorAll("form[data-order]")) {
    const card = form.querySelector("[data-totals]");
    if (!card) continue;
    const fill = () => {
      if (card.quote) fillTotals(form, card, card.quote);
    };
    card.addEventListener("quote", fill);
    fill();
    const apply = form.querySelector("[data-coupon-apply]");
    if (apply) apply.addEventListener("click", () => card.dispatchEvent(new CustomEvent("requote")));
  }
}

// payChoice keeps checkout's button and note in step with the payment
// method: "confirm and pay" and the secure page for a card, "confirm"
// and paying the courier for cash.
function payChoice() {
  for (const form of document.querySelectorAll("form[data-order]")) {
    const cta = form.querySelector("[data-card-label]");
    const note = form.querySelector("[data-card-text]");
    if (!cta) continue;
    const update = () => {
      const el = form.querySelector('[name="pay_method"]:checked');
      const card = !!el && el.value === "card";
      cta.textContent = card ? cta.dataset.cardLabel : cta.dataset.codLabel;
      if (note) note.querySelector("[data-secure-text]").textContent = card ? note.dataset.cardText : note.dataset.codText;
    };
    for (const el of form.querySelectorAll('[name="pay_method"]')) el.addEventListener("change", update);
  }
}

totals();
payChoice();
