// Package views holds the server's own pages: those of a store that
// cannot be shown.
package views

// ErrorCSS is the unavailable pages' stylesheet, after the brand's
// @font-face rules (the approved mockup).
const ErrorCSS = `*{box-sizing:border-box}` +
	`html,body{height:100%}` +
	`body{margin:0;display:flex;flex-direction:column;min-height:100vh;background:#f6f8fb;color:#0f172a;` +
	`font-family:"Iftah Sans Arabic",system-ui,"Segoe UI",Tahoma,sans-serif;line-height:1.6;-webkit-font-smoothing:antialiased}` +
	`main{flex:1;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px;padding:32px 24px;text-align:center}` +
	`.icon{display:inline-flex;align-items:center;justify-content:center;width:64px;height:64px;border-radius:20px}` +
	`.amber{background:#fef3c7;color:#b45309}.sky{background:#e0f2fe;color:#0369a1}` +
	`.slate{background:#f1f5f9;color:#334155}.red{background:#fee2e2;color:#b91c1c}` +
	`h1{margin:0;font-size:clamp(22px,4vw,28px);line-height:1.35}` +
	`p{margin:0;max-width:420px;color:#475569;font-size:15px;line-height:1.8}` +
	`.en{margin-top:6px;color:#64748b;font-size:13px}` +
	`a{color:#0f2e52}a:hover{color:#15803d}` +
	`.button{display:inline-flex;align-items:center;min-height:44px;padding:0 20px;border-radius:10px;background:#0f2e52;color:#fff;font-weight:700;text-decoration:none}` +
	`.button:hover{background:#173e6b;color:#fff}` +
	`.quiet{display:inline-flex;align-items:center;min-height:44px;font-size:14px;font-weight:600}` +
	`footer{display:flex;align-items:center;justify-content:center;gap:8px;padding:14px;border-top:1px solid #e2e8f0;color:#64748b;font-size:12px}` +
	`.mark{display:inline-flex;align-items:center;justify-content:center;width:20px;height:20px;border-radius:6px;background:#111827;color:#facc15;font-weight:700;font-size:12px}`
