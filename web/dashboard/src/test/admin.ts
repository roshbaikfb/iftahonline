import type { Admin, Permission } from '../admin/session';

// rolePermissions mirror ADR-0077's table (the server's domain.Role).
export const rolePermissions: Record<Admin['role'], Permission[]> = {
  owner: ['stores.read', 'stores.manage', 'contacts.read', 'billing.read', 'billing.manage', 'catalog.read', 'catalog.manage', 'content.read', 'content.manage', 'audit.read', 'team.manage'],
  operations: ['stores.read', 'stores.manage', 'contacts.read', 'billing.read', 'catalog.read'],
  finance: ['stores.read', 'billing.read', 'billing.manage', 'catalog.read', 'catalog.manage'],
  content: ['content.read', 'content.manage'],
  viewer: ['stores.read', 'billing.read', 'catalog.read', 'content.read'],
};

// testAdmin is a signed-in admin in role, an owner by default.
export function testAdmin(over: Partial<Admin> = {}, role: Admin['role'] = 'owner'): Admin {
  return { id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة', role, permissions: rolePermissions[role], ...over };
}
