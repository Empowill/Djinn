package plan

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/tilasm"
)

// ArtifactModel makes the calls of a publish: read the live version, read the page, publish it; nothing to write.
const ArtifactModel = "claude-haiku-5-5"

// artifactTool is Claude Code's tool that publishes on claude.ai. claude -p offers it only with CLAUDE_CODE_ARTIFACT=1,
// which a --settings file's env may set (W488).
const artifactTool = "Artifact"

// artifactTools are the tools of an Artifact call: the Artifact tool, and Read, as the Artifact tool publishes no file
// its session has not read (W493: Haiku refused to, the Artifact tool alone).
const artifactTools = artifactTool + ",Read"

// artifactSettings are the settings of every Artifact call: the variable that offers the tool.
const artifactSettings = `{"env":{"CLAUDE_CODE_ARTIFACT":"1"}}`

// The limits of a publish: a call that makes two tool calls, maybe three when the first publish again is refused.
const (
	artifactTurns     = 20
	artifactBudgetUSD = 0.5
	// ArtifactTimeout is how long a publish may take, its wait for the paid gate left out.
	ArtifactTimeout = 5 * time.Minute
	// artifactCheckTimeout is how long the check waits for the call's first event.
	artifactCheckTimeout = 30 * time.Second
)

// Publisher publishes tilasms on claude.ai with Claude Code, one short claude -p call each, and says whether it can.
type Publisher struct {
	// Home is Djinn's data folder: the accounts a signed-in Claude is looked for in.
	Home string
	// UserHome and Path are the user's home folder and the PATH claude is found on; empty: this process's.
	UserHome, Path string
	// Gate takes the paid gate for what, for the task taskID when a worker publishes (empty otherwise), and gives the
	// release; nil: no gate.
	Gate func(ctx context.Context, taskID, what string) (release func(), err error)
	// Timeout is how long a publish may take; ArtifactTimeout by default.
	Timeout time.Duration

	mu      sync.Mutex
	checked *artifactCheck // The Artifact tool looked for, once per start; nil until then.
}

// artifactCheck is what the first call found of the Artifact tool.
type artifactCheck struct {
	block  planv1.PublishBlock
	detail string
}

// Published is what a publish did, read from the Artifact tool's result.
type Published struct {
	URL     string
	Version int32 // The artifact's own version on claude.ai.
	CostUSD float64
}

func (p *Publisher) paths() (userHome, path string) {
	userHome, path = p.UserHome, p.Path
	if userHome == "" {
		userHome, _ = os.UserHomeDir()
	}
	if path == "" {
		path = os.Getenv("PATH")
	}
	return userHome, path
}

// Check says whether tilasms can be published: claude installed, signed in, and offering the Artifact tool. The
// tool is looked for once per start, by a call cut at its first event, before any model call: it costs nothing.
// Installed and signed in are checked each time.
func (p *Publisher) Check(ctx context.Context) (block planv1.PublishBlock, detail string, env []string) {
	userHome, path := p.paths()
	command, err := lookProgram(providerPrograms[planv1.Provider_PROVIDER_CLAUDE], path)
	if err != nil {
		return planv1.PublishBlock_PUBLISH_BLOCK_NO_CLAUDE, "", nil
	}
	env, ok := signedInEnv(ctx, planv1.Provider_PROVIDER_CLAUDE, p.Home, userHome, path)
	if !ok {
		return planv1.PublishBlock_PUBLISH_BLOCK_SIGNED_OUT, "", nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.checked == nil {
		tools, err := p.initTools(ctx, command, env)
		switch {
		case err != nil:
			p.checked = &artifactCheck{block: planv1.PublishBlock_PUBLISH_BLOCK_CHECK_FAILED, detail: err.Error()}
		case !slices.Contains(tools, artifactTool):
			p.checked = &artifactCheck{block: planv1.PublishBlock_PUBLISH_BLOCK_NO_ARTIFACT_TOOL}
		default:
			p.checked = &artifactCheck{}
		}
		logx.L().Info("tilasm publish checked", "block", p.checked.block.String(), "detail", p.checked.detail)
	}
	return p.checked.block, p.checked.detail, env
}

// initTools are the tools claude offers an Artifact call, from the init event its stream opens with; the call is cut
// there, before it reaches the model.
func (p *Publisher) initTools(ctx context.Context, command string, env []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, artifactCheckTimeout)
	defer cancel()
	cmd := p.command(ctx, command, env, "", artifactArgs("Answer OK.", 1))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 64<<10), 16<<20)
	for lines.Scan() {
		var ev artifactEvent
		if json.Unmarshal(lines.Bytes(), &ev) == nil && ev.Type == "system" && ev.Subtype == "init" {
			cancel() // Cut before the call reaches the model.
			_ = cmd.Wait()
			return ev.Tools, nil
		}
	}
	_ = cmd.Wait() // Its stderr is whole once it ended.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("claude gave no init event in %s", artifactCheckTimeout)
	}
	return nil, fmt.Errorf("claude ended before its init event: %s", tail(stderr.String()))
}

func (p *Publisher) command(ctx context.Context, command string, env []string, dir string, args []string) *exec.Cmd {
	userHome, path := p.paths()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path, "NO_COLOR=1")
	if abs, err := filepath.Abs(dir); dir != "" && err == nil && runtime.GOOS != "windows" {
		// exec sets PWD only when it builds the environment itself: Djinn's own would name Djinn's folder, and a
		// shell that finds it wrong takes the folder with its links resolved (/private/var for /var on macOS).
		cmd.Env = append(cmd.Env, "PWD="+abs)
	}
	if userHome != "" {
		cmd.Env = append(cmd.Env, "HOME="+userHome, "USERPROFILE="+userHome)
	}
	cmd.Env = append(cmd.Env, env...)
	cmd.WaitDelay = time.Second
	return cmd
}

// artifactArgs are the arguments of an Artifact call: the Artifact tool alone, its edits accepted, no prompt that a
// person would answer, no MCP server, no session kept.
func artifactArgs(prompt string, turns int) []string {
	return []string{
		"-p", prompt, "--model", ArtifactModel, "--output-format", "stream-json", "--verbose",
		"--tools", artifactTools, "--allowedTools", artifactTools,
		"--permission-mode", "acceptEdits", "--permission-prompts", "none",
		"--strict-mcp-config", "--no-session-persistence", "--settings", artifactSettings,
		"--max-turns", strconv.Itoa(turns), "--max-budget-usd", strconv.FormatFloat(artifactBudgetUSD, 'f', -1, 64),
	}
}

// artifactPrompt is the call's whole prompt: the tool calls to make, with their arguments, and nothing else to do.
func artifactPrompt(b *tilasm.Bundle, url string) string {
	call := map[string]any{"action": "publish", "file_path": filepath.Join(b.Dir, tilasm.Index)}
	if url != "" {
		call["url"] = url
	} else {
		call["icon"] = "document"
	}
	if len(b.Files) > 0 {
		files := make(map[string]string, len(b.Files))
		for _, f := range b.Files {
			files[f] = f
		}
		call["files"] = files
	}
	args, _ := json.Marshal(call) // Strings and a map of strings: it cannot fail.
	var s strings.Builder
	s.WriteString("Publish a page on claude.ai with the Artifact tool, and do nothing else.\n\n")
	s.WriteString("Djinn built this page from a tilasm its owner checked and asked to publish: send it as it is, " +
		"never changed. Read it first, as the Artifact tool asks, without commenting on it.\n\n")
	n := 1
	if url != "" {
		fmt.Fprintf(&s, "%d. Call Artifact with {\"action\": \"read\", \"url\": %q}, to view its live version. Its saved "+
			"file needs no reading.\n", n, url)
		n++
	}
	fmt.Fprintf(&s, "%d. Read %s.\n", n, filepath.Join(b.Dir, tilasm.Index))
	if len(b.Files) > 0 {
		s.WriteString("   The files beside it are its images, fonts and attachments, as its owner made them: they need no reading.\n")
	}
	fmt.Fprintf(&s, "%d. Call Artifact with %s.\n", n+1, args)
	if url != "" {
		s.WriteString("If that publish is refused because the live version was not read, call it again with the same arguments.\n")
	}
	s.WriteString("\nThen answer with the first line of the publish result, word for word.")
	return s.String()
}

// artifactEvent is a line of claude's stream-json, the parts an Artifact call reads.
type artifactEvent struct {
	Type    string   `json:"type"`
	Subtype string   `json:"subtype"`
	Tools   []string `json:"tools"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	IsError bool    `json:"is_error"`
	Result  string  `json:"result"`
	Cost    float64 `json:"total_cost_usd"`
}

// artifactPublished is the line of the Artifact tool's result that a publish succeeded with: Published <file> at
// https://claude.ai/artifact/<id> (Version N, version id …).
var artifactPublished = regexp.MustCompile(`Published .+? at (https://claude\.ai/artifact/[A-Za-z0-9_-]+) \(Version (\d+)`)

// readArtifactStream reads an Artifact call's stream: the address and version of the last publish its tool results
// tell, what the call cost, and why when no publish succeeded: the Artifact tool's answer to the last publish, else
// the model's last words.
func readArtifactStream(r *bufio.Scanner) (pub Published, why string, failed bool) {
	publishes := map[string]bool{} // The tool calls that publish, by id.
	var refused, said string
	for r.Scan() {
		var ev artifactEvent
		if json.Unmarshal(r.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, id := range publishCalls(ev.Message.Content) {
				publishes[id] = true
			}
		case "user":
			for _, res := range toolResults(ev.Message.Content) {
				if m := artifactPublished.FindStringSubmatch(res.text); m != nil {
					n, _ := strconv.ParseInt(m[2], 10, 32)
					pub.URL, pub.Version = m[1], int32(n)
				} else if publishes[res.id] {
					refused = res.text
				}
			}
		case "result":
			pub.CostUSD, said = ev.Cost, ev.Result
			failed = ev.IsError || ev.Subtype != "success"
		}
	}
	return pub, cmp.Or(refused, said), failed
}

// publishCalls are the ids of the Artifact calls that publish in a message's content.
func publishCalls(content json.RawMessage) []string {
	var blocks []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input struct {
			Action string `json:"action"`
		} `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var ids []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == artifactTool && b.Input.Action == "publish" {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

// toolResult is a tool's answer to the call id.
type toolResult struct{ id, text string }

// toolResults are the tool results of a message's content.
func toolResults(content json.RawMessage) []toolResult {
	var blocks []struct {
		Type    string          `json:"type"`
		ID      string          `json:"tool_use_id"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []toolResult
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		var text string
		if json.Unmarshal(b.Content, &text) != nil {
			var parts []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(b.Content, &parts)
			var s strings.Builder
			for _, p := range parts {
				s.WriteString(p.Text)
			}
			text = s.String()
		}
		out = append(out, toolResult{id: b.ID, text: text})
	}
	return out
}

// tail is the end of a command's output, for an error: its last 500 bytes, on a line.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = "…" + s[len(s)-500:]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

// short is the start of why a call failed, on a line: its first 500 bytes.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 500 {
		s = strings.ToValidUTF8(s[:500], "") + "…"
	}
	return s
}

// Publish publishes the bundle b on claude.ai, at url when it has one, under the paid gate. The address comes from the
// Artifact tool's result, never from the model's words: a call that tells none failed.
func (p *Publisher) Publish(ctx context.Context, taskID, code string, b *tilasm.Bundle, url string) (*Published, error) {
	block, detail, env := p.Check(ctx)
	if block != planv1.PublishBlock_PUBLISH_BLOCK_UNSPECIFIED {
		return nil, &PublishBlockedError{Block: block, Detail: detail}
	}
	_, path := p.paths()
	command, err := lookProgram(providerPrograms[planv1.Provider_PROVIDER_CLAUDE], path)
	if err != nil {
		return nil, err
	}
	if p.Gate != nil {
		release, err := p.Gate(ctx, taskID, "publish tilasm "+code+" on claude.ai")
		if err != nil {
			return nil, fmt.Errorf("take the paid gate: %w", err)
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(p.Timeout, ArtifactTimeout))
	defer cancel()
	start := time.Now()
	cmd := p.command(ctx, command, env, b.Dir, artifactArgs(artifactPrompt(b, url), artifactTurns))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 64<<10), 64<<20)
	pub, why, failed := readArtifactStream(lines)
	waitErr := cmd.Wait()
	logx.L().Info("tilasm published", "tilasm", code, "files", len(b.Files), "bytes", b.Size, "url", pub.URL,
		"artifact_version", pub.Version, "cost_usd", pub.CostUSD, "took", time.Since(start).Round(time.Millisecond),
		"failed", failed, "err", waitErr)
	if pub.URL == "" {
		why = cmp.Or(why, tail(stderr.String()))
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("claude did not publish %s in %s", code, cmp.Or(p.Timeout, ArtifactTimeout))
		case why == "" && waitErr != nil:
			return nil, fmt.Errorf("claude did not publish %s: %w", code, waitErr)
		}
		return nil, fmt.Errorf("claude did not publish %s: %s", code, short(why))
	}
	if url != "" && pub.URL != url {
		logx.L().Warn("tilasm published at a new address", "tilasm", code, "was", url, "now", pub.URL)
	}
	return &pub, nil
}

// PublishBlockedError is why a publish cannot start on this machine.
type PublishBlockedError struct {
	Block  planv1.PublishBlock
	Detail string
}

func (e *PublishBlockedError) Error() string {
	why := map[planv1.PublishBlock]string{
		planv1.PublishBlock_PUBLISH_BLOCK_NO_CLAUDE:        "Claude Code is not installed: no claude on the PATH",
		planv1.PublishBlock_PUBLISH_BLOCK_SIGNED_OUT:       "Claude Code is not signed in: run claude auth login",
		planv1.PublishBlock_PUBLISH_BLOCK_NO_ARTIFACT_TOOL: "Claude Code offers no Artifact tool on this account (its version, plan or organization)",
		planv1.PublishBlock_PUBLISH_BLOCK_CHECK_FAILED:     "the call that looks for Claude Code's Artifact tool failed",
	}[e.Block]
	if e.Detail != "" {
		why += ": " + e.Detail
	}
	return "cannot publish on claude.ai: " + why
}
