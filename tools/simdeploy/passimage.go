package main

import "regexp"

// passImageRe is what the app accepts in an image header (app/ui.js safeUrl): an https URL, or dmz:<scid> naming a
// profile doc that the app reads through the user's own node (app/profile.js, docs/PROFILE_DOCS.md). founders.bas
// stores -pass-image as both iconURLHdr and fileURL and never changes them, so a value the app would refuse to
// read is refused here, before the install.
var passImageRe = regexp.MustCompile(`^(?:(?i:https)://[^\s"'<>\\]{3,300}|dmz:[0-9a-f]{64})$`)

// passImageOK: empty (no image), https, or dmz:<64 lowercase hex>.
func passImageOK(s string) bool { return s == "" || passImageRe.MatchString(s) }
