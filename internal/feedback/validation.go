package feedback

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"
)

const (
	MaxImages            = 6
	MaxImageBytes        = 5 * 1024 * 1024
	MaxDescriptionLength = 20000
	MaxRequestBytes      = MaxImages*MaxImageBytes + 1024*1024
)

type Image struct {
	Data      []byte
	Extension string
}

func ParseImage(data []byte) (Image, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("image exceeds the 5 MB limit or is empty")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 || config.Height <= 0 {
		return Image{}, fmt.Errorf("invalid JPEG or PNG image")
	}
	if format == "jpeg" {
		format = "jpg"
	}
	return Image{Data: data, Extension: format}, nil
}

func ValidateContent(description string, count int) error {
	if count > MaxImages {
		return fmt.Errorf("at most 6 images are allowed")
	}
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		return fmt.Errorf("feedback text exceeds the length limit")
	}
	if strings.TrimSpace(description) == "" && count == 0 {
		return fmt.Errorf("provide feedback text or images")
	}
	return nil
}
