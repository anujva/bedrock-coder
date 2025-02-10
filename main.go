package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// OuterPayload represents a wrapper that might contain a "body" field as an escaped JSON string.
type OuterPayload struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// InputPayload represents the inner payload with the actual fields we care about.
type InputPayload struct {
	System           string            `json:"system"`
	AnthropicVersion string            `json:"anthropic_version"`
	Headers          map[string]string `json:"headers"`
	Prompt           string            `json:"prompt,omitempty"`
	Messages         []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages,omitempty"`
}

// Request defines the JSON payload sent to Bedrock.
type Request struct {
	Prompt            string   `json:"prompt"`
	MaxTokensToSample int      `json:"max_tokens_to_sample"`
	Temperature       float64  `json:"temperature,omitempty"`
	StopSequences     []string `json:"stop_sequences,omitempty"`
}

// Response defines the JSON structure of each response chunk.
type Response struct {
	Completion string `json:"completion"`
}

// StreamingOutputHandler is a callback that processes each chunk.
type StreamingOutputHandler func(ctx context.Context, part []byte) error

// InvokeModelWithResponseStreamWrapper encapsulates the BedrockRuntime client.
type InvokeModelWithResponseStreamWrapper struct {
	BedrockRuntimeClient *bedrockruntime.Client
}

// InvokeModelWithResponseStream sends the prompt (with required enclosures)
// to the model using the InvokeModelWithResponseStream API and processes the stream
// using the provided handler. It returns the combined response.
func (wrapper InvokeModelWithResponseStreamWrapper) InvokeModelWithResponseStream(
	ctx context.Context,
	prompt string,
	handler StreamingOutputHandler,
) (Response, error) {
	modelId := "anthropic.claude-v2"
	// Anthropic Claude requires the prompt to be enclosed.
	prefix := "Human: "
	postfix := "\n\nAssistant:"
	fullPrompt := prefix + prompt + postfix

	log.Printf("[InvokeModelWithResponseStream] Using full prompt: %q", fullPrompt)

	// Prepare the request payload.
	reqPayload := Request{
		Prompt:            fullPrompt,
		MaxTokensToSample: 200,
		Temperature:       0.5,
		StopSequences:     []string{"\n\nHuman:"},
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		log.Printf("[InvokeModelWithResponseStream] Error marshaling payload: %v", err)
		return Response{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	log.Printf("[InvokeModelWithResponseStream] Marshaled payload: %s", string(body))

	streamInput := &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(modelId),
		ContentType: aws.String("application/json"),
		Body:        body,
	}

	log.Printf("[InvokeModelWithResponseStream] Calling Bedrock API")
	output, err := wrapper.BedrockRuntimeClient.InvokeModelWithResponseStream(ctx, streamInput)
	if err != nil {
		log.Printf("[InvokeModelWithResponseStream] Error invoking model: %v", err)
		return Response{}, fmt.Errorf("failed to invoke model with streaming: %w", err)
	}
	log.Printf("[InvokeModelWithResponseStream] Received streaming response")

	var combinedResult string
	for event := range output.GetStream().Events() {
		switch v := event.(type) {
		case *types.ResponseStreamMemberChunk:
			log.Printf("[Stream] Received chunk of length %d", len(v.Value.Bytes))
			var respChunk Response
			decoder := json.NewDecoder(bytes.NewReader(v.Value.Bytes))
			if err := decoder.Decode(&respChunk); err != nil {
				log.Printf("[Stream] Error decoding chunk: %v", err)
				return respChunk, fmt.Errorf("failed to decode chunk: %w", err)
			}
			log.Printf("[Stream] Decoded chunk: %q", respChunk.Completion)
			if err := handler(ctx, []byte(respChunk.Completion)); err != nil {
				log.Printf("[Stream] Handler error: %v", err)
				return respChunk, err
			}
			combinedResult += respChunk.Completion
		case *types.UnknownUnionMember:
			log.Printf("[Stream] Received unknown tag: %s", v.Tag)
		default:
			log.Printf("[Stream] Received unexpected event type")
		}
	}
	log.Printf(
		"[InvokeModelWithResponseStream] Finished stream; combined length: %d",
		len(combinedResult),
	)
	return Response{Completion: combinedResult}, nil
}

// globalStreamingMode determines whether the server streams or aggregates responses.
// Set to true to stream; false to send the full response at once.
var globalStreamingMode bool

// invokeHandler is an HTTP handler that decodes a JSON payload (which may be wrapped inside a "body" field),
// extracts the prompt (from the first message or the top-level prompt), and then streams or aggregates
// the response based on the global mode.
func invokeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Handler] Received request from %s", r.RemoteAddr)

	// First, decode the outer payload.
	var outer OuterPayload
	if err := json.NewDecoder(r.Body).Decode(&outer); err != nil {
		http.Error(w, "Invalid outer payload: "+err.Error(), http.StatusBadRequest)
		log.Printf("[Handler] Error decoding outer payload: %v", err)
		return
	}
	r.Body.Close()
	log.Printf("[Handler] Outer payload: %+v", outer)

	var inner InputPayload
	// If the "body" field is present, unmarshal it.
	if outer.Body != "" {
		log.Printf("[Handler] Found 'body' field; attempting to unmarshal inner payload")
		if err := json.Unmarshal([]byte(outer.Body), &inner); err != nil {
			http.Error(w, "Failed to unmarshal inner body: "+err.Error(), http.StatusBadRequest)
			log.Printf("[Handler] Error unmarshaling inner payload: %v", err)
			return
		}
		log.Printf("[Handler] Inner payload: %+v", inner)
	} else {
		// Otherwise, use outer payload fields as fallback.
		log.Printf("[Handler] No 'body' field; using outer payload as fallback")
		inner = InputPayload{
			Prompt:  outer.Method, // fallback; adjust as needed
			Headers: outer.Headers,
		}
	}

	// Extract prompt: use first message if available, otherwise use top-level prompt.
	var prompt string
	if len(inner.Messages) > 0 {
		prompt = inner.Messages[0].Content
		log.Printf("[Handler] Extracted prompt from messages: %q", prompt)
	} else if inner.Prompt != "" {
		prompt = inner.Prompt
		log.Printf("[Handler] Extracted prompt from top-level prompt: %q", prompt)
	} else {
		http.Error(w, "No messages or prompt provided", http.StatusBadRequest)
		log.Printf("[Handler] No messages or prompt provided")
		return
	}

	// Load AWS configuration.
	log.Printf("[Handler] Loading AWS configuration")
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion("us-east-1"))
	if err != nil {
		http.Error(w, "Failed to load AWS config: "+err.Error(), http.StatusInternalServerError)
		log.Printf("[Handler] Error loading AWS config: %v", err)
		return
	}

	client := bedrockruntime.NewFromConfig(cfg)
	wrapper := InvokeModelWithResponseStreamWrapper{
		BedrockRuntimeClient: client,
	}
	log.Printf("[Handler] AWS client and wrapper ready")

	// Create a context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if globalStreamingMode {
		// Streaming mode: set headers and use a flushable writer.
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Transfer-Encoding", "chunked")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			log.Printf("[Handler] ResponseWriter does not support streaming")
			return
		}
		log.Printf("[Handler] Streaming mode enabled; sending chunks immediately")
		streamHandler := func(ctx context.Context, part []byte) error {
			log.Printf("[Handler][Stream] Writing chunk: %q", part)
			if _, err := w.Write(part); err != nil {
				log.Printf("[Handler][Stream] Error writing chunk: %v", err)
				return err
			}
			flusher.Flush()
			return nil
		}
		_, err = wrapper.InvokeModelWithResponseStream(ctx, prompt, streamHandler)
		if err != nil {
			http.Error(
				w,
				"Failed to stream Bedrock response: "+err.Error(),
				http.StatusInternalServerError,
			)
			log.Printf("[Handler] Streaming invocation error: %v", err)
			return
		}
		log.Printf("[Handler] Finished streaming invocation")
	} else {
		// Aggregation mode: accumulate chunks, then send full response.
		log.Printf("[Handler] Aggregation mode enabled; accumulating full response")
		var aggregatedOutput string
		streamHandler := func(ctx context.Context, part []byte) error {
			log.Printf("[Handler][Aggregate] Received chunk: %q", part)
			aggregatedOutput += string(part)
			return nil
		}
		_, err := wrapper.InvokeModelWithResponseStream(ctx, prompt, streamHandler)
		if err != nil {
			http.Error(w, "Failed to get aggregated response: "+err.Error(), http.StatusInternalServerError)
			log.Printf("[Handler] Aggregation error: %v", err)
			return
		}
		log.Printf("[Handler] Aggregated response length: %d", len(aggregatedOutput))
		w.Header().Set("Content-Type", "text/plain")
		if _, err := w.Write([]byte(aggregatedOutput)); err != nil {
			log.Printf("[Handler] Error writing aggregated response: %v", err)
		}
	}
}

func main() {
	// Decide the mode once at startup.
	globalStreamingMode = true // currently going with always streaming

	mux := http.NewServeMux()
	mux.HandleFunc("/sign_and_send", invokeHandler)

	// Listen on Unix domain socket if SIGNER_SOCKET is set.
	if socket := os.Getenv("SIGNER_SOCKET"); socket != "" {
		if _, err := os.Stat(socket); err == nil {
			if err := os.Remove(socket); err != nil {
				log.Fatalf("Failed to remove existing socket %s: %v", socket, err)
			}
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			log.Fatalf("Failed to listen on unix socket %s: %v", socket, err)
		}
		log.Printf("Bedrock invoker running on unix socket %s", socket)
		if err := http.Serve(listener, mux); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	} else {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		addr := ":" + port
		log.Printf("Bedrock invoker running on port %s", port)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}
}
