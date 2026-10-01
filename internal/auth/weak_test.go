package auth

import (
	"context"
	"testing"
)

func TestWeakPasswordsAreRefused(t *testing.T) {
	for _, pw := range []string{
		"password1234", "Password2024!", "p@ssw0rd2024!!", "PASSWORDPASSWORD",
		"qwertyuiop12", "123456789012", "aaaaaaaaaaaa", "abcdefghijkl",
		"iloveyou1234", "football2023", "soundstorm2025", "letmein!!!!!",
		"!!!!????####", "0987654321098", "zxcvbnm12345", "monkeydragon",
	} {
		if err := checkPassword(pw, "someone"); err == nil {
			t.Errorf("%q should be refused", pw)
		}
	}
	if err := checkPassword("gabriel12345", "gabriel"); err == nil {
		t.Error("the account's own name with digits should be refused")
	}
	if err := checkPassword("short1!", "x"); err == nil {
		t.Error("under twelve characters should be refused")
	}
}

func TestReasonablePasswordsAreAllowed(t *testing.T) {
	for _, pw := range []string{
		"correct horse battery staple", "violet-tractor-glacier-7",
		"MyCatEatsPickles2!", "owner-password-1", "kx9#Tq2mZv!8",
		"rainy tuesday in lisbon", "Gabriel likes thunderstorms",
	} {
		if err := checkPassword(pw, "gabriel"); err != nil {
			t.Errorf("%q should be allowed, got %v", pw, err)
		}
	}
}

// Signing in with a password that no longer meets the rules - set before they
// were tightened - asks for a new one: the one moment the server sees it.
func TestSigningInWithAnOldWeakPasswordAsksForANewOne(t *testing.T) {
	m := newManager(t)
	u, err := m.Signup("gabe", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	// As if set under the old rule.
	salt, hash, err := derive("pass1234", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPassword(u.ID, salt, hash, iterations); err != nil {
		t.Fatal(err)
	}
	_, _, user, err := m.SignIn(context.Background(), "client", "gabe", "pass1234")
	if err != nil {
		t.Fatalf("an old password still signs in: %v", err)
	}
	if !user.MustChangePassword {
		t.Fatal("signing in with a weak password should ask for a new one")
	}
	if stored, _ := m.store.User(u.ID); !stored.MustChangePassword {
		t.Fatal("the request to change it should be kept")
	}
	// Choosing a good one clears it.
	if err := m.SetPassword(u, u.ID, "violet-tractor-glacier-7"); err != nil {
		t.Fatal(err)
	}
	if stored, _ := m.store.User(u.ID); stored.MustChangePassword {
		t.Fatal("a new password should clear the request")
	}
}
