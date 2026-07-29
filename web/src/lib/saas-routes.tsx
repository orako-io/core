// SaaS-only routes seam. Empty in the community/EE build — the SaaS build splices
// in the real implementation from the private overlay, so no
// billing source ships in core.
export function SaasRoutes() {
  return null
}
