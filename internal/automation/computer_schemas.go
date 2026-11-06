package automation

import "encoding/json"

func ComputerToolSchemas(engine ...string) []json.RawMessage {
	auv := len(engine) > 0 && engine[0] == "auv"
	tools := []struct {
		name, description string
		properties        map[string]any
		required          []string
	}{
		{"computer_release", "Release this conversation's computer engine, temporary application grants and desktop lease after finishing.", map[string]any{}, nil},
		{"computer_windows", "List visible application windows with application and access-level metadata. Choose one exact pid/window_id; no window content is read.", map[string]any{}, nil},
		{"computer_observe", "Observe one exact window after the user allows its application. Returns a window screenshot and UIA elements with snapshot_id. Observe again after every action or changed target.", map[string]any{}, []string{"pid", "window_id"}},
		{"computer_click", "Click an observed UIA element, or window-local screenshot coordinates. Uses background delivery only. Verify the result with computer_observe.", map[string]any{"element_index": map[string]any{"type": "integer", "minimum": 0}, "x": map[string]any{"type": "number", "minimum": 0}, "y": map[string]any{"type": "number", "minimum": 0}}, []string{"pid", "window_id", "snapshot_id"}},
		{"computer_type", "Append text to an observed editable UIA element using background delivery. This appends; it does not replace existing content. Verify with computer_observe.", map[string]any{"element_index": map[string]any{"type": "integer", "minimum": 0}, "text": map[string]any{"type": "string", "maxLength": 16384}}, []string{"pid", "window_id", "snapshot_id", "element_index", "text"}},
		{"computer_scroll", "Scroll the observed exact window in the background. Windows scrolling targets the focused region; verify with computer_observe.", map[string]any{"direction": map[string]any{"enum": []string{"up", "down", "left", "right"}}, "amount": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "by": map[string]any{"enum": []string{"line", "page"}}}, []string{"pid", "window_id", "snapshot_id", "direction"}},
		{"computer_key", "Send one key to an observed element using background delivery only. No foreground fallback. Verify with computer_observe.", map[string]any{"element_index": map[string]any{"type": "integer", "minimum": 0}, "key": map[string]any{"type": "string", "maxLength": 40}, "modifiers": map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{"enum": []string{"ctrl", "shift", "alt"}}}}, []string{"pid", "window_id", "snapshot_id", "element_index", "key"}},
	}
	var out []json.RawMessage
	for _, t := range tools {
		if auv {
			switch t.name {
			case "computer_type", "computer_key":
				continue
			case "computer_observe":
				t.description = "Observe one exact permitted window as a screenshot with snapshot_id. AUV on Windows has no UIA elements or background text/key input. Locate controls in the image; observe again after every action."
			case "computer_click":
				delete(t.properties, "element_index")
				t.required = []string{"pid", "window_id", "snapshot_id", "x", "y"}
				t.description = "Click window-local screenshot pixels in the observed exact window using AUV background-only delivery. No indexed UIA controls. Observe to verify delivery."
			case "computer_scroll":
				t.properties["x"] = map[string]any{"type": "number", "minimum": 0}
				t.properties["y"] = map[string]any{"type": "number", "minimum": 0}
				t.description = "Scroll in the observed AUV window at optional window-local screenshot x/y (both required together); defaults to image center. amount is lines (40 pixels each) or pages (80% of image height/width). Background only; observe to verify."
			}
		}
		if t.name != "computer_windows" && t.name != "computer_release" {
			t.properties["pid"] = map[string]any{"type": "integer", "minimum": 1}
			t.properties["window_id"] = map[string]any{"type": "integer", "minimum": 1}
		}
		if t.name != "computer_windows" && t.name != "computer_observe" && t.name != "computer_release" {
			t.properties["snapshot_id"] = map[string]any{"type": "string"}
		}
		v := map[string]any{"name": t.name, "description": t.description, "inputSchema": map[string]any{"type": "object", "properties": t.properties, "additionalProperties": false}}
		if len(t.required) > 0 {
			v["inputSchema"].(map[string]any)["required"] = t.required
		}
		b, _ := json.Marshal(v)
		out = append(out, b)
	}
	return out
}
