# Global external Application Load Balancer over one serverless NEG per
# region. Serverless NEGs take no classic health check: the balancer follows
# Cloud Run service health (readiness probes, ≥ 60 % of a region's
# instances) and, optionally, outlier detection on 5xx. Plain HTTP on a
# global IPv4 address — no domain, no certificate — because the traffic is
# synthetic and the deployment lives for one run.
resource "google_compute_region_network_endpoint_group" "gateway" {
  for_each = google_cloud_run_v2_service.gateway

  name                  = "pusaq-gateway-${each.key}"
  region                = each.key
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = each.value.name
  }
}

# No timeout_sec: serverless NEG backends reject it (their timeout is fixed;
# the request timeout lives on the Cloud Run service).
resource "google_compute_backend_service" "gateway" {
  name                  = "pusaq-gateway"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTP"

  dynamic "backend" {
    for_each = google_compute_region_network_endpoint_group.gateway
    content {
      group = backend.value.id
    }
  }

  # Optional second mechanism: eject a region after consecutive 5xx. Off by
  # default so a run attributes failover to service health alone.
  dynamic "outlier_detection" {
    for_each = var.outlier_detection ? [1] : []
    content {
      consecutive_errors                    = 5
      consecutive_gateway_failure           = 3
      enforcing_consecutive_errors          = 100
      enforcing_consecutive_gateway_failure = 100
      max_ejection_percent                  = 50
      interval {
        seconds = 1
      }
      base_ejection_time {
        seconds = 30
      }
    }
  }

  # Request logs say which region's NEG answered: a third vantage point
  # next to k6 and the gateways' own events.
  log_config {
    enable      = true
    sample_rate = 1
  }
}

resource "google_compute_url_map" "gateway" {
  name            = "pusaq-gateway"
  default_service = google_compute_backend_service.gateway.id
}

resource "google_compute_target_http_proxy" "gateway" {
  name    = "pusaq-gateway"
  url_map = google_compute_url_map.gateway.id
}

resource "google_compute_global_address" "gateway" {
  name       = "pusaq-gateway"
  ip_version = "IPV4"
}

resource "google_compute_global_forwarding_rule" "gateway" {
  name                  = "pusaq-gateway"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  target                = google_compute_target_http_proxy.gateway.id
  ip_address            = google_compute_global_address.gateway.id
  ip_protocol           = "TCP"
  port_range            = "80"
}
