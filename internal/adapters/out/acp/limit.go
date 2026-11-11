package acp

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultLimitWait is how long a job is paused on an agent's usage or rate
// limit when the agent's answer does not say when it may go on.
const DefaultLimitWait = 30 * time.Minute

// maxLimitReason is the most bytes of an agent's answer a limit keeps as its
// reason: enough for the provider's message, short enough for a chat.
const maxLimitReason = 500

// limit recognizes one agent's usage or rate limit in its error answer, by
// the answer's JSON-RPC code and its text, message and data together, and
// reads from the text when the agent may go on: zero when it does not say.
type limit struct {
	match  func(code int, text string) bool
	resume func(text string, now time.Time) time.Time
}

// limits are the known agents' limits, Claude Code's, Codex's and Gemini
// CLI's, then any agent's that says "rate limit" outright. roles.yaml names
// agents freely, so an answer is checked against all of them rather than
// against the one its command looks like.
var limits = []limit{
	{match: claudeLimited, resume: claudeResume},
	{match: codexLimited, resume: retryIn},
	{match: geminiLimited, resume: geminiResume},
	{match: rateLimited, resume: retryIn},
}

// limited tells whether err, the agent's answer to a prompt, is its usage or
// rate limit, and if so its words and when it may go on: when the agent says,
// or DefaultLimitWait after now.
func limited(err error, now time.Time) (string, time.Time, bool) {
	var r *refusal
	if !errors.As(err, &r) {
		return "", time.Time{}, false
	}
	text := strings.TrimSpace(r.Message + " " + data(r.Data))
	for _, l := range limits {
		if !l.match(r.Code, strings.ToLower(text)) {
			continue
		}
		until := l.resume(text, now)
		if !until.After(now) {
			until = now.Add(DefaultLimitWait)
		}
		return reason(text), until, true
	}
	return "", time.Time{}, false
}

// data is an error's data as text: a JSON string as it reads, anything else
// as it was sent.
func data(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// reason is text cut to maxLimitReason bytes, on a rune boundary.
func reason(text string) string {
	if len(text) <= maxLimitReason {
		return text
	}
	cut := maxLimitReason
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// Claude Code: "Claude AI usage limit reached|<unix seconds>", "5-hour limit
// reached ∙ resets 3pm", "You've hit your limit · resets 10:30am
// (Europe/Madrid)", or the Anthropic API's rate_limit_error.
var (
	claudeEpoch  = regexp.MustCompile(`(?i)limit reached\|(\d{9,})`)
	claudeResets = regexp.MustCompile(`(?i)resets\s+(?:(mon|tue|wed|thu|fri|sat|sun)[a-z]*\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)?(?:\s*\(([^)]+)\))?`)
)

func claudeLimited(_ int, text string) bool {
	return strings.Contains(text, "usage limit reached") || strings.Contains(text, "rate_limit_error") ||
		strings.Contains(text, "hit your limit") || (strings.Contains(text, "limit reached") && strings.Contains(text, "resets"))
}

func claudeResume(text string, now time.Time) time.Time {
	if m := claudeEpoch.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(sec, 0).In(now.Location())
		}
	}
	m := claudeResets.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}
	}
	hour, _ := strconv.Atoi(m[2])
	minute, _ := strconv.Atoi(m[3])
	switch strings.ToLower(m[4]) {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 12 {
			hour += 12
		}
	}
	if hour > 23 || minute > 59 {
		return time.Time{}
	}
	loc := now.Location()
	if m[5] != "" {
		if l, err := time.LoadLocation(m[5]); err == nil {
			loc = l
		}
	}
	at := now.In(loc)
	next := time.Date(at.Year(), at.Month(), at.Day(), hour, minute, 0, 0, loc)
	if day := weekday(m[1]); day >= 0 {
		next = next.AddDate(0, 0, (int(day)-int(next.Weekday())+7)%7)
		if !next.After(now) {
			next = next.AddDate(0, 0, 7)
		}
	} else if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.In(now.Location())
}

// weekday is the day a three-letter English abbreviation names, or -1.
func weekday(abbr string) time.Weekday {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.EqualFold(d.String()[:3], abbr) {
			return d
		}
	}
	return -1
}

// Codex: "You've hit your usage limit. … try again in 1 day 2 hours 5
// minutes.", the usage_limit_reached and rate_limit_exceeded error types, and
// OpenAI's "429 Too Many Requests".
func codexLimited(_ int, text string) bool {
	return strings.Contains(text, "usage limit") || strings.Contains(text, "usage_limit_reached") ||
		strings.Contains(text, "rate_limit_exceeded") || strings.Contains(text, "too many requests")
}

// Gemini CLI: Google's 429 RESOURCE_EXHAUSTED, "Quota exceeded for quota
// metric …", with "Please retry in 23.5s" or a retryDelay of "23s".
var geminiDelay = regexp.MustCompile(`(?i)"?retryDelay"?\s*:\s*"(\d+(?:\.\d+)?s)"`)

func geminiLimited(code int, text string) bool {
	return code == 429 || strings.Contains(text, "resource_exhausted") || strings.Contains(text, "quota exceeded") ||
		strings.Contains(text, "ratelimitexceeded")
}

func geminiResume(text string, now time.Time) time.Time {
	if m := geminiDelay.FindStringSubmatch(text); m != nil {
		if d, err := time.ParseDuration(m[1]); err == nil {
			return now.Add(d)
		}
	}
	return retryIn(text, now)
}

// rateLimited is any agent's answer that names a rate limit outright, such
// as "rate limited" or "rate limit exceeded".
func rateLimited(_ int, text string) bool {
	return strings.Contains(text, "rate limit") || strings.Contains(text, "rate-limit")
}

var (
	retryAfter = regexp.MustCompile(`(?i)(?:try again|retry) (?:in|after)\s+([^;,\n]+)`)
	unit       = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
)

// unitOf is how long one of each of unit's words lasts.
var unitOf = map[string]time.Duration{
	"day": 24 * time.Hour, "days": 24 * time.Hour, "d": 24 * time.Hour,
	"hour": time.Hour, "hours": time.Hour, "hr": time.Hour, "hrs": time.Hour, "h": time.Hour,
	"minute": time.Minute, "minutes": time.Minute, "min": time.Minute, "mins": time.Minute, "m": time.Minute,
	"second": time.Second, "seconds": time.Second, "sec": time.Second, "secs": time.Second, "s": time.Second,
}

// retryIn reads "try again in …" or "retry after …" from text, as a Go
// duration ("6.5s", "1m30s") or in words ("1 day 2 hours 5 minutes"), and
// is now plus it; zero when text says neither.
func retryIn(text string, now time.Time) time.Time {
	m := retryAfter.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}
	}
	// A sentence that follows is not part of the wait.
	after, _, _ := strings.Cut(m[1], ". ")
	after = strings.TrimSuffix(strings.TrimSpace(after), ".")
	if first, _, _ := strings.Cut(after, " "); first != "" {
		if d, err := time.ParseDuration(first); err == nil {
			return now.Add(d)
		}
	}
	var total time.Duration
	for _, u := range unit.FindAllStringSubmatch(after, -1) {
		n, err := strconv.ParseFloat(u[1], 64)
		if err != nil {
			continue
		}
		total += time.Duration(n * float64(unitOf[strings.ToLower(u[2])]))
	}
	if total == 0 {
		return time.Time{}
	}
	return now.Add(total)
}
