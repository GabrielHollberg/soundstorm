//go:build !linux

package caretaker

import "context"

// watchButton has no power button to read off Linux.
func (u *Updater) watchButton(context.Context) {}
