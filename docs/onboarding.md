# 🚀 Onboarding Guide: Downstream Application Integrations

Welcome to the **Open-LLM Gateway & Inference** developer framework! This guide provides standard REST connection parameters, security validation handshakes, multi-language client templates, and connection timeout rules to help your application consume scale-to-zero LLM resources securely and efficiently.

---

## 🏗️ Architectural Integration Overview

 downstream clients connect directly to the **Go API Gateway** public endpoint. The Gateway manages standard CORS, validates pre-shared credentials, and secure-proxies completions requests over high-speed GCP VPC network paths to the isolated, scale-to-zero GPU vLLM inference node in real-time.

```
+------------------------------------+
|     Your Downstream Application    |
| (e.g. sound-profile-builder sub)  |
+-----------------+------------------+
                  |
                  | 1. HTTP POST /v1/chat/completions
                  |    Headers: Authorization: Bearer <secret_key>
                  v
+-----------------+------------------+
|           Go API Gateway           | (Warm CPU proxy, public edge endpoint)
+-----------------+------------------+
                  |
                  | 2. VPC Egress Route (mTLS & Google IAM Signed)
                  v
+-----------------+------------------+
|         vLLM GPU Container         | (Private IP endpoint, scale-to-zero GPU)
+------------------------------------+
```

---

## 🗝️ Security Handshake & Secrets Management

Every request to secure completions endpoints must be authorized via a pre-shared master Bearer token in the standard HTTP header:
```http
Authorization: Bearer YOUR_PRE_SHARED_SECRET_KEY
```

### Fetching the Production Key
For automated staging or pipeline orchestrations, system administrators can retrieve the active key version securely from Google Secret Manager:
```bash
gcloud secrets versions access latest --secret="open-llm-api-auth-secret" --project="quacktastic-waffle"
```

---

## ⏳ Mandatory Cold-Start Grace Strategies

Because the vLLM L4 GPU container scales down to **zero active instances when idle** to maintain perfect $0.00 baseline infrastructure costs, initial requests after a period of inactivity will trigger a serverless cold boot:
* **Fresh Host Cold Start**: **Takes approx. 4.5 minutes (270 seconds)** while the host mounts GCSFuse, streams the 14GB of checkpoints (~61s per shard split), and maps CUDA graphs paths into memory VRAM.
* **Cached Host Warm Start**: **Takes approx. 35 to 45 seconds** if scheduled onto a warm physical node with active cache segments.

### 🚨 CRITICAL CLIENT RULE: Read Timeouts
To prevent client-side failures and drop-outs during cold boot cycles, **all downstream application frameworks, network routers, and HTTP clients MUST configure their network read/connection timeouts to at least 10 minutes (600 seconds)!**

---

## 💻 Language Integration Templates

Our system is **strictly OpenAI REST API compliant**, allowing you to use standard off-the-shelf SDK packages directly by overriding the base connection parameters:

### 1. Python (OpenAI SDK)
```python
from openai import OpenAI

# Initialize standard client pointing to our Gateway coordinates with safe timeouts
client = OpenAI(
    base_url="http://benweitzer2.c.googlers.com:8080/v1",  # Local Dev FQDN (or production URL)
    api_key="open-llm-dev-token",                          # Pre-shared Bearer key
    timeout=600.0,                                         # 10-minute timeout for cold boots!
)

# Stream responses in real-time (highly recommended for natural conversational loops)
stream = client.chat.completions.create(
    model="qwen-2.5-7b-instruct",
    messages=[
        {"role": "system", "content": "You are a brief assistant."},
        {"role": "user", "content": "Explain serverless GPU hosting in a sentence."}
    ],
    stream=True
)

for chunk in stream:
    if chunk.choices[0].delta.content is not None:
        print(chunk.choices[0].delta.content, end="", flush=True)
```

### 2. Node.js (OpenAI JS SDK)
```javascript
import OpenAI from 'openai';

const openai = new OpenAI({
  baseURL: 'http://benweitzer2.c.googlers.com:8080/v1',
  apiKey: 'open-llm-dev-token',
  timeout: 600000, // 10 minutes (in milliseconds)
});

async function main() {
  const stream = await openai.chat.completions.create({
    model: 'qwen-2.5-7b-instruct',
    messages: [{ role: 'user', content: 'What are the benefits of scale-to-zero L4 GPUs?' }],
    stream: true,
  });
  
  for await (const chunk of stream) {
    process.stdout.write(chunk.choices[0]?.delta?.content || '');
  }
}

main().catch(console.error);
```

### 3. Go (Raw HTTP Client with SSE Stream Parser)
```go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request structure complying with standard OpenAI completions schema
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

func main() {
	url := "http://benweitzer2.c.googlers.com:8080/v1/chat/completions"
	payload := ChatRequest{
		Model: "qwen-2.5-7b-instruct",
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello from Go!"},
		},
		Stream: true,
	}

	jsonBytes, _ := json.Marshal(payload)

	// Configure transport with generous 10-minute read timeout for safe cold starts
	client := &http.Client{
		Timeout: 10 * time.Minute,
	}

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer open-llm-dev-token")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("❌ Connection Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ Server Error (%d): %s\n", resp.StatusCode, string(body))
		return
	}

	// Read standard Server-Sent Events line-by-line
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		
		line = strings.TrimSpace(line)
		if line == "" || line == "data: [DONE]" {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			var chunk map[string]interface{}
			json.Unmarshal([]byte(line[6:]), &chunk)
			
			// Parse incremental content delta blocks safely
			if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if delta, ok := choice["delta"].(map[string]interface{}); ok {
						if content, ok := delta["content"].(string); ok {
							fmt.Print(content)
						}
					}
				}
			}
		}
	}
}
```

---

## 📡 Diagnostic Endpoints & Monitoring

Downstream client tools and integrations can query the basic system checks and live parameters via:
* **Gateway Health Status Probe**: Send standard GET requests to `/health` to verify public route operations.
* **Models Zoo list**: Send authorized GET requests to `/v1/models` to discover current weights paths configuration.
