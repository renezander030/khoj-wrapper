package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/systray"
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
	Files          []KhojFile `json:"files,omitempty"`
}

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

type SessionRequest struct {
	AgentSlug string `json:"agent_slug"`
}

type SessionResponse struct {
	ConversationID string `json:"conversation_id"`
}

type ConversationState struct {
	LastConversationID string    `json:"last_conversation_id"`
	AgentSlug          string    `json:"agent_slug"`
	CreatedAt          time.Time `json:"created_at"`
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

// Global variables for conversation management
var (
	conversationID   string
	currentAgentSlug string
	newConversation  bool
)

// Command-line flags
var (
	flagNewConversation = flag.Bool("n", false, "Start a new conversation")
	flagConversationID  = flag.String("conversation-id", "", "Override conversation ID")
)

const (
	conversationStateFile = "conversation_state.json"
	defaultAgentSlug      = "sonnet-short-025716"
	clipboardTimeout      = 30 * time.Second
)

// Windows API declarations for clipboard and keyboard monitoring
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procSendInput        = user32.NewProc("SendInput")
	procMessageBox       = user32.NewProc("MessageBoxW")
)

// Windows constants
const (
	VK_Q            = 0x51
	VK_CONTROL      = 0x11
	CF_UNICODETEXT  = 13
	INPUT_KEYBOARD  = 1
	KEYEVENTF_KEYUP = 0x0002
)

// Windows structures
type INPUT struct {
	Type uint32
	Ki   KEYBDINPUT
}

type KEYBDINPUT struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// Global variables for clipboard monitoring
var (
	clipboardActive bool
)

// loadConversationState loads the conversation state from JSON file
func loadConversationState() (*ConversationState, error) {
	data, err := os.ReadFile(conversationStateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConversationState{}, nil // Return empty state if file doesn't exist
		}
		return nil, fmt.Errorf("failed to read conversation state file: %w", err)
	}

	var state ConversationState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse conversation state: %w", err)
	}

	return &state, nil
}

// saveConversationState saves the conversation state to JSON file
func saveConversationState(state *ConversationState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal conversation state: %w", err)
	}

	if err := os.WriteFile(conversationStateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write conversation state file: %w", err)
	}

	return nil
}

// createNewConversation creates a new conversation session via Khoj API
func createNewConversation(apiBase, apiKey string) (string, error) {
	agentSlug := currentAgentSlug
	if agentSlug == "" {
		agentSlug = defaultAgentSlug
	}

	sessionReq := SessionRequest{
		AgentSlug: agentSlug,
	}

	jsonData, err := json.Marshal(sessionReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session request: %w", err)
	}

	req, err := http.NewRequest("POST", apiBase+"/api/chat/sessions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create session request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "KhojProvider/1.0")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("session creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var sessionResp SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return "", fmt.Errorf("failed to decode session response: %w", err)
	}

	return sessionResp.ConversationID, nil
}

// initializeConversationID sets up the conversation ID based on command-line flags and saved state
func initializeConversationID() error {
	// Parse command-line flags
	flag.Parse()

	// Check for conversation ID override from command line
	if *flagConversationID != "" {
		conversationID = *flagConversationID
		log.Printf("Using conversation ID from command line: %s", conversationID)
		return nil
	}

	// Check for new conversation flag
	newConversation = *flagNewConversation
	if newConversation {
		log.Printf("Will create new conversation when server starts")
		return nil
	}

	// Load conversation state from file
	state, err := loadConversationState()
	if err != nil {
		return fmt.Errorf("failed to load conversation state: %w", err)
	}

	if state.LastConversationID == "" {
		log.Printf("No saved conversation found, will create new conversation when server starts")
		newConversation = true
		// Set default agent slug if not set
		if currentAgentSlug == "" {
			currentAgentSlug = defaultAgentSlug
		}
		return nil
	}

	conversationID = state.LastConversationID
	if state.AgentSlug != "" {
		currentAgentSlug = state.AgentSlug
	} else {
		currentAgentSlug = defaultAgentSlug
	}
	log.Printf("Using saved conversation ID: %s (created: %s)", conversationID, state.CreatedAt.Format(time.RFC3339))
	log.Printf("Using agent slug: %s", currentAgentSlug)
	return nil
}

// createNewConversationFromMenu creates a new conversation and updates the menu
func createNewConversationFromMenu() error {
	apiBase := os.Getenv("KHOJ_API_BASE")
	if apiBase == "" {
		apiBase = "https://app.khoj.dev"
	}

	apiKey := os.Getenv("KHOJ_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("KHOJ_API_KEY not set")
	}

	newConvID, err := createNewConversation(apiBase, apiKey)
	if err != nil {
		return fmt.Errorf("failed to create new conversation: %w", err)
	}

	conversationID = newConvID

	// Save the new conversation state
	state := &ConversationState{
		LastConversationID: conversationID,
		AgentSlug:          currentAgentSlug,
		CreatedAt:          time.Now(),
	}
	if err := saveConversationState(state); err != nil {
		log.Printf("Warning: Failed to save conversation state: %v", err)
	}

	log.Printf("✅ New conversation created from menu: %s", conversationID)
	return nil
}

// getConversationDisplayID returns the last 4 characters of the conversation ID for display
func getConversationDisplayID() string {
	if conversationID == "" {
		return "None"
	}
	if len(conversationID) <= 4 {
		return conversationID
	}
	return "..." + conversationID[len(conversationID)-4:]
}

// updateConversationID updates the current conversation ID and saves state
func updateConversationID(newID string) error {
	if newID == "" {
		return fmt.Errorf("conversation ID cannot be empty")
	}

	conversationID = newID

	// Save the updated conversation state
	state := &ConversationState{
		LastConversationID: conversationID,
		AgentSlug:          currentAgentSlug,
		CreatedAt:          time.Now(),
	}
	if err := saveConversationState(state); err != nil {
		return fmt.Errorf("failed to save conversation state: %w", err)
	}

	log.Printf("✅ Conversation ID updated: %s", conversationID)
	return nil
}

// updateAgentSlug updates the current agent slug and saves state
func updateAgentSlug(newSlug string) error {
	if newSlug == "" {
		newSlug = defaultAgentSlug
	}

	currentAgentSlug = newSlug

	// Save the updated conversation state
	state := &ConversationState{
		LastConversationID: conversationID,
		AgentSlug:          currentAgentSlug,
		CreatedAt:          time.Now(),
	}
	if err := saveConversationState(state); err != nil {
		return fmt.Errorf("failed to save conversation state: %w", err)
	}

	log.Printf("✅ Agent slug updated: %s", currentAgentSlug)
	return nil
}

// openBrowser opens a URL in the default browser across different platforms
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin": // macOS
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Run()
}

// showInputDialog creates a temporary web server to show an input dialog
func showInputDialog(title, prompt, defaultValue string) (string, error) {
	// Find an available port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", fmt.Errorf("failed to find available port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Channel to receive the result
	resultCh := make(chan string, 1)
	errorCh := make(chan error, 1)

	// Create HTTP server
	mux := http.NewServeMux()

	// Serve the input form
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 50px; background: #f5f5f5; }
        .container { background: white; padding: 30px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); max-width: 500px; margin: 0 auto; }
        h2 { color: #333; margin-bottom: 20px; }
        input[type="text"] { width: 100%%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; font-size: 16px; margin: 10px 0; }
        button { background: #007cba; color: white; padding: 12px 24px; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; margin-right: 10px; }
        button:hover { background: #005a87; }
        .cancel { background: #666; }
        .cancel:hover { background: #444; }
    </style>
</head>
<body>
    <div class="container">
        <h2>%s</h2>
        <form method="POST" action="/submit">
            <p>%s</p>
            <input type="text" name="value" value="%s" required autofocus>
            <br><br>
            <button type="submit">OK</button>
            <button type="button" class="cancel" onclick="window.close()">Cancel</button>
        </form>
    </div>
    <script>
        // Auto-select the input text
        document.querySelector('input[name="value"]').select();
        // Handle form submission
        document.querySelector('form').onsubmit = function(e) {
            e.preventDefault();
            fetch('/submit', {
                method: 'POST',
                body: new FormData(this)
            }).then(() => {
                window.close();
            });
        };
    </script>
</body>
</html>`, title, title, prompt, defaultValue)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})

	// Handle form submission
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			value := r.FormValue("value")
			resultCh <- value
			w.Write([]byte("OK - You can close this window"))
		}
	})

	server := &http.Server{
		Addr:    ":" + strconv.Itoa(port),
		Handler: mux,
	}

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errorCh <- err
		}
	}()

	// Open browser
	url := fmt.Sprintf("http://localhost:%d", port)
	go func() {
		time.Sleep(100 * time.Millisecond) // Give server time to start
		if err := openBrowser(url); err != nil {
			log.Printf("Failed to open browser: %v", err)
		}
	}()

	// Wait for result or timeout
	select {
	case result := <-resultCh:
		server.Close()
		return result, nil
	case err := <-errorCh:
		server.Close()
		return "", err
	case <-time.After(5 * time.Minute): // 5 minute timeout
		server.Close()
		return "", fmt.Errorf("input dialog timed out")
	}
}

// editConversationIDDialog shows a dialog to edit the conversation ID
func editConversationIDDialog() error {
	currentID := conversationID
	if currentID == "" {
		currentID = "No conversation ID set"
	}

	newID, err := showInputDialog(
		"Edit Conversation ID",
		"Enter the new conversation ID:",
		currentID,
	)
	if err != nil {
		return fmt.Errorf("failed to show input dialog: %w", err)
	}

	if newID == "" || newID == currentID {
		return nil // User cancelled or no change
	}

	return updateConversationID(newID)
}

// editAgentSlugDialog shows a dialog to edit the agent slug
func editAgentSlugDialog() error {
	currentSlug := currentAgentSlug
	if currentSlug == "" {
		currentSlug = defaultAgentSlug
	}

	newSlug, err := showInputDialog(
		"Edit Agent Slug",
		"Enter the new agent slug (e.g., sonnet-short-025716, gpt-4o-mini):",
		currentSlug,
	)
	if err != nil {
		return fmt.Errorf("failed to show input dialog: %w", err)
	}

	if newSlug == "" || newSlug == currentSlug {
		return nil // User cancelled or no change
	}

	return updateAgentSlug(newSlug)
}

// Windows-specific clipboard and keyboard functions
func getClipboardText() (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("clipboard functionality only available on Windows")
	}

	r1, _, err := procOpenClipboard.Call(0)
	if r1 == 0 {
		return "", fmt.Errorf("failed to open clipboard: %v", err)
	}
	defer procCloseClipboard.Call()

	h, _, err := procGetClipboardData.Call(CF_UNICODETEXT)
	if h == 0 {
		return "", fmt.Errorf("failed to get clipboard data: %v", err)
	}

	l, _, err := procGlobalLock.Call(h)
	if l == 0 {
		return "", fmt.Errorf("failed to lock global memory: %v", err)
	}
	defer procGlobalUnlock.Call(h)

	text := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(l))[:])
	return text, nil
}

func sendText(text string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("text sending only available on Windows")
	}

	log.Printf("📝 Sending %d characters to cursor position...", len(text))

	// Try multiple approaches for better reliability

	// Method 1: Try clipboard + Ctrl+V approach
	log.Printf("🔄 Trying clipboard + Ctrl+V method...")
	err := setClipboardText(text)
	if err != nil {
		log.Printf("⚠️ Failed to set clipboard: %v", err)
	} else {
		// Small delay to ensure clipboard is set
		time.Sleep(100 * time.Millisecond)

		err = simulateCtrlV()
		if err != nil {
			log.Printf("⚠️ Failed to simulate Ctrl+V: %v", err)
		} else {
			log.Printf("✅ Clipboard + Ctrl+V method succeeded")
			return nil
		}
	}

	// Method 2: Try direct window message approach
	log.Printf("🔄 Trying direct window message method...")
	err = sendTextViaWindowMessage(text)
	if err != nil {
		log.Printf("⚠️ Window message method failed: %v", err)
	} else {
		log.Printf("✅ Window message method succeeded")
		return nil
	}

	// Method 3: Fallback to character-by-character typing
	log.Printf("🔄 Falling back to character-by-character typing...")
	return sendTextCharByChar(text)
}

func setClipboardText(text string) error {
	// Open clipboard
	r1, _, err := procOpenClipboard.Call(0)
	if r1 == 0 {
		return fmt.Errorf("failed to open clipboard: %v", err)
	}
	defer procCloseClipboard.Call()

	// Clear clipboard
	user32.NewProc("EmptyClipboard").Call()

	// Convert text to UTF16
	utf16Text := syscall.StringToUTF16(text)

	// Allocate global memory
	globalAlloc := kernel32.NewProc("GlobalAlloc")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")

	size := len(utf16Text) * 2                            // 2 bytes per UTF16 character
	hMem, _, _ := globalAlloc.Call(0x2000, uintptr(size)) // GMEM_MOVEABLE
	if hMem == 0 {
		return fmt.Errorf("failed to allocate global memory")
	}

	pMem, _, _ := globalLock.Call(hMem)
	if pMem == 0 {
		return fmt.Errorf("failed to lock global memory")
	}

	// Copy text to global memory
	for i, char := range utf16Text {
		*(*uint16)(unsafe.Pointer(pMem + uintptr(i*2))) = char
	}

	globalUnlock.Call(hMem)

	// Set clipboard data
	setClipboardData := user32.NewProc("SetClipboardData")
	r2, _, _ := setClipboardData.Call(CF_UNICODETEXT, hMem)
	if r2 == 0 {
		return fmt.Errorf("failed to set clipboard data")
	}

	return nil
}

func simulateCtrlV() error {
	log.Printf("🔄 Simulating Ctrl+V keypress...")

	// Simulate Ctrl+V keypress with proper key sequence

	// Key down: Ctrl
	ctrlDown := INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     VK_CONTROL,
			DwFlags: 0, // Key down
		},
	}

	// Key down: V
	vDown := INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     0x56, // V key
			DwFlags: 0,    // Key down
		},
	}

	// Key up: V
	vUp := INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     0x56, // V key
			DwFlags: KEYEVENTF_KEYUP,
		},
	}

	// Key up: Ctrl
	ctrlUp := INPUT{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     VK_CONTROL,
			DwFlags: KEYEVENTF_KEYUP,
		},
	}

	// Send Ctrl down
	ret1, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&ctrlDown)), unsafe.Sizeof(ctrlDown))
	log.Printf("🔄 Ctrl down result: %d", ret1)

	// Small delay
	time.Sleep(50 * time.Millisecond)

	// Send V down
	ret2, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&vDown)), unsafe.Sizeof(vDown))
	log.Printf("🔄 V down result: %d", ret2)

	// Small delay
	time.Sleep(50 * time.Millisecond)

	// Send V up
	ret3, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&vUp)), unsafe.Sizeof(vUp))
	log.Printf("🔄 V up result: %d", ret3)

	// Small delay
	time.Sleep(50 * time.Millisecond)

	// Send Ctrl up
	ret4, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&ctrlUp)), unsafe.Sizeof(ctrlUp))
	log.Printf("🔄 Ctrl up result: %d", ret4)

	if ret1 == 0 || ret2 == 0 || ret3 == 0 || ret4 == 0 {
		return fmt.Errorf("SendInput failed - results: %d,%d,%d,%d", ret1, ret2, ret3, ret4)
	}

	log.Printf("✅ Ctrl+V simulation completed successfully")
	return nil
}

func sendTextViaWindowMessage(text string) error {
	log.Printf("🔄 Sending text via window messages...")

	// Get the foreground window (where the cursor is)
	getForegroundWindow := user32.NewProc("GetForegroundWindow")
	sendMessage := user32.NewProc("SendMessageW")

	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return fmt.Errorf("no foreground window found")
	}

	log.Printf("🔄 Found foreground window: %v", hwnd)

	// Send each character as WM_CHAR message
	const WM_CHAR = 0x0102

	runes := []rune(text)
	for i, char := range runes {
		if i%100 == 0 {
			log.Printf("🔄 Sending char %d/%d via message", i, len(runes))
		}

		sendMessage.Call(hwnd, WM_CHAR, uintptr(char), 0)
		// Suppress individual character failure messages for cleaner output

		// Small delay
		time.Sleep(1 * time.Millisecond)
	}

	log.Printf("✅ Window message method completed")
	return nil
}

func sendTextCharByChar(text string) error {
	log.Printf("🔄 Sending text character by character (%d chars)...", len(text))

	// Convert to runes for proper Unicode handling
	runes := []rune(text)

	for i, char := range runes {
		if i%100 == 0 {
			log.Printf("🔄 Progress: %d/%d characters", i, len(runes))
		}

		// Use Unicode input for better character support
		input := INPUT{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				WVk:         0, // Use 0 for Unicode input
				WScan:       uint16(char),
				DwFlags:     4, // KEYEVENTF_UNICODE
				Time:        0,
				DwExtraInfo: 0,
			},
		}

		// Send the character
		procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
		// Suppress individual character failure messages for cleaner output

		// Small delay between characters (adjust if too slow)
		time.Sleep(2 * time.Millisecond)
	}

	log.Printf("✅ Character-by-character sending completed")
	return nil
}

// bringToForeground aggressively brings windows to foreground
func bringToForeground() {
	if runtime.GOOS != "windows" {
		return
	}

	// Get Windows API functions
	getCurrentThreadId := kernel32.NewProc("GetCurrentThreadId")
	getForegroundWindow := user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessId := user32.NewProc("GetWindowThreadProcessId")
	attachThreadInput := user32.NewProc("AttachThreadInput")
	allowSetForegroundWindow := user32.NewProc("AllowSetForegroundWindow")

	// Get current thread ID
	currentThreadId, _, _ := getCurrentThreadId.Call()

	// Get foreground window and its thread
	foregroundWindow, _, _ := getForegroundWindow.Call()
	if foregroundWindow != 0 {
		foregroundThreadId, _, _ := getWindowThreadProcessId.Call(foregroundWindow, 0)

		if foregroundThreadId != currentThreadId {
			// Attach to foreground thread to bypass focus stealing prevention
			attachThreadInput.Call(currentThreadId, foregroundThreadId, 1)

			// Allow our process to set foreground window
			allowSetForegroundWindow.Call(uintptr(0xFFFFFFFF)) // ASFW_ANY

			// Small delay
			time.Sleep(10 * time.Millisecond)

			// Detach from foreground thread
			attachThreadInput.Call(currentThreadId, foregroundThreadId, 0)
		}
	}

	// Also allow our process specifically
	allowSetForegroundWindow.Call(uintptr(0xFFFFFFFF))

	log.Printf("🔄 Aggressively prepared foreground permissions")
}

// forceWindowToForeground uses multiple techniques to force window to front
func forceWindowToForeground() {
	if runtime.GOOS != "windows" {
		return
	}

	// Find our MessageBox window and force it to foreground
	findWindow := user32.NewProc("FindWindowW")
	setForegroundWindow := user32.NewProc("SetForegroundWindow")
	showWindow := user32.NewProc("ShowWindow")
	bringWindowToTop := user32.NewProc("BringWindowToTop")
	setWindowPos := user32.NewProc("SetWindowPos")

	// Try to find MessageBox window (class name "#32770")
	className, _ := syscall.UTF16PtrFromString("#32770")
	hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(className)), 0)

	if hwnd != 0 {
		// Multiple attempts to bring window to front
		showWindow.Call(hwnd, 9) // SW_RESTORE
		showWindow.Call(hwnd, 5) // SW_SHOW
		bringWindowToTop.Call(hwnd)
		setForegroundWindow.Call(hwnd)

		// Set window as topmost temporarily
		setWindowPos.Call(hwnd, uintptr(0xFFFFFFFF), 0, 0, 0, 0, 0x0001|0x0002|0x0040) // HWND_TOPMOST, SWP_NOMOVE|SWP_NOSIZE|SWP_SHOWWINDOW

		log.Printf("🔄 Forced MessageBox window to foreground")
	}
}

// showModernInputDialog shows a simple but reliable input dialog
func showModernInputDialog(title, prompt, defaultValue string) (string, bool) {
	if runtime.GOOS != "windows" {
		return defaultValue, false
	}

	log.Printf("🔔 Showing input dialog for user prompt")

	// Force current process to foreground
	bringToForeground()

	// Get desktop window as parent
	getDesktopWindow := user32.NewProc("GetDesktopWindow")
	desktopWindow, _, _ := getDesktopWindow.Call()

	// First, show a choice dialog
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	promptPtr, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s\n\nDefault: \"%s\"\n\nYES = Use default prompt\nNO = Enter custom prompt\nCANCEL = Abort", prompt, defaultValue))

	// Start a goroutine to force the dialog to foreground after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond) // Wait for dialog to appear
		forceWindowToForeground()
	}()

	// MB_YESNOCANCEL = 3, MB_ICONQUESTION = 32, MB_TOPMOST = 0x40000, MB_SETFOREGROUND = 0x10000, MB_SYSTEMMODAL = 0x1000
	ret, _, _ := procMessageBox.Call(desktopWindow, uintptr(unsafe.Pointer(promptPtr)), uintptr(unsafe.Pointer(titlePtr)), 3|32|0x40000|0x10000|0x1000)

	switch ret {
	case 6: // YES - use default
		log.Printf("✅ User chose default prompt: %s", defaultValue)
		return defaultValue, false
	case 7: // NO - get custom input
		log.Printf("🔄 User wants to enter custom prompt")
		return showSimpleTextInput(title, "Enter your custom prompt:", defaultValue)
	default: // CANCEL or close
		log.Printf("ℹ️ User cancelled the dialog")
		return "", true
	}
}

// showSimpleTextInput shows a working text input dialog
func showSimpleTextInput(title, prompt, defaultValue string) (string, bool) {
	if runtime.GOOS != "windows" {
		return defaultValue, false
	}

	// Force to foreground before showing input dialog
	bringToForeground()

	// Create a VBScript that forces the dialog to foreground
	script := fmt.Sprintf(`
Set objShell = CreateObject("WScript.Shell")

' Bring the script window to foreground first
objShell.AppActivate "Windows Script Host"

' Show InputBox and force it to foreground
strInput = InputBox("%s", "%s", "%s")

' Force the dialog to stay on top
objShell.AppActivate "%s"

If strInput <> "" Then
    Set objFSO = CreateObject("Scripting.FileSystemObject")
    Set objFile = objFSO.CreateTextFile("temp_input_result.txt", True)
    objFile.WriteLine "OK:" & strInput
    objFile.Close
Else
    Set objFSO = CreateObject("Scripting.FileSystemObject")
    Set objFile = objFSO.CreateTextFile("temp_input_result.txt", True)
    objFile.WriteLine "CANCEL:"
    objFile.Close
End If
`, prompt, title, defaultValue, title)

	// Write VBScript to file
	scriptFile := "temp_input_dialog.vbs"
	err := os.WriteFile(scriptFile, []byte(script), 0644)
	if err != nil {
		log.Printf("⚠️ Failed to write VBScript: %v", err)
		return defaultValue, false
	}

	// Execute VBScript with wscript (shows GUI)
	cmd := exec.Command("wscript", scriptFile)
	err = cmd.Run()
	if err != nil {
		log.Printf("⚠️ Failed to run VBScript: %v", err)
		os.Remove(scriptFile)
		return defaultValue, false
	}

	// Read result from file
	resultFile := "temp_input_result.txt"
	output, err := os.ReadFile(resultFile)
	if err != nil {
		log.Printf("⚠️ Failed to read input result: %v", err)
		os.Remove(scriptFile)
		return defaultValue, false
	}

	// Clean up
	os.Remove(scriptFile)
	os.Remove(resultFile)

	// Parse result
	result := strings.TrimSpace(string(output))
	if strings.HasPrefix(result, "OK:") {
		userInput := strings.TrimPrefix(result, "OK:")
		log.Printf("✅ User entered custom prompt: %s", userInput)
		return userInput, false
	} else {
		log.Printf("ℹ️ User cancelled custom input")
		return "", true
	}
}

func showNotification(title, message string) {
	if runtime.GOOS != "windows" {
		log.Printf("%s: %s", title, message)
		return
	}

	// Log the notification (works in both console and windowsgui mode)
	log.Printf("📢 %s: %s", title, message)

	// Update systray tooltip with notification
	notificationText := fmt.Sprintf("🔔 %s: %s", title, message)
	systray.SetTooltip(notificationText)

	// Show Windows notification - different approach for windowsgui vs console mode
	go func() {
		log.Printf("🔔 Attempting to show notification: %s - %s", title, message)

		// Try toast library first (works in console mode)
		if showToastNotification(title, message) {
			log.Printf("✅ Toast notification shown successfully")
		} else {
			log.Printf("⚠️ Toast notification failed, trying PowerShell method...")
			// Fallback to PowerShell method for windowsgui mode
			showPowerShellNotification(title, message)
		}

		// Keep the tooltip notification visible for 5 seconds
		time.Sleep(5 * time.Second)
		systray.SetTooltip("Khoj OpenAI Wrapper Server")
	}()
}

// showToastNotification tries to show notification using PowerShell (cross-platform compatible)
func showToastNotification(title, message string) bool {
	if runtime.GOOS != "windows" {
		return false
	}

	// Use PowerShell with Windows.UI.Notifications for proper toast
	script := fmt.Sprintf(`
		try {
			[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
			[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
			[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null

			$APP_ID = 'Microsoft.Windows.Computer'
			$template = @"
<toast>
    <visual>
        <binding template="ToastGeneric">
            <text>%s</text>
            <text>%s</text>
        </binding>
    </visual>
    <audio src="ms-winsoundevent:Notification.Default" />
</toast>
"@

			$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
			$xml.LoadXml($template)
			$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
			[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($APP_ID).Show($toast)
			exit 0
		} catch {
			exit 1
		}
	`, title, message)

	// Execute PowerShell script silently
	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-Command", script)
	err := cmd.Run()
	if err != nil {
		log.Printf("⚠️ PowerShell toast notification failed: %v", err)
		return false
	}

	return true
}

// showPowerShellNotification shows notification using PowerShell (works in windowsgui mode)
func showPowerShellNotification(title, message string) {
	if runtime.GOOS != "windows" {
		return
	}

	// Use PowerShell with Windows.UI.Notifications for proper toast in windowsgui mode
	script := fmt.Sprintf(`
		[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
		[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
		[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null

		$APP_ID = 'Microsoft.Windows.Computer'
		$template = @"
<toast>
    <visual>
        <binding template="ToastGeneric">
            <text>%s</text>
            <text>%s</text>
        </binding>
    </visual>
    <audio src="ms-winsoundevent:Notification.Default" />
</toast>
"@

		$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
		$xml.LoadXml($template)
		$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
		[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($APP_ID).Show($toast)
	`, title, message)

	// Execute PowerShell script
	go func() {
		cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-Command", script)
		err := cmd.Run()
		if err != nil {
			log.Printf("⚠️ PowerShell notification failed: %v", err)
			// Final fallback to simple message box
			showFallbackNotification(title, message)
		} else {
			log.Printf("✅ PowerShell notification sent successfully")
		}
	}()
}

// showFallbackNotification shows a simple fallback notification
func showFallbackNotification(title, message string) {
	if runtime.GOOS != "windows" {
		return
	}

	// Simple MessageBox as absolute fallback
	go func() {
		titlePtr, _ := syscall.UTF16PtrFromString(title)
		messagePtr, _ := syscall.UTF16PtrFromString(message)

		// MB_OK = 0, MB_ICONINFORMATION = 64, MB_TOPMOST = 0x40000
		procMessageBox.Call(0, uintptr(unsafe.Pointer(messagePtr)), uintptr(unsafe.Pointer(titlePtr)), 0|64|0x40000)
	}()
}

// checkNotificationSettings checks Windows notification settings
func checkNotificationSettings() {
	if runtime.GOOS != "windows" {
		return
	}

	log.Printf("🔍 Checking Windows notification settings...")

	// Check if notifications are enabled globally
	// This is a simplified check - in reality, there are many registry keys to check
	log.Printf("ℹ️ Common reasons toast notifications might not appear:")
	log.Printf("   1. Focus Assist is enabled (Priority only or Alarms only)")
	log.Printf("   2. Notifications are disabled in Windows Settings")
	log.Printf("   3. App notifications are disabled for this application")
	log.Printf("   4. Do Not Disturb mode is enabled")
	log.Printf("   5. Presentation mode is active")
	log.Printf("   6. Windows notification service is not running")

	log.Printf("💡 To check: Windows Settings > System > Notifications & actions")
	log.Printf("💡 To check Focus Assist: Windows key + U, then F")
}

// processClipboardWithAI processes clipboard content with AI and inserts response at cursor
func processClipboardWithAI() {
	if runtime.GOOS != "windows" {
		log.Printf("Clipboard AI feature only available on Windows")
		return
	}

	if clipboardActive {
		log.Printf("Clipboard AI already processing, ignoring request")
		showNotification("Khoj AI", "Already processing a request...")
		return
	}

	clipboardActive = true
	defer func() {
		clipboardActive = false
		log.Printf("🔄 Clipboard AI processing completed")
	}()

	log.Printf("🚀 Starting clipboard AI processing...")

	// Get clipboard content
	clipboardText, err := getClipboardText()
	if err != nil {
		log.Printf("❌ Failed to get clipboard text: %v", err)
		showNotification("Khoj AI Error", fmt.Sprintf("Failed to read clipboard: %v", err))
		return
	}

	if strings.TrimSpace(clipboardText) == "" {
		log.Printf("⚠️ Clipboard is empty")
		showNotification("Khoj AI", "Clipboard is empty - copy some text first")
		return
	}

	log.Printf("📋 Clipboard content: %d characters", len(clipboardText))

	// Show dialog to get user prompt
	userPrompt, cancelled := showModernInputDialog("Khoj AI - Add Context", "Add instructions or context for the AI:", "Explain this in two sentences")
	if cancelled {
		log.Printf("ℹ️ User cancelled the prompt dialog")
		return
	}

	// Show single notification after user confirms
	showNotification("Khoj AI", "Processing clipboard content...")

	// Prepare the final prompt with user input
	var finalPrompt string
	if userPrompt != "" && userPrompt != "Explain this in two sentences" {
		finalPrompt = fmt.Sprintf("%s\n\nContent:\n%s", userPrompt, clipboardText)
	} else {
		finalPrompt = fmt.Sprintf("Explain this in two sentences:\n\n%s", clipboardText)
	}

	// Create context with timeout - don't defer cancel here since we need it in the goroutine
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)

	// Get API configuration
	apiBase := os.Getenv("KHOJ_API_BASE")
	if apiBase == "" {
		apiBase = "https://app.khoj.dev"
	}

	apiKey := os.Getenv("KHOJ_API_KEY")
	if apiKey == "" {
		log.Printf("❌ KHOJ_API_KEY not set")
		showNotification("Khoj AI Error", "API key not configured")
		return
	}

	log.Printf("🔧 Using API base: %s", apiBase)
	log.Printf("🔧 Using conversation ID: %s", conversationID)

	// Process with AI using existing conversation context
	log.Printf("🤖 Sending request to Khoj AI...")

	go func() {
		defer cancel() // Cancel context when goroutine completes

		// Use the existing Khoj chat API with conversation context
		aiResponse, err := sendToKhojChat(apiBase, apiKey, conversationID, finalPrompt, ctx)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				log.Printf("⏰ AI request timed out after %v", clipboardTimeout)
				// Only show notification for timeout errors
				showNotification("Khoj AI Timeout", fmt.Sprintf("Timed out after %d seconds", int(clipboardTimeout.Seconds())))
			} else {
				log.Printf("❌ AI request failed: %v", err)
				// Only show notification for critical errors
				showNotification("Khoj AI Error", fmt.Sprintf("Request failed: %v", err))
			}
			return
		}

		log.Printf("✅ Received AI response (%d characters)", len(aiResponse))

		// Send the AI response to the current cursor position
		log.Printf("⌨️ Inserting response at cursor...")
		err = sendText(aiResponse)
		if err != nil {
			log.Printf("❌ Failed to send text: %v", err)
			// Only show notification for insertion errors
			showNotification("Khoj AI Error", fmt.Sprintf("Failed to insert: %v", err))
		} else {
			log.Printf("✅ Successfully inserted AI response")
			// No success notification - user can see the text was inserted
		}
	}()
}

// sendToKhojChat sends a message to Khoj using the existing conversation context
func sendToKhojChat(apiBase, apiKey, conversationID, message string, ctx context.Context) (string, error) {
	// Prepare the request body
	requestBody := map[string]interface{}{
		"q":               message,
		"conversation_id": conversationID,
		"stream":          false,
		"train":           false,
		"agent":           currentAgentSlug,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the request
	url := fmt.Sprintf("%s/api/chat", apiBase)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the response
	var khojResp KhojResponse
	if err := json.Unmarshal(body, &khojResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return khojResp.Response, nil
}

// setupKeyboardMonitoring sets up polling-based Ctrl+Q detection
func setupKeyboardMonitoring() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("keyboard monitoring only available on Windows")
	}

	log.Printf("� Setting up keyboard monitoring for Ctrl+Q...")

	// Start polling for Ctrl+Q combination
	go func() {
		getAsyncKeyState := user32.NewProc("GetAsyncKeyState")

		var lastCtrlQState bool
		ticker := time.NewTicker(50 * time.Millisecond) // Check every 50ms
		defer ticker.Stop()

		log.Printf("✅ Keyboard monitoring started! Press Ctrl+Q to use Clipboard AI")
		showNotification("Khoj AI Ready", "Press Ctrl+Q to process clipboard")

		for {
			select {
			case <-ticker.C:
				// Check if both Ctrl and Q are pressed
				ctrlState, _, _ := getAsyncKeyState.Call(VK_CONTROL)
				qState, _, _ := getAsyncKeyState.Call(VK_Q)

				ctrlPressed := (ctrlState & 0x8000) != 0
				qPressed := (qState & 0x8000) != 0

				currentCtrlQState := ctrlPressed && qPressed

				// Trigger only on the rising edge (when Ctrl+Q becomes pressed)
				if currentCtrlQState && !lastCtrlQState {
					log.Printf("🎯 Ctrl+Q detected! Processing clipboard with AI...")

					// Show immediate notification and process
					go func() {
						showNotification("Khoj AI", "Processing clipboard...")
						processClipboardWithAI()
					}()
				}

				lastCtrlQState = currentCtrlQState
			}
		}
	}()

	return nil
}

// testKeyboardState manually checks if Ctrl+Q is currently pressed (for debugging)
func testKeyboardState() {
	if runtime.GOOS != "windows" {
		return
	}

	getAsyncKeyState := user32.NewProc("GetAsyncKeyState")

	qState, _, _ := getAsyncKeyState.Call(VK_Q)
	ctrlState, _, _ := getAsyncKeyState.Call(VK_CONTROL)

	qPressed := (qState & 0x8000) != 0
	ctrlPressed := (ctrlState & 0x8000) != 0

	log.Printf("🔍 Manual key state check:")
	log.Printf("  Q key: %t (raw: %d/0x%x)", qPressed, qState, qState)
	log.Printf("  Ctrl key: %t (raw: %d/0x%x)", ctrlPressed, ctrlState, ctrlState)

	if qPressed && ctrlPressed {
		log.Printf("🎯 Manual detection: Ctrl+Q is currently pressed!")
		showNotification("Debug", "Ctrl+Q detected manually!")
	} else {
		log.Printf("ℹ️ Ctrl+Q not currently pressed")
		showNotification("Debug", fmt.Sprintf("Q:%t Ctrl:%t", qPressed, ctrlPressed))
	}
}

// stopKeyboardMonitoring stops the keyboard monitoring (placeholder for cleanup)
func stopKeyboardMonitoring() {
	// The polling goroutine will stop when the application exits
	log.Printf("Keyboard monitoring stopped")
}

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

var iconData = []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x20, 0x20, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0xa8, 0x10, 0x00, 0x00, 0x16, 0x00, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x0b, 0x01, 0x00, 0x00, 0x62, 0x01, 0x00, 0x00, 0x94, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x94, 0x01, 0x00, 0x00, 0x66, 0x01, 0x00, 0x00, 0x0d, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x65, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xfb, 0x01, 0x00, 0x00, 0x6c, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x98, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x95, 0x01, 0x00, 0x00, 0x5a, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x5a, 0x01, 0x00, 0x00, 0x90, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x9f, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x9a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x58, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa2, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x9a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x58, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x59, 0x01, 0x00, 0x00, 0x92, 0x01, 0x00, 0x00, 0x16, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa2, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x9a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x58, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x98, 0x01, 0x00, 0x00, 0xe6, 0x01, 0x00, 0x00, 0x2d, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa2, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x9a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x58, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x19, 0x01, 0x00, 0x00, 0x2f, 0x01, 0x00, 0x00, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa2, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x9a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x60, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x0b, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x0c, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x59, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa2, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x8a, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xda, 0x01, 0x00, 0x00, 0xc6, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xc6, 0x01, 0x00, 0x00, 0xd9, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x92, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x31, 0x01, 0x00, 0x00, 0xc6, 0x01, 0x00, 0x00, 0xef, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xef, 0x01, 0x00, 0x00, 0xca, 0x01, 0x00, 0x00, 0x36, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x15, 0x01, 0x00, 0x00, 0x2d, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x2d, 0x01, 0x00, 0x00, 0x16, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x02, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x10, 0x01, 0x00, 0x00, 0x23, 0x01, 0x00, 0x00, 0x23, 0x01, 0x00, 0x00, 0x12, 0x01, 0x00, 0x00, 0x02, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xa3, 0x01, 0x00, 0x00, 0x23, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x2c, 0x01, 0x00, 0x00, 0x8a, 0x01, 0x00, 0x00, 0xcc, 0x01, 0x00, 0x00, 0xe6, 0x01, 0x00, 0x00, 0xe7, 0x01, 0x00, 0x00, 0xcf, 0x01, 0x00, 0x00, 0x90, 0x01, 0x00, 0x00, 0x31, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x4d, 0x01, 0x00, 0x00, 0xe4, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0x61, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x02, 0x01, 0x00, 0x00, 0x5b, 0x01, 0x00, 0x00, 0xdf, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xf5, 0x01, 0x00, 0x00, 0xdc, 0x01, 0x00, 0x00, 0xdb, 0x01, 0x00, 0x00, 0xf3, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xe4, 0x01, 0x00, 0x00, 0x64, 0x01, 0x00, 0x00, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x4d, 0x01, 0x00, 0x00, 0xe4, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0x83, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x51, 0x01, 0x00, 0x00, 0xed, 0x01, 0x00, 0x00, 0xf6, 0x01, 0x00, 0x00, 0x9d, 0x01, 0x00, 0x00, 0x3b, 0x01, 0x00, 0x00, 0x17, 0x01, 0x00, 0x00, 0x18, 0x01, 0x00, 0x00, 0x3a, 0x01, 0x00, 0x00, 0x98, 0x01, 0x00, 0x00, 0xf5, 0x01, 0x00, 0x00, 0xf2, 0x01, 0x00, 0x00, 0x5c, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x4d, 0x01, 0x00, 0x00, 0xe3, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xb5, 0x01, 0x00, 0x00, 0x3d, 0x01, 0x00, 0x00, 0x34, 0x01, 0x00, 0x00, 0x34, 0x01, 0x00, 0x00, 0x42, 0x01, 0x00, 0x00, 0xcf, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xab, 0x01, 0x00, 0x00, 0x3b, 0x01, 0x00, 0x00, 0x31, 0x01, 0x00, 0x00, 0x2d, 0x01, 0x00, 0x00, 0x07, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x72, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0xd7, 0x01, 0x00, 0x00, 0x22, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x10, 0x01, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x3a, 0x01, 0x00, 0x00, 0xe3, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xf5, 0x01, 0x00, 0x00, 0xf3, 0x01, 0x00, 0x00, 0xf4, 0x01, 0x00, 0x00, 0xf4, 0x01, 0x00, 0x00, 0xf4, 0x01, 0x00, 0x00, 0xfe, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xf6, 0x01, 0x00, 0x00, 0xf3, 0x01, 0x00, 0x00, 0xf6, 0x01, 0x00, 0x00, 0xd9, 0x01, 0x00, 0x00, 0x20, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0xab, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x6e, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x2a, 0x01, 0x00, 0x00, 0xb7, 0x01, 0x00, 0x00, 0x69, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x20, 0x01, 0x00, 0x00, 0xbb, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xd9, 0x01, 0x00, 0x00, 0xbe, 0x01, 0x00, 0x00, 0xc0, 0x01, 0x00, 0x00, 0xc1, 0x01, 0x00, 0x00, 0xeb, 0x01, 0x00, 0x00, 0xfe, 0x01, 0x00, 0x00, 0xd0, 0x01, 0x00, 0x00, 0xbf, 0x01, 0x00, 0x00, 0xc0, 0x01, 0x00, 0x00, 0xc1, 0x01, 0x00, 0x00, 0xaa, 0x01, 0x00, 0x00, 0x19, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x58, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa8, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x53, 0x01, 0x00, 0x00, 0xf3, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0x67, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x21, 0x01, 0x00, 0x00, 0xba, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xc6, 0x01, 0x00, 0x00, 0x2c, 0x01, 0x00, 0x00, 0x06, 0x01, 0x00, 0x00, 0x0f, 0x01, 0x00, 0x00, 0xb9, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0x44, 0x01, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x37, 0x01, 0x00, 0x00, 0xf6, 0x01, 0x00, 0x00, 0xbe, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x02, 0x01, 0x00, 0x00, 0x67, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0x67, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x21, 0x01, 0x00, 0x00, 0xba, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xbe, 0x01, 0x00, 0x00, 0x25, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0xb0, 0x01, 0x00, 0x00, 0xfc, 0x01, 0x00, 0x00, 0x47, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x09, 0x01, 0x00, 0x00, 0x42, 0x01, 0x00, 0x00, 0x4d, 0x01, 0x00, 0x00, 0x4c, 0x01, 0x00, 0x00, 0x4a, 0x01, 0x00, 0x00, 0x75, 0x01, 0x00, 0x00, 0xf9, 0x01, 0x00, 0x00, 0xd0, 0x01, 0x00, 0x00, 0x51, 0x01, 0x00, 0x00, 0x4b, 0x01, 0x00, 0x00, 0x4e, 0x01, 0x00, 0x00, 0xb1, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xf0, 0x01, 0x00, 0x00, 0x67, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x21, 0x01, 0x00, 0x00, 0xba, 0x01, 0x00, 0x00, 0xf8, 0x01, 0x00, 0x00, 0x5f, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x89, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0x80, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x1d, 0x01, 0x00, 0x00, 0xde, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xfd, 0x01, 0x00, 0x00, 0xfb, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xfe, 0x01, 0x00, 0x00, 0xfc, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xee, 0x01, 0x00, 0x00, 0x46, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x24, 0x01, 0x00, 0x00, 0x5d, 0x01, 0x00, 0x00, 0x0a, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x42, 0x01, 0x00, 0x00, 0xf3, 0x01, 0x00, 0x00, 0xdc, 0x01, 0x00, 0x00, 0x2e, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x13, 0x01, 0x00, 0x00, 0x92, 0x01, 0x00, 0x00, 0xa9, 0x01, 0x00, 0x00, 0xa4, 0x01, 0x00, 0x00, 0xc7, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xf4, 0x01, 0x00, 0x00, 0xb3, 0x01, 0x00, 0x00, 0xa7, 0x01, 0x00, 0x00, 0xa8, 0x01, 0x00, 0x00, 0xa5, 0x01, 0x00, 0x00, 0xcc, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xae, 0x01, 0x00, 0x00, 0x1a, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x08, 0x01, 0x00, 0x00, 0x9f, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xc2, 0x01, 0x00, 0x00, 0x36, 0x01, 0x00, 0x00, 0x02, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x37, 0x01, 0x00, 0x00, 0xc3, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xa1, 0x01, 0x00, 0x00, 0x0a, 0x01, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x2f, 0x01, 0x00, 0x00, 0xcc, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xad, 0x01, 0x00, 0x00, 0x19, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x20, 0x01, 0x00, 0x00, 0xbc, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xe6, 0x01, 0x00, 0x00, 0x9e, 0x01, 0x00, 0x00, 0x6e, 0x01, 0x00, 0x00, 0x6e, 0x01, 0x00, 0x00, 0x9e, 0x01, 0x00, 0x00, 0xe6, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xbd, 0x01, 0x00, 0x00, 0x21, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x2d, 0x01, 0x00, 0x00, 0xcb, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xad, 0x01, 0x00, 0x00, 0x19, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x1d, 0x01, 0x00, 0x00, 0x93, 0x01, 0x00, 0x00, 0xeb, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xeb, 0x01, 0x00, 0x00, 0x94, 0x01, 0x00, 0x00, 0x1d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x50, 0x01, 0x00, 0x00, 0xef, 0x01, 0x00, 0x00, 0xae, 0x01, 0x00, 0x00, 0x19, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x30, 0x01, 0x00, 0x00, 0x6c, 0x01, 0x00, 0x00, 0x8e, 0x01, 0x00, 0x00, 0x8e, 0x01, 0x00, 0x00, 0x6c, 0x01, 0x00, 0x00, 0x31, 0x01, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x04, 0x01, 0x00, 0x00, 0x45, 0x01, 0x00, 0x00, 0x1b, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xf0, 0x00, 0x00, 0x0f, 0xf0, 0x00, 0x00, 0x0f, 0xf0, 0x00, 0x00, 0x0f, 0xf1, 0xff, 0xff, 0x87, 0xf1, 0x1f, 0xff, 0x87, 0xf1, 0x1f, 0xff, 0x87, 0xf1, 0x1f, 0xff, 0x87, 0xf0, 0x00, 0x00, 0x07, 0xf0, 0x00, 0x00, 0x0f, 0xf0, 0x00, 0x00, 0x0f, 0xf8, 0x00, 0x00, 0x1f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xf9, 0xf8, 0x1f, 0xff, 0xf8, 0xe0, 0x07, 0xff, 0xf0, 0xc0, 0x03, 0xff, 0xe0, 0xc0, 0x03, 0xff, 0xc0, 0x00, 0x41, 0x9f, 0x80, 0x00, 0x61, 0x0f, 0x80, 0x00, 0x71, 0x07, 0xc0, 0x00, 0x70, 0x03, 0xe0, 0x0e, 0x00, 0x01, 0xf0, 0x8e, 0x00, 0x01, 0xf8, 0x86, 0x00, 0x01, 0xff, 0x81, 0x80, 0x83, 0xff, 0xc0, 0x03, 0x07, 0xff, 0xe0, 0x07, 0x0f, 0xff, 0xf0, 0x0f, 0x1f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

type serverControl struct {
	srv     *http.Server
	stopCh  chan struct{}
	running bool
}

var globalServer *serverControl

func getAPIKeyStatus() string {
	apiKey := os.Getenv("KHOJ_API_KEY")
	if apiKey == "" || apiKey == "dummy" {
		return "🔑 API Key: Not Set"
	}
	return "🔑 API Key: ✅ Set"
}

func onReady() {
	systray.SetIcon(iconData)
	systray.SetTitle("Khoj Provider")
	systray.SetTooltip("Khoj OpenAI Wrapper Server")

	// Set up keyboard monitoring for Ctrl+Q (Windows only)
	if runtime.GOOS == "windows" {
		if err := setupKeyboardMonitoring(); err != nil {
			log.Printf("Failed to setup keyboard monitoring: %v", err)
		}

		// Check notification settings on startup
		checkNotificationSettings()
	}

	// Menu items
	mStart := systray.AddMenuItem("Start Server", "Start the server")
	mStop := systray.AddMenuItem("Stop Server", "Stop the server")
	mStatus := systray.AddMenuItem("Status: Stopped", "Server status")
	systray.AddSeparator()

	// Conversation management
	mConvID := systray.AddMenuItem("Conv: "+getConversationDisplayID(), "Current conversation ID")
	mConvID.Disable() // Read-only status
	mNewConv := systray.AddMenuItem("🆕 New Conversation", "Create a new conversation")
	mEditConv := systray.AddMenuItem("✏️ Edit Conversation ID", "Change conversation ID")
	mAgentSlug := systray.AddMenuItem("🤖 Agent: "+currentAgentSlug, "Current agent slug")
	mAgentSlug.Disable() // Read-only status
	mEditAgent := systray.AddMenuItem("⚙️ Edit Agent Slug", "Change agent slug")
	systray.AddSeparator()

	mAPIKey := systray.AddMenuItem(getAPIKeyStatus(), "API Key status")
	mAPIKey.Disable() // Read-only status
	systray.AddSeparator()

	// Clipboard AI feature (Windows only)
	var mClipboardAI *systray.MenuItem
	var mTestKeys *systray.MenuItem
	var mTestNotification *systray.MenuItem
	if runtime.GOOS == "windows" {
		mClipboardAI = systray.AddMenuItem("📋 Clipboard AI (Ctrl+Q)", "Process clipboard with AI and insert at cursor")
		mTestKeys = systray.AddMenuItem("🔍 Test Keyboard State", "Debug keyboard hook detection")
		mTestNotification = systray.AddMenuItem("🔔 Test Notification", "Test Windows toast notification")
		systray.AddSeparator()
	}

	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	mStop.Disable()

	// Initialize server control
	globalServer = &serverControl{
		stopCh:  make(chan struct{}),
		running: false,
	}

	// Handle menu clicks
	go func() {
		for {
			select {
			case <-mStart.ClickedCh:
				if !globalServer.running {
					go startServer()
					mStart.Disable()
					mStop.Enable()
					mStatus.SetTitle("Status: Running")
					systray.SetTooltip("Khoj Server: Running on port 3002")
				}

			case <-mStop.ClickedCh:
				if globalServer.running {
					stopServer()
					mStart.Enable()
					mStop.Disable()
					mStatus.SetTitle("Status: Stopped")
					systray.SetTooltip("Khoj Server: Stopped")
				}

			case <-mNewConv.ClickedCh:
				if err := createNewConversationFromMenu(); err != nil {
					log.Printf("Failed to create new conversation: %v", err)
				} else {
					mConvID.SetTitle("Conv: " + getConversationDisplayID())
				}

			case <-mEditConv.ClickedCh:
				if err := editConversationIDDialog(); err != nil {
					log.Printf("Failed to edit conversation ID: %v", err)
				} else {
					mConvID.SetTitle("Conv: " + getConversationDisplayID())
				}

			case <-mEditAgent.ClickedCh:
				if err := editAgentSlugDialog(); err != nil {
					log.Printf("Failed to edit agent slug: %v", err)
				} else {
					mAgentSlug.SetTitle("🤖 Agent: " + currentAgentSlug)
				}

			case <-mQuit.ClickedCh:
				if globalServer.running {
					stopServer()
				}
				systray.Quit()
				return
			}
		}
	}()

	// Handle clipboard AI menu clicks in a separate goroutine (Windows only)
	if mClipboardAI != nil {
		go func() {
			for {
				select {
				case <-mClipboardAI.ClickedCh:
					log.Printf("📋 Clipboard AI menu clicked")
					go processClipboardWithAI()
				}
			}
		}()
	}

	// Handle test keyboard state menu clicks (Windows only)
	if mTestKeys != nil {
		go func() {
			for {
				select {
				case <-mTestKeys.ClickedCh:
					log.Printf("🔍 Test keyboard state menu clicked")
					testKeyboardState()
				}
			}
		}()
	}

	// Handle test notification menu clicks (Windows only)
	if mTestNotification != nil {
		go func() {
			for {
				select {
				case <-mTestNotification.ClickedCh:
					log.Printf("🔔 Test notification menu clicked")
					checkNotificationSettings()
					showNotification("Test Notification", "This is a test notification to verify Windows toast notifications are working.")
				}
			}
		}()
	}

	// Auto-start server
	go startServer()
	mStart.Disable()
	mStop.Enable()
	mStatus.SetTitle("Status: Running")
}

func startServer() {
	apiBase := os.Getenv("KHOJ_API_BASE")
	if apiBase == "" {
		apiBase = "https://app.khoj.dev"
	}

	apiKey := os.Getenv("KHOJ_API_KEY")
	if apiKey == "" {
		log.Printf("KHOJ_API_KEY not set, using default")
		apiKey = "dummy"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3002"
	}

	// log.Printf("Starting Khoj provider with API Base: %s", apiBase)
	// log.Printf("API Key: %s...", apiKey[:min(len(apiKey), 8)])

	timeoutStr := os.Getenv("KHOJ_TIMEOUT")
	timeout := 120 * time.Second
	if timeoutStr != "" {
		if parsedTimeout, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsedTimeout
		}
	}

	log.Printf("Using timeout: %v", timeout)
	provider := NewKhojProviderWithTimeout(apiBase, apiKey, timeout)

	// Handle conversation creation if needed
	if newConversation || conversationID == "" {
		log.Printf("Creating new conversation...")
		newConvID, err := createNewConversation(apiBase, apiKey)
		if err != nil {
			log.Printf("Failed to create new conversation: %v", err)
			globalServer.running = false
			return
		}

		conversationID = newConvID
		newConversation = false

		// Save the new conversation ID to file
		state := &ConversationState{
			LastConversationID: conversationID,
			AgentSlug:          currentAgentSlug,
			CreatedAt:          time.Now(),
		}
		if err := saveConversationState(state); err != nil {
			log.Printf("Warning: Failed to save conversation state: %v", err)
		}

		log.Printf("✅ New conversation created: %s", conversationID)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
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

	globalServer.srv = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	globalServer.running = true
	// log.Printf("Khoj provider server starting on :%s", port)

	if err := globalServer.srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("Server error: %v", err)
		globalServer.running = false
	}
}

func stopServer() {
	if globalServer.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		globalServer.srv.Shutdown(ctx)
		globalServer.running = false
		// log.Printf("Server stopped")
	}
}

func onExit() {
	// Clean up keyboard monitoring
	if runtime.GOOS == "windows" {
		stopKeyboardMonitoring()
	}

	if globalServer.running {
		stopServer()
	}
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
		ConversationID: conversationID, // Use global conversation ID (empty for new conversations)
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
	log.Printf("Using conversation ID: %s", conversationID)

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
	// Initialize conversation ID from environment variables and command-line flags
	if err := initializeConversationID(); err != nil {
		log.Fatal("Conversation ID initialization failed: ", err)
	}

	// Initialize systray
	systray.Run(onReady, onExit)
}
