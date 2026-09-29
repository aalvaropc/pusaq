# One VPC with a subnet per region. The gateway reaches its region's
# Memorystore instance and the internal-ingress mock through Direct VPC
# egress; Private Google Access lets run.app resolve without a NAT.
resource "google_compute_network" "runtime" {
  name                    = "pusaq-runtime"
  auto_create_subnetworks = false

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "run" {
  for_each = toset(var.regions)

  name                     = "pusaq-run-${each.key}"
  region                   = each.key
  network                  = google_compute_network.runtime.id
  ip_cidr_range            = cidrsubnet("10.8.0.0/16", 8, index(var.regions, each.key))
  private_ip_google_access = true
}
