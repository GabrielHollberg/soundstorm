package auth

import (
	"errors"
	"strings"
	"unicode"
)

// Weak passwords are refused when a password is set, never when one is
// checked: refusing one somebody already has would lock them out.
//
// The sign-in throttle holds a guesser to about one try a minute per account,
// whatever addresses they use (Tor hands out a new one whenever asked) - some
// 1,400 a day. That is nothing against a password with any thought in it and
// everything against "password1": guessing lists start with the same few
// hundred. So twelve characters at least, and not one of those lists' words
// with digits or symbols around it, a run of one key, a keyboard row or a
// counting sequence, or the account's own name. Built in, not fetched: the
// server asks nobody about passwords.

var errWeakPassword = errors.New("that password is one of the first a guesser would try; choose something less common - a few unrelated words make a good one")

// commonBases are the words at the heart of the passwords guessers try
// first - leaked-password lists' top entries with their digits and symbols
// stripped - plus the obvious ones for this app.
var commonBases = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		password passw0rd passwort motdepasse contrasena senha wachtwoord
		qwerty qwertyuiop azerty asdf asdfgh asdfghjkl zxcvbn zxcvbnm qazwsx
		letmein welcome hello hi admin administrator root login user guest
		test default changeme secret master access monkey dragon shadow
		sunshine princess football baseball soccer hockey basketball iloveyou
		loveyou lovely love trustno1 whatever superman batman spiderman
		starwars pokemon charlie michael jennifer jordan hunter killer
		freedom ninja mustang harley ranger buster tigger thomas daniel
		andrew joshua matthew robert jessica ashley amanda summer winter
		spring autumn flower cookie chocolate cheese banana apple orange
		computer internet google facebook youtube twitter instagram
		samsung iphone android windows microsoft linux music movie movies
		soundstorm storm cloud media library family friends home house
		myspace abc abcd abcdef abc123 qwe asd zaq zaq1 xsw qwer
		one two three four five six seven eight nine ten
		jesus god angel heaven blessed christ faith hope
		money dollar cash rich gold silver diamond lucky
		mother father mom dad baby babygirl babyboy daddy mommy sister brother
		london paris berlin newyork america canada england texas florida
		january february march april may june july august september october
		november december monday tuesday friday saturday sunday
		red blue green black white purple yellow pink
		cat dog puppy kitty bear lion tiger eagle horse
		game gamer player playstation xbox nintendo minecraft fortnite
		hello world helloworld iloveu iloveyou2 princess1 secret1 batman1
	`) {
		commonBases[w] = true
	}
}

// keyboardRuns are sequences a lazy password walks along.
var keyboardRuns = []string{
	"abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba",
	"0123456789012345678901234567890", "9876543210987654321098765432109",
	"qwertyuiopasdfghjklzxcvbnm", "qwertyuiop", "asdfghjkl", "zxcvbnm",
	"1qaz2wsx3edc4rfv5tgb6yhn7ujm8ik9ol0p", "qazwsxedcrfvtgbyhnujmikolp",
	"poiuytrewq", "lkjhgfdsa", "mnbvcxz",
}

// weakPassword says why a new password is too easy to guess, or nil.
func weakPassword(password, username string) error {
	lower := strings.ToLower(password)
	if strings.Count(lower, string([]rune(lower)[0])) == len([]rune(lower)) {
		return errWeakPassword // one character over and over
	}
	for _, run := range keyboardRuns {
		if len(lower) >= 6 && strings.Contains(run+run, lower) {
			return errWeakPassword
		}
	}
	// The word inside: digits, symbols and spaces stripped from both ends,
	// and common letter-for-digit swaps undone ("p@ssw0rd2024!").
	core := strings.TrimFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	core = strings.NewReplacer("@", "a", "0", "o", "1", "i", "3", "e", "$", "s", "5", "s", "!", "i").Replace(core)
	core = strings.TrimFunc(core, func(r rune) bool { return !unicode.IsLetter(r) })
	if core == "" || commonBases[core] {
		return errWeakPassword // all digits and symbols, or a list word dressed up
	}
	// A list word repeated ("passwordpassword") or two glued together.
	for w := range commonBases {
		if len(w) >= 3 && strings.HasPrefix(core, w) && commonBases[strings.TrimPrefix(core, w)] {
			return errWeakPassword
		}
		if len(w) >= 3 && strings.Repeat(w, len(core)/len(w)) == core {
			return errWeakPassword
		}
	}
	// The account's own name with a little around it.
	if name := strings.ToLower(strings.TrimSpace(username)); len(name) >= 3 && strings.Contains(core, name) && len(core)-len(name) < 6 {
		return errWeakPassword
	}
	return nil
}
