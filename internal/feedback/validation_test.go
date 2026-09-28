package feedback

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestValidateFeedback(t *testing.T) {
	for _, tc := range []struct {
		text  string
		count int
		valid bool
	}{
		{"text", 0, true}, {"", 1, true}, {" ", 0, false}, {"text", 7, false}, {strings.Repeat("中", MaxDescriptionLength+1), 0, false},
	} {
		if err := ValidateContent(tc.text, tc.count); (err == nil) != tc.valid {
			t.Errorf("count=%d valid=%v err=%v", tc.count, tc.valid, err)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseImage(buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("fake.png"), make([]byte, MaxImageBytes+1)} {
		if _, err := ParseImage(data); err == nil {
			t.Error("accepted invalid image")
		}
	}
}
