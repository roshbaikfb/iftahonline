import type { components } from '../api/client';
import type { OrderStatus } from './orders';

export type RiskBand = components['schemas']['RiskBand'];
export type OrderRisk = components['schemas']['OrderRisk'];
export type OutcomeReason = components['schemas']['OutcomeReasonInput'];

// bandLabel names a customer's band (ADR-0065) as merchants read it, with
// the pill's colour.
export const bandLabel: Record<RiskBand, { label: string; cls: string }> = {
  new: { label: 'عميل جديد', cls: 'risk-new' },
  low: { label: 'موثوق', cls: 'risk-low' },
  medium: { label: 'مخاطرة متوسطة', cls: 'risk-medium' },
  high: { label: 'مخاطرة عالية', cls: 'risk-high' },
};

// takesReason are the moves that ask why the order did not reach its
// customer: the answer teaches the band.
export const takesReason = new Set<OrderStatus>(['cancelled', 'failed_delivery', 'returned']);

export const reasonLabel: Record<OutcomeReason, string> = {
  refused: 'العميل رفض الطلب',
  no_answer: 'العميل لم يرد',
};

export type RiskSettings = components['schemas']['RiskSettings'];
export type RiskRule = components['schemas']['RiskRule'];

// ruleLabel names a rule's starting band in the settings' lists.
export const ruleLabel: Record<RiskRule, string> = {
  off: 'متوقف',
  medium: 'من المخاطرة المتوسطة',
  high: 'المخاطرة العالية فقط',
};

// canManageRules: changing the rules needs orders.manage.
export const canManageRules = (r: string | undefined) => r === 'owner' || r === 'manager' || r === 'support';
