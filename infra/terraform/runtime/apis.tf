# APIs the runtime needs on top of the foundation's. disable_on_destroy =
# false keeps `destroy` from switching off APIs other workloads rely on.
locals {
  services = [
    "run.googleapis.com",
    "compute.googleapis.com",
    "redis.googleapis.com",
  ]
}

resource "google_project_service" "enabled" {
  for_each = toset(local.services)

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}
