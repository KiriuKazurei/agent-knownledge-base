package providers

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

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/model"
)

// GenerateStream requests provider-side streaming and forwards decoded text
// deltas synchronously. The callback supplies backpressure so a slow client
// cannot create an unbounded answer buffer. Compatible endpoints that ignore
// stream=true are accepted through a one-delta JSON fallback.
func (c *Client) GenerateStream(ctx context.Context, p model.Provider, key, prompt string, evidence []model.Evidence, onDelta func(string) error) error {
	if onDelta == nil {
		return errors.New("stream delta callback is required")
	}
	var contextText strings.Builder
	for i, item := range evidence {
		fmt.Fprintf(&contextText, "[%d] %s\n%s\n\n", i+1, item.Title, item.Text)
	}
	system := "Answer using only the supplied evidence. Cite claims with [n]. If the evidence is insufficient, say so."
	anthropic := p.Kind == "anthropic"
	var target string
	var payload any
	if anthropic {
		var err error
		target, err = endpoint(p.BaseURL, "/v1/messages")
		if err != nil {
			return err
		}
		payload = map[string]any{
			"model": p.Model, "max_tokens": 1200, "stream": true, "system": system,
			"messages": []map[string]string{{"role": "user", "content": "Evidence:\n" + contextText.String() + "\nQuestion: " + prompt}},
		}
	} else {
		var err error
		target, err = endpoint(p.BaseURL, "/v1/chat/completions")
		if err != nil {
			return err
		}
		payload = map[string]any{
			"model": p.Model, "temperature": 0.2, "stream": true,
			"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": "Evidence:\n" + contextText.String() + "\nQuestion: " + prompt}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if anthropic {
		request.Header.Set("x-api-key", key)
		request.Header.Set("anthropic-version", "2023-06-01")
	} else if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return fmt.Errorf("provider returned %s: %s", response.Status, string(message))
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		var result struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			return err
		}
		if anthropic {
			for _, part := range result.Content {
				if part.Text != "" {
					if err := onDelta(part.Text); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			return errors.New("provider returned no stream content")
		}
		return onDelta(result.Choices[0].Message.Content)
	}
	return consumeSSE(response.Body, anthropic, onDelta)
}

func consumeSSE(reader io.Reader, anthropic bool, onDelta func(string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := data.String()
		data.Reset()
		if payload == "[DONE]" {
			return nil
		}
		if anthropic {
			var event struct {
				Type  string `json:"type"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				return err
			}
			if event.Type == "error" {
				return fmt.Errorf("provider stream error: %s", event.Error.Message)
			}
			if event.Type == "content_block_delta" && event.Delta.Text != "" {
				return onDelta(event.Delta.Text)
			}
			return nil
		}
		var event struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		if event.Error.Message != "" {
			return fmt.Errorf("provider stream error: %s", event.Error.Message)
		}
		if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
			return onDelta(event.Choices[0].Delta.Content)
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}
