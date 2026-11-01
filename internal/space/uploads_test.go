package space

import (
	"path/filepath"
	"testing"
)

func TestValidateUploads(t *testing.T) {
	ok := Manifest{ID: "flashcard", Name: "闪卡", Version: "1.0.0", Entry: "main.qml",
		Service: &Service{Exec: "bin/x", Port: 9210},
		Uploads: []Upload{{ID: "anki", Label: "闪卡", Accept: []string{".apkg", ".txt"}, Path: "/import", List: "/decks"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法的登记被拒: %v", err)
	}
	bad := []func(m *Manifest){
		func(m *Manifest) { m.Service = nil },
		func(m *Manifest) { m.Uploads[0].Accept = []string{"apkg"} },
		func(m *Manifest) { m.Uploads[0].Path = "/../etc" },
		func(m *Manifest) { m.Uploads[0].Path = "" },
		func(m *Manifest) { m.Uploads[0].Label = "" },
		func(m *Manifest) { m.Uploads = append(m.Uploads, m.Uploads[0]) },
	}
	for i, f := range bad {
		m := ok
		m.Uploads = append([]Upload(nil), ok.Uploads...)
		m.Uploads[0].Accept = append([]string(nil), ok.Uploads[0].Accept...)
		f(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("第 %d 个坏登记没被拒", i)
		}
	}
}

func TestFocusedOnlyUploadSlot(t *testing.T) {
	tmp := t.TempDir()
	r := &Registry{BuiltinDir: filepath.Join(tmp, "builtin"), UserDir: filepath.Join(tmp, "apps"), DataDir: filepath.Join(tmp, "data"), Arch: "armv7"}
	writeApp(t, r.UserDir, "stickers", `{"id":"stickers","name":"贴纸","version":"2.1.0","entry":"main.qml","service":{"exec":"bin/server","port":9200},"uploads":[{"id":"photo","label":"实拍","accept":[".png"],"path":"/sticker/photo","focused_only":true}]}`)
	writeApp(t, r.UserDir, "flashcard", `{"id":"flashcard","name":"闪卡","version":"1.0.0","entry":"main.qml","service":{"exec":"bin/server","port":9210},"uploads":[{"id":"anki","label":"闪卡","accept":[".apkg"],"path":"/import"}]}`)
	h := &Handler{Reg: r}
	for _, focus := range []string{"", "doc", "flashcard.anki", "stickers.other"} {
		for _, slot := range h.uploadSlots(focus) {
			if slot.App == "stickers" {
				t.Errorf("公共页面 %q 不应出现实拍", focus)
			}
		}
	}
	found := false
	for _, slot := range h.uploadSlots("stickers.photo") {
		if slot.App == "stickers" && slot.ID == "photo" {
			found = true
		}
	}
	if !found {
		t.Fatal("实拍专属二维码页面应显示上传项")
	}
}
