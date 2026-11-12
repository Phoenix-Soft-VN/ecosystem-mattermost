// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

package phoenix

// TenantUsersPredicate returns the SQL fragment (with one placeholder argument: the tenant id)
// that restricts a user-id column to principals bound to that tenant. Stores AND it into the
// query itself, so it applies before DISTINCT / LIMIT / OFFSET / COUNT and never as a response filter.
func TenantUsersPredicate(userIDColumn string) string {
	return userIDColumn + " IN (SELECT native_user_id FROM phoenix_user_bindings WHERE tenant_id = ?)"
}
