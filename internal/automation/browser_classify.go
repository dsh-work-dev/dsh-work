package automation

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/local/dsh-work/internal/settings"
)

//go:embed risk-keywords.json
var riskKeywordsJSON []byte

type riskKeywords struct {
	Category string   `json:"category"`
	Words    []string `json:"words"`
}

var browserKeywords = func() []riskKeywords {
	var v []riskKeywords
	if err := json.Unmarshal(riskKeywordsJSON, &v); err != nil {
		panic(err)
	}
	return v
}()

type BrowserElement struct {
	Name         string `json:"name"`
	Tag          string `json:"tag"`
	Role         string `json:"role"`
	Type         string `json:"type"`
	Autocomplete string `json:"autocomplete"`
	Multiline    bool   `json:"multiline"`
	InForm       bool   `json:"inForm"`
	Submit       bool   `json:"submit"`
	SubmitName   string `json:"submitName"`
}
type BrowserTab struct {
	Index int    `json:"index"`
	URL   string `json:"url"`
}
type BrowserEvidence struct {
	URL      string           `json:"url"`
	Elements []BrowserElement `json:"elements"`
	Tabs     []BrowserTab     `json:"tabs"`
}

func browserHost(address string, navigate bool) (string, error) {
	u, e := url.Parse(address)
	if e != nil {
		return "", errors.New("BROWSER_URL_BLOCKED")
	}
	if address == "about:blank" {
		return "", nil
	}
	if !navigate && (u.Scheme == "blob" || u.Scheme == "data") {
		return "", nil
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return "", errors.New("BROWSER_URL_BLOCKED")
	}
	h, e := settings.BrowserAddressHost(u.Scheme + "://" + u.Host)
	if e != nil {
		return "", errors.New("BROWSER_URL_BLOCKED")
	}
	return h, nil
}
func keywordCategory(name, address string) string {
	name = strings.ToLower(name)
	for _, entry := range browserKeywords {
		for _, word := range entry.Words {
			if strings.Contains(name, word) {
				return entry.Category
			}
		}
	}
	if strings.Contains(strings.ToLower(address), "settings") && (strings.Contains(name, "save") || strings.Contains(name, "apply") || strings.Contains(name, "保存") || strings.Contains(name, "適用")) {
		return "settings"
	}
	return ""
}
func secretElement(e BrowserElement) bool {
	name := strings.ToLower(e.Name)
	return strings.EqualFold(e.Type, "password") || strings.Contains(strings.ToLower(e.Autocomplete), "one-time-code") || strings.Contains(name, "验证码") || strings.Contains(name, "otp") || strings.Contains(name, "code") || strings.Contains(name, "認証コード")
}
func browserCategory(r Request, e BrowserEvidence) (string, error) {
	tool := strings.TrimPrefix(r.Tool, "browser_")
	var target BrowserElement
	if len(e.Elements) > 0 {
		target = e.Elements[0]
	}
	click := func(v BrowserElement) string {
		if v.Tag == "" {
			return "unknown"
		}
		if c := keywordCategory(v.Name, e.URL); c != "" {
			return c
		}
		if v.Submit {
			return "submit"
		}
		return ""
	}
	enter := func(v BrowserElement) string {
		if v.Tag == "" {
			return "unknown"
		}
		if v.Tag == "button" || v.Tag == "a" || v.Role == "button" || v.Role == "link" {
			return click(v)
		}
		if v.Multiline {
			return "send"
		}
		name := strings.ToLower(v.Name)
		if v.Type == "search" || v.Role == "searchbox" || strings.Contains(name, "search") || strings.Contains(name, "搜索") || strings.Contains(name, "検索") {
			return ""
		}
		if v.InForm {
			if c := keywordCategory(v.SubmitName, e.URL); c != "" {
				return c
			}
			return "submit"
		}
		return ""
	}
	switch tool {
	case "type", "fill_form":
		if len(e.Elements) == 0 {
			return "", errors.New("BROWSER_TARGET_UNVERIFIED: take a fresh snapshot before entering text")
		}
		for _, v := range e.Elements {
			if v.Tag == "" || v.Tag == "iframe" || v.Tag == "frame" {
				return "", errors.New("BROWSER_TARGET_UNVERIFIED")
			}
			if secretElement(v) {
				return "", errors.New("BROWSER_SECRET_INPUT_BLOCKED: Passwords and verification codes are entered by the user. Ask the user to type it, then continue.")
			}
		}
		if submit, _ := r.Arguments["submit"].(bool); submit {
			return enter(target), nil
		}
		return "", nil
	case "press_key":
		if target.Tag == "" || target.Tag == "iframe" || target.Tag == "frame" {
			return "", errors.New("BROWSER_TARGET_UNVERIFIED")
		}
		if secretElement(target) {
			return "", errors.New("BROWSER_SECRET_INPUT_BLOCKED: Passwords and verification codes are entered by the user.")
		}
		key, _ := r.Arguments["key"].(string)
		parts := strings.Split(key, "+")
		if strings.EqualFold(parts[len(parts)-1], "Enter") {
			return enter(target), nil
		}
		return "", nil
	case "drag":
		names := []string{}
		submit := false
		for _, v := range e.Elements {
			if v.Tag == "" {
				return "unknown", nil
			}
			names = append(names, v.Name)
			submit = submit || v.Submit
		}
		if c := keywordCategory(strings.Join(names, " "), e.URL); c != "" {
			return c, nil
		}
		if submit {
			return "submit", nil
		}
		return "", nil
	case "click", "select_option":
		return click(target), nil
	case "file_upload":
		return "upload", nil
	case "drop":
		if paths, ok := r.Arguments["paths"].([]any); ok && len(paths) > 0 {
			return "upload", nil
		}
		if paths, ok := r.Arguments["paths"].([]string); ok && len(paths) > 0 {
			return "upload", nil
		}
		if secretElement(target) {
			return "", errors.New("BROWSER_SECRET_INPUT_BLOCKED")
		}
		return "", nil
	case "handle_dialog":
		if accept, _ := r.Arguments["accept"].(bool); accept {
			return "unknown", nil
		}
		return "", nil
	case "evaluate", "run_code_unsafe":
		return "script", nil
	case "snapshot", "tabs", "take_screenshot", "navigate", "navigate_back", "wait_for", "console_messages", "network_requests", "network_request", "hover", "resize", "find", "close":
		return "", nil
	default:
		return "unknown", nil
	}
}
