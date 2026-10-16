package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/openresponses"
)

// The renderings below are ports of Observation.to_llm_content, the
// text OpenHands sends for a tool result, per observation class:
// openhands-sdk/openhands/sdk/tool/schema.py (the default),
// openhands-tools/openhands/tools/terminal/definition.py,
// task/definition.py, browser_use/definition.py and
// tom_consult/definition.py, read at SDK 1.0.0, 1.28.1 and 1.55.0, and
// the send-time cut in llm/message.py.

// Limits and texts of the SDK's truncation (utils/truncate.py,
// terminal/constants.py).
const (
	textContentLimit = 50_000
	maxCmdOutput     = 30_000
	truncateNotice   = "<response clipped><NOTE>Due to the max output limit, only part of the full response has been shown to you.</NOTE>"
	truncatePersist  = "<response clipped><NOTE>Due to the max output limit, only part of the full response has been shown to you. The complete output has been saved to %s - you can use other tools to view the full content (truncated part starts around line %d).</NOTE>"
	errorHeader      = "[An error occurred during execution.]\n"
)

// Observation kinds with a rendering of their own.
const (
	obsTerminal    = "TerminalObservation"
	obsExecuteBash = "ExecuteBashObservation"
	obsTask        = "TaskObservation"
	obsBrowser     = "BrowserObservation"
	obsTom         = "ConsultTomObservation"
	obsSleeptime   = "SleeptimeComputeObservation"
)

// defaultRendered are the observation kinds of SDK 1.0.0 to 1.55.0
// that send the default rendering: the error header when is_error, then
// the content.
var defaultRendered = map[string]bool{
	"ApplyPatchObservation": true, "AskOracleObservation": true,
	"ClassifyAndSwitchLLMObservation": true, "ClientToolObservation": true,
	"DelegateObservation": true, "EditObservation": true,
	"FileEditorObservation": true, "FinishObservation": true,
	"GlobObservation": true, "GrepObservation": true,
	"InvokeSkillObservation": true, "ListDirectoryObservation": true,
	"MCPToolObservation": true, "PlanningFileEditorObservation": true,
	"ReadFileObservation": true, "SwitchLLMObservation": true,
	"TaskTrackerObservation": true, "ThinkObservation": true,
	"VisionInspectObservation": true, "WorkflowObservation": true,
	"WriteFileObservation": true,
}

// outPart is one part of a tool result as OpenHands sends it: text or
// an image URL.
type outPart struct {
	text   string
	image  string
	isText bool
}

// contentParts are an observation's content items as parts.
func contentParts(cs openhands.Contents) []outPart {
	if cs.IsText {
		return []outPart{{text: cs.Text, isText: true}}
	}
	var out []outPart
	for _, p := range cs.Items {
		if p.Err != nil {
			continue
		}
		switch p.Type {
		case openhands.ContentText:
			out = append(out, outPart{text: p.Text, isText: true})
		case openhands.ContentImage:
			for _, u := range p.ImageURLs {
				out = append(out, outPart{image: u})
			}
		}
	}
	return out
}

// textOfObs is Observation.text: the content's text parts joined.
func textOfObs(o *openhands.Observation) string {
	var b strings.Builder
	for _, p := range contentParts(o.Content) {
		if p.isText {
			b.WriteString(p.text)
		}
	}
	return b.String()
}

// render is o.to_llm_content for its kind. A kind with no port is
// rendered as the default and reported.
func (c *conv) render(i int, o *openhands.Observation) []outPart {
	var out []outPart
	header := func() {
		if o.IsError {
			out = append(out, outPart{text: errorHeader, isText: true})
		}
	}
	switch o.Kind {
	case obsTerminal, obsExecuteBash:
		header()
		m := o.Terminal()
		ret := m.Prefix + textOfObs(o) + m.Suffix
		if m.WorkingDir != "" {
			ret += "\n[Current working directory: " + m.WorkingDir + "]"
		}
		if m.PyInterpreterPath != "" {
			ret += "\n[Python interpreter: " + m.PyInterpreterPath + "]"
		}
		if m.ExitCode != -1 {
			ret += fmt.Sprintf("\n[Command finished with exit code %d]", m.ExitCode)
		}
		out = append(out, outPart{text: c.truncate(i, ret, maxCmdOutput, o.FullOutputSaveDir, "terminal"), isText: true})
	case obsTask:
		out = append(out, outPart{text: "Task ID: " + o.TaskID + "\nSubagent: " + o.Subagent + "\nStatus: " + o.Status, isText: true})
		header()
		out = append(out, contentParts(o.Content)...)
	case obsBrowser:
		header()
		if t := textOfObs(o); t != "" {
			out = append(out, outPart{text: c.truncate(i, t, textContentLimit, o.FullOutputSaveDir, "browser"), isText: true})
		}
		if o.ScreenshotData != "" {
			mime := imageMime(o.ScreenshotData)
			if o.FullOutputSaveDir != "" {
				ext := strings.TrimPrefix(mime, "image/")
				if ext == "jpeg" {
					ext = "jpg"
				}
				path := pyJoin(o.FullOutputSaveDir, "browser_screenshot_"+shortHash(o.ScreenshotData)+"."+ext)
				out = append(out, outPart{text: "Screenshot saved to: " + path, isText: true})
				c.at(agentimport.Lossy, "openhands/output-path", i, "the output names the file OpenHands saved the screenshot to, rebuilt from its directory and the image's hash, as it would be had the save succeeded")
			}
			out = append(out, outPart{image: "data:" + mime + ";base64," + o.ScreenshotData})
		}
	case obsTom:
		if o.Suggestions == "" {
			return []outPart{{text: "Tom agent did not provide suggestions.", isText: true}}
		}
		parts := []string{"Tom agent's guidance:\n" + o.Suggestions}
		if o.Reasoning != "" {
			parts = append(parts, "\nReasoning: "+o.Reasoning)
		}
		if f, ok := openhands.Float(o.Confidence); ok {
			parts = append(parts, fmt.Sprintf("\nConfidence: %.0f%%", f*100))
		}
		return []outPart{{text: strings.Join(parts, "\n"), isText: true}}
	case obsSleeptime:
		n, _, _ := openhands.Int(o.SessionsProcessed)
		if n > 0 {
			return []outPart{{text: fmt.Sprintf("Successfully indexed %d conversation(s) for user modeling.\n%s", n, o.Message), isText: true}}
		}
		return []outPart{{text: "Sleeptime compute completed.\n" + o.Message, isText: true}}
	default:
		if !defaultRendered[o.Kind] {
			c.at(agentimport.Lossy, "openhands/observation-render", i, "OpenHands renders a %s with code of its own, which this converter does not port; the output is the default rendering of its content", o.Kind)
		}
		header()
		out = append(out, contentParts(o.Content)...)
	}
	return out
}

// imageMime is the SDK's detect_image_mime_type: by the base64 data's
// first characters, PNG when none matches.
func imageMime(b64 string) string {
	switch {
	case strings.HasPrefix(b64, "/9j/"):
		return "image/jpeg"
	case strings.HasPrefix(b64, "iVBORw0KGgo"):
		return "image/png"
	case strings.HasPrefix(b64, "R0lGODlh"):
		return "image/gif"
	case strings.HasPrefix(b64, "UklGR"):
		return "image/webp"
	}
	return "image/png"
}

// truncate is maybe_truncate with a save directory: the middle of a
// text over limit characters replaced by the notice, which names the
// file the tool saved the whole text to when it had a directory to
// save it in.
func (c *conv) truncate(i int, s string, limit int, saveDir, prefix string) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	notice := []rune(truncateNotice)
	if len(notice) >= limit {
		return string(notice[:limit])
	}
	available := limit - len(notice)
	head := available/2 + available%2
	final := notice
	if saveDir != "" {
		path := pyJoin(saveDir, prefix+"_output_"+shortHash(s)+".txt")
		final = []rune(fmt.Sprintf(truncatePersist, path, splitLines(string(r[:head]))+1))
		c.at(agentimport.Lossy, "openhands/output-path", i, "the output names the file OpenHands saved the whole text to, rebuilt from its directory and the text's hash, as it would be had the save succeeded")
	}
	if len(final) >= limit {
		return string(final[:limit])
	}
	remaining := limit - len(final)
	head = min(head, remaining)
	tail := remaining - head
	c.at(agentimport.Lossy, "openhands/output-truncation", i, "the output is cut to %d characters, as the tool cut it before sending; the observation member holds it whole", limit)
	out := string(r[:head]) + string(final)
	if tail > 0 {
		out += string(r[len(r)-tail:])
	}
	return out
}

// sendCut is the cut OpenHands makes to a tool result's text at send
// time (Message._maybe_truncate_tool_text): the default notice, no
// file.
func sendCut(s string) (string, bool) {
	r := []rune(s)
	if len(r) <= textContentLimit {
		return s, false
	}
	notice := []rune(truncateNotice)
	available := textContentLimit - len(notice)
	head := available/2 + available%2
	tail := textContentLimit - len(notice) - head
	return string(r[:head]) + truncateNotice + string(r[len(r)-tail:]), true
}

// shortHash is the first eight hex digits of the SHA-256 of s, as the
// SDK names the files it saves.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// pyJoin is str(Path(dir) / name): repeated slashes and "."
// components dropped, a trailing slash removed.
func pyJoin(dir, name string) string {
	lead := ""
	switch {
	case strings.HasPrefix(dir, "//") && !strings.HasPrefix(dir, "///"):
		lead = "//"
	case strings.HasPrefix(dir, "/"):
		lead = "/"
	}
	var parts []string
	for _, p := range strings.Split(dir, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	parts = append(parts, name)
	return lead + strings.Join(parts, "/")
}

// splitLines is len(s.splitlines()) in Python: the number of lines s
// holds, split at every line boundary Python knows, a final boundary
// ending the last line rather than starting another.
func splitLines(s string) int {
	n := 0
	r := []rune(s)
	start := 0
	for k := 0; k < len(r); k++ {
		switch r[k] {
		case '\r':
			if k+1 < len(r) && r[k+1] == '\n' {
				k++
			}
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
		default:
			continue
		}
		n++
		start = k + 1
	}
	if start < len(r) {
		n++
	}
	return n
}

// outputData builds a function_call_output's output from the parts
// OpenHands sends: one text as a string, anything else as parts, each
// text cut as OpenHands cuts tool text at send time.
func (c *conv) outputData(i int, parts []outPart) openresponses.FunctionCallOutputData {
	cut := false
	for k := range parts {
		if parts[k].isText {
			if t, ok := sendCut(parts[k].text); ok {
				parts[k].text, cut = t, true
			}
		}
	}
	if cut {
		c.at(agentimport.Lossy, "openhands/output-truncation", i, "a text of the output is cut to %d characters, as OpenHands cuts tool text when it sends it", textContentLimit)
	}
	if len(parts) == 1 && parts[0].isText {
		return openresponses.FunctionCallOutputData{Text: parts[0].text}
	}
	if len(parts) == 0 {
		return openresponses.FunctionCallOutputData{Text: ""}
	}
	out := openresponses.Contents{}
	for _, p := range parts {
		if p.isText {
			out = append(out, &openresponses.InputText{Text: p.text})
		} else {
			out = append(out, &openresponses.InputImage{ImageURL: p.image})
		}
	}
	return openresponses.FunctionCallOutputData{Parts: out}
}
