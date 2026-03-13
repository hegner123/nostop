// Package api provides types and client for the Claude Messages API.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamReader reads Server-Sent Events from a Claude API streaming response.
// It parses the SSE format and returns typed StreamEvent objects.
type StreamReader struct {
	reader *bufio.Reader
	body   io.ReadCloser
	closed bool
}

// NewStreamReader creates a new StreamReader from an io.ReadCloser (typically an HTTP response body).
func NewStreamReader(body io.ReadCloser) *StreamReader {
	return &StreamReader{
		reader: bufio.NewReader(body),
		body:   body,
	}
}

// Next reads the next streaming event from the response.
// Returns io.EOF when the stream is complete.
// Returns an error if the stream contains an error event or parsing fails.
func (s *StreamReader) Next() (*StreamEvent, error) {
	if s.closed {
		return nil, io.EOF
	}

	for {
		eventType, data, err := s.readSSEEvent()
		if err != nil {
			return nil, err
		}

		// Skip empty events
		if eventType == "" && data == "" {
			continue
		}

		// Parse the event
		event, err := s.parseEvent(eventType, data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse event: %w", err)
		}

		// Skip ping events
		if event.Type == StreamEventPing {
			continue
		}

		// Check for error events
		if event.Type == StreamEventError && event.Error != nil {
			return event, &APIError{
				Type:         "error",
				ErrorDetails: *event.Error,
			}
		}

		return event, nil
	}
}

// Close closes the underlying reader.
func (s *StreamReader) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
}

// Collect reads all events and assembles them into a final Response.
// This is a convenience method for when you don't need to process events individually.
// It closes the stream when done.
func (s *StreamReader) Collect() (*Response, error) {
	defer s.Close()

	var response *Response
	var contentBlocks []ContentBlock
	var currentBlockIndex int = -1
	var textBuilder strings.Builder
	var jsonBuilder strings.Builder
	var thinkingBuilder strings.Builder

	for {
		event, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		switch event.Type {
		case StreamEventMessageStart:
			if event.Message != nil {
				response = event.Message
				// Initialize content blocks slice
				contentBlocks = make([]ContentBlock, 0)
			}

		case StreamEventContentBlockStart:
			if event.ContentBlock != nil {
				currentBlockIndex++
				// Add the new content block
				contentBlocks = append(contentBlocks, *event.ContentBlock)
				// Reset builders for new block
				textBuilder.Reset()
				jsonBuilder.Reset()
				thinkingBuilder.Reset()
			}

		case StreamEventContentBlockDelta:
			if event.Delta != nil && currentBlockIndex >= 0 && currentBlockIndex < len(contentBlocks) {
				switch event.Delta.Type {
				case "text_delta":
					textBuilder.WriteString(event.Delta.Text)
					contentBlocks[currentBlockIndex].Text = textBuilder.String()
				case "input_json_delta":
					jsonBuilder.WriteString(event.Delta.PartialJSON)
					// Store partial JSON - will be parsed at end
					contentBlocks[currentBlockIndex].Input = json.RawMessage(jsonBuilder.String())
				case "thinking_delta":
					thinkingBuilder.WriteString(event.Delta.Thinking)
					contentBlocks[currentBlockIndex].Thinking = thinkingBuilder.String()
				}
			}

		case StreamEventContentBlockStop:
			// Block is complete, nothing special to do

		case StreamEventMessageDelta:
			if response != nil && event.Delta != nil {
				response.StopReason = event.Delta.StopReason
				response.StopSequence = event.Delta.StopSequence
			}
			if response != nil && event.Usage != nil {
				response.Usage = *event.Usage
			}

		case StreamEventMessageStop:
			// Stream is complete
		}
	}

	if response == nil {
		return nil, errors.New("no message_start event received")
	}

	response.Content = contentBlocks
	return response, nil
}

// readSSEEvent reads a single SSE event from the stream.
// SSE format: lines starting with "event:" and "data:", separated by blank lines.
func (s *StreamReader) readSSEEvent() (eventType string, data string, err error) {
	var dataLines []string

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				// If we have data, return it before EOF
				if eventType != "" || len(dataLines) > 0 {
					return eventType, strings.Join(dataLines, "\n"), nil
				}
				return "", "", io.EOF
			}
			return "", "", fmt.Errorf("failed to read line: %w", err)
		}

		// Trim the newline
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		// Empty line marks end of event
		if line == "" {
			if eventType != "" || len(dataLines) > 0 {
				return eventType, strings.Join(dataLines, "\n"), nil
			}
			continue
		}

		// Parse the line
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
		// Ignore comments (lines starting with :) and other lines
	}
}

// parseEvent parses an SSE event into a StreamEvent.
func (s *StreamReader) parseEvent(eventType, data string) (*StreamEvent, error) {
	event := &StreamEvent{
		Type: StreamEventType(eventType),
	}

	// Trim leading space from data (SSE spec allows optional space after colon)
	data = strings.TrimPrefix(data, " ")

	if data == "" {
		return event, nil
	}

	// Parse the JSON data based on event type
	switch event.Type {
	case StreamEventMessageStart:
		var payload struct {
			Message *Response `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse message_start: %w", err)
		}
		event.Message = payload.Message

	case StreamEventContentBlockStart:
		var payload struct {
			Index        int           `json:"index"`
			ContentBlock *ContentBlock `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse content_block_start: %w", err)
		}
		event.Index = &payload.Index
		event.ContentBlock = payload.ContentBlock

	case StreamEventContentBlockDelta:
		var payload struct {
			Index int          `json:"index"`
			Delta *StreamDelta `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse content_block_delta: %w", err)
		}
		event.Index = &payload.Index
		event.Delta = payload.Delta

	case StreamEventContentBlockStop:
		var payload struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse content_block_stop: %w", err)
		}
		event.Index = &payload.Index

	case StreamEventMessageDelta:
		var payload struct {
			Delta *StreamDelta `json:"delta"`
			Usage *Usage       `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse message_delta: %w", err)
		}
		event.Delta = payload.Delta
		event.Usage = payload.Usage

	case StreamEventMessageStop:
		// No payload to parse

	case StreamEventPing:
		// No payload to parse

	case StreamEventError:
		var payload struct {
			Error *ErrorDetail `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, fmt.Errorf("failed to parse error: %w", err)
		}
		event.Error = payload.Error
	}

	return event, nil
}

// Client methods for streaming

// Stream sends a request to the Claude API and returns a StreamReader for processing events.
// The caller is responsible for closing the StreamReader when done.
// If a model resolver is configured, model-not-found errors trigger automatic
// fallback to alternative models in the same capability tier.
func (c *Client) Stream(ctx context.Context, req *Request) (*StreamReader, error) {
	// Enable streaming
	streamTrue := true
	req.Stream = &streamTrue

	return withModelFallback(c.resolver, req, func() (*StreamReader, error) {
		return c.streamOnce(ctx, req)
	})
}

// streamOnce performs a single streaming request without model fallback.
func (c *Client) streamOnce(ctx context.Context, req *Request) (*StreamReader, error) {
	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("read error response: %w", readErr)
		}
		return nil, c.parseErrorWithHeaders(resp, respBody)
	}

	// Verify we got an SSE response
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		defer resp.Body.Close()
		return nil, fmt.Errorf("unexpected content type: %s", contentType)
	}

	return NewStreamReader(resp.Body), nil
}

// StreamCallback is a function called for each streaming event.
// Return a non-nil error to stop the stream.
type StreamCallback func(event *StreamEvent) error

// StreamWithCallback sends a streaming request and calls the callback for each event.
// This is a convenience method that handles the streaming loop.
func (c *Client) StreamWithCallback(ctx context.Context, req *Request, callback StreamCallback) (*Response, error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var response *Response
	var contentBlocks []ContentBlock
	var currentBlockIndex int = -1
	var textBuilder strings.Builder
	var jsonBuilder strings.Builder
	var thinkingBuilder strings.Builder

	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}

		// Call the callback
		if err := callback(event); err != nil {
			return nil, err
		}

		// Build up the response
		switch event.Type {
		case StreamEventMessageStart:
			if event.Message != nil {
				response = event.Message
				contentBlocks = make([]ContentBlock, 0)
			}

		case StreamEventContentBlockStart:
			if event.ContentBlock != nil {
				currentBlockIndex++
				contentBlocks = append(contentBlocks, *event.ContentBlock)
				textBuilder.Reset()
				jsonBuilder.Reset()
				thinkingBuilder.Reset()
			}

		case StreamEventContentBlockDelta:
			if event.Delta != nil && currentBlockIndex >= 0 && currentBlockIndex < len(contentBlocks) {
				switch event.Delta.Type {
				case "text_delta":
					textBuilder.WriteString(event.Delta.Text)
					contentBlocks[currentBlockIndex].Text = textBuilder.String()
				case "input_json_delta":
					jsonBuilder.WriteString(event.Delta.PartialJSON)
					contentBlocks[currentBlockIndex].Input = json.RawMessage(jsonBuilder.String())
				case "thinking_delta":
					thinkingBuilder.WriteString(event.Delta.Thinking)
					contentBlocks[currentBlockIndex].Thinking = thinkingBuilder.String()
				}
			}

		case StreamEventMessageDelta:
			if response != nil && event.Delta != nil {
				response.StopReason = event.Delta.StopReason
				response.StopSequence = event.Delta.StopSequence
			}
			if response != nil && event.Usage != nil {
				response.Usage = *event.Usage
			}
		}
	}

	if response == nil {
		return nil, errors.New("no message_start event received")
	}

	response.Content = contentBlocks
	return response, nil
}
