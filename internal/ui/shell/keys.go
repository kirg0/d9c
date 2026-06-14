package shell

import tea "github.com/charmbracelet/bubbletea"

// encodeKey translates a bubbletea key event into the byte sequence a terminal
// would send to the remote process's stdin. It returns nil for keys with no
// terminal representation (e.g. function keys we don't map), which the caller
// drops. An Alt modifier is sent as an ESC prefix (the conventional meta
// encoding).
func encodeKey(msg tea.KeyMsg) []byte {
	b := encodeBase(msg)
	if b == nil {
		return nil
	}
	if msg.Alt {
		return append([]byte{0x1b}, b...)
	}
	return b
}

func encodeBase(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	default:
		// Control keys: bubbletea assigns each a KeyType equal to its C0 byte
		// value (ctrl+a=1 … ctrl+z=26, enter=13, tab=9, esc=27, backspace=127).
		if t := msg.Type; (t >= 0 && t <= 31) || t == 127 {
			return []byte{byte(t)}
		}
		return nil
	}
}
