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
Because the heavy GPU vLLM container aggressively scales to zero when idle to maintain $0.00 baseline costs, the very first request after an idle period will trigger a serverless cold boot:
* **Cold Boot (Fresh Host Node)**: **Takes approx. 4.5 minutes (270 seconds)**. GCSFuse lazy-loads all 4 SafeTensors checkpoint shards sequentially (approx. 61 seconds per 3.5GB segment) and takes ~30 seconds for PyTorch to initialize VRAM allocations and build static CUDA graph paths.
* **Warm Boot (Cached Host Node)**: **Takes approx. 35 to 45 seconds** if scheduled onto a host with warm FUSE caches.

To prevent connection drop-outs during cold boots, **all consuming client applications and orchestrators MUST configure their HTTP connection and read timeouts to at least 5 to 10 minutes (300s - 600s)**!

### Example: Python Integration (OpenAI SDK)

```python
from openai import OpenAI

# Initialize the gateway client with generous timeouts to stand the cold boot window
client = OpenAI(
    base_url="https://open-llm-gateway-abcde-uc.a.run.app/v1",  # Replace with actual Gateway URL
    api_key="YOUR_PRE_SHARED_SECRET_KEY",
    timeout=600.0,  # 10-minute timeout ensures all cold starts succeed!
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

### Secure Local Development (Direct Corporate Routing)
To build, run, and test your local Go Gateway server securely connected to the live, private L4 GPU backend in the cloud (without modifying any code or opening vulnerable public ports!):

1. **Start the secure tunnel loop**:
   From your Cloudtop root workspace directory, simply execute:
   ```bash
   ./run-dev-tunnel.sh
   ```
   * **What it does**: Automatically checks/installs the `google-cloud-cli-cloud-run-proxy` package, boots a secure OIDC IAM loopback tunnel on port `8001` with your active service account credentials, auto-fetches your Secret key fallback parameters, and launches your local Go Gateway server on port `8080` pointing to the tunnel!

2. **Open the Direct corporate Web Console**:
   The script will dynamically query your Cloudtop's corporate hostname and print a clickable link. Navigate your corporate laptop browser directly to:
   👉 **`http://benweitzer2.c.googlers.com:8080/`**
   *(No manual SSH port forwarding or local loop tunnels required! Your corporate laptop VPN resolves this endpoint natively).*

3. **Frictionless UI Authentication**:
   The master pre-shared developer token is **automatically pre-populated and validated** right on page load! Simply type prompts and start testing the real-time VRAM completion pipelines instantly!

---

### Local Sandbox Mode (100% Offline Testing)
If you are working offline or iterating fast on front-end CSS/HTML/JS console features without needing live model generations:

1. **Launch the offline Mock vLLM Server** (simulates real-time chunk delays at 25 tokens/sec):
   ```bash
   python3 /usr/local/google/home/benweitzer/.gemini/jetski/brain/610fa260-94b9-45d8-93a3-65f4151144fa/scratch/mock_vllm.py
   ```

2. **Start your local Gateway pointing to the mock port**:
   ```bash
   cd gateway
   PORT=8080 API_AUTH_SECRET="open-llm-dev-token" DEV_MODE="true" VLLM_API_URL="http://localhost:8000" go run main.go
   ```
3. Open your browser to `http://localhost:8080/` (or your FQDN link!) and use the pre-filled token `open-llm-dev-token` to start instant, offline sandbox prompting!

---

## 🔮 Future Scalability: Expanding the Context Window (TODO Roadmap)

To expand your model's active context window safely beyond the current **2,048 tokens** memory boundary to support larger document feeds or historical chats (without triggering GPU VRAM Out-of-Memory crashes on the L4 GPU), we have outlined the following structural engineering strategies:

* **[ ] Task 1: Enable AWQ/GPTQ/FP8 Model Quantization (High-Impact)**
  * **How it works**: Compresses model weights from standard BF16 (16-bit) down to high-performance FP8 (8-bit) or INT4/AWQ (4-bit) representation.
  * **The Gain**: Drops the raw model parameters footprint from 14GB to **7GB (FP8) or 3.5GB (INT4)** VRAM! This immediately frees up **17GB to 20.5GB** of GPU memory to hold massive self-attention KV Cache pools, letting you scale your context length to **8k or 16k tokens** on the same single L4 GPU!

* **[ ] Task 2: Configure FP8 KV Cache Compressions**
  * **How it works**: Compresses attention matrices values down to 8-bit precision by appending the vLLM startup parameter `--kv-cache-dtype fp8`.
  * **The Gain**: Halves the dynamic memory footprint per active sequence block, immediately doubling your model's request concurrency and context handling capacity.

* **[ ] Task 3: Enable Chunked Prefills & FlashAttention-3**
  * **How it works**: Standard processes map your entire prompt sequence at once, causing heavy memory peaks on large prompts. Enabling chunked prefills (`--enable-chunked-prefill`) chunks and sequences the input workload.
  * **The Gain**: Smooths dynamic memory allocation surges, preventing OOM spikes on heavy document prompt submissions.

* **[ ] Task 4: Scale Out to Multi-GPU Node Pools (Tensor Parallelism)**
  * **How it works**: For extreme enterprise requirements (e.g., 32k or 128k context lengths), configure your Terraform templates to scale out execution across multiple GPUs (e.g., set `tensor_parallel_size = 2` L4 GPUs or scale up to a single A100 80GB VRAM node).
  * **The Gain**: Splits the computational weights workload and KV cache pools across multiple units, opening unlimited scaling routes!
