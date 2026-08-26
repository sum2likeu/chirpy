package auth

import (
	"fmt"
	"testing"
)

func TestSomething(t *testing.T) {
	got, err := HashPassword("input")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "input" {
		t.Errorf("got %v, want a hash", got)
	}
	hashbool, err := CheckPasswordHash("input", got)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hashbool == true {
		fmt.Println("pass")
	} else {
		fmt.Println("fail")
	}
}
