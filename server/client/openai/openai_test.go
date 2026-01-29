package openai

import (
	"context"
	"cursortab/assert"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDoCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method, "HTTP method")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"), "Content-Type header")

		body, _ := io.ReadAll(r.Body)
		var req CompletionRequest
		json.Unmarshal(body, &req)

		assert.False(t, req.Stream, "Stream should be false")

		resp := CompletionResponse{
			ID:    "test-id",
			Model: req.Model,
			Choices: []struct {
				Index        int    `json:"index"`
				Text         string `json:"text"`
				Logprobs     any    `json:"logprobs"`
				FinishReason string `json:"finish_reason"`
			}{
				{Index: 0, Text: "completion text", FinishReason: "stop"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	resp, err := client.DoCompletion(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	})

	assert.NoError(t, err, "DoCompletion")
	assert.Equal(t, "test-id", resp.ID, "ID")
	assert.Equal(t, 1, len(resp.Choices), "Choices length")
	assert.Equal(t, "completion text", resp.Choices[0].Text, "Text")
}

func TestDoCompletion_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	_, err := client.DoCompletion(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	})

	assert.Error(t, err, "Expected error for HTTP 500")
	assert.True(t, strings.Contains(err.Error(), "500"), "Error should mention status code")
}

func TestDoCompletion_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	_, err := client.DoCompletion(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	})

	assert.Error(t, err, "Expected error for invalid JSON")
}

func TestDoLineStream_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"), "Accept header")

		flusher, ok := w.(http.Flusher)
		assert.True(t, ok, "ResponseWriter should support Flusher")

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send SSE events
		events := []string{
			`{"id":"1","choices":[{"text":"line 1\n","index":0}]}`,
			`{"id":"2","choices":[{"text":"line 2\n","index":0}]}`,
		}
		for _, evt := range events {
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	var lines []string
	for line := range stream.LinesChan() {
		lines = append(lines, line)
	}

	result := <-stream.DoneChan()

	assert.Equal(t, 2, len(lines), "lines length")
	assert.Equal(t, "line 1", lines[0], "first line")
	assert.Equal(t, "line 2", lines[1], "second line")
	assert.Equal(t, "line 1\nline 2\n", result.Text, "result text")
}

func TestDoLineStream_MaxLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send more lines than maxLines
		for i := 1; i <= 10; i++ {
			evt := `{"id":"1","choices":[{"text":"line\n","index":0}]}`
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 3, nil) // maxLines = 3

	var lines []string
	for line := range stream.LinesChan() {
		lines = append(lines, line)
	}

	result := <-stream.DoneChan()

	assert.Equal(t, 3, len(lines), "lines length")
	assert.True(t, result.StoppedEarly, "StoppedEarly")
}

func TestDoLineStream_StopToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"id":"1","choices":[{"text":"hello","index":0}]}`,
			`{"id":"2","choices":[{"text":"<STOP>more","index":0}]}`,
		}
		for _, evt := range events {
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, []string{"<STOP>"})

	var lines []string
	for line := range stream.LinesChan() {
		lines = append(lines, line)
	}

	result := <-stream.DoneChan()

	// Should stop at <STOP> token
	assert.Equal(t, "stop", result.FinishReason, "FinishReason")
	assert.Equal(t, "hello", result.Text, "Text")
}

func TestDoLineStream_Cancel(t *testing.T) {
	started := make(chan bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		close(started)
		for i := 0; i < 100; i++ {
			evt := `{"id":"1","choices":[{"text":"x","index":0}]}`
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	// Wait for server to start sending data
	<-started
	time.Sleep(50 * time.Millisecond)
	stream.Cancel()

	result := <-stream.DoneChan()

	// Result should indicate the stream was stopped (either cancelled or incomplete)
	assert.True(t, result.FinishReason == "cancelled" || result.FinishReason == "", "FinishReason should be cancelled or empty")
}

func TestDoTokenStream_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		tokens := []string{"hello", " ", "world"}
		for _, tok := range tokens {
			evt := `{"id":"1","choices":[{"text":"` + tok + `","index":0}]}`
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
		}
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoTokenStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	var texts []string
	for text := range stream.LinesChan() {
		texts = append(texts, text)
	}

	result := <-stream.DoneChan()

	// Token stream sends cumulative text
	assert.GreaterOrEqual(t, len(texts), 3, "emissions count")
	assert.Equal(t, "hello world", result.Text, "final text")
}

func TestDoTokenStream_MaxChars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send lots of tokens
		for i := 0; i < 50; i++ {
			evt := `{"id":"1","choices":[{"text":"word ","index":0}]}`
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoTokenStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 20, nil) // maxChars = 20

	for range stream.LinesChan() {
		// Drain channel
	}

	result := <-stream.DoneChan()

	assert.True(t, result.StoppedEarly, "StoppedEarly")
	assert.GreaterOrEqual(t, len(result.Text), 20, "text length")
}

func TestDoTokenStream_StopToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`{"id":"1","choices":[{"text":"hello","index":0}]}`,
			`{"id":"2","choices":[{"text":"\nmore","index":0}]}`,
		}
		for _, evt := range events {
			w.Write([]byte("data: " + evt + "\n\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoTokenStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, []string{"\n"})

	for range stream.LinesChan() {
		// Drain
	}

	result := <-stream.DoneChan()

	assert.Equal(t, "stop", result.FinishReason, "FinishReason")
	assert.Equal(t, "hello", result.Text, "Text")
}

func TestDoLineStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	for range stream.LinesChan() {
		// Should be empty
	}

	result := <-stream.DoneChan()

	assert.Equal(t, "error", result.FinishReason, "FinishReason")
}

func TestDoLineStream_SkipsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send invalid JSON followed by valid
		w.Write([]byte("data: not json\n\n"))
		flusher.Flush()
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"text\":\"valid\\n\",\"index\":0}]}\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	var lines []string
	for line := range stream.LinesChan() {
		lines = append(lines, line)
	}

	<-stream.DoneChan()

	assert.Equal(t, 1, len(lines), "lines length (invalid JSON skip)")
}

func TestDoLineStream_SkipsComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		w.Write([]byte(": this is a comment\n\n"))
		flusher.Flush()
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"text\":\"text\\n\",\"index\":0}]}\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx := context.Background()

	stream := client.DoLineStream(ctx, &CompletionRequest{
		Model:  "test-model",
		Prompt: "hello",
	}, 0, nil)

	var lines []string
	for line := range stream.LinesChan() {
		lines = append(lines, line)
	}

	<-stream.DoneChan()

	assert.Equal(t, 1, len(lines), "lines length (comments skip)")
}
