# The gateway, one service per region behind the global balancer. Readiness
# is what the balancer's service health reads (docs/design.md §7): failing
# `/readyz` on purpose is the chaos experiment, so the probe is tight.
resource "random_password" "admin_token" {
  length  = 32
  special = false
}

resource "google_cloud_run_v2_service" "gateway" {
  for_each = toset(var.regions)

  name     = "pusaq-gateway"
  location = each.key
  # Reachable directly by its run.app URL as well as through the balancer:
  # the experiment injects readiness failures and probes each region on its
  # own URL. Completions need an API key; the admin route needs the token.
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    scaling {
      min_instance_count = var.min_instances
      max_instance_count = var.max_instances
    }

    timeout                          = "120s"
    max_instance_request_concurrency = 200

    vpc_access {
      network_interfaces {
        network    = google_compute_network.runtime.id
        subnetwork = google_compute_subnetwork.run[each.key].id
      }
      # All traffic through the VPC: the mock's internal ingress only admits
      # calls that arrive from inside it.
      egress = "ALL_TRAFFIC"
    }

    containers {
      image = "${var.registry}/pusaq:${var.image_tag}"

      ports {
        container_port = 8080
      }

      # CPU stays allocated outside requests: the cost-event emitter drains
      # asynchronously after the response, and the injected not-ready
      # window and readiness probe must not depend on a throttled clock.
      # Cloud Run requires at least 512 MiB in that mode.
      resources {
        cpu_idle = false
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "PUSAQ_ADDR"
        value = ":8080"
      }
      env {
        name  = "PUSAQ_REGION"
        value = each.key
      }
      env {
        name  = "PUSAQ_REDIS_ADDR"
        value = "${google_redis_instance.region[each.key].host}:${google_redis_instance.region[each.key].port}"
      }
      env {
        name  = "PUSAQ_UPSTREAMS"
        value = "mock=${google_cloud_run_v2_service.mock[each.key].uri}"
      }
      env {
        name  = "PUSAQ_API_KEYS"
        value = var.api_keys
      }
      env {
        name  = "PUSAQ_RPM_LIMIT"
        value = tostring(var.rpm_limit_per_region)
      }
      env {
        name  = "PUSAQ_RPM_BURST"
        value = tostring(var.rpm_burst_per_region)
      }
      env {
        name  = "PUSAQ_TPM_LIMIT"
        value = "0"
      }
      env {
        name  = "PUSAQ_ADMIN_TOKEN"
        value = random_password.admin_token.result
      }
      env {
        name  = "PUSAQ_DRAIN_DELAY"
        value = "0s"
      }
      env {
        name  = "PUSAQ_TELEMETRY_BUFFER"
        value = "65536"
      }

      readiness_probe {
        http_get {
          path = "/readyz"
          port = 8080
        }
        period_seconds    = var.readiness_period_seconds
        timeout_seconds   = 1
        failure_threshold = var.readiness_failure_threshold
        success_threshold = 1
      }
    }
  }

  depends_on = [google_project_service.enabled]
}

resource "google_cloud_run_v2_service_iam_member" "gateway_invoker" {
  for_each = google_cloud_run_v2_service.gateway

  name     = each.value.name
  location = each.value.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
