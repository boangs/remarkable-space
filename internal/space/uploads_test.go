package space

import "testing"

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
