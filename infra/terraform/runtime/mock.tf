# The upstream for the run: the mock provider, one per region, reachable
# only from inside the VPC (internal ingress). The invoker binding is what
# lets the gateway call it without an identity token; the ingress setting is
# what keeps it private.
resource "google_cloud_run_v2_service" "mock" {
  for_each = toset(var.regions)

  name                = "pusaq-mock"
  location            = each.key
  ingress             = "INGRESS_TRAFFIC_INTERNAL_ONLY"
  deletion_protection = false

  template {
    scaling {
      min_instance_count = 1
      max_instance_count = 2
    }

    containers {
      image = "${var.registry}/mockprovider:${var.image_tag}"
      args = [
        "--addr", ":9090",
        "--latency-p50", var.mock_latency_p50,
        "--latency-p99", var.mock_latency_p99,
        "--stream-chunk-delay", var.mock_stream_chunk_delay,
      ]

      ports {
        container_port = 9090
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "256Mi"
        }
      }
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_cloud_run_v2_service_iam_member" "mock_invoker" {
  for_each = google_cloud_run_v2_service.mock

  name     = each.value.name
  location = each.value.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
