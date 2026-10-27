package space

import "testing"

// The default zone must be one the settings list can show as selected.
func TestDefaultTimezoneIsListed(t *testing.T) {
	for _, c := range tzChoices {
		if c.Name == defaultTimezone {
			return
		}
	}
	t.Errorf("defaultTimezone %q is not in tzChoices", defaultTimezone)
}

// With no preference, no $TZ and no config file, the shell must not show Beijing time.
func TestLocalTimezoneDefault(t *testing.T) {
	t.Setenv("TZ", "")
	// no ~/.config/rmkit-cn/space.json under this fake home
	t.Setenv("HOME", t.TempDir())
	// and no shell/config.json under this data dir
	h := &Handler{Reg: &Registry{DataDir: t.TempDir()}}

	name, off := h.localTimezone()
	if name != defaultTimezone {
		t.Errorf("zone = %q, want %q", name, defaultTimezone)
	}
	// Berlin is UTC+1 in winter and UTC+2 in summer
	if off != 60 && off != 120 {
		t.Errorf("offset = %d minutes, want 60 or 120", off)
	}
}
