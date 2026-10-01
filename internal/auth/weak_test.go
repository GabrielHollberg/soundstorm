package auth

import "testing"

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
