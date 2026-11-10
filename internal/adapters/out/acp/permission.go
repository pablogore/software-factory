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
	"github.com/pablogore/software-factory/internal/ports"
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
		reason = judge(s.spec, s.dir, req.ToolCall)
	}
	option := ""
	if reason == "" {
		if option = choose(req.Options, "allow_once"); option == "" {
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

// defaultTools are the tools of the job's MCP server a role that lists none
// may call, as the worker allows them.
var defaultTools = []string{"submit_result", "progress"}

// jobTool tells whether call is one of tools, the role's, or defaultTools
// when it lists none, on one of servers, the job's MCP servers, by the name
// claude-code-acp gives it: "mcp__<server>__<tool>". Only a call of kind
// other, the one claude-code-acp gives MCP tools, or of no kind is one: any
// other kind is judged by its kind, whatever its title.
func jobTool(servers []ports.MCPServer, tools []string, call toolCall) bool {
	if call.Kind != "" && call.Kind != "other" {
		return false
	}
	if len(tools) == 0 {
		tools = defaultTools
	}
	for _, m := range servers {
		if tool, ok := strings.CutPrefix(call.Title, "mcp__"+m.Name+"__"); ok && slices.Contains(tools, tool) {
			return true
		}
	}
	return false
}

// judge is why a session started with spec, in the worktree dir, may not
// make call, or "" when it may. Every role reads, searches and calls the
// job tools its role lists; only a build role edits files and runs
// commands. No role reaches a path outside dir, and a call that cannot be
// read is denied.
func judge(spec ports.SessionSpec, dir string, call toolCall) string {
	kind := spec.Kind
	if jobTool(spec.MCPServers, spec.Tools, call) {
		return ""
	}
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

// maxDirs is how many directories a command line may have changed to, as
// far as the policy follows it, before it is denied.
const maxDirs = 64

// judgeCommand is why the command raw, a command line or its words, run in
// cwd may not run, or "". It sees what the words say, not what a shell
// would expand: a command inside `bash -c` or a path in a variable is the
// gate's to catch. A relative path is judged against cwd and against every
// directory a cd before it may have moved to, since a cd in a subshell or a
// pipe does not last.
func judgeCommand(root, cwd string, raw json.RawMessage) string {
	var cmds []command
	var words []string
	if line, ok := text(raw); ok {
		var err error
		if cmds, err = splitCommands(line); err != nil {
			return "an unreadable command: " + err.Error()
		}
	} else if json.Unmarshal(raw, &words) == nil && len(words) > 0 {
		cmds = []command{{words: words}}
	}
	if len(cmds) == 0 {
		return "a command without a command line"
	}
	dirs := []string{cwd}
	for _, c := range cmds {
		reason, to := judgeWords(root, dirs, c)
		if reason != "" {
			return reason
		}
		if to == "" {
			continue
		}
		for _, d := range dirs {
			next := to
			if !filepath.IsAbs(next) {
				next = d + string(filepath.Separator) + to
			}
			if !slices.Contains(dirs, next) {
				dirs = append(dirs, next)
			}
		}
		if len(dirs) > maxDirs {
			return "a command that changes directory too many times"
		}
	}
	return ""
}

// wrapper is a command that runs the command after its options: short
// holds its short options that take the next word as their value, long its
// long ones, and args is how many words it reads after its options before
// the command.
type wrapper struct {
	short string
	long  []string
	args  int
}

// wrappers run the command that follows them.
var wrappers = map[string]wrapper{
	"env":     {short: "uCS", long: []string{"--unset", "--chdir", "--split-string"}},
	"nice":    {short: "n", long: []string{"--adjustment"}},
	"timeout": {short: "sk", long: []string{"--signal", "--kill-after"}, args: 1},
	"nohup":   {},
	"time":    {short: "fo", long: []string{"--format", "--output"}},
	"command": {},
	"exec":    {short: "a"},
	"xargs":   {short: "adEILnPs", long: []string{"--arg-file", "--delimiter", "--eof", "--replace", "--max-lines", "--max-args", "--max-procs", "--max-chars", "--process-slot-var"}},
	"stdbuf":  {short: "ioe", long: []string{"--input", "--output", "--error"}},
}

// skip splits words, what follows the wrapper's name, into its options with
// their values and the rest, which starts with the command it runs.
func (w wrapper) skip(words []string) (opts, rest []string) {
	i := 0
	for i < len(words) {
		a := words[i]
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		i++
		if strings.HasPrefix(a, "--") {
			if slices.Contains(w.long, a) && i < len(words) {
				i++
			}
			continue
		}
		for j, r := range a[1:] {
			if strings.ContainsRune(w.short, r) {
				if j+2 == len(a) && i < len(words) {
					i++
				}
				break
			}
		}
	}
	n := min(i+w.args, len(words))
	return words[:n], words[n:]
}

// deniedCommands are the commands no builder runs: they reach outside the
// worktree (the network, GitHub, other users), or move along a directory
// stack the policy does not follow.
var deniedCommands = []string{"sudo", "su", "doas", "curl", "wget", "gh", "pushd", "popd"}

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

// reservedWords are the shell's words that open a command: the command they
// run is the word after them. The words that close one (fi, done, "}",
// esac) are left as its name, so the paths after them are still judged.
var reservedWords = []string{"!", "{", "if", "then", "elif", "else", "do", "while", "until", "coproc"}

// compoundWords are the reserved words that open a compound command, the
// body a named coproc may run.
var compoundWords = []string{"{", "if", "while", "until", "case", "for", "select"}

// gitOptions are git's own options that take the next word as their value.
var gitOptions = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix"}

// judgeWords is why the simple command c, whose relative paths may be
// relative to any of dirs, may not run, or "". For a cd that may run, to is
// the directory it changes to.
func judgeWords(root string, dirs []string, c command) (reason, to string) {
	words := c.words
	var opts []string
	for len(words) > 0 {
		// In "function NAME" and "coproc NAME" before a compound command,
		// NAME names what follows it; it is not a command.
		if words[0] == "function" || (words[0] == "coproc" && len(words) > 2 && slices.Contains(compoundWords, words[2])) {
			words = words[min(2, len(words)):]
			continue
		}
		if isAssignment(words[0]) || slices.Contains(reservedWords, words[0]) {
			words = words[1:]
			continue
		}
		w, ok := wrappers[filepath.Base(words[0])]
		if !ok {
			break
		}
		o, rest := w.skip(words[1:])
		opts, words = append(opts, o...), rest
	}
	if len(words) == 0 {
		return outside(root, dirs, append(opts, c.redirs...)), ""
	}
	name, args := filepath.Base(words[0]), words[1:]
	if slices.Contains(deniedCommands, name) {
		return name + " is denied", ""
	}
	if name == "git" && gitCommand(args) == "push" {
		return "git push is denied", ""
	}
	if subs, ok := installers[name]; ok {
		if i := slices.IndexFunc(args, func(a string) bool { return !strings.HasPrefix(a, "-") }); i >= 0 && slices.Contains(subs, args[i]) {
			return "installing packages is denied: " + name + " " + args[i], ""
		}
	}
	if reason := outside(root, dirs, slices.Concat(opts, args, c.redirs)); reason != "" {
		return reason, ""
	}
	if name == "cd" {
		return cd(root, dirs, args)
	}
	return "", ""
}

// cd is why a cd with args may not run, or the directory it changes to. It
// must name one in the worktree: a bare cd, "cd -" or a directory in a
// variable go where the policy cannot follow.
func cd(root string, dirs, args []string) (reason, to string) {
	i := slices.IndexFunc(args, func(a string) bool { return a == "-" || !strings.HasPrefix(a, "-") })
	if i < 0 || args[i] == "-" || strings.ContainsRune(args[i], '$') {
		return "cd is denied without a directory of the worktree", ""
	}
	for _, d := range dirs {
		if !within(root, d, args[i]) {
			return "a path outside the worktree: " + args[i], ""
		}
	}
	return "", args[i]
}

// gitCommand is the subcommand git's args run, past git's own options.
func gitCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case slices.Contains(gitOptions, a):
			i++
		case !strings.HasPrefix(a, "-"):
			return a
		}
	}
	return ""
}

// outside is why words may not run: the first one with the shape of a path
// outside the worktree from any of dirs. It is "" when there is none.
func outside(root string, dirs, words []string) string {
	for _, a := range words {
		p, ok := pathArg(a)
		if !ok {
			continue
		}
		for _, d := range dirs {
			if !within(root, d, p) {
				return "a path outside the worktree: " + a
			}
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

// fd tells whether w, right before a redirection, names the descriptor it
// redirects: a number, or a {name} the shell sets to a new one.
func fd(w string) bool {
	if name, ok := strings.CutPrefix(w, "{"); ok {
		name, ok = strings.CutSuffix(name, "}")
		return ok && isAssignment(name+"=")
	}
	return w != "" && strings.Trim(w, "0123456789") == ""
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

// command is a simple command: its words, and the words its redirections
// take, their targets and descriptors, which are none of its words.
type command struct {
	words  []string
	redirs []string
}

// splitCommands is line's simple commands read as a shell reads quotes,
// escapes, redirections, command substitutions and here-documents.
// Operators, pipes and subshells end a command; redirections end a word,
// and the word after one is its target, wherever it stands in the command.
// The commands of a substitution, in double quotes or not, are commands of
// their own, and the substitution stands in its word as "$", as a variable
// does.
func splitCommands(line string) ([]command, error) {
	l := &lexer{rs: []rune(line)}
	if err := l.commands(0); err != nil {
		return nil, err
	}
	return l.cmds, nil
}

// lexer reads a command line from rs, at i, into cmds.
type lexer struct {
	rs   []rune
	i    int
	cmds []command
	// docs are the here-documents whose bodies start at the next line.
	docs []heredoc
}

// heredoc is a here-document: the line that ends it, whether its body is
// quoted text, with no substitutions, and whether "<<-" strips the leading
// tabs of its lines.
type heredoc struct {
	delim  string
	quoted bool
	tabs   bool
}

// at tells whether the rune at i is r.
func (l *lexer) at(i int, r rune) bool {
	return i < len(l.rs) && l.rs[i] == r
}

// commands reads simple commands up to end: the ")" of a "$(", the "`" of a
// backtick substitution, or the end of the line when end is 0. It leaves
// l.i on end.
func (l *lexer) commands(end rune) error {
	var words, redirs []string
	var w strings.Builder
	inWord := false
	// target is whether the next word is a redirection's target.
	target := false
	depth := 0
	endWord := func() {
		if inWord {
			if target {
				redirs = append(redirs, w.String())
			} else {
				words = append(words, w.String())
			}
			w.Reset()
			inWord = false
			target = false
		}
	}
	// redirect starts a redirection at l.i: a number or a {name} right
	// before it is the descriptor it redirects, not a word.
	redirect := func() {
		if inWord && fd(w.String()) {
			redirs = append(redirs, w.String())
			w.Reset()
			inWord = false
		}
		endWord()
	}
	endCmd := func() {
		endWord()
		target = false
		if len(words) > 0 || len(redirs) > 0 {
			l.cmds = append(l.cmds, command{words: words, redirs: redirs})
			words, redirs = nil, nil
		}
	}
	for ; l.i < len(l.rs); l.i++ {
		r := l.rs[l.i]
		switch {
		case end != 0 && r == end && depth == 0:
			endCmd()
			return nil
		case r == '\'':
			j := slices.Index(l.rs[l.i+1:], '\'')
			if j < 0 {
				return errors.New("unclosed single quote")
			}
			w.WriteString(string(l.rs[l.i+1 : l.i+1+j]))
			inWord = true
			l.i += j + 1
		case r == '"':
			l.i++
			if err := l.quoted(&w, '"'); err != nil {
				return err
			}
			inWord = true
		case r == '\\':
			if l.i+1 < len(l.rs) {
				l.i++
				w.WriteRune(l.rs[l.i])
				inWord = true
			}
		case r == '$' && l.at(l.i+1, '('), r == '`':
			if err := l.substitution(); err != nil {
				return err
			}
			w.WriteRune('$')
			inWord = true
		case r == ' ' || r == '\t':
			endWord()
		case r == '\n':
			endCmd()
			if err := l.heredocs(); err != nil {
				return err
			}
		case r == '(':
			depth++
			endCmd()
		case r == ')':
			depth = max(depth-1, 0)
			endCmd()
		case strings.ContainsRune(";&|", r):
			endCmd()
		case r == '<' && l.at(l.i+1, '<') && l.at(l.i+2, '<'):
			// A here-string: its word is its target.
			redirect()
			target = true
			l.i += 2
		case r == '<' && l.at(l.i+1, '<'):
			redirect()
			l.i += 2
			if err := l.heredoc(); err != nil {
				return err
			}
		case r == '<' || r == '>':
			redirect()
			target = true
			if l.at(l.i+1, '&') || (r == '>' && l.at(l.i+1, '|')) {
				l.i++
			}
		case r == '#' && !inWord:
			for l.i+1 < len(l.rs) && l.rs[l.i+1] != '\n' {
				l.i++
			}
		default:
			w.WriteRune(r)
			inWord = true
		}
	}
	if end != 0 {
		return errors.New("unclosed command substitution")
	}
	endCmd()
	if len(l.docs) > 0 {
		return errors.New("unclosed here-document")
	}
	return nil
}

// substitution reads the commands of the command substitution that starts
// at l.i, "$(" or "`", and leaves l.i on its end.
func (l *lexer) substitution() error {
	if l.rs[l.i] == '`' {
		l.i++
		return l.commands('`')
	}
	l.i += 2
	return l.commands(')')
}

// quoted reads double-quoted text from l.i into w up to end, the closing
// '"', or to the end of the line when end is 0, and leaves l.i on end. Its
// command substitutions are read as commands.
func (l *lexer) quoted(w *strings.Builder, end rune) error {
	for ; l.i < len(l.rs); l.i++ {
		r := l.rs[l.i]
		switch {
		case end != 0 && r == end:
			return nil
		case r == '\\' && l.i+1 < len(l.rs) && strings.ContainsRune("$`\"\\\n", l.rs[l.i+1]):
			l.i++
			w.WriteRune(l.rs[l.i])
		case r == '$' && l.at(l.i+1, '('), r == '`':
			if err := l.substitution(); err != nil {
				return err
			}
			w.WriteRune('$')
		default:
			w.WriteRune(r)
		}
	}
	if end != 0 {
		return errors.New("unclosed double quote")
	}
	return nil
}

// heredoc reads the delimiter of a here-document from l.i, after its "<<",
// and leaves l.i on its last rune; its body is read at the next line.
func (l *lexer) heredoc() error {
	var d heredoc
	if l.at(l.i, '-') {
		d.tabs = true
		l.i++
	}
	for l.at(l.i, ' ') || l.at(l.i, '\t') {
		l.i++
	}
	var w strings.Builder
	for ; l.i < len(l.rs) && !strings.ContainsRune(" \t\n;&|<>()", l.rs[l.i]); l.i++ {
		switch r := l.rs[l.i]; r {
		case '\'', '"':
			j := slices.Index(l.rs[l.i+1:], r)
			if j < 0 {
				return errors.New("unclosed quote in a here-document's delimiter")
			}
			w.WriteString(string(l.rs[l.i+1 : l.i+1+j]))
			l.i += j + 1
			d.quoted = true
		case '\\':
			d.quoted = true
			if l.i+1 < len(l.rs) {
				l.i++
				w.WriteRune(l.rs[l.i])
			}
		default:
			w.WriteRune(r)
		}
	}
	if w.Len() == 0 {
		return errors.New("a here-document without a delimiter")
	}
	d.delim = w.String()
	l.docs = append(l.docs, d)
	l.i--
	return nil
}

// heredocs reads the bodies of the pending here-documents from the line
// after l.i, a newline, and leaves l.i on the newline that ends the last
// delimiter. A body whose delimiter is not quoted is read as double-quoted
// text, so its command substitutions are read as commands.
func (l *lexer) heredocs() error {
	docs := l.docs
	l.docs = nil
	for _, d := range docs {
		start := l.i + 1
		line := start
		for {
			end := len(l.rs)
			if j := slices.Index(l.rs[line:], '\n'); j >= 0 {
				end = line + j
			}
			got := string(l.rs[line:end])
			if d.tabs {
				got = strings.TrimLeft(got, "\t")
			}
			if got == d.delim {
				if !d.quoted {
					body := &lexer{rs: l.rs[start:line]}
					var w strings.Builder
					if err := body.quoted(&w, 0); err != nil {
						return err
					}
					l.cmds = append(l.cmds, body.cmds...)
				}
				l.i = end
				break
			}
			if end == len(l.rs) {
				return errors.New("unclosed here-document")
			}
			line = end + 1
		}
	}
	return nil
}
