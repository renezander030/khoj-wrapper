# Khoj Wrapper - Claude 4 for $20/month from your command line

**Use Claude 4 for just $20 flat from your command line.** This application provides an OpenAI-compatible API wrapper for Khoj, allowing you to access Claude Sonnet 4 and other premium AI models through any OpenAI-compatible client at a fraction of the cost.

## Key Features

- **💰 Cost-effective**: Access Claude Sonnet 4 for $20/month flat rate through Khoj
- **🔌 OpenAI Compatible**: Works with any OpenAI API client (aichat, Continue.dev, etc.)
- **🖥️ System Tray**: Clean Windows system tray integration with start/stop controls
- **📁 File Support**: Handle file uploads and code diffs for development workflows
- **🔄 Auto-start**: Configure for Windows startup to run automatically
- **🌐 CORS Enabled**: Full web client compatibility
- **📊 Health Monitoring**: Built-in health check endpoint

## For Developers

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/khojWrapper.git
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

1. **Start the wrapper**: Run `khoj-wrapper.exe` or use the system tray
2. **Verify it's running**: Visit `http://localhost:3002/health`
3. **Use with any OpenAI client**: Point your client to `http://localhost:3002/v1`

The wrapper runs on port 3002 by default and provides these endpoints:
- `/health` - Health check
- `/v1/chat/completions` - Chat completions (OpenAI compatible)
- `/v1/completions` - Text completions (OpenAI compatible)
- `/v1/models` - Available models

## Support
<a href="https://www.buymeacoffee.com/reneza"> <img align="left" src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" height="50" width="210" alt="reneza" /></a>


## License

MIT License - see LICENSE file for details.
