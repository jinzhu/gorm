//go:build kingbase_test
// +build kingbase_test

package testutil

import (
	"bytes"
	"database/sql/driver"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDriverLogLargeParametersAreSummarized(t *testing.T) {
	binary := bytes.Repeat([]byte{0xff}, 16777216)
	text := strings.Repeat("汉", 2000)
	got := readableValues([]driver.Value{binary, text, "KeepCase"})
	for _, item := range got[:2] {
		message, ok := item.(string)
		if !ok || len(message) > 512 || !utf8.ValidString(message) || !strings.Contains(message, "字节") {
			t.Fatalf("unbounded or invalid log value: type=%T", item)
		}
	}
	if !strings.Contains(got[0].(string), "16777216") || got[2] != `"KeepCase"` {
		t.Fatal("size or small parameter changed")
	}
	if len(binary) != 16777216 || binary[0] != 0xff {
		t.Fatal("logging mutated database input")
	}
}
