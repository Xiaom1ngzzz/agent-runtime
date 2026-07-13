package main

import (
	"testing"
)

func TestM3MinimalAgent(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
