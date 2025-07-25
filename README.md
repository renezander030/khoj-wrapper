# Khoj Wrapper - Claude 4 for $20/month from your command line

**Use Claude 4 for just $20 flat from your command line.** This application provides an OpenAI-compatible API wrapper for Khoj, allowing you to access Claude Sonnet 4 and other premium AI models through any OpenAI-compatible client at a fraction of the cost.

## Key Features

- **💰 Cost-effective**: Access Claude Sonnet 4 for $20/month flat rate through Khoj
- **🔌 OpenAI Compatible**: Works with any OpenAI API client (aichat, Continue.dev, etc.)
- **🖥️ System Tray**: Clean Windows system tray integration with start/stop controls
- **🔄 Auto-start**: Configure for Windows startup to run automatically
- **📊 Health Monitoring**: Built-in health check endpoint

## For Developers

### Building from Source

```bash
# Clone the repository
git clone (this repository URL)
cd khojWrapper

# Install dependencies
go mod tidy

# Update the conversation ID (you can get the ID from khoj by starting a chat with any model of your choice)
const continueConversationID = "your-conversation-id-here"

# Build for Windows
go build -o khoj-wrapper.exe khoj_provider.go

# Run
./khoj-wrapper.exe
```

### Client Configuration

This wrapper is compatible with [aichat](https://github.com/sigoden/aichat) and other OpenAI-compatible clients.

#### AIChat Configuration

Add to your `~/.config/aichat/config.yaml`:

```yaml
clients:
  - type: openai
    name: khoj
    api_base: http://localhost:3002/v1
    api_key: dummy
    models:
      - name: khoj-chat
        max_input_tokens: 40960
        max_output_tokens: 2048
```

Then use with:
```bash
aichat -m khoj-chat "Hello Claude!"
```

#### Continue.dev Configuration

Add to your Continue configuration:

```json
{
  "models": [
    {
      "title": "Khoj Claude",
      "provider": "openai",
      "model": "khoj-chat",
      "apiBase": "http://localhost:3002/v1",
      "apiKey": "your-khoj-api-key"
    }
  ]
}
```

Note: For now only chat will work.

## Windows Setup

### Environment Variables

1. **Set Khoj API Key** (Required):
   - Press `Win + R`, type `sysdm.cpl`, press Enter
   - Click "Environment Variables"
   - Under "User variables", click "New"
   - Variable name: `KHOJ_API_KEY`
   - Variable value: Your Khoj API key from [app.khoj.dev](https://app.khoj.dev)

2. **Optional Environment Variables**:
   ```
   KHOJ_API_BASE=https://app.khoj.dev (default)
   PORT=3002 (default)
   KHOJ_TIMEOUT=120s (default)
   ```

### Windows Autostart

1. **Using Startup Folder** (Recommended):
   - Press `Win + R`, type `shell:startup`, press Enter
   - Copy `khoj-wrapper.exe` to this folder
   - The application will start automatically on Windows boot

2. **Using Task Scheduler** (Advanced):
   - Open Task Scheduler
   - Create Basic Task → "Khoj Wrapper"
   - Trigger: "When the computer starts"
   - Action: "Start a program"
   - Program: Path to `khoj-wrapper.exe`
   - Check "Run with highest privileges"

### Getting Your Khoj API Key

1. Visit [app.khoj.dev](https://app.khoj.dev)
2. Sign up for a $20/month subscription
3. Go to Settings → API Keys
4. Generate a new API key
5. Set it as the `KHOJ_API_KEY` environment variable

## Usage

### First Time Setup

1. **Start the wrapper**: Run `khoj-wrapper.exe`
2. **Automatic conversation creation**: The app will automatically create a new conversation on first run
3. **Conversation persistence**: Your conversation ID is saved to `conversation_state.json`
4. **Verify it's running**: Visit `http://localhost:3002/health`
5. **Use with any OpenAI client**: Point your client to `http://localhost:3002/v1`

### Command Line Options

```bash
khoj-wrapper.exe [options]

Options:
  -n                    Start a new conversation (creates fresh conversation session)
  -conversation-id ID   Use specific conversation ID (overrides saved state)
```

### System Tray Features

The application provides a rich system tray interface for conversation management:

- **🆕 New Conversation**: Click to create a new conversation session instantly
- **Conv: ...xxxx**: Shows the last 4 characters of your current conversation ID
- **✏️ Edit Conversation ID**: Opens a web form to change the active conversation ID
- **🤖 Agent**: Shows the current agent slug being used
- **⚙️ Edit Agent Slug**: Opens a web form to change the AI agent slug

### Conversation Management

- **Automatic Creation**: If no saved conversation exists, a new one is created automatically
- **Persistent State**: Conversation IDs are saved in `conversation_state.json` in the app directory
- **New Conversations**: Use `-n` flag or system tray menu to start fresh conversations anytime
- **Manual Override**: Use `-conversation-id` to switch to specific conversation contexts

### Finding Agent Slugs

To use different AI agents (models), you need to find their agent slugs from the Khoj web interface:

1. **Open Khoj Web App**: Go to [app.khoj.dev](https://app.khoj.dev) in your browser
2. **Open Developer Tools**: Press `F12` or right-click → "Inspect Element"
3. **Go to Network Tab**: Click the "Network" tab in developer tools
4. **Start New Conversation**: Click "New Conversation" in the web interface
5. **Select Your Agent**: Choose the AI model/agent you want to use
6. **Find the Request**: Look for a POST request to `/api/chat/sessions` in the network tab
7. **Check Payload**: Click on the request and look at the "Payload" or "Request" section
8. **Copy Agent Slug**: Find the `agent_slug` value (e.g., `"sonnet-short-025716"`, `"gpt-4o-mini"`, etc.)

#### Common Agent Slugs
- `sonnet-short-025716` - Claude Sonnet (default)
- `gpt-4o-mini` - GPT-4o Mini
- `gpt-4o` - GPT-4o
- `o1-preview` - OpenAI o1 Preview
- `gemini-pro` - Google Gemini Pro

#### Using Custom Agent Slugs

1. **System Tray Menu**: Click "⚙️ Edit Agent Slug" for a user-friendly web form
2. **Edit JSON File**: Alternatively, modify `conversation_state.json` and change the `agent_slug` field
3. **Restart Application**: Changes take effect immediately (no restart needed for menu changes)
4. **System Tray**: The current agent is displayed in the system tray menu

#### Web-Based Editing

The edit functions open a clean web form in your default browser:
- **User-friendly interface** with proper styling and validation
- **Auto-focus and text selection** for quick editing
- **Automatic saving** when you submit the form
- **Real-time menu updates** to reflect changes
- **5-minute timeout** for security (form closes automatically)

The wrapper runs on port 3002 by default and provides these endpoints:
- `/health` - Health check
- `/v1/chat/completions` - Chat completions (OpenAI compatible)
- `/v1/completions` - Text completions (OpenAI compatible)
- `/v1/models` - Available models

### Advanced Features

- **Automatic Conversation Management**: Creates and manages Khoj conversation sessions automatically
- **Persistent State**: Conversation context is maintained across app restarts via JSON file
- **Session Creation**: Uses Khoj's `/api/chat/sessions` endpoint with `sonnet-short-025716` agent
- **Flexible Conversation Control**: Switch between conversations or start fresh ones as needed

## Future Additions
- **📁 File Support**: Handle file uploads and code diffs for development workflows

## Support
<p><a href="https://www.buymeacoffee.com/reneza"> <img align="left" src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" height="50" width="210" alt="reneza" /></a></p><br><br>


## License

MIT License - see LICENSE file for details.
