#!/bin/bash

echo "🔨 Building PrivateGPT Bridge Server..."

# Проверяем, что go.mod существует
if [ ! -f "go.mod" ]; then
    echo "❌ go.mod not found! Run: go mod init first"
    exit 1
fi

# Проверяем, что static/index.html существует
if [ ! -f "static/index.html" ]; then
    echo "❌ static/index.html not found!"
    exit 1
fi

# Скачиваем зависимости (если есть)
echo "📦 Downloading dependencies..."
go mod tidy

# Собираем Go приложение
echo "🏗️ Building Go server..."
go build -o bridge main.go

# Проверяем успешность сборки
if [ $? -eq 0 ]; then
    echo "✅ Build successful!"
    echo ""
    echo "🚀 To start server, run:"
    echo "   ./bridge"
    echo ""
    echo "📂 Server will serve:"
    echo "   - HTML files from: ./static/"
    echo "   - API proxy to: http://localhost:8001"
    echo "   - Server port: :8080"
else
    echo "❌ Build failed!"
    exit 1
fi
