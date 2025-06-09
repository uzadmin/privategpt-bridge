# 🚀 PrivateGPT Bridge Server

Go proxy server with modern web interface for PrivateGPT API.

## ⚡ Quick Start

```bash
git clone https://github.com/uzadmin/privategpt-bridge.git
cd privategpt-bridge
chmod +x build.sh
./build.sh
./bridge
```

Open http://localhost:8080

## 📋 Requirements

- **Go 1.21+**
- **PrivateGPT** running on localhost:8001

## 🔧 Setup

### 1. Install and Run PrivateGPT
```bash
git clone https://github.com/zylon-ai/private-gpt.git
cd private-gpt
pip install -r requirements.txt
python -m private_gpt
```

### 2. Run Bridge Server
```bash
# In another terminal
git clone https://github.com/uzadmin/privategpt-bridge.git
cd privategpt-bridge
./build.sh
./bridge
```

## ✨ Features

- 🔄 **Reverse Proxy** - Routes browser requests to PrivateGPT API
- 🎨 **Modern UI** - Vue.js interface with file upload
- 📁 **File Support** - PDF, DOCX, TXT, MD, CSV and more
- 🤖 **System Prompts** - Customize AI behavior
- 🌐 **CORS Ready** - Browser compatible

## 🏗️ Architecture

```
Browser → Go Server (8080) → PrivateGPT API (8001)
```

## 📝 Usage

1. **Upload Documents** - Drag & drop files or click upload area
2. **Chat** - Ask questions about your documents
3. **System Prompts** - Configure AI behavior with custom prompts

## 🔧 Configuration

Edit ports in `main.go`:

```go
const (
    PRIVATEGPT_HOST = "http://localhost:8001"  // PrivateGPT API
    SERVER_PORT     = ":8080"                  // Bridge server port
)
```

## 📄 Supported File Formats

PDF, DOCX, DOC, TXT, MD, HTML, CSV, JSON, PPTX, PPT, EPUB, IPYNB

## 🛠️ Development

```bash
# Run in development mode
go run main.go

# Build optimized binary
go build -o bridge main.go
```

## 🐛 Troubleshooting

**API Unavailable**: Check PrivateGPT is running on http://localhost:8001/health

**Port Conflict**: Change `SERVER_PORT` in main.go

**Upload Fails**: Check file size (max 50MB) and format support

## 📁 Project Structure

```
privategpt-bridge/
├── go.mod              # Go module
├── main.go             # Main server
├── build.sh            # Build script
├── static/
│   └── index.html      # Web interface
└── README.md
```

## 📄 License

MIT License

---

⭐ **Star this repo if it helps you!**
