output "lb_url" {
  description = "Base URL of the global balancer (plain HTTP); what k6 targets."
  value       = "http://${google_compute_global_address.gateway.address}"
}

output "gateway_urls" {
  description = "Per-region gateway URLs, for readiness injection and per-region probes."
  value       = { for region, svc in google_cloud_run_v2_service.gateway : region => svc.uri }
}

output "mock_urls" {
  description = "Per-region mock provider URLs (internal ingress: reachable from the VPC only)."
  value       = { for region, svc in google_cloud_run_v2_service.mock : region => svc.uri }
}

output "redis_hosts" {
  description = "Per-region Memorystore endpoints."
  value       = { for region, r in google_redis_instance.region : region => "${r.host}:${r.port}" }
}

output "admin_token" {
  description = "Bearer token for the gateways' admin endpoint."
  value       = random_password.admin_token.result
  sensitive   = true
}

output "image" {
  description = "Gateway image deployed."
  value       = "${var.registry}/pusaq:${var.image_tag}"
}
