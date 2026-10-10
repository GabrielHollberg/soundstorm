package caretaker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone names known whatever the system carries
)

// The box's time zone: the owner's, set from their browser (EmberStorm asks,
// PUT /timezone), so the night the box keeps for updates (2 to 5, and
// Debian's security updates and their restart) is their night. The box
// came up in UTC, which put all of it in a US family's evening (the box's
// blind review).

// zoneShape is a time zone's name as tz names them: "America/Denver",
// "Europe/London", "America/Argentina/Buenos_Aires", "UTC".
var zoneShape = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$`)

// errBadZone is a time zone that is not one.
var errBadZone = errors.New("not a time zone")

// zoneName is the box's time zone, read from the system each time (it may
// have been set since the caretaker started).
func (u *Updater) zoneName() string {
	target, err := os.Readlink(u.cfg.LocalTime)
	if err != nil {
		return "UTC"
	}
	if i := strings.Index(target, "zoneinfo/"); i >= 0 {
		return target[i+len("zoneinfo/"):]
	}
	return filepath.Base(target)
}

// localZone is the box's time zone as a location, UTC if it cannot be read.
func (u *Updater) localZone() *time.Location {
	if loc, err := time.LoadLocation(u.zoneName()); err == nil {
		return loc
	}
	return time.UTC
}

// setZone makes name the box's time zone.
func (u *Updater) setZone(ctx context.Context, name string) error {
	if !zoneShape.MatchString(name) {
		return errBadZone
	}
	if _, err := time.LoadLocation(name); err != nil {
		return errBadZone
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := u.run(ctx, "timedatectl", "set-timezone", name); err != nil {
		return err
	}
	u.log.Info("the box's time zone was set", "zone", name)
	return nil
}
