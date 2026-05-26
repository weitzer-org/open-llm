# 🪐 Scale-to-Zero Open-LLM Gateway & Inference Architecture

A highly cost-efficient, production-grade framework for hosting open-source, Flash-equivalent Large Language Models (such as Qwen 2.5, Llama 3.1) on Google Cloud Platform. 

By splitting the workload into a **two-tier architecture**, the system stays warm and highly responsive at near-zero baseline costs via a lightweight Go API Gateway, while routing model processing requests to an isolated, **scale-to-zero GPU vLLM container** that only charges for exact token-generation seconds.

---

## 🏗️ System Architecture

```
                                    +---------------------------------------+
                                    |         Google Cloud Platform         |
                                    |                                       |
+------------------+                |  +---------------------------------+  |
|  Client (Web /   |  1. HTTPS REST |  |         Go API Gateway          |  |
|  SDK connection) +--------------->|  |      (Cloud Run CPU, Warm)      |  |
+------------------+  Bearer Auth   |  +----------------+----------------+  |
                                    |                   |                   |
                                    |                   | 3. Direct VPC      |
                                    |                   |    Egress Route   |
                                    |                   v                   |
+------------------+                |  +---------------------------------+  |
|  Secret Manager  |  2. Fetches    |  |       vLLM Inference Node       |  |
|  (preshared key) +<---------------+  |      (Cloud Run GPU L4,         |  |
+------------------+     token      |  |       Scale-to-Zero 0-1)        |  |
                                    |  +----------------+----------------+  |
                                    |                   |                   |
                                    |                   | 4. GCS FUSE       |
                                    |                   |    Lazy Load      |
                                    |                   v                   |
                                    |  +---------------------------------+  |
                                    |  |         Model Zoo Bucket        |  |
                                    |  |          (GCS Storage)          |  |
                                    |  +---------------------------------+  |
                                    +---------------------------------------+
```

1. **Go API Gateway**: CPU-only, warm standby. Serves a stunning web interface console for prompting at `/`, processes REST paths `/v1/chat/completions` and `/v1/models`, enforces Pre-Shared Key Bearer token authorization, and streams responses instantly back to client tools via SSE chunking (custom `httputil.ReverseProxy` with negative `FlushInterval`).
2. **vLLM Inference Backend**: NVIDIA L4 GPU (24GB VRAM, 4 vCPU, 16GB Memory). Ingress is set to internal-only, keeping it private and inaccessible from the open internet. Automatically spins down to 0 instances when idle, incurring zero baseline costs.
3. **Storage (Model Zoo)**: A regional Google Cloud Storage bucket acts as a model weights repository mounted inside the GPU container as a local drive via Cloud Storage FUSE. Direct regional access guarantees $0.00 network egress charges for loading weights.

---

## 📂 Project Structure

- `gateway/`: Go API proxy program code, Docker definitions, and embedded template files.
- `terraform/`: Infrastructure as Code configurations (Secret Manager, GCS, Cloud Run, IAM roles, and Cloud Build triggers).
- `cloudbuild.yaml`: Automated pipeline orchestrator compiling packages, executing test gates, bootstrapping systems, building tags, and deploying updates.

---

## 🗝️ Initial Security & Credentials Retrieval

Authentication is managed via a pre-shared master token generated automatically at first boot and stored securely in Google Secret Manager (under the secret ID `open-llm-api-auth-secret`).

To fetch the master token for your developer clients or web console log-ins, run the following CLI command:

```bash
gcloud secrets versions access latest --secret="open-llm-api-auth-secret" --project="quacktastic-waffle"
```

*Note: Save this key in your browser console sidebar's credentials block (persists via `localStorage`) to interact with the testing UI!*

---

## 🚀 Client Integration & Subagent Setup

Consuming applications (such as local subagent orchestrators) can connect to the Gateway seamlessly using the standard OpenAI SDK by overriding the `baseURL` and supplying the pre-shared key.

### ⚠️ IMPORTANT: Handling Cold Starts
Because the heavy GPU layers aggressively scale to zero when idle, **the very first request after an idle period will experience a 15 to 45-second cold start penalty** (allocating resources and lazy-loading weights via GCS FUSE). 

To prevent client drop-outs, **consuming HTTP clients MUST configure their HTTP connection/request timeout to at least 60 to 90 seconds**!

### Example: Python Integration (OpenAI SDK)

```python
from openai import OpenAI

# Initialize the gateway client with generous timeouts to support cold boots
client = OpenAI(
    base_url="https://open-llm-gateway-abcde-uc.a.run.app/v1",  # Replace with actual Gateway URL
    api_key="YOUR_PRE_SHARED_SECRET_KEY",
    timeout=90.0,  # 90-second timeout ensures cold starts succeed!
)

# Call the model via streaming (highly recommended for conversational feel)
stream = client.chat.completions.create(
    model="active-model",
    messages=[
        {"role": "system", "content": "You are a precise data extractor."},
        {"role": "user", "content": "Extract values from raw log: ..."}
    ],
    stream=True,
)

for chunk in stream:
    if chunk.choices[0].delta.content is not None:
        print(chunk.choices[0].delta.content, end="")
```

---

## 🦁 The Platform Engineer's Guide (Model Zoo & Swaps)

To change the active model (e.g., swapping from Qwen 2.5 7B to Llama 3.1 8B) without rebuilding any containers or touching Gateway code:

### Step 1: Upload model weights to GCS
Download the raw model weights (SafeTensors layout, tokenizers, config JSON files) and upload them to a dedicated folder inside the Model Zoo bucket.

```bash
# Upload a new model structure to GCS
gcloud storage cp -r ./Llama-3.1-8B-Instruct gs://open-llm-models-quacktastic-waffle/llama-3.1-8b-instruct
```

### Step 2: Update active model variable in Terraform
Open `terraform/variables.tf` (or supply a `.tfvars` file / command flag) and update the `active_model_path` variable to target the new folder name:

```hcl
variable "active_model_path" {
  type    = string
  default = "llama-3.1-8b-instruct" # Updated to point to new subfolder!
}
```

### Step 3: Commit & Deploy
Commit and push the changes to the `main` branch. The Cloud Build CI/CD pipeline will automatically build/verify, run the Terraform plans, reboot the vLLM Cloud Run service, update its GCS FUSE parameters, and roll out the new version. The new weights will be loaded on the next call's cold start.

---

## 🛠️ Local Development & Operations

### Run Unit Tests
To execute the unit test suites (validating handlers, CORS middleware, token check gates, and mock proxies) locally:

```bash
cd gateway
go test -v ./...
```

### Run Gateway locally (Local Development Loop)
You can run the Go Gateway locally. By default, if `API_AUTH_SECRET` is empty, it runs in **unauthenticated mode**, and if `VLLM_API_URL` is empty, it routes to `localhost:8000` (perfect for testing with a mock server or a local llama.cpp/vLLM instance!).

```bash
cd gateway
# Start local gateway server on :8080
PORT=8080 VLLM_API_URL="http://localhost:8000" go run main.go
```
