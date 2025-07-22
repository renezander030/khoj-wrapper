package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OpenAI API structures
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Arguments   json.RawMessage        `json:"arguments,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallId string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type KhojRequest struct {
	Q              string     `json:"q"`
	ConversationID string     `json:"conversation_id,omitempty"`
	Stream         bool       `json:"stream"`
	ClientID       string     `json:"client_id,omitempty"`
	Files          []KhojFile `json:"files,omitempty"` // Add this line
}

// Add this new struct
type KhojFile struct {
	Name     string `json:"name"`
	Content  string `json:"content"`
	FileType string `json:"file_type"`
	Size     int    `json:"size"`
}

type KhojResponse struct {
	Response       string                   `json:"response"`
	ConversationID string                   `json:"conversation_id"`
	Context        []map[string]interface{} `json:"context,omitempty"`
	OnlineContext  map[string]interface{}   `json:"online_context,omitempty"`
	CreatedBy      string                   `json:"created_by"`
	ByKhoj         bool                     `json:"by_khoj"`
	Intent         map[string]interface{}   `json:"intent,omitempty"`
	Detail         map[string]interface{}   `json:"detail,omitempty"`
}

type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type MCPSession struct {
	Name    string    `json:"name"`
	Command string    `json:"command"`
	Tools   []MCPTool `json:"tools"`
	Process *exec.Cmd `json:"-"`
}

type KhojProvider struct {
	APIBase    string
	APIKey     string
	HTTPClient *http.Client
	MCPManager *MCPToolManager
}

type MCPToolManager struct {
	Sessions map[string]*MCPSession
}

const continueConversationID = "96b2b033-f967-4fbd-af86-dd92001538a7"

type ChatCompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	Purpose     string    `json:"purpose,omitempty"`
}

// Generate contextual diff showing only changed sections with context
func generateContextualDiff(originalLines, modifiedLines []string, filename string) string {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// Find changed sections
	changes := findChangedSections(originalLines, modifiedLines)

	if len(changes) == 0 {
		// No changes found
		diff.WriteString("@@ -0,0 +0,0 @@\n")
		return diff.String()
	}

	// Generate hunks for each changed section
	for _, change := range changes {
		contextLines := 3 // Show 3 lines of context before/after changes

		startOrig := max(0, change.OrigStart-contextLines)
		endOrig := min(len(originalLines), change.OrigEnd+contextLines)
		startMod := max(0, change.ModStart-contextLines)
		endMod := min(len(modifiedLines), change.ModEnd+contextLines)

		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			startOrig+1, endOrig-startOrig,
			startMod+1, endMod-startMod))

		// Show context before changes
		for i := startOrig; i < change.OrigStart && i < len(originalLines); i++ {
			diff.WriteString(fmt.Sprintf(" %s\n", originalLines[i]))
		}

		// Show removed lines
		for i := change.OrigStart; i < change.OrigEnd && i < len(originalLines); i++ {
			diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
		}

		// Show added lines
		for i := change.ModStart; i < change.ModEnd && i < len(modifiedLines); i++ {
			diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
		}

		// Show context after changes
		for i := change.OrigEnd; i < endOrig && i < len(originalLines); i++ {
			diff.WriteString(fmt.Sprintf(" %s\n", originalLines[i]))
		}
	}

	return diff.String()
}

// Structure to represent a changed section
type ChangeSection struct {
	OrigStart, OrigEnd int
	ModStart, ModEnd   int
}

// Find sections that have changed between original and modified - HANDLES MID-FILE CHANGES
func findChangedSections(originalLines, modifiedLines []string) []ChangeSection {
	var changes []ChangeSection

	// Find the first line that differs
	i := 0
	minLen := min(len(originalLines), len(modifiedLines))

	// Scan through both files to find first difference
	for i < minLen {
		if originalLines[i] != modifiedLines[i] {
			// Found start of change at line i
			changeStart := i

			// Now find the end of this change block
			origEnd := i
			modEnd := i

			// Simple approach: assume a small block of changes (max 10 lines)
			maxChangeLines := 10
			linesProcessed := 0

			// Advance through the changed section
			for linesProcessed < maxChangeLines {
				// Check if we've reached end of either file
				if origEnd >= len(originalLines) || modEnd >= len(modifiedLines) {
					break
				}

				// If lines match again, we might be at the end of the change
				if origEnd < len(originalLines) && modEnd < len(modifiedLines) &&
					originalLines[origEnd] == modifiedLines[modEnd] {
					// Check if next few lines also match (stable end)
					matchCount := 0
					for k := 0; k < 3 && origEnd+k < len(originalLines) && modEnd+k < len(modifiedLines); k++ {
						if originalLines[origEnd+k] == modifiedLines[modEnd+k] {
							matchCount++
						} else {
							break
						}
					}
					if matchCount >= 2 {
						// Found stable end of change
						break
					}
				}

				origEnd++
				modEnd++
				linesProcessed++
			}

			// Add this change (limit to first 3 lines for minimal diff)
			changes = append(changes, ChangeSection{
				OrigStart: changeStart,
				OrigEnd:   min(changeStart+3, origEnd),
				ModStart:  changeStart,
				ModEnd:    min(changeStart+3, modEnd),
			})

			// Return only first change to keep it minimal
			return changes
		}
		i++
	}

	// Handle length differences (additions/deletions at end)
	if len(originalLines) != len(modifiedLines) {
		if len(originalLines) < len(modifiedLines) {
			// Lines added at end
			changes = append(changes, ChangeSection{
				OrigStart: len(originalLines),
				OrigEnd:   len(originalLines),
				ModStart:  len(originalLines),
				ModEnd:    min(len(originalLines)+3, len(modifiedLines)),
			})
		} else {
			// Lines removed from end
			changes = append(changes, ChangeSection{
				OrigStart: len(modifiedLines),
				OrigEnd:   min(len(modifiedLines)+3, len(originalLines)),
				ModStart:  len(modifiedLines),
				ModEnd:    len(modifiedLines),
			})
		}
	}

	return changes
}

// Helper functions
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Calculate similarity between two sets of lines
func calculateSimilarity(original, modified []string) float64 {
	if len(original) == 0 && len(modified) == 0 {
		return 1.0
	}
	if len(original) == 0 || len(modified) == 0 {
		return 0.0
	}

	// Simple similarity calculation based on common lines
	commonLines := 0
	originalSet := make(map[string]bool)
	for _, line := range original {
		originalSet[line] = true
	}

	for _, line := range modified {
		if originalSet[line] {
			commonLines++
		}
	}

	maxLines := len(original)
	if len(modified) > maxLines {
		maxLines = len(modified)
	}

	return float64(commonLines) / float64(maxLines)
}

// Generate unified diff between original and modified content
func generateUnifiedDiff(original, modified, filename string) string {
	originalLines := strings.Split(original, "\n")
	modifiedLines := strings.Split(modified, "\n")

	log.Printf("File diff generation: original_lines=%d, modified_lines=%d", len(originalLines), len(modifiedLines))

	// For large files with small changes, use optimized diff
	if len(originalLines) > 1000 || len(modifiedLines) > 1000 {
		log.Printf("Using optimized diff for large file")
		return generateOptimizedDiff(originalLines, modifiedLines, filename)
	}

	// For smaller files, use simple line-by-line diff
	return generateSimpleDiff(originalLines, modifiedLines, filename)
}

// Generate optimized diff that only shows actual changes - TRULY MINIMAL VERSION
func generateOptimizedDiff(originalLines, modifiedLines []string, filename string) string {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// Find actual changed sections (not individual lines)
	changes := findChangedSections(originalLines, modifiedLines)

	if len(changes) == 0 {
		diff.WriteString("@@ -0,0 +0,0 @@\n")
		return diff.String()
	}

	// Process only the first change section and limit its size
	change := changes[0]

	// Limit the change to maximum 10 lines total
	maxLinesPerSection := 5

	origStart := change.OrigStart
	origEnd := min(change.OrigEnd, origStart+maxLinesPerSection)
	modStart := change.ModStart
	modEnd := min(change.ModEnd, modStart+maxLinesPerSection)

	removedCount := origEnd - origStart
	addedCount := modEnd - modStart

	diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		origStart+1, removedCount,
		modStart+1, addedCount))

	// Show removed lines (limited)
	for i := origStart; i < origEnd && i < len(originalLines); i++ {
		diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
	}

	// Show added lines (limited)
	for i := modStart; i < modEnd && i < len(modifiedLines); i++ {
		diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
	}

	return diff.String()
}

// Find limited changes - only return first few actual differences
func findLimitedChanges(originalLines, modifiedLines []string, maxChanges int) []ChangeSection {
	var changes []ChangeSection

	minLen := min(len(originalLines), len(modifiedLines))

	// Find first few different lines
	for i := 0; i < minLen && len(changes) < maxChanges; i++ {
		if originalLines[i] != modifiedLines[i] {
			// Found a difference - create a minimal change section
			changes = append(changes, ChangeSection{
				OrigStart: i,
				OrigEnd:   i + 1, // Just one line
				ModStart:  i,
				ModEnd:    i + 1, // Just one line
			})
		}
	}

	// Handle case where one file is longer
	if len(originalLines) != len(modifiedLines) && len(changes) < maxChanges {
		if len(originalLines) > len(modifiedLines) {
			// Original has more lines
			changes = append(changes, ChangeSection{
				OrigStart: len(modifiedLines),
				OrigEnd:   min(len(modifiedLines)+3, len(originalLines)), // Show max 3 extra lines
				ModStart:  len(modifiedLines),
				ModEnd:    len(modifiedLines),
			})
		} else {
			// Modified has more lines
			changes = append(changes, ChangeSection{
				OrigStart: len(originalLines),
				OrigEnd:   len(originalLines),
				ModStart:  len(originalLines),
				ModEnd:    min(len(originalLines)+3, len(modifiedLines)), // Show max 3 extra lines
			})
		}
	}

	return changes
}

// Find the first line that differs between two slices
func findFirstDifference(original, modified []string) int {
	minLen := min(len(original), len(modified))
	for i := 0; i < minLen; i++ {
		if original[i] != modified[i] {
			return i
		}
	}

	// If one file is longer than the other, the first difference is at the end of the shorter one
	if len(original) != len(modified) {
		return minLen
	}

	return -1 // Files are identical
}

// Find the last line that differs between two slices
func findLastDifference(original, modified []string) int {
	origLen := len(original)
	modLen := len(modified)

	// Start from the end and work backwards
	i, j := origLen-1, modLen-1

	for i >= 0 && j >= 0 && original[i] == modified[j] {
		i--
		j--
	}

	// Return the last different line in the original file
	if i >= 0 {
		return i
	}

	// If we've exhausted the original but not the modified,
	// the last difference is at the end of the original
	return origLen - 1
}

// Simple diff for smaller files - NO CONTEXT LINES
func generateSimpleDiff(originalLines, modifiedLines []string, filename string) string {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// Count only changed lines
	changedOrigLines := 0
	changedModLines := 0

	maxLines := max(len(originalLines), len(modifiedLines))
	for i := 0; i < maxLines; i++ {
		origLineExists := i < len(originalLines)
		modLineExists := i < len(modifiedLines)

		if origLineExists && modLineExists {
			if originalLines[i] != modifiedLines[i] {
				changedOrigLines++
				changedModLines++
			}
		} else if origLineExists {
			changedOrigLines++
		} else if modLineExists {
			changedModLines++
		}
	}

	diff.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", changedOrigLines, changedModLines))

	// Show only changed lines, no context
	for i := 0; i < maxLines; i++ {
		origLineExists := i < len(originalLines)
		modLineExists := i < len(modifiedLines)

		if origLineExists && modLineExists {
			if originalLines[i] != modifiedLines[i] {
				diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
				diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
			}
			// Skip identical lines (no context)
		} else if origLineExists {
			diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
		} else if modLineExists {
			diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
		}
	}

	return diff.String()
}

// Generate a simple replacement diff (more efficient for large changes)
func generateSimpleReplacement(original, modified, filename string) string {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	originalLines := strings.Split(original, "\n")
	modifiedLines := strings.Split(modified, "\n")

	diff.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(originalLines), len(modifiedLines)))

	// Show only first few lines of removal and addition to keep diff manageable
	maxShowLines := 10

	// Show some removed lines
	for i := 0; i < min(len(originalLines), maxShowLines); i++ {
		diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
	}
	if len(originalLines) > maxShowLines {
		diff.WriteString(fmt.Sprintf("-... (%d more lines removed)\n", len(originalLines)-maxShowLines))
	}

	// Show some added lines
	for i := 0; i < min(len(modifiedLines), maxShowLines); i++ {
		diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
	}
	if len(modifiedLines) > maxShowLines {
		diff.WriteString(fmt.Sprintf("+... (%d more lines added)\n", len(modifiedLines)-maxShowLines))
	}

	return diff.String()
}

// Generate proper unified diff for similar files
func generateProperUnifiedDiff(originalLines, modifiedLines []string, filename string) string {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	maxLines := len(originalLines)
	if len(modifiedLines) > maxLines {
		maxLines = len(modifiedLines)
	}

	diff.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(originalLines), len(modifiedLines)))

	for i := 0; i < maxLines; i++ {
		if i < len(originalLines) && i < len(modifiedLines) {
			if originalLines[i] != modifiedLines[i] {
				diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
				diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
			} else {
				diff.WriteString(fmt.Sprintf(" %s\n", originalLines[i]))
			}
		} else if i < len(originalLines) {
			diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
		} else if i < len(modifiedLines) {
			diff.WriteString(fmt.Sprintf("+%s\n", modifiedLines[i]))
		}
	}

	return diff.String()
}

func NewKhojProvider(apiBase, apiKey string) *KhojProvider {
	return &KhojProvider{
		APIBase: apiBase,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		MCPManager: &MCPToolManager{
			Sessions: make(map[string]*MCPSession),
		},
	}
}

// HandleChatCompletion processes ONLY regular chat completion requests
func (kp *KhojProvider) HandleChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	log.Printf("Processing regular chat completion for model: %s", req.Model)

	// Build prompt from messages (WITHOUT file contents)
	var prompt strings.Builder
	var files []KhojFile

	for i, msg := range req.Messages {
		// Don't include large file contents in the prompt text
		messageContent := msg.Content

		// Check if this message contains file-like content
		isLargeContent := len(msg.Content) > 10000
		containsHTML := strings.Contains(msg.Content, "<!DOCTYPE html>") || strings.Contains(msg.Content, "<html")

		log.Printf("=== DEBUG: Message %d Analysis ===", i+1)
		log.Printf("Content length: %d, isLargeContent: %v, containsHTML: %v", len(msg.Content), isLargeContent, containsHTML)

		if isLargeContent && containsHTML {
			// This is file content - add to files array, not prompt
			filename := "main.html"
			if strings.Contains(msg.Content, "index.html") {
				filename = "index.html"
			}

			file := KhojFile{
				Name:     filename,
				Content:  msg.Content,
				FileType: "html",
				Size:     len(msg.Content),
			}
			files = append(files, file)

			log.Printf("=== DEBUG: Adding file to Khoj request ===")
			log.Printf("File name: %s", file.Name)
			log.Printf("File size: %d bytes", file.Size)
			log.Printf("File type: %s", file.FileType)

			// Replace the large content with a reference in the prompt
			messageContent = fmt.Sprintf("[File: %s (%d bytes) - sent in files array]", filename, len(msg.Content))
		}

		prompt.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, messageContent))
	}

	finalPrompt := prompt.String()

	// Call Khoj API with files separate from prompt
	khojReq := &KhojRequest{
		Q:              finalPrompt,
		Stream:         false,
		ConversationID: continueConversationID,
		ClientID:       "khoj-provider-continue",
		Files:          files, // Send files here, not in prompt
	}

	// DEBUG: Log what you send to Khoj
	log.Printf("=== DEBUG: Khoj API Request ===")
	log.Printf("Query (prompt): %s", finalPrompt)
	log.Printf("Files count: %d", len(khojReq.Files))

	if len(khojReq.Files) > 0 {
		for i, file := range khojReq.Files {
			log.Printf("File %d: Name=%s, Size=%d bytes, Type=%s", i+1, file.Name, file.Size, file.FileType)
			if len(file.Content) > 200 {
				log.Printf("File %d content preview: %s...", i+1, file.Content[:200])
			}
		}
	} else {
		log.Printf("No files being sent to Khoj")
	}

	khojResp, err := kp.callKhojAPI(ctx, khojReq)
	if err != nil {
		return nil, fmt.Errorf("khoj API call failed: %w", err)
	}

	// DEBUG: Log what you get back from Khoj
	log.Printf("=== DEBUG: Khoj API Response ===")
	log.Printf("Response length: %d characters", len(khojResp.Response))
	log.Printf("Response preview: %s", khojResp.Response[:min(300, len(khojResp.Response))])

	response := &ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: khojResp.Response,
				},
				FinishReason: "stop",
			},
		},
		Usage: Usage{
			PromptTokens:     len(finalPrompt) / 4,
			CompletionTokens: len(khojResp.Response) / 4,
			TotalTokens:      (len(finalPrompt) + len(khojResp.Response)) / 4,
		},
	}

	return response, nil
}

func (kp *KhojProvider) callKhojAPI(ctx context.Context, req *KhojRequest) (*KhojResponse, error) {
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("Retrying Khoj API call (attempt %d/%d)", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}

		jsonData, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		log.Printf("Making Khoj API call to: %s", kp.APIBase+"/api/chat")

		httpReq, err := http.NewRequestWithContext(ctx, "POST", kp.APIBase+"/api/chat", bytes.NewBuffer(jsonData))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("User-Agent", "KhojProvider/1.0")
		if kp.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+kp.APIKey)
		}

		resp, err := kp.HTTPClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			log.Printf("Khoj API call failed (attempt %d): %v", attempt+1, lastErr)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			continue
		}

		log.Printf("Khoj API response status: %d, body length: %d", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("khoj API error %d: %s", resp.StatusCode, string(body))
			if resp.StatusCode >= 500 {
				continue
			}
			return nil, lastErr
		}

		var khojResp KhojResponse
		if err := json.Unmarshal(body, &khojResp); err != nil {
			lastErr = fmt.Errorf("failed to decode response: %w", err)
			log.Printf("Response body: %s", string(body))
			continue
		}

		log.Printf("Successfully parsed Khoj response")
		return &khojResp, nil
	}

	return nil, fmt.Errorf("khoj API call failed after %d attempts: %w", maxRetries, lastErr)
}

func NewKhojProviderWithTimeout(apiBase, apiKey string, timeout time.Duration) *KhojProvider {
	return &KhojProvider{
		APIBase: apiBase,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
		MCPManager: &MCPToolManager{
			Sessions: make(map[string]*MCPSession),
		},
	}
}

func (kp *KhojProvider) handleStreamingRequest(w http.ResponseWriter, r *http.Request, req *ChatCompletionRequest) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	ctx := r.Context()

	resp, err := kp.HandleChatCompletion(ctx, req)
	if err != nil {
		log.Printf("Error in HandleChatCompletion: %v", err)
		errorChunk := map[string]interface{}{
			"error": map[string]interface{}{
				"message": err.Error(),
				"type":    "api_error",
			},
		}
		errorData, _ := json.Marshal(errorChunk)
		fmt.Fprintf(w, "data: %s\n\n", errorData)
		return
	}

	content := resp.Choices[0].Message.Content
	chunkSize := 50

	for i := 0; i < len(content); i += chunkSize {
		select {
		case <-ctx.Done():
			log.Printf("Client disconnected during streaming")
			return
		default:
		}

		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}

		chunk := map[string]interface{}{
			"id":      resp.ID,
			"object":  "chat.completion.chunk",
			"created": resp.Created,
			"model":   resp.Model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"content": content[i:end],
					},
					"finish_reason": nil,
				},
			},
		}

		chunkData, _ := json.Marshal(chunk)

		if _, err := fmt.Fprintf(w, "data: %s\n\n", chunkData); err != nil {
			log.Printf("Error writing chunk: %v", err)
			return
		}

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		time.Sleep(5 * time.Millisecond)
	}

	finalChunk := map[string]interface{}{
		"id":      resp.ID,
		"object":  "chat.completion.chunk",
		"created": resp.Created,
		"model":   resp.Model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": "stop",
			},
		},
	}

	finalData, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\n", finalData)
	fmt.Fprintf(w, "data: [DONE]\n\n")

	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// Generate minimal diff that focuses only on the actual changes
func generateMinimalDiff(original, modified, filename string) string {
	originalLines := strings.Split(original, "\n")
	modifiedLines := strings.Split(modified, "\n")

	// Find the actual differences
	changes := findActualChanges(originalLines, modifiedLines)

	if len(changes) == 0 {
		return "" // No changes
	}

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// Generate only the first significant change to keep it minimal
	change := changes[0]
	contextLines := 2 // Minimal context

	startLine := max(0, change.StartLine-contextLines)
	endLine := min(len(originalLines), change.EndLine+contextLines)

	diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
		startLine+1, endLine-startLine,
		startLine+1, endLine-startLine+(change.LinesAdded-change.LinesRemoved)))

	// Show minimal context and changes
	for i := startLine; i < change.StartLine && i < len(originalLines); i++ {
		diff.WriteString(fmt.Sprintf(" %s\n", originalLines[i]))
	}

	// Show the actual change
	for i := change.StartLine; i < change.EndLine && i < len(originalLines); i++ {
		diff.WriteString(fmt.Sprintf("-%s\n", originalLines[i]))
	}

	// Show the replacement (simplified)
	if change.LinesAdded > 0 {
		diff.WriteString(fmt.Sprintf("+%s\n", "<!-- Changes applied -->"))
	}

	return diff.String()
}

type Change struct {
	StartLine    int
	EndLine      int
	LinesAdded   int
	LinesRemoved int
}

func findActualChanges(original, modified []string) []Change {
	// Simplified change detection
	if len(original) != len(modified) {
		return []Change{{
			StartLine:    0,
			EndLine:      min(len(original), 10), // Show only first 10 lines of change
			LinesAdded:   len(modified) - len(original),
			LinesRemoved: max(0, len(original)-len(modified)),
		}}
	}

	// Find first difference
	for i := 0; i < len(original) && i < len(modified); i++ {
		if original[i] != modified[i] {
			return []Change{{
				StartLine:    i,
				EndLine:      min(i+5, len(original)), // Show 5 lines of change
				LinesAdded:   0,
				LinesRemoved: 0,
			}}
		}
	}

	return []Change{} // No changes
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	apiBase := os.Getenv("KHOJ_API_BASE")
	if apiBase == "" {
		apiBase = "https://app.khoj.dev"
	}

	apiKey := os.Getenv("KHOJ_API_KEY")
	if apiKey == "" {
		log.Fatal("KHOJ_API_KEY environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting Khoj provider with API Base: %s", apiBase)
	log.Printf("API Key: %s...", apiKey[:min(len(apiKey), 8)])

	timeoutStr := os.Getenv("KHOJ_TIMEOUT")
	timeout := 120 * time.Second
	if timeoutStr != "" {
		if parsedTimeout, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsedTimeout
		}
	}

	log.Printf("Using timeout: %v", timeout)
	provider := NewKhojProviderWithTimeout(apiBase, apiKey, timeout)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Starting request - User-Agent: %s", r.Header.Get("User-Agent"))
		log.Printf("Request headers: %+v", r.Header)

		enableCORS(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading request body: %v", err)
			http.Error(w, "Error reading request", http.StatusBadRequest)
			return
		}

		// Check if this is an applyToFile request FIRST
		var rawRequest map[string]interface{}
		if err := json.Unmarshal(body, &rawRequest); err != nil {
			log.Printf("Error parsing JSON: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// If not applyToFile, parse as normal ChatCompletionRequest
		var req ChatCompletionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("Error decoding ChatCompletionRequest: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Handle streaming vs non-streaming for normal requests
		if req.Stream {
			provider.handleStreamingRequest(w, r, &req)
			return
		}

		// Non-streaming response
		resp, err := provider.HandleChatCompletion(r.Context(), &req)
		if err != nil {
			log.Printf("Error handling chat completion: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	http.HandleFunc("/v1/completions", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Starting completions request - User-Agent: %s", r.Header.Get("User-Agent"))
		log.Printf("Request headers: %+v", r.Header)

		enableCORS(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading request body: %v", err)
			http.Error(w, "Error reading request", http.StatusBadRequest)
			return
		}

		var req struct {
			Model       string  `json:"model"`
			Prompt      string  `json:"prompt"`
			MaxTokens   int     `json:"max_tokens,omitempty"`
			Temperature float64 `json:"temperature,omitempty"`
			Stream      bool    `json:"stream,omitempty"`
		}

		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("Error decoding completions request: %v", err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Check if this is a large request (likely Apply) and contains HTML
		isApplyRequest := strings.Contains(req.Prompt, "<!DOCTYPE html>") || strings.Contains(req.Prompt, "<html")

		log.Printf("Request analysis: prompt_length=%d, isApplyRequest=%v", len(req.Prompt), isApplyRequest)

		// If it's a large HTML request, treat it as Apply request
		if isApplyRequest {
			log.Printf("Detected Apply request - generating diff for Continue.dev")

			// DEBUG: Log what Apply sends you
			log.Printf("=== DEBUG: Apply Request Data ===")
			log.Printf("Prompt length: %d characters", len(req.Prompt))
			if len(req.Prompt) > 500 {
				log.Printf("First 500 chars of prompt: %s", req.Prompt[:500])
				log.Printf("Last 500 chars of prompt: %s", req.Prompt[len(req.Prompt)-500:])
			} else {
				log.Printf("Full prompt: %s", req.Prompt)
			}

			// Extract the new content from the prompt
			newContent := req.Prompt

			// Clean up the prompt to extract just the HTML content
			if strings.Contains(newContent, "```html") {
				startIdx := strings.Index(newContent, "```html")
				if startIdx != -1 {
					startIdx += len("```html")
					endIdx := strings.LastIndex(newContent, "```")
					if endIdx != -1 && endIdx > startIdx {
						newContent = newContent[startIdx:endIdx]
					}
				}
			}

			newContent = strings.TrimPrefix(newContent, "<|im_start|>user\n")
			newContent = strings.TrimSuffix(newContent, "<|im_end|>")
			newContent = strings.TrimSpace(newContent)

			// Use default filepath and extract filename
			filepath := "file:///c:/Users/rene.zander/dev/ProjectImpactWorkflowWithAI/app/main.html"
			filename := "main.html" // Extract filename from filepath

			// Read the original file
			var originalContent string
			localPath := strings.TrimPrefix(filepath, "file:///")
			if runtime.GOOS == "windows" {
				localPath = strings.ReplaceAll(localPath, "/", "\\")
			}

			if fileBytes, err := os.ReadFile(localPath); err == nil {
				originalContent = string(fileBytes)
			} else {
				log.Printf("Could not read original file %s: %v", localPath, err)
				originalContent = ""
			}

			log.Printf("=== DEBUG: File Content Analysis ===")
			log.Printf("Filename: %s", filename)
			log.Printf("Original file size: %d bytes", len(originalContent))
			log.Printf("Modified file size: %d bytes", len(newContent))

			// Generate a minimal, focused diff
			diffContent := generateUnifiedDiff(originalContent, newContent, filepath)

			// DEBUG: Log what you send back
			log.Printf("=== DEBUG: Generated Diff Response ===")
			log.Printf("Diff length: %d characters", len(diffContent))
			if len(diffContent) > 300 {
				log.Printf("Diff content preview (first 300 chars): %s", diffContent[:300])
			} else {
				log.Printf("Full diff content: %s", diffContent)
			}

			// For Apply requests, return ONLY the diff content as plain text
			w.Header().Set("Content-Type", "text/plain")

			// For Apply requests, return ONLY the diff content as plain text
			log.Printf("=== DEBUG: FINAL APPLY RESPONSE ===")
			log.Printf("Response Content-Type: text/plain")
			log.Printf("Response Status: 200 OK")
			log.Printf("Response Body Length: %d bytes", len(diffContent))
			log.Printf("=== COMPLETE RESPONSE BODY START ===")
			log.Printf("%s", diffContent)
			log.Printf("=== COMPLETE RESPONSE BODY END ===")

			// Log each line of the diff for detailed analysis
			lines := strings.Split(diffContent, "\n")
			log.Printf("=== DIFF LINE BY LINE ANALYSIS ===")
			log.Printf("Total lines in diff: %d", len(lines))
			for i, line := range lines {
				if strings.TrimSpace(line) != "" {
					log.Printf("Line %d: %s", i+1, line)
				}
			}
			log.Printf("=== END LINE BY LINE ANALYSIS ===")

			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(diffContent))
			return

			w.Write([]byte(diffContent))
			return
		}

		// Handle normal completions request
		log.Printf("Processing as normal completions request")

		chatReq := &ChatCompletionRequest{
			Model: req.Model,
			Messages: []Message{
				{Role: "user", Content: req.Prompt},
			},
			MaxTokens:   req.MaxTokens,
			Temperature: req.Temperature,
			Stream:      req.Stream,
		}

		chatResp, err := provider.HandleChatCompletion(r.Context(), chatReq)
		if err != nil {
			log.Printf("Error handling completion: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		completionResp := struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			Model   string `json:"model"`
			Choices []struct {
				Text         string `json:"text"`
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}{
			ID:      chatResp.ID,
			Object:  "text_completion",
			Created: chatResp.Created,
			Model:   chatResp.Model,
			Choices: []struct {
				Text         string `json:"text"`
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Text:         chatResp.Choices[0].Message.Content,
					Index:        0,
					FinishReason: "stop",
				},
			},
			Usage: struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			}(chatResp.Usage),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(completionResp)
	})

	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		enableCORS(w)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		models := map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{
					"id":       "khoj-chat",
					"object":   "model",
					"created":  time.Now().Unix(),
					"owned_by": "khoj",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)
	})

	log.Printf("Khoj provider server starting on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
