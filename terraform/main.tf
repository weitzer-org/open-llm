# ========================================================================
# 🪐 OPEN-LLM GATEWAY & INFERENCE INFRASTRUCTURE PROVISIONING
# ========================================================================

# 1. State Backend Settings (using regional tf-state GCS bucket)
terraform {
  backend "gcs" {
    bucket = "tf-state-quacktastic-waffle"
    prefix = "open-llm/state/staging"
  }
}

# 2. Target GCP Project Scope Reference
data "google_project" "project" {
  project_id = var.project_id
}

# 3. Enable Required Google API Services
locals {
  apis = [
    "cloudbuild.googleapis.com",
    "run.googleapis.com",
    "artifactregistry.googleapis.com",
    "developerconnect.googleapis.com",
    "secretmanager.googleapis.com",
    "storage.googleapis.com",
    "iam.googleapis.com"
  ]
}

resource "google_project_service" "apis" {
  for_each           = toset(local.apis)
  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

# ========================================================================
# 4. Dedicated Service Accounts & Additive least-privilege IAM Roles
# ========================================================================

# A. Go Gateway API Service Account
resource "google_service_account" "gateway_sa" {
  project      = var.project_id
  account_id   = "open-llm-gateway-sa"
  display_name = "Open-LLM Gateway Endpoint Worker Service Account"
  depends_on   = [google_project_service.apis["iam.googleapis.com"]]
}

# B. vLLM Inference Engine Service Account
resource "google_service_account" "vllm_sa" {
  project      = var.project_id
  account_id   = "open-llm-vllm-sa"
  display_name = "Open-LLM vLLM GPU Server Service Account"
  depends_on   = [google_project_service.apis["iam.googleapis.com"]]
}

# C. CD Pipeline Operator Service Account
resource "google_service_account" "pipeline_sa" {
  project      = var.project_id
  account_id   = "open-llm-pipeline-sa"
  display_name = "Open-LLM CD Pipeline Operator Service Account"
  depends_on   = [google_project_service.apis["iam.googleapis.com"]]
}

# D. Grant Additive Least-Privilege Roles to Pipeline SA for cloud build & deployments
locals {
  pipeline_roles = [
    "roles/cloudbuild.builds.builder",
    "roles/run.developer",
    "roles/artifactregistry.writer",
    "roles/iam.serviceAccountUser",
    "roles/storage.admin",
    "roles/secretmanager.admin",
    "roles/compute.networkUser"
  ]
}

resource "google_project_iam_member" "pipeline_sa_roles" {
  for_each = toset(local.pipeline_roles)
  project  = var.project_id
  role     = each.key
  member   = "serviceAccount:${google_service_account.pipeline_sa.email}"
}

# Grant Cloud Build Service Agent permission to act as our custom pipeline SA
resource "google_service_account_iam_member" "cloudbuild_sa_user" {
  service_account_id = google_service_account.pipeline_sa.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
}

# Grant custom SA permission to act as service agent for triggers
resource "google_project_iam_member" "cloudbuild_service_agent" {
  project = var.project_id
  role    = "roles/cloudbuild.serviceAgent"
  member  = "serviceAccount:${google_service_account.pipeline_sa.email}"
}

# ========================================================================
# 5. Secret Manager Configuration (API Pre-Shared Key Store)
# ========================================================================

resource "google_secret_manager_secret" "api_auth_secret" {
  project   = var.project_id
  secret_id = "open-llm-api-auth-secret"
  depends_on = [google_project_service.apis["secretmanager.googleapis.com"]]

  replication {
    auto {}
  }
}

resource "random_password" "api_auth_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret_version" "api_auth_secret" {
  secret      = google_secret_manager_secret.api_auth_secret.id
  secret_data = random_password.api_auth_secret.result
}

# Grant Gateway SA permission to read this specific secret (Least Privilege!)
resource "google_secret_manager_secret_iam_member" "gateway_secret_accessor" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.api_auth_secret.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.gateway_sa.email}"
}

# Grant corporate Cloudtop developer SA permission to read this secret for local validation
resource "google_secret_manager_secret_iam_member" "jetski_secret_accessor" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.api_auth_secret.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:jetski-secret-accessor@quacktastic-waffle.iam.gserviceaccount.com"
}


# ========================================================================
# 6. Storage Infrastructure (GCS Model Zoo Bucket)
# ========================================================================

resource "google_storage_bucket" "model_bucket" {
  project                     = var.project_id
  name                        = "open-llm-models-${var.project_id}"
  location                    = var.region
  storage_class               = "REGIONAL"
  uniform_bucket_level_access = true
  force_destroy               = false
  depends_on                  = [google_project_service.apis["storage.googleapis.com"]]

  versioning {
    enabled = true
  }
}

# Grant vLLM SA permission to read model objects (Least Privilege!)
resource "google_storage_bucket_iam_member" "vllm_storage_viewer" {
  bucket = google_storage_bucket.model_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.vllm_sa.email}"
}

# ========================================================================
# 7. Artifact Registry (Go Gateway Image Store)
# ========================================================================

resource "google_artifact_registry_repository" "repo" {
  project       = var.project_id
  location      = var.region
  repository_id = "open-llm-repo"
  format        = "DOCKER"
  description   = "Open-LLM Docker Registry for CPU Gateway images"
  depends_on    = [google_project_service.apis["artifactregistry.googleapis.com"]]
}

# ========================================================================
# 8. Serverless Services Provisioning
# ========================================================================

# A. Scale-To-Zero vLLM Inference Engine (GPU Service, Internal Only)
resource "google_cloud_run_v2_service" "vllm" {
  project             = var.project_id
  name                = "open-llm-vllm"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  launch_stage        = "BETA"
  deletion_protection = false
  depends_on          = [google_project_service.apis["run.googleapis.com"], google_storage_bucket_iam_member.vllm_storage_viewer]

  template {
    service_account               = google_service_account.vllm_sa.email
    timeout                       = "600s" # 10 minutes max connection duration
    gpu_zonal_redundancy_disabled = true

    containers {
      image = var.vllm_image
      
      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu              = "8"
          memory           = "32Gi"
          "nvidia.com/gpu" = "1"
        }
      }

      # GCS FUSE direct local folder mounting
      volume_mounts {
        name       = "model-volume"
        mount_path = "/models"
      }

      args = [
        "--model", "/models/${var.active_model_path}",
        "--port", "8080",
        "--host", "0.0.0.0",
        "--max-model-len", tostring(var.max_model_len),
        "--gpu-memory-utilization", tostring(var.gpu_memory_utilization),
        "--disable-log-requests",
        "--served-model-name", "active-model"
      ]

      # startup probe to hold container warm-up during heavy model weights loading (5 mins ceiling)
      startup_probe {
        timeout_seconds   = 5
        period_seconds    = 10
        failure_threshold = 60 # 60 attempts * 10s = 600s (10 mins)
        http_get {
          path = "/health"
          port = 8080
        }
      }
    }

    volumes {
      name = "model-volume"
      gcs {
        bucket    = google_storage_bucket.model_bucket.name
        read_only = true
      }
    }

    node_selector {
      accelerator = "nvidia-l4"
    }

    scaling {
      min_instance_count = 0 # Scale to Zero!
      max_instance_count = 1 # Serves one model at a time (F2)
    }
  }
}

# Grant Gateway SA permission to invoke the public vLLM service over private or public routes
resource "google_cloud_run_v2_service_iam_member" "vllm_gateway_invoker" {
  project    = var.project_id
  location   = var.region
  name       = google_cloud_run_v2_service.vllm.name
  role       = "roles/run.invoker"
  member     = "serviceAccount:${google_service_account.gateway_sa.email}"
  depends_on = [google_cloud_run_v2_service.vllm]
}

# Grant corporate Cloudtop developer SA permission to invoke the public vLLM service for local development loops
resource "google_cloud_run_v2_service_iam_member" "vllm_jetski_invoker" {
  project    = var.project_id
  location   = var.region
  name       = google_cloud_run_v2_service.vllm.name
  role       = "roles/run.invoker"
  member     = "serviceAccount:jetski-secret-accessor@quacktastic-waffle.iam.gserviceaccount.com"
  depends_on = [google_cloud_run_v2_service.vllm]
}

# B. Go API Gateway & Dashboard Console (CPU Service, Publicly Accessible)
resource "google_cloud_run_v2_service" "gateway" {
  project             = var.project_id
  name                = "open-llm-gateway"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis["run.googleapis.com"], google_secret_manager_secret_iam_member.gateway_secret_accessor]

  template {
    service_account = google_service_account.gateway_sa.email

    containers {
      image = var.gateway_image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "PORT"
        value = "8080"
      }

      env {
        name  = "VLLM_API_URL"
        value = google_cloud_run_v2_service.vllm.uri
      }

      # Mount Secret Manager pre-shared key directly to API_AUTH_SECRET env var
      env {
        name = "API_AUTH_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.api_auth_secret.secret_id
            version = "latest"
          }
        }
      }
    }

    # Direct VPC Egress routing through default regional network interfaces (No VPC Connector Baseline Costs!)
    vpc_access {
      network_interfaces {
        network    = var.vpc_network
        subnetwork = var.vpc_subnetwork
      }
      egress = "ALL_TRAFFIC"
    }

    scaling {
      min_instance_count = var.gateway_min_instances
      max_instance_count = 5
    }
  }
}

# Permit general unauthenticated public dashboard connections & token REST queries
resource "google_cloud_run_v2_service_iam_member" "gateway_public" {
  project    = var.project_id
  location   = var.region
  name       = google_cloud_run_v2_service.gateway.name
  role       = "roles/run.invoker"
  member     = "allUsers"
  depends_on = [google_cloud_run_v2_service.gateway]
}

# ========================================================================
# 9. Developer Connect & Cloud Build Trigger System
# ========================================================================

# A. Developer Connect Git Link to GitHub
resource "google_developer_connect_git_repository_link" "main" {
  project                 = var.project_id
  location                = var.region
  parent_connection       = var.developer_connect_connection_id
  git_repository_link_id  = "open-llm-git-link"
  clone_uri               = var.github_repo_uri
  depends_on              = [google_project_service.apis["developerconnect.googleapis.com"]]
}

# B. Auto Continuous Deployment pipeline on pushes to main branch
resource "google_cloudbuild_trigger" "open_llm" {
  project     = var.project_id
  location    = var.region
  name        = "open-llm-trigger"
  description = "Continuous Deployment trigger for open-llm gateway and vLLM architecture updates"
  depends_on  = [
    google_project_service.apis["cloudbuild.googleapis.com"],
    google_service_account_iam_member.cloudbuild_sa_user,
    google_project_iam_member.cloudbuild_service_agent
  ]

  developer_connect_event_config {
    git_repository_link = google_developer_connect_git_repository_link.main.id
    push {
      branch = "^main$"
    }
  }

  filename        = "cloudbuild.yaml"
  service_account = google_service_account.pipeline_sa.id
}
