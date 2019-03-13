package main

import (
	"testing"
)

func Test_TileFormat_ContentType(t *testing.T) {
	var conditions = []struct {
		in  TileFormat
		out string
	}{
		{PNG, "image/png"},
		{JPG, "image/jpeg"},
		{PNG, "image/png"},
		{PBF, "application/x-protobuf"},
		{WEBP, "image/webp"},
	}

	for _, condition := range conditions {
		if condition.in.ContentType() != condition.out {
			t.Errorf("%q.ContentType() => %q, expected %q", condition.in, condition.in.ContentType(), condition.out)
		}
	}
}
