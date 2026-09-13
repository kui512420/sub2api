package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestIsManagedObjectKey(t *testing.T) {
	prefixes := []string{"images/", "videos/"}
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"image ok", "images/imgtask_abc-0.png", true},
		{"video ok", "videos/abc.mp4", true},
		{"backup forbidden", "backups/2026/dump.zip", false},
		{"bucket root forbidden", "config.yaml", false},
		{"parent traversal in image", "images/../../etc/passwd", false},
		{"leading traversal", "../images/a.png", false},
		{"empty key", "", false},
		{"prefix only is allowed", "images/", true},
		{"case sensitive prefix", "Images/a.png", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsManagedObjectKey(tc.key, prefixes); got != tc.want {
				t.Fatalf("IsManagedObjectKey(%q)=%v want %v", tc.key, got, tc.want)
			}
		})
	}

	// 白名单为空时一律拒绝，默认拒绝优先。
	if IsManagedObjectKey("images/a.png", nil) {
		t.Fatal("empty allow-list must reject every key")
	}
}

func TestInferStoredObjectKind(t *testing.T) {
	cases := map[string]string{
		"images/a.png": StoredObjectKindImage,
		"images/b.JPG": StoredObjectKindImage,
		"videos/c.mp4": StoredObjectKindVideo,
		"clip.MOV":     StoredObjectKindVideo,
		"notes.txt":    StoredObjectKindOther,
	}
	for key, want := range cases {
		if got := InferStoredObjectKind(key); got != want {
			t.Fatalf("InferStoredObjectKind(%q)=%q want %q", key, got, want)
		}
	}
}

func TestManagedContinuationRoundTrip(t *testing.T) {
	pairs := [][2]string{
		{"imgToken123", "vidToken456"},
		{"", ""},
		{"onlyImage", ""},
		{"", "onlyVideo"},
	}
	for _, p := range pairs {
		encoded := encodeManagedContinuation(p[0], p[1])
		gi, gv := decodeManagedContinuation(encoded)
		if gi != p[0] || gv != p[1] {
			t.Fatalf("round trip mismatch: got (%q,%q) want (%q,%q)", gi, gv, p[0], p[1])
		}
	}

	// 非法 base64（单类别裸 token）应原样作为 image token 返回，不报错。
	gi, gv := decodeManagedContinuation("plain-aws-token")
	if gi != "plain-aws-token" || gv != "" {
		t.Fatalf("bare token decode = (%q,%q), want (plain-aws-token,\"\")", gi, gv)
	}
}

func TestManagedImagePrefix(t *testing.T) {
	if got := managedImagePrefix(&config.ImageStorageConfig{Prefix: ""}); got != "images/" {
		t.Fatalf("default prefix=%q want images/", got)
	}
	if got := managedImagePrefix(&config.ImageStorageConfig{Prefix: "media/"}); got != "media/" {
		t.Fatalf("custom prefix=%q want media/", got)
	}
}
