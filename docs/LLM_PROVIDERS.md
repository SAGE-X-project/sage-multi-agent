# LLM Provider Configuration Guide

This document explains how to configure and use different LLM providers in the SAGE Multi-Agent system.

## Table of Contents

- [Overview](#overview)
- [Supported Providers](#supported-providers)
- [Configuration](#configuration)
  - [OpenAI](#openai)
  - [Gemini (Native API)](#gemini-native-api)
  - [Gemini (OpenAI-Compatible)](#gemini-openai-compatible)
  - [Anthropic/Claude](#anthropicclaude)
- [Usage Examples](#usage-examples)
- [Troubleshooting](#troubleshooting)

---

## Overview

The SAGE Multi-Agent system supports multiple LLM providers through a unified `Client` interface. You can switch between providers by setting environment variables without changing any code.

**Key Features:**
- **Pluggable architecture**: Easy to add new providers
- **Environment-based configuration**: No code changes required
- **Automatic retry logic**: Built-in error handling and rate limiting
- **Timeout management**: Configurable request timeouts
- **Graceful degradation**: Falls back to rule-based logic if LLM is unavailable

---

## Supported Providers

| Provider | Identifier | Status | Default Model |
|----------|------------|--------|---------------|
| OpenAI | `openai` | ✅ Fully Supported | `gpt-4o-mini` |
| Gemini (Native) | `gemini-native` | ✅ Fully Supported | `gemini-2.0-flash-exp` |
| Gemini (OpenAI-Compatible) | `gemini` | ✅ Fully Supported | `gemini-2.5-flash` |
| Anthropic Claude | `anthropic` or `claude` | ✅ Fully Supported | `claude-3-5-sonnet-20241022` |

---

## Configuration

### OpenAI

OpenAI is the default provider. It works with official OpenAI API, Azure OpenAI, and any OpenAI-compatible API.

#### Required Environment Variables

```bash
export LLM_PROVIDER=openai
export OPENAI_API_KEY=sk-...
```

#### Optional Environment Variables

```bash
# Override base URL (useful for Azure OpenAI or local servers)
export OPENAI_BASE_URL=https://api.openai.com/v1

# Override model (default: gpt-4o-mini)
export OPENAI_MODEL=gpt-4o

# Set custom timeout (default: 12s)
export LLM_TIMEOUT=15s
```

#### Example Configuration

```bash
# Standard OpenAI
export LLM_PROVIDER=openai
export OPENAI_API_KEY=sk-proj-...
export OPENAI_MODEL=gpt-4o-mini

# Azure OpenAI
export LLM_PROVIDER=openai
export OPENAI_API_KEY=<your-azure-key>
export OPENAI_BASE_URL=https://<your-resource>.openai.azure.com/openai/deployments/<deployment-id>

# Local LLM (Ollama, LM Studio, etc.)
export LLM_PROVIDER=openai
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_MODEL=llama3.2
export LLM_ALLOW_NO_KEY=true
```

---

### Gemini (Native API)

Uses Google's native Gemini REST API for full feature access. **Recommended** for Gemini usage.

#### Required Environment Variables

```bash
export LLM_PROVIDER=gemini-native
export GEMINI_API_KEY=AIza...
```

#### Optional Environment Variables

```bash
# Override model (default: gemini-2.0-flash-exp)
export GEMINI_MODEL=gemini-2.0-pro

# Set custom timeout (default: 12s)
export LLM_TIMEOUT=15s
```

#### Example Configuration

```bash
export LLM_PROVIDER=gemini-native
export GEMINI_API_KEY=AIzaSyC...
export GEMINI_MODEL=gemini-2.0-flash-exp
```

#### Supported Models

- `gemini-2.0-flash-exp` (default, fastest)
- `gemini-2.0-pro` (more capable)
- `gemini-2.5-flash` (latest flash model)
- `gemini-1.5-pro` (stable version)

#### Getting API Key

1. Visit [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Sign in with your Google account
3. Click "Get API Key"
4. Copy the API key

---

### Gemini (OpenAI-Compatible)

Uses Gemini's OpenAI-compatible endpoint. Useful if you want to use Gemini with existing OpenAI-compatible code.

#### Required Environment Variables

```bash
export LLM_PROVIDER=gemini
export GEMINI_API_KEY=AIza...
```

#### Optional Environment Variables

```bash
# Override base URL (rarely needed)
export GEMINI_API_URL=https://generativelanguage.googleapis.com/v1beta/openai

# Override model (default: gemini-2.5-flash)
export GEMINI_MODEL=gemini-2.5-flash
```

#### Example Configuration

```bash
export LLM_PROVIDER=gemini
export GEMINI_API_KEY=AIzaSyC...
export GEMINI_MODEL=gemini-2.5-flash
```

**Note:** The native API (`gemini-native`) is recommended over this OpenAI-compatible endpoint for better performance and feature support.

---

### Anthropic/Claude

Uses Anthropic's Claude API for high-quality reasoning and long context support.

#### Required Environment Variables

```bash
export LLM_PROVIDER=anthropic  # or "claude"
export ANTHROPIC_API_KEY=sk-ant-...
```

#### Optional Environment Variables

```bash
# Override model (default: claude-3-5-sonnet-20241022)
export ANTHROPIC_MODEL=claude-3-opus-20240229

# Set custom timeout (default: 12s)
export LLM_TIMEOUT=15s
```

#### Example Configuration

```bash
export LLM_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-api03-...
export ANTHROPIC_MODEL=claude-3-5-sonnet-20241022
```

#### Supported Models

- `claude-3-5-sonnet-20241022` (default, best balance)
- `claude-3-opus-20240229` (most capable, slower)
- `claude-3-haiku-20240307` (fastest, economical)

#### Getting API Key

1. Visit [Anthropic Console](https://console.anthropic.com/)
2. Sign up for an account
3. Navigate to API Keys section
4. Create a new API key

---

## Usage Examples

### Basic Usage in Code

The LLM client is automatically initialized from environment variables:

```go
import (
    "context"
    "log"
    "github.com/sage-x-project/sage-multi-agent/llm"
)

func main() {
    // Create client from environment variables
    client, err := llm.NewFromEnv()
    if err != nil {
        log.Fatalf("Failed to create LLM client: %v", err)
    }

    // Use the client
    ctx := context.Background()
    systemPrompt := "You are a helpful assistant."
    userMessage := "What is the capital of France?"

    response, err := client.Chat(ctx, systemPrompt, userMessage)
    if err != nil {
        log.Fatalf("LLM request failed: %v", err)
    }

    log.Printf("Response: %s", response)
}
```

### Switching Providers

Simply change the `LLM_PROVIDER` environment variable and restart your application:

```bash
# Use OpenAI
export LLM_PROVIDER=openai
export OPENAI_API_KEY=sk-...

# Switch to Gemini
export LLM_PROVIDER=gemini-native
export GEMINI_API_KEY=AIza...

# Switch to Claude
export LLM_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-...
```

### Agent Integration

All agents in the SAGE system automatically use the configured LLM provider:

```go
// agents/root/agent.go
func (r *RootAgent) ensureLLM() {
    if r.llmClient != nil {
        return
    }

    // This automatically uses the provider from environment
    if c, err := llm.NewFromEnv(); err == nil {
        r.llmClient = c
        r.logger.Printf("[llm] ready with provider from env")
    } else {
        r.logger.Printf("[llm] disabled: %v", err)
    }
}
```

---

## Troubleshooting

### Issue: "llm client disabled (missing key or base url)"

**Cause:** No API key configured for the selected provider.

**Solution:**
1. Set the appropriate API key environment variable
2. Or, if using a local LLM, set `LLM_ALLOW_NO_KEY=true`

```bash
export OPENAI_API_KEY=sk-...
# OR
export LLM_ALLOW_NO_KEY=true
```

---

### Issue: "429 rate limit" errors

**Cause:** Too many requests to the LLM API.

**Solution:**
1. The client automatically retries with exponential backoff
2. If errors persist, check your API quota/limits
3. Consider upgrading your API plan or using a different model

---

### Issue: Timeout errors

**Cause:** LLM API is taking too long to respond.

**Solution:**
Increase the timeout:

```bash
export LLM_TIMEOUT=30s
```

---

### Issue: "no text content found" with Anthropic

**Cause:** Anthropic API returned empty or non-text content.

**Solution:**
1. Check your API key is valid
2. Verify the model name is correct
3. Check Anthropic's status page for outages

---

### Issue: Provider not recognized

**Cause:** Invalid `LLM_PROVIDER` value.

**Solution:**
Use one of the supported provider identifiers:
- `openai`
- `gemini`
- `gemini-native`
- `anthropic` or `claude`

---

## Best Practices

### 1. **Use Native APIs When Available**

For Gemini, prefer `gemini-native` over `gemini` (OpenAI-compatible):

```bash
# ✅ Recommended
export LLM_PROVIDER=gemini-native

# ⚠️ Works but less optimal
export LLM_PROVIDER=gemini
```

### 2. **Set Appropriate Timeouts**

Adjust timeout based on your use case:

```bash
# Quick responses
export LLM_TIMEOUT=5s

# Complex reasoning
export LLM_TIMEOUT=30s
```

### 3. **Choose Models Based on Task**

- **Simple classification/routing**: Use fast models (gpt-4o-mini, gemini-2.0-flash-exp, claude-3-haiku)
- **Complex reasoning**: Use capable models (gpt-4o, gemini-2.0-pro, claude-3-5-sonnet)
- **Long context**: Use claude-3-5-sonnet (200K context)

### 4. **Monitor API Costs**

Different models have different pricing. For high-volume production:

```bash
# Most economical options
export OPENAI_MODEL=gpt-4o-mini          # OpenAI
export GEMINI_MODEL=gemini-2.0-flash-exp # Gemini (often free tier available)
export ANTHROPIC_MODEL=claude-3-haiku    # Anthropic
```

### 5. **Use Environment Files**

Create separate `.env` files for different environments:

```bash
# .env.development
LLM_PROVIDER=openai
OPENAI_API_KEY=sk-...

# .env.production
LLM_PROVIDER=gemini-native
GEMINI_API_KEY=AIza...
```

---

## Implementation Details

### Client Interface

All providers implement the same simple interface:

```go
type Client interface {
    Chat(ctx context.Context, system, user string) (string, error)
}
```

### Provider Files

- `llm/llm.go` - Core interface, OpenAI client, and provider factory
- `llm/gemini.go` - Gemini native API client
- `llm/anthropic.go` - Anthropic Claude API client
- `llm/logrt.go` - HTTP logging transport for debugging

### Adding New Providers

To add a new provider:

1. Create a new file in `llm/` (e.g., `llm/cohere.go`)
2. Implement the `Client` interface
3. Add a case in `NewFromEnv()` switch statement in `llm/llm.go`
4. Update `.env.example` with configuration
5. Update this documentation

---

## API Reference

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `LLM_PROVIDER` | Provider identifier | `openai` |
| `LLM_TIMEOUT` | Request timeout | `12s` |
| `LLM_ALLOW_NO_KEY` | Allow missing API key for localhost | `false` |
| `OPENAI_API_KEY` | OpenAI API key | - |
| `OPENAI_BASE_URL` | OpenAI base URL | `https://api.openai.com/v1` |
| `OPENAI_MODEL` | OpenAI model | `gpt-4o-mini` |
| `GEMINI_API_KEY` | Gemini API key | - |
| `GEMINI_MODEL` | Gemini model | varies by provider type |
| `ANTHROPIC_API_KEY` | Anthropic API key | - |
| `ANTHROPIC_MODEL` | Anthropic model | `claude-3-5-sonnet-20241022` |

---

## Additional Resources

- [OpenAI API Documentation](https://platform.openai.com/docs/api-reference)
- [Google Gemini API Documentation](https://ai.google.dev/docs)
- [Anthropic Claude API Documentation](https://docs.anthropic.com/)
- [SAGE Multi-Agent Architecture](./ARCHITECTURE.md)

---

**Last Updated:** 2025-11-04
**Version:** 1.0.0
