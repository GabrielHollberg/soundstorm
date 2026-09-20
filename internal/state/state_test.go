package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// A version 1 file is what every install that exists right now has. Losing an
// account, signing everybody out, or dropping bookmarks on upgrade would all be
// worse than the feature is worth, so this is the test that matters most here.
func TestAVersionOneFileUpgradesWithoutLosingAnything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	expiry := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339Nano)
	legacy := `{
	  "version": 1,
	  "user": {
	    "name": "Gabe",
	    "salt": "c2FsdHk=",
	    "hash": "aGFzaHk=",
	    "iterations": 600000,
	    "createdAt": "2026-01-02T03:04:05Z"
	  },
	  "sessions": { "tok-abc": "` + expiry + `" },
	  "backends": { "navidrome": { "type": "navidrome", "baseUrl": "http://navidrome:4533" } },
	  "progress": { "ebooks/dune.epub": { "location": "epubcfi(/6/4!/2)", "fraction": 0.42 } }
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s := open(t, dir)

	// The single account becomes the owner, keeping its name and its password.
	users := s.Users()
	if len(users) != 1 {
		t.Fatalf("want 1 account, got %d", len(users))
	}
	owner := users[0]
	if owner.Name != "Gabe" {
		t.Errorf("name = %q", owner.Name)
	}
	if !owner.IsOwner() {
		t.Errorf("role = %q: the existing account must be able to manage the server", owner.Role)
	}
	if owner.ID == "" {
		t.Error("the upgraded account has no id")
	}
	if string(owner.Hash) != "hashy" || owner.Iterations != 600000 {
		t.Errorf("password material was not carried over: %+v", owner)
	}

	// Nobody is signed out.
	got, ok := s.SessionUser("tok-abc")
	if !ok {
		t.Fatal("the existing session stopped working; everyone would be signed out by an upgrade")
	}
	if got.ID != owner.ID {
		t.Errorf("session belongs to %q, want the owner", got.ID)
	}

	// And nobody loses their place in a book.
	if _, ok := s.Progress(owner.ID + "/ebooks/dune.epub"); !ok {
		t.Error("the reading position was not moved under its owner")
	}

	// Backends are untouched, so nothing has to be provisioned again.
	if _, ok := s.Backend("navidrome"); !ok {
		t.Error("provisioned credentials were lost")
	}

	// The old key is gone from the file rather than lingering as a second
	// source of truth.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]json.RawMessage
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if _, present := onDisk["user"]; present {
		t.Error("the version 1 account is still in the file alongside the new one")
	}
	if _, present := onDisk["users"]; !present {
		t.Error("no accounts map was written")
	}
}

// Running the migration twice must not produce two owners.
func TestUpgradingIsNotRepeated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"user":{"name":"Gabe"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	first := open(t, dir)
	id := first.Users()[0].ID

	second := open(t, dir)
	if n := len(second.Users()); n != 1 {
		t.Fatalf("a second open produced %d accounts", n)
	}
	if second.Users()[0].ID != id {
		t.Error("the account was given a new id, which would orphan its bookmarks")
	}
}

func TestTheFirstAccountIsAlwaysTheOwner(t *testing.T) {
	s := open(t, t.TempDir())

	// Even asking for a member: there has to be somebody who can manage the
	// server, and the first account is the only candidate.
	first, err := s.AddUser(User{Name: "gabe", Role: RoleMember})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if !first.IsOwner() {
		t.Errorf("first account role = %q", first.Role)
	}

	second, err := s.AddUser(User{Name: "sam", Role: RoleMember})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if second.IsOwner() {
		t.Error("the second account was made an owner too")
	}
}

// Two accounts differing only in capitalisation would make signing in
// ambiguous, since names are matched case-insensitively.
func TestNamesCannotCollide(t *testing.T) {
	s := open(t, t.TempDir())
	if _, err := s.AddUser(User{Name: "Gabe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser(User{Name: "gabe"}); err == nil {
		t.Error("two accounts were allowed to share a name")
	}

	// But a name is stored as typed, and found however it is typed.
	found, ok := s.UserByName("  GABE ")
	if !ok || found.Name != "Gabe" {
		t.Errorf("UserByName = %+v, %v", found, ok)
	}
}

// Removing somebody has to remove what they left behind, or their sessions keep
// working and their bookmarks sit in the file forever.
func TestDeletingAnAccountTakesEverythingWithIt(t *testing.T) {
	s := open(t, t.TempDir())
	owner, _ := s.AddUser(User{Name: "gabe"})
	member, _ := s.AddUser(User{Name: "sam"})

	if err := s.AddSession("sam-token", member.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSession("gabe-token", owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProgress(member.ID+"/ebooks/dune.epub", Progress{Location: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIdentity(member.ID, "audiobookshelf", Identity{Username: "soundstorm-x"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteUser(member.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, ok := s.SessionUser("sam-token"); ok {
		t.Error("a removed account's session still works")
	}
	if _, ok := s.Progress(member.ID + "/ebooks/dune.epub"); ok {
		t.Error("a removed account's bookmarks are still stored")
	}
	if _, ok := s.Identity(member.ID, "audiobookshelf"); ok {
		t.Error("a removed account's backend identity is still stored")
	}
	// And everybody else is untouched.
	if _, ok := s.SessionUser("gabe-token"); !ok {
		t.Error("removing one account signed out another")
	}
}

// Nothing could manage the server afterwards, and recovering would mean editing
// the state file by hand - exactly what this project exists to avoid.
func TestTheOwnerCannotBeRemoved(t *testing.T) {
	s := open(t, t.TempDir())
	owner, _ := s.AddUser(User{Name: "gabe"})
	if err := s.DeleteUser(owner.ID); err == nil {
		t.Error("the owner was removed")
	}
}

// A session whose account is gone is not a session. This is what makes removing
// somebody take effect now rather than whenever their cookie happened to expire.
func TestASessionWithoutAnAccountIsNotLive(t *testing.T) {
	s := open(t, t.TempDir())
	if err := s.AddSession("orphan", "nobody", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SessionUser("orphan"); ok {
		t.Error("a session for an account that does not exist was accepted")
	}
}

func TestExpiredSessionsAreNotLive(t *testing.T) {
	s := open(t, t.TempDir())
	user, _ := s.AddUser(User{Name: "gabe"})
	if err := s.AddSession("stale", user.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SessionUser("stale"); ok {
		t.Error("an expired session was accepted")
	}
}
