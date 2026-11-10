package acp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// requested answers the agent's requests: the client serves only
// permissions; files and terminals the agent does on its own.
func (s *session) requested(method string, params json.RawMessage) (any, *rpcError) {
	if method != "session/request_permission" {
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
	}
	return s.permission(params)
}

type permissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  toolCall           `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type toolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Locations  []struct {
		Path string `json:"path"`
	} `json:"locations"`
	RawInput json.RawMessage `json:"rawInput"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}

// permission answers session/request_permission by the policy of the
// session's role kind, and records every denial on the running turn. It
// allows only once, so every later call is asked about again.
func (s *session) permission(params json.RawMessage) (any, *rpcError) {
	var req permissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		s.denied(toolCall{}, "unreadable permission request: "+err.Error())
		return nil, &rpcError{Code: codeInvalidParams, Message: "unreadable permission request"}
	}
	reason := "the request is for another session"
	if req.SessionID == s.id {
		reason = judge(s.spec.Kind, s.dir, req.ToolCall)
	}
	option := ""
	if reason == "" {
		if option = choose(req.Options, "allow_once", "allow_always"); option == "" {
			reason = "the agent offers no option to allow it"
		}
	}
	if reason != "" {
		s.denied(req.ToolCall, reason)
		option = choose(req.Options, "reject_once", "reject_always")
	}
	if option == "" {
		return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
	}
	return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option}}, nil
}

// choose is the first option of the first of kinds the agent offers.
func choose(options []permissionOption, kinds ...string) string {
	for _, k := range kinds {
		for _, o := range options {
			if o.Kind == k && o.OptionID != "" {
				return o.OptionID
			}
		}
	}
	return ""
}

// denied records that call was denied for reason as progress of the running
// turn; agents ask for permissions only while they work on a prompt.
func (s *session) denied(call toolCall, reason string) {
	s.mu.Lock()
	t := s.turn
	s.mu.Unlock()
	if t == nil {
		return
	}
	tool := call.Kind
	switch {
	case tool == "":
		tool = call.Title
	case call.Title != "":
		tool += ": " + call.Title
	}
	s.emit(t, domain.AgentProgressed{Kind: domain.ProgressPermissionDenied, ToolCall: call.ToolCallID, Text: tool, Reason: reason})
}

// pathKeys are the fields of a tool's raw input that name the file it works
// on, as the agents' own tools call them.
var pathKeys = []string{"path", "file_path", "filePath", "notebook_path", "abs_path"}

// judge is why a session of a role of kind, in the worktree dir, may not
// make call, or "" when it may. Every role reads and searches; only a build
// role edits files and runs commands. No role reaches a path outside dir,
// and a call that cannot be read is denied.
func judge(kind domain.Kind, dir string, call toolCall) string {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "the worktree cannot be resolved: " + err.Error()
	}
	var input map[string]json.RawMessage
	if len(call.RawInput) > 0 && json.Unmarshal(call.RawInput, &input) != nil {
		input = nil
	}
	var paths []string
	for _, l := range call.Locations {
		paths = append(paths, l.Path)
	}
	for _, k := range pathKeys {
		if p, ok := text(input[k]); ok {
			paths = append(paths, p)
		}
	}
	cwd := root
	if p, ok := text(input["cwd"]); ok {
		paths = append(paths, p)
		cwd = p
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(root, cwd)
		}
	}
	for _, p := range paths {
		if !within(root, root, p) {
			return "a path outside the worktree: " + p
		}
	}
	switch call.Kind {
	case "read", "search", "think":
		return ""
	case "edit", "delete", "move":
		if kind != domain.KindBuild {
			return fmt.Sprintf("a role of kind %q may not edit files", kind)
		}
		if len(paths) == 0 {
			return "an edit without paths"
		}
		return ""
	case "execute":
		if kind != domain.KindBuild {
			return fmt.Sprintf("a role of kind %q may not run commands", kind)
		}
		return judgeCommand(root, cwd, input["command"])
	}
	return fmt.Sprintf("an unknown tool kind %q", call.Kind)
}

// text is raw as a string, when it is a non-empty one.
func text(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || s == "" {
		return "", false
	}
	return s, true
}

// judgeCommand is why the command raw, a command line or its words, run in
// cwd may not run, or "". It sees what the words say, not what a shell
// would expand: a command inside `bash -c` or a path in a variable is the
// gate's to catch.
func judgeCommand(root, cwd string, raw json.RawMessage) string {
	var cmds [][]string
	var words []string
	if line, ok := text(raw); ok {
		var err error
		if cmds, err = splitCommands(line); err != nil {
			return "an unreadable command: " + err.Error()
		}
	} else if json.Unmarshal(raw, &words) == nil && len(words) > 0 {
		cmds = [][]string{words}
	}
	if len(cmds) == 0 {
		return "a command without a command line"
	}
	for _, words := range cmds {
		if reason := judgeWords(root, cwd, words); reason != "" {
			return reason
		}
	}
	return ""
}

// wrappers run the command that follows them.
var wrappers = []string{"env", "command", "exec", "time", "nohup", "nice", "xargs"}

// deniedCommands are the commands no builder runs: they reach outside the worktree
// (the network, GitHub, other users).
var deniedCommands = []string{"sudo", "su", "doas", "curl", "wget", "gh"}

// installers are the package managers and the subcommands of theirs that
// install packages.
var installers = map[string][]string{
	"npm":     {"install", "i", "add", "ci"},
	"pnpm":    {"install", "i", "add"},
	"yarn":    {"install", "add"},
	"bun":     {"install", "i", "add"},
	"pip":     {"install"},
	"pip3":    {"install"},
	"pipx":    {"install"},
	"uv":      {"add", "pip"},
	"go":      {"install", "get"},
	"cargo":   {"install", "add"},
	"gem":     {"install"},
	"brew":    {"install"},
	"apt":     {"install"},
	"apt-get": {"install"},
	"dnf":     {"install"},
	"yum":     {"install"},
	"apk":     {"add"},
}

// judgeWords is why the simple command words, run in cwd, may not run, or "".
func judgeWords(root, cwd string, words []string) string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case isAssignment(w), slices.Contains(wrappers, filepath.Base(w)), strings.HasPrefix(w, "-") && w != "-":
			words = words[1:]
			continue
		}
		break
	}
	if len(words) == 0 {
		return ""
	}
	name, args := filepath.Base(words[0]), words[1:]
	if slices.Contains(deniedCommands, name) {
		return name + " is denied"
	}
	if name == "git" && slices.Contains(args, "push") {
		return "git push is denied"
	}
	if subs, ok := installers[name]; ok {
		if i := slices.IndexFunc(args, func(a string) bool { return !strings.HasPrefix(a, "-") }); i >= 0 && slices.Contains(subs, args[i]) {
			return "installing packages is denied: " + name + " " + args[i]
		}
	}
	for _, a := range args {
		if p, ok := pathArg(a); ok && !within(root, cwd, p) {
			return "a path outside the worktree: " + a
		}
	}
	return ""
}

// isAssignment tells whether w sets a variable for the command after it.
func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// pathArg is the path an argument names, if it looks like one: a flag's
// value after "=", or a word with a slash, a leading dot or a leading tilde.
func pathArg(a string) (string, bool) {
	if strings.HasPrefix(a, "-") {
		_, v, ok := strings.Cut(a, "=")
		if !ok {
			return "", false
		}
		a = v
	}
	if a == "" {
		return "", false
	}
	return a, strings.ContainsRune(a, '/') || strings.HasPrefix(a, ".") || strings.HasPrefix(a, "~")
}

// within tells whether p, relative to base when it is not absolute, is dir
// root or under it once its links are followed. A path from the home ("~")
// is not; /dev/null, where commands throw output away, is.
func within(root, base, p string) bool {
	switch {
	case p == "/dev/null":
		return true
	case p == "" || strings.HasPrefix(p, "~"):
		return false
	case !filepath.IsAbs(p):
		p = base + string(filepath.Separator) + p
	}
	resolved, err := resolve(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolve is the absolute path p names once its links are followed. A part
// that does not exist yet is joined as written: a file about to be created
// is no link. p is not cleaned first, so ".." after a link leaves where the
// link points, as the system does.
func resolve(p string) (string, error) {
	var rest []string
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		i := strings.LastIndexByte(cur, filepath.Separator)
		if i < 0 {
			return "", err
		}
		rest = append([]string{cur[i+1:]}, rest...)
		cur = cur[:i]
		if cur == "" {
			cur = string(filepath.Separator)
		}
	}
}

// splitCommands is line's simple commands, each as its words, read as a
// shell reads quotes and escapes. Operators, pipes, subshells and command
// substitutions end a command; redirections end a word, so their target is
// a word of its own.
func splitCommands(line string) ([][]string, error) {
	var cmds [][]string
	var words []string
	var w strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, w.String())
			w.Reset()
			inWord = false
		}
	}
	endCmd := func() {
		endWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\'':
			j := slices.Index(rs[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unclosed single quote")
			}
			w.WriteString(string(rs[i+1 : i+1+j]))
			inWord = true
			i += j + 1
		case r == '"':
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
				}
				w.WriteRune(rs[j])
			}
			if j >= len(rs) {
				return nil, errors.New("unclosed double quote")
			}
			inWord = true
			i = j
		case r == '\\':
			if i+1 < len(rs) {
				i++
				w.WriteRune(rs[i])
				inWord = true
			}
		case r == ' ' || r == '\t':
			endWord()
		case strings.ContainsRune(";&|\n()`", r):
			endCmd()
		case r == '<' || r == '>':
			endWord()
			if i+1 < len(rs) && rs[i+1] == '&' {
				i++
			}
		case r == '#' && !inWord:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			endCmd()
		default:
			w.WriteRune(r)
			inWord = true
		}
	}
	endCmd()
	return cmds, nil
}
