provider "google" {
  project = var.project_id
  region  = var.region

  # The budgets API bills its quota to the caller's project; pin it.
  user_project_override = true
  billing_project       = var.project_id

  # Applied to every resource that supports labels. The label-filtered budget
  # in budget.tf keys on `project = "pusaq"`; the runtime module sets the
  # same label, so its resources are counted too.
  default_labels = var.labels
}
