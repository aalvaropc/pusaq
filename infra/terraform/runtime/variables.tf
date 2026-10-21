variable "project_id" {
  description = "GCP project that hosts Pusaq."
  type        = string
  default     = "aalvaropc"
}

variable "regions" {
  description = "Cloud Run regions the gateway is deployed to, one Memorystore instance each. Two or more."
  type        = list(string)
  default     = ["us-central1", "us-east1"]

  validation {
    condition     = length(var.regions) >= 2 && length(distinct(var.regions)) == length(var.regions)
    error_message = "regions must name at least two distinct regions."
  }
}

variable "registry" {
  description = "Docker registry path the foundation publishes images to (foundation output artifact_registry_url)."
  type        = string
  default     = "us-central1-docker.pkg.dev/aalvaropc/pusaq"
}

variable "image_tag" {
  description = "Tag of the pusaq and mockprovider images to deploy. Pin the commit SHA for a published run."
  type        = string
  validation {
    condition     = can(regex("^[0-9a-f]{40}$", var.image_tag))
    error_message = "The image tag must be a full lowercase commit SHA."
  }
}

variable "min_instances" {
  description = "Minimum gateway instances per region. Cloud Run service health needs at least one; scale-to-zero is not failover."
  type        = number
  default     = 1
}

variable "max_instances" {
  description = "Maximum gateway instances per region. Readiness injection is per instance, so 1 makes it regional."
  type        = number
  default     = 1
}

variable "rpm_limit_per_region" {
  description = "Per-tenant request limit per region, requests per minute. Quotas are split across regions (design.md §7): half the nominal limit each."
  type        = number
  default     = 300
}

variable "rpm_burst_per_region" {
  description = "Per-tenant request bucket capacity per region."
  type        = number
  default     = 50
}

variable "api_keys" {
  description = "PUSAQ_API_KEYS for the run (key:tenant, comma-separated). Throwaway keys: the balancer speaks plain HTTP."
  type        = string
  sensitive   = true
  default     = "chaos-main:tenant-main,chaos-stream:tenant-stream,chaos-split:tenant-split"
}

variable "readiness_period_seconds" {
  description = "Seconds between readiness probes of a gateway instance."
  type        = number
  default     = 2
}

variable "readiness_failure_threshold" {
  description = "Consecutive readiness probe failures before Cloud Run stops routing to the instance."
  type        = number
  default     = 1
}

variable "outlier_detection" {
  description = "Also enable outlier detection on the balancer's backend service (5xx-based ejection). Off by default so a run attributes failover to service health alone."
  type        = bool
  default     = false
}

variable "mock_latency_p50" {
  description = "Mock provider median latency (Go duration)."
  type        = string
  default     = "200ms"
}

variable "mock_latency_p99" {
  description = "Mock provider tail latency (Go duration)."
  type        = string
  default     = "800ms"
}

variable "mock_stream_chunk_delay" {
  description = "Delay between streamed chunks on the mock (Go duration), so streams last long enough to be in flight when readiness is failed."
  type        = string
  default     = "200ms"
}

variable "labels" {
  description = "Labels applied to every resource; the foundation's budget filters on `project`."
  type        = map(string)
  default = {
    project    = "pusaq"
    managed_by = "terraform"
  }
}
