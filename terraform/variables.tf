variable "project_id" {
  type        = string
  description = "Target Google Cloud Project ID"
  default     = "quacktastic-waffle"
}

variable "region" {
  type        = string
  description = "Target Google Cloud regional scope for standard provisioning"
  default     = "us-central1"
}

variable "environment" {
  type        = string
  description = "Target deployment workspace slot"
  default     = "staging"
}

variable "github_repo_uri" {
  type        = string
  description = "Target GitHub repository link to connect in Developer Connect"
  default     = "https://github.com/weitzer-org/open-llm.git"
}

variable "developer_connect_connection_id" {
  type        = string
  description = "Name/ID of the pre-existing Developer Connect GitHub Connection"
  default     = "gsr-code-review"
}

variable "vpc_network" {
  type        = string
  description = "The target VPC network to configure Direct VPC egress paths"
  default     = "default"
}

variable "vpc_subnetwork" {
  type        = string
  description = "The target subnetwork within the VPC network for network allocation"
  default     = "default"
}

variable "active_model_path" {
  type        = string
  description = "Relative path within GCS FUSE mount bucket representing active safetensors weights (e.g. qwen-2.5-7b-instruct)"
  default     = "qwen-2.5-7b-instruct-fp8"
}

variable "gateway_image" {
  type        = string
  description = "Fully qualified Docker image URI for the Go API Gateway (provided dynamically during CI/CD steps)"
  default     = "us-central1-docker.pkg.dev/quacktastic-waffle/open-llm-repo/gateway:latest"
}

variable "vllm_image" {
  type        = string
  description = "Standard, pre-compiled vLLM engine container image tag"
  default     = "vllm/vllm-openai:v0.6.3.post1"
}

variable "max_model_len" {
  type        = number
  description = "vLLM context length window size constraint"
  default     = 8192
}

variable "gpu_memory_utilization" {
  type        = number
  description = "Fraction of VRAM reserved for KV cache allocations in vLLM"
  default     = 0.90
}

variable "gateway_min_instances" {
  type        = number
  description = "Minimum count of container copies for Go Gateway service (set to 0 for true scale-to-zero, 1 for continuous hot standby)"
  default     = 0
}
