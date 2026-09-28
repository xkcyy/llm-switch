package provider

import "testing"

// 只有明确声明 image 才产生 vision 能力；其余模态不猜。
func TestCapabilitiesFromModalities(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"text"}, ""},
		{[]string{"text", "audio", "video"}, ""},
		{[]string{"text", "image"}, "vision"},
		{[]string{"IMAGE"}, "vision"},
		{[]string{" image "}, "vision"},
	}
	for _, c := range cases {
		got := capabilitiesFromModalities(c.in)
		if c.want == "" {
			if got != nil {
				t.Fatalf("modalities %v 不应产出能力，得到 %v", c.in, got)
			}
			continue
		}
		if len(got) != 2 || got[0] != "tools" || got[1] != c.want {
			t.Fatalf("modalities %v → %v，want [tools %s]", c.in, got, c.want)
		}
	}
}
