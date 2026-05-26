# 🪐 AI Subagent Skill: Open-LLM Gateway & Inference Endpoint Operations

This document defines the interface parameters, credentials lookup paradigms, connection timeout rules, and diagnostic routines required for **Autonomous AI Developer Agents** to consume and operate the Open-LLM scale-to-zero model architecture successfully.

---

## 🎯 Capability Capabilities & Specifications

The framework hosts an OpenAI REST-compliant completions API backed by high-performance local-compute models (e.g. Qwen 2.5 7B, Llama 3.1 8B) running on NVIDIA L4 serverless GPU nodes.

### Standard Endpoint Coordinates
* **Chat Completions Endpoint**: `POST /v1/chat/completions` (supports standard ChatCompletion payloads).
* **Models Zoo Lookup**: `GET /v1/models` (retrieves active config pathways).
* **System Health Check**: `GET /health` (probes public route status).

---

## 🛠️ Security Handshake & Token Lookup Idiom

All endpoint requests must be signed with a pre-shared master Bearer key in the standard HTTP header:
```http
Authorization: Bearer <master_secret_token>
```

### 🤖 Safe Credentials Discovery Pattern (AI Routine)
When writing programs or scripts that need to authorize calls to this endpoint:
1. **Never hardcode raw API tokens!**
2. **First check if GCloud credentials are active** in the shell context. If you are authenticated, retrieve the master key securely from GCP Secret Manager using this exact execution block:
   ```bash
   gcloud secrets versions access latest --secret="open-llm-api-auth-secret" --project="quacktastic-waffle" 2>/dev/null
   ```
3. **Fallback Dev Token**: If the Secret Manager query fails (e.g., in targeted developer environments where resources are not yet fully applied to the cloud!), default to the safe development token:
   ```
   open-llm-dev-token
   ```

---

## ⏳ Critical Rule: Cold Start Timeouts (Must Enforce!)

Because the L4 GPU container scales down to **0 active instances when idle** to maintain $0.00 infrastructure baseline billing costs, incoming requests to a spun-down node trigger serverless container startup:
* **Host Provisioning + GCSFuse Models Load**: **Takes up to 4.5 minutes (270 seconds)** for GCSFuse to map and stream checkpoints (~61s per shard segment) and PyTorch to generate static CUDA graphs.
* **Warm Node Cache Boot**: **Takes 35 to 45 seconds** if scheduled on a host with warm FUSE caches.

### 🚨 Mandatory Agent Timeout Instruction
When writing tools, HTTP integrations, or network connections that call these model endpoints, **YOU MUST CONFIGURE YOUR NETWORK READ AND REQUEST TIMEOUTS TO AT LEAST 10 MINUTES (600 SECONDS)!** Any default library timeout (like Python's standard 10-second default, or OpenAI's default 60-second limit) will exit with connection dropouts during serverless cold boots.

---

## 💻 Python Client Pattern (OpenAI SDK)

Always use the standard multi-line configuration pattern with explicit timeouts when provisioning consumer connections:

```python
import os
import subprocess
from openai import OpenAI

def get_authorized_client() -> OpenAI:
    # 1. Attempt to resolve live master key dynamically
    api_key = None
    try:
        api_key = subprocess.check_output(
            ["gcloud", "secrets", "versions", "access", "latest", 
             "--secret=open-llm-api-auth-secret", "--project=quacktastic-waffle"],
            stderr=subprocess.DEVNULL
        ).decode("utf-8").strip()
    except Exception:
        # 2. Fallback to standard developer token
        api_key = "open-llm-dev-token"
        
    # 3. Standardize client with 600-second read timeout for cold boot safety
    return OpenAI(
        base_url="http://benweitzer2.c.googlers.com:8080/v1",
        api_key=api_key,
        timeout=600.0
    )
```

---

## 🧪 Dev Sandbox Testing & Offline Mocks Loop

To facilitate fast, zero-cost developer testing of UI, SSE parser boundaries, and CSS/HTML layouts:
* **High-Fidelity Offline Mock Server**: A zero-dependency stream simulator is located at:
  [mock_vllm.py](file:///usr/local/google/home/benweitzer/.gemini/jetski/brain/610fa260-94b9-45d8-93a3-65f4151144fa/scratch/mock_vllm.py)
* **Starting Offline Sandbox Loop**:
  ```bash
  # Terminal 1: Spin up mock completions backend on :8000
  python3 /usr/local/google/home/benweitzer/.gemini/jetski/brain/610fa260-94b9-45d8-93a3-65f4151144fa/scratch/mock_vllm.py
  
  # Terminal 2: Run Go Gateway pointing to mock loop
  PORT=8080 API_AUTH_SECRET="open-llm-dev-token" DEV_MODE="true" VLLM_API_URL="http://localhost:8000" go run gateway/main.go
  ```

---

## 🔍 Diagnostics & Active Troubleshooting Routines

When you encounter connection exceptions or slow/stuck stream pipelines:
1. **Locate the Dev Environment Logs**:
   * Go Gateway standard outputs: `/tmp/open-llm-dev/gateway.log`
   * Secure OIDC Tunnel standard outputs: `/tmp/open-llm-dev/tunnel.log`
2. **Examine Live GPU Model Runner Shards Progress**:
   If the GPU container is stuck or boot probes are failing, query Cloud Logging records directly to inspect FUSE segment loading speeds:
   ```bash
   gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=open-llm-vllm" --limit=30 --project=quacktastic-waffle --format="value(textPayload)"
   ```
3. **Verify GCP State backend Locks**:
   If continuous deployment triggers fail with lock errors, identify the lock parameters and manually remove the stale GCS lock asset file:
   ```bash
   gcloud storage rm gs://tf-state-quacktastic-waffle/open-llm/state/staging/default.tflock
   ```
