package enrich

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/provenance"
	"gopkg.in/yaml.v3"
)

// Input holds everything needed to run git history enrichment on a set of draft records.
type Input struct {
	RepoDir     string   // root of the git repository (working dir for git log)
	RecordFiles []string // paths to draft blueprint records to enrich
	APIKey      string   // LLM API key; falls back to OPENAI_API_KEY or ANTHROPIC_API_KEY env vars
	Provider    string   // "openai" or "anthropic"; auto-detected from env vars if empty
	Model       string   // optional model override; sensible defaults per provider
	LLMBaseURL  string   // optional base URL for local LLMs (e.g., http://localhost:1234); provider path appended automatically
	Progress    func(msg string) // optional; called at each stage per record for progress reporting
}

// Result is the outcome of enriching one record.
type Result struct {
	RecordFilePath string
	Intent         string
	Skipped        bool  // no commits found or no LLM configured
	Err            error // non-fatal; caller logs and continues
}

// Enrich collects git commit history for each record's affected_scope files, synthesizes
// an intent summary via LLM, and updates each record's intent field in place.
// Per-record errors are captured in Result.Err rather than returned; the error return
// is reserved for structural failures that prevent any enrichment.
func Enrich(in Input) ([]Result, error) {
	return EnrichWithBaseURL(in, in.LLMBaseURL)
}

// EnrichWithBaseURL is like Enrich but uses baseURL as the LLM API endpoint instead of
// the provider default. Pass an empty string to use the real endpoint. Exposed for tests.
func EnrichWithBaseURL(in Input, baseURL string) ([]Result, error) {
	provider, apiKey := resolveProvider(in.Provider, in.APIKey)
	progress := in.Progress
	if progress == nil {
		progress = func(string) {}
	}

	fileLoader := provenance.NewLoader("", nil)
	results := make([]Result, 0, len(in.RecordFiles))

	for _, recordFile := range in.RecordFiles {
		res := Result{RecordFilePath: recordFile}
		id := filepath.Base(strings.TrimSuffix(recordFile, ".yml"))

		record, err := fileLoader.LoadFile(recordFile)
		if err != nil {
			res.Err = fmt.Errorf("load record: %w", err)
			progress(fmt.Sprintf("  ✗ %s: %v", id, res.Err))
			results = append(results, res)
			continue
		}

		progress(fmt.Sprintf("  → %s: collecting git history (%d files)...", id, len(record.AffectedScope)))
		messages, err := collectCommitMessages(in.RepoDir, record.AffectedScope)
		if err != nil {
			res.Err = fmt.Errorf("git log: %w", err)
			progress(fmt.Sprintf("  ✗ %s: %v", id, res.Err))
			results = append(results, res)
			continue
		}

		if len(messages) == 0 {
			res.Skipped = true
			progress(fmt.Sprintf("  - %s: skipped (no git history found)", id))
			results = append(results, res)
			continue
		}

		if provider == "" || apiKey == "" {
			res.Skipped = true
			progress(fmt.Sprintf("  - %s: skipped (no LLM API key configured)", id))
			results = append(results, res)
			continue
		}

		progress(fmt.Sprintf("  → %s: calling %s (%d commit messages)...", id, provider, len(messages)))
		intent, err := synthesizeIntent(provider, in.Model, apiKey, baseURL, messages, record.AffectedScope)
		if err != nil {
			res.Err = fmt.Errorf("LLM synthesis: %v", err)
			progress(fmt.Sprintf("  ✗ %s: %v", id, res.Err))
			results = append(results, res)
			continue
		}

		if err := updateRecordIntent(recordFile, intent); err != nil {
			res.Err = fmt.Errorf("save record: %w", err)
			progress(fmt.Sprintf("  ✗ %s: %v", id, res.Err))
			results = append(results, res)
			continue
		}

		res.Intent = intent
		// Truncate long intents in the progress line for readability.
		display := intent
		if len(display) > 80 {
			display = display[:77] + "..."
		}
		progress(fmt.Sprintf("  ✓ %s: %q", id, display))
		results = append(results, res)
	}

	return results, nil
}

// collectCommitMessages runs git log --follow for each file and returns deduplicated
// commit subject lines. Files with no history are silently skipped.
func collectCommitMessages(repoDir string, files []string) ([]string, error) {
	seen := make(map[string]bool)
	var messages []string

	for _, f := range files {
		if f == "" {
			continue
		}
		// Remap paths that are relative to the process CWD (e.g., "../other-repo/foo.rb")
		// to paths relative to repoDir ("foo.rb"). Plain repo-relative paths like
		// "app/models/user.rb" pass through unchanged.
		gitPath := f
		if repoDir != "" && (filepath.IsAbs(f) || strings.HasPrefix(f, ".."+string(filepath.Separator))) {
			if abs, err := filepath.Abs(f); err == nil {
				if rel, err := filepath.Rel(repoDir, abs); err == nil {
					gitPath = rel
				}
			}
		}
		cmd := exec.Command("git", "log", "--follow", "--format=%s", "--", gitPath)
		if repoDir != "" {
			cmd.Dir = repoDir
		}
		out, err := cmd.Output()
		if err != nil {
			// Git exits non-zero for untracked/missing files; treat as no history.
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			messages = append(messages, line)
		}
	}

	return messages, nil
}

// resolveProvider returns (provider, apiKey), falling back to environment variables
// when the caller leaves them empty.
func resolveProvider(provider, apiKey string) (string, string) {
	if provider == "" {
		if k := os.Getenv("OPENAI_API_KEY"); k != "" {
			provider = "openai"
			if apiKey == "" {
				apiKey = k
			}
		} else if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
			provider = "anthropic"
			if apiKey == "" {
				apiKey = k
			}
		}
	}
	if apiKey == "" {
		switch provider {
		case "openai":
			apiKey = os.Getenv("OPENAI_API_KEY")
		case "anthropic":
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	return provider, apiKey
}

// synthesizeIntent calls the configured LLM with a focused prompt and returns
// a 1-2 sentence intent summary.
func synthesizeIntent(provider, model, apiKey, baseURL string, messages, files []string) (string, error) {
	prompt := buildPrompt(messages, files)
	switch provider {
	case "openai":
		return callOpenAI(apiKey, model, baseURL, prompt)
	case "anthropic":
		return callAnthropic(apiKey, model, baseURL, prompt)
	default:
		return "", fmt.Errorf("unsupported LLM provider: %q", provider)
	}
}

// buildPrompt constructs the focused intent-synthesis prompt.
func buildPrompt(messages, files []string) string {
	var b strings.Builder
	b.WriteString("Given these commit messages for files [")
	b.WriteString(strings.Join(files, ", "))
	b.WriteString("]:\n\n")
	for _, m := range messages {
		b.WriteString("- ")
		b.WriteString(m)
		b.WriteString("\n")
	}
	b.WriteString("\nWrite a 1-2 sentence intent summarizing what this code does and why it exists. ")
	b.WriteString("Return only the intent text, no preamble or explanation.")
	return b.String()
}

// --- OpenAI ---

type openAIChatRequest struct {
	Model     string         `json:"model"`
	Messages  []openAIChatMsg `json:"messages"`
	MaxTokens int            `json:"max_tokens,omitempty"`
}

type openAIChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIChatMsg `json:"message"`
	} `json:"choices"`
}

func callOpenAI(apiKey, model, baseURL, prompt string) (string, error) {
	if model == "" {
		model = "gpt-4o-mini"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	reqBody := openAIChatRequest{
		Model:     model,
		Messages:  []openAIChatMsg{{Role: "user", Content: prompt}},
		MaxTokens: 256,
	}
	return doLLMCall(endpoint, apiKey, nil, reqBody, parseOpenAIResponse)
}

func parseOpenAIResponse(body []byte) (string, error) {
	var resp openAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse OpenAI response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI returned no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// --- Anthropic ---

type anthropicRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func callAnthropic(apiKey, model, baseURL, prompt string) (string, error) {
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/messages"
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 256,
		Messages:  []anthropicMsg{{Role: "user", Content: prompt}},
	}
	return doLLMCall(endpoint, apiKey, map[string]string{"anthropic-version": "2023-06-01"}, reqBody, parseAnthropicResponse)
}

func parseAnthropicResponse(body []byte) (string, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse Anthropic response: %w", err)
	}
	if len(resp.Content) == 0 {
		return "", fmt.Errorf("Anthropic returned no content")
	}
	return strings.TrimSpace(resp.Content[0].Text), nil
}

// --- shared HTTP helper ---

func doLLMCall(url, apiKey string, extraHeaders map[string]string, reqBody any, parse func([]byte) (string, error)) (string, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM API error (HTTP %d): %s", resp.StatusCode, body)
	}

	return parse(body)
}

// updateRecordIntent reads a draft provenance record YAML, sets the intent field,
// and writes it back. Uses full yaml re-marshal so the intent block scalar is
// correctly formatted regardless of the file's existing intent value.
func updateRecordIntent(filePath, intent string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read record: %w", err)
	}

	var record provenance.Record
	if err := yaml.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("unmarshal record: %w", err)
	}
	record.FilePath = filePath
	record.Intent = intent

	out, err := yaml.Marshal(&record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	return os.WriteFile(filePath, out, 0644)
}
