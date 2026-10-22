variable "project_id" {
  description = "GCP project that hosts Pusaq."
  type        = string
  default     = "aalvaropc"
}

variable "region" {
  description = "Region for regional resources (Artifact Registry)."
  type        = string
  default     = "us-central1"
}

variable "github_repo" {
  description = "GitHub repository (owner/name) allowed to publish images through workload identity federation."
  type        = string
  default     = "aalvaropc/pusaq"
}

variable "labels" {
  description = "Labels applied to every resource; the budget filters on `project`."
  type        = map(string)
  default = {
    project    = "pusaq"
    managed_by = "terraform"
  }
}

variable "artifact_repo_id" {
  description = "Artifact Registry repository id for Pusaq images."
  type        = string
  default     = "pusaq"
}

variable "workload_identity_pool_id" {
  description = "Existing workload identity pool the GitHub provider is added to."
  type        = string
  default     = "github-pool"
}

variable "billing_account" {
  description = "Billing account id (XXXXXX-XXXXXX-XXXXXX) the budget is attached to."
  type        = string
}

variable "budget_amount_units" {
  description = "Monthly budget for resources labeled project=pusaq, in whole USD."
  type        = string
}

variable "notification_channel" {
  description = "Existing Cloud Monitoring notification channel (projects/<id>/notificationChannels/<id>) that receives budget alerts."
  type        = string
}

variable "github_repository_id" {
  description = "Immutable numeric GitHub repository ID allowed to publish."
  type        = string
  default     = "1345668693"
  validation {
    condition     = can(regex("^[0-9]+$", var.github_repository_id))
    error_message = "The repository ID must be numeric."
  }
}

variable "github_owner_id" {
  description = "Immutable numeric GitHub owner ID allowed to publish."
  type        = string
  default     = "110061249"
  validation {
    condition     = can(regex("^[0-9]+$", var.github_owner_id))
    error_message = "The owner ID must be numeric."
  }
}
