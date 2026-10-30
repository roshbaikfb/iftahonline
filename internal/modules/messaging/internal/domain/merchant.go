package domain

// MerchantKind is a notice the platform sends a store's owners on
// WhatsApp, from its own number (ADR-0054). It is not a customer
// message: no quota, no message log.
type MerchantKind string

// Merchant notices (billing, ADR-0051).
const (
	MerchantPaymentFailed MerchantKind = "billing_payment_failed"   // store, invoice, amount, closing date
	MerchantSuspended     MerchantKind = "billing_store_suspended"  // store
	MerchantPaid          MerchantKind = "billing_payment_received" // store, invoice, amount
)

// MerchantTemplate is a merchant notice's approved WhatsApp template
// and how many body values it takes ({{1}}…).
func MerchantTemplate(k MerchantKind) (name string, params int, ok bool) {
	switch k {
	case MerchantPaymentFailed:
		return "billing_payment_failed_v1", 4, true
	case MerchantSuspended:
		return "billing_store_suspended_v1", 1, true
	case MerchantPaid:
		return "billing_payment_received_v1", 3, true
	}
	return "", 0, false
}

// MerchantParams are a merchant notice's body values, made fit for
// WhatsApp (one line each, bounded, never empty).
func MerchantParams(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = param(v)
	}
	return out
}
