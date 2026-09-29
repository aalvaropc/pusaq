# One Redis per region, deliberately (design.md §5.6): limits are per region
# during failover, never coupled across regions. Basic tier, 1 GB, direct
# peering into the runtime VPC — the smallest thing that bills by the hour.
resource "google_redis_instance" "region" {
  for_each = toset(var.regions)

  name               = "pusaq-${each.key}"
  region             = each.key
  tier               = "BASIC"
  memory_size_gb     = 1
  redis_version      = "REDIS_7_2"
  authorized_network = google_compute_network.runtime.id
  connect_mode       = "DIRECT_PEERING"

  depends_on = [google_project_service.enabled]
}
