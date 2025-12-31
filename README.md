# bedrock-go

A Go HTTP server wrapper for AWS Bedrock Runtime API with streaming support, specifically designed for Anthropic Claude models.

## Features

- HTTP proxy server for AWS Bedrock Runtime API
- Streaming and aggregated response modes
- Flexible JSON payload support (wrapped/escaped JSON in body field)
- Configurable listeners: Unix domain socket or TCP port
- Detailed logging for debugging
- Extensible request/response handling

## Prerequisites

- Go 1.23.5 or higher
- AWS account with Bedrock access enabled
- Anthropic Claude v2 model access in AWS Bedrock
- AWS credentials configured via:
  - Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`)
  - `~/.aws/credentials` file
  - IAM role (for EC2/Lambda)

## Installation

```bash
# Clone the repository
git clone https://github.com/anujva/bedrock-go.git
cd bedrock-go

# Download dependencies
go mod download

# Build the binary
go build -o bedrock-go
```

## Configuration

Set the following environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP port to listen on | `8080` |
| `SIGNER_SOCKET` | Unix domain socket path (overrides PORT) | - |
| `AWS_REGION` | AWS region for Bedrock | `us-east-1` |

## Usage

### Running the Server

**TCP Mode:**
```bash
export PORT=8080
./bedrock-go
```

**Unix Domain Socket Mode:**
```bash
export SIGNER_SOCKET=/tmp/bedrock.sock
./bedrock-go
```

### Making Requests

The server accepts POST requests to `/invoke` with a JSON payload.

**Request Format:**
```json
{
  "url": "https://bedrock-runtime.us-east-1.amazonaws.com",
  "method": "POST",
  "headers": {
    "content-type": "application/json",
    "accept": "application/json"
  },
  "body": "{\"system\":\"You are a helpful assistant\",\"anthropic_version\":\"bedrock-2023-05-31\",\"messages\":[{\"role\":\"user\",\"content\":\"What is the capital of France?\"}]}"
}
```

**Example with curl:**
```bash
curl -X POST http://localhost:8080/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://bedrock-runtime.us-east-1.amazonaws.com",
    "method": "POST",
    "headers": {"content-type": "application/json"},
    "body": "{\"system\":\"You are a helpful assistant\",\"anthropic_version\":\"bedrock-2023-05-31\",\"messages\":[{\"role\":\"user\",\"content\":\"What is the capital of France?\"}]}"
  }'
```

**Simplified format (without body field):**
```bash
curl -X POST http://localhost:8080/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "method": "What is the capital of France?"
  }'
```

## API Reference

### POST /invoke

Invokes the Anthropic Claude model via AWS Bedrock.

**Request Body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `url` | string | No | Target URL (informational only) |
| `method` | string | No | HTTP method (informational only) |
| `headers` | object | No | Request headers |
| `body` | string | No* | JSON string containing actual request payload |

*If `body` is not provided, the outer fields are used as a fallback.

**Inner Payload (when using `body` field):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `system` | string | No | System prompt for the model |
| `anthropic_version` | string | No | API version |
| `messages` | array | No* | Array of message objects with `role` and `content` |
| `prompt` | string | No* | Direct prompt string (alternative to messages) |

*Either `messages` or `prompt` must be provided.

**Response:**

In streaming mode (default), responses are streamed as plain text chunks. In aggregation mode, the complete response is returned at once.

## Model Configuration

Default model settings (configurable in `main.go`):

- **Model ID**: `anthropic.claude-v2`
- **Max Tokens**: 200
- **Temperature**: 0.5
- **Stop Sequences**: `\n\nHuman:`
- **Request Timeout**: 30 seconds

To enable aggregated mode instead of streaming, set `globalStreamingMode = false` in `main.go:263`.

## Development

### Project Structure

```
.
├── main.go          # Main server implementation
├── go.mod           # Go module dependencies
├── go.sum           # Dependency checksums
└── payload.json     # Example request payload
```

### Key Components

- **InvokeModelWithResponseStreamWrapper**: Encapsulates the Bedrock Runtime client and streaming logic
- **StreamingOutputHandler**: Callback interface for processing response chunks
- **invokeHandler**: HTTP handler that processes incoming requests and manages streaming/aggregation

### Logging

The server logs detailed information about:
- Incoming requests and payload structure
- AWS configuration loading
- Bedrock API calls
- Stream processing and chunk handling
- Errors and failures

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
