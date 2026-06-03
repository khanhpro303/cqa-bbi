// Shared validation for ERP endpoint permissions.
//
// Only product/inventory reads are scoped by VTHH product group, so when one of
// those resources is enabled it MUST carry a non-empty group filter. Orders,
// customers and debt are scoped by partner and never render a group selector.

export const RESOURCES_REQUIRING_GROUPS = ['products', 'inventory'] as const

export interface PermissionEndpoint {
  resource: string
  is_enabled: boolean
  product_groups_arr?: string[]
  brands_arr?: string[]
  all_brands?: boolean
}

/**
 * Returns the list of resource names that are enabled but missing their
 * required VTHH product-group filter. An empty array means the set is valid.
 */
export function findEndpointsMissingGroups(
  endpoints: PermissionEndpoint[],
): string[] {
  return endpoints
    .filter(
      (ep) =>
        ep.is_enabled &&
        RESOURCES_REQUIRING_GROUPS.includes(
          ep.resource as (typeof RESOURCES_REQUIRING_GROUPS)[number],
        ) &&
        (ep.product_groups_arr?.filter((g) => g && g.trim()).length ?? 0) === 0,
    )
    .map((ep) => ep.resource)
}

/**
 * Returns the list of enabled product/inventory resources that have NOT made an
 * explicit brand choice — neither "all brands" nor a non-empty brand list. An
 * empty array means the brand boundary is validly configured. Mirrors the group
 * requirement; the runtime still treats an empty list as "all" (legacy-safe).
 */
export function findEndpointsMissingBrands(
  endpoints: PermissionEndpoint[],
): string[] {
  return endpoints
    .filter(
      (ep) =>
        ep.is_enabled &&
        RESOURCES_REQUIRING_GROUPS.includes(
          ep.resource as (typeof RESOURCES_REQUIRING_GROUPS)[number],
        ) &&
        !ep.all_brands &&
        (ep.brands_arr?.filter((b) => b && b.trim()).length ?? 0) === 0,
    )
    .map((ep) => ep.resource)
}
