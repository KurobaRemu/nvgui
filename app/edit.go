package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *Shell) HandleEditCommand(c *ui.Context, ev ui.InputEvent) bool {
	if s == nil || s.impl == nil || s.impl.checkOpen() != nil || s.impl.session == nil {
		return false
	}
	command := editCommand(ev)
	if command == "" {
		return false
	}
	if c != nil {
		s.impl.mu.Lock()
		s.impl.services = c.Services()
		s.impl.mu.Unlock()
	}
	switch command {
	case "copy":
		return s.impl.copyToClipboard()
	case "cut":
		return s.impl.cutToClipboard()
	case "paste":
		return s.impl.pasteFromClipboard()
	default:
		return false
	}
}

func editCommand(ev ui.InputEvent) string {
	if ev.Kind == ui.InputCommand {
		switch ev.Text {
		case "copy", "cut", "paste":
			return ev.Text
		default:
			return ""
		}
	}
	if ev.Kind != ui.InputKeyDown || ev.Mods != ui.Cmd {
		return ""
	}
	switch ev.Key {
	case ui.KeyC:
		return "copy"
	case ui.KeyX:
		return "cut"
	case ui.KeyV:
		return "paste"
	default:
		return ""
	}
}

func (impl *shellImpl) copyToClipboard() bool {
	value, err := impl.readRegister()
	if err != nil {
		return false
	}
	impl.writeClipboard(value)
	return true
}

func (impl *shellImpl) cutToClipboard() bool {
	mode := impl.handler.Mode()
	if isVisualMode(mode) {
		if err := impl.session.Nvim().FeedKeys("x", "nx", false); err != nil {
			return false
		}
		return impl.copyToClipboard()
	}
	if !impl.copyToClipboard() {
		return false
	}
	if isEditingMode(mode) {
		return true
	}
	return impl.session.Nvim().FeedKeys(`"_x`, "n", false) == nil
}

func (impl *shellImpl) pasteFromClipboard() bool {
	textValue := impl.readClipboard()
	if textValue == "" {
		return true
	}
	textValue = strings.ReplaceAll(textValue, "\r\n", "\n")
	textValue = strings.ReplaceAll(textValue, "\r", "\n")
	return impl.queue.Enqueue(ui.InputEvent{Kind: ui.InputText, Text: textValue}) == nil
}

func (impl *shellImpl) readRegister() (string, error) {
	var value string
	err := impl.session.Nvim().Eval(`getreg('"')`, &value)
	return value, err
}

func isVisualMode(mode string) bool {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	switch normalized {
	case "v", "\x16":
		return true
	}
	return strings.HasPrefix(normalized, "visual")
}

func isEditingMode(mode string) bool {
	family := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(mode)), func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(family) == 0 {
		return false
	}
	switch family[0] {
	case "insert", "cmdline", "terminal", "replace":
		return true
	default:
		return false
	}
}

func (impl *shellImpl) readClipboard() string {
	if impl.clipboard != nil {
		return impl.clipboard.ReadText()
	}
	impl.mu.Lock()
	services := impl.services
	impl.mu.Unlock()
	return services.ReadClipboard()
}

func (impl *shellImpl) writeClipboard(value string) {
	if impl.clipboard != nil {
		impl.clipboard.WriteText(value)
		return
	}
	impl.mu.Lock()
	services := impl.services
	impl.mu.Unlock()
	services.WriteClipboard(value)
}
