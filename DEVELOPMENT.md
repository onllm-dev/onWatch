# Development Guide

Complete instructions for building SynTrack from source on any platform.

---

## Prerequisites

- Go 1.22 or later
- Git

---

## Quick Build (Any Platform)

```bash
git clone https://github.com/onllm-dev/syntrack.git
cd syntrack
go build -ldflags="-s -w" -o syntrack .
```

---

## Platform-Specific Setup

### macOS

**Install Go (if not installed):**
```bash
brew install go
```

**Build:**
```bash
go build -ldflags="-s -w" -o syntrack .
```

---

### Ubuntu / Debian

**Install Go:**
```bash
sudo apt update && sudo apt install -y golang-go git
```

**Build:**
```bash
go build -ldflags="-s -w" -o syntrack .
```

---

### CentOS / RHEL / Fedora

**Install Go:**
```bash
sudo dnf install -y golang git
```

**Or on older CentOS/RHEL:**
```bash
sudo yum install -y golang git
```

**Build:**
```bash
go build -ldflags="-s -w" -o syntrack .
```

---

### Windows

**Install Go:**

1. Download from https://go.dev/dl/
2. Run the MSI installer

**Or use Chocolatey:**
```powershell
choco install golang git
```

**Or use Winget:**
```powershell
winget install GoLang.Go
```

**Build (PowerShell):**
```powershell
go build -ldflags="-s -w" -o syntrack.exe .
```

**Build (CMD):**
```cmd
go build -ldflags="-s -w" -o syntrack.exe .
```

---

## Cross-Compilation

Build for a different platform from your current machine:

**macOS → Linux:**
```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o syntrack-linux-amd64 .
```

**macOS → Windows:**
```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o syntrack-windows-amd64.exe .
```

**Linux → macOS (ARM64):**
```bash
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o syntrack-darwin-arm64 .
```

---

## Development Workflow

### 1. Clone and Setup

```bash
git clone https://github.com/onllm-dev/syntrack.git
cd syntrack
cp .env.example .env
```

### 2. Configure Environment

Edit `.env` with your API key:
```bash
# macOS/Linux
sed -i '' 's/syn_your_api_key_here/syn_your_actual_key/' .env

# Linux (GNU sed)
sed -i 's/syn_your_api_key_here/syn_your_actual_key/' .env

# Windows (PowerShell)
(Get-Content .env) -replace 'syn_your_api_key_here', 'syn_your_actual_key' | Set-Content .env
```

### 3. Build and Run

```bash
go build -ldflags="-s -w" -o syntrack . && ./syntrack --debug
```

---

## Using Make

If you have Make installed:

```bash
make build    # Build production binary
make test     # Run all tests
make run      # Build and run
make clean    # Clean artifacts
```

---

## Testing

**Run all tests:**
```bash
go test ./...
```

**Run with race detection:**
```bash
go test -race ./...
```

**Run with coverage:**
```bash
go test -cover ./...
```

---

## Production Build

Strip debug symbols for smaller binary:

```bash
go build -ldflags="-s -w -X main.version=1.0.0" -o syntrack .
```

**Binary sizes by platform:**
- macOS ARM64: ~12 MB
- Linux AMD64: ~13 MB
- Windows AMD64: ~13 MB

---

## Troubleshooting

### "go: command not found"

**macOS:** `brew install go`

**Ubuntu/Debian:** `sudo apt install golang-go`

**CentOS/RHEL:** `sudo dnf install golang`

**Windows:** Download from https://go.dev/dl/

### "cannot find module"

```bash
go mod download
```

### Permission denied (Unix)

```bash
chmod +x syntrack
```

---

## Dependencies

SynTrack has minimal external dependencies:

| Package | Purpose |
|---------|---------|
| `modernc.org/sqlite` | Pure Go SQLite driver |
| `github.com/joho/godotenv` | .env file loading |

**Install dependencies:**
```bash
go mod tidy
```

---

## Docker Build (Optional)

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -ldflags="-s -w" -o syntrack .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/syntrack .
COPY .env.example .env
CMD ["./syntrack"]
```

Build and run:
```bash
docker build -t syntrack .
docker run -p 8932:8932 -v $(pwd)/.env:/root/.env syntrack
```
