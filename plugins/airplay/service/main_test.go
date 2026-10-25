//go:build airplay

package main

import (
	"strings"
	"testing"
)

func TestConfigAndDescriptor(t *testing.T) {
	c := configuration{"Mini \"Room\"", "", "plughw:0,0", -10, 7000}
	s, e := configText(c)
	if e != nil || !strings.Contains(s, `name = "Mini \"Room\""`) {
		t.Fatalf("%s %v", s, e)
	}
	c.Name = "bad\nname"
	if _, e = configText(c); e == nil {
		t.Fatal("newline accepted")
	}
	n, _, p, txt, e := descriptor([]byte("7000\nAABBCCDDEEFF@Mini\n_raop._tcp\ncn=0,1\npw=false\n"))
	if e != nil || p != 7000 || n != "AABBCCDDEEFF@Mini" || len(txt) != 2 {
		t.Fatal(n, p, txt, e)
	}
	if _, _, _, _, e = descriptor([]byte("7000\nx\n_other._tcp\nx=y\n")); e == nil {
		t.Fatal("bad service accepted")
	}
}
