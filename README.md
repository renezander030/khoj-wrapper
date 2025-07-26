# Khoj Wrapper - Claude 4 for $20/month from your command line

**Use Claude 4 for just $20 flat from your command line.** This cross-platform application provides an OpenAI-compatible API wrapper for Khoj, allowing you to access Claude Sonnet 4 and other premium AI models through any OpenAI-compatible client at a fraction of the cost.

**Supports Windows, macOS, and Linux** with identical functionality across all platforms.

## Key Features

- **💰 Cost-effective**: Access Claude Sonnet 4 for $20/month flat rate through Khoj
- **🔌 OpenAI Compatible**: Works with any OpenAI API client (aichat, Continue.dev, etc.)
- **🏢 Corporate Friendly**: Works seamlessly in corporate environments with proxy support
- **🖥️ Cross-Platform**: Native system tray integration on Windows, macOS, and Linux
- **🔇 Silent Operation**: Windows version runs without console window (background operation)
- **📁 File Support**: Handle file uploads and code diffs for development workflows
- **🔄 Auto-start**: Configure for system startup across all platforms
- **🌐 CORS Enabled**: Full web client compatibility
- **📊 Health Monitoring**: Built-in health check endpoint
- **🎛️ GUI Management**: Web-based conversation and agent management

## Installation

### Pre-Built Binaries (Recommended)

Download the latest release for your platform from the [Releases page](https://github.com/yourusername/khojWrapper/releases):

- **Windows (64-bit)**: `khoj-wrapper-windows-amd64.exe`
- **macOS (Intel)**: `khoj-wrapper-macos-amd64`
- **macOS (Apple Silicon)**: `khoj-wrapper-macos-arm64`
- **Linux (64-bit)**: `khoj-wrapper-linux-amd64`

#### Quick Installation
```bash
# Example for Linux (replace with your platform)
wget https://github.com/yourusername/khojWrapper/releases/latest/download/khoj-wrapper-linux-amd64
chmod +x khoj-wrapper-linux-amd64
./khoj-wrapper-linux-amd64
```

#### Verify Download (Optional)
```bash
# Download checksum file
wget https://github.com/yourusername/khojWrapper/releases/latest/download/khoj-wrapper-linux-amd64.sha256

# Verify integrity
sha256sum -c khoj-wrapper-linux-amd64.sha256
```

## For Developers

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/khojWrapper.git
cd khojWrapper

# Install dependencies
go mod tidy

# Build for your current platform
go build -o khoj-wrapper khoj_provider.go

# Run
./khoj-wrapper
```

### Cross-Platform Building

```bash
# Build for Windows (from any platform)
GOOS=windows GOARCH=amd64 go build -o khoj-wrapper.exe khoj_provider.go

# Build for macOS (from any platform)
GOOS=darwin GOARCH=amd64 go build -o khoj-wrapper-macos khoj_provider.go

# Build for Linux (from any platform)
GOOS=linux GOARCH=amd64 go build -o khoj-wrapper-linux khoj_provider.go

# Build for ARM64 (Apple Silicon, Raspberry Pi, etc.)
GOOS=darwin GOARCH=arm64 go build -o khoj-wrapper-macos-arm64 khoj_provider.go
GOOS=linux GOARCH=arm64 go build -o khoj-wrapper-linux-arm64 khoj_provider.go
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

## Platform Setup

### Environment Variables (All Platforms)

#### Windows
1. **Set Khoj API Key** (Required):
   - Press `Win + R`, type `sysdm.cpl`, press Enter
   - Click "Environment Variables"
   - Under "User variables", click "New"
   - Variable name: `KHOJ_API_KEY`
   - Variable value: Your Khoj API key from [app.khoj.dev](https://app.khoj.dev)

#### macOS/Linux
1. **Set Khoj API Key** (Required):
   ```bash
   # Add to your shell profile (~/.bashrc, ~/.zshrc, etc.)
   export KHOJ_API_KEY="your-khoj-api-key-here"

   # Or set temporarily for current session
   export KHOJ_API_KEY="your-khoj-api-key-here"
   ```

2. **Optional Environment Variables**:
   ```
   KHOJ_API_BASE=https://app.khoj.dev (default)
   PORT=3002 (default)
   KHOJ_TIMEOUT=120s (default)
   ```

### Autostart Configuration

#### Windows
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

#### macOS
1. **Using Login Items**:
   - System Preferences → Users & Groups → Login Items
   - Click "+" and add `khoj-wrapper-macos`
   - The application will start automatically on login

2. **Using LaunchAgent** (Advanced):
   ```bash
   # Create launch agent file
   cat > ~/Library/LaunchAgents/com.khoj.wrapper.plist << EOF
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
       <key>Label</key>
       <string>com.khoj.wrapper</string>
       <key>ProgramArguments</key>
       <array>
           <string>/path/to/khoj-wrapper-macos</string>
       </array>
       <key>RunAtLoad</key>
       <true/>
   </dict>
   </plist>
   EOF

   # Load the launch agent
   launchctl load ~/Library/LaunchAgents/com.khoj.wrapper.plist
   ```

#### Linux
1. **Using Autostart Desktop Entry**:
   ```bash
   # Create autostart directory if it doesn't exist
   mkdir -p ~/.config/autostart

   # Create desktop entry
   cat > ~/.config/autostart/khoj-wrapper.desktop << EOF
   [Desktop Entry]
   Type=Application
   Name=Khoj Wrapper
   Exec=/path/to/khoj-wrapper-linux
   Hidden=false
   NoDisplay=false
   X-GNOME-Autostart-enabled=true
   EOF
   ```

2. **Using systemd user service** (Advanced):
   ```bash
   # Create service file
   cat > ~/.config/systemd/user/khoj-wrapper.service << EOF
   [Unit]
   Description=Khoj Wrapper Service
   After=graphical-session.target

   [Service]
   Type=simple
   ExecStart=/path/to/khoj-wrapper-linux
   Restart=always

   [Install]
   WantedBy=default.target
   EOF

   # Enable and start the service
   systemctl --user daemon-reload
   systemctl --user enable khoj-wrapper.service
   systemctl --user start khoj-wrapper.service
   ```

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

## Platform-Specific Notes

### Windows
- **System Tray**: Full native support
- **Browser**: Uses default browser via `start` command
- **Dependencies**: None (self-contained executable)
- **Console Window**: Hidden by default (runs silently in background)

### macOS
- **System Tray**: Full native support (appears in menu bar)
- **Browser**: Uses default browser via `open` command
- **Dependencies**: None (self-contained executable)
- **Permissions**: May require allowing the app in Security & Privacy settings

### Linux
- **System Tray**: Requires desktop environment with system tray support
  - ✅ KDE, XFCE, MATE, Cinnamon (built-in support)
  - ✅ GNOME (requires [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/))
  - ✅ i3, sway (with status bars like i3status, waybar)
- **Browser**: Uses default browser via `xdg-open` command
- **Dependencies**: `xdg-open` (usually pre-installed)

## Corporate Environment Notes

- Works behind corporate firewalls and proxies
- No special network configuration required
- All traffic goes through standard HTTPS to Khoj servers
- Can be deployed on internal networks for team use
- Cross-platform deployment for mixed environments

## Contributing

### Development Setup

1. **Clone the repository**:
   ```bash
   git clone https://github.com/yourusername/khojWrapper.git
   cd khojWrapper
   ```

2. **Install dependencies**:
   ```bash
   go mod tidy
   ```

### Troubleshooting Builds

**Common Issues:**

1. **macOS Build Failures**:
   ```
   Error: cgo: C compiler "clang" not found
   ```
   - **Cause**: Cross-compiling macOS requires clang and CGO
   - **Quick Fix**: Use `build-simple.*` scripts (skips macOS on non-macOS)
   - **Complete Fix**: Install clang or build on macOS system
   - **CI/CD**: GitHub Actions uses macOS runners automatically

2. **Missing Dependencies**:
   ```
   Error: go: module not found
   ```
   - **Solution**: Run `go mod tidy` before building

3. **Permission Errors** (Linux/macOS):
   ```
   Error: permission denied
   ```
   - **Solution**: `chmod +x scripts/build-all.sh`

4. **CGO Errors on Cross-compilation**:
   - **Solution**: Use platform-specific runners in CI/CD
   - **Local**: Build on target platform for best results

### Quick Reference

## Support
<p><a href="https://www.buymeacoffee.com/reneza"> <img align="left" src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" height="50" width="210" alt="reneza" /></a></p><br><br>


## License

MIT License - see LICENSE file for details.
