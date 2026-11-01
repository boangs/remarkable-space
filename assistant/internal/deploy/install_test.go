package deploy

import "testing"

func TestApploadDirForArch(t *testing.T) {
	for arch, want := range map[string]string{
		"aarch64": "third_party/appload/aarch64",
		"armv7l":  "third_party/appload/armv7",
	} {
		if got := apploadDirForArch(arch); got != want {
			t.Errorf("apploadDirForArch(%q) = %q, want %q", arch, got, want)
		}
	}
}
