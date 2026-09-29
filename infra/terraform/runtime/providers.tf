provider "google" {
  project = var.project_id
  region  = var.regions[0]

  # Everything this module creates is counted by the label-filtered budget
  # in the foundation (project = "pusaq").
  default_labels = var.labels
}
