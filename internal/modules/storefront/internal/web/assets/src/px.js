// The store's ad pixels (ADR-0043), on stores that connected any. The
// page names them in a JSON data block (#px): each provider's public
// IDs, whether to ask the shopper first, and the conversions to report
// with the event_id the server also sends. This loads each provider's
// official tag after the page has loaded, and only after consent where
// the store asks for it. No merchant script ever runs.

const CONSENT = "px-consent"; // "yes" or "no", remembered on this device
const SENT = "px-sent"; // event_ids already reported (a reload sends none twice)

function read() {
  const el = document.getElementById("px");
  if (!el) return null;
  try {
    return JSON.parse(el.textContent);
  } catch {
    return null;
  }
}

function stored(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null; // storage blocked: ask again next time
  }
}

function store(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // storage blocked: the choice holds for this page only
  }
}

function load(src) {
  const s = document.createElement("script");
  s.async = true;
  s.src = src;
  document.head.append(s);
}

// value turns minor units into the decimal amount providers expect.
const value = (e) => e.v / 10 ** e.u;

// Each provider: start(pixel) loads its tag; track(pixel, event) reports
// one conversion, with the event_id where the provider deduplicates by
// it against its server API.
const providers = {
  meta: {
    start(p) {
      if (!window.fbq) {
        const f = (window.fbq = function (...args) {
          if (f.callMethod) f.callMethod(...args);
          else f.queue.push(args);
        });
        window._fbq = f;
        f.push = f;
        f.loaded = true;
        f.version = "2.0";
        f.queue = [];
        load("https://connect.facebook.net/en_US/fbevents.js");
      }
      window.fbq("init", p.id);
      window.fbq("track", "PageView");
    },
    track(p, e) {
      const data = { value: value(e), currency: e.c, content_ids: e.ids || [], content_type: "product" };
      window.fbq("trackSingle", p.id, e.n, data, e.id ? { eventID: e.id } : undefined);
    },
  },
  tiktok: {
    names: { ViewContent: "ViewContent", AddToCart: "AddToCart", InitiateCheckout: "InitiateCheckout", Purchase: "CompletePayment" },
    start(p) {
      if (!window.ttq) {
        const q = (window.ttq = []);
        window.TiktokAnalyticsObject = "ttq";
        for (const m of ["page", "track", "identify", "instances", "debug", "on", "off", "once", "ready", "alias", "group", "enableCookie", "disableCookie"]) {
          q[m] = (...args) => q.push([m, ...args]);
        }
        q.load = (id) => {
          q._i = q._i || {};
          q._i[id] = [];
          q._i[id]._u = "https://analytics.tiktok.com/i18n/pixel/events.js";
          q._t = q._t || {};
          q._t[id] = Date.now();
          q._o = q._o || {};
          q._o[id] = {};
          load(`https://analytics.tiktok.com/i18n/pixel/events.js?sdkid=${encodeURIComponent(id)}&lib=ttq`);
        };
      }
      window.ttq.load(p.id);
      window.ttq.page();
    },
    track(p, e) {
      const data = { value: value(e), currency: e.c, contents: (e.ids || []).map((id) => ({ content_id: id })), content_type: "product" };
      window.ttq.track(this.names[e.n], data, e.id ? { event_id: e.id } : undefined);
    },
  },
  snap: {
    names: { ViewContent: "VIEW_CONTENT", AddToCart: "ADD_CART", InitiateCheckout: "START_CHECKOUT", Purchase: "PURCHASE" },
    start(p) {
      if (!window.snaptr) {
        const a = (window.snaptr = function (...args) {
          if (a.handleRequest) a.handleRequest(...args);
          else a.queue.push(args);
        });
        a.queue = [];
        load("https://sc-static.net/scevent.min.js");
      }
      window.snaptr("init", p.id, {});
      window.snaptr("track", "PAGE_VIEW");
    },
    track(p, e) {
      const data = { price: value(e), currency: e.c, item_ids: e.ids || [] };
      if (e.id) data.client_dedup_id = e.id;
      if (e.ref) data.transaction_id = e.ref;
      window.snaptr("track", this.names[e.n], data);
    },
  },
  google_ads: {
    start(p) {
      gtag(p.id);
    },
    track(p, e) {
      if (e.n !== "Purchase") return; // the purchase conversion is the one the label names
      window.gtag("event", "conversion", { send_to: `${p.id}/${p.l}`, value: value(e), currency: e.c, transaction_id: e.ref });
    },
  },
  ga4: {
    names: { ViewContent: "view_item", AddToCart: "add_to_cart", InitiateCheckout: "begin_checkout", Purchase: "purchase" },
    start(p) {
      gtag(p.id);
    },
    track(p, e) {
      const data = { send_to: p.id, value: value(e), currency: e.c, items: (e.ids || []).map((id) => ({ item_id: id })) };
      if (e.ref) data.transaction_id = e.ref;
      window.gtag("event", this.names[e.n], data);
    },
  },
};

// gtag loads Google's tag once (Google Ads and GA4 share it) and
// configures id.
function gtag(id) {
  if (!window.gtag) {
    window.dataLayer = window.dataLayer || [];
    window.gtag = function () {
      // gtag reads the arguments object itself, not an array
      window.dataLayer.push(arguments); // eslint-disable-line prefer-rest-params
    };
    window.gtag("js", new Date());
    load(`https://www.googletagmanager.com/gtag/js?id=${encodeURIComponent(id)}`);
  }
  window.gtag("config", id);
}

function start(cfg) {
  const pixels = cfg.p.filter((p) => providers[p.k]);
  for (const p of pixels) {
    try {
      providers[p.k].start(p);
    } catch {
      // one provider's failure leaves the others measuring
    }
  }
  const sent = (stored(SENT) || "").split(" ").filter(Boolean);
  for (const e of cfg.ev || []) {
    if (e.id && sent.includes(e.id)) continue;
    for (const p of pixels) {
      try {
        providers[p.k].track(p, e);
      } catch {
        // as above
      }
    }
    if (e.id) sent.push(e.id);
  }
  store(SENT, sent.slice(-20).join(" "));
}

// later runs fn once the page has loaded and the browser is idle, so the
// tags never compete with the page's own first paint.
function later(fn) {
  const idle = () => (window.requestIdleCallback ? requestIdleCallback(fn, { timeout: 3000 }) : setTimeout(fn, 1));
  if (document.readyState === "complete") idle();
  else addEventListener("load", idle, { once: true });
}

function ask(cfg) {
  const box = document.querySelector("[data-consent]");
  if (!box) return;
  box.hidden = false;
  box.querySelector("[data-consent-yes]").addEventListener("click", () => {
    store(CONSENT, "yes");
    box.hidden = true;
    later(() => start(cfg));
  });
  box.querySelector("[data-consent-no]").addEventListener("click", () => {
    store(CONSENT, "no");
    box.hidden = true;
  });
}

const cfg = read();
if (cfg && Array.isArray(cfg.p)) {
  const choice = stored(CONSENT);
  if (!cfg.ask || choice === "yes") later(() => start(cfg));
  else if (choice !== "no") ask(cfg);
}
